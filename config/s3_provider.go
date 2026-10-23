// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml"
)

// S3ProviderSource constrains the allowed provider mode values.
type S3ProviderSource string

const (
	S3ProviderSourceApp    S3ProviderSource = "app"
	S3ProviderSourceCustom S3ProviderSource = "custom"

	// S3ProviderNameMaxLen is the maximum allowed length for a provider name.
	S3ProviderNameMaxLen = 255
)

// S3Provider holds the definition of a saved S3 provider in the cluster library.
// It is persisted per-cluster in a JSON file outside config.toml.
//
// Credential handling: AccessKey and SecretKey are held in plaintext in memory
// (needed by the provisioning path). They are encrypted at rest
// (config.GetEncryptedString) before being written to disk, and decrypted on load.
// Product policy classifies AccessKey as config/env (not secret), but API contract
// still forbids returning stored credentials to the UI.
// MarshalJSON therefore omits both AccessKey and SecretKey; use the dedicated
// persistence struct in cluster_s3_providers.go for file I/O.
type S3Provider struct {
	Name           string           `json:"name"                  groups:"web"`
	ProviderSource S3ProviderSource `json:"providerSource"        groups:"web"`
	// ProviderApp is the sibling app host:port reference; required for app mode, forbidden in custom mode.
	ProviderApp string `json:"providerApp,omitempty" groups:"web"`
	// Endpoint is the S3-compatible URL; required for custom mode, forbidden in app mode.
	Endpoint string `json:"endpoint,omitempty"    groups:"web"`
	Region   string `json:"region,omitempty"      groups:"web"`
	// AccessKey and SecretKey are never emitted by MarshalJSON.
	// File persistence bypasses MarshalJSON via a private disk struct.
	AccessKey string `json:"-" groups:"apps"`
	SecretKey string `json:"-" groups:"apps"`
	// Cloud18SSO: the provider is reached with the Cloud18 SSO (#2091): it holds no keys, a
	// registered replication-manager gets short-lived ones from its STS with its Cloud18
	// identity. A replication-manager not registered cannot use it.
	Cloud18SSO bool `json:"cloud18Sso,omitempty" groups:"web"`
	// RoleArn: the storage's role the SSO identity assumes; empty, it is computed as MinIO
	// names it (arn:minio:iam:<region>::role/<base64url(sha1(client id))>): set only to override
	RoleArn string `json:"roleArn,omitempty" groups:"web"`
}

// s3ProviderWeb is the JSON representation used for all API/web serialization.
// It omits credentials so that json.Marshal(S3Provider) is safe by default.
type s3ProviderWeb struct {
	Name           string           `json:"name"`
	ProviderSource S3ProviderSource `json:"providerSource"`
	ProviderApp    string           `json:"providerApp,omitempty"`
	Endpoint       string           `json:"endpoint,omitempty"`
	Region         string           `json:"region,omitempty"`
	Cloud18SSO     bool             `json:"cloud18Sso,omitempty"`
	RoleArn        string           `json:"roleArn,omitempty"`
}

// MarshalJSON emits only non-sensitive fields.
// Credentials (AccessKey, SecretKey) are always omitted by API serialization.
// File persistence must use a private disk struct that bypasses this method —
// see cluster/cluster_s3_providers.go.
func (p S3Provider) MarshalJSON() ([]byte, error) {
	return json.Marshal(s3ProviderWeb{
		Name:           p.Name,
		ProviderSource: p.ProviderSource,
		ProviderApp:    p.ProviderApp,
		Endpoint:       p.Endpoint,
		Region:         p.Region,
		Cloud18SSO:     p.Cloud18SSO,
		RoleArn:        p.RoleArn,
	})
}

// ValidateS3ProviderName checks that name is non-empty, has no leading/trailing
// whitespace, no path separators, and does not exceed S3ProviderNameMaxLen.
// Name uniqueness is enforced case-sensitively throughout the system
// ("Provider" and "provider" are treated as distinct names).
func ValidateS3ProviderName(name string) error {
	if name == "" {
		return fmt.Errorf("provider name must not be empty")
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("provider name must not have leading or trailing whitespace")
	}
	if len(name) > S3ProviderNameMaxLen {
		return fmt.Errorf("provider name exceeds maximum length of %d", S3ProviderNameMaxLen)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("provider name must not contain path separators")
	}
	return nil
}

// Validate enforces the S3Provider model contract:
//   - name must pass ValidateS3ProviderName
//   - providerSource must be exactly "app" or "custom"
//   - app mode:    providerApp required (non-empty after trim); endpoint/region/accesskey/secretkey must be empty
//   - custom mode: endpoint required (non-empty after trim); providerApp must be empty;
//     with cloud18Sso, accesskey/secretkey must be empty
//   - app mode never takes cloud18Sso
func (p *S3Provider) Validate() error {
	if err := ValidateS3ProviderName(p.Name); err != nil {
		return err
	}
	switch p.ProviderSource {
	case S3ProviderSourceApp:
		if strings.TrimSpace(p.ProviderApp) == "" {
			return fmt.Errorf("providerApp is required when providerSource is %q", S3ProviderSourceApp)
		}
		if p.Endpoint != "" || p.Region != "" || p.AccessKey != "" || p.SecretKey != "" {
			return fmt.Errorf("endpoint, region, accesskey, and secretkey must be empty when providerSource is %q", S3ProviderSourceApp)
		}
		if p.Cloud18SSO {
			return fmt.Errorf("cloud18Sso is for a custom provider, not providerSource %q", S3ProviderSourceApp)
		}
	case S3ProviderSourceCustom:
		if strings.TrimSpace(p.Endpoint) == "" {
			return fmt.Errorf("endpoint is required when providerSource is %q", S3ProviderSourceCustom)
		}
		if p.ProviderApp != "" {
			return fmt.Errorf("providerApp must be empty when providerSource is %q", S3ProviderSourceCustom)
		}
		if p.RoleArn != "" && !p.Cloud18SSO {
			return fmt.Errorf("roleArn is for a provider reached with the Cloud18 SSO (cloud18Sso)")
		}
		if p.Cloud18SSO && (p.AccessKey != "" || p.SecretKey != "") {
			return fmt.Errorf("accesskey and secretkey must be empty with cloud18Sso: the keys come from the Cloud18 SSO")
		}
	default:
		return fmt.Errorf("providerSource must be %q or %q, got %q",
			S3ProviderSourceApp, S3ProviderSourceCustom, p.ProviderSource)
	}
	return nil
}

// S3ProviderBlocks are [[s3-provider]] blocks as read from a config file (#2155), keys
// encrypted: Config.S3Providers, read once by the cluster's LoadS3Providers; the saved config
// is rendered from the cluster's provider list.
type S3ProviderBlocks []map[string]interface{}

// S3ProviderFromSettings reads an [[s3-provider]] block, of inject.toml or of the cluster's
// config (#2155): a custom provider by default, cloud18-sso and role-arn for one reached with
// the Cloud18 SSO. Keys are returned as written (encrypted in the cluster's config).
func S3ProviderFromSettings(m map[string]interface{}) S3Provider {
	p := S3Provider{ProviderSource: S3ProviderSourceCustom}
	for k, v := range m {
		s := fmt.Sprintf("%v", v)
		switch k {
		case "name":
			p.Name = s
		case "provider-source":
			p.ProviderSource = S3ProviderSource(s)
		case "provider-app":
			p.ProviderApp = s
		case "endpoint":
			p.Endpoint = s
		case "region":
			p.Region = s
		case "access-key":
			p.AccessKey = s
		case "secret-key":
			p.SecretKey = s
		case "cloud18-sso":
			p.Cloud18SSO = s == "true"
		case "role-arn":
			p.RoleArn = s
		}
	}
	return p
}

// S3ProviderSettings is the reverse of S3ProviderFromSettings: the block's keys, empty ones
// left out.
func S3ProviderSettings(p S3Provider) map[string]interface{} {
	m := map[string]interface{}{"name": p.Name, "provider-source": string(p.ProviderSource)}
	for k, v := range map[string]string{"provider-app": p.ProviderApp, "endpoint": p.Endpoint, "region": p.Region,
		"access-key": p.AccessKey, "secret-key": p.SecretKey, "role-arn": p.RoleArn} {
		if v != "" {
			m[k] = v
		}
	}
	if p.Cloud18SSO {
		m["cloud18-sso"] = true
	}
	return m
}

// S3ProvidersToml renders providers as [[<section>.s3-provider]] blocks for the saved cluster
// config (#2155); the caller passes them with their keys already encrypted.
func S3ProvidersToml(section string, providers []S3Provider) (string, error) {
	var b strings.Builder
	for _, p := range providers {
		b.WriteString("\n[[" + section + ".s3-provider]]\n")
		m := S3ProviderSettings(p)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t, err := toml.TreeFromMap(map[string]interface{}{k: m[k]})
			if err != nil {
				return "", err
			}
			line, err := t.ToTomlString()
			if err != nil {
				return "", err
			}
			b.WriteString(line)
		}
	}
	return b.String(), nil
}
