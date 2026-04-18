// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"fmt"
	"strings"
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
}

// s3ProviderWeb is the JSON representation used for all API/web serialization.
// It omits credentials so that json.Marshal(S3Provider) is safe by default.
type s3ProviderWeb struct {
	Name           string           `json:"name"`
	ProviderSource S3ProviderSource `json:"providerSource"`
	ProviderApp    string           `json:"providerApp,omitempty"`
	Endpoint       string           `json:"endpoint,omitempty"`
	Region         string           `json:"region,omitempty"`
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
//   - custom mode: endpoint required (non-empty after trim); providerApp must be empty
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
	case S3ProviderSourceCustom:
		if strings.TrimSpace(p.Endpoint) == "" {
			return fmt.Errorf("endpoint is required when providerSource is %q", S3ProviderSourceCustom)
		}
		if p.ProviderApp != "" {
			return fmt.Errorf("providerApp must be empty when providerSource is %q", S3ProviderSourceCustom)
		}
	default:
		return fmt.Errorf("providerSource must be %q or %q, got %q",
			S3ProviderSourceApp, S3ProviderSourceCustom, p.ProviderSource)
	}
	return nil
}
