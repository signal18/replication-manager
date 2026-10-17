// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
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
// logs in to the Cloud18 GitLab with its own identity (cloud18-gitlab-user), gets an OpenID
// Connect identity token for the application the storage trusts (cloud18-sso-client-id), and
// trades it at the storage's STS (AssumeRoleWithWebIdentity) for short-lived keys, renewed
// before they expire. The storage's policy opens to that identity its own folder only, named
// after its permanent GitLab user id (${jwt:sub}, never its username, which can change hands):
// two SSO users never see each other's backups.

// ErrCloud18SSONotRegistered: an SSO provider needs a replication-manager registered to Cloud18.
var ErrCloud18SSONotRegistered = errors.New("the S3 provider is reached with the Cloud18 SSO: this replication-manager is not registered to Cloud18")

const (
	cloud18SSOSessionSeconds = 3600
	cloud18SSORenewBefore    = 5 * time.Minute
	cloud18SSOHTTPTimeout    = 30 * time.Second
)

// cloud18SSOConfig is what the exchange needs, read from the config at each exchange.
type cloud18SSOConfig struct {
	issuer, user, password, clientID, clientSecret string
}

func (cluster *Cluster) cloud18SSOConfig() (cloud18SSOConfig, error) {
	c := cluster.Conf
	if !c.Cloud18 || c.Cloud18GitUser == "" {
		return cloud18SSOConfig{}, ErrCloud18SSONotRegistered
	}
	cfg := cloud18SSOConfig{
		issuer:       strings.TrimRight(c.OAuthProvider, "/"),
		user:         c.Cloud18GitUser,
		password:     c.GetDecryptedValue("cloud18-gitlab-password"),
		clientID:     c.Cloud18SSOClientID,
		clientSecret: c.GetDecryptedValue("cloud18-sso-client-secret"),
	}
	if cfg.clientID == "" {
		cfg.clientID, cfg.clientSecret = c.OAuthClientID, c.GetDecryptedValue("api-oauth-client-secret")
	}
	if cfg.issuer == "" || cfg.clientID == "" || cfg.password == "" {
		return cloud18SSOConfig{}, errors.New("Cloud18 SSO: no GitLab application (cloud18-sso-client-id) or no GitLab login")
	}
	return cfg, nil
}

// cloud18IdentityToken logs in to the Cloud18 GitLab and returns the OpenID Connect identity
// token and its sub claim, the GitLab user id. The application is not confidential: the
// instance's own login is the proof, no shared secret is handed out (sent only when set).
func cloud18IdentityToken(cfg cloud18SSOConfig) (string, string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {cfg.user},
		"password":   {cfg.password},
		"client_id":  {cfg.clientID},
		"scope":      {"openid profile email"},
	}
	if cfg.clientSecret != "" {
		form.Set("client_secret", cfg.clientSecret)
	}
	resp, err := (&http.Client{Timeout: cloud18SSOHTTPTimeout}).PostForm(cfg.issuer+"/oauth/token", form)
	if err != nil {
		return "", "", fmt.Errorf("Cloud18 SSO login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Cloud18 SSO login: GitLab answered %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.IDToken == "" {
		return "", "", errors.New("Cloud18 SSO login: GitLab gave no identity token (scope openid on the application?)")
	}
	user, err := identityTokenUser(tok.IDToken)
	if err != nil {
		return "", "", err
	}
	return tok.IDToken, user, nil
}

// identityTokenUser reads the sub claim of an identity token, the GitLab user id the folder is
// named after (the storage checks the signature; this side only needs the folder's name).
func identityTokenUser(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errors.New("Cloud18 SSO: malformed identity token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("Cloud18 SSO: malformed identity token")
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("Cloud18 SSO: malformed identity token")
	}
	user := claims.Sub
	if user == "" || strings.ContainsAny(user, "/\\") {
		return "", errors.New("Cloud18 SSO: the identity token names no user")
	}
	return user, nil
}

// minioRoleArn is the role MinIO gives an OpenID configuration with a role policy:
// arn:minio:iam:<region>::role/<base64url(sha1(client id))>, so nobody copies it from its log.
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
	roleArn  string // "": computed from the client id (minioRoleArn)
	mu       sync.Mutex
	user     string
}

func (p *cloud18SSOCredentials) Retrieve() (credentials.Value, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg, err := p.cluster.cloud18SSOConfig()
	if err != nil {
		return credentials.Value{}, err
	}
	idToken, user, err := cloud18IdentityToken(cfg)
	if err != nil {
		return credentials.Value{}, err
	}
	role := p.roleArn
	if role == "" {
		role = minioRoleArn(p.region, cfg.clientID)
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
		if _, err := cluster.cloud18SSOConfig(); err != nil {
			return err
		}
	}
	return nil
}
