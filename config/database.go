// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

// DatabaseConfig contains all database server-related configuration
type DatabaseConfig struct {
	// Credentials and Hosts
	Credential           string `mapstructure:"db-servers-credential" toml:"db-servers-credential" json:"dbServersCredential"`
	Hosts                string `mapstructure:"db-servers-hosts" toml:"db-servers-hosts" json:"dbServersHosts"`
	StateChangeScript    string `mapstructure:"db-servers-state-change-script" toml:"db-servers-state-change-script" json:"dbServersStateChangeScript"`

	// Delayed Replication
	DelayedHosts string `mapstructure:"replication-delayed-hosts" toml:"replication-delayed-hosts" json:"replicationDelayedHosts"`
	DelayedTime  int    `mapstructure:"replication-delayed-time" toml:"replication-delayed-time" json:"replicationDelayedTime" validate:"min=0"`

	// TLS/SSL Configuration
	TLSUseGeneratedCertificate bool   `mapstructure:"db-servers-tls-use-generated-cert" toml:"db-servers-tls-use-generated-cert" json:"dbServersUseGeneratedCert"`
	TLSCA                      string `mapstructure:"db-servers-tls-ca-cert" toml:"db-servers-tls-ca-cert" json:"dbServersTlsCaCert"`
	TLSClientKey               string `mapstructure:"db-servers-tls-client-key" toml:"db-servers-tls-client-key" json:"dbServersTlsClientKey"`
	TLSClientCert              string `mapstructure:"db-servers-tls-client-cert" toml:"db-servers-tls-client-cert" json:"dbServersTlsClientCert"`
	TLSServerKey               string `mapstructure:"db-servers-tls-server-key" toml:"db-servers-tls-server-key" json:"dbServersTlsServerKey"`
	TLSServerCert              string `mapstructure:"db-servers-tls-server-cert" toml:"db-servers-tls-server-cert" json:"dbServersTlsServerCert"`
	TLSSSLMode                 string `mapstructure:"db-servers-tls-ssl-mode" toml:"db-servers-tls-ssl-mode" json:"dbServersTlsSslMode" validate:"omitempty,oneof=DISABLED PREFERRED REQUIRED VERIFY_CA VERIFY_IDENTITY"`

	// Server Selection
	PreferedMaster   string `mapstructure:"db-servers-prefered-master" toml:"db-servers-prefered-master" json:"dbServersPreferedMaster"`
	BackupServers    string `mapstructure:"db-servers-backup-hosts" toml:"db-servers-backup-hosts" json:"dbServersBackupHosts"`
	IgnoredHosts     string `mapstructure:"db-servers-ignored-hosts" toml:"db-servers-ignored-hosts" json:"dbServersIgnoredHosts"`
	IgnoredReadOnly  string `mapstructure:"db-servers-ignored-readonly" toml:"db-servers-ignored-readonly" json:"dbServersIgnoredReadonly"`

	// Timeouts
	ConnectTimeout int `mapstructure:"db-servers-connect-timeout" toml:"db-servers-connect-timeout" json:"dbServersConnectTimeout" validate:"min=1,max=300"`
	ExecTimeout    int `mapstructure:"db-servers-exec-timeout" toml:"db-servers-exec-timeout" json:"dbServersExecTimeout" validate:"min=1,max=3600"`
	ReadTimeout    int `mapstructure:"db-servers-read-timeout" toml:"db-servers-read-timeout" json:"dbServersReadTimeout" validate:"min=1,max=86400"`

	// Network
	Locality    string `mapstructure:"db-servers-locality" toml:"db-servers-locality" json:"dbServersLocality"`
	BindAddress string `mapstructure:"db-servers-bind-address" toml:"db-servers-bind-address" json:"dbServersBindAddress"`
}

// Validate performs validation on DatabaseConfig
func (d *DatabaseConfig) Validate() error {
	// Validate timeouts
	if d.ConnectTimeout < 1 || d.ConnectTimeout > 300 {
		return NewValidationError("db-servers-connect-timeout", d.ConnectTimeout, "must be between 1 and 300 seconds")
	}

	if d.ExecTimeout < 1 || d.ExecTimeout > 3600 {
		return NewValidationError("db-servers-exec-timeout", d.ExecTimeout, "must be between 1 and 3600 seconds")
	}

	if d.ReadTimeout < 1 || d.ReadTimeout > 86400 {
		return NewValidationError("db-servers-read-timeout", d.ReadTimeout, "must be between 1 and 86400 seconds")
	}

	// Validate SSL mode if specified
	validSSLModes := map[string]bool{
		"DISABLED":        true,
		"PREFERRED":       true,
		"REQUIRED":        true,
		"VERIFY_CA":       true,
		"VERIFY_IDENTITY": true,
		"":                true, // Empty is valid (no SSL)
	}
	if !validSSLModes[d.TLSSSLMode] {
		return NewValidationError("db-servers-tls-ssl-mode", d.TLSSSLMode, "must be one of: DISABLED, PREFERRED, REQUIRED, VERIFY_CA, VERIFY_IDENTITY")
	}

	// Validate delayed replication time
	if d.DelayedTime < 0 {
		return NewValidationError("replication-delayed-time", d.DelayedTime, "must be non-negative")
	}

	return nil
}
