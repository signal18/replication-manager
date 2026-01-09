// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

// ReplicationConfig contains all replication-related configuration
type ReplicationConfig struct {
	// Basic Replication Settings
	Credential       string `mapstructure:"replication-credential" toml:"replication-credential" json:"replicationCredential"`
	SourceName       string `mapstructure:"replication-source-name" toml:"replication-source-name" json:"replicationSourceName"`
	UseSSL           bool   `mapstructure:"replication-use-ssl" toml:"replication-use-ssl" json:"replicationUseSsl"`
	ErrorScript      string `mapstructure:"replication-error-script" toml:"replication-error-script" json:"replicationErrorScript"`
	RestartOnSQLErrorMatch string `mapstructure:"replication-restart-on-sqlerror-match" toml:"replication-restart-on-sqlerror-match" json:"replicationRestartOnSqlLErrorMatch"`

	// Master Connection
	MasterConnectRetry int `mapstructure:"replication-master-connect-retry" toml:"replication-master-connect-retry" json:"replicationMasterConnectRetry" validate:"min=1"`
	MasterRetryCount   int `mapstructure:"replication-master-retry-count" toml:"replication-master-retry-count" json:"replicationMasterRetryCount" validate:"min=0"`

	// Multi-source Replication
	MultisourceHeadClusters string `mapstructure:"replication-multisource-head-clusters" toml:"replication-multisource-head-clusters" json:"replicationMultisourceHeadClusters"`

	// Topology Types
	ActivePassive       bool   `mapstructure:"replication-active-passive" toml:"replication-active-passive" json:"replicationActivePassive"`
	DynamicTopology     bool   `mapstructure:"replication-dynamic-topology" toml:"replication-dynamic-topology" json:"replicationDynamicTopology"`
	MultiMaster         bool   `mapstructure:"replication-multi-master" toml:"replication-multi-master" json:"replicationMultiMaster"`
	MultiMasterConcurrentWrite bool `mapstructure:"replication-multi-master-concurrent-write" toml:"replication-multi-master-concurrent-write" json:"replicationMultiMasterConcurrentWrite"`
	MultiMasterRing     bool   `mapstructure:"replication-multi-master-ring" toml:"replication-multi-master-ring" json:"replicationMultiMasterRing"`
	MultiMasterRingUnsafe bool `mapstructure:"replication-multi-master-ring-unsafe" toml:"replication-multi-master-ring-unsafe" json:"replicationMultiMasterRingUnsafe"`
	MultiTierSlave      bool   `mapstructure:"replication-multi-tier-slave" toml:"replication-multi-tier-slave" json:"replicationMultiTierSlave"`
	NoRelay             bool   `mapstructure:"replication-master-slave-never-relay" toml:"replication-master-slave-never-relay" json:"replicationMasterSlaveNeverRelay"`

	// Multi-Master Wsrep (Galera)
	MultiMasterWsrep      bool   `mapstructure:"replication-multi-master-wsrep" toml:"replication-multi-master-wsrep" json:"replicationMultiMasterWsrep"`
	MultiMasterWsrepSSTMethod string `mapstructure:"replication-multi-master-wsrep-sst-method" toml:"replication-multi-master-wsrep-sst-method" json:"replicationMultiMasterWsrepSSTMethod" validate:"omitempty,oneof=mariabackup xtrabackup-v2 rsync mysqldump"`
	MultiMasterWsrepPort  int    `mapstructure:"replication-multi-master-wsrep-port" toml:"replication-multi-master-wsrep-port" json:"replicationMultiMasterWsrepPort" validate:"omitempty,min=1024,max=65535"`

	// Group Replication
	MultiMasterGrouprep     bool `mapstructure:"replication-multi-master-grouprep" toml:"replication-multi-master-grouprep" json:"replicationMultiMasterGrouprep"`
	MultiMasterGrouprepPort int  `mapstructure:"replication-multi-master-grouprep-port" toml:"replication-multi-master-grouprep-port" json:"replicationMultiMasterGrouprepPort" validate:"omitempty,min=1024,max=65535"`

	// PostgreSQL Replication
	MasterSlavePgStream  bool `mapstructure:"replication-master-slave-pg-stream" toml:"replication-master-slave-pg-stream" json:"replicationMasterSlavePgStream"`
	MasterSlavePgLogical bool `mapstructure:"replication-master-slave-pg-logical" toml:"replication-master-slave-pg-logical" json:"replicationMasterSlavePgLogical"`
}

// Validate performs validation on ReplicationConfig
func (r *ReplicationConfig) Validate() error {
	// Validate master connect retry
	if r.MasterConnectRetry < 1 {
		return NewValidationError("replication-master-connect-retry", r.MasterConnectRetry, "must be at least 1 second")
	}

	// Validate port numbers
	if r.MultiMasterWsrepPort != 0 && (r.MultiMasterWsrepPort < 1024 || r.MultiMasterWsrepPort > 65535) {
		return NewValidationError("replication-multi-master-wsrep-port", r.MultiMasterWsrepPort, "must be between 1024 and 65535")
	}

	if r.MultiMasterGrouprepPort != 0 && (r.MultiMasterGrouprepPort < 1024 || r.MultiMasterGrouprepPort > 65535) {
		return NewValidationError("replication-multi-master-grouprep-port", r.MultiMasterGrouprepPort, "must be between 1024 and 65535")
	}

	// Validate SST method
	if r.MultiMasterWsrep && r.MultiMasterWsrepSSTMethod != "" {
		validMethods := map[string]bool{
			"mariabackup":    true,
			"xtrabackup-v2":  true,
			"rsync":          true,
			"mysqldump":      true,
		}
		if !validMethods[r.MultiMasterWsrepSSTMethod] {
			return NewValidationError("replication-multi-master-wsrep-sst-method", r.MultiMasterWsrepSSTMethod, "must be one of: mariabackup, xtrabackup-v2, rsync, mysqldump")
		}
	}

	// Validate topology conflicts
	topologyCount := 0
	if r.ActivePassive { topologyCount++ }
	if r.MultiMaster { topologyCount++ }
	if r.MultiMasterRing { topologyCount++ }
	if r.MultiMasterWsrep { topologyCount++ }
	if r.MultiMasterGrouprep { topologyCount++ }
	if r.MasterSlavePgStream { topologyCount++ }
	if r.MasterSlavePgLogical { topologyCount++ }

	if topologyCount > 1 {
		return NewValidationError("replication-topology", topologyCount, "multiple replication topologies selected - only one topology can be active")
	}

	// PostgreSQL topologies are mutually exclusive
	if r.MasterSlavePgStream && r.MasterSlavePgLogical {
		return NewValidationError("replication-postgresql", "both", "PostgreSQL streaming and logical replication cannot both be enabled")
	}

	return nil
}
