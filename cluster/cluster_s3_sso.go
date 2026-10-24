// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/signal18/replication-manager/config"
)

// An S3 provider reached with the Cloud18 SSO (#2091) holds no keys: this replication-manager
// gets a token of its own Cloud18 identity client (#2159, config.Cloud18IdentityToken, the
// client_credentials grant of the Cloud18 realm: no password), and trades it at the storage's
// STS (AssumeRoleWithWebIdentity) for short-lived keys, renewed before they expire. The storage
// trusts the realm with the audience config.ConstCloud18StorageAudience, and its policy opens to
// that identity its own folder only, named after the token's sub (${jwt:sub}): the instance's
// permanent GitLab user id, carried by its client, so the folder did not move with the switch
// from the GitLab login. Two SSO users never see each other's backups.

// ErrCloud18SSONotRegistered: an SSO provider needs a replication-manager registered to Cloud18.
var ErrCloud18SSONotRegistered = errors.New("the S3 provider is reached with the Cloud18 SSO: this replication-manager is not registered to Cloud18")

const (
	cloud18SSOSessionSeconds = 3600
	cloud18SSORenewBefore    = 5 * time.Minute
	cloud18SSOHTTPTimeout    = 30 * time.Second
)

// cloud18SSOReady says why the Cloud18 SSO cannot be used here, nil when it can.
func (cluster *Cluster) cloud18SSOReady() error {
	if !cluster.Conf.Cloud18 {
		return ErrCloud18SSONotRegistered
	}
	if cluster.Conf.Cloud18IdentityClientID == "" {
		return config.ErrCloud18IdentityNotSet
	}
	return nil
}

// minioRoleArn is the role MinIO gives an OpenID configuration with a role policy:
// arn:minio:iam:<region>::role/<base64url(sha1(client id))>, so nobody copies it from its log;
// the Cloud18 storages' client id is the audience config.ConstCloud18StorageAudience.
func minioRoleArn(region, clientID string) string {
	sum := sha1.Sum([]byte(clientID))
	return "arn:minio:iam:" + region + "::role/" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// stsResponse is the part of an AssumeRoleWithWebIdentity answer this side reads.
type stsResponse struct {
	Result struct {
		Credentials struct {
			AccessKeyID     string    `xml:"AccessKeyId"`
			SecretAccessKey string    `xml:"SecretAccessKey"`
			SessionToken    string    `xml:"SessionToken"`
			Expiration      time.Time `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:"AssumeRoleWithWebIdentityResult"`
}

// assumeRoleWithWebIdentity trades the identity token for short-lived keys at endpoint.
func assumeRoleWithWebIdentity(endpoint, roleArn, idToken string) (credentials.Value, time.Time, error) {
	form := url.Values{
		"Action":           {"AssumeRoleWithWebIdentity"},
		"Version":          {"2011-06-15"},
		"WebIdentityToken": {idToken},
		"DurationSeconds":  {fmt.Sprintf("%d", cloud18SSOSessionSeconds)},
	}
	if roleArn != "" {
		form.Set("RoleArn", roleArn)
	}
	resp, err := (&http.Client{Timeout: cloud18SSOHTTPTimeout}).PostForm(strings.TrimRight(endpoint, "/")+"/", form)
	if err != nil {
		return credentials.Value{}, time.Time{}, fmt.Errorf("Cloud18 SSO: storage STS: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return credentials.Value{}, time.Time{}, fmt.Errorf("Cloud18 SSO: the storage refused the identity (STS answered %d)", resp.StatusCode)
	}
	var r stsResponse
	if err := xml.Unmarshal(body, &r); err != nil || r.Result.Credentials.AccessKeyID == "" {
		return credentials.Value{}, time.Time{}, errors.New("Cloud18 SSO: the storage gave no keys")
	}
	c := r.Result.Credentials
	return credentials.Value{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken, ProviderName: "cloud18-sso"}, c.Expiration, nil
}

// cloud18SSOCredentials is the renewing source of an SSO provider's keys: the mount's S3
// client asks it again when they near their end.
type cloud18SSOCredentials struct {
	credentials.Expiry
	cluster  *Cluster
	endpoint string
	region   string
	roleArn  string // "": computed from the storages' audience (minioRoleArn)
	mu       sync.Mutex
	user     string
}

func (p *cloud18SSOCredentials) Retrieve() (credentials.Value, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.cluster.cloud18SSOReady(); err != nil {
		return credentials.Value{}, err
	}
	idToken, user, err := p.cluster.Conf.Cloud18IdentityToken()
	if err != nil {
		return credentials.Value{}, err
	}
	role := p.roleArn
	if role == "" {
		role = minioRoleArn(p.region, config.ConstCloud18StorageAudience)
	}
	v, exp, err := assumeRoleWithWebIdentity(p.endpoint, role, idToken)
	if err != nil {
		return credentials.Value{}, err
	}
	p.user = user
	p.SetExpiration(exp, cloud18SSORenewBefore)
	return v, nil
}

// User is the SSO user the keys were issued to, its folder in the bucket.
func (p *cloud18SSOCredentials) User() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.user
}

// newCloud18SSOCredentials gets the first keys at once, so a refused identity is a state of
// the caller and not a failure inside a running mount.
func (cluster *Cluster) newCloud18SSOCredentials(endpoint, region, roleArn string) (*credentials.Credentials, *cloud18SSOCredentials, error) {
	src := &cloud18SSOCredentials{cluster: cluster, endpoint: endpoint, region: region, roleArn: roleArn}
	creds := credentials.NewCredentials(src)
	if _, err := creds.Get(); err != nil {
		return nil, nil, err
	}
	return creds, src, nil
}

// ensure the type is used as the SDK expects
var _ credentials.Provider = (*cloud18SSOCredentials)(nil)

// s3ProviderUsable says why an S3 provider cannot be used here, nil when it can.
func (cluster *Cluster) s3ProviderUsable(p config.S3Provider) error {
	if p.Cloud18SSO {
		return cluster.cloud18SSOReady()
	}
	return nil
}
