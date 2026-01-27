// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package config

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"path/filepath"
	"regexp"
	"slices"

	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	masker "github.com/ggwhite/go-masker"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"

	"github.com/spf13/viper"
)

type Config struct {
	Version                                   string                 `mapstructure:"-" toml:"-" json:"version"`
	FullVersion                               string                 `mapstructure:"-" toml:"-" json:"fullVersion"`
	GoOS                                      string                 `mapstructure:"goos" toml:"-" json:"goOS"`
	GoArch                                    string                 `mapstructure:"goarch" toml:"-" json:"goArch"`
	WithTarball                               string                 `mapstructure:"-" toml:"-" json:"withTarball"`
	WithEmbed                                 string                 `mapstructure:"-" toml:"-" json:"withEmbed"`
	MemProfile                                string                 `mapstructure:"-" toml:"-" json:"-"`
	Include                                   string                 `mapstructure:"include" toml:"-" json:"-"`
	MonitoringSystemUser                      string                 `scope:"server" mapstructure:"user" toml:"-" json:"-"`
	BaseDir                                   string                 `scope:"server" mapstructure:"monitoring-basedir" toml:"monitoring-basedir" json:"monitoringBasedir"`
	WorkingDir                                string                 `scope:"server" mapstructure:"monitoring-datadir" toml:"monitoring-datadir" json:"monitoringDatadir"`
	ShareDir                                  string                 `mapstructure:"monitoring-sharedir" toml:"monitoring-sharedir" json:"monitoringSharedir"`
	ConfDir                                   string                 `scope:"server" mapstructure:"monitoring-confdir" toml:"monitoring-confdir" json:"monitoringConfdir"`
	ConfDirBackup                             string                 `scope:"server" mapstructure:"monitoring-confdir-backup" toml:"monitoring-confdir-backup" json:"monitoringConfdirBackup"`
	ConfDirExtra                              string                 `scope:"server" mapstructure:"monitoring-confdir-extra" toml:"monitoring-confdir-extra" json:"monitoringConfdirExtra"`
	ConfRewrite                               bool                   `scope:"server" mapstructure:"monitoring-save-config" toml:"monitoring-save-config" json:"monitoringSaveConfig"`
	ConfRestoreOnStart                        bool                   `scope:"server" mapstructure:"monitoring-restore-config-on-start"  toml:"monitoring-restore-config-on-start" json:"monitoringRestoreConfigOnStart"`
	MonitoringMergeConfigOnStart              bool                   `scope:"server" mapstructure:"monitoring-merge-config-on-start"  toml:"monitoring-merge-config-on-start" json:"monitoringMergeConfigOnStart"`
	MonitoringSSLCert                         string                 `scope:"server" mapstructure:"monitoring-ssl-cert" toml:"monitoring-ssl-cert" json:"monitoringSSLCert"`
	MonitoringSSLKey                          string                 `scope:"server" mapstructure:"monitoring-ssl-key" toml:"monitoring-ssl-key" json:"monitoringSSLKey"`
	MonitoringKeyPath                         string                 `scope:"server" mapstructure:"monitoring-key-path" toml:"monitoring-key-path" json:"monitoringKeyPath"`
	MonitoringKeyPathGitOverwrite             bool                   `scope:"server" mapstructure:"monitoring-key-path-git-overwrite" toml:"monitoring-key-path-git-overwrite" json:"monitoringKeyPathGitOverwrite"`
	MonitoringTicker                          int64                  `mapstructure:"monitoring-ticker" toml:"monitoring-ticker" json:"monitoringTicker"`
	MonitorWaitRetry                          int64                  `mapstructure:"monitoring-wait-retry" toml:"monitoring-wait-retry" json:"monitoringWaitRetry"`
	Socket                                    string                 `mapstructure:"monitoring-socket" toml:"monitoring-socket" json:"monitoringSocket"`
	TunnelHost                                string                 `mapstructure:"monitoring-tunnel-host" toml:"monitoring-tunnel-host" json:"monitoringTunnelHost"`
	TunnelCredential                          string                 `mapstructure:"monitoring-tunnel-credential" toml:"monitoring-tunnel-credential" json:"monitoringTunnelCredential"`
	TunnelKeyPath                             string                 `mapstructure:"monitoring-tunnel-key-path" toml:"monitoring-tunnel-key-path" json:"monitoringTunnelKeyPath"`
	MonitorAddress                            string                 `scope:"server" mapstructure:"monitoring-address" toml:"monitoring-address" json:"monitoringAddress"`
	MonitorWriteHeartbeat                     bool                   `mapstructure:"monitoring-write-heartbeat" toml:"monitoring-write-heartbeat" json:"monitoringWriteHeartbeat"`
	MonitorPause                              bool                   `mapstructure:"monitoring-pause" toml:"monitoring-pause" json:"monitoringPause"`
	MonitorWriteHeartbeatCredential           string                 `mapstructure:"monitoring-write-heartbeat-credential" toml:"monitoring-write-heartbeat-credential" json:"monitoringWriteHeartbeatCredential"`
	MonitorVariableDiff                       bool                   `mapstructure:"monitoring-variable-diff" toml:"monitoring-variable-diff" json:"monitoringVariableDiff"`
	MonitorSchemaChange                       bool                   `mapstructure:"monitoring-schema-change" toml:"monitoring-schema-change" json:"monitoringSchemaChange"`
	MonitorSchemaColumns                      bool                   `mapstructure:"monitoring-schema-columns" toml:"monitoring-schema-columns" json:"monitoringSchemaColumns"`
	MonitorSchemaIndexes                      bool                   `mapstructure:"monitoring-schema-indexes" toml:"monitoring-schema-indexes" json:"monitoringSchemaIndexes"`
	MonitorSchemaOnReplicas                   bool                   `mapstructure:"monitoring-schema-on-replicas" toml:"monitoring-schema-on-replicas" json:"monitoringSchemaOnReplicas"`
	MonitorSchemaIgnoreTables                 string                 `mapstructure:"monitoring-schema-ignore-tables" toml:"monitoring-schema-ignore-tables" json:"monitoringSchemaIgnoreTables"`
	MonitorSchemaScheduler                    bool                   `mapstructure:"monitoring-schema-scheduler" toml:"monitoring-schema-scheduler" json:"monitoringSchemaScheduler"`
	MonitorSchemaSchedulerCron                string                 `mapstructure:"monitoring-schema-scheduler-cron" toml:"monitoring-schema-scheduler-cron" json:"monitoringSchemaSchedulerCron"`
	MonitorQueryRules                         bool                   `mapstructure:"monitoring-query-rules" toml:"monitoring-query-rules" json:"monitoringQueryRules"`
	MonitorSchemaChangeScript                 string                 `mapstructure:"monitoring-schema-change-script" toml:"monitoring-schema-change-script" json:"monitoringSchemaChangeScript"`
	MonitorCheckGrants                        bool                   `mapstructure:"monitoring-check-grants" toml:"monitoring-check-grants" json:"monitoringCheckGrants"`
	MonitorProcessList                        bool                   `mapstructure:"monitoring-processlist" toml:"monitoring-processlist" json:"monitoringProcesslist"`
	MonitorProcessListLimit                   string                 `mapstructure:"monitoring-processlist-limit" toml:"monitoring-processlist-limit" json:"monitoringProcesslistLimit"`
	MonitorProcessListInactive                bool                   `mapstructure:"monitoring-processlist-inactive" toml:"monitoring-processlist-inactive" json:"monitoringProcesslistInactive"`
	MonitorProcessListTransactions            bool                   `mapstructure:"monitoring-processlist-transactions" toml:"monitoring-processlist-transactions" json:"monitoringProcesslistTransactions"`
	MonitorProcessListInformationSchema       bool                   `mapstructure:"monitoring-processlist-information-schema" toml:"monitoring-processlist-information-schema" json:"monitoringProcesslistInformationSchema"`
	MonitorQueries                            bool                   `mapstructure:"monitoring-queries" toml:"monitoring-queries" json:"monitoringQueries"`
	MonitorPFS                                bool                   `mapstructure:"monitoring-performance-schema" toml:"monitoring-performance-schema" json:"monitoringPerformanceSchema"`
	MonitorPFSInstruments                     bool                   `mapstructure:"monitoring-performance-schema-instruments" toml:"monitoring-performance-schema-instruments" json:"monitoringPerformanceSchemaInstruments"`
	MonitorPFSMutex                           bool                   `mapstructure:"monitoring-performance-schema-mutex" toml:"monitoring-performance-schema-mutex" json:"monitoringPerformanceSchemaMutex"`
	MonitorPFSLatch                           bool                   `mapstructure:"monitoring-performance-schema-latch" toml:"monitoring-performance-schema-latch" json:"monitoringPerformanceSchemaLatch"`
	MonitorPFSMemory                          bool                   `mapstructure:"monitoring-performance-schema-memory" toml:"monitoring-performance-schema-memory" json:"monitoringPerformanceSchemaMemory"`
	MonitorPlugins                            bool                   `mapstructure:"monitoring-plugins" toml:"monitoring-plugins" json:"monitoringPlugins"`
	MonitorInnoDBStatus                       bool                   `mapstructure:"monitoring-innodb-status" toml:"monitoring-innodb-status" json:"monitoringInnoDBStatus"`
	MonitorLongQueryWithProcess               bool                   `mapstructure:"monitoring-long-query-with-process" toml:"monitoring-long-query-with-process" json:"monitoringLongQueryWithProcess"`
	MonitorLongQueryTime                      int                    `mapstructure:"monitoring-long-query-time" toml:"monitoring-long-query-time" json:"monitoringLongQueryTime"`
	MonitorLongQueryScript                    string                 `mapstructure:"monitoring-long-query-script" toml:"monitoring-long-query-script" json:"monitoringLongQueryScript"`
	MonitorLongQueryWithTable                 bool                   `mapstructure:"monitoring-long-query-with-table" toml:"monitoring-long-query-with-table" json:"monitoringLongQueryWithTable"`
	MonitorLongQueryLogLength                 int                    `mapstructure:"monitoring-long-query-log-length" toml:"monitoring-long-query-log-length" json:"monitoringLongQueryLogLength"`
	MonitorErrorLogLength                     int                    `mapstructure:"monitoring-error-log-length" toml:"monitoring-error-log-length" json:"monitoringErrorLogLength"`
	MonitorSqlErrorLogLength                  int                    `mapstructure:"monitoring-sql-error-log-length" toml:"monitoring-sql-error-log-length" json:"monitoringSqlErrorLogLength"`
	MonitorAuditLogLength                     int                    `mapstructure:"monitoring-audit-log-length" toml:"monitoring-audit-log-length" json:"monitoringAuditLogLength"`
	MonitorCapture                            bool                   `mapstructure:"monitoring-capture" toml:"monitoring-capture" json:"monitoringCapture"`
	MonitorCaptureFileKeep                    int                    `mapstructure:"monitoring-capture-file-keep" toml:"monitoring-capture-file-keep" json:"monitoringCaptureFileKeep"`
	MonitorDiskUsage                          bool                   `mapstructure:"monitoring-disk-usage" toml:"monitoring-disk-usage" json:"monitoringDiskUsage"`
	MonitorDiskUsagePct                       int                    `mapstructure:"monitoring-disk-usage-pct" toml:"monitoring-disk-usage-pct" json:"monitoringDiskUsagePct"`
	MonitorCaptureTrigger                     string                 `mapstructure:"monitoring-capture-trigger" toml:"monitoring-capture-trigger" json:"monitoringCaptureTrigger"`
	MonitorIgnoreErrors                       string                 `mapstructure:"monitoring-ignore-errors" toml:"monitoring-ignore-errors" json:"monitoringIgnoreErrors"`
	MonitorTenant                             string                 `mapstructure:"monitoring-tenant" toml:"monitoring-tenant" json:"monitoringTenant"`
	MonitoringAlertTrigger                    string                 `mapstructure:"monitoring-alert-trigger" toml:"monitoring-alert-trigger" json:"monitoringAlertTrigger"`
	MonitoringQueryTimeout                    int                    `mapstructure:"monitoring-query-timeout" toml:"monitoring-query-timeout" json:"monitoringQueryTimeout"`
	MonitoringOpenStateScript                 string                 `mapstructure:"monitoring-open-state-script" toml:"monitoring-open-state-script" json:"monitoringOpenStateScript"`
	MonitoringCloseStateScript                string                 `mapstructure:"monitoring-close-state-script" toml:"monitoring-close-state-script" json:"monitoringCloseStateScript"`
	Interactive                               bool                   `mapstructure:"interactive" toml:"-" json:"interactive"`
	Verbose                                   bool                   `mapstructure:"verbose" toml:"verbose" json:"verbose"`
	LogFile                                   string                 `scope:"server" mapstructure:"log-file" toml:"log-file" json:"logFile"`
	LogFileLevel                              int                    `scope:"server" mapstructure:"log-level-file" toml:"log-level-file" json:"logFileLevel"`
	LogSyslog                                 bool                   `scope:"server" mapstructure:"log-syslog" toml:"log-syslog" json:"logSyslog"`
	LogLevel                                  int                    `mapstructure:"log-level" toml:"log-level" json:"logLevel"`
	LogRotateMaxSize                          int                    `mapstructure:"log-rotate-max-size" toml:"log-rotate-max-size" json:"logRotateMaxSize"`
	LogRotateMaxBackup                        int                    `mapstructure:"log-rotate-max-backup" toml:"log-rotate-max-backup" json:"logRotateMaxBackup"`
	LogRotateMaxAge                           int                    `mapstructure:"log-rotate-max-age" toml:"log-rotate-max-age" json:"logRotateMaxAge"`
	LogTask                                   bool                   `mapstructure:"log-task" toml:"log-task" json:"logTask"`
	LogTaskLevel                              int                    `mapstructure:"log-level-task" toml:"log-level-task" json:"logTaskLevel"`
	LogSST                                    bool                   `mapstructure:"log-sst" toml:"log-sst" json:"logSst"`                  // internal replication-manager sst
	LogSSTLevel                               int                    `mapstructure:"log-level-sst" toml:"log-level-sst" json:"logSstLevel"` // internal replication-manager sst
	SSTSendBuffer                             int                    `mapstructure:"sst-send-buffer" toml:"sst-send-buffer" json:"sstSendBuffer"`
	LogHeartbeat                              bool                   `mapstructure:"log-heartbeat" toml:"log-heartbeat" json:"logHeartbeat"`
	LogHeartbeatLevel                         int                    `mapstructure:"log-level-heartbeat" toml:"log-level-heartbeat" json:"logHeartbeatLevel"`
	LogSQLInMonitoring                        bool                   `mapstructure:"log-sql-in-monitoring"  toml:"log-sql-in-monitoring" json:"logSqlInMonitoring"`
	LogSQLLevel                               int                    `mapstructure:"log-level-sql"  toml:"log-level-sql" json:"logSqlLevel"`
	LogAppLevel                               int                    `mapstructure:"log-level-app"  toml:"log-level-app" json:"logAppLevel"`
	LogWriterElection                         bool                   `mapstructure:"log-writer-election"  toml:"log-writer-election" json:"logWriterElection"`
	LogWriterElectionLevel                    int                    `mapstructure:"log-level-writer-election"  toml:"log-level-writer-election" json:"logWriterElectionLevel"`
	LogGit                                    bool                   `scope:"server" mapstructure:"log-git" toml:"log-git" json:"logGit"`
	LogGitLevel                               int                    `scope:"server" mapstructure:"log-level-git" toml:"log-level-git" json:"logGitLevel"`
	LogConfigLoad                             bool                   `mapstructure:"log-config-load" toml:"log-config-load" json:"logConfigLoad"`
	LogConfigLoadLevel                        int                    `mapstructure:"log-level-config-load" toml:"log-level-config-load" json:"logConfigLoadLevel"`
	LogBackupStream                           bool                   `mapstructure:"log-backup-stream" toml:"log-backup-stream" json:"logBackupStream"`
	LogBackupStreamLevel                      int                    `mapstructure:"log-level-backup-stream" toml:"log-level-backup-stream" json:"logBackupStreamLevel"`
	LogOrchestrator                           bool                   `mapstructure:"log-orchestrator" toml:"log-orchestrator" json:"logOrchestrator"`
	LogOrchestratorLevel                      int                    `mapstructure:"log-level-orchestrator" toml:"log-level-orchestrator" json:"logOrchestratorLevel"`
	LogTopology                               bool                   `mapstructure:"log-topology" toml:"log-topology" json:"logTopology"`
	LogTopologyLevel                          int                    `mapstructure:"log-level-topology" toml:"log-level-topology" json:"logTopologyLevel"`
	LogProxy                                  bool                   `mapstructure:"log-proxy" toml:"log-proxy" json:"logProxy"`
	LogProxyLevel                             int                    `mapstructure:"log-level-proxy" toml:"log-level-proxy" json:"logProxyLevel"`
	LogGraphite                               bool                   `mapstructure:"log-graphite" toml:"log-graphite" json:"logGraphite"`
	LogGraphiteLevel                          int                    `mapstructure:"log-level-graphite" toml:"log-level-graphite" json:"logGraphiteLevel"`
	LogBinlogPurge                            bool                   `mapstructure:"log-binlog-purge" toml:"log-binlog-purge" json:"logBinlogPurge"`
	LogBinlogPurgeLevel                       int                    `mapstructure:"log-level-binlog-purge" toml:"log-level-binlog-purge" json:"logBinlogPurgeLevel"`
	LogResticLevel                            int                    `mapstructure:"log-level-restic" toml:"log-level-restic" json:"logResticLevel"`
	LogMailerLevel                            int                    `mapstructure:"log-level-mailer" toml:"log-level-mailer" json:"logMailerLevel"`
	LogSupport                                bool                   `scope:"server" mapstructure:"log-support" toml:"log-support" json:"logSupport"`
	LogSupportLevel                           int                    `scope:"server" mapstructure:"log-level-support" toml:"log-level-support" json:"logSupportLevel"`
	LogExternalScript                         bool                   `mapstructure:"log-external-script" toml:"log-external-script" json:"ExternalScript"`
	LogExternalScriptLevel                    int                    `mapstructure:"log-level-external-script" toml:"log-level-external-script" json:"logExternalScriptLevel"`
	LogStatsLevel                             int                    `scope:"server" mapstructure:"log-level-stats" toml:"log-level-stats" json:"logStatsLevel"`
	LogLevelDatabaseErrors                    int                    `mapstructure:"log-level-database-errors" toml:"log-level-database-errors" json:"logLevelDatabaseErrors"`
	LogLevelDatabaseSqlErrors                 int                    `mapstructure:"log-level-database-sql-errors" toml:"log-level-database-sql-errors" json:"logLevelDatabaseSqlErrors"`
	LogLevelDatabaseSlowquery                 int                    `mapstructure:"log-level-database-slowquery" toml:"log-level-database-slowquery" json:"logLevelDatabaseSlowquery"`
	LogLevelDatabaseOptimize                  int                    `mapstructure:"log-level-database-optimize" toml:"log-level-database-optimize" json:"logLevelDatabaseOptimize"`
	LogLevelDatabaseAudit                     int                    `mapstructure:"log-level-database-audit" toml:"log-level-database-audit" json:"logLevelDatabaseAudit"`
	User                                      string                 `mapstructure:"db-servers-credential" toml:"db-servers-credential" json:"dbServersCredential"`
	Hosts                                     string                 `mapstructure:"db-servers-hosts" toml:"db-servers-hosts" json:"dbServersHosts"`
	DbServersChangeStateScript                string                 `mapstructure:"db-servers-state-change-script" toml:"db-servers-state-change-script" json:"dbServersStateChangeScript"`
	HostsDelayed                              string                 `mapstructure:"replication-delayed-hosts" toml:"replication-delayed-hosts" json:"replicationDelayedHosts"`
	HostsDelayedTime                          int                    `mapstructure:"replication-delayed-time" toml:"replication-delayed-time" json:"replicationDelayedTime"`
	DBServersTLSUseGeneratedCertificate       bool                   `mapstructure:"db-servers-tls-use-generated-cert" toml:"db-servers-tls-use-generated-cert" json:"dbServersUseGeneratedCert"`
	HostsTLSCA                                string                 `mapstructure:"db-servers-tls-ca-cert" toml:"db-servers-tls-ca-cert" json:"dbServersTlsCaCert"`
	HostsTlsCliKey                            string                 `mapstructure:"db-servers-tls-client-key" toml:"db-servers-tls-client-key" json:"dbServersTlsClientKey"`
	HostsTlsCliCert                           string                 `mapstructure:"db-servers-tls-client-cert" toml:"db-servers-tls-client-cert" json:"dbServersTlsClientCert"`
	HostsTlsSrvKey                            string                 `mapstructure:"db-servers-tls-server-key" toml:"db-servers-tls-server-key" json:"dbServersTlsServerKey"`
	HostsTlsSrvCert                           string                 `mapstructure:"db-servers-tls-server-cert" toml:"db-servers-tls-server-cert" json:"dbServersTlsServerCert"`
	HostsTlsSslMode                           string                 `mapstructure:"db-servers-tls-ssl-mode" toml:"db-servers-tls-ssl-mode" json:"dbServersTlsSslMode"`
	PrefMaster                                string                 `mapstructure:"db-servers-prefered-master" toml:"db-servers-prefered-master" json:"dbServersPreferedMaster"`
	BackupServers                             string                 `mapstructure:"db-servers-backup-hosts" toml:"db-servers-backup-hosts" json:"dbServersBackupHosts"`
	IgnoreSrv                                 string                 `mapstructure:"db-servers-ignored-hosts" toml:"db-servers-ignored-hosts" json:"dbServersIgnoredHosts"`
	IgnoreSrvRO                               string                 `mapstructure:"db-servers-ignored-readonly" toml:"db-servers-ignored-readonly" json:"dbServersIgnoredReadonly"`
	Timeout                                   int                    `mapstructure:"db-servers-connect-timeout" toml:"db-servers-connect-timeout" json:"dbServersConnectTimeout"`
	ExecTimeout                               int                    `mapstructure:"db-servers-exec-timeout" toml:"db-servers-exec-timeout" json:"dbServersExecTimeout"`
	ReadTimeout                               int                    `mapstructure:"db-servers-read-timeout" toml:"db-servers-read-timeout" json:"dbServersReadTimeout"`
	DBServersLocality                         string                 `mapstructure:"db-servers-locality" toml:"db-servers-locality" json:"dbServersLocality"`
	DbServersBindAddress                      string                 `mapstructure:"db-servers-bind-address" toml:"db-servers-bind-address" json:"dbServersBindAddress"`
	PRXServersReadOnMaster                    bool                   `mapstructure:"proxy-servers-read-on-master" toml:"proxy-servers-read-on-master" json:"proxyServersReadOnMaster"`
	PRXServersReadOnMasterNoSlave             bool                   `mapstructure:"proxy-servers-read-on-master-no-slave" toml:"proxy-servers-read-on-master-no-slave" json:"proxyServersReadOnMasterNoSlave"`
	PRXServersBackendCompression              bool                   `mapstructure:"proxy-servers-backend-compression" toml:"proxy-servers-backend-compression" json:"proxyServersBackendCompression"`
	PRXServersBackendMaxReplicationLag        int                    `mapstructure:"proxy-servers-backend-max-replication-lag" toml:"proxy-servers-backend--max-replication-lag" json:"proxyServersBackendMaxReplicationLag"`
	PRXServersBackendMaxConnections           int                    `mapstructure:"proxy-servers-backend-max-connections" toml:"proxy-servers-backend--max-connections" json:"proxyServersBackendMaxConnections"`
	PRXServersChangeStateScript               string                 `mapstructure:"proxy-servers-change-state-script" toml:"proxy-servers-change-state-script" json:"proxyServersChangeStateScript"`
	ClusterHead                               string                 `mapstructure:"cluster-head" toml:"cluster-head" json:"clusterHead"`
	ReplicationMultisourceHeadClusters        string                 `mapstructure:"replication-multisource-head-clusters" toml:"replication-multisource-head-clusters" json:"replicationMultisourceHeadClusters"`
	MasterConnectRetry                        int                    `mapstructure:"replication-master-connect-retry" toml:"replication-master-connect-retry" json:"replicationMasterConnectRetry"`
	MasterRetryCount                          int                    `mapstructure:"replication-master-retry-count" toml:"replication-master-retry-count" json:"replicationMasterRetryCount"`
	RplUser                                   string                 `mapstructure:"replication-credential" toml:"replication-credential" json:"replicationCredential"`
	ReplicationErrorScript                    string                 `mapstructure:"replication-error-script" toml:"replication-error-script" json:"replicationErrorScript"`
	MasterConn                                string                 `mapstructure:"replication-source-name" toml:"replication-source-name" json:"replicationSourceName"`
	ReplicationSSL                            bool                   `mapstructure:"replication-use-ssl" toml:"replication-use-ssl" json:"replicationUseSsl"`
	ActivePassive                             bool                   `mapstructure:"replication-active-passive" toml:"replication-active-passive" json:"replicationActivePassive"`
	DynamicTopology                           bool                   `mapstructure:"replication-dynamic-topology" toml:"replication-dynamic-topology" json:"replicationDynamicTopology"`
	MultiMasterRing                           bool                   `mapstructure:"replication-multi-master-ring" toml:"replication-multi-master-ring" json:"replicationMultiMasterRing"`
	MultiMasterRingUnsafe                     bool                   `mapstructure:"replication-multi-master-ring-unsafe" toml:"replication-multi-master-ring-unsafe" json:"replicationMultiMasterRingUnsafe"`
	MultiMasterWsrep                          bool                   `mapstructure:"replication-multi-master-wsrep" toml:"replication-multi-master-wsrep" json:"replicationMultiMasterWsrep"`
	MultiMasterGrouprep                       bool                   `mapstructure:"replication-multi-master-grouprep" toml:"replication-multi-master-grouprep" json:"replicationMultiMasterGrouprep"`
	MultiMasterGrouprepPort                   int                    `mapstructure:"replication-multi-master-grouprep-port" toml:"replication-multi-master-grouprep-port" json:"replicationMultiMasterGrouprepPort"`
	MultiMasterWsrepSSTMethod                 string                 `mapstructure:"replication-multi-master-wsrep-sst-method" toml:"replication-multi-master-wsrep-sst-method" json:"replicationMultiMasterWsrepSSTMethod"`
	MultiMasterWsrepPort                      int                    `mapstructure:"replication-multi-master-wsrep-port" toml:"replication-multi-master-wsrep-port" json:"replicationMultiMasterWsrepPort"`
	MultiMaster                               bool                   `mapstructure:"replication-multi-master" toml:"replication-multi-master" json:"replicationMultiMaster"`
	MultiMasterConcurrentWrite                bool                   `mapstructure:"replication-multi-master-concurrent-write" toml:"replication-multi-master-concurrent-write" json:"replicationMultiMasterConcurrentWrite"`
	MultiTierSlave                            bool                   `mapstructure:"replication-multi-tier-slave" toml:"replication-multi-tier-slave" json:"replicationMultiTierSlave"`
	MasterSlavePgStream                       bool                   `mapstructure:"replication-master-slave-pg-stream" toml:"replication-master-slave-pg-stream" json:"replicationMasterSlavePgStream"`
	MasterSlavePgLogical                      bool                   `mapstructure:"replication-master-slave-pg-logical" toml:"replication-master-slave-pg-logical" json:"replicationMasterSlavePgLogical"`
	ReplicationNoRelay                        bool                   `mapstructure:"replication-master-slave-never-relay" toml:"replication-master-slave-never-relay" json:"replicationMasterSlaveNeverRelay"`
	ReplicationRestartOnSQLErrorMatch         string                 `mapstructure:"replication-restart-on-sqlerror-match" toml:"replication-restart-on-sqlerror-match" json:"eeplicationRestartOnSqlLErrorMatch"`
	SwitchWaitKill                            int64                  `mapstructure:"switchover-wait-kill" toml:"switchover-wait-kill" json:"switchoverWaitKill"`
	SwitchWaitTrx                             int64                  `mapstructure:"switchover-wait-trx" toml:"switchover-wait-trx" json:"switchoverWaitTrx"`
	SwitchWaitWrite                           int                    `mapstructure:"switchover-wait-write-query" toml:"switchover-wait-write-query" json:"switchoverWaitWriteQuery"`
	SwitchGtidCheck                           bool                   `mapstructure:"switchover-at-equal-gtid" toml:"switchover-at-equal-gtid" json:"switchoverAtEqualGtid"`
	SwitchLowerRelease                        bool                   `mapstructure:"switchover-lower-release" toml:"switchover-lower-release" json:"switchoverLowerRelease"`
	SwitchSync                                bool                   `mapstructure:"switchover-at-sync" toml:"switchover-at-sync" json:"switchoverAtSync"`
	SwitchMaxDelay                            int64                  `mapstructure:"switchover-max-slave-delay" toml:"switchover-max-slave-delay" json:"switchoverMaxSlaveDelay"`
	SwitchSlaveWaitCatch                      bool                   `mapstructure:"switchover-slave-wait-catch" toml:"switchover-slave-wait-catch" json:"switchoverSlaveWaitCatch"`
	SwitchSlaveWaitRouteChange                int                    `mapstructure:"switchover-wait-route-change" toml:"switchover-wait-route-change" json:"switchoverWaitRouteChange"`
	SwitchDecreaseMaxConn                     bool                   `mapstructure:"switchover-decrease-max-conn" toml:"switchover-decrease-max-conn" json:"switchoverDecreaseMaxConn"`
	SwitchDecreaseMaxConnValue                int64                  `mapstructure:"switchover-decrease-max-conn-value" toml:"switchover-decrease-max-conn-value" json:"switchoverDecreaseMaxConnValue"`
	SwitchLockUserOnFreeze                    bool                   `mapstructure:"switchover-lock-user-on-freeze" toml:"switchover-lock-user-on-freeze" json:"switchoverLockUserOnFreeze"`
	SwitchRedirectOnFreeze                    bool                   `mapstructure:"switchover-redirect-on-freeze" toml:"switchover-redirect-on-freeze" json:"switchoverRedirectOnFreeze"`
	FailLimit                                 int                    `mapstructure:"failover-limit" toml:"failover-limit" json:"failoverLimit"`
	PreScript                                 string                 `mapstructure:"failover-pre-script" toml:"failover-pre-script" json:"failoverPreScript"`
	PostScript                                string                 `mapstructure:"failover-post-script" toml:"failover-post-script" json:"failoverPostScript"`
	ReadOnly                                  bool                   `mapstructure:"failover-readonly-state" toml:"failover-readonly-state" json:"failoverReadOnlyState"`
	FailoverSemiSyncState                     bool                   `mapstructure:"failover-semisync-state" toml:"failover-semisync-state" json:"failoverSemisyncState"`
	SuperReadOnly                             bool                   `mapstructure:"failover-superreadonly-state" toml:"failover-superreadonly-state" json:"failoverSuperReadOnlyState"`
	FailTime                                  int64                  `mapstructure:"failover-time-limit" toml:"failover-time-limit" json:"failoverTimeLimit"`
	FailSync                                  bool                   `mapstructure:"failover-at-sync" toml:"failover-at-sync" json:"failoverAtSync"`
	FailEventScheduler                        bool                   `mapstructure:"failover-event-scheduler" toml:"failover-event-scheduler" json:"failoverEventScheduler"`
	FailEventStatus                           bool                   `mapstructure:"failover-event-status" toml:"failover-event-status" json:"failoverEventStatus"`
	FailRestartUnsafe                         bool                   `mapstructure:"failover-restart-unsafe" toml:"failover-restart-unsafe" json:"failoverRestartUnsafe"`
	FailResetTime                             int64                  `mapstructure:"failcount-reset-time" toml:"failover-reset-time" json:"failoverResetTime"`
	FailMode                                  string                 `mapstructure:"failover-mode" toml:"failover-mode" json:"failoverMode"`
	FailMaxDelay                              int64                  `mapstructure:"failover-max-slave-delay" toml:"failover-max-slave-delay" json:"failoverMaxSlaveDelay"`
	FailoverMdevCheck                         bool                   `mapstructure:"failover-mdev-check" toml:"failover-mdev-check" json:"failoverMdevCheck"`
	FailoverMdevLevel                         string                 `mapstructure:"failover-mdev-level" toml:"failover-mdev-level" json:"failoverMdevLevel"`
	MaxFail                                   int                    `mapstructure:"failover-falsepositive-ping-counter" toml:"failover-falsepositive-ping-counter" json:"failoverFalsePositivePingCounter"`
	CheckFalsePositiveHeartbeat               bool                   `mapstructure:"failover-falsepositive-heartbeat" toml:"failover-falsepositive-heartbeat" json:"failoverFalsePositiveHeartbeat"`
	CheckFalsePositiveMaxscale                bool                   `mapstructure:"failover-falsepositive-maxscale" toml:"failover-falsepositive-maxscale" json:"failoverFalsePositiveMaxscale"`
	CheckFalsePositiveHeartbeatTimeout        int                    `mapstructure:"failover-falsepositive-heartbeat-timeout" toml:"failover-falsepositive-heartbeat-timeout" json:"failoverFalsePositiveHeartbeatTimeout"`
	CheckFalsePositiveMaxscaleTimeout         int                    `mapstructure:"failover-falsepositive-maxscale-timeout" toml:"failover-falsepositive-maxscale-timeout" json:"failoverFalsePositiveMaxscaleTimeout"`
	CheckFalsePositiveExternal                bool                   `mapstructure:"failover-falsepositive-external" toml:"failover-falsepositive-external" json:"failoverFalsePositiveExternal"`
	CheckFalsePositiveExternalPort            int                    `mapstructure:"failover-falsepositive-external-port" toml:"failover-falsepositive-external-port" json:"failoverFalsePositiveExternalPort"`
	FailoverLogFileKeep                       int                    `mapstructure:"failover-log-file-keep" toml:"failover-log-file-keep" json:"failoverLogFileKeep"`
	FailoverSwitchToPrefered                  bool                   `mapstructure:"failover-switch-to-prefered" toml:"failover-switch-to-prefered" json:"failoverSwithToPrefered"`
	DelayStatCapture                          bool                   `mapstructure:"delay-stat-capture" toml:"delay-stat-capture" json:"delayStatCapture"`
	PrintDelayStat                            bool                   `mapstructure:"print-delay-stat" toml:"print-delay-stat" json:"printDelayStat"`
	PrintDelayStatHistory                     bool                   `mapstructure:"print-delay-stat-history" toml:"print-delay-stat-history" json:"printDelayStatHistory"`
	PrintDelayStatInterval                    int                    `mapstructure:"print-delay-stat-interval" toml:"print-delay-stat-interval" json:"printDelayStatInterval"`
	DelayStatRotate                           int                    `mapstructure:"delay-stat-rotate" toml:"delay-stat-rotate" json:"delayStatRotate"`
	FailoverCheckDelayStat                    bool                   `mapstructure:"failover-check-delay-stat" toml:"failover-check-delay-stat" json:"failoverCheckDelayStat"`
	Autorejoin                                bool                   `mapstructure:"autorejoin" toml:"autorejoin" json:"autorejoin"`
	Autoseed                                  bool                   `mapstructure:"autoseed" toml:"autoseed" json:"autoseed"`
	AutorejoinForceRestore                    bool                   `mapstructure:"autorejoin-force-restore" toml:"autorejoin-force-restore" json:"autorejoinForceRestore"`
	AutorejoinFlashback                       bool                   `mapstructure:"autorejoin-flashback" toml:"autorejoin-flashback" json:"autorejoinFlashback"`
	AutorejoinMysqldump                       bool                   `mapstructure:"autorejoin-mysqldump" toml:"autorejoin-mysqldump" json:"autorejoinMysqldump"`
	AutorejoinZFSFlashback                    bool                   `mapstructure:"autorejoin-zfs-flashback" toml:"autorejoin-zfs-flashback" json:"autorejoinZfsFlashback"`
	AutorejoinPhysicalBackup                  bool                   `mapstructure:"autorejoin-physical-backup" toml:"autorejoin-physical-backup" json:"autorejoinPhysicalBackup"`
	AutorejoinLogicalBackup                   bool                   `mapstructure:"autorejoin-logical-backup" toml:"autorejoin-logical-backup" json:"autorejoinLogicalBackup"`
	RejoinScript                              string                 `mapstructure:"autorejoin-script" toml:"autorejoin-script" json:"autorejoinScript"`
	AutorejoinBackupBinlog                    bool                   `mapstructure:"autorejoin-backup-binlog" toml:"autorejoin-backup-binlog" json:"autorejoinBackupBinlog"`
	AutorejoinSemisync                        bool                   `mapstructure:"autorejoin-flashback-on-sync" toml:"autorejoin-flashback-on-sync" json:"autorejoinFlashbackOnSync"`
	AutorejoinNoSemisync                      bool                   `mapstructure:"autorejoin-flashback-on-unsync" toml:"autorejoin-flashback-on-unsync" json:"autorejoinFlashbackOnUnsync"`
	AutorejoinSlavePositionalHeartbeat        bool                   `mapstructure:"autorejoin-slave-positional-heartbeat" toml:"autorejoin-slave-positional-heartbeat" json:"autorejoinSlavePositionalHeartbeat"`
	CheckType                                 string                 `mapstructure:"check-type" toml:"check-type" json:"checkType"`
	CheckReplFilter                           bool                   `mapstructure:"check-replication-filters" toml:"check-replication-filters" json:"checkReplicationFilters"`
	CheckBinFilter                            bool                   `mapstructure:"check-binlog-filters" toml:"check-binlog-filters" json:"checkBinlogFilters"`
	CheckBinServerId                          int                    `mapstructure:"check-binlog-server-id" toml:"check-binlog-server-id" json:"checkBinlogServerId"`
	CheckGrants                               bool                   `mapstructure:"check-grants" toml:"check-grants" json:"checkGrants"`
	RplChecks                                 bool                   `mapstructure:"check-replication-state" toml:"check-replication-state" json:"checkReplicationState"`
	RplCheckErrantTrx                         bool                   `mapstructure:"check-replication-errant-trx" toml:"check-replication-errant-trx" json:"checkReplicationErrantTrx"`
	ForceSlaveHeartbeat                       bool                   `mapstructure:"force-slave-heartbeat" toml:"force-slave-heartbeat" json:"forceSlaveHeartbeat"`
	ForceSlaveHeartbeatTime                   int                    `mapstructure:"force-slave-heartbeat-time" toml:"force-slave-heartbeat-time" json:"forceSlaveHeartbeatTime"`
	ForceSlaveHeartbeatRetry                  int                    `mapstructure:"force-slave-heartbeat-retry" toml:"force-slave-heartbeat-retry" json:"forceSlaveHeartbeatRetry"`
	ForceSlaveGtid                            bool                   `mapstructure:"force-slave-gtid-mode" toml:"force-slave-gtid-mode" json:"forceSlaveGtidMode"`
	ForceSlaveGtidStrict                      bool                   `mapstructure:"force-slave-gtid-mode-strict" toml:"force-slave-gtid-mode-strict" json:"forceSlaveGtidModeStrict"`
	ForceSlaveNoGtid                          bool                   `mapstructure:"force-slave-no-gtid-mode" toml:"force-slave-no-gtid-mode" json:"forceSlaveNoGtidMode"`
	ForceSlaveIdempotent                      bool                   `mapstructure:"force-slave-idempotent" toml:"force-slave-idempotent" json:"forceSlaveIdempotent"`
	ForceSlaveStrict                          bool                   `mapstructure:"force-slave-strict" toml:"force-slave-strict" json:"forceSlaveStrict"`
	ForceSlaveParallelMode                    string                 `mapstructure:"force-slave-parallel-mode" toml:"force-slave-parallel-mode" json:"forceSlaveParallelMode"`
	ForceSlaveSemisync                        bool                   `mapstructure:"force-slave-semisync" toml:"force-slave-semisync" json:"forceSlaveSemisync"`
	ForceSlaveReadOnly                        bool                   `mapstructure:"force-slave-readonly" toml:"force-slave-readonly" json:"forceSlaveReadonly"`
	ForceBinlogRow                            bool                   `mapstructure:"force-binlog-row" toml:"force-binlog-row" json:"forceBinlogRow"`
	ForceBinlogAnnotate                       bool                   `mapstructure:"force-binlog-annotate" toml:"force-binlog-annotate" json:"forceBinlogAnnotate"`
	ForceBinlogCompress                       bool                   `mapstructure:"force-binlog-compress" toml:"force-binlog-compress" json:"forceBinlogCompress"`
	ForceBinlogSlowqueries                    bool                   `mapstructure:"force-binlog-slowqueries" toml:"force-binlog-slowqueries" json:"forceBinlogSlowqueries"`
	ForceBinlogChecksum                       bool                   `mapstructure:"force-binlog-checksum" toml:"force-binlog-checksum" json:"forceBinlogChecksum"`
	ForceBinlogPurge                          bool                   `mapstructure:"force-binlog-purge" toml:"force-binlog-purge" json:"forceBinlogPurge"`
	ForceBinlogPurgeReplicas                  bool                   `mapstructure:"force-binlog-purge-replicas" toml:"force-binlog-purge-replicas" json:"forceBinlogPurgeReplicas"`
	ForceBinlogPurgeOnRestore                 bool                   `mapstructure:"force-binlog-purge-on-restore" toml:"force-binlog-purge-on-restore" json:"forceBinlogPurgeOnRestore"`
	ForceBinlogPurgeTotalSize                 int                    `mapstructure:"force-binlog-purge-total-size" toml:"force-binlog-purge-total-size" json:"forceBinlogPurgeTotalSize"`
	ForceBinlogPurgeMinReplica                int                    `mapstructure:"force-binlog-purge-min-replica" toml:"force-binlog-purge-min-replica" json:"forceBinlogPurgeMinReplica"`
	ForceInmemoryBinlogCacheSize              bool                   `mapstructure:"force-inmemory-binlog-cache-size" toml:"force-inmemory-binlog-cache-size" json:"forceInmemoryBinlogCacheSize"`
	ForceDiskRelayLogSizeLimit                bool                   `mapstructure:"force-disk-relaylog-size-limit" toml:"force-disk-relaylog-size-limit" json:"forceDiskRelaylogSizeLimit"`
	ForceDiskRelayLogSizeLimitSize            uint64                 `mapstructure:"force-disk-relaylog-size-limit-size"  toml:"force-disk-relaylog-size-limit-size" json:"forceDiskRelaylogSizeLimitSize"`
	ForceSyncBinlog                           bool                   `mapstructure:"force-sync-binlog" toml:"force-sync-binlog" json:"forceSyncBinlog"`
	ForceSyncInnoDB                           bool                   `mapstructure:"force-sync-innodb" toml:"force-sync-innodb" json:"forceSyncInnodb"`
	ForceNoslaveBehind                        bool                   `mapstructure:"force-noslave-behind" toml:"force-noslave-behind" json:"forceNoslaveBehind"`
	Spider                                    bool                   `mapstructure:"spider" toml:"-" json:"-"`
	BindAddr                                  string                 `scope:"server" mapstructure:"http-bind-address" toml:"http-bind-address" json:"httpBindAdress"`
	HttpPort                                  string                 `scope:"server" mapstructure:"http-port" toml:"http-port" json:"httpPort"`
	HttpServ                                  bool                   `scope:"server" mapstructure:"http-server" toml:"http-server" json:"httpServer"`
	ApiServ                                   bool                   `scope:"server" mapstructure:"api-server" toml:"api-server" json:"apiServer"`
	HttpRoot                                  string                 `scope:"server" mapstructure:"http-root" toml:"http-root" json:"httpRoot"`
	HttpAuth                                  bool                   `scope:"server" mapstructure:"http-auth" toml:"http-auth" json:"httpAuth"`
	HttpUseReact                              bool                   `scope:"server" mapstructure:"http-use-react" toml:"http-use-react" json:"http-use-react"`
	HttpBootstrapButton                       bool                   `scope:"server" mapstructure:"http-bootstrap-button" toml:"http-bootstrap-button" json:"httpBootstrapButton"`
	SessionLifeTime                           int                    `scope:"server" mapstructure:"http-session-lifetime" toml:"http-session-lifetime" json:"httpSessionLifetime"`
	HttpRefreshInterval                       int                    `scope:"server" mapstructure:"http-refresh-interval" toml:"http-refresh-interval" json:"httpRefreshInterval"`
	Daemon                                    bool                   `mapstructure:"daemon" toml:"-" json:"-"`
	MailFrom                                  string                 `scope:"server" mapstructure:"mail-from" toml:"mail-from" json:"mailFrom"`
	MailTo                                    string                 `scope:"server" mapstructure:"mail-to" toml:"mail-to" json:"mailTo"`
	MailSMTPAddr                              string                 `scope:"server" mapstructure:"mail-smtp-addr" toml:"mail-smtp-addr" json:"mailSmtpAddr"`
	MailSMTPUser                              string                 `scope:"server" mapstructure:"mail-smtp-user" toml:"mail-smtp-user" json:"mailSmtpUser"`
	MailSMTPPassword                          string                 `scope:"server" mapstructure:"mail-smtp-password" toml:"mail-smtp-password" json:"mailSmtpPassword"`
	MailSMTPTLSSkipVerify                     bool                   `scope:"server" mapstructure:"mail-smtp-tls-skip-verify" toml:"mail-smtp-tls-skip-verify" json:"mailSmtpTlsSkipVerify"`
	MailMaxPool                               int                    `scope:"server" mapstructure:"mail-max-pool" toml:"mail-max-pool" json:"mailMaxPool"`
	MailTimeout                               int                    `scope:"server" mapstructure:"mail-timeout" toml:"mail-timeout" json:"mailTimeout"`
	SlackURL                                  string                 `mapstructure:"alert-slack-url" toml:"alert-slack-url" json:"alertSlackUrl"`
	SlackChannel                              string                 `mapstructure:"alert-slack-channel" toml:"alert-slack-channel" json:"alertSlackChannel"`
	SlackUser                                 string                 `mapstructure:"alert-slack-user" toml:"alert-slack-user" json:"alertSlackUser"`
	PushoverAppToken                          string                 `mapstructure:"alert-pushover-app-token" toml:"alert-pushover-app-token" json:"alertPushoverAppToken"`
	PushoverUserToken                         string                 `mapstructure:"alert-pushover-user-token" toml:"alert-pushover-user-token" json:"alertPushoverUserToken"`
	TeamsUrl                                  string                 `mapstructure:"alert-teams-url" toml:"alert-teams-url" json:"alertTeamsUrl"`
	TeamsProxyUrl                             string                 `mapstructure:"alert-teams-proxy-url" toml:"alert-teams-proxy-url" json:"alertTeamsProxyUrl"`
	TeamsAlertState                           string                 `mapstructure:"alert-teams-state" toml:"alert-teams-state" json:"alertTeamsState"`
	Heartbeat                                 bool                   `mapstructure:"heartbeat-table" toml:"heartbeat-table" json:"heartbeatTable"`
	ExtProxyOn                                bool                   `mapstructure:"extproxy" toml:"extproxy" json:"extproxy"`
	ExtProxyVIP                               string                 `mapstructure:"extproxy-address" toml:"extproxy-address" json:"extproxyAddress"`
	MdbsProxyOn                               bool                   `mapstructure:"shardproxy" toml:"shardproxy" json:"shardproxy"`
	MdbsProxyDebug                            bool                   `mapstructure:"shardproxy-debug" toml:"shardproxy-debug" json:"shardproxyDebug"`
	MdbsProxyLogLevel                         int                    `mapstructure:"log-level-shardproxy" toml:"log-level-shardproxy" json:"shardproxyLogLevel"`
	MdbsProxyHosts                            string                 `mapstructure:"shardproxy-servers" toml:"shardproxy-servers" json:"shardproxyServers"`
	MdbsJanitorWeights                        string                 `mapstructure:"shardproxy-janitor-weights" toml:"shardproxy-janitor-weights" json:"shardproxyJanitorWeights"`
	MdbsProxyCredential                       string                 `mapstructure:"shardproxy-credential" toml:"shardproxy-credential" json:"shardproxyCredential"`
	MdbsHostsIPV6                             string                 `mapstructure:"shardproxy-servers-ipv6" toml:"shardproxy-servers-ipv6" json:"shardproxyServers-ipv6"`
	MdbsProxyCopyGrants                       bool                   `mapstructure:"shardproxy-copy-grants" toml:"shardproxy-copy-grants" json:"shardproxyCopyGrants"`
	MdbsProxyLoadSystem                       bool                   `mapstructure:"shardproxy-load-system" toml:"shardproxy-load-system" json:"shardproxyLoadSystem"`
	MdbsUniversalTables                       string                 `mapstructure:"shardproxy-universal-tables" toml:"shardproxy-universal-tables" json:"shardproxyUniversalTables"`
	MdbsIgnoreTables                          string                 `mapstructure:"shardproxy-ignore-tables" toml:"shardproxy-ignore-tables" json:"shardproxyIgnoreTables"`
	MxsOn                                     bool                   `mapstructure:"maxscale" toml:"maxscale" json:"maxscale"`
	MxsDebug                                  bool                   `mapstructure:"maxscale-debug" toml:"maxscale-debug" json:"maxscaleDebug"`
	MxsLogLevel                               int                    `mapstructure:"log-level-maxscale" toml:"log-level-maxscale" json:"maxscaleLogLevel"`
	MxsHost                                   string                 `mapstructure:"maxscale-servers" toml:"maxscale-servers" json:"maxscaleServers"`
	MxsPort                                   string                 `mapstructure:"maxscale-port" toml:"maxscale-port" json:"maxscalePort"`
	MxsUser                                   string                 `mapstructure:"maxscale-user" toml:"maxscale-user" json:"maxscaleUser"`
	MxsPass                                   string                 `mapstructure:"maxscale-pass" toml:"maxscale-pass" json:"maxscalePass"`
	MxsHostsIPV6                              string                 `mapstructure:"maxscale-servers-ipv6" toml:"maxscale-servers-ipv6" json:"maxscaleServers-ipv6"`
	MxsJanitorWeights                         string                 `mapstructure:"maxscale-janitor-weights" toml:"maxscale-janitor-weights" json:"maxscaleJanitorWeights"`
	MxsWritePort                              int                    `mapstructure:"maxscale-write-port" toml:"maxscale-write-port" json:"maxscaleWritePort"`
	MxsReadPort                               int                    `mapstructure:"maxscale-read-port" toml:"maxscale-read-port" json:"maxscaleReadPort"`
	MxsReadWritePort                          int                    `mapstructure:"maxscale-read-write-port" toml:"maxscale-read-write-port" json:"maxscaleReadWritePort"`
	MxsMaxinfoPort                            int                    `mapstructure:"maxscale-maxinfo-port" toml:"maxscale-maxinfo-port" json:"maxscaleMaxinfoPort"`
	MxsBinlogOn                               bool                   `mapstructure:"maxscale-binlog" toml:"maxscale-binlog" json:"maxscaleBinlog"`
	MxsBinlogPort                             int                    `mapstructure:"maxscale-binlog-port" toml:"maxscale-binlog-port" json:"maxscaleBinlogPort"`
	MxsDisableMonitor                         bool                   `mapstructure:"maxscale-disable-monitor" toml:"maxscale-disable-monitor" json:"maxscaleDisableMonitor"`
	MxsGetInfoMethod                          string                 `mapstructure:"maxscale-get-info-method" toml:"maxscale-get-info-method" json:"maxscaleGetInfoMethod"`
	MxsServerMatchPort                        bool                   `mapstructure:"maxscale-server-match-port" toml:"maxscale-server-match-port" json:"maxscaleServerMatchPort"`
	MxsBinaryPath                             string                 `mapstructure:"maxscale-binary-path" toml:"maxscale-binary-path" json:"maxscalemBinaryPath"`
	MyproxyOn                                 bool                   `mapstructure:"myproxy" toml:"myproxy" json:"myproxy"`
	MyproxyDebug                              bool                   `mapstructure:"myproxy-debug" toml:"myproxy-debug" json:"myproxyDebug"`
	MyproxyLogLevel                           int                    `mapstructure:"log-level-myproxy" toml:"log-level-myproxy" json:"myproxyLogLevel"`
	MyproxyPort                               int                    `mapstructure:"myproxy-port" toml:"myproxy-port" json:"myproxyPort"`
	MyproxyUser                               string                 `mapstructure:"myproxy-user" toml:"myproxy-user" json:"myproxyUser"`
	MyproxyPassword                           string                 `mapstructure:"myproxy-password" toml:"myproxy-password" json:"myproxyPassword"`
	HaproxyOn                                 bool                   `mapstructure:"haproxy" toml:"haproxy" json:"haproxy"`
	HaproxyDebug                              bool                   `mapstructure:"haproxy-debug" toml:"haproxy-debug" json:"haproxyDebug"`
	HaproxyLogLevel                           int                    `mapstructure:"log-level-haproxy" toml:"log-level-haproxy" json:"haproxyLogLevel"`
	HaproxyUser                               string                 `mapstructure:"haproxy-user" toml:"haproxy-user" json:"haproxylUser"`
	HaproxyPassword                           string                 `mapstructure:"haproxy-password" toml:"haproxy-password" json:"haproxyPassword"`
	HaproxyMode                               string                 `mapstructure:"haproxy-mode" toml:"haproxy-mode" json:"haproxyMode"`
	HaproxyHosts                              string                 `mapstructure:"haproxy-servers" toml:"haproxy-servers" json:"haproxyServers"`
	HaproxyJanitorWeights                     string                 `mapstructure:"haproxy-janitor-weights" toml:"haproxy-janitor-weights" json:"haproxyJanitorWeights"`
	HaproxyWritePort                          int                    `mapstructure:"haproxy-write-port" toml:"haproxy-write-port" json:"haproxyWritePort"`
	HaproxyReadPort                           int                    `mapstructure:"haproxy-read-port" toml:"haproxy-read-port" json:"haproxyReadPort"`
	HaproxyStatPort                           int                    `mapstructure:"haproxy-stat-port" toml:"haproxy-stat-port" json:"haproxyStatPort"`
	HaproxyAPIPort                            int                    `mapstructure:"haproxy-api-port" toml:"haproxy-api-port" json:"haproxyAPIPort"`
	HaproxyWriteBindIp                        string                 `mapstructure:"haproxy-ip-write-bind" toml:"haproxy-ip-write-bind" json:"haproxyIpWriteBind"`
	HaproxyReadBindIp                         string                 `mapstructure:"haproxy-ip-read-bind" toml:"haproxy-ip-read-bind" json:"haproxyIpReadBind"`
	HaproxyHostsIPV6                          string                 `mapstructure:"haproxy-servers-ipv6" toml:"haproxy-servers-ipv6" json:"haproxyServers-ipv6"`
	HaproxyBinaryPath                         string                 `mapstructure:"haproxy-binary-path" toml:"haproxy-binary-path" json:"haproxyBinaryPath"`
	HaproxyAPIReadBackend                     string                 `mapstructure:"haproxy-api-read-backend"  toml:"haproxy-api-read-backend" json:"haproxyAPIReadBackend"`
	HaproxyAPIWriteBackend                    string                 `mapstructure:"haproxy-api-write-backend"  toml:"haproxy-api-write-backend" json:"haproxyAPIWriteBackend"`
	HaproxyStagingPort                        string                 `mapstructure:"haproxy-staging-port"  toml:"haproxy-staging-port" json:"haproxyStagingPort"`
	HaproxyStagingBind                        string                 `mapstructure:"haproxy-staging-bind" toml:"haproxy-staging-bind" json:"haproxyStagingBind"`
	HaproxyStagingBackend                     string                 `mapstructure:"haproxy-staging-backend" toml:"haproxy-staging-backend" json:"haproxyStagingBackend"`
	ProxysqlOn                                bool                   `mapstructure:"proxysql" toml:"proxysql" json:"proxysql"`
	ProxysqlDebug                             bool                   `mapstructure:"proxysql-debug" toml:"proxysql-debug" json:"proxysqlDebug"`
	ProxysqlLogLevel                          int                    `mapstructure:"log-level-proxysql" toml:"log-level-proxysql" json:"proxysqlLogLevel"`
	ProxysqlSaveToDisk                        bool                   `mapstructure:"proxysql-save-to-disk" toml:"proxysql-save-to-disk" json:"proxysqlSaveToDisk"`
	ProxysqlHosts                             string                 `mapstructure:"proxysql-servers" toml:"proxysql-servers" json:"proxysqlServers"`
	ProxysqlHostsIPV6                         string                 `mapstructure:"proxysql-servers-ipv6" toml:"proxysql-servers-ipv6" json:"proxysqlServersIpv6"`
	ProxysqlJanitorWeights                    string                 `mapstructure:"proxysql-janitor-weights" toml:"proxysql-janitor-weights" json:"proxysqlJanitorWeights"`
	ProxysqlPort                              string                 `mapstructure:"proxysql-port" toml:"proxysql-port" json:"proxysqlPort"`
	ProxysqlAdminPort                         string                 `mapstructure:"proxysql-admin-port" toml:"proxysql-admin-port" json:"proxysqlAdminPort"`
	ProxysqlUser                              string                 `mapstructure:"proxysql-user" toml:"proxysql-user" json:"proxysqlUser"`
	ProxysqlPassword                          string                 `mapstructure:"proxysql-password" toml:"proxysql-password" json:"proxysqlPassword"`
	ProxysqlWriterHostgroup                   string                 `mapstructure:"proxysql-writer-hostgroup" toml:"proxysql-writer-hostgroup" json:"proxysqlWriterHostgroup"`
	ProxysqlReaderHostgroup                   string                 `mapstructure:"proxysql-reader-hostgroup" toml:"proxysql-reader-hostgroup" json:"proxysqlReaderHostgroup"`
	ProxysqlCopyGrants                        bool                   `mapstructure:"proxysql-bootstrap-users" toml:"proxysql-bootstrap-users" json:"proxysqlBootstrapUsers"`
	ProxysqlBootstrap                         bool                   `mapstructure:"proxysql-bootstrap" toml:"proxysql-bootstrap" json:"proxysqlBootstrap"`
	ProxysqlBootstrapVariables                bool                   `mapstructure:"proxysql-bootstrap-variables" toml:"proxysql-bootstrap-variables" json:"proxysqlBootstrapVariables"`
	ProxysqlBootstrapHG                       bool                   `mapstructure:"proxysql-bootstrap-hostgroups" toml:"proxysql-bootstrap-hostgroups" json:"proxysqlBootstrapHostgroups"`
	ProxysqlBootstrapQueryRules               bool                   `mapstructure:"proxysql-bootstrap-query-rules" toml:"proxysql-bootstrap-query-rules" json:"proxysqlBootstrapQueryRules"`
	ProxysqlMultiplexing                      bool                   `mapstructure:"proxysql-multiplexing" toml:"proxysql-multiplexing" json:"proxysqlMultiplexing"`
	ProxysqlBinaryPath                        string                 `mapstructure:"proxysql-binary-path" toml:"proxysql-binary-path" json:"proxysqlBinaryPath"`
	ProxysqlWriteTrackState                   string                 `mapstructure:"proxysql-write-track-state" toml:"proxysql-write-track-state" json:"proxysqlWriteTrackState"`
	ProxysqlreadTrackState                    string                 `mapstructure:"proxysql-read-track-state" toml:"proxysql-read-track-state" json:"proxysqlReadTrackState"`
	ProxyJanitorDebug                         bool                   `mapstructure:"proxyjanitor-debug" toml:"proxyjanitor-debug" json:"proxyjanitorDebug"`
	ProxyJanitorLogLevel                      int                    `mapstructure:"log-level-proxyjanitor" toml:"log-level-proxyjanitor" json:"proxyjanitorLogLevel"`
	ProxyJanitorHosts                         string                 `mapstructure:"proxyjanitor-servers" toml:"proxyjanitor-servers" json:"proxyjanitorServers"`
	ProxyJanitorHostsIPV6                     string                 `mapstructure:"proxyjanitor-servers-ipv6" toml:"proxyjanitor-servers-ipv6" json:"proxyjanitorServers-ipv6"`
	ProxyJanitorPort                          string                 `mapstructure:"proxyjanitor-port" toml:"proxyjanitor-port" json:"proxyjanitorPort"`
	ProxyJanitorAdminPort                     string                 `mapstructure:"proxyjanitor-admin-port" toml:"proxyjanitor-admin-port" json:"proxyjanitorAdminPort"`
	ProxyJanitorUser                          string                 `mapstructure:"proxyjanitor-user" toml:"proxyjanitor-user" json:"proxyjanitorUser"`
	ProxyJanitorPassword                      string                 `mapstructure:"proxyjanitor-password" toml:"proxyjanitor-password" json:"proxyjanitorPassword"`
	ProxyJanitorBinaryPath                    string                 `mapstructure:"proxyjanitor-binary-path" toml:"proxyjanitor-binary-path" json:"proxyjanitorBinaryPath"`
	MysqlRouterOn                             bool                   `mapstructure:"mysqlrouter" toml:"mysqlrouter" json:"mysqlrouter"`
	MysqlRouterDebug                          bool                   `mapstructure:"mysqlrouter-debug" toml:"mysqlrouter-debug" json:"mysqlrouterDebug"`
	MysqlRouterLogLevel                       int                    `mapstructure:"log-level-mysqlrouter" toml:"log-level-mysqlrouter" json:"mysqlrouterLogLevel"`
	MysqlRouterHosts                          string                 `mapstructure:"mysqlrouter-servers" toml:"mysqlrouter-servers" json:"mysqlrouterServers"`
	MysqlRouterJanitorWeights                 string                 `mapstructure:"mysqlrouter-janitor-weights" toml:"mysqlrouter-janitor-weights" json:"mysqlrouterJanitorWeights"`
	MysqlRouterPort                           string                 `mapstructure:"mysqlrouter-port" toml:"mysqlrouter-port" json:"mysqlrouterPort"`
	MysqlRouterUser                           string                 `mapstructure:"mysqlrouter-user" toml:"mysqlrouter-user" json:"mysqlrouterUser"`
	MysqlRouterPass                           string                 `mapstructure:"mysqlrouter-pass" toml:"mysqlrouter-pass" json:"mysqlrouterPass"`
	MysqlRouterWritePort                      int                    `mapstructure:"mysqlrouter-write-port" toml:"mysqlrouter-write-port" json:"mysqlrouterWritePort"`
	MysqlRouterReadPort                       int                    `mapstructure:"mysqlrouter-read-port" toml:"mysqlrouter-read-port" json:"mysqlrouterReadPort"`
	MysqlRouterReadWritePort                  int                    `mapstructure:"mysqlrouter-read-write-port" toml:"mysqlrouter-read-write-port" json:"mysqlrouterReadWritePort"`
	SphinxOn                                  bool                   `mapstructure:"sphinx" toml:"sphinx" json:"sphinx"`
	SphinxDebug                               bool                   `mapstructure:"sphinx-debug" toml:"sphinx-debug" json:"sphinxDebug"`
	SphinxLogLevel                            int                    `mapstructure:"log-level-sphinx" toml:"log-level-sphinx" json:"sphinxLogLevel"`
	SphinxHosts                               string                 `mapstructure:"sphinx-servers" toml:"sphinx-servers" json:"sphinxServers"`
	SphinxHostsIPV6                           string                 `mapstructure:"sphinx-servers-ipv6" toml:"sphinx-servers-ipv6" json:"sphinxServers-ipv6"`
	SphinxJanitorWeights                      string                 `mapstructure:"sphinx-janitor-weights" toml:"sphinx-janitor-weights" json:"sphinxJanitorWeights"`
	SphinxConfig                              string                 `mapstructure:"sphinx-config" toml:"sphinx-config" json:"sphinxConfig"`
	SphinxQLPort                              string                 `mapstructure:"sphinx-sql-port" toml:"sphinx-sql-port" json:"sphinxSqlPort"`
	SphinxPort                                string                 `mapstructure:"sphinx-port" toml:"sphinx-port" json:"sphinxPort"`
	RegistryConsul                            bool                   `mapstructure:"registry-consul" toml:"registry-consul" json:"registryConsul"`
	RegistryConsulDebug                       bool                   `mapstructure:"registry-consul-debug" toml:"registry-consul-debug" json:"registryConsulDebug"`
	RegistryConsulLogLevel                    int                    `mapstructure:"log-level-registry-consul" toml:"log-level-registry-consul" json:"registryConsulLogLevel"`
	RegistryConsulCredential                  string                 `mapstructure:"registry-consul-credential" toml:"registry-consul-credential" json:"registryConsulCredential"`
	RegistryConsulToken                       string                 `mapstructure:"registry-consul-token" toml:"registry-consul-token" json:"registryConsulToken"`
	RegistryConsulHosts                       string                 `mapstructure:"registry-servers" toml:"registry-servers" json:"registryServers"`
	RegistryConsulJanitorWeights              string                 `mapstructure:"registry-janitor-weights" toml:"registry-janitor-weights" json:"registryJanitorWeights"`
	KeyPath                                   string                 `mapstructure:"keypath" toml:"-" json:"-"`
	Topology                                  string                 `mapstructure:"topology" toml:"-" json:"-"` // use by bootstrap
	TopologyTarget                            string                 `mapstructure:"topology-target" toml:"topology-target" json:"topologyTarget"`
	TopologyStaging                           bool                   `mapstructure:"topology-staging" toml:"topology-staging" json:"topologyStaging"`
	TopologyStagingRefreshScript              string                 `mapstructure:"topology-staging-refresh-script" toml:"topology-staging-refresh-script" json:"topologyStagingRefreshScript"`
	TopologyStagingPostDetachScript           string                 `mapstructure:"topology-staging-post-detach-script" toml:"topology-staging-post-detach-script" json:"topologyStagingPostDetachScript"`
	StagingProxyHosts                         string                 `mapstructure:"staging-proxy-hosts" toml:"staging-proxy-hosts" json:"stagingProxyHosts"`
	StagingServerHost                         string                 `mapstructure:"staging-server-host" toml:"staging-server-host" json:"stagingServerHost"`
	GraphiteMetrics                           bool                   `scope:"server" mapstructure:"graphite-metrics" toml:"graphite-metrics" json:"graphiteMetrics"`
	GraphiteEmbedded                          bool                   `scope:"server" mapstructure:"graphite-embedded" toml:"graphite-embedded" json:"graphiteEmbedded"`
	GraphiteWhitelist                         bool                   `scope:"server" mapstructure:"graphite-whitelist" toml:"graphite-whitelist" json:"graphiteWhitelist"`
	GraphiteBlacklist                         bool                   `scope:"server" mapstructure:"graphite-blacklist" toml:"graphite-blacklist" json:"graphiteBlacklist"`
	GraphiteWhitelistTemplate                 string                 `scope:"server" mapstructure:"graphite-whitelist-template" toml:"graphite-whitelist-template" json:"graphiteWhitelistTemplate"`
	GraphiteCarbonHost                        string                 `scope:"server" mapstructure:"graphite-carbon-host" toml:"graphite-carbon-host" json:"graphiteCarbonHost"`
	GraphiteCarbonPort                        int                    `scope:"server" mapstructure:"graphite-carbon-port" toml:"graphite-carbon-port" json:"graphiteCarbonPort"`
	GraphiteCarbonApiPort                     int                    `scope:"server" mapstructure:"graphite-carbon-api-port" toml:"graphite-carbon-api-port" json:"graphiteCarbonApiPort"`
	GraphiteCarbonServerPort                  int                    `scope:"server" mapstructure:"graphite-carbon-server-port" toml:"graphite-carbon-server-port" json:"graphiteCarbonServerPort"`
	GraphiteCarbonLinkPort                    int                    `scope:"server" mapstructure:"graphite-carbon-link-port" toml:"graphite-carbon-link-port" json:"graphiteCarbonLinkPort"`
	GraphiteCarbonPicklePort                  int                    `scope:"server" mapstructure:"graphite-carbon-pickle-port" toml:"graphite-carbon-pickle-port" json:"graphiteCarbonPicklePort"`
	GraphiteCarbonPprofPort                   int                    `scope:"server" mapstructure:"graphite-carbon-pprof-port" toml:"graphite-carbon-pprof-port" json:"graphiteCarbonPprofPort"`
	SysbenchBinaryPath                        string                 `scope:"server" mapstructure:"sysbench-binary-path" toml:"sysbench-binary-path" json:"sysbenchBinaryPath"`
	SysbenchTest                              string                 `mapstructure:"sysbench-test" toml:"sysbench-test" json:"sysbenchBinaryTest"`
	SysbenchV1                                bool                   `scope:"server" mapstructure:"sysbench-v1" toml:"sysbench-v1" json:"sysbenchV1"`
	SysbenchTime                              int                    `mapstructure:"sysbench-time" toml:"sysbench-time" json:"sysbenchTime"`
	SysbenchThreads                           int                    `mapstructure:"sysbench-threads" toml:"sysbench-threads" json:"sysbenchThreads"`
	SysbenchTables                            int                    `mapstructure:"sysbench-tables" toml:"sysbench-tables" json:"sysbenchTables"`
	SysbenchScale                             int                    `mapstructure:"sysbench-scale" toml:"sysbench-scale" json:"sysbenchScale"`
	Arbitration                               bool                   `scope:"server" mapstructure:"arbitration-external" toml:"arbitration-external" json:"arbitrationExternal"`
	ArbitrationSasSecret                      string                 `scope:"server" mapstructure:"arbitration-external-secret" toml:"arbitration-external-secret" json:"arbitrationExternalSecret"`
	ArbitrationSasHosts                       string                 `scope:"server" mapstructure:"arbitration-external-hosts" toml:"arbitration-external-hosts" json:"arbitrationExternalHosts"`
	ArbitrationSasUniqueId                    int                    `scope:"server" mapstructure:"arbitration-external-unique-id" toml:"arbitration-external-unique-id" json:"arbitrationExternalUniqueId"`
	ArbitrationPeerHosts                      string                 `scope:"server" mapstructure:"arbitration-peer-hosts" toml:"arbitration-peer-hosts" json:"arbitrationPeerHosts"`
	ArbitrationFailedMasterScript             string                 `scope:"server" mapstructure:"arbitration-failed-master-script" toml:"arbitration-failed-master-script" json:"arbitrationFailedMasterScript"`
	ArbitratorAddress                         string                 `mapstructure:"arbitrator-bind-address" toml:"arbitrator-bind-address" json:"arbitratorBindAddress"`
	ArbitratorDriver                          string                 `mapstructure:"arbitrator-driver" toml:"arbitrator-driver" json:"arbitratorDriver"`
	ArbitrationReadTimout                     int                    `scope:"server" mapstructure:"arbitration-read-timeout" toml:"arbitration-read-timeout" json:"arbitrationReadTimout"`
	SwitchoverCopyOldLeaderGtid               bool                   `toml:"-" json:"-"` //suspicious code
	Test                                      bool                   `mapstructure:"test" toml:"test" json:"test"`
	TestInjectTraffic                         bool                   `mapstructure:"test-inject-traffic" toml:"test-inject-traffic" json:"testInjectTraffic"`
	TestInjectTrafficStaging                  bool                   `mapstructure:"test-inject-traffic-staging" toml:"test-inject-traffic-staging" json:"testInjectTrafficStaging"`
	Enterprise                                bool                   `toml:"enterprise" json:"enterprise"` //used to talk to opensvc collector
	KubeConfig                                string                 `mapstructure:"kube-config" toml:"kube-config" json:"kubeConfig"`
	SlapOSConfig                              string                 `mapstructure:"slapos-config" toml:"slapos-config" json:"slaposConfig"`
	SlapOSDBPartitions                        string                 `mapstructure:"slapos-db-partitions" toml:"slapos-db-partitions" json:"slaposDbPartitions"`
	SlapOSProxySQLPartitions                  string                 `mapstructure:"slapos-proxysql-partitions" toml:"slapos-proxysql-partitions" json:"slaposProxysqlPartitions"`
	SlapOSHaProxyPartitions                   string                 `mapstructure:"slapos-haproxy-partitions" toml:"slapos-haproxy-partitions" json:"slaposHaproxyPartitions"`
	SlapOSMaxscalePartitions                  string                 `mapstructure:"slapos-maxscale-partitions" toml:"slapos-maxscale-partitions" json:"slaposMaxscalePartitions"`
	SlapOSShardProxyPartitions                string                 `mapstructure:"slapos-shardproxy-partitions" toml:"slapos-shardproxy-partitions" json:"slaposShardproxyPartitions"`
	SlapOSSphinxPartitions                    string                 `mapstructure:"slapos-sphinx-partitions" toml:"slapos-sphinx-partitions" json:"slaposSphinxPartitions"`
	SlapOSAppPartitions                       string                 `mapstructure:"slapos-app-partitions" toml:"slapos-app-partitions" json:"slaposAppPartitions"`
	ProvHost                                  string                 `mapstructure:"opensvc-host" toml:"opensvc-host" json:"opensvcHost"`
	OnPremiseSSH                              bool                   `mapstructure:"onpremise-ssh" toml:"onpremise-ssh" json:"onpremiseSsh"`
	OnPremiseSSHPort                          int                    `mapstructure:"onpremise-ssh-port" toml:"onpremise-ssh-port" json:"onpremiseSshPort"`
	OnPremiseSSHCredential                    string                 `mapstructure:"onpremise-ssh-credential" toml:"onpremise-ssh-credential" json:"onpremiseSshCredential"`
	OnPremiseSSHPrivateKey                    string                 `mapstructure:"onpremise-ssh-private-key" toml:"onpremise-ssh-private-key" json:"onpremiseSshPrivateKey"`
	OnPremiseSSHStartDbScript                 string                 `mapstructure:"onpremise-ssh-start-db-script" toml:"onpremise-ssh-start-db-script" json:"onpremiseSshStartDbScript"`
	OnPremiseSSHStartProxyScript              string                 `mapstructure:"onpremise-ssh-start-proxy-script" toml:"onpremise-ssh-start-proxy-script" json:"onpremiseSshStartProxyScript"`
	OnPremiseSSHStopProxyScript               string                 `mapstructure:"onpremise-ssh-stop-proxy-script" toml:"onpremise-ssh-stop-proxy-script" json:"onpremiseSshStopProxyScript"`
	OnPremiseSSHDbJobScript                   string                 `mapstructure:"onpremise-ssh-db-job-script" toml:"onpremise-ssh-db-job-script" json:"onpremiseSshDbJobScript"`
	ProvOpensvcP12Certificate                 string                 `mapstructure:"opensvc-p12-certificate" toml:"opensvc-p12-certificate" json:"opensvcP12Certificate"`
	ProvOpensvcP12Secret                      string                 `mapstructure:"opensvc-p12-secret" toml:"opensvc-p12-secret" json:"opensvcP12Secret"`
	ProvOpensvcUseCollectorAPI                bool                   `mapstructure:"opensvc-use-collector-api" toml:"opensvc-use-collector-api" json:"opensvcUseCollectorApi"`
	ProvOpensvcCollectorAccount               string                 `mapstructure:"opensvc-collector-account" toml:"opensvc-collector-account" json:"opensvcCollectorAccount"`
	ProvUser                                  string                 `mapstructure:"opensvc-user" toml:"opensvc-user" json:"opensvcUser"`
	ProvCodeApp                               string                 `mapstructure:"opensvc-codeapp" toml:"opensvc-codeapp" json:"opensvcCodeapp"`
	ProvSerialized                            bool                   `mapstructure:"prov-serialized" toml:"prov-serialized" json:"provSerialized"`
	ProvObjectAllowOverwrite                  bool                   `mapstructure:"prov-object-allow-overwrite" toml:"prov-object-allow-overwrite" json:"provObjectAllowOverwrite"`
	ProvOrchestrator                          string                 `mapstructure:"prov-orchestrator" toml:"prov-orchestrator" json:"provOrchestrator"`
	ProvOrchestratorEnable                    string                 `mapstructure:"prov-orchestrator-enable" toml:"prov-orchestrator-enable" json:"provOrchestratorEnable"`
	ProvOrchestratorCluster                   string                 `mapstructure:"prov-orchestrator-cluster" toml:"prov-orchestrator-cluster" json:"provOrchestratorCluster"`
	ProvDBApplyDynamicConfig                  bool                   `mapstructure:"prov-db-apply-dynamic-config" toml:"prov-db-apply-dynamic-config" json:"provDBApplyDynamicConfig"`
	ProvDBForceWriteConfig                    bool                   `mapstructure:"prov-db-force-write-config" toml:"prov-db-force-write-config" json:"provDBForceWriteConfig"`
	ProvDBConfigPreserve                      bool                   `mapstructure:"prov-db-config-preserve" toml:"prov-db-config-preserve" json:"provDbConfigPreserve"`
	ProvDBConfigPreserveVars                  string                 `mapstructure:"prov-db-config-preserve-vars" toml:"prov-db-config-preserve-vars" json:"provDbConfigPreserveVars"`
	ProvDBClientBasedir                       string                 `mapstructure:"prov-db-client-basedir" toml:"prov-db-client-basedir" json:"provDbClientBasedir"`
	ProvDBBinaryBasedir                       string                 `mapstructure:"prov-db-binary-basedir" toml:"prov-db-binary-basedir" json:"provDbBinaryBasedir"`
	ProvDBBinaryLogName                       string                 `mapstructure:"prov-db-binary-log-name" toml:"prov-db-binary-log-name" json:"provDbBinaryLogName"`
	ProvType                                  string                 `mapstructure:"prov-db-service-type" toml:"prov-db-service-type" json:"provDbServiceType"`
	ProvAgents                                string                 `mapstructure:"prov-db-agents" toml:"prov-db-agents" json:"provDbAgents"`
	ProvMem                                   string                 `measurement:"M,bytes,required" mapstructure:"prov-db-memory" toml:"prov-db-memory" json:"provDbMemory"`
	ProvMemSharedPct                          string                 `mapstructure:"prov-db-memory-shared-pct" toml:"prov-db-memory-shared-pct" json:"provDbMemorySharedPct"`
	ProvMemThreadedPct                        string                 `mapstructure:"prov-db-memory-threaded-pct" toml:"prov-db-memory-threaded-pct" json:"provDbMemoryThreadedPct"`
	ProvIops                                  string                 `mapstructure:"prov-db-disk-iops" toml:"prov-db-disk-iops" json:"provDbDiskIops"`
	ProvIopsLatency                           string                 `mapstructure:"prov-db-disk-iops-latency" toml:"prov-db-disk-iops-latency" json:"provDbDiskIopsLatency"`
	ProvExpireLogDays                         int                    `mapstructure:"prov-db-expire-log-days" toml:"prov-db-expire-log-days" json:"provDbExpireLogDays"`
	ProvMaxConnections                        int                    `mapstructure:"prov-db-max-connections" toml:"prov-db-max-connections" json:"provDbMaxConnections"`
	ProvCores                                 string                 `mapstructure:"prov-db-cpu-cores" toml:"prov-db-cpu-cores" json:"provDbCpuCores"`
	ProvTags                                  string                 `mapstructure:"prov-db-tags" toml:"prov-db-tags" json:"provDbTags"`
	ProvBinaryInTarball                       bool                   `mapstructure:"prov-db-binary-in-tarball" toml:"prov-db-binary-in-tarball" json:"provDbBinaryInTarball"`
	ProvBinaryTarballName                     string                 `mapstructure:"prov-db-binary-tarball-name" toml:"prov-db-binary-tarball-name" json:"provDbBinaryTarballName"`
	ProvDomain                                string                 `mapstructure:"prov-db-domain" toml:"prov-db-domain" json:"provDbDomain"`
	ProvDisk                                  string                 `measurement:"G,bytes,required" mapstructure:"prov-db-disk-size" toml:"prov-db-disk-size" json:"provDbDiskSize"`
	ProvDiskSystemSize                        string                 `measurement:"G,bytes,required" mapstructure:"prov-db-disk-system-size" toml:"prov-db-disk-system-size" json:"provDbDiskSystemSize"`
	ProvDiskTempSize                          string                 `measurement:"M,bytes,required" mapstructure:"prov-db-disk-temp-size" toml:"prov-db-disk-temp-size" json:"provDbDiskTempSize"`
	ProvDiskDockerSize                        string                 `measurement:"G,bytes,required" mapstructure:"prov-db-disk-docker-size" toml:"prov-db-disk-docker-size" json:"provDbDiskDockerSize"`
	ProvVolumeDocker                          string                 `mapstructure:"prov-db-volume-docker" toml:"prov-db-volume-docker" json:"provDbVolumeDocker"`
	ProvVolumeData                            string                 `mapstructure:"prov-db-volume-data" toml:"prov-db-volume-data" json:"provDbVolumeData"`
	ProvDiskFS                                string                 `mapstructure:"prov-db-disk-fs" toml:"prov-db-disk-fs" json:"provDbDiskFs"`
	ProvDiskFSCompress                        string                 `mapstructure:"prov-db-disk-fs-compress" toml:"prov-db-disk-fs-compress" json:"provDbDiskFsCompress"`
	ProvDiskPool                              string                 `mapstructure:"prov-db-disk-pool" toml:"prov-db-disk-pool" json:"provDbDiskPool"`
	ProvDiskDevice                            string                 `mapstructure:"prov-db-disk-device" toml:"prov-db-disk-device" json:"provDbDiskDevice"`
	ProvDiskType                              string                 `mapstructure:"prov-db-disk-type" toml:"prov-db-disk-type" json:"provDbDiskType"`
	ProvDiskSnapshot                          bool                   `mapstructure:"prov-db-disk-snapshot-prefered-master" toml:"prov-db-disk-snapshot-prefered-master" json:"provDbDiskSnapshotPreferedMaster"`
	ProvDiskSnapshotKeep                      int                    `mapstructure:"prov-db-disk-snapshot-keep" toml:"prov-db-disk-snapshot-keep" json:"provDbDiskSnapshotKeep"`
	ProvNetIface                              string                 `mapstructure:"prov-db-net-iface" toml:"prov-db-net-iface" json:"provDbNetIface"`
	ProvNetmask                               string                 `mapstructure:"prov-db-net-mask" toml:"prov-db-net-mask" json:"provDbNetMask"`
	ProvGateway                               string                 `mapstructure:"prov-db-net-gateway" toml:"prov-db-net-gateway" json:"provDbNetGateway"`
	ProvDbImg                                 string                 `mapstructure:"prov-db-docker-img" toml:"prov-db-docker-img" json:"provDbDockerImg"`
	ProvDBDockerTmpfsSize                     string                 `measurement:"M,bytes" mapstructure:"prov-db-docker-tmpfs-size" toml:"prov-db-docker-tmpfs-size" json:"provDbDockerTmpfsSize"`
	ProvDBDockerRunArgs                       string                 `mapstructure:"prov-db-docker-run-args" toml:"prov-db-docker-run-args" json:"provDbDockerRunArgs"`
	ProvDBDockerRunArgsLimit                  bool                   `mapstructure:"prov-db-docker-run-args-limit" toml:"prov-db-docker-run-args-limit" json:"provDbDockerRunArgsLimit"`
	ProvDBJobsDockerRunArgs                   string                 `mapstructure:"prov-db-jobs-docker-run-args" toml:"prov-db-jobs-docker-run-args" json:"provDbJobsDockerRunArgs"`
	ProvDatadirVersion                        string                 `mapstructure:"prov-db-datadir-version" toml:"prov-db-datadir-version" json:"provDbDatadirVersion"`
	ProvDBLoadSQL                             string                 `mapstructure:"prov-db-load-sql" toml:"prov-db-load-sql" json:"provDbLoadSql"`
	ProvDBLoadCSV                             string                 `mapstructure:"prov-db-load-csv" toml:"prov-db-load-csv" json:"provDbLoadCsv"`
	ProvProxType                              string                 `mapstructure:"prov-proxy-service-type" toml:"prov-proxy-service-type" json:"provProxyServiceType"`
	ProvProxAgents                            string                 `mapstructure:"prov-proxy-agents" toml:"prov-proxy-agents" json:"provProxyAgents"`
	ProvProxAgentsFailover                    string                 `mapstructure:"prov-proxy-agents-failover" toml:"prov-proxy-agents-failover" json:"provProxyAgentsFailover"`
	ProvProxMem                               string                 `measurement:"M,bytes,required" mapstructure:"prov-proxy-memory" toml:"prov-proxy-memory" json:"provProxyMemory"`
	ProvProxCores                             string                 `mapstructure:"prov-proxy-cpu-cores" toml:"prov-proxy-cpu-cores" json:"provProxyCpuCores"`
	ProvProxDisk                              string                 `measurement:"G,bytes,required" mapstructure:"prov-proxy-disk-size" toml:"prov-proxy-disk-size" json:"provProxyDiskSize"`
	ProvProxDiskFS                            string                 `mapstructure:"prov-proxy-disk-fs" toml:"prov-proxy-disk-fs" json:"provProxyDiskFs"`
	ProvProxDiskPool                          string                 `mapstructure:"prov-proxy-disk-pool" toml:"prov-proxy-disk-pool" json:"provProxyDiskPool"`
	ProvProxDiskDevice                        string                 `mapstructure:"prov-proxy-disk-device" toml:"prov-proxy-disk-device" json:"provProxyDiskDevice"`
	ProvProxDiskType                          string                 `mapstructure:"prov-proxy-disk-type" toml:"prov-proxy-disk-type" json:"provProxyDiskType"`
	ProvProxVolumeData                        string                 `mapstructure:"prov-proxy-volume-data" toml:"prov-proxy-volume-data" json:"provProxyVolumeData"`
	ProvProxNetIface                          string                 `mapstructure:"prov-proxy-net-iface" toml:"prov-proxy-net-iface" json:"provProxyNetIface"`
	ProvProxNetmask                           string                 `mapstructure:"prov-proxy-net-mask" toml:"prov-proxy-net-mask" json:"provProxyNetMask"`
	ProvProxGateway                           string                 `mapstructure:"prov-proxy-net-gateway" toml:"prov-proxy-net-gateway" json:"provProxyNetGateway"`
	ProvProxRouteAddr                         string                 `mapstructure:"prov-proxy-route-addr" toml:"prov-proxy-route-addr" json:"provProxyRouteAddr"`
	ProvProxRoutePort                         string                 `mapstructure:"prov-proxy-route-port" toml:"prov-proxy-route-port" json:"provProxyRoutePort"`
	ProvProxRouteMask                         string                 `mapstructure:"prov-proxy-route-mask" toml:"prov-proxy-route-mask" json:"provProxyRouteMask"`
	ProvProxRoutePolicy                       string                 `mapstructure:"prov-proxy-route-policy" toml:"prov-proxy-route-policy" json:"provProxyRoutePolicy"`
	ProvProxShardingImg                       string                 `mapstructure:"prov-proxy-docker-shardproxy-img" toml:"prov-proxy-docker-shardproxy-img" json:"provProxyDockerShardproxyImg"`
	ProvProxMaxscaleImg                       string                 `mapstructure:"prov-proxy-docker-maxscale-img" toml:"prov-proxy-docker-maxscale-img" json:"provProxyDockerMaxscaleImg"`
	ProvProxHaproxyImg                        string                 `mapstructure:"prov-proxy-docker-haproxy-img" toml:"prov-proxy-docker-haproxy-img" json:"provProxyDockerHaproxyImg"`
	ProvProxProxysqlImg                       string                 `mapstructure:"prov-proxy-docker-proxysql-img" toml:"prov-proxy-docker-proxysql-img" json:"provProxyDockerProxysqlImg"`
	ProvProxMysqlRouterImg                    string                 `mapstructure:"prov-proxy-docker-mysqlrouter-img" toml:"prov-proxy-docker-mysqlrouter-img" json:"provProxyDockerMysqlrouterImg"`
	ProvProxDockerRunArgs                     string                 `mapstructure:"prov-proxy-docker-run-args" toml:"prov-proxy-docker-run-args" json:"provProxyDockerRunArgs"`
	ProvProxTags                              string                 `mapstructure:"prov-proxy-tags" toml:"prov-proxy-tags" json:"provProxyTags"`
	ProvSphinxAgents                          string                 `mapstructure:"prov-sphinx-agents" toml:"prov-sphinx-agents" json:"provSphinxAgents"`
	ProvSphinxImg                             string                 `mapstructure:"prov-sphinx-docker-img" toml:"prov-sphinx-docker-img" json:"provSphinxDockerImg"`
	ProvSphinxMem                             string                 `measurement:"M,bytes,required" mapstructure:"prov-sphinx-memory" toml:"prov-sphinx-memory" json:"provSphinxMemory"`
	ProvSphinxDisk                            string                 `measurement:"G,bytes,required" mapstructure:"prov-sphinx-disk-size" toml:"prov-sphinx-disk-size" json:"provSphinxDiskSize"`
	ProvSphinxCores                           string                 `mapstructure:"prov-sphinx-cpu-cores" toml:"prov-sphinx-cpu-cores" json:"provSphinxCpuCores"`
	ProvSphinxMaxChildren                     string                 `mapstructure:"prov-sphinx-max-childrens" toml:"prov-sphinx-max-childrens" json:"provSphinxMaxChildrens"`
	ProvSphinxDiskPool                        string                 `mapstructure:"prov-sphinx-disk-pool" toml:"prov-sphinx-disk-pool" json:"provSphinxDiskPool"`
	ProvSphinxDiskFS                          string                 `mapstructure:"prov-sphinx-disk-fs" toml:"prov-sphinx-disk-fs" json:"provSphinxDiskFs"`
	ProvSphinxDiskDevice                      string                 `mapstructure:"prov-sphinx-disk-device" toml:"prov-sphinx-disk-device" json:"provSphinxDiskDevice"`
	ProvSphinxDiskType                        string                 `mapstructure:"prov-sphinx-disk-type" toml:"prov-sphinx-disk-type" json:"provSphinxDiskType"`
	ProvSphinxTags                            string                 `mapstructure:"prov-sphinx-tags" toml:"prov-sphinx-tags" json:"provSphinxTags"`
	ProvSphinxCron                            string                 `mapstructure:"prov-sphinx-reindex-schedule" toml:"prov-sphinx-reindex-schedule" json:"provSphinxReindexSchedule"`
	ProvSphinxType                            string                 `mapstructure:"prov-sphinx-service-type" toml:"prov-sphinx-service-type" json:"provSphinxServiceType"`
	ProvSSLCa                                 string                 `mapstructure:"prov-tls-server-ca" toml:"prov-tls-server-ca" json:"provTlsServerCa"`
	ProvSSLCert                               string                 `mapstructure:"prov-tls-server-cert" toml:"prov-tls-server-cert" json:"provTlsServerCert"`
	ProvSSLKey                                string                 `mapstructure:"prov-tls-server-key" toml:"prov-tls-server-key" json:"provTlsServerKey"`
	ProvSSLCaUUID                             string                 `mapstructure:"prov-tls-server-ca-uuid" toml:"-" json:"-"`
	ProvSSLCertUUID                           string                 `mapstructure:"prov-tls-server-cert-uuid" toml:"-" json:"-"`
	ProvSSLKeyUUID                            string                 `mapstructure:"prov-tls-server-key-uuid" toml:"-" json:"-"`
	ProvNetCNI                                bool                   `mapstructure:"prov-net-cni" toml:"prov-net-cni" json:"provNetCni"`
	ProvNetCNICluster                         string                 `mapstructure:"prov-net-cni-cluster" toml:"prov-net-cni-cluster" json:"provNetCniCluster"`
	ProvNetDockerRunArgs                      string                 `mapstructure:"prov-net-docker-run-args" toml:"prov-net-docker-run-args" json:"provNetDockerRunArgs"`
	ProvDockerDaemonPrivate                   bool                   `mapstructure:"prov-docker-daemon-private" toml:"prov-docker-daemon-private" json:"provDockerDaemonPrivate"`
	ProvServicePlan                           string                 `mapstructure:"prov-service-plan" toml:"prov-service-plan" json:"provServicePlan"`
	ProvServicePlanRegistry                   string                 `scope:"server" mapstructure:"prov-service-plan-registry" toml:"prov-service-plan-registry" json:"provServicePlanRegistry"`
	ProvDbBootstrapScript                     string                 `mapstructure:"prov-db-bootstrap-script" toml:"prov-db-bootstrap-script" json:"provDbBootstrapScript"`
	ProvProxyBootstrapScript                  string                 `mapstructure:"prov-proxy-bootstrap-script" toml:"prov-proxy-bootstrap-script" json:"provProxyBootstrapScript"`
	ProvDbCleanupScript                       string                 `mapstructure:"prov-db-cleanup-script" toml:"prov-db-cleanup-script" json:"provDbCleanupScript"`
	ProvProxyCleanupScript                    string                 `mapstructure:"prov-proxy-cleanup-script" toml:"prov-proxy-cleanup-script" json:"provProxyCleanupScript"`
	ProvDbStartScript                         string                 `mapstructure:"prov-db-start-script" toml:"prov-db-start-script" json:"provDbStartScript"`
	ProvDbStartFetchConfig                    bool                   `mapstructure:"prov-db-start-fetch-config" toml:"prov-db-start-fetch-config" json:"provDbStartFetchConfig"`
	ProvProxyStartScript                      string                 `mapstructure:"prov-proxy-start-script" toml:"prov-proxy-start-script" json:"provProxyStartScript"`
	ProvProxyStartFetchConfig                 bool                   `mapstructure:"prov-proxy-start-fetch-config" toml:"prov-proxy-start-fetch-config" json:"provProxyStartFetchConfig"`
	ProvDbStopScript                          string                 `mapstructure:"prov-db-stop-script" toml:"prov-db-stop-script" json:"provDbStopScript"`
	ProvProxyStopScript                       string                 `mapstructure:"prov-proxy-stop-script" toml:"prov-proxy-stop-script" json:"provProxyStopScript"`
	ProvDBCompliance                          string                 `mapstructure:"prov-db-compliance" toml:"prov-db-compliance" json:"provDBCompliance"`
	ProvProxyCompliance                       string                 `mapstructure:"prov-proxy-compliance" toml:"prov-proxy-compliance" json:"provProxyCompliance"`
	ProvDockerRegistryCredentials             string                 `mapstructure:"prov-docker-registry-credentials" toml:"prov-docker-registry-credentials" json:"provDockerRegistryCredentials"`
	AppOn                                     bool                   `mapstructure:"app" toml:"app" json:"app"`
	AppHosts                                  string                 `mapstructure:"app-hosts" toml:"app-hosts" json:"appHosts"`
	AppHostsIPV6                              string                 `mapstructure:"app-hosts-ipv6" toml:"app-hosts-ipv6" json:"appHostsIpv6"`
	ProvAppMem                                string                 `measurement:"M,bytes,required" mapstructure:"prov-app-memory" toml:"prov-app-memory" json:"provAppMemory" groups:"apps"`
	ProvAppDisk                               string                 `measurement:"G,bytes,required" mapstructure:"prov-app-disk-size" toml:"prov-app-disk-size" json:"provAppDiskSize" groups:"apps"`
	ProvAppVolumePools                        string                 `mapstructure:"prov-app-volume-pools" toml:"prov-app-volume-pools" json:"provAppVolumePools" groups:"apps"`
	ProvAppCpuCores                           string                 `mapstructure:"prov-app-cpu-cores" toml:"prov-app-cpu-cores" json:"provAppCpuCores" groups:"apps"`
	ProvAppAgents                             string                 `mapstructure:"prov-app-agents" toml:"prov-app-agents" json:"provAppAgents" groups:"apps"`
	ProvAppHATopology                         string                 `mapstructure:"prov-app-ha-topology" toml:"prov-app-ha-topology" json:"provAppHaTopology" groups:"apps"`
	ProvAppTemplateRepo                       string                 `mapstructure:"prov-app-template-repo" toml:"prov-app-template-repo" json:"provAppTemplateRepo" groups:"apps"`
	ProvAppTemplateRepoBranch                 string                 `mapstructure:"prov-app-template-repo-branch" toml:"prov-app-template-repo-branch" json:"provAppTemplateRepoBranch" groups:"apps"`
	ProvAppTemplateRepoUser                   string                 `mapstructure:"prov-app-template-repo-user" toml:"prov-app-template-repo-user" json:"provAppTemplateRepoUser" groups:"apps"`
	ProvAppTemplateRepoPassword               string                 `mapstructure:"prov-app-template-repo-password" toml:"prov-app-template-repo-password" json:"provAppTemplateRepoPassword" groups:"apps"`
	ProvAppTemplateRepoTimeout                int                    `mapstructure:"prov-app-template-repo-timeout" toml:"prov-app-template-repo-timeout" json:"provAppTemplateRepoTimeout" groups:"apps"`
	TemplateVariableMaxDepth                  int                    `mapstructure:"template-var-max-depth" toml:"template-var-max-depth" json:"templateVarMaxDepth"`
	TemplateStrict                            bool                   `mapstructure:"template-strict" toml:"template-strict" json:"templateStrict"`
	APIUsers                                  string                 `mapstructure:"api-credentials" toml:"api-credentials" json:"apiCredentials"`
	APIUsersExternal                          string                 `mapstructure:"api-credentials-external" toml:"api-credentials-external" json:"apiCredentialsExternal"`
	APIUsersACLAllow                          string                 `mapstructure:"api-credentials-acl-allow" toml:"api-credentials-acl-allow" json:"apiCredentialsACLAllow"`
	APIUsersACLAllowExternal                  string                 `mapstructure:"api-credentials-acl-allow-external" toml:"api-credentials-acl-allow-external" json:"apiCredentialsACLAllowExternal"`
	APIUsersACLDiscard                        string                 `mapstructure:"api-credentials-acl-discard" toml:"api-credentials-acl-discard" json:"apiCredentialsACLDiscard"`
	APIUsersACLDiscardExternal                string                 `mapstructure:"api-credentials-acl-discard-external" toml:"api-credentials-acl-discard-external" json:"apiCredentialsACLDiscardExternal"`
	APISecureConfig                           bool                   `mapstructure:"api-credentials-secure-config" toml:"api-credentials-secure-config" json:"apiCredentialsSecureConfig"`
	APIPort                                   string                 `scope:"server" mapstructure:"api-port" toml:"api-port" json:"apiPort"`
	APIBind                                   string                 `scope:"server" mapstructure:"api-bind" toml:"api-bind" json:"apiBind"`
	APIPublicURL                              string                 `scope:"server" mapstructure:"api-public-url" toml:"api-public-url" json:"apiPublicUrl"`
	APIHttpsBind                              bool                   `scope:"server" mapstructure:"api-https-bind" toml:"api-secure" json:"apiHttpsBind"`
	APIErrorSuppress                          bool                   `scope:"server" mapstructure:"api-error-suppress" toml:"api-error-suppress" json:"apiErrorSuppress"`
	APIErrorLimit                             int                    `scope:"server" mapstructure:"api-error-limit" toml:"api-error-limit" json:"apiErrorLimit"`
	APIErrorLimitDuration                     int                    `scope:"server" mapstructure:"api-error-limit-duration" toml:"api-error-limit-duration" json:"apiErrorLimitDuration"`
	APIErrorDisregardPort                     bool                   `scope:"server" mapstructure:"api-error-disregard-port" toml:"api-error-disregard-port" json:"apiErrorDisregardPort"`
	AlertScript                               string                 `mapstructure:"alert-script" toml:"alert-script" json:"alertScript"`
	ConfigFile                                string                 `mapstructure:"config" toml:"-" json:"-"`
	MonitorScheduler                          bool                   `mapstructure:"monitoring-scheduler" toml:"monitoring-scheduler" json:"monitoringScheduler"`
	SchedulerReceiverPorts                    string                 `mapstructure:"scheduler-db-servers-receiver-ports" toml:"scheduler-db-servers-receiver-ports" json:"schedulerDbServersReceiverPorts"`
	SchedulerSenderPorts                      string                 `mapstructure:"scheduler-db-servers-sender-ports" toml:"scheduler-db-servers-sender-ports" json:"schedulerDbServersSenderPorts"`
	SchedulerReceiverUseSSL                   bool                   `mapstructure:"scheduler-db-servers-receiver-use-ssl" toml:"scheduler-db-servers-receiver-use-ssl" json:"schedulerDbServersReceiverUseSSL"`
	SchedulerBackupLogical                    bool                   `mapstructure:"scheduler-db-servers-logical-backup" toml:"scheduler-db-servers-logical-backup" json:"schedulerDbServersLogicalBackup"`
	SchedulerBackupPhysical                   bool                   `mapstructure:"scheduler-db-servers-physical-backup" toml:"scheduler-db-servers-physical-backup" json:"schedulerDbServersPhysicalBackup"`
	SchedulerDatabaseLogs                     bool                   `mapstructure:"scheduler-db-servers-logs" toml:"scheduler-db-servers-logs" json:"schedulerDbServersLogs"`
	SchedulerDatabaseOptimize                 bool                   `mapstructure:"scheduler-db-servers-optimize" toml:"scheduler-db-servers-optimize" json:"schedulerDbServersOptimize"`
	SchedulerDatabaseAnalyze                  bool                   `mapstructure:"scheduler-db-servers-analyze" toml:"scheduler-db-servers-analyze" json:"schedulerDbServersAnalyze"`
	SchedulerAlertDisable                     bool                   `mapstructure:"scheduler-alert-disable" toml:"scheduler-alert-disable" json:"schedulerAlertDisable"`
	SchedulerAlertDisableCron                 string                 `mapstructure:"scheduler-alert-disable-cron" toml:"scheduler-alert-disable-cron" json:"schedulerAlertDisableCron"`
	SchedulerAlertDisableTime                 int                    `mapstructure:"scheduler-alert-disable-time" toml:"scheduler-alert-disable-time" json:"schedulerAlertDisableTime"`
	OptimizeUseSQL                            bool                   `mapstructure:"optimize-use-sql" toml:"optimize-use-sql" json:"optimizeUseSql"`
	AnalyzeUseSQL                             bool                   `mapstructure:"analyze-use-sql" toml:"analyze-use-sql" json:"analyzeUseSql"`
	AnalyzeUsePersistent                      bool                   `mapstructure:"analyze-use-persistent" toml:"analyze-use-persistent" json:"analyzeUsePersistent"`
	BackupLogicalCron                         string                 `mapstructure:"scheduler-db-servers-logical-backup-cron" toml:"scheduler-db-servers-logical-backup-cron" json:"schedulerDbServersLogicalBackupCron"`
	BackupPhysicalCron                        string                 `mapstructure:"scheduler-db-servers-physical-backup-cron" toml:"scheduler-db-servers-physical-backup-cron" json:"schedulerDbServersPhysicalBackupCron"`
	BackupDatabaseLogCron                     string                 `mapstructure:"scheduler-db-servers-logs-cron" toml:"scheduler-db-servers-logs-cron" json:"schedulerDbServersLogsCron"`
	BackupDatabaseOptimizeCron                string                 `mapstructure:"scheduler-db-servers-optimize-cron" toml:"scheduler-db-servers-optimize-cron" json:"schedulerDbServersOptimizeCron"`
	BackupDatabaseAnalyzeCron                 string                 `mapstructure:"scheduler-db-servers-analyze-cron" toml:"scheduler-db-servers-analyze-cron" json:"schedulerDbServersAnalyzeCron"`
	BackupSaveScript                          string                 `mapstructure:"backup-save-script" toml:"backup-save-script" json:"backupSaveScript"`
	BackupLoadScript                          string                 `mapstructure:"backup-load-script" toml:"backup-load-script" json:"backupLoadScript"`
	BackupLogicalPostScript                   string                 `mapstructure:"backup-logical-post-script" toml:"backup-logical-post-script" json:"backupLogicalPostScript"`
	BackupPhysicalPostScript                  string                 `mapstructure:"backup-physical-post-script" toml:"backup-physical-post-script" json:"backupPhysicalPostScript"`
	CompressBackups                           bool                   `mapstructure:"compress-backups" toml:"compress-backups" json:"compressBackups"`
	CompressBackupsCompressionLevel           int                    `mapstructure:"compress-backups-compression-level" toml:"compress-backups-compression-level" json:"compressBackupsCompressionLevel"`
	CompressBackupsParallelBlocks             int                    `mapstructure:"compress-backups-parallel-blocks" toml:"compress-backups-parallel-blocks" json:"compressBackupsParallelBlocks"`
	BackupSplitMysqlUser                      bool                   `mapstructure:"backup-split-mysql-user" toml:"backup-split-mysql-user" json:"backupSplitMysqlUser"`
	BackupRestoreMysqlUser                    bool                   `mapstructure:"backup-restore-mysql-user" toml:"backup-restore-mysql-user" json:"backupRestoreMysqlUser"`
	BackupCheckFreeSpace                      bool                   `mapstructure:"backup-check-free-space" toml:"backup-check-free-space" json:"backupCheckFreeSpace"`
	BackupDiskTresholdWarn                    int                    `mapstructure:"backup-disk-treshold-warn" toml:"backup-disk-treshold-warn" json:"backupDiskTresholdWarn"`
	BackupDiskTresholdCrit                    int                    `mapstructure:"backup-disk-treshold-crit" toml:"backup-disk-treshold-crit" json:"backupDiskTresholdCrit"`
	BackupEstimateSize                        bool                   `mapstructure:"backup-estimate-size" toml:"backup-estimate-size" json:"backupEstimateSize"`
	BackupEstimateSizePercentage              int                    `mapstructure:"backup-estimate-size-percentage" toml:"backup-estimate-size-percentage" json:"backupEstimateSizePercentage"`
	BackupGrowthPercentage                    int                    `mapstructure:"backup-growth-percentage" toml:"backup-growth-percentage" json:"backupGrowthPercentage"`
	SchedulerDatabaseLogsTableRotate          bool                   `mapstructure:"scheduler-db-servers-logs-table-rotate" toml:"scheduler-db-servers-logs-table-rotate" json:"schedulerDbServersLogsTableRotate"`
	SchedulerDatabaseLogsTableRotateCron      string                 `mapstructure:"scheduler-db-servers-logs-table-rotate-cron" toml:"scheduler-db-servers-logs-table-rotate-cron" json:"schedulerDbServersLogsTableRotateCron"`
	SchedulerMaintenanceDatabaseLogsTableKeep int                    `mapstructure:"scheduler-db-servers-logs-table-keep" toml:"scheduler-db-servers-logs-table-keep" json:"schedulerDatabaseLogsTableKeep"`
	SchedulerSLARotateCron                    string                 `mapstructure:"scheduler-sla-rotate-cron" toml:"scheduler-sla-rotate-cron" json:"schedulerSlaRotateCron"`
	SchedulerRollingRestart                   bool                   `mapstructure:"scheduler-rolling-restart" toml:"scheduler-rolling-restart" json:"schedulerRollingRestart"`
	SchedulerRollingRestartCron               string                 `mapstructure:"scheduler-rolling-restart-cron" toml:"scheduler-rolling-restart-cron" json:"schedulerRollingRestartCron"`
	SchedulerRollingReprov                    bool                   `mapstructure:"scheduler-rolling-reprov" toml:"scheduler-rolling-reprov" json:"schedulerRollingReprov"`
	SchedulerRollingReprovCron                string                 `mapstructure:"scheduler-rolling-reprov-cron" toml:"scheduler-rolling-reprov-cron" json:"schedulerRollingReprovCron"`
	SchedulerJobsSSH                          bool                   `mapstructure:"scheduler-jobs-ssh" toml:"scheduler-jobs-ssh" json:"schedulerJobsSsh"`
	SchedulerJobsSSHCron                      string                 `mapstructure:"scheduler-jobs-ssh-cron" toml:"scheduler-jobs-ssh-cron" json:"schedulerJobsSshCron"`
	Backup                                    bool                   `mapstructure:"backup" toml:"backup" json:"backup"`
	BackupLogicalType                         string                 `mapstructure:"backup-logical-type" toml:"backup-logical-type" json:"backupLogicalType"`
	BackupLogicalLoadThreads                  int                    `mapstructure:"backup-logical-load-threads" toml:"backup-logical-load-threads" json:"backupLogicalLoadThreads"`
	BackupLogicalDumpThreads                  int                    `mapstructure:"backup-logical-dump-threads" toml:"backup-logical-dump-threads" json:"backupLogicalDumpThreads"`
	BackupLogicalDumpSystemTables             bool                   `mapstructure:"backup-logical-dump-system-tables" toml:"backup-logical-dump-system-tables" json:"backupLogicalDumpSystemTables"`
	BackupPhysicalType                        string                 `mapstructure:"backup-physical-type" toml:"backup-physical-type" json:"backupPhysicalType"`
	BackupKeepUntilValid                      bool                   `mapstructure:"backup-keep-until-valid" toml:"backup-keep-until-valid" json:"backupKeepUntilValid"`
	BackupKeepLast                            int                    `mapstructure:"backup-keep-last" toml:"backup-keep-last" json:"backupKeepLast"`
	BackupKeepHourly                          int                    `mapstructure:"backup-keep-hourly" toml:"backup-keep-hourly" json:"backupKeepHourly"`
	BackupKeepDaily                           int                    `mapstructure:"backup-keep-daily" toml:"backup-keep-daily" json:"backupKeepDaily"`
	BackupKeepWeekly                          int                    `mapstructure:"backup-keep-weekly" toml:"backup-keep-weekly" json:"backupKeepWeekly"`
	BackupKeepMonthly                         int                    `mapstructure:"backup-keep-monthly" toml:"backup-keep-monthly" json:"backupKeepMonthly"`
	BackupKeepYearly                          int                    `mapstructure:"backup-keep-yearly" toml:"backup-keep-yearly" json:"backupKeepYearly"`
	BackupKeepWithin                          string                 `mapstructure:"backup-keep-within" toml:"backup-keep-within" json:"backupKeepWithin"`
	BackupKeepWithinHourly                    string                 `mapstructure:"backup-keep-within-hourly" toml:"backup-keep-within-hourly" json:"backupKeepWithinHourly"`
	BackupKeepWithinDaily                     string                 `mapstructure:"backup-keep-within-daily" toml:"backup-keep-within-daily" json:"backupKeepWithinDaily"`
	BackupKeepWithinWeekly                    string                 `mapstructure:"backup-keep-within-weekly" toml:"backup-keep-within-weekly" json:"backupKeepWithinWeekly"`
	BackupKeepWithinMonthly                   string                 `mapstructure:"backup-keep-within-monthly" toml:"backup-keep-within-monthly" json:"backupKeepWithinMonthly"`
	BackupKeepWithinYearly                    string                 `mapstructure:"backup-keep-within-yearly" toml:"backup-keep-within-yearly" json:"backupKeepWithinYearly"`
	BackupResticTags                          string                 `mapstructure:"backup-restic-tags" toml:"backup-restic-tags" json:"backupResticTags"`
	BackupResticHost                          string                 `mapstructure:"backup-restic-host" toml:"backup-restic-host" json:"backupResticHost"`
	BackupResticPurgeGroupBy                  string                 `mapstructure:"backup-restic-purge-group-by" toml:"backup-restic-purge-group-by" json:"backupResticPurgeGroupBy"`
	BackupResticPurgeKeepTag                  string                 `mapstructure:"backup-restic-purge-keep-tag" toml:"backup-restic-purge-keep-tag" json:"backupResticPurgeKeepTag"`
	BackupRestic                              bool                   `mapstructure:"backup-restic" toml:"backup-restic" json:"backupRestic"`
	BackupResticBinaryPath                    string                 `mapstructure:"backup-restic-binary-path" toml:"backup-restic-binary-path" json:"backupResticBinaryPath"`
	BackupResticLocalRepository               string                 `mapstructure:"backup-restic-local-repository" toml:"backup-restic-local-repository" json:"backupResticLocalRepository"`
	BackupResticAwsAccessKeyId                string                 `mapstructure:"backup-restic-aws-access-key-id" toml:"backup-restic-aws-access-key-id" json:"backupResticAwsAccessKeyId"`
	BackupResticAwsAccessSecret               string                 `mapstructure:"backup-restic-aws-access-secret"  toml:"backup-restic-aws-access-secret" json:"-"`
	BackupResticRepository                    string                 `mapstructure:"backup-restic-repository" toml:"backup-restic-repository" json:"backupResticRepository"`
	BackupResticPassword                      string                 `mapstructure:"backup-restic-password"  toml:"backup-restic-password" json:"-"`
	BackupResticAws                           bool                   `mapstructure:"backup-restic-aws"  toml:"backup-restic-aws" json:"backupResticAws"`
	BackupResticTimeout                       int                    `mapstructure:"backup-restic-timeout"  toml:"backup-restic-timeout" json:"backupResticTimeout"`
	BackupResticDirMode                       int                    `mapstructure:"backup-restic-dir-mode" toml:"backup-restic-dir-mode" json:"backupResticDirMode"`
	BackupResticFileMode                      int                    `mapstructure:"backup-restic-file-mode" toml:"backup-restic-file-mode" json:"backupResticFileMode"`
	BackupResticPurgeOldestOnDiskSpace        bool                   `mapstructure:"backup-restic-purge-oldest-on-disk-space" toml:"backup-restic-purge-oldest-on-disk-space" json:"backupResticPurgeOldestOnDiskSpace"`
	BackupResticPurgeOldestOnDiskThreshold    int                    `mapstructure:"backup-restic-purge-oldest-on-disk-threshold" toml:"backup-restic-purge-oldest-on-disk-treshold" json:"backupResticPurgeOldestOnDiskTreshold"`
	BackupStreaming                           bool                   `mapstructure:"backup-streaming" toml:"backup-streaming" json:"backupStreaming"`
	BackupStreamingDebug                      bool                   `mapstructure:"backup-streaming-debug" toml:"backup-streaming-debug" json:"backupStreamingDebug"`
	BackupStreamingAwsAccessKeyId             string                 `mapstructure:"backup-streaming-aws-access-key-id" toml:"backup-streaming-aws-access-key-id" json:"-"`
	BackupStreamingAwsAccessSecret            string                 `mapstructure:"backup-streaming-aws-access-secret"  toml:"backup-streaming-aws-access-secret" json:"-"`
	BackupStreamingEndpoint                   string                 `mapstructure:"backup-streaming-endpoint" toml:"backup-streaming-endpoint" json:"backupStreamingEndpoint"`
	BackupStreamingRegion                     string                 `mapstructure:"backup-streaming-region" toml:"backup-streaming-region" json:"backupStreamingRegion"`
	BackupStreamingBucket                     string                 `mapstructure:"backup-streaming-bucket" toml:"backup-streaming-bucket" json:"backupStreamingBucket"`
	BackupMysqldumpPath                       string                 `mapstructure:"backup-mysqldump-path" toml:"backup-mysqldump-path" json:"backupMysqldumpPath"`
	BackupMysqldumpOptions                    string                 `mapstructure:"backup-mysqldump-options" toml:"backup-mysqldump-options" json:"backupMysqldumpOptions"`
	BackupMyDumperPath                        string                 `mapstructure:"backup-mydumper-path" toml:"backup-mydumper-path" json:"backupMydumperPath"`
	BackupMyLoaderPath                        string                 `mapstructure:"backup-myloader-path" toml:"backup-myloader-path" json:"backupMyloaderPath"`
	BackupMyLoaderOptions                     string                 `mapstructure:"backup-myloader-options" toml:"backup-myloader-options" json:"backupMyLoaderOptions"`
	BackupMyDumperOptions                     string                 `mapstructure:"backup-mydumper-options" toml:"backup-mydumper-options" json:"backupMyDumperOptions"`
	BackupMyDumperRegex                       string                 `mapstructure:"backup-mydumper-regex" toml:"backup-mydumper-regex" json:"backupMyDumperRegex"`
	BackupMysqlbinlogPath                     string                 `mapstructure:"backup-mysqlbinlog-path" toml:"backup-mysqlbinlog-path" json:"backupMysqlbinlogPath"`
	BackupMysqlclientPath                     string                 `mapstructure:"backup-mysqlclient-path" toml:"backup-mysqlclient-path" json:"backupMysqlclientPath"`
	BackupMysqlclientOptions                  string                 `mapstructure:"backup-mysqlclient-options" toml:"backup-mysqlclient-options" json:"backupMysqlclientOptions"`
	BackupMytopPath                           string                 `mapstructure:"backup-mytop-path" toml:"backup-mytop-path" json:"backupMytopPath"`
	BackupGottyClientPath                     string                 `mapstructure:"backup-gotty-client-path" toml:"backup-gotty-client-path" json:"backupGottyClientPath"`
	BackupBinlogs                             bool                   `mapstructure:"backup-binlogs" toml:"backup-binlogs" json:"backupBinlogs"`
	BackupBinlogsKeep                         int                    `mapstructure:"backup-binlogs-keep" toml:"backup-binlogs-keep" json:"backupBinlogsKeep"`
	BackupLockDDL                             bool                   `mapstructure:"backup-lockddl" toml:"backup-lockddl" json:"backupLockDDL"`
	BackupRestoreVersionStrict                bool                   `mapstructure:"backup-restore-version-strict" toml:"backup-restore-version-strict" json:"backupRestoreVersionStrict"`
	BinlogCopyMode                            string                 `mapstructure:"binlog-copy-mode" toml:"binlog-copy-mode" json:"binlogCopyMode"`
	BinlogCopyScript                          string                 `mapstructure:"binlog-copy-script" toml:"binlog-copy-script" json:"binlogCopyScript"`
	BinlogRotationScript                      string                 `mapstructure:"binlog-rotation-script" toml:"binlog-rotation-script" json:"binlogRotationScript"`
	BinlogParseMode                           string                 `mapstructure:"binlog-parse-mode" toml:"binlog-parse-mode" json:"binlogParseMode"`
	ClusterConfigPath                         string                 `mapstructure:"cluster-config-file" toml:"-" json:"-"`
	VaultServerAddr                           string                 `mapstructure:"vault-server-addr" toml:"vault-server-addr" json:"vaultServerAddr"`
	VaultRoleId                               string                 `mapstructure:"vault-role-id" toml:"vault-role-id" json:"vaultRoleId"`
	VaultSecretId                             string                 `mapstructure:"vault-secret-id" toml:"vault-secret-id" json:"vaultSecretId"`
	VaultMode                                 string                 `mapstructure:"vault-mode" toml:"vault-mode" json:"vaultMode"`
	VaultMount                                string                 `mapstructure:"vault-mount" toml:"vault-mount" json:"vaultMount"`
	VaultAuth                                 string                 `mapstructure:"vault-auth" toml:"vault-auth" json:"vaultAuth"`
	VaultToken                                string                 `mapstructure:"vault-token" toml:"vault-token" json:"vaultToken"`
	LogVault                                  bool                   `mapstructure:"log-vault" toml:"log-vault" json:"logVault"`
	LogVaultLevel                             int                    `mapstructure:"log-level-vault" toml:"log-level-vault" json:"logVaultLevel"`
	GitUrl                                    string                 `scope:"server" mapstructure:"git-url" toml:"git-url" json:"gitUrl"`
	GitUrlPull                                string                 `scope:"server" mapstructure:"git-url-pull" toml:"git-url-pull" json:"gitUrlPull"`
	GitUsername                               string                 `scope:"server" mapstructure:"git-username" toml:"git-username" json:"gitUsername"`
	GitAccesToken                             string                 `scope:"server" mapstructure:"git-acces-token" toml:"git-acces-token" json:"-"`
	GitMonitoringTicker                       int                    `scope:"server" mapstructure:"git-monitoring-ticker" toml:"git-monitoring-ticker" json:"gitMonitoringTicker"`
	Cloud18                                   bool                   `scope:"server" mapstructure:"cloud18"  toml:"cloud18" json:"cloud18"`
	Cloud18Domain                             string                 `scope:"server" mapstructure:"cloud18-domain" toml:"cloud18-domain" json:"cloud18Domain" groups:"apps"`
	Cloud18SubDomain                          string                 `scope:"server" mapstructure:"cloud18-sub-domain" toml:"cloud18-sub-domain" json:"cloud18SubDomain" groups:"apps"`
	Cloud18SubDomainZone                      string                 `scope:"server" mapstructure:"cloud18-sub-domain-zone" toml:"cloud18-sub-domain-zone" json:"cloud18SubDomainZone" groups:"apps"`
	Cloud18GitUser                            string                 `scope:"server" mapstructure:"cloud18-gitlab-user" toml:"cloud18-gitlab-user" json:"cloud18GitUser"`
	Cloud18GitPassword                        string                 `scope:"server" mapstructure:"cloud18-gitlab-password" toml:"cloud18-gitlab-password" json:"-"`
	Cloud18GatewayDomainName                  string                 `scope:"server" mapstructure:"cloud18-gateway-domain-name" toml:"cloud18-gateway-domain-name"  json:"cloud18GatewayDomainName"`
	Cloud18GatewayService                     string                 `scope:"server" mapstructure:"cloud18-gateway-service" toml:"Cloud18-gateway-service" json:"cloud18GatewayService"`
	Cloud18DomainAddScript                    string                 `scope:"server" mapstructure:"cloud18-domain-add-script" toml:"cloud18-domain-add-script" json:"cloud18DomainAddScript"`
	Cloud18DomainDropScript                   string                 `scope:"server" mapstructure:"cloud18-domain-drop-script" toml:"cloud18-domain-drop-script" json:"cloud18DomainDropScript"`
	Cloud18DomainUser                         string                 `scope:"server" mapstructure:"cloud18-domain-user" toml:"cloud18-domain-user" json:"cloud18DomainUser"`
	Cloud18DomainSecret                       string                 `scope:"server" mapstructure:"cloud18-domain-secret" toml:"cloud18-domain-secret" json:"cloud18DomainSecret"`
	Cloud18Shared                             bool                   `mapstructure:"cloud18-shared"  toml:"cloud18-shared" json:"cloud18Shared"`
	Cloud18PlatformDescription                string                 `mapstructure:"cloud18-platform-description"  toml:"cloud18-platform-description" json:"cloud18PlatformDescription"`
	Cloud18MonthlyInfraCost                   float64                `mapstructure:"cloud18-monthly-infra-cost"  toml:"cloud18-monthly-infra-cost" json:"cloud18MonthlyInfraCost"`
	Cloud18MonthlyLicenseCost                 float64                `mapstructure:"cloud18-monthly-license-cost"  toml:"cloud18-monthly-license-cost" json:"cloud18MonthlyLicenseCost"`
	Cloud18MonthlySysopsCost                  float64                `mapstructure:"cloud18-monthly-sysops-cost"  toml:"cloud18-monthly-sysops-cost" json:"cloud18MonthlySysopsCost"`
	Cloud18MonthlyDbopsCost                   float64                `mapstructure:"cloud18-monthly-dbops-cost"  toml:"cloud18-monthly-dbops-cost" json:"cloud18MonthlyDbopsCost"`
	Cloud18MonthlyExternalSysopsCost          float64                `mapstructure:"cloud18-monthly-external-sysops-cost" toml:"cloud18-monthly-external-sysops-cost" json:"cloud18MonthlyExternalSysopsCost"`
	Cloud18MonthlyExternalDbopsCost           float64                `mapstructure:"cloud18-monthly-external-dbops-cost" toml:"cloud18-monthly-external-dbops-cost" json:"cloud18MonthlyExternalDbopsCost"`
	Cloud18PromotionPct                       float64                `mapstructure:"cloud18-promotion-pct"  toml:"cloud18-promotion-pct" json:"cloud18PromotionPct"`
	Cloud18SlaResponseTime                    float64                `mapstructure:"cloud18-sla-response-time"  toml:"cloud18-sla-response-time" json:"cloud18SlaResponseTime"`
	Cloud18SlaRepairTime                      float64                `mapstructure:"cloud18-sla-repair-time"  toml:"cloud18-sla-repair-time" json:"cloud18SlaRepairTime"`
	Cloud18SlaProvisionTime                   float64                `mapstructure:"cloud18-sla-provision-time"  toml:"cloud18-sla-provision-time" json:"cloud18SlaProvisionTime"`
	Cloud18CostCurrency                       string                 `mapstructure:"cloud18-cost-currency"  toml:"cloud18-cost-currency" json:"cloud18CostCurrency"`
	Cloud18InfraCPUModel                      string                 `mapstructure:"cloud18-infra-cpu-model"  toml:"cloud18-infra-cpu-model" json:"cloud18InfraCpuModel"`
	Cloud18InfraCPUFreq                       string                 `mapstructure:"cloud18-infra-cpu-freq"  toml:"cloud18-infra-cpu-freq" json:"cloud18InfraCpuFreq"`
	Cloud18InfraDescription                   string                 `mapstructure:"cloud18-infra-description"  toml:"cloud18-infra-description" json:"cloud18InfraDescription"`
	Cloud18InfraDataCenters                   string                 `mapstructure:"cloud18-infra-data-centers"  toml:"cloud18-infra-data-centers" json:"cloud18InfraDataCenters"`
	Cloud18InfraPublicBandwidth               float64                `mapstructure:"cloud18-infra-public-bandwidth"  toml:"cloud18-infra-public-bandwidth" json:"cloud18InfraPublicBandwidth"`
	Cloud18InfraGeoLocalizations              string                 `mapstructure:"cloud18-infra-geo-localizations"  toml:"cloud18-infra-geo-localizations" json:"cloud18InfraGeoLocalizations"`
	Cloud18DbOps                              string                 `mapstructure:"cloud18-dbops"  toml:"cloud18-dbops" json:"cloud18DbOps"`
	Cloud18ExternalDbOps                      string                 `mapstructure:"cloud18-external-dbops"  toml:"cloud18-external-dbops" json:"cloud18ExternalDbOps"`
	Cloud18ExternalDbOpsStatus                string                 `mapstructure:"cloud18-external-dbops-status"  toml:"cloud18-external-dbops-status" json:"cloud18ExternalDbOpsStatus"`
	Cloud18ExternalSysOps                     string                 `mapstructure:"cloud18-external-sysops" toml:"cloud18-external-sysops" json:"cloud18ExternalSysOps"`
	Cloud18ExternalSysOpsStatus               string                 `mapstructure:"cloud18-external-sysops-status" toml:"cloud18-external-sysops-status" json:"cloud18ExternalSysOpsStatus"`
	Cloud18InfraCertifications                string                 `mapstructure:"cloud18-infra-certifications"  toml:"cloud18-infra-certifications" json:"cloud18InfraCertifications"`
	Cloud18OpenDbops                          bool                   `mapstructure:"cloud18-open-dbops"  toml:"cloud18-open-dbops" json:"cloud18OpenDbops"`
	Cloud18SubscribedDbops                    bool                   `mapstructure:"cloud18-subscribed-dbops"  toml:"cloud18-subscribed-dbops" json:"cloud18SubscribedDbops"`
	Cloud18OpenSysops                         bool                   `mapstructure:"cloud18-open-sysops"  toml:"cloud18-open-sysops" json:"cloud18OpenSysops"`
	Cloud18DatabaseReadWriteSplitSrvRecord    string                 `mapstructure:"cloud18-database-read-write-split-srv-record"  toml:"cloud18-database-read-write-split-srv-record" json:"cloud18DatabaseReadWriteSplitSrvRecord"`
	Cloud18DatabaseReadSrvRecord              string                 `mapstructure:"cloud18-database-read-srv-record"  toml:"cloud18-database-read-srv-record" json:"cloud18DatabaseReadSrvRecord"`
	Cloud18DatabaseReadWriteSrvRecord         string                 `mapstructure:"cloud18-database-read-write-srv-record"  toml:"cloud18-database-read-write-srv-record" json:"cloud18DatabaseReadWriteSrvRecord"`
	Cloud18DbaUserCredentials                 string                 `mapstructure:"cloud18-dba-user-credentials"  toml:"cloud18-dba-user-credentials" json:"cloud18DbaUserCredential"`
	Cloud18SponsorUserCredentials             string                 `mapstructure:"cloud18-sponsor-user-credentials"  toml:"cloud18-sponsor-user-credentials" json:"cloud18SponsorUserCredential"`
	Cloud18SalesSubscriptionScript            string                 `mapstructure:"cloud18-sales-subscription-script"  toml:"cloud18-sales-subscription-script" json:"cloud18SalesSubscriptionScript"`
	Cloud18SalesSubscriptionValidateScript    string                 `mapstructure:"cloud18-sales-subscription-validate-script"  toml:"cloud18-sales-subscription-validate-script" json:"cloud18SalesSubscriptionValidateScript"`
	Cloud18SalesUnsubscribeScript             string                 `mapstructure:"cloud18-sales-unsubscribe-script"  toml:"cloud18-sales-unsubscribe-script" json:"cloud18SalesUnsubscribeScript"`
	Cloud18SalesExternalOpsValidateScript     string                 `mapstructure:"cloud18-sales-external-ops-validate-script"  toml:"cloud18-sales-external-ops-validate-script" json:"cloud18SalesExternalOpsValidateScript"`
	Cloud18SalesExternalOpsStopScript         string                 `mapstructure:"cloud18-sales-external-ops-stop-script"  toml:"cloud18-sales-external-ops-stop-script" json:"cloud18SalesExternalOpsStopScript"`
	Cloud18Alert                              bool                   `mapstructure:"cloud18-alert"  toml:"cloud18-alert" json:"cloud18Alert"`
	Cloud18AlertSlackChannel                  string                 `mapstructure:"cloud18-alert-slack-channel"  toml:"cloud18-alert-slack-channel" json:"cloud18AlertSlackChannel"`
	Cloud18AlertSlackURL                      string                 `mapstructure:"cloud18-alert-slack-url"  toml:"cloud18-alert-slack-url" json:"cloud18AlertSlackUrl"`
	Cloud18AlertSlackUser                     string                 `mapstructure:"cloud18-alert-slack-user"  toml:"cloud18-alert-slack-user" json:"cloud18AlertSlackUser"`
	Cloud18HealthRefreshInterval              int                    `mapstructure:"cloud18-health-refresh-interval"  toml:"cloud18-health-refresh-interval" json:"cloud18HealthRefreshInterval"`
	Cloud18ApplicationCredits                 int                    `mapstructure:"cloud18-application-credits" toml:"Cloud18-application-credits" json:"cloud18ApplicationCredits"`
	Cloud18ApplicationCreditsUsed             int                    `mapstructure:"cloud18-application-credits-used" toml:"Cloud18-application-credits-used" json:"cloud18ApplicationCreditsUsed"`
	Cloud18ApplicationCreditsPrice            int                    `mapstructure:"cloud18-application-credits-price" toml:"Cloud18-application-credits-price" json:"cloud18ApplicationCreditsPrice"`
	ProvRegister                              bool                   `mapstructure:"opensvc-register" toml:"opensvc-register" json:"opensvcRegister"`
	ProvAdminUser                             string                 `mapstructure:"opensvc-admin-user" toml:"opensvc-admin-user" json:"opensvcAdminUser"`
	Measurement                               bool                   `mapstructure:"measurement" toml:"measurement" json:"measurement"`
	MeasurementAutoClampLimit                 bool                   `mapstructure:"measurement-auto-clamp-limit"  toml:"measurement-auto-clamp-limit" json:"measurementAutoClampLimit"`
	LogSecrets                                bool                   `mapstructure:"log-secrets"  toml:"log-secrets" json:"-"`
	Apps                                      []*AppConfig           `mapstructure:"apps" toml:"apps" json:"apps" groups:"apps"`
	Secrets                                   map[string]Secret      `toml:"-" json:"-"`
	SecretKey                                 []byte                 `toml:"-" json:"-"`
	ImmuableFlagMap                           map[string]interface{} `toml:"-" json:"-"`
	DynamicFlagMap                            map[string]interface{} `toml:"-" json:"-"`
	DefaultFlagMap                            map[string]interface{} `toml:"-" json:"-"`
	OAuthProvider                             string                 `mapstructure:"api-oauth-provider-url" toml:"api-oauth-provider-url" json:"apiOAuthProvider"`
	OAuthClientID                             string                 `mapstructure:"api-oauth-client-id" toml:"api-oauth-client-id" json:"apiOAuthClientID"`
	OAuthClientSecret                         string                 `mapstructure:"api-oauth-client-secret" toml:"api-oauth-client-secret" json:"apiOAuthClientSecret"`
	CacheStaticMaxAge                         int                    `mapstructure:"cache-static-max-age" toml:"cache-static-max-age" json:"-"`
	TokenTimeout                              int                    `scope:"server" mapstructure:"api-token-timeout" toml:"api-token-timeout" json:"apiTokenTimeout"`
	JobLogBatchSize                           int                    `mapstructure:"job-log-batch-size" toml:"job-log-batch-size" json:"jobLogBatchSize"`
	ApiSwaggerEnabled                         bool                   `scope:"server" mapstructure:"api-swagger-enabled" toml:"api-swagger-enabled" json:"apiSwaggerEnabled"`
	TerminalSessionEnabled                    bool                   `scope:"server" mapstructure:"terminal-session-enabled" toml:"terminal-session-enabled" json:"terminalSessionEnabled"`
	TerminalSessionResume                     bool                   `scope:"server" mapstructure:"terminal-session-resume" toml:"terminal-session-resume" json:"terminalSessionResume"`
	TerminalSessionManager                    string                 `mapstructure:"terminal-session-manager" toml:"terminal-session-manager" json:"terminalSessionManager"`
	//OAuthRedirectURL                          string                 `mapstructure:"api-oauth-redirect-url" toml:"git-url" json:"-"`
	//	BackupResticStoragePolicy                  string `mapstructure:"backup-restic-storage-policy"  toml:"backup-restic-storage-policy" json:"backupResticStoragePolicy"`
	//ProvMode                           string `mapstructure:"prov-mode" toml:"prov-mode" json:"provMode"` //InitContainer vs API
}

type AppConfig struct {
	ProvAppType           string      `mapstructure:"prov-app-service-type" toml:"prov-app-service-type" json:"provAppServiceType"`
	ProvAppMem            string      `measurement:"M,bytes,required" mapstructure:"prov-app-memory" toml:"prov-app-memory" json:"provAppMemory"`
	ProvAppCpuCores       string      `mapstructure:"prov-app-cpu-cores" toml:"prov-app-cpu-cores" json:"provAppCpuCores"`
	ProvAppDisk           string      `measurement:"G,bytes,required" mapstructure:"prov-app-disk-size" toml:"prov-app-disk-size" json:"provAppDiskSize"`
	ProvAppDiskType       string      `mapstructure:"prov-app-disk-type" toml:"prov-app-disk-type" json:"provAppDiskType"`
	ProvAppDockerImg      string      `mapstructure:"prov-app-docker-img" toml:"prov-app-docker-img" json:"provAppDockerImg"`
	ProvAppDockerCmd      string      `mapstructure:"prov-app-docker-cmd" toml:"prov-app-docker-cmd" json:"provAppDockerCmd"`
	ProvAppRouteAddr      string      `mapstructure:"prov-app-route-addr" toml:"prov-app-route-addr" json:"provAppRouteAddr"`
	ProvAppRoutePort      string      `mapstructure:"prov-app-route-port" toml:"prov-app-route-port" json:"provAppRoutePort"`
	ProvAppRouteMask      string      `mapstructure:"prov-app-route-mask" toml:"prov-app-route-mask" json:"provAppRouteMask"`
	ProvAppTemplate       string      `mapstructure:"prov-app-template" toml:"prov-app-template" json:"provAppTemplate"`
	ProvAppAgents         string      `mapstructure:"prov-app-agents" toml:"prov-app-agents" json:"provAppAgents"`
	ProvAppHATopology     string      `mapstructure:"prov-app-ha-topology" toml:"prov-app-ha-topology" json:"provAppHaTopology"`
	ProvAppAgentsFailover string      `mapstructure:"prov-app-agents-failover" toml:"prov-app-agents-failover" json:"provAppAgentsFailover"`
	ProvAppCreditUsed     int         `mapstructure:"prov-app-credit-used" toml:"prov-app-credit-used" json:"provAppCreditUsed"`
	ProvAppCreditPlanned  int         `mapstructure:"prov-app-credit-planned" toml:"prov-app-credit-planned" json:"provAppCreditPlanned"`
	AppHost               string      `mapstructure:"app-host" toml:"app-host" json:"appHost"`
	AppHostsIPV6          string      `mapstructure:"app-hosts-ipv6" toml:"app-hosts-ipv6" json:"appHostsIpv6"`
	AppPort               string      `mapstructure:"app-port" toml:"app-port" json:"appPort"`
	AppDbUser             string      `mapstructure:"app-db-user" toml:"app-db-user" json:"appDbUser" groups:"apps"`
	AppDbPass             string      `mapstructure:"app-db-pass" toml:"app-db-pass" json:"appDbPass" groups:"apps"`
	AppDbPassClear        string      `mapstructure:"app-db-pass-clear" toml:"-" json:"-" app:"-"`
	AppDbSchema           string      `mapstructure:"app-db-schema" toml:"app-db-schema" json:"appDbSchema" groups:"apps"`
	AppS3Provider         bool        `mapstructure:"app-s3-provider" toml:"app-s3-provider" json:"appS3Provider"`
	Deployment            *Deployment `mapstructure:"deployment" toml:"deployment" json:"deployment" groups:"apps"`
}

func (appcnf *AppConfig) GetDeploymentVariables(name string) *VariableMapping {
	for _, v := range appcnf.Deployment.Variables {
		if v.Name == name {
			return &v
		}
	}

	return nil
}

type AgentVariable struct {
	Agent string `mapstructure:"agent" toml:"agent" json:"agent"`
	Value string `mapstructure:"value" toml:"value" json:"value"`
}

type AVSlice []AgentVariable

func (old AVSlice) Merge(new AVSlice, addFunc func(new AgentVariable) AgentVariable, updateFunc func(old, new AgentVariable) AgentVariable) AVSlice {
	agentMap := make(map[string]AgentVariable)
	addMap := make(map[string]bool)
	for _, av := range new {
		agentMap[av.Agent] = av // Update or add the agent variable
		addMap[av.Agent] = true
	}
	for _, av := range old {
		if newVal, exists := agentMap[av.Agent]; exists {
			addMap[av.Agent] = false // Mark as not added

			// If the value is different, we may want to update it
			if newVal.Value != av.Value {
				// If updateFunc is provided, use it to update the value
				if updateFunc != nil {
					agentMap[av.Agent] = updateFunc(av, agentMap[av.Agent])
				}
			}
		}
	}

	var merged AVSlice
	for agent, av := range agentMap {
		if addMap[agent] && addFunc != nil {
			// If addFunc is provided, use it to add the agent variable
			merged = append(merged, addFunc(av))
		} else {
			merged = append(merged, av)
		}
	}

	return merged
}

func (a AVSlice) Len() int           { return len(a) }
func (a AVSlice) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a AVSlice) Less(i, j int) bool { return a[i].Agent < a[j].Agent }

type WorkLoad struct {
	DBTableSize   int64   `json:"dbTableSize"`
	DBIndexSize   int64   `json:"dbIndexSize"`
	Connections   int     `json:"connections"`
	QPS           int64   `json:"qps"`
	CpuThreadPool float64 `json:"cpuThreadPool"`
	CpuUserStats  float64 `json:"cpuUserStats"`
	BusyTime      string
}

type ConfigVariableType struct {
	Id        int    `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Label     string `json:"label"`
}

type Secret struct {
	OldValue string
	Value    string
}

type Partner struct {
	Id          int
	Name        string
	IsDbops     int
	IsSysops    int
	DbopsEmail  string
	SysopsEmail string
	Stars       int
}

// Compliance created in OpenSVC collector and exported as JSON
type Compliance struct {
	Filtersets []ComplianceFilterset `json:"filtersets"`
	Rulesets   []ComplianceRuleset   `json:"rulesets"`
}

// Compliance created in OpenSVC collector and exported as JSON
type ComplianceFilterset struct {
	ID    uint   `json:"id"`
	Stats bool   `json:"fset_stats"`
	Name  string `json:"fset_name"`
}

type ComplianceRuleset struct {
	ID        uint                 `json:"id"`
	Name      string               `json:"ruleset_name"`
	Filter    string               `json:"fset_name"`
	Variables []ComplianceVariable `json:"variables"`
}

type ComplianceVariable struct {
	Value string `json:"var_value"`
	Class string `json:"var_class"`
	Name  string `json:"var_name"`
}

type QueryRule struct {
	Id                   uint32         `json:"ruleId" db:"rule_id"`
	Active               int            `json:"active" db:"active"`
	UserName             sql.NullString `json:"userName" db:"username"`
	SchemaName           sql.NullString `json:"schemaName" db:"schemaname"`
	Digest               sql.NullString `json:"digest" db:"digest"`
	Match_Digest         sql.NullString `json:"matchDigest" db:"match_digest"`
	Match_Pattern        sql.NullString `json:"matchPattern" db:"match_pattern"`
	DestinationHostgroup sql.NullInt64  `json:"destinationHostgroup" db:"destination_hostgroup"`
	MirrorHostgroup      sql.NullInt64  `json:"mirrorHostgroup" db:"mirror_hostgroup"`
	Multiplex            sql.NullInt64  `json:"multiplex" db:"multiplex"`
	Proxies              string         `json:"proxies" db:"proxies"`
}

type MyDumperMetaData struct {
	MetaDir        string    `json:"metadir" db:"metadir"`
	StartTimestamp time.Time `json:"start_timestamp" db:"start_timestamp"`
	BinLogFileName string    `json:"log_filename" db:"log_filename"`
	BinLogFilePos  uint64    `json:"log_pos" db:"log_pos"`
	BinLogUuid     string    `json:"log_uuid" db:"log_uuid"`
	EndTimestamp   time.Time `json:"end_timestamp" db:"end_timestamp"`
}

type ConfVersion struct {
	ConfInit     Config `json:"-"`
	ConfDecode   Config `json:"-"`
	ConfDynamic  Config `json:"-"`
	ConfImmuable Config `json:"-"`
}

const (
	OpenSVCTopologyFailover string = "failover"
	OpenSVCTopologyFlex     string = "flex"
)

// Log levels
const (
	LvlInfo = "INFO"
	LvlWarn = "WARN"
	LvlErr  = "ERROR"
	LvlDbg  = "DEBUG"
)

// Log levels
const (
	NumLvlError = 1
	NumLvlWarn  = 2
	NumLvlInfo  = 3
	NumLvlDebug = 4
)

const (
	ConstStreamingSubDir string = "backups"
)
const (
	ConstProxyMaxscale    string = "maxscale"
	ConstProxyHaproxy     string = "haproxy"
	ConstProxySqlproxy    string = "proxysql"
	ConstProxyJanitor     string = "proxyjanitor"
	ConstProxySpider      string = "shardproxy"
	ConstProxyExternal    string = "extproxy"
	ConstProxyMysqlrouter string = "mysqlrouter"
	ConstProxySphinx      string = "sphinx"
	ConstProxyMyProxy     string = "myproxy"
	ConstProxyConsul      string = "consul"
)

type ServicePlan struct {
	Id            int     `json:"id,string"`
	Plan          string  `json:"plan"`
	DbMemory      int     `json:"dbmemory,string"`
	DbCores       int     `json:"dbcores,string"`
	DbDataSize    int     `json:"dbdatasize,string"`
	DbSystemSize  int     `json:"dbsystemsize,string"`
	DbIops        int     `json:"dbiops,string"`
	PrxDataSize   int     `json:"prxdatasize,string"`
	PrxCores      int     `json:"prxcores,string"`
	InfraCost     float64 `json:"infracost,string"`
	LicenceCost   float64 `json:"licencecost,string"`
	DbaCost       float64 `json:"dbacost,string"`
	SysCost       float64 `json:"syscost,string"`
	Devise        string  `json:"devise"`
	CPU           string  `json:"cpu"`
	CPUFreq       string  `json:"dbcpufreq"`
	Infra         string  `json:"infra"`
	Zone          string  `json:"zone"`
	DC            string  `json:"dc"`
	ResponseTime  float64 `json:"gti,string"`
	RepairTime    float64 `json:"gtr,string"`
	ProvisionTime float64 `json:"provtime,string"`
	PromotionPct  float64 `json:"promo,string"`
	BP            float64 `json:"bp,string"`
	Certs         string  `json:"certs"`
	ExtDbOps      string  `json:"extdbops"`
	ExtSysOps     string  `json:"extsysops"`
}

type DockerTag struct {
	Results []TagResult `json:"results"`
}

type TagResult struct {
	Name string `json:"name"`
}

type DockerRepo struct {
	Name  string    `json:"name"`
	Image string    `json:"image"`
	Tags  DockerTag `json:"tags"`
}

type DockerRepos struct {
	Repos []DockerRepo `json:"repos"`
}

// Stucture to hold header of mytop
type TopMetrics struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}
type TopGraph struct {
	Name string       `json:"name"`
	Data []TopMetrics `json:"data"`
}
type TopHeader struct {
	Graphs []TopGraph `json:"graphs"`
}

type ServerTop struct {
	Id          string                 `json:"id"`
	Url         string                 `json:"url"`
	Header      TopHeader              `json:"header"`
	Processlist []dbhelper.Processlist `json:"processlist"`
}

const (
	VaultConfigStoreV2 string = "config_store_v2"
	VaultDbEngine      string = "database_engine"
)

/* replaced by v3.Tag
type Tag struct {
	Id       uint   `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}
*/

type Grant struct {
	Grant  string `json:"grant"`
	Enable bool   `json:"enable"`
}

type Role struct {
	Role   string `json:"role"`
	Enable bool   `json:"enable"`
}

const (
	ExternalActive  string = "active"
	ExternalPending string = "pending"
	ExternalQuote   string = "quote"
)

const (
	RoleSysOps                string = "sysops"
	RoleDBOps                 string = "dbops"
	RoleExtSysOps             string = "extsysops"
	RoleExtDBOps              string = "extdbops"
	RoleSponsor               string = "sponsor"
	RoleUnsubscribed          string = "unsubscribed"
	RoleUnsubscribedExtDBOps  string = "unsubscribed-extdbops"
	RoleUnsubscribedExtSysOps string = "unsubscribed-extsysops"
	RolePending               string = "pending"
	RolePendingExtDBOps       string = "pending-extdbops"
	RolePendingExtSysOps      string = "pending-extsysops"
	RoleQuoteExtDBOps         string = "quote-extdbops"
	RoleQuoteExtSysOps        string = "quote-extsysops"
	RoleVisitor               string = "visitor"
)

const (
	GrantDBStart           string = "db-start"
	GrantDBStop            string = "db-stop"
	GrantDBKill            string = "db-kill"
	GrantDBOptimize        string = "db-optimize"
	GrantDBAnalyse         string = "db-analyse"
	GrantDBReplication     string = "db-replication"
	GrantDBBackup          string = "db-backup"
	GrantDBRestore         string = "db-restore"
	GrantDBReadOnly        string = "db-readonly"
	GrantDBLogs            string = "db-logs"
	GrantDBShowVariables   string = "db-show-variables"
	GrantDBShowStatus      string = "db-show-status"
	GrantDBShowSchema      string = "db-show-schema"
	GrantDBShowProcess     string = "db-show-process"
	GrantDBShowLogs        string = "db-show-logs"
	GrantDBCapture         string = "db-capture"
	GrantDBMaintenance     string = "db-maintenance"
	GrantDBConfigCreate    string = "db-config-create"
	GrantDBConfigRessource string = "db-config-ressource"
	GrantDBConfigFlag      string = "db-config-flag"
	GrantDBConfigGet       string = "db-config-get"

	GrantClusterCreate             string = "cluster-create"
	GrantClusterDelete             string = "cluster-delete"
	GrantClusterCreateMonitor      string = "cluster-create-monitor"
	GrantClusterDropMonitor        string = "cluster-drop-monitor"
	GrantClusterFailover           string = "cluster-failover"
	GrantClusterSwitchover         string = "cluster-switchover"
	GrantClusterRolling            string = "cluster-rolling"
	GrantClusterSettings           string = "cluster-settings"
	GrantClusterGrant              string = "cluster-grant"
	GrantClusterAnalyze            string = "cluster-analyze"
	GrantClusterChecksum           string = "cluster-checksum"
	GrantClusterSharding           string = "cluster-sharding"
	GrantClusterReplication        string = "cluster-replication"
	GrantClusterCertificatesRotate string = "cluster-certificates-rotate"
	GrantClusterCertificatesReload string = "cluster-certificates-reload"
	GrantClusterBench              string = "cluster-bench"
	GrantClusterProcess            string = "cluster-process" //Can ssh for jobs
	GrantClusterTest               string = "cluster-test"
	GrantClusterTraffic            string = "cluster-traffic"
	GrantClusterShowBackups        string = "cluster-show-backups"
	GrantClusterShowRoutes         string = "cluster-show-routes"
	GrantClusterShowGraphs         string = "cluster-show-graphs"
	GrantClusterConfigGraphs       string = "cluster-config-graphs"
	GrantClusterShowAgents         string = "cluster-show-agents"
	GrantClusterShowCertificates   string = "cluster-show-certificates"
	GrantClusterRotatePasswords    string = "cluster-rotate-passwords"
	GrantClusterResetSLA           string = "cluster-reset-sla"
	GrantClusterDebug              string = "cluster-debug"
	GrantClusterStaging            string = "cluster-staging"
	GrantClusterAlert              string = "cluster-alert"
	GrantClusterDocker             string = "cluster-docker"

	GrantProxyConfigCreate    string = "proxy-config-create"
	GrantProxyConfigGet       string = "proxy-config-get"
	GrantProxyConfigRessource string = "proxy-config-ressource"
	GrantProxyConfigFlag      string = "proxy-config-flag"
	GrantProxyStart           string = "proxy-start"
	GrantProxyStop            string = "proxy-stop"

	GrantAppConfig     string = "app-config"
	GrantAppDocker     string = "app-docker"
	GrantAppDeployment string = "app-deployment"
	GrantAppStart      string = "app-start"
	GrantAppStop       string = "app-stop"
	GrantAppGit        string = "app-git"

	GrantProvClusterProvision   string = "prov-cluster-provision"
	GrantProvClusterUnprovision string = "prov-cluster-unprovision"
	GrantProvProxyProvision     string = "prov-proxy-provision"
	GrantProvProxyUnprovision   string = "prov-proxy-unprovision"
	GrantProvDBProvision        string = "prov-db-provision"
	GrantProvDBUnprovision      string = "prov-db-unprovision"
	GrantProvAppProvision       string = "prov-app-provision"
	GrantProvAppUnprovision     string = "prov-app-unprovision"
	GrantProvSettings           string = "prov-settings"
	GrantProvCluster            string = "prov-cluster"

	GrantGlobalSettings string = "global-settings" // Can update global settings
	GrantGlobalGrant    string = "global-grant"    // Can grant global settings

	GrantGrantShow   string = "grant-show"   // Can show users settings
	GrantGrantAdd    string = "grant-add"    // Can add new user
	GrantGrantDrop   string = "grant-drop"   // Can drop user ACL
	GrantGrantModify string = "grant-modify" // Can modify user ACL
	GrantGrantGlobal string = "grant-global" // Can grant global acl

	GrantShow         string = "show"    // Can show basic view
	GrantExternalRole string = "extrole" // Can manage external ops

	GrantSalesValidate    string = "sales-validate"    // Can validate sales
	GrantSalesRefuse      string = "sales-refuse"      // Can refuse sales
	GrantSalesUnsubscribe string = "sales-unsubscribe" // Can unsubscribe sales

	GrantTerminalDatabase string = "terminal-db"
	GrantTerminalProxy    string = "terminal-proxy"
	GrantTerminalGlobal   string = "terminal-global" // Can use global terminal
)

const (
	ConstOrchestratorOpenSVC    string = "opensvc"
	ConstOrchestratorKubernetes string = "kube"
	ConstOrchestratorSlapOS     string = "slapos"
	ConstOrchestratorLocalhost  string = "local"
	ConstOrchestratorOnPremise  string = "onpremise"
)

const (
	ConstBackupLogicalTypeMysqldump string = "mysqldump"
	ConstBackupLogicalTypeMydumper  string = "mydumper"
	ConstBackupLogicalTypeRiver     string = "internal"
	ConstBackupLogicalTypeDumpling  string = "dumpling"
)

const (
	ConstBackupPhysicalTypeXtrabackup  string = "xtrabackup"
	ConstBackupPhysicalTypeMariaBackup string = "mariabackup"
)

const (
	ConstBackupBinlogTypeMysqlbinlog string = "mysqlbinlog"
	ConstBackupBinlogTypeSSH         string = "ssh"
	ConstBackupBinlogTypeScript      string = "script"
	ConstBackupBinlogTypeGoMySQL     string = "gomysql"
)

/*
This is the list of modules to be used in LogModulePrintF
*/
const (
	ConstLogModGeneral        = 0
	ConstLogModWriterElection = 1
	ConstLogModSST            = 2
	ConstLogModHeartBeat      = 3
	ConstLogModConfigLoad     = 4
	ConstLogModGit            = 5
	ConstLogModBackupStream   = 6
	ConstLogModOrchestrator   = 7
	ConstLogModVault          = 8
	ConstLogModTopology       = 9
	ConstLogModProxy          = 10
	ConstLogModProxySQL       = 11
	ConstLogModHAProxy        = 12
	ConstLogModProxyJanitor   = 13
	ConstLogModMaxscale       = 14
	ConstLogModGraphite       = 15
	ConstLogModPurge          = 16
	ConstLogModTask           = 17
	ConstLogModRestic         = 18
	ConstLogModMailer         = 19
	ConstLogModSupport        = 20
	ConstLogModExternalScript = 21
	ConstLogModStats          = 22
	ConstLogModSQL            = 23
	ConstLogModApp            = 24
	ConstLogModDbErrors       = 25
	ConstLogModDbSlowquery    = 26
	ConstLogModDbOptimize     = 27
	ConstLogModDbAudit        = 28
	ConstLogModDbSqlErrors    = 29
)

/*
This is the list of modules to be used in LogModulePrintF
*/
const (
	ConstLogNameGeneral        string = "log-general"
	ConstLogNameWriterelection string = "log-writer-election"
	ConstLogNameSST            string = "log-sst"
	ConstLogNameHeartBeat      string = "log-heartbeat"
	ConstLogNameConfigLoad     string = "log-config-load"
	ConstLogNameGit            string = "log-git"
	ConstLogNameBackupStream   string = "log-backup-stream"
	ConstLogNameOrchestrator   string = "log-orchestrator"
	ConstLogNameVault          string = "log-vault"
	ConstLogNameTopology       string = "log-topology"
	ConstLogNameProxy          string = "log-proxy"
	ConstLogNameProxySQL       string = "log-proxysql"
	ConstLogNameHAProxy        string = "log-haproxy"
	ConstLogNameProxyJanitor   string = "log-proxy-janitor"
	ConstLogNameMaxscale       string = "log-maxscale"
	ConstLogNameGraphite       string = "log-graphite"
	ConstLogNamePurge          string = "log-binlog-purge"
	ConstLogNameTask           string = "log-task"
	ConstLogNameRestic         string = "log-restic"
	ConstLogNameMailer         string = "log-mailer"
	ConstLogNameExternalScript string = "log-external-script"
	ConstLogNameLogSQL         string = "log-sql"
	ConstLogNameSupport        string = "log-support"
	ConstLogNameStats          string = "log-stats"
	ConstLogNameApp            string = "log-app"
	ConstLogNameDbErrors       string = "log-database-errors"
	ConstLogNameDbSlowquery    string = "log-database-slowquery"
	ConstLogNameDbOptimize     string = "log-database-optimize"
	ConstLogNameDbAuditlog     string = "log-database-auditlog"
	ConstLogNameDbSqlError     string = "log-database-sqlerrorlog"
)

/*
This is the list of task to be used in Task struct and Task table
*/
type TaskName string

const (
	ConstTaskXB                 TaskName = "xtrabackup"
	ConstTaskMB                 TaskName = "mariabackup"
	ConstTaskError              TaskName = "errorlog"
	ConstTaskSlowQuery          TaskName = "slowquery"
	ConstTaskSqlError           TaskName = "sqlerrorlog"
	ConstTaskAuditLog           TaskName = "auditlog"
	ConstTaskZFS                TaskName = "zfssnapback"
	ConstTaskOptimize           TaskName = "optimize"
	ConstTaskReseedXB           TaskName = "reseedxtrabackup"
	ConstTaskReseedMB           TaskName = "reseedmariabackup"
	ConstTaskDump               TaskName = "reseedmysqldump"
	ConstTaskFlashXB            TaskName = "flashbackxtrabackup"
	ConstTaskFlashMB            TaskName = "flashbackmariadbackup"
	ConstTaskFlashDump          TaskName = "flashbackmysqldump"
	ConstTaskStop               TaskName = "stop"
	ConstTaskRestart            TaskName = "restart"
	ConstTaskStart              TaskName = "start"
	ConstTaskPrintCurrentConfig TaskName = "printdefault-current"
	ConstTaskPrintDummyConfig   TaskName = "printdefault-dummy"
	ConstTaskJobsCheck          TaskName = "jobs-check"
	ConstTaskJobsUpgrade        TaskName = "jobs-upgrade"
)

/*
This is the list of graphite template
*/
const (
	ConstGraphiteTemplateNone    = "none"
	ConstGraphiteTemplateMinimal = "minimal"
	ConstGraphiteTemplateGrafana = "grafana"
	ConstGraphiteTemplateAll     = "all"
)

/*
This is the list of topology
*/
const (
	TopoMasterSlave         string = "master-slave"
	TopoUnknown             string = ""
	TopoBinlogServer        string = "binlog-server"
	TopoMultiTierSlave      string = "multi-tier-slave"
	TopoMultiMaster         string = "multi-master"
	TopoMultiMasterRing     string = "multi-master-ring"
	TopoMultiMasterWsrep    string = "multi-master-wsrep"
	TopoMultiMasterGrouprep string = "multi-master-grprep"
	TopoMasterSlavePgLog    string = "master-slave-pg-logical"
	TopoMasterSlavePgStream string = "master-slave-pg-stream"
	TopoActivePassive       string = "active-passive"
)

func (conf *Config) GetSecrets() map[string]Secret {
	// to store the flags to encrypt in the git (in Save() function)
	return conf.Secrets
}

func (conf *Config) DecryptSecretsFromConfig() {
	conf.Secrets = map[string]Secret{
		"api-credentials":                       {"", ""},
		"api-credentials-external":              {"", ""},
		"db-servers-credential":                 {"", ""},
		"monitoring-write-heartbeat-credential": {"", ""},
		"onpremise-ssh-credential":              {"", ""},
		"replication-credential":                {"", ""},
		"shardproxy-credential":                 {"", ""},
		"haproxy-password":                      {"", ""},
		"maxscale-pass":                         {"", ""},
		"myproxy-password":                      {"", ""},
		"proxysql-password":                     {"", ""},
		"proxyjanitor-password":                 {"", ""},
		"vault-secret-id":                       {"", ""},
		"opensvc-p12-secret":                    {"", ""},
		"backup-restic-aws-access-secret":       {"", ""},
		"backup-streaming-aws-access-secret":    {"", ""},
		"backup-restic-password":                {"", ""},
		"arbitration-external-secret":           {"", ""},
		"alert-pushover-user-token":             {"", ""},
		"alert-pushover-app-token":              {"", ""},
		"git-acces-token":                       {"", ""},
		"mail-smtp-password":                    {"", ""},
		"cloud18-gitlab-password":               {"", ""},
		"cloud18-dba-user-credentials":          {"", ""},
		"cloud18-sponsor-user-credentials":      {"", ""},
		"cloud18-domain-secret":                 {"", ""},
		"vault-token":                           {"", ""},
		"api-oauth-client-secret":               {"", ""},
		"meet-token":                            {"", ""}}

	for k := range conf.Secrets {

		origin_value, ok := conf.DynamicFlagMap[k]
		if !ok {
			origin_value, ok = conf.ImmuableFlagMap[k]
			if !ok {
				origin_value = conf.DefaultFlagMap[k]
			}

		}
		var secret Secret
		secret.Value = fmt.Sprintf("%v", origin_value)

		/* Decrypt feature not managed within log modules config due to risk of credentials leak */
		if conf.LogSecrets {
			log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("DecryptSecretsFromConfig: %s", secret.Value)
		}

		lst_cred := strings.Split(secret.Value, ",")
		var tab_cred []string
		for _, cred := range lst_cred {
			if strings.Contains(cred, ":") {
				user, pass := misc.SplitPair(cred)
				tab_cred = append(tab_cred, user+":"+conf.GetDecryptedPassword(k, pass))
			} else {
				if len(cred) > 1 {
					tab_cred = append(tab_cred, conf.GetDecryptedPassword(k, cred))
				} else {
					//Show warnings on empty credentials
					if conf.IsEligibleForPrinting(ConstLogModConfigLoad, LvlWarn) {
						log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Warnf("Empty credential do not decrypt key: %s", k)
					}
				}
			}
		}
		secret.Value = strings.Join(tab_cred, ",")
		//log.Printf("Decrypting secret variable %s=%s", k, secret.Value)
		conf.Secrets[k] = secret
	}
}

func (conf *Config) GetVaultCredentials(client *vault.Client, path string, key string) (string, error) {
	if conf.IsVaultUsed() && conf.IsPath(path) {
		if conf.VaultMode == VaultConfigStoreV2 {
			secret, err := client.KVv2(conf.VaultMount).Get(context.Background(), path)

			if err != nil {
				return "", err
			}
			return secret.Data[key].(string), nil
		} else {
			secret, err := client.KVv1("").Get(context.Background(), path)
			if err != nil {
				return "", err
			}
			return secret.Data["username"].(string) + ":" + secret.Data["password"].(string), nil
		}
	}
	return "", errors.New("Failed to get vault credentials")
}

func (conf *Config) DecryptSecretsFromVault() {
	for k, v := range conf.Secrets {
		origin_value := v.Value
		var secret Secret
		secret.Value = fmt.Sprintf("%v", origin_value)
		if conf.IsVaultUsed() && conf.IsPath(secret.Value) {
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Decrypting all the secret variables on Vault")
			vault_config := vault.DefaultConfig()
			vault_config.Address = conf.VaultServerAddr
			client, err := conf.GetVaultConnection()
			if err == nil {
				if conf.VaultMode == VaultConfigStoreV2 {
					vault_value, err := conf.GetVaultCredentials(client, secret.Value, k)
					if err != nil {
						log.Printf("Unable to get %s Vault secret: %v", k, err)
					} else if vault_value != "" {
						secret.Value = vault_value
					}
				}
			} else {
				log.Printf("Unable to initialize AppRole auth method: %v", err)
			}
			conf.Secrets[k] = secret
		}
	}
}

func (conf *Config) GetVaultConnection() (*vault.Client, error) {
	if conf.IsVaultUsed() {
		log.Printf("Vault AppRole Authentification")
		config := vault.DefaultConfig()

		config.Address = conf.VaultServerAddr

		client, err := vault.NewClient(config)
		if err != nil {
			log.Printf("Unable to initialize AppRole auth method: %v", err)
			return nil, err
		}

		roleID := conf.VaultRoleId
		secretID := &auth.SecretID{FromString: conf.GetDecryptedPassword("vault-secret-id", conf.VaultSecretId)}
		if roleID == "" || secretID == nil {
			log.Printf("Unable to initialize AppRole auth method: %v", err)
			return nil, err
		}

		appRoleAuth, err := auth.NewAppRoleAuth(
			roleID,
			secretID,
		)
		if err != nil {
			log.Printf("Unable to initialize AppRole auth method: %v", err)
			return nil, err
		}

		authInfo, err := client.Auth().Login(context.Background(), appRoleAuth)
		if err != nil {
			log.Printf("Unable to initialize AppRole auth method: %v", err)
			return nil, err
		}
		if authInfo == nil {
			log.Printf("Unable to initialize AppRole auth method: %v", err)
			return nil, err
		}
		return client, err
	}
	return nil, errors.New("Not using Vault")
}

func (conf *Config) GetDecryptedPassword(key string, value string) string {

	if conf.SecretKey != nil && strings.HasPrefix(value, "hash_") {
		value = strings.TrimPrefix(value, "hash_")
		p := crypto.Password{Key: conf.SecretKey}
		if conf.IsEligibleForPrinting(ConstLogModConfigLoad, LvlDbg) {
			log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Debugf("GetDecryptedPassword: decrypting key `%s`: %s", key, value)
		}

		if value != "" {
			p.CipherText = value
			err := p.Decrypt()
			if err != nil {
				return value
			} else {
				value = p.PlainText
				return value
			}
		}
	}
	return value
}

func (conf *Config) Reveal(clusterName string, tmpDir string) {
	fileName := fmt.Sprintf("%s/%s.reveal", tmpDir, clusterName)
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Printf("Erreur lors de la création du fichier %s: %v\n", fileName, err)
		return
	}
	defer file.Close()

	// Utiliser la réflexion pour parcourir les champs de Config
	val := reflect.ValueOf(conf).Elem()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		key := val.Type().Field(i).Name

		if field.Kind() == reflect.String && strings.HasPrefix(field.String(), "hash_") {
			decryptedValue := conf.GetDecryptedPassword(key, field.String())
			fmt.Fprintf(file, "Key: %s, Decrypted Value: %s\n", key, decryptedValue)
		}
	}

	// Lecture et affichage du contenu du fichier
	readAndPrintFile(fileName)
}

func readAndPrintFile(fileName string) {
	content, err := os.ReadFile(fileName)
	if err != nil {
		fmt.Printf("Erreur lors de la lecture du fichier %s: %v\n", fileName, err)
		return
	}
	fmt.Printf("Contenu de %s:\n%s\n", fileName, string(content))
}

func (conf *Config) IsPath(str string) bool {

	if strings.Contains(str, "=") || strings.Contains(str, "+") {
		return false
	}
	return strings.Contains(str, "/")
}

func (conf *Config) IsVaultUsed() bool {
	if conf.VaultServerAddr == "" {
		return false
	}
	return true
}

func (conf *Config) GenerateKey(Logger *logrus.Logger) error {
	_, err := os.Stat(conf.MonitoringKeyPath)
	// Check if the file does not exist
	if err == nil {
		Logger.Debugf("Repman discovered that key is already generated. Using existing key.")
		return nil
	} else {
		if !os.IsNotExist(err) {
			Logger.Errorf("Error when checking key for encryption: %v", err)
			return err
		}

		fallbackDir := conf.ConfDirExtra
		if fallbackDir == "" {
			if homeDir, homeErr := os.UserHomeDir(); homeErr == nil {
				fallbackDir = filepath.Join(homeDir, ".config", "replication-manager")
			}
		}
		fallbackPath := ""
		if fallbackDir != "" {
			fallbackPath = filepath.Join(fallbackDir, ".replication-manager.key")
			Logger.Debugf("Key not found. Checking in extra path : %s", fallbackPath)

			_, err = os.Stat(fallbackPath)
			if err == nil {
				Logger.Debugf("Repman discovered key in alternative path. Using existing key on %s", fallbackPath)
				conf.MonitoringKeyPath = fallbackPath
				return nil
			}
		}

		Logger.Debugf("Key not found. Generating : %s", conf.MonitoringKeyPath)

		if err = misc.TryOpenFile(conf.MonitoringKeyPath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil && conf.WithEmbed == "OFF" {
			if fallbackPath == "" {
				Logger.Errorf("File %s is not accessible and no fallback path is available", conf.MonitoringKeyPath)
				return err
			}

			Logger.Debugf("File %s is not accessible. Try using alternative path: %s", conf.MonitoringKeyPath, fallbackPath)

			_, err := os.Stat(fallbackPath)
			if err == nil {
				Logger.Infof("Repman discovered key in alternative path. Using existing key on %s", fallbackPath)
				conf.MonitoringKeyPath = fallbackPath
				return nil
			}

			_, err = os.Stat(fallbackDir)
			if err != nil {
				if !os.IsNotExist(err) {
					Logger.Errorf("Can't access %s : %v", fallbackDir, err)
					return err
				}
				err = os.MkdirAll(fallbackDir, 0755)
				if err != nil {
					Logger.Errorf("Can't create directory %s : %v", fallbackDir, err)
					return err
				}
			}

			if err := misc.TryOpenFile(fallbackPath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil {
				Logger.Errorf("Can't write keys in %s : %v", fallbackDir, err)
				return err
			}

			// New path is writable
			conf.MonitoringKeyPath = fallbackPath
			Logger.Debugf("Path writable. Flag 'monitoring-key-path' set to: %s.", fallbackPath)
			Logger.Debugf("Generating key on: %s", conf.MonitoringKeyPath)
		}

		p := crypto.Password{}
		p.Key, err = crypto.Keygen()
		if err != nil {
			Logger.Errorf("Error when generating key for encryption: %v", err)
			return err
		}
		err = crypto.WriteKey(p.Key, conf.MonitoringKeyPath, false)
		if err != nil {
			Logger.Errorf("Error when writing key for encryption: %v", err)
			return err
		}
	}

	return nil
}

func (conf *Config) LoadEncrytionKey() ([]byte, error) {
	sec, err := crypto.ReadKey(conf.MonitoringKeyPath)
	if err != nil {
		conf.SecretKey = nil
	}
	conf.SecretKey = sec
	return conf.SecretKey, err
}

func (conf *Config) WriteKeyToWorkingDir() (string, error) {
	paths := strings.Split(conf.MonitoringKeyPath, "/")
	filename := paths[len(paths)-1]
	return filename, crypto.WriteKey(conf.SecretKey, conf.WorkingDir+"/"+filename, conf.MonitoringKeyPathGitOverwrite)
}

func (conf *Config) GetEncryptedString(str string) string {
	p := crypto.Password{PlainText: str}

	if conf.SecretKey == nil {
		conf.LoadEncrytionKey()
	}

	if conf.SecretKey != nil {
		p.Key = conf.SecretKey
		p.Encrypt()
		return "hash_" + p.CipherText
	}
	return str
}

func (conf *Config) GetDecryptedValue(key string) string {
	return conf.Secrets[key].Value
}

func (conf *Config) PrintSecret(value string) string {
	return masker.String(masker.MAddress, value)
}

func (conf *Config) CloneConfigFromGit(url string, user string, tok string, dir string) error {
	var err error
	var r *git.Repository
	var w *git.Worktree

	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}

	if conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
		log.Printf("Clone from git : url %s, tok %s, dir %s\n", url, conf.PrintSecret(tok), dir)
	}

	path := dir
	if _, err = os.Stat(path + "/.git"); err == nil {

		// We instantiate a new repository targeting the given path (the .git folder)
		r, err = git.PlainOpen(path)
		if err != nil {
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
				log.Errorf("Git error : cannot PlainOpen : %s", err)
			}
			return err
		}

		// Get the working directory for the repository
		w, err = r.Worktree()
		if err != nil {
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
				log.Errorf("Git error : cannot Worktree : %s", err)
			}
			return err
		}
		// Pull the latest changes from the origin remote and merge into the current branch
		//git_ex.Info("git pull origin")
		err = w.Pull(&git.PullOptions{
			RemoteName:   "origin",
			Auth:         auth,
			SingleBranch: true,
			//RemoteURL:    url,
			Force: true,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate && err != transport.ErrEmptyRemoteRepository {
			if err == transport.ErrRepositoryNotFound {
				conf.CreateGitlabProjects()
			} else {
				if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
					log.Errorf("Git error : cannot Pull : %s", err)
				}
			}
		}

	} else {
		// Clone the given repository to the given directory
		//git_ex.Info("git clone %s %s --recursive", url, path)

		_, err = git.PlainClone(path, false, &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
			Depth:             1,
		})

		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				conf.CreateGitlabProjects()
			}
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
				log.Errorf("Git error : cannot Clone %s repository : %s", url, err)
			}
		}
	}

	return err
}

func (conf *Config) PushConfigToGit(url string, tok string, user string, dir string, clusterList []string) error {

	if conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
		log.Debugf("Push to git : tok %s, dir %s, user %s, clustersList : %v\n", conf.PrintSecret(tok), dir, user, clusterList)
	}
	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}
	path := dir

	var r *git.Repository
	if _, err := os.Stat(path + "/.git"); os.IsNotExist(err) {
		r, err = git.PlainClone(path, false, &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
		})
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, &git.CloneOptions{
					URL:               url,
					RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
					Auth:              auth,
				})
				if err != nil {
					log.Errorf("Git error : cannot Clone %s repository : %s", url, err)
					return err
				}
			} else {
				if conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
					log.Errorf("Git error : cannot Clone %s repository : %s", url, err)
				}
				return err
			}
		}

		w, err := r.Worktree()
		if err != nil {
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
				log.Errorf("Git error : cannot Worktree : %s", err)
			}
			return err
		}

		// checkout and keep files
		w.Checkout(&git.CheckoutOptions{Keep: true})
	} else {
		r, err = git.PlainOpen(path)
		if err != nil {
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
				log.Errorf("Git error : cannot PlainOpen : %s", err)
			}
			return err
		}
	}

	w, err := r.Worktree()
	if err != nil {
		if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
			log.Errorf("Git error : cannot Worktree : %s", err)
		}
		return err
	}

	if len(clusterList) != 0 {
		for _, name := range clusterList {
			// Adds the new file to the staging area.
			err = w.AddGlob(name + "/*.toml")
			if err != nil {
				if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
					log.Errorf("Git error : cannot Add %s : %s", name+"/*.toml", err)
				}
			}

			if _, err := os.Stat(conf.WorkingDir + "/" + name + "/agents.json"); !os.IsNotExist(err) {
				_, err = w.Add(name + "/agents.json")
				if err != nil {
					if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
						log.Errorf("Git error : cannot Add %s : %s", name+"/agents.json", err)
					}
				}
				_, err = w.Add(name + "/queryrules.json")
				if err != nil {
					if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
						log.Errorf("Git error : cannot Add %s : %s", name+"/queryrules.json", err)
					}
				}
			}
		}
	}

	// cloud18.toml will be in pull repo
	// if _, err := os.Stat(conf.WorkingDir + "/.pull/cloud18.toml"); !os.IsNotExist(err) {
	// 	_, err = w.Add("cloud18.toml")
	// 	if err != nil {
	// 		if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
	// 			log.Errorf("Git error : cannot Add cloud18.toml : %s", err)
	// 		}
	// 	}
	// }

	if _, err := os.Stat(conf.WorkingDir + "/default.toml"); !os.IsNotExist(err) {
		_, err = w.Add("default.toml")
		if err != nil {
			if conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
				log.Errorf("Git error : cannot Add default.toml : %s", err)
			}
		}
	}

	msg := "Update file"

	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication-manager",
			When: time.Now(),
		},
		All: true,
	})

	if err != nil {
		log.Errorf("Git error : cannot Commit : %s", err)
		return err
	}

	err = w.Pull(&git.PullOptions{
		RemoteName: "origin",
		Auth:       auth,
		RemoteURL:  url,
		Force:      true,
	})

	if err != nil && fmt.Sprintf("%v", err) != "already up-to-date" && err != transport.ErrEmptyRemoteRepository {
		log.Errorf("Git error : cannot Pull %s repository : %s", url, err)
	}

	// push using default options
	err = r.Push(&git.PushOptions{Auth: auth, RemoteURL: url})
	if err != nil {
		log.Errorf("Git error : cannot Push : %s", err)
	}

	return err
}

// PullAndMergeWithConflictResolution pulls and merges changes, handling conflicts manually.
func ForcePullFromRepo(r *git.Repository, url string, auth *git_https.BasicAuth) error {
	// Fetch the changes from the remote repository
	err := r.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Auth:       auth,
		RemoteURL:  url,
		Force:      true,
	})
	if err != nil && err.Error() != "already up-to-date" {
		return fmt.Errorf("cannot fetch from repository %s: %w", url, err)
	}

	// Get the local and remote references
	localRef, err := r.Head()
	if err != nil {
		return fmt.Errorf("cannot get local HEAD reference: %w", err)
	}

	// Get the remote reference for the same branch (assuming you are pulling from the same branch)
	remoteRefName := plumbing.NewRemoteReferenceName("origin", localRef.Name().Short())
	remoteRef, err := r.Reference(remoteRefName, true)
	if err != nil {
		return fmt.Errorf("cannot get remote reference: %w", err)
	}

	w, _ := r.Worktree()
	if err = w.Reset(&git.ResetOptions{Commit: remoteRef.Hash(), Mode: git.HardReset}); err == nil {
		if err := w.Pull(&git.PullOptions{
			RemoteName: "origin",
			Auth:       auth,
			RemoteURL:  url,
			Force:      true,
		}); err != nil {
			fmt.Printf("git error: %s", err.Error())
		}
	}

	return nil
}

/*
	func (conf *Config) PullByGitCli() {
		// Store the initial directory path
		initialDir, err := os.Getwd()
		if err != nil {
			fmt.Println("Failed to get current directory:", err)
			return
		}
		// Change to the desired Git repository directory
		repoDir := conf.WorkingDir
		if err := os.Chdir(repoDir); err != nil {
			log.Errorf("Failed to change directory:", err)
			return
		}

		// Execute "git pull" command
		cmd := exec.Command("git", "pull", "-f")
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Errorf("Failed to execute 'git pull' command:", err)
			return
		}

		log.Infof("Git pull output:", string(output))

		log.Infof("Merge accepted successfully. %s", output)

		// Change back to the initial directory
		if err := os.Chdir(initialDir); err != nil {
			fmt.Println("Failed to change back to initial directory:", err)
			return
		}
	}
*/
func GetBackupBinlogType() map[string]bool {
	return map[string]bool{
		ConstBackupBinlogTypeMysqlbinlog: true,
		ConstBackupBinlogTypeSSH:         true,
		ConstBackupBinlogTypeScript:      true,
	}
}

func GetBinlogParseMode() map[string]bool {
	return map[string]bool{
		ConstBackupBinlogTypeMysqlbinlog: true,
		ConstBackupBinlogTypeGoMySQL:     true,
	}
}

func GetBackupPhysicalType() map[string]bool {
	return map[string]bool{
		ConstBackupPhysicalTypeXtrabackup:  true,
		ConstBackupPhysicalTypeMariaBackup: true,
	}
}

func GetBackupLogicalType() map[string]bool {
	return map[string]bool{
		ConstBackupLogicalTypeMysqldump: true,
		ConstBackupLogicalTypeMydumper:  true,
		ConstBackupLogicalTypeRiver:     false,
		ConstBackupLogicalTypeDumpling:  false,
	}
}

func (conf *Config) GetOrchestratorsProv() []ConfigVariableType {

	return []ConfigVariableType{
		ConfigVariableType{
			Id:        1,
			Name:      ConstOrchestratorOpenSVC,
			Available: strings.Contains(conf.ProvOrchestratorEnable, ConstOrchestratorOpenSVC),
			Label:     "",
		},
		ConfigVariableType{
			Id:        2,
			Name:      ConstOrchestratorKubernetes,
			Available: strings.Contains(conf.ProvOrchestratorEnable, ConstOrchestratorKubernetes),
			Label:     "",
		},
		ConfigVariableType{
			Id:        3,
			Name:      ConstOrchestratorSlapOS,
			Available: strings.Contains(conf.ProvOrchestratorEnable, ConstOrchestratorSlapOS),
			Label:     "",
		},
		ConfigVariableType{
			Id:        4,
			Name:      ConstOrchestratorLocalhost,
			Available: strings.Contains(conf.ProvOrchestratorEnable, ConstOrchestratorLocalhost),
			Label:     "",
		},
		ConfigVariableType{
			Id:        5,
			Name:      ConstOrchestratorOnPremise,
			Available: strings.Contains(conf.ProvOrchestratorEnable, ConstOrchestratorOnPremise),
			Label:     "",
		},
	}
}

func GetMonitorType() map[string]string {

	return map[string]string{
		"mariadb":    "database",
		"mysql":      "database",
		"percona":    "database",
		"postgresql": "database",
		"maxscale":   "proxy",
		"proxysql":   "proxy",
		"shardproxy": "proxy",
		"haproxy":    "proxy",
		"myproxy":    "proxy",
		"extproxy":   "proxy",
		"sphinx":     "proxy",
		"app":        "app",
	}
}

func GetDiskType() map[string]string {

	return map[string]string{
		"loopback":  "loopback",
		"physical":  "physical",
		"pool":      "pool",
		"directory": "directory",
		"volume":    "volume",
	}
}

func GetFSType() map[string]bool {

	return map[string]bool{
		"ext4": true,
		"zfs":  true,
		"xfs":  true,
		"aufs": true,
		"nfs":  false,
	}
}

func GetSysbenchTests() map[string]bool {
	return map[string]bool{
		"oltp_read_write":       true,
		"oltp_read_only":        true,
		"oltp_update_non_index": true,
		"oltp_update_index":     true,
		"tpcc":                  true,
	}
}

func GetVMType() map[string]bool {

	return map[string]bool{
		"package": false,
		"docker":  true,
		"podman":  true,
		"oci":     true,
		"kvm":     false,
		"zone":    false,
		"lxc":     false,
	}
}

func GetPoolType() map[string]bool {

	return map[string]bool{
		"none":  true,
		"zpool": true,
		"lvm":   true,
	}
}

func GetSSLMode() map[string]bool {
	return map[string]bool{
		"DISABLED":        true,
		"PREFERRED":       true,
		"REQUIRED":        true,
		"VERIFY_CA":       true,
		"VERIFY_IDENTITY": true,
	}
}

func GetTopologyType() map[string]string {
	return map[string]string{
		TopoMasterSlave:         TopoMasterSlave,
		TopoBinlogServer:        TopoBinlogServer,
		TopoMultiTierSlave:      TopoMultiTierSlave,
		TopoMultiMaster:         TopoMultiMaster,
		TopoMultiMasterRing:     TopoMultiMasterRing,
		TopoMultiMasterWsrep:    TopoMultiMasterWsrep,
		TopoMultiMasterGrouprep: TopoMultiMasterGrouprep,
		TopoMasterSlavePgLog:    TopoMasterSlavePgLog,
		TopoMasterSlavePgStream: TopoMasterSlavePgStream,
		TopoActivePassive:       TopoActivePassive,
		TopoUnknown:             TopoUnknown,
	}
}

func (conf *Config) GetMemoryPctShared() (map[string]int, error) {
	engines := make(map[string]int)
	tblengine := strings.Split(conf.ProvMemSharedPct, ",")
	for _, engine := range tblengine {
		keyval := strings.Split(engine, ":")
		val, err := strconv.Atoi(keyval[1])

		if err != nil {
			return engines, err
		}
		//		log.Printf("%s", keyval[1])
		engines[keyval[0]] = val
	}
	return engines, nil
}

func (conf *Config) GetMemoryPctThreaded() (map[string]int, error) {
	engines := make(map[string]int)
	tblengine := strings.Split(conf.ProvMemThreadedPct, ",")
	for _, engine := range tblengine {
		keyval := strings.Split(engine, ":")
		val, err := strconv.Atoi(keyval[1])
		if err != nil {
			return engines, err
		}
		engines[keyval[0]] = val
	}
	return engines, nil
}

func GetGrantType() map[string]string {
	return map[string]string{
		GrantDBStart:                   GrantDBStart,
		GrantDBStop:                    GrantDBStop,
		GrantDBKill:                    GrantDBKill,
		GrantDBOptimize:                GrantDBOptimize,
		GrantDBAnalyse:                 GrantDBAnalyse,
		GrantDBReplication:             GrantDBReplication,
		GrantDBBackup:                  GrantDBBackup,
		GrantDBRestore:                 GrantDBRestore,
		GrantDBReadOnly:                GrantDBReadOnly,
		GrantDBLogs:                    GrantDBLogs,
		GrantDBCapture:                 GrantDBCapture,
		GrantDBMaintenance:             GrantDBMaintenance,
		GrantDBConfigCreate:            GrantDBConfigCreate,
		GrantDBConfigRessource:         GrantDBConfigRessource,
		GrantDBConfigFlag:              GrantDBConfigFlag,
		GrantDBConfigGet:               GrantDBConfigGet,
		GrantDBShowVariables:           GrantDBShowVariables,
		GrantDBShowStatus:              GrantDBShowStatus,
		GrantDBShowSchema:              GrantDBShowSchema,
		GrantDBShowProcess:             GrantDBShowProcess,
		GrantDBShowLogs:                GrantDBShowLogs,
		GrantClusterCreate:             GrantClusterCreate,
		GrantClusterDelete:             GrantClusterDelete,
		GrantClusterCreateMonitor:      GrantClusterCreateMonitor,
		GrantClusterDropMonitor:        GrantClusterDropMonitor,
		GrantClusterFailover:           GrantClusterFailover,
		GrantClusterSwitchover:         GrantClusterSwitchover,
		GrantClusterRolling:            GrantClusterRolling,
		GrantClusterSettings:           GrantClusterSettings,
		GrantClusterGrant:              GrantClusterGrant,
		GrantClusterReplication:        GrantClusterReplication,
		GrantClusterAnalyze:            GrantClusterAnalyze,
		GrantClusterChecksum:           GrantClusterChecksum,
		GrantClusterSharding:           GrantClusterSharding,
		GrantClusterCertificatesRotate: GrantClusterCertificatesRotate,
		GrantClusterCertificatesReload: GrantClusterCertificatesReload,
		GrantClusterBench:              GrantClusterBench,
		GrantClusterTest:               GrantClusterTest,
		GrantClusterTraffic:            GrantClusterTraffic,
		GrantClusterProcess:            GrantClusterProcess,
		GrantClusterDebug:              GrantClusterDebug,
		GrantClusterShowBackups:        GrantClusterShowBackups,
		GrantClusterShowAgents:         GrantClusterShowAgents,
		GrantClusterShowGraphs:         GrantClusterShowGraphs,
		GrantClusterConfigGraphs:       GrantClusterConfigGraphs,
		GrantClusterShowRoutes:         GrantClusterShowRoutes,
		GrantClusterShowCertificates:   GrantClusterShowCertificates,
		GrantClusterResetSLA:           GrantClusterResetSLA,
		GrantClusterRotatePasswords:    GrantClusterRotatePasswords,
		GrantClusterStaging:            GrantClusterStaging,
		GrantClusterAlert:              GrantClusterAlert,
		GrantClusterDocker:             GrantClusterDocker,
		GrantProxyConfigCreate:         GrantProxyConfigCreate,
		GrantProxyConfigGet:            GrantProxyConfigGet,
		GrantProxyConfigRessource:      GrantProxyConfigRessource,
		GrantProxyConfigFlag:           GrantProxyConfigFlag,
		GrantProxyStart:                GrantProxyStart,
		GrantProxyStop:                 GrantProxyStop,
		GrantProvSettings:              GrantProvSettings,
		GrantProvCluster:               GrantProvCluster,
		GrantProvClusterProvision:      GrantProvClusterProvision,
		GrantProvClusterUnprovision:    GrantProvClusterUnprovision,
		GrantProvDBUnprovision:         GrantProvDBUnprovision,
		GrantProvDBProvision:           GrantProvDBProvision,
		GrantProvProxyProvision:        GrantProvProxyProvision,
		GrantProvProxyUnprovision:      GrantProvProxyUnprovision,
		GrantAppConfig:                 GrantAppConfig,
		GrantAppDocker:                 GrantAppDocker,
		GrantAppDeployment:             GrantAppDeployment,
		GrantAppStart:                  GrantAppStart,
		GrantAppStop:                   GrantAppStop,
		GrantAppGit:                    GrantAppGit,
		GrantGlobalGrant:               GrantGlobalGrant,
		GrantGlobalSettings:            GrantGlobalSettings,
		GrantSalesValidate:             GrantSalesValidate,
		GrantSalesRefuse:               GrantSalesRefuse,
		GrantSalesUnsubscribe:          GrantSalesUnsubscribe,
		GrantExternalRole:              GrantExternalRole,
		GrantGrantShow:                 GrantGrantShow,
		GrantGrantAdd:                  GrantGrantAdd,
		GrantGrantModify:               GrantGrantModify,
		GrantGrantDrop:                 GrantGrantDrop,
		GrantGrantGlobal:               GrantGrantGlobal,
		GrantShow:                      GrantShow,
		GrantTerminalDatabase:          GrantTerminalDatabase,
		GrantTerminalProxy:             GrantTerminalProxy,
		GrantTerminalGlobal:            GrantTerminalGlobal,
	}
}

func GetGrantDB() []string {
	return []string{
		GrantDBStart,
		GrantDBStop,
		GrantDBKill,
		GrantDBOptimize,
		GrantDBAnalyse,
		GrantDBReplication,
		GrantDBBackup,
		GrantDBRestore,
		GrantDBReadOnly,
		GrantDBLogs,
		GrantDBCapture,
		GrantDBMaintenance,
		GrantDBConfigCreate,
		GrantDBConfigRessource,
		GrantDBConfigFlag,
		GrantDBConfigGet,
		GrantDBShowVariables,
		GrantDBShowStatus,
		GrantDBShowSchema,
		GrantDBShowProcess,
		GrantDBShowLogs,
	}
}

func HasAllDBGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantDB() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantCluster() []string {
	return []string{
		GrantClusterCreate,
		GrantClusterDelete,
		GrantClusterCreateMonitor,
		GrantClusterDropMonitor,
		GrantClusterFailover,
		GrantClusterSwitchover,
		GrantClusterRolling,
		GrantClusterSettings,
		GrantClusterGrant,
		GrantClusterReplication,
		GrantClusterAnalyze,
		GrantClusterChecksum,
		GrantClusterSharding,
		GrantClusterCertificatesRotate,
		GrantClusterCertificatesReload,
		GrantClusterBench,
		GrantClusterTest,
		GrantClusterTraffic,
		GrantClusterProcess,
		GrantClusterDebug,
		GrantClusterShowBackups,
		GrantClusterShowAgents,
		GrantClusterShowGraphs,
		GrantClusterConfigGraphs,
		GrantClusterShowRoutes,
		GrantClusterShowCertificates,
		GrantClusterResetSLA,
		GrantClusterRotatePasswords,
		GrantClusterStaging,
		GrantClusterAlert,
		GrantClusterDocker,
	}
}

func HasAllClusterGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantCluster() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantProxy() []string {
	return []string{
		GrantProxyConfigCreate,
		GrantProxyConfigGet,
		GrantProxyConfigRessource,
		GrantProxyConfigFlag,
		GrantProxyStart,
		GrantProxyStop,
	}
}

func HasAllProxyGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantProxy() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantProvision() []string {
	return []string{
		GrantProvSettings,
		GrantProvCluster,
		GrantProvClusterProvision,
		GrantProvClusterUnprovision,
		GrantProvDBUnprovision,
		GrantProvDBProvision,
		GrantProvProxyProvision,
		GrantProvProxyUnprovision,
	}
}

func HasAllProvisionGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantProvision() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantApp() []string {
	return []string{
		GrantAppStart,
		GrantAppStop,
		GrantAppConfig,
		GrantAppDocker,
		GrantAppDeployment,
		GrantAppGit,
	}
}

func HasAllAppGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantApp() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantGlobal() []string {
	return []string{
		GrantGlobalGrant,
		GrantGlobalSettings,
	}
}

func HasAllGlobalGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantGlobal() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantSales() []string {
	return []string{
		GrantSalesValidate,
		GrantSalesRefuse,
		GrantSalesUnsubscribe,
	}
}

func HasAllSalesGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantSales() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantGrant() []string {
	return []string{
		GrantGrantShow,
		GrantGrantAdd,
		GrantGrantModify,
		GrantGrantDrop,
		GrantGrantGlobal,
	}
}

func HasAllGrantGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantGrant() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantTerminal() []string {
	return []string{
		GrantTerminalDatabase,
		GrantTerminalProxy,
		GrantTerminalGlobal,
	}
}

func HasAllTerminalGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantTerminal() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetCompactGrants(grants map[string]bool) ([]string, []string) {
	var compactGrants []string
	var compactDiscardGrants []string
	var counter int

	// DB
	tmp := make([]string, 0)
	if HasAllDBGrants(grants) {
		compactGrants = append(compactGrants, "db")
	} else {
		for _, grant := range GetGrantDB() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "db")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Cluster
	tmp = make([]string, 0)
	counter = 0
	if HasAllClusterGrants(grants) {
		compactGrants = append(compactGrants, "cluster")
	} else {
		for _, grant := range GetGrantCluster() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "cluster")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Proxy
	tmp = make([]string, 0)
	counter = 0
	if HasAllProxyGrants(grants) {
		compactGrants = append(compactGrants, "proxy")
	} else {
		for _, grant := range GetGrantProxy() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "proxy")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Provision
	tmp = make([]string, 0)
	counter = 0
	if HasAllProvisionGrants(grants) {
		compactGrants = append(compactGrants, "prov")
	} else {
		for _, grant := range GetGrantProvision() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "prov")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// App
	tmp = make([]string, 0)
	counter = 0
	if HasAllProvisionGrants(grants) {
		compactGrants = append(compactGrants, "app")
	} else {
		for _, grant := range GetGrantProvision() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "app")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Global
	tmp = make([]string, 0)
	counter = 0
	if HasAllGlobalGrants(grants) {
		compactGrants = append(compactGrants, "global")
	} else {
		for _, grant := range GetGrantGlobal() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "global")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Sales
	tmp = make([]string, 0)
	counter = 0
	if HasAllSalesGrants(grants) {
		compactGrants = append(compactGrants, "sales")
	} else {
		for _, grant := range GetGrantSales() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "sales")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Grant
	tmp = make([]string, 0)
	counter = 0
	if HasAllGrantGrants(grants) {
		compactGrants = append(compactGrants, "grant")
	} else {
		for _, grant := range GetGrantGrant() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "grant")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Grant
	tmp = make([]string, 0)
	counter = 0
	if HasAllTerminalGrants(grants) {
		compactGrants = append(compactGrants, "terminal")
	} else {
		for _, grant := range GetGrantTerminal() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "terminal")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	if grants["show"] {
		compactGrants = append(compactGrants, "show")
	} else {
		compactDiscardGrants = append(compactDiscardGrants, "show")
	}

	if grants["extrole"] {
		compactGrants = append(compactGrants, "extrole")
	} else {
		compactDiscardGrants = append(compactDiscardGrants, "extrole")
	}

	return compactGrants, compactDiscardGrants
}

func GetRoleType() map[string]string {
	return map[string]string{
		RoleSysOps:                RoleSysOps,
		RoleDBOps:                 RoleDBOps,
		RoleExtSysOps:             RoleExtSysOps,
		RoleExtDBOps:              RoleExtDBOps,
		RoleSponsor:               RoleSponsor,
		RolePending:               RolePending,
		RolePendingExtDBOps:       RolePendingExtDBOps,
		RolePendingExtSysOps:      RolePendingExtSysOps,
		RoleQuoteExtDBOps:         RoleQuoteExtDBOps,
		RoleQuoteExtSysOps:        RoleQuoteExtSysOps,
		RoleUnsubscribed:          RoleUnsubscribed,
		RoleUnsubscribedExtDBOps:  RoleUnsubscribedExtDBOps,
		RoleUnsubscribedExtSysOps: RoleUnsubscribedExtSysOps,
		RoleVisitor:               RoleVisitor,
	}
}

func GetCompactRoles(roles map[string]bool) []string {
	rolelist := make([]string, 0)
	for role, v := range roles {
		if v {
			rolelist = append(rolelist, role)
		}
	}

	return rolelist
}

func GetDefaultAllowDiscardACL(role string) (string, string) {
	switch role {
	case RoleSysOps:
		return "*", ""
	case RoleExtSysOps:
		return "*", "sales global extrole"
	case RoleDBOps:
		return "*", "cluster prov sales global"
	case RoleSponsor:
		return "db show proxy grant extrole sales-unsubscribe app", ""
	case RoleExtDBOps:
		return "db show proxy grant", "extrole"
	default:
		return "show", ""
	}
}

func GetDefaultGrants(role string) string {
	grants := make(map[string]bool)
	gtypes := GetGrantType()
	allow, discard := GetDefaultAllowDiscardACL(role)

	for _, grant := range gtypes {
		found := false

		if allow == "*" {
			found = true
		} else {
			for _, acl := range strings.Split(allow, " ") {
				if strings.HasPrefix(grant, acl) && acl != "" {
					found = true
					break
				}
			}
		}

		if discard != "" {
			for _, acl := range strings.Split(discard, " ") {
				if strings.HasPrefix(grant, acl) && acl != "" {
					found = false
					break
				}
			}
		}

		grants[grant] = found
	}

	allowlist, _ := GetCompactGrants(grants)

	return strings.Join(allowlist, " ")
}

func GetRoleDefaultGrant(roles string) string {
	list := strings.Split(roles, " ")
	if slices.Contains(list, RoleSysOps) {
		return GetDefaultGrants(RoleSysOps)
	} else if slices.Contains(list, RoleExtSysOps) {
		return GetDefaultGrants(RoleExtSysOps)
	} else if slices.Contains(list, RoleSponsor) {
		return GetDefaultGrants(RoleSponsor)
	} else if slices.Contains(list, RoleDBOps) {
		return GetDefaultGrants(RoleDBOps)
	} else if slices.Contains(list, RoleExtDBOps) {
		return GetDefaultGrants(RoleExtDBOps)
	}
	return ""
}

func (conf *Config) GetDockerRepos(file string, is_not_embed bool) ([]DockerRepo, error) {
	var repos DockerRepos
	var byteValue []byte
	if is_not_embed {
		jsonFile, err := os.Open(file)
		if err != nil {
			return repos.Repos, err
		}

		defer jsonFile.Close()
		byteValue, _ = io.ReadAll(jsonFile)
	} else {
		byteValue, _ = share.EmbededDbModuleFS.ReadFile("repo/repos.json")
	}

	err := json.Unmarshal([]byte(byteValue), &repos)
	if err != nil {
		return repos.Repos, err
	}

	return repos.Repos, nil
}

type Tarball struct {
	Name            string `json:"name"`
	Checksum        string `json:"checksum,omitempty"`
	OperatingSystem string `json:"OS"`
	Url             string `json:"url"`
	Flavor          string `json:"flavor"`
	Minimal         bool   `json:"minimal"`
	Size            int64  `json:"size"`
	ShortVersion    string `json:"short_version"`
	Version         string `json:"version"`
	UpdatedBy       string `json:"updated_by,omitempty"`
	Notes           string `json:"notes,omitempty"`
	DateAdded       string `json:"date_added,omitempty"`
}

type Tarballs struct {
	Tarballs []Tarball `json:"tarballs"`
}

func (conf *Config) GetTarballs(is_not_embed bool) ([]Tarball, error) {

	var tarballs Tarballs
	var byteValue []byte
	if is_not_embed {

		file := conf.ShareDir + "/repo/tarballs.json"
		log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("GetTarballs1 file value : %s ", file)
		jsonFile, err := os.Open(file)
		if err != nil {
			return tarballs.Tarballs, err
		}

		defer jsonFile.Close()
		byteValue, _ = io.ReadAll(jsonFile)
	} else {
		jsonFile, err := share.EmbededDbModuleFS.Open("repo/tarballs.json")
		if err != nil {
			return tarballs.Tarballs, err
		}
		byteValue, _ = io.ReadAll(jsonFile)
	}
	//byteValue, _ := io.ReadAll(jsonFile)

	err := json.Unmarshal([]byte(byteValue), &tarballs)
	if err != nil {
		return tarballs.Tarballs, err
	}

	return tarballs.Tarballs, nil
}

func (conf *Config) GetTarballUrl(name string) (string, error) {

	tarballs, _ := conf.GetTarballs(true)
	for _, tarball := range tarballs {
		if tarball.Name == name {
			return tarball.Url, nil
		}
	}
	return "", errors.New("tarball not found in collection")
}

func (conf Config) PrintConf() {
	values := reflect.ValueOf(conf)
	types := values.Type()
	log.Printf("PRINT CONF")
	for i := 0; i < values.NumField(); i++ {

		if types.Field(i).Type.String() == "string" {
			fmt.Printf("%s : %s (string)\n", types.Field(i).Name, values.Field(i).String())
		}
		if types.Field(i).Type.String() == "bool" {
			fmt.Printf("%s : %s (bool)\n", types.Field(i).Name, values.Field(i).String())
		}
		if types.Field(i).Type.String() == "int" || types.Field(i).Type.String() == "uint64" || types.Field(i).Type.String() == "int64" {
			fmt.Printf("%s : %s (int)\n", types.Field(i).Name, values.Field(i).String())
		}

	}
}

func (conf Config) MergeConfig(path string, name string, ImmMap map[string]interface{}, DefMap map[string]interface{}, confPath string) error {
	dynRead := viper.GetViper()
	viper.SetConfigName("overwrite")
	dynRead.SetConfigType("toml")

	dynMap := make(map[string]interface{})

	dirPath := path + "/" + name
	if strings.ToLower(name) == "default" {
		dirPath = path
	}

	if _, err := os.Stat(dirPath + "/overwrite.toml"); os.IsNotExist(err) {
		log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("No monitoring saved config found %s", dirPath+"/overwrite.toml")
		return err
	} else {
		log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("Parsing saved config from working directory %s", dirPath+"/overwrite.toml")
		dynRead.AddConfigPath(dirPath)
		err := dynRead.ReadInConfig()
		if err != nil {
			fmt.Printf("Could not read in config %s: %s", dirPath+"/overwrite.toml", err)
		}

		dynSub := dynRead.Sub("overwrite-" + name)
		if dynSub != nil {
			for _, f := range dynSub.AllKeys() {
				v := dynSub.Get(f)
				_, ok := ImmMap[f]
				if ok && v != nil && v != ImmMap[f] {
					_, ok := DefMap[f]
					if ok && v != DefMap[f] {
						dynMap[f] = dynSub.Get(f)
					}
					if !ok {
						dynMap[f] = dynSub.Get(f)
					}
				}
			}
		}
	}
	//fmt.Printf("%v\n", DefMap)
	//fmt.Printf("%v\n", dynMap)
	//fmt.Printf("%v\n", ImmMap)
	conf.WriteMergeConfig(confPath, dynMap)
	return nil
}

func (conf Config) WriteMergeConfig(confPath string, dynMap map[string]interface{}) error {
	input, err := os.ReadFile(confPath)
	if err != nil {
		fmt.Printf("Cannot read config file %s : %s", confPath, err)
		return err
	}

	lines := strings.Split(string(input), "\n")

	for i, line := range lines {
		for k, v := range dynMap {
			tmp := strings.Split(line, "=")
			tmp[0] = strings.ReplaceAll(tmp[0], " ", "")
			if tmp[0] == k {
				//fmt.Printf("Write Merge Conf : line %s, k %s, v %v\n", line, k, v)
				switch v.(type) {
				case string:
					lines[i] = k + " = " + fmt.Sprintf("\"%v\"", v)
				default:
					lines[i] = k + " = " + fmt.Sprintf("%v", v)
				}

			}
		}

	}
	output := strings.Join(lines, "\n")
	err = os.WriteFile(confPath, []byte(output), 0644)
	if err != nil {
		fmt.Printf("Cannot write config file %s : %s", confPath, err)
		return err
	}
	return nil
}

type ConfigAttr struct {
	Key   string
	Toml  string
	Type  string
	Value any
}

func (conf *Config) GetConfigurationByScope(scope string) map[string]ConfigAttr {
	var attrs map[string]ConfigAttr = make(map[string]ConfigAttr)

	to := reflect.TypeOf(conf)
	vo := reflect.ValueOf(conf)
	for i := 0; i < to.NumField(); i++ {
		f := to.Field(i)
		v := vo.Field(i).Interface()
		if f.Tag.Get("scope") == "server" {
			attrs[f.Name] = ConfigAttr{
				Key:   f.Name,
				Toml:  f.Tag.Get("toml"),
				Type:  f.Type.Name(),
				Value: v,
			}
		}
	}

	return attrs
}

func GetScope(conf Config, toml string) (string, bool) {
	to := reflect.TypeOf(conf)
	for i := 0; i < to.NumField(); i++ {
		f := to.Field(i)
		if f.Tag.Get("toml") == toml {
			return f.Tag.Get("scope"), true
		}
	}

	return "", false
}

func GetParamsByScope(scopeFilter string) map[string]bool {
	conf := Config{}
	to := reflect.TypeOf(conf)
	var params map[string]bool = make(map[string]bool)

	for i := 0; i < to.NumField(); i++ {
		f := to.Field(i)
		if f.Tag.Get("scope") == scopeFilter {
			params[f.Tag.Get("toml")] = true
		}
	}

	return params
}

func IsScope(toml string, scope string) bool {
	tconfig := Config{}
	if tscope, ok := GetScope(tconfig, toml); ok {
		return tscope == scope
	}
	return false
}

func (conf *Config) ReadCloud18Config(v *viper.Viper, path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if v == nil {
		return
	}

	subViper := v.Sub("default")
	if subViper == nil {
		subViper = viper.New()
	}
	subViper.SetConfigType("toml")

	fmt.Printf("Parsing saved config from working directory %s ", path)

	subViper.SetConfigFile(path)
	err := subViper.MergeInConfig()
	if err != nil {
		log.Error("Config error in " + path + ":" + err.Error())
	}

	subViper.Unmarshal(&conf)

}

func (conf *Config) IsEligibleForPrinting(module int, level string) bool {
	//Always print state
	if level == "STATE" {
		return true
	}
	var lvl int
	lvl = 0
	switch level {
	case "ERROR", "ALERT":
		lvl = NumLvlError
		break
	case "WARN", "START":
		lvl = NumLvlWarn
		break
	case "INFO", "TEST", "BENCH":
		lvl = NumLvlInfo
		break
	case "DEBUG":
		lvl = NumLvlDebug
		break
	}

	if lvl > 0 {
		switch {
		case module == ConstLogModGeneral:
			return conf.LogLevel >= lvl
		case module == ConstLogModWriterElection:
			if conf.LogWriterElection {
				return conf.LogWriterElectionLevel >= lvl
			}
		case module == ConstLogModSST:
			if conf.LogSST {
				return conf.LogSSTLevel >= lvl
			}
		case module == ConstLogModHeartBeat:
			if conf.LogHeartbeat {
				return conf.LogHeartbeatLevel >= lvl
			}
		case module == ConstLogModConfigLoad:
			if conf.LogConfigLoad {
				return conf.LogConfigLoadLevel >= lvl
			}
		case module == ConstLogModGit:
			if conf.LogGit {
				return conf.LogGitLevel >= lvl
			}
		case module == ConstLogModBackupStream:
			if conf.LogBackupStream {
				return conf.LogBackupStreamLevel >= lvl
			}
		case module == ConstLogModOrchestrator:
			if conf.LogOrchestrator {
				return conf.LogOrchestratorLevel >= lvl
			}
		case module == ConstLogModVault:
			if conf.LogVault {
				return conf.LogVaultLevel >= lvl
			}
		case module == ConstLogModTopology:
			if conf.LogTopology {
				return conf.LogTopologyLevel >= lvl
			}
		case module == ConstLogModProxy:
			if conf.LogProxy {
				return conf.LogProxyLevel >= lvl
			}
		case module == ConstLogModProxySQL:
			if conf.ProxysqlDebug {
				return conf.ProxysqlLogLevel >= lvl
			}
		case module == ConstLogModHAProxy:
			if conf.HaproxyDebug {
				return conf.HaproxyLogLevel >= lvl
			}
		case module == ConstLogModProxyJanitor:
			if conf.ProxyJanitorDebug {
				return conf.ProxyJanitorLogLevel >= lvl
			}
		case module == ConstLogModMaxscale:
			if conf.MxsDebug {
				return conf.MxsLogLevel >= lvl
			}
		case module == ConstLogModGraphite:
			if conf.LogGraphite {
				return conf.LogGraphiteLevel >= lvl
			}
		case module == ConstLogModPurge:
			if conf.LogBinlogPurge {
				return conf.LogBinlogPurgeLevel >= lvl
			}
		case module == ConstLogModTask:
			if conf.LogTask {
				return conf.LogTaskLevel >= lvl
			}
		case module == ConstLogModExternalScript:
			if conf.LogExternalScript {
				return conf.LogExternalScriptLevel >= lvl
			}
		case module == ConstLogModRestic:
			return conf.LogResticLevel >= lvl
		case module == ConstLogModMailer:
			return conf.LogMailerLevel >= lvl
		case module == ConstLogModSupport:
			return conf.LogSupportLevel >= lvl
		case module == ConstLogModStats:
			return conf.LogStatsLevel >= lvl
		case module == ConstLogModSQL:
			return conf.LogSQLLevel >= lvl
		case module == ConstLogModApp:
			return conf.LogAppLevel >= lvl
		case module == ConstLogModDbErrors:
			return conf.LogLevelDatabaseErrors >= lvl
		case module == ConstLogModDbSlowquery:
			return conf.LogLevelDatabaseSlowquery >= lvl
		case module == ConstLogModDbOptimize:
			return conf.LogLevelDatabaseOptimize >= lvl
		case module == ConstLogModDbAudit:
			return conf.LogLevelDatabaseAudit >= lvl
		case module == ConstLogModDbSqlErrors:
			return conf.LogLevelDatabaseSqlErrors >= lvl
		}
	}

	return false
}

func (conf *Config) SetLogOutput(out io.Writer) {
	log.SetOutput(out)
}

func ToLogrusLevel(l int) log.Level {
	if l > 4 {
		l = 4
	}
	switch l {
	case 2:
		return log.WarnLevel
	case 3:
		return log.InfoLevel
	case 4:
		return log.DebugLevel
	}
	//Always return at least error level to make sure Logger not exit
	return log.ErrorLevel
}

func (conf *Config) GetGraphiteTemplateList() map[string]bool {
	return map[string]bool{
		ConstGraphiteTemplateNone:    true,
		ConstGraphiteTemplateMinimal: true,
		ConstGraphiteTemplateGrafana: true,
		ConstGraphiteTemplateAll:     true,
	}
}

type JobResult struct {
	Xtrabackup            bool `json:"xtrabackup"`
	Mariabackup           bool `json:"mariabackup"`
	Zfssnapback           bool `json:"zfssnapback"`
	Optimize              bool `json:"optimize"`
	Reseedxtrabackup      bool `json:"reseedxtrabackup"`
	Reseedmariabackup     bool `json:"reseedmariabackup"`
	Flashbackxtrabackup   bool `json:"flashbackxtrabackup"`
	Flashbackmariadbackup bool `json:"flashbackmariadbackup"`
	Stop                  bool `json:"stop"`
	Start                 bool `json:"start"`
	Restart               bool `json:"restart"`
}

type Task struct {
	Id      int64  `json:"id" db:"id"`
	Task    string `json:"task" db:"task"`
	Port    int    `json:"port" db:"port"`
	Server  string `json:"server" db:"server"`
	Done    int    `json:"done" db:"done"`
	State   int    `json:"state" db:"state"`
	Result  string `json:"result,omitempty" db:"result"`
	Payload string `json:"payload,omitempty" db:"payload"`
	Start   int64  `json:"start" db:"utc_start"`
	End     int64  `json:"end,omitempty" db:"utc_end"`
}

func (t *Task) Set(nt Task) {
	t.Id = nt.Id
	t.Task = nt.Task
	t.Port = nt.Port
	t.Server = nt.Server
	t.Done = nt.Done
	t.State = nt.State
	t.Result = nt.Result
	t.Payload = nt.Payload
	t.Start = nt.Start
	t.End = nt.End
}

type TaskSorter []Task

func (a TaskSorter) Len() int           { return len(a) }
func (a TaskSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TaskSorter) Less(i, j int) bool { return a[i].Task < a[j].Task }

func GetLabels(v any) []string {
	t := reflect.TypeOf(v)
	labels := make([]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			labels[i] = jsonTag
		} else {
			labels[i] = field.Name
		}
	}
	return labels
}

func GetLabelsAsMap(v any) map[string]bool {
	t := reflect.TypeOf(v)
	labels := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if len(parts) > 1 {
				jsonTag = parts[0]
			}
			labels[jsonTag] = true
		} else {
			labels[field.Name] = true
		}
	}
	return labels
}

type ServerTaskList struct {
	ServerURL string `json:"serverUrl"`
	Tasks     []Task `json:"tasks"`
}

type JobEntries struct {
	Header  map[string]bool           `json:"header"`
	Servers map[string]ServerTaskList `json:"servers"`
}

func GetTagsForLog(module int) string {
	switch module {
	case ConstLogModGeneral:
		return "general"
	case ConstLogModWriterElection:
		return "election"
	case ConstLogModSST:
		return "sst"
	case ConstLogModHeartBeat:
		return "heartbeat"
	case ConstLogModConfigLoad:
		return "conf"
	case ConstLogModGit:
		return "git"
	case ConstLogModSupport:
		return "support"
	case ConstLogModBackupStream:
		return "backup"
	case ConstLogModOrchestrator:
		return "orchestrator"
	case ConstLogModVault:
		return "vault"
	case ConstLogModTopology:
		return "topology"
	case ConstLogModProxy:
		return "proxy"
	case ConstLogModProxySQL:
		return "proxysql"
	case ConstLogModHAProxy:
		return "haproxy"
	case ConstLogModProxyJanitor:
		return "prxjanitor"
	case ConstLogModMaxscale:
		return "maxscale"
	case ConstLogModGraphite:
		return "graphite"
	case ConstLogModPurge:
		return "purge"
	case ConstLogModTask:
		return "job"
	case ConstLogModExternalScript:
		return "externalscript"
	case ConstLogModStats:
		return "stats"
	case ConstLogModSQL:
		return "sql"
	case ConstLogModApp:
		return "app"
	case ConstLogModRestic:
		return "restic"
	case ConstLogModMailer:
		return "mailer"
	case ConstLogModDbErrors:
		return "errorlog"
	case ConstLogModDbSlowquery:
		return "slowquery"
	case ConstLogModDbOptimize:
		return "optimize"
	case ConstLogModDbAudit:
		return "auditlog"
	}
	return ""
}

// If task is about backup and reseed, it will use log backup stream else will use log task
func GetModuleNameForTask(task string) string {
	// check if input compatible with TaskName type

	switch TaskName(task) {
	case ConstTaskXB, ConstTaskMB, ConstTaskReseedXB, ConstTaskReseedMB, ConstTaskDump, ConstTaskFlashXB, ConstTaskFlashMB, ConstTaskFlashDump:
		return ConstLogNameBackupStream
	case ConstTaskError:
		return ConstLogNameDbErrors
	case ConstTaskSlowQuery:
		return ConstLogNameDbSlowquery
	default:
		return ConstLogNameTask
	}
}

func GetIndexFromModuleName(module string) int {
	switch module {
	case ConstLogNameGeneral:
		return ConstLogModGeneral
	case ConstLogNameWriterelection:
		return ConstLogModWriterElection
	case ConstLogNameSST:
		return ConstLogModSST
	case ConstLogNameHeartBeat:
		return ConstLogModHeartBeat
	case ConstLogNameConfigLoad:
		return ConstLogModConfigLoad
	case ConstLogNameGit:
		return ConstLogModGit
	case ConstLogNameBackupStream:
		return ConstLogModBackupStream
	case ConstLogNameOrchestrator:
		return ConstLogModOrchestrator
	case ConstLogNameVault:
		return ConstLogModVault
	case ConstLogNameTopology:
		return ConstLogModTopology
	case ConstLogNameProxy:
		return ConstLogModProxy
	case ConstLogNameProxySQL:
		return ConstLogModProxySQL
	case ConstLogNameHAProxy:
		return ConstLogModHAProxy
	case ConstLogNameProxyJanitor:
		return ConstLogModProxyJanitor
	case ConstLogNameMaxscale:
		return ConstLogModMaxscale
	case ConstLogNameGraphite:
		return ConstLogModGraphite
	case ConstLogNamePurge:
		return ConstLogModPurge
	case ConstLogNameTask:
		return ConstLogModTask
	case ConstLogNameExternalScript:
		return ConstLogModExternalScript
	case ConstLogNameStats:
		return ConstLogModStats
	case ConstLogNameLogSQL:
		return ConstLogModSQL
	case ConstLogNameApp:
		return ConstLogModApp
	case ConstLogNameSupport:
		return ConstLogModSupport
	case ConstLogNameRestic:
		return ConstLogModRestic
	case ConstLogNameMailer:
		return ConstLogModMailer
	case ConstLogNameDbErrors:
		return ConstLogModDbErrors
	case ConstLogNameDbSlowquery:
		return ConstLogModDbSlowquery
	case ConstLogNameDbOptimize:
		return ConstLogModDbOptimize
	case ConstLogNameDbAuditlog:
		return ConstLogModDbAudit
	}
	return -1
}

func IsValidLogLevel(lvl string) bool {
	switch lvl {
	case LvlErr, LvlWarn, LvlInfo, LvlDbg:
		return true
	}
	return false
}

type LogEntry struct {
	Server string `json:"server"`
	Log    string `json:"log"`
	Level  string `json:"level"`
}

func (conf *Config) IsVariableImmutable(v string) bool {
	_, ok := conf.ImmuableFlagMap[v]
	return ok
}

func (conf *Config) IsVariableServerLevel(v string) bool {
	_, ok := conf.ImmuableFlagMap[v]
	return ok
}

func (conf *Config) SetApiTokenTimeout(value int) {
	conf.TokenTimeout = value
}

func (conf *Config) SwitchCloud18Shared() {
	if conf.Cloud18 {
		conf.Cloud18Shared = !conf.Cloud18Shared
	}
}

func (conf *Config) SwitchCloud18Alert() {
	conf.Cloud18Alert = !conf.Cloud18Alert
}

func (conf *Config) SwitchCloud18() {
	conf.Cloud18 = !conf.Cloud18
}

func (conf *Config) SetRestoreConfigOnStart(val bool) {
	conf.ConfRestoreOnStart = val
}

func (conf *Config) SetLogGitLevel(value int) {
	conf.LogGitLevel = value
	if value > 0 {
		conf.LogGit = true
	} else {
		conf.LogGit = false
	}

	if value == 4 {
		conf.GitMonitoringTicker = 30
	} else {
		conf.GitMonitoringTicker = 300
	}
}

func (conf *Config) SetLogSupportLevel(value int) {
	conf.LogSupportLevel = value
	if value > 0 {
		conf.LogSupport = true
	} else {
		conf.LogSupport = false
	}
}

func (conf *Config) GetImmutableChecksum() (hash.Hash, error) {
	new_h := md5.New()

	Container := make([]string, 0)

	for k, v := range conf.ImmuableFlagMap {
		if _, ok := conf.Secrets[k]; !ok {
			Container = append(Container, fmt.Sprintf("%s=%v", k, v))
		}
	}

	misc.SortKeysAsc(Container)

	js, err := json.Marshal(Container)
	if err != nil {
		return new_h, err
	}

	_, err = io.Copy(new_h, bytes.NewBuffer(js))
	return new_h, err
}

func (conf *Config) GetSecretChecksum() (hash.Hash, error) {
	new_h := md5.New()

	Container := make([]string, 0)

	for k, v := range conf.Secrets {
		Container = append(Container, fmt.Sprintf("%s=%v", k, v))
	}

	misc.SortKeysAsc(Container)

	js, err := json.Marshal(Container)
	if err != nil {
		return new_h, err
	}

	_, err = io.Copy(new_h, bytes.NewBuffer(js))
	return new_h, err
}

func (conf *Config) CreateGitlabProjects() {
	acces_tok, err := githelper.GetGitLabTokenBasicAuth(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg))
	if err != nil {
		if conf.Verbose || conf.IsEligibleForPrinting(ConstLogModGit, LvlErr) {
			log.Error(err.Error() + conf.GetDecryptedValue("cloud18-gitlab-password") + "\n")
		}
		return
	}

	uid, err := githelper.GetGitLabUserId(acces_tok, conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg))
	if err != nil {
		if conf.Verbose || conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
			log.Error(err.Error() + "\n")
		}
		return
	} else if uid == 0 {
		if conf.Verbose || conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg) {
			log.Error("Invalid user Id \n")
		}
		return
	}

	repopath := conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
	name := conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone

	githelper.GitLabCreateProject(conf.Secrets["git-acces-token"].Value, name, repopath, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg))
	githelper.GitLabCreatePullProject(conf.Secrets["git-acces-token"].Value, name, repopath, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(ConstLogModGit, LvlDbg))
}

func (conf *Config) SetMailSmtpAddr(value string) {
	conf.MailSMTPAddr = value
}

func (conf *Config) SetMailSmtpPassword(value string) {
	conf.MailSMTPPassword = value
}

func (conf *Config) SetMailSmtpUser(value string) {
	conf.MailSMTPUser = value
}

func (conf *Config) SetMailFrom(value string) {
	conf.MailFrom = value
}

func (conf *Config) SetMailTo(value string) {
	conf.MailTo = value
}

func (conf *Config) SwitchMailSmtpTlsSkipVerify() {
	conf.MailSMTPTLSSkipVerify = !conf.MailSMTPTLSSkipVerify
}

// ResticDurationChecker checks if the duration string is valid for restic retention
// The duration string should be in the format of "1y2m3d4h"
func ResticDurationChecker(keep string) bool {
	keep = strings.ToLower(keep)
	r := regexp.MustCompile(`^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$`)

	return r.MatchString(keep)
}

// ResticDurationChecker checks if the duration string is valid for restic retention
// The duration string should be in the format of "1y2m3d4h"
func (conf *Config) CheckKeepWithin() error {
	if !ResticDurationChecker(conf.BackupKeepWithin) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within': %s", conf.BackupKeepWithin)
	}

	if !ResticDurationChecker(conf.BackupKeepWithinHourly) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within-hourly': %s", conf.BackupKeepWithinHourly)
	}

	if !ResticDurationChecker(conf.BackupKeepWithinDaily) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within-daily': %s", conf.BackupKeepWithinDaily)
	}

	if !ResticDurationChecker(conf.BackupKeepWithinWeekly) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within-weekly': %s", conf.BackupKeepWithinWeekly)
	}

	if !ResticDurationChecker(conf.BackupKeepWithinMonthly) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within-monthly': %s", conf.BackupKeepWithinMonthly)
	}

	if !ResticDurationChecker(conf.BackupKeepWithinYearly) {
		return fmt.Errorf("Invalid duration format 'backup-keep-within-yearly': %s", conf.BackupKeepWithinYearly)
	}

	return nil
}

func isValidResticMode(value int) bool {
	if value == 0 {
		return true
	}
	if value < 600 || value > 777 {
		return false
	}
	_, err := strconv.ParseUint(strconv.Itoa(value), 8, 32)
	return err == nil
}

func parseResticMode(value int, defaultMode os.FileMode) os.FileMode {
	if value <= 0 {
		return defaultMode
	}
	if !isValidResticMode(value) {
		return defaultMode
	}

	parsed, err := strconv.ParseUint(strconv.Itoa(value), 8, 32)
	if err != nil {
		return defaultMode
	}

	return os.FileMode(parsed)
}

func (conf *Config) ValidateResticPermissions() error {
	if !isValidResticMode(conf.BackupResticDirMode) {
		return NewValidationError("backup-restic-dir-mode", conf.BackupResticDirMode, "expected octal value in 6xx/7xx range, like 700")
	}
	if !isValidResticMode(conf.BackupResticFileMode) {
		return NewValidationError("backup-restic-file-mode", conf.BackupResticFileMode, "expected octal value in 6xx/7xx range, like 600")
	}
	return nil
}

func (conf *Config) GetResticDirMode() os.FileMode {
	return parseResticMode(conf.BackupResticDirMode, 0700)
}

func (conf *Config) GetResticFileMode() os.FileMode {
	return parseResticMode(conf.BackupResticFileMode, 0600)
}

func (conf *Config) GetResticTimeout() time.Duration {
	if conf.BackupResticTimeout <= 0 {
		return 2 * time.Hour
	}
	return time.Duration(conf.BackupResticTimeout) * time.Second
}

type MeasurementConfig struct {
	Min      int
	Max      int
	Required bool
	Bytes    bool
}

func (m MeasurementConfig) String() string {
	var parts []string
	if m.Min > 0 {
		parts = append(parts, fmt.Sprintf("min:%d", m.Min))
	}
	if m.Max > 0 {
		parts = append(parts, fmt.Sprintf("max:%d", m.Max))
	}
	if m.Required {
		parts = append(parts, "required")
	}
	if m.Bytes {
		parts = append(parts, "bytes")
	}
	return strings.Join(parts, ", ")
}

var mUnits []string = []string{"0", "K", "M", "G", "T", "P", "E", "Z", "Y"}

type ErrorMeasurement struct {
	Field   string
	Old     string
	New     string
	Message string
}

func (e ErrorMeasurement) Error() string {
	return fmt.Sprintf("Old: %s, New: %s, Message: %s", e.Old, e.New, e.Message)
}

type ErrorConfigs []ErrorMeasurement

func (e ErrorConfigs) Error() string {
	var sb strings.Builder
	for _, v := range e {
		sb.WriteString(fmt.Sprintf("Field: %s, Error: %s\n", v.Field, v.Error()))
	}
	return sb.String()
}

func ParseConfigMeasurement(conf interface{}, defaultmap map[string]interface{}, clampToLimit bool) ErrorConfigs {
	errormap := make(ErrorConfigs, 0)
	if conf == nil {
		return errormap
	}

	to := reflect.TypeOf(conf).Elem()
	vo := reflect.ValueOf(conf).Elem()

	for i := 0; i < to.NumField(); i++ {
		f := to.Field(i)
		// Not measured field
		tag, ok := f.Tag.Lookup("measurement")
		if !ok {
			continue
		}

		// Not string field, no need to parse
		v, ok := vo.Field(i).Interface().(string)
		if !ok {
			continue
		}

		mtag, err := GetTagDetails(tag)
		if err != nil {
			errormap = append(errormap, ErrorMeasurement{Field: f.Name, Old: v, New: v, Message: fmt.Sprintf("error parsing tag for %s: %s", f.Name, err)})
			continue
		}

		if mtag.Parent != "" {
			// Check if parent is bool with true value
			pvb, ok := vo.FieldByName(mtag.Parent).Interface().(bool)
			if !ok || !pvb {
				// Parent is not bool or false, skip parsing
				continue
			}
		}

		// Parse unit measurement
		val, err := ParseUnitMeasurement(mtag, v, clampToLimit)
		if err != nil {
			dvalue, ok := defaultmap[f.Tag.Get("mapstructure")]
			if !ok {
				errormap = append(errormap, ErrorMeasurement{Field: f.Name, Old: v, New: v, Message: fmt.Sprintf("error parsing %s with no default: %s", f.Name, err)})
				continue
			}

			// fallback to default value
			val = dvalue.(string)
			errormap = append(errormap, ErrorMeasurement{Field: f.Name, Old: v, New: val, Message: fmt.Sprintf("error parsing %s: %s", f.Name, err)})
		}

		if !vo.Field(i).CanSet() {
			errormap = append(errormap, ErrorMeasurement{Field: f.Name, Old: v, New: v, Message: fmt.Sprintf("field %s is not settable", f.Name)})
			continue
		}

		vo.Field(i).SetString(val)
	}

	return errormap
}

// GetMeasurementTag returns the measurement tag of the field by the toml key
// The measurement tag is defined in the struct tag with the key "measurement"
// The measurement tag is used to convert the value to the base unit
// The format of the measurement tag is "base, [required, bytes]"
func GetMeasurementTag(s interface{}, tag string, list ...string) (map[string]string, error) {
	m := make(map[string]string)

	to := reflect.TypeOf(s)
	if to.Kind() == reflect.Ptr {
		to = to.Elem()
	}

	if tag == "name" {
		for _, fname := range list {
			f, ok := to.FieldByName(fname)
			if ok {
				m[fname] = f.Tag.Get("measurement")
			} else {
				return m, fmt.Errorf("field %s not found", fname)
			}
		}
	} else {
		for i := 0; i < to.NumField(); i++ {
			f := to.Field(i)
			ftag := strings.Split(f.Tag.Get(tag), ",")[0]
			if slices.Contains(list, ftag) {
				m[ftag] = f.Tag.Get("measurement")
			}
		}

		for _, fname := range list {
			if _, ok := m[fname]; !ok {
				return m, fmt.Errorf("field %s not found", fname)
			}
		}
	}

	return m, nil
}

type MeasurementTag struct {
	Parent   string
	Base     string
	IsBytes  bool
	Required bool
	Idx      int
	Min      int
	Max      int
}

func GetTagDetails(tag string) (*MeasurementTag, error) {
	result := &MeasurementTag{}
	/* Tag format: base, [required, bytes, min:, max:] */
	// measurement tag should have value
	if tag == "" {
		return nil, fmt.Errorf("tag cannot be empty, allowed values : %v", mUnits)
	}

	// split tag into base and optional parts
	parts := strings.Split(tag, ",")
	if parts[0] == "" {
		return nil, fmt.Errorf("base cannot be empty, use 0 for default")
	}

	// get base unit
	base := strings.ToUpper(strings.TrimSpace(parts[0]))
	if slices.Contains(mUnits, base) {
		result.Idx = slices.Index(mUnits, base)
	} else {
		return nil, fmt.Errorf("invalid unit: %s", base)
	}

	if len(parts) > 1 {
		for _, p := range parts[1:] {
			trimmed := strings.TrimSpace(p)
			if trimmed == "bytes" {
				result.IsBytes = true
			}
			if trimmed == "required" {
				result.Required = true
			}
			if strings.HasPrefix(trimmed, "min:") {
				min, _ := strconv.Atoi(strings.Split(trimmed, ":")[1])
				if min < 0 {
					return nil, fmt.Errorf("min value should be bigger than 0")
				}

				result.Min = min
			}
			if strings.HasPrefix(trimmed, "max:") {
				max, _ := strconv.Atoi(strings.Split(trimmed, ":")[1])
				if max < 0 {
					return nil, fmt.Errorf("max value should be bigger than 0")
				}

				result.Max = max
			}
			if strings.HasPrefix(trimmed, "parent:") {
				parent := strings.Split(trimmed, ":")[1]
				result.Parent = strings.TrimSpace(parent)
			}
		}
	}

	return result, nil
}

func ParseUnitMeasurement(mtag *MeasurementTag, vstr string, clampToLimit bool) (string, error) {
	var unit string
	var vidx int
	var step int = 1000
	var result string = vstr

	// check if value is empty
	if vstr == "" {
		if mtag.Required {
			return result, fmt.Errorf("value is required")
		} else {
			return result, nil
		}
	}

	// convert value to upper case
	vstr = strings.ToUpper(strings.TrimSpace(vstr))

	/* Value format: <number>[<unit>] */
	// split value into number and unit
	// 1. (\d+) : number (required)
	// 2. ([K|M|G|T|P|E|Z|Y])? : unit (optional)
	// 3. (B)? : bytes (optional)
	r := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([KMGTPEZY])?(B)?$`)
	matches := r.FindStringSubmatch(vstr)
	if len(matches) < 2 {
		return result, fmt.Errorf("invalid value: %s", vstr)
	}

	// get value
	vstr = matches[1]
	// convert value to integer
	val, err := strconv.Atoi(vstr)
	if err != nil {
		return result, fmt.Errorf("invalid value: %s", vstr)
	}

	// get unit
	unit = matches[2]

	// check if unit is empty
	if unit == "" {
		return vstr, nil
	}

	// check if unit is valid
	if !slices.Contains(mUnits, unit) {
		return result, fmt.Errorf("invalid unit: %s", unit)
	}

	// get unit index
	vidx = slices.Index(mUnits, unit)

	// unit should bigger than base
	if vidx < mtag.Idx {
		return result, fmt.Errorf("invalid minimum unit '%s': %s", mtag.Base, unit)
	}

	// convert step to 1024 if bytes
	if mtag.IsBytes {
		step = 1024
	}

	// convert value to base unit
	if vidx > mtag.Idx {
		for i := 0; i < vidx-mtag.Idx; i++ {
			val *= step
		}
	}

	if val < mtag.Min {
		if !clampToLimit {
			return result, fmt.Errorf("value should be bigger than %d", mtag.Min)
		}

		val = mtag.Min
	}

	if mtag.Max > 0 && val > mtag.Max {
		if !clampToLimit {
			return result, fmt.Errorf("value should be smaller than %d", mtag.Max)
		}

		val = mtag.Max
	}

	result = strconv.Itoa(val)

	return result, nil
}

func ParseUnitMeasurementToInt(tag, vstr string, clampToLimit bool) (int, error) {
	mtag, err := GetTagDetails(tag)
	if err != nil {
		return 0, err
	}

	valstr, err := ParseUnitMeasurement(mtag, vstr, clampToLimit)
	if err != nil {
		return 0, err
	}

	val, err := strconv.Atoi(valstr)
	if err != nil {
		return 0, fmt.Errorf("invalid value: %s", valstr)
	}

	return val, nil
}

type VolumePool struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
}

const PoolTypeLocal = "local"
const PoolTypeRemote = "shared"

func (conf *Config) GetAppVolumePools(pooltype string) map[string]VolumePool {
	pools := make(map[string]VolumePool)

	vols := strings.Split(conf.ProvAppVolumePools, ",")
	for _, vol := range vols {
		var name, poolType, mode, description string
		parts := strings.SplitN(vol, ":", 4)
		name = strings.TrimSpace(parts[0])
		poolType = PoolTypeLocal
		if len(parts) > 1 {
			poolType = strings.TrimSpace(parts[1])
		}

		if pooltype != "" && poolType != pooltype {
			continue
		}

		if len(parts) > 2 {
			mode = strings.TrimSpace(parts[2])
		}

		if len(parts) > 3 {
			description = strings.TrimSpace(parts[3])
		}

		pools[name] = VolumePool{
			Name:        name,
			Type:        poolType,
			Mode:        mode,
			Description: description,
		}
	}

	return pools
}

func (conf *Config) LoadAppTemplateList() ([]string, error) {
	var result []string = make([]string, 0)
	var gitpass, gitrepo, gitbranch, cacheDir string
	var timeout int = conf.Timeout
	if conf.ProvAppTemplateRepo != "" {
		gitpass = conf.GetDecryptedPassword("App Template Repo Pass", conf.ProvAppTemplateRepoPassword)
		gitrepo = conf.ProvAppTemplateRepo
		gitbranch = conf.ProvAppTemplateRepoBranch
		cacheDir = filepath.Join(conf.WorkingDir, ".cache", "git", "repos")
		tree, err := githelper.GetTemplateFromRepo(gitrepo, gitpass, gitbranch, cacheDir, timeout, true)
		if err != nil {
			return result, err
		}
		result = tree.PrintTree(".toml", true, true)
	}

	// remove empty entries
	cleaned := make([]string, 0)
	for _, v := range result {
		if strings.TrimSpace(v) != "" {
			cleaned = append(cleaned, v)
		}
	}
	result = cleaned

	return result, nil
}

func GetKeyAliasMap() map[string]string {
	return map[string]string{
		//"old-name": "new-name",
		// "user": "db-servers-credential",
		//"api-user", "api-credential")
		"monitoring-config-rewrite":     "monitoring-save-config",
		"api-user":                      "api-credentials",
		"replication-master-connection": "replication-source-name",
		"master-connection":             "replication-source-name",
		"logfile":                       "log-file",
		"wait-kill":                     "switchover-wait-kill",
		"hosts":                         "db-servers-hosts",
		"hosts-tls-ca-cert":             "db-servers-tls-ca-cert",
		"hosts-tls-client-key":          "db-servers-tls-client-key",
		"hosts-tls-client-cert":         "db-servers-tls-client-cert",
		"connect-timeout":               "db-servers-connect-timeout",
		"rpluser":                       "replication-credential",
		"prefmaster":                    "db-servers-prefered-master",
		"ignore-servers":                "db-servers-ignored-hosts",
		"master-connect-retry":          "replication-master-connection-retry",
		"readonly":                      "failover-readonly-state",
		"mdbshardproxy-hosts":           "shardproxy-servers",
		"shardproxy-hosts":              "shardproxy-servers",
		"multimaster":                   "replication-multi-master",
		"multi-tier-slave":              "replication-multi-tier-slave",
		"pre-failover-script":           "failover-pre-script",
		"post-failover-script":          "failover-post-script",
		"rejoin-script":                 "autorejoin-script",
		"share-directory":               "monitoring-sharedir",
		"working-directory":             "monitoring-datadir",
		"interactive":                   "failover-mode",
		"failcount":                     "failover-falsepositive-ping-counter",
		"wait-write-query":              "switchover-wait-write-query",
		"wait-trx":                      "switchover-wait-trx",
		"gtidcheck":                     "switchover-at-equal-gtid",
		"maxdelay":                      "failover-max-slave-delay",
		"maxscale-host":                 "maxscale-servers",
		"api-credential":                "api-credentials",
		"backup-binlogs-method":         "binlog-copy-mode",
		"backup-binlogs-script":         "binlog-copy-script",
		"monitoring-erreur-log-length":  "monitoring-error-log-length",

		// log level aliases
		"log-file-level":            "log-level-file",
		"log-task-level":            "log-level-task",
		"log-sst-level":             "log-level-sst",
		"log-heartbeat-level":       "log-level-heartbeat",
		"log-sql-level":             "log-level-sql",
		"log-app-level":             "log-level-app",
		"log-writer-election-level": "log-level-writer-election",
		"log-git-level":             "log-level-git",
		"log-config-load-level":     "log-level-config-load",
		"log-backup-stream-level":   "log-level-backup-stream",
		"log-orchestrator-level":    "log-level-orchestrator",
		"log-topology-level":        "log-level-topology",
		"log-proxy-level":           "log-level-proxy",
		"log-graphite-level":        "log-level-graphite",
		"log-binlog-purge-level":    "log-level-binlog-purge",
		"log-restic-level":          "log-level-restic",
		"log-mailer-level":          "log-level-mailer",
		"log-support-level":         "log-level-support",
		"log-external-script-level": "log-level-external-script",
		"log-stats-level":           "log-level-stats",
		"log-fetch-errorlog-level":  "log-level-database-errors",
		"log-fetch-slowquery-level": "log-level-database-slowquery",
		"log-optimize-level":        "log-level-database-optimize",
		"log-fetch-auditlog-level":  "log-level-database-audit",
		"shardproxy-log-level":      "log-level-shardproxy",
		"maxscale-log-level":        "log-level-maxscale",
		"myproxy-log-level":         "log-level-myproxy",
		"haproxy-log-level":         "log-level-haproxy",
		"proxysql-log-level":        "log-level-proxysql",
		"proxyjanitor-log-level":    "log-level-proxyjanitor",
		"mysqlrouter-log-level":     "log-level-mysqlrouter",
		"sphinx-log-level":          "log-level-sphinx",
		"registry-consul-log-level": "log-level-registry-consul",
		"log-vault-level":           "log-level-vault",
	}
}
