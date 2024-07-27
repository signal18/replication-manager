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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	masker "github.com/ggwhite/go-masker"
	"github.com/go-git/go-git/v5"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/misc"
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
	BaseDir                                   string                 `mapstructure:"monitoring-basedir" toml:"monitoring-basedir" json:"monitoringBasedir"`
	WorkingDir                                string                 `mapstructure:"monitoring-datadir" toml:"monitoring-datadir" json:"monitoringDatadir"`
	ShareDir                                  string                 `mapstructure:"monitoring-sharedir" toml:"monitoring-sharedir" json:"monitoringSharedir"`
	ConfDir                                   string                 `mapstructure:"monitoring-confdir" toml:"monitoring-confdir" json:"monitoringConfdir"`
	ConfRewrite                               bool                   `mapstructure:"monitoring-save-config" toml:"monitoring-save-config" json:"monitoringSaveConfig"`
	MonitoringSSLCert                         string                 `mapstructure:"monitoring-ssl-cert" toml:"monitoring-ssl-cert" json:"monitoringSSLCert"`
	MonitoringSSLKey                          string                 `mapstructure:"monitoring-ssl-key" toml:"monitoring-ssl-key" json:"monitoringSSLKey"`
	MonitoringKeyPath                         string                 `mapstructure:"monitoring-key-path" toml:"monitoring-key-path" json:"monitoringKeyPath"`
	MonitoringTicker                          int64                  `mapstructure:"monitoring-ticker" toml:"monitoring-ticker" json:"monitoringTicker"`
	MonitorWaitRetry                          int64                  `mapstructure:"monitoring-wait-retry" toml:"monitoring-wait-retry" json:"monitoringWaitRetry"`
	Socket                                    string                 `mapstructure:"monitoring-socket" toml:"monitoring-socket" json:"monitoringSocket"`
	TunnelHost                                string                 `mapstructure:"monitoring-tunnel-host" toml:"monitoring-tunnel-host" json:"monitoringTunnelHost"`
	TunnelCredential                          string                 `mapstructure:"monitoring-tunnel-credential" toml:"monitoring-tunnel-credential" json:"monitoringTunnelCredential"`
	TunnelKeyPath                             string                 `mapstructure:"monitoring-tunnel-key-path" toml:"monitoring-tunnel-key-path" json:"monitoringTunnelKeyPath"`
	MonitorAddress                            string                 `mapstructure:"monitoring-address" toml:"monitoring-address" json:"monitoringAddress"`
	MonitorWriteHeartbeat                     bool                   `mapstructure:"monitoring-write-heartbeat" toml:"monitoring-write-heartbeat" json:"monitoringWriteHeartbeat"`
	MonitorPause                              bool                   `mapstructure:"monitoring-pause" toml:"monitoring-pause" json:"monitoringPause"`
	MonitorWriteHeartbeatCredential           string                 `mapstructure:"monitoring-write-heartbeat-credential" toml:"monitoring-write-heartbeat-credential" json:"monitoringWriteHeartbeatCredential"`
	MonitorVariableDiff                       bool                   `mapstructure:"monitoring-variable-diff" toml:"monitoring-variable-diff" json:"monitoringVariableDiff"`
	MonitorSchemaChange                       bool                   `mapstructure:"monitoring-schema-change" toml:"monitoring-schema-change" json:"monitoringSchemaChange"`
	MonitorQueryRules                         bool                   `mapstructure:"monitoring-query-rules" toml:"monitoring-query-rules" json:"monitoringQueryRules"`
	MonitorSchemaChangeScript                 string                 `mapstructure:"monitoring-schema-change-script" toml:"monitoring-schema-change-script" json:"monitoringSchemaChangeScript"`
	MonitorCheckGrants                        bool                   `mapstructure:"monitoring-check-grants" toml:"monitoring-check-grants" json:"monitoringCheckGrants"`
	MonitorProcessList                        bool                   `mapstructure:"monitoring-processlist" toml:"monitoring-processlist" json:"monitoringProcesslist"`
	MonitorQueries                            bool                   `mapstructure:"monitoring-queries" toml:"monitoring-queries" json:"monitoringQueries"`
	MonitorPFS                                bool                   `mapstructure:"monitoring-performance-schema" toml:"monitoring-performance-schema" json:"monitoringPerformanceSchema"`
	MonitorPlugins                            bool                   `mapstructure:"monitoring-plugins" toml:"monitoring-plugins" json:"monitoringPlugins"`
	MonitorInnoDBStatus                       bool                   `mapstructure:"monitoring-innodb-status" toml:"monitoring-innodb-status" json:"monitoringInnoDBStatus"`
	MonitorLongQueryWithProcess               bool                   `mapstructure:"monitoring-long-query-with-process" toml:"monitoring-long-query-with-process" json:"monitoringLongQueryWithProcess"`
	MonitorLongQueryTime                      int                    `mapstructure:"monitoring-long-query-time" toml:"monitoring-long-query-time" json:"monitoringLongQueryTime"`
	MonitorLongQueryScript                    string                 `mapstructure:"monitoring-long-query-script" toml:"monitoring-long-query-script" json:"monitoringLongQueryScript"`
	MonitorLongQueryWithTable                 bool                   `mapstructure:"monitoring-long-query-with-table" toml:"monitoring-long-query-with-table" json:"monitoringLongQueryWithTable"`
	MonitorLongQueryLogLength                 int                    `mapstructure:"monitoring-long-query-log-length" toml:"monitoring-long-query-log-length" json:"monitoringLongQueryLogLength"`
	MonitorErrorLogLength                     int                    `mapstructure:"monitoring-erreur-log-length" toml:"monitoring-erreur-log-length" json:"monitoringErreurLogLength"`
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
	LogFile                                   string                 `mapstructure:"log-file" toml:"log-file" json:"logFile"`
	LogSyslog                                 bool                   `mapstructure:"log-syslog" toml:"log-syslog" json:"logSyslog"`
	LogLevel                                  int                    `mapstructure:"log-level" toml:"log-level" json:"logLevel"`
	LogRotateMaxSize                          int                    `mapstructure:"log-rotate-max-size" toml:"log-rotate-max-size" json:"logRotateMaxSize"`
	LogRotateMaxBackup                        int                    `mapstructure:"log-rotate-max-backup" toml:"log-rotate-max-backup" json:"logRotateMaxBackup"`
	LogRotateMaxAge                           int                    `mapstructure:"log-rotate-max-age" toml:"log-rotate-max-age" json:"logRotateMaxAge"`
	LogTask                                   bool                   `mapstructure:"log-task" toml:"log-task" json:"logTask"`
	LogTaskLevel                              int                    `mapstructure:"log-task-level" toml:"log-task-level" json:"logTaskLevel"`
	LogSST                                    bool                   `mapstructure:"log-sst" toml:"log-sst" json:"logSst"`                  // internal replication-manager sst
	LogSSTLevel                               int                    `mapstructure:"log-sst-level" toml:"log-sst-level" json:"logSstLevel"` // internal replication-manager sst
	SSTSendBuffer                             int                    `mapstructure:"sst-send-buffer" toml:"sst-send-buffer" json:"sstSendBuffer"`
	LogHeartbeat                              bool                   `mapstructure:"log-heartbeat" toml:"log-heartbeat" json:"logHeartbeat"`
	LogHeartbeatLevel                         int                    `mapstructure:"log-heartbeat-level" toml:"log-heartbeat-level" json:"logHeartbeatLevel"`
	LogSQLInMonitoring                        bool                   `mapstructure:"log-sql-in-monitoring"  toml:"log-sql-in-monitoring" json:"logSqlInMonitoring"`
	LogWriterElection                         bool                   `mapstructure:"log-writer-election"  toml:"log-writer-election" json:"logWriterElection"`
	LogWriterElectionLevel                    int                    `mapstructure:"log-writer-election-level"  toml:"log-writer-election-level" json:"logWriterElectionLevel"`
	LogGit                                    bool                   `mapstructure:"log-git" toml:"log-git" json:"logGit"`
	LogGitLevel                               int                    `mapstructure:"log-git-level" toml:"log-git-level" json:"logGitLevel"`
	LogConfigLoad                             bool                   `mapstructure:"log-config-load" toml:"log-config-load" json:"logConfigLoad"`
	LogConfigLoadLevel                        int                    `mapstructure:"log-config-load-level" toml:"log-config-load-level" json:"logConfigLoadLevel"`
	LogBackupStream                           bool                   `mapstructure:"log-backup-stream" toml:"log-backup-stream" json:"logBackupStream"`
	LogBackupStreamLevel                      int                    `mapstructure:"log-backup-stream-level" toml:"log-backup-stream-level" json:"logBackupStreamLevel"`
	LogOrchestrator                           bool                   `mapstructure:"log-orchestrator" toml:"log-orchestrator" json:"logOrchestrator"`
	LogOrchestratorLevel                      int                    `mapstructure:"log-orchestrator-level" toml:"log-orchestrator-level" json:"logOrchestratorLevel"`
	LogTopology                               bool                   `mapstructure:"log-topology" toml:"log-topology" json:"logTopology"`
	LogTopologyLevel                          int                    `mapstructure:"log-topology-level" toml:"log-topology-level" json:"logTopologyLevel"`
	LogProxy                                  bool                   `mapstructure:"log-proxy" toml:"log-proxy" json:"logProxy"`
	LogProxyLevel                             int                    `mapstructure:"log-proxy-level" toml:"log-proxy-level" json:"logProxyLevel"`
	LogGraphite                               bool                   `mapstructure:"log-graphite" toml:"log-graphite" json:"logGraphite"`
	LogGraphiteLevel                          int                    `mapstructure:"log-graphite-level" toml:"log-graphite-level" json:"logGraphiteLevel"`
	LogBinlogPurge                            bool                   `mapstructure:"log-binlog-purge" toml:"log-binlog-purge" json:"logBinlogPurge"`
	LogBinlogPurgeLevel                       int                    `mapstructure:"log-binlog-purge-level" toml:"log-binlog-purge-level" json:"logBinlogPurgeLevel"`
	User                                      string                 `mapstructure:"db-servers-credential" toml:"db-servers-credential" json:"dbServersCredential"`
	Hosts                                     string                 `mapstructure:"db-servers-hosts" toml:"db-servers-hosts" json:"dbServersHosts"`
	DbServersChangeStateScript                string                 `mapstructure:"db-servers-change-state-script" toml:"db-servers-change-state-script" json:"dbServersChangeStateScript"`
	HostsDelayed                              string                 `mapstructure:"replication-delayed-hosts" toml:"replication-delayed-hosts" json:"replicationDelayedHosts"`
	HostsDelayedTime                          int                    `mapstructure:"replication-delayed-time" toml:"replication-delayed-time" json:"replicationDelayedTime"`
	DBServersTLSUseGeneratedCertificate       bool                   `mapstructure:"db-servers-tls-use-generated-cert" toml:"db-servers-tls-use-generated-cert" json:"dbServersUseGeneratedCert"`
	HostsTLSCA                                string                 `mapstructure:"db-servers-tls-ca-cert" toml:"db-servers-tls-ca-cert" json:"dbServersTlsCaCert"`
	HostsTlsCliKey                            string                 `mapstructure:"db-servers-tls-client-key" toml:"db-servers-tls-client-key" json:"dbServersTlsClientKey"`
	HostsTlsCliCert                           string                 `mapstructure:"db-servers-tls-client-cert" toml:"db-servers-tls-client-cert" json:"dbServersTlsClientCert"`
	HostsTlsSrvKey                            string                 `mapstructure:"db-servers-tls-server-key" toml:"db-servers-tls-server-key" json:"dbServersTlsServerKey"`
	HostsTlsSrvCert                           string                 `mapstructure:"db-servers-tls-server-cert" toml:"db-servers-tls-server-cert" json:"dbServersTlsServerCert"`
	PrefMaster                                string                 `mapstructure:"db-servers-prefered-master" toml:"db-servers-prefered-master" json:"dbServersPreferedMaster"`
	BackupServers                             string                 `mapstructure:"db-servers-backup-hosts" toml:"db-servers-backup-hosts" json:"dbServersBackupHosts"`
	IgnoreSrv                                 string                 `mapstructure:"db-servers-ignored-hosts" toml:"db-servers-ignored-hosts" json:"dbServersIgnoredHosts"`
	IgnoreSrvRO                               string                 `mapstructure:"db-servers-ignored-readonly" toml:"db-servers-ignored-readonly" json:"dbServersIgnoredReadonly"`
	Timeout                                   int                    `mapstructure:"db-servers-connect-timeout" toml:"db-servers-connect-timeout" json:"dbServersConnectTimeout"`
	ReadTimeout                               int                    `mapstructure:"db-servers-read-timeout" toml:"db-servers-read-timeout" json:"dbServersReadTimeout"`
	DBServersLocality                         string                 `mapstructure:"db-servers-locality" toml:"db-servers-locality" json:"dbServersLocality"`
	PRXServersReadOnMaster                    bool                   `mapstructure:"proxy-servers-read-on-master" toml:"proxy-servers-read-on-master" json:"proxyServersReadOnMaster"`
	PRXServersReadOnMasterNoSlave             bool                   `mapstructure:"proxy-servers-read-on-master-no-slave" toml:"proxy-servers-read-on-master-no-slave" json:"proxyServersReadOnMasterNoSlave"`
	PRXServersBackendCompression              bool                   `mapstructure:"proxy-servers-backend-compression" toml:"proxy-servers-backend-compression" json:"proxyServersBackendCompression"`
	PRXServersBackendMaxReplicationLag        int                    `mapstructure:"proxy-servers-backend-max-replication-lag" toml:"proxy-servers-backend--max-replication-lag" json:"proxyServersBackendMaxReplicationLag"`
	PRXServersBackendMaxConnections           int                    `mapstructure:"proxy-servers-backend-max-connections" toml:"proxy-servers-backend--max-connections" json:"proxyServersBackendMaxConnections"`
	PRXServersChangeStateScript               string                 `mapstructure:"proxy-servers-change-state-script" toml:"proxy-servers-change-state-script" json:"proxyServersChangeStateScript"`
	ClusterHead                               string                 `mapstructure:"cluster-head" toml:"cluster-head" json:"clusterHead"`
	ReplicationMultisourceHeadClusters        string                 `mapstructure:"replication-multisource-head-clusters" toml:"replication-multisource-head-clusters" json:"replicationMultisourceHeadClusters"`
	MasterConnectRetry                        int                    `mapstructure:"replication-master-connect-retry" toml:"replication-master-connect-retry" json:"replicationMasterConnectRetry"`
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
	BindAddr                                  string                 `mapstructure:"http-bind-address" toml:"http-bind-address" json:"httpBindAdress"`
	HttpPort                                  string                 `mapstructure:"http-port" toml:"http-port" json:"httpPort"`
	HttpServ                                  bool                   `mapstructure:"http-server" toml:"http-server" json:"httpServer"`
	ApiServ                                   bool                   `mapstructure:"http-server" toml:"api-server" json:"apiServer"`
	HttpRoot                                  string                 `mapstructure:"http-root" toml:"http-root" json:"httpRoot"`
	HttpAuth                                  bool                   `mapstructure:"http-auth" toml:"http-auth" json:"httpAuth"`
	HttpBootstrapButton                       bool                   `mapstructure:"http-bootstrap-button" toml:"http-bootstrap-button" json:"httpBootstrapButton"`
	SessionLifeTime                           int                    `mapstructure:"http-session-lifetime" toml:"http-session-lifetime" json:"httpSessionLifetime"`
	HttpRefreshInterval                       int                    `mapstructure:"http-refresh-interval" toml:"http-refresh-interval" json:"httpRefreshInterval"`
	Daemon                                    bool                   `mapstructure:"daemon" toml:"-" json:"-"`
	MailFrom                                  string                 `mapstructure:"mail-from" toml:"mail-from" json:"mailFrom"`
	MailTo                                    string                 `mapstructure:"mail-to" toml:"mail-to" json:"mailTo"`
	MailSMTPAddr                              string                 `mapstructure:"mail-smtp-addr" toml:"mail-smtp-addr" json:"mailSmtpAddr"`
	MailSMTPUser                              string                 `mapstructure:"mail-smtp-user" toml:"mail-smtp-user" json:"mailSmtpUser"`
	MailSMTPPassword                          string                 `mapstructure:"mail-smtp-password" toml:"mail-smtp-password" json:"mailSmtpPassword"`
	MailSMTPTLSSkipVerify                     bool                   `mapstructure:"mail-smtp-tls-skip-verify" toml:"mail-smtp-tls-skip-verify" json:"mailSmtpTlsSkipVerify"`
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
	MdbsProxyLogLevel                         int                    `mapstructure:"shardproxy-log-level" toml:"shardproxy-log-level" json:"shardproxyLogLevel"`
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
	MxsLogLevel                               int                    `mapstructure:"maxscale-log-level" toml:"maxscale-log-level" json:"maxscaleLogLevel"`
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
	MyproxyLogLevel                           int                    `mapstructure:"myproxy-log-level" toml:"myproxy-log-level" json:"myproxyLogLevel"`
	MyproxyPort                               int                    `mapstructure:"myproxy-port" toml:"myproxy-port" json:"myproxyPort"`
	MyproxyUser                               string                 `mapstructure:"myproxy-user" toml:"myproxy-user" json:"myproxyUser"`
	MyproxyPassword                           string                 `mapstructure:"myproxy-password" toml:"myproxy-password" json:"myproxyPassword"`
	HaproxyOn                                 bool                   `mapstructure:"haproxy" toml:"haproxy" json:"haproxy"`
	HaproxyDebug                              bool                   `mapstructure:"haproxy-debug" toml:"haproxy-debug" json:"haproxyDebug"`
	HaproxyLogLevel                           int                    `mapstructure:"haproxy-log-level" toml:"haproxy-log-level" json:"haproxyLogLevel"`
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
	HaproxyHostsIPV6                          string                 `mapstructure:"haproxy-servers-ipv6" toml:"haproxy-servers-ipv6" json:"haproxyServers-ipv6"`
	HaproxyReadBindIp                         string                 `mapstructure:"haproxy-ip-read-bind" toml:"haproxy-ip-read-bind" json:"haproxyIpReadBind"`
	HaproxyBinaryPath                         string                 `mapstructure:"haproxy-binary-path" toml:"haproxy-binary-path" json:"haproxyBinaryPath"`
	HaproxyAPIReadBackend                     string                 `mapstructure:"haproxy-api-read-backend"  toml:"haproxy-api-read-backend" json:"haproxyAPIReadBackend"`
	HaproxyAPIWriteBackend                    string                 `mapstructure:"haproxy-api-write-backend"  toml:"haproxy-api-write-backend" json:"haproxyAPIWriteBackend"`
	ProxysqlOn                                bool                   `mapstructure:"proxysql" toml:"proxysql" json:"proxysql"`
	ProxysqlDebug                             bool                   `mapstructure:"proxysql-debug" toml:"proxysql-debug" json:"proxysqlDebug"`
	ProxysqlLogLevel                          int                    `mapstructure:"proxysql-log-level" toml:"proxysql-log-level" json:"proxysqlLogLevel"`
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
	ProxysqlCopyGrants                        bool                   `mapstructure:"proxysql-bootstrap-users" toml:"proxysql-bootstarp-users" json:"proxysqlBootstrapyUsers"`
	ProxysqlBootstrap                         bool                   `mapstructure:"proxysql-bootstrap" toml:"proxysql-bootstrap" json:"proxysqlBootstrap"`
	ProxysqlBootstrapVariables                bool                   `mapstructure:"proxysql-bootstrap-variables" toml:"proxysql-bootstrap-variables" json:"proxysqlBootstrapVariables"`
	ProxysqlBootstrapHG                       bool                   `mapstructure:"proxysql-bootstrap-hostgroups" toml:"proxysql-bootstrap-hostgroups" json:"proxysqlBootstrapHostgroups"`
	ProxysqlBootstrapQueryRules               bool                   `mapstructure:"proxysql-bootstrap-query-rules" toml:"proxysql-bootstrap-query-rules" json:"proxysqlBootstrapQueryRules"`
	ProxysqlMultiplexing                      bool                   `mapstructure:"proxysql-multiplexing" toml:"proxysql-multiplexing" json:"proxysqlMultiplexing"`
	ProxysqlBinaryPath                        string                 `mapstructure:"proxysql-binary-path" toml:"proxysql-binary-path" json:"proxysqlBinaryPath"`
	ProxyJanitorDebug                         bool                   `mapstructure:"proxyjanitor-debug" toml:"proxyjanitor-debug" json:"proxyjanitorDebug"`
	ProxyJanitorLogLevel                      int                    `mapstructure:"proxyjanitor-log-level" toml:"proxyjanitor-log-level" json:"proxyjanitorLogLevel"`
	ProxyJanitorHosts                         string                 `mapstructure:"proxyjanitor-servers" toml:"proxyjanitor-servers" json:"proxyjanitorServers"`
	ProxyJanitorHostsIPV6                     string                 `mapstructure:"proxyjanitor-servers-ipv6" toml:"proxyjanitor-servers-ipv6" json:"proxyjanitorServers-ipv6"`
	ProxyJanitorPort                          string                 `mapstructure:"proxyjanitor-port" toml:"proxyjanitor-port" json:"proxyjanitorPort"`
	ProxyJanitorAdminPort                     string                 `mapstructure:"proxyjanitor-admin-port" toml:"proxyjanitor-admin-port" json:"proxyjanitorAdminPort"`
	ProxyJanitorUser                          string                 `mapstructure:"proxyjanitor-user" toml:"proxyjanitor-user" json:"proxyjanitorUser"`
	ProxyJanitorPassword                      string                 `mapstructure:"proxyjanitor-password" toml:"proxyjanitor-password" json:"proxyjanitorPassword"`
	ProxyJanitorBinaryPath                    string                 `mapstructure:"proxyjanitor-binary-path" toml:"proxyjanitor-binary-path" json:"proxyjanitorBinaryPath"`
	MysqlRouterOn                             bool                   `mapstructure:"mysqlrouter" toml:"mysqlrouter" json:"mysqlrouter"`
	MysqlRouterDebug                          bool                   `mapstructure:"mysqlrouter-debug" toml:"mysqlrouter-debug" json:"mysqlrouterDebug"`
	MysqlRouterLogLevel                       int                    `mapstructure:"mysqlrouter-log-level" toml:"mysqlrouter-log-level" json:"mysqlrouterLogLevel"`
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
	SphinxLogLevel                            int                    `mapstructure:"sphinx-log-level" toml:"sphinx-log-level" json:"sphinxLogLevel"`
	SphinxHosts                               string                 `mapstructure:"sphinx-servers" toml:"sphinx-servers" json:"sphinxServers"`
	SphinxHostsIPV6                           string                 `mapstructure:"sphinx-servers-ipv6" toml:"sphinx-servers-ipv6" json:"sphinxServers-ipv6"`
	SphinxJanitorWeights                      string                 `mapstructure:"sphinx-janitor-weights" toml:"sphinx-janitor-weights" json:"sphinxJanitorWeights"`
	SphinxConfig                              string                 `mapstructure:"sphinx-config" toml:"sphinx-config" json:"sphinxConfig"`
	SphinxQLPort                              string                 `mapstructure:"sphinx-sql-port" toml:"sphinx-sql-port" json:"sphinxSqlPort"`
	SphinxPort                                string                 `mapstructure:"sphinx-port" toml:"sphinx-port" json:"sphinxPort"`
	RegistryConsul                            bool                   `mapstructure:"registry-consul" toml:"registry-consul" json:"registryConsul"`
	RegistryConsulDebug                       bool                   `mapstructure:"registry-consul-debug" toml:"registry-consul-debug" json:"registryConsulDebug"`
	RegistryConsulLogLevel                    int                    `mapstructure:"registry-consul-log-level" toml:"registry-consul-log-level" json:"registryConsulLogLevel"`
	RegistryConsulCredential                  string                 `mapstructure:"registry-consul-credential" toml:"registry-consul-credential" json:"registryConsulCredential"`
	RegistryConsulToken                       string                 `mapstructure:"registry-consul-token" toml:"registry-consul-token" json:"registryConsulToken"`
	RegistryConsulHosts                       string                 `mapstructure:"registry-servers" toml:"registry-servers" json:"registryServers"`
	RegistryConsulJanitorWeights              string                 `mapstructure:"registry-janitor-weights" toml:"registry-janitor-weights" json:"registryJanitorWeights"`
	KeyPath                                   string                 `mapstructure:"keypath" toml:"-" json:"-"`
	Topology                                  string                 `mapstructure:"topology" toml:"-" json:"-"` // use by bootstrap
	TopologyTarget                            string                 `mapstructure:"topology-target" toml:"topology-target" json:"topologyTarget"`
	GraphiteMetrics                           bool                   `mapstructure:"graphite-metrics" toml:"graphite-metrics" json:"graphiteMetrics"`
	GraphiteEmbedded                          bool                   `mapstructure:"graphite-embedded" toml:"graphite-embedded" json:"graphiteEmbedded"`
	GraphiteWhitelist                         bool                   `mapstructure:"graphite-whitelist" toml:"graphite-whitelist" json:"graphiteWhitelist"`
	GraphiteBlacklist                         bool                   `mapstructure:"graphite-blacklist" toml:"graphite-blacklist" json:"graphiteBlacklist"`
	GraphiteWhitelistTemplate                 string                 `mapstructure:"graphite-whitelist-template" toml:"graphite-whitelist-template" json:"graphiteWhitelistTemplate"`
	GraphiteCarbonHost                        string                 `mapstructure:"graphite-carbon-host" toml:"graphite-carbon-host" json:"graphiteCarbonHost"`
	GraphiteCarbonPort                        int                    `mapstructure:"graphite-carbon-port" toml:"graphite-carbon-port" json:"graphiteCarbonPort"`
	GraphiteCarbonApiPort                     int                    `mapstructure:"graphite-carbon-api-port" toml:"graphite-carbon-api-port" json:"graphiteCarbonApiPort"`
	GraphiteCarbonServerPort                  int                    `mapstructure:"graphite-carbon-server-port" toml:"graphite-carbon-server-port" json:"graphiteCarbonServerPort"`
	GraphiteCarbonLinkPort                    int                    `mapstructure:"graphite-carbon-link-port" toml:"graphite-carbon-link-port" json:"graphiteCarbonLinkPort"`
	GraphiteCarbonPicklePort                  int                    `mapstructure:"graphite-carbon-pickle-port" toml:"graphite-carbon-pickle-port" json:"graphiteCarbonPicklePort"`
	GraphiteCarbonPprofPort                   int                    `mapstructure:"graphite-carbon-pprof-port" toml:"graphite-carbon-pprof-port" json:"graphiteCarbonPprofPort"`
	SysbenchBinaryPath                        string                 `mapstructure:"sysbench-binary-path" toml:"sysbench-binary-path" json:"sysbenchBinaryPath"`
	SysbenchTest                              string                 `mapstructure:"sysbench-test" toml:"sysbench-test" json:"sysbenchBinaryTest"`
	SysbenchV1                                bool                   `mapstructure:"sysbench-v1" toml:"sysbench-v1" json:"sysbenchV1"`
	SysbenchTime                              int                    `mapstructure:"sysbench-time" toml:"sysbench-time" json:"sysbenchTime"`
	SysbenchThreads                           int                    `mapstructure:"sysbench-threads" toml:"sysbench-threads" json:"sysbenchThreads"`
	SysbenchTables                            int                    `mapstructure:"sysbench-tables" toml:"sysbench-tables" json:"sysbenchTables"`
	SysbenchScale                             int                    `mapstructure:"sysbench-scale" toml:"sysbench-scale" json:"sysbenchScale"`
	Arbitration                               bool                   `mapstructure:"arbitration-external" toml:"arbitration-external" json:"arbitrationExternal"`
	ArbitrationSasSecret                      string                 `mapstructure:"arbitration-external-secret" toml:"arbitration-external-secret" json:"arbitrationExternalSecret"`
	ArbitrationSasHosts                       string                 `mapstructure:"arbitration-external-hosts" toml:"arbitration-external-hosts" json:"arbitrationExternalHosts"`
	ArbitrationSasUniqueId                    int                    `mapstructure:"arbitration-external-unique-id" toml:"arbitration-external-unique-id" json:"arbitrationExternalUniqueId"`
	ArbitrationPeerHosts                      string                 `mapstructure:"arbitration-peer-hosts" toml:"arbitration-peer-hosts" json:"arbitrationPeerHosts"`
	ArbitrationFailedMasterScript             string                 `mapstructure:"arbitration-failed-master-script" toml:"arbitration-failed-master-script" json:"arbitrationFailedMasterScript"`
	ArbitratorAddress                         string                 `mapstructure:"arbitrator-bind-address" toml:"arbitrator-bind-address" json:"arbitratorBindAddress"`
	ArbitratorDriver                          string                 `mapstructure:"arbitrator-driver" toml:"arbitrator-driver" json:"arbitratorDriver"`
	ArbitrationReadTimout                     int                    `mapstructure:"arbitration-read-timeout" toml:"arbitration-read-timeout" json:"arbitrationReadTimout"`
	SwitchoverCopyOldLeaderGtid               bool                   `toml:"-" json:"-"` //suspicious code
	Test                                      bool                   `mapstructure:"test" toml:"test" json:"test"`
	TestInjectTraffic                         bool                   `mapstructure:"test-inject-traffic" toml:"test-inject-traffic" json:"testInjectTraffic"`
	Enterprise                                bool                   `toml:"enterprise" json:"enterprise"` //used to talk to opensvc collector
	KubeConfig                                string                 `mapstructure:"kube-config" toml:"kube-config" json:"kubeConfig"`
	SlapOSConfig                              string                 `mapstructure:"slapos-config" toml:"slapos-config" json:"slaposConfig"`
	SlapOSDBPartitions                        string                 `mapstructure:"slapos-db-partitions" toml:"slapos-db-partitions" json:"slaposDbPartitions"`
	SlapOSProxySQLPartitions                  string                 `mapstructure:"slapos-proxysql-partitions" toml:"slapos-proxysql-partitions" json:"slaposProxysqlPartitions"`
	SlapOSHaProxyPartitions                   string                 `mapstructure:"slapos-haproxy-partitions" toml:"slapos-haproxy-partitions" json:"slaposHaproxyPartitions"`
	SlapOSMaxscalePartitions                  string                 `mapstructure:"slapos-maxscale-partitions" toml:"slapos-maxscale-partitions" json:"slaposMaxscalePartitions"`
	SlapOSShardProxyPartitions                string                 `mapstructure:"slapos-shardproxy-partitions" toml:"slapos-shardproxy-partitions" json:"slaposShardproxyPartitions"`
	SlapOSSphinxPartitions                    string                 `mapstructure:"slapos-sphinx-partitions" toml:"slapos-sphinx-partitions" json:"slaposSphinxPartitions"`
	ProvHost                                  string                 `mapstructure:"opensvc-host" toml:"opensvc-host" json:"opensvcHost"`
	OnPremiseSSH                              bool                   `mapstructure:"onpremise-ssh" toml:"onpremise-ssh" json:"onpremiseSsh"`
	OnPremiseSSHPort                          int                    `mapstructure:"onpremise-ssh-port" toml:"onpremise-ssh-port" json:"onpremiseSshPort"`
	OnPremiseSSHCredential                    string                 `mapstructure:"onpremise-ssh-credential" toml:"onpremise-ssh-credential" json:"onpremiseSshCredential"`
	OnPremiseSSHPrivateKey                    string                 `mapstructure:"onpremise-ssh-private-key" toml:"onpremise-ssh-private-key" json:"onpremiseSshPrivateKey"`
	OnPremiseSSHStartDbScript                 string                 `mapstructure:"onpremise-ssh-start-db-script" toml:"onpremise-ssh-start-db-script" json:"onpremiseSshStartDbScript"`
	OnPremiseSSHStartProxyScript              string                 `mapstructure:"onpremise-ssh-start-proxy-script" toml:"onpremise-ssh-start-proxy-script" json:"onpremiseSshStartProxyScript"`
	OnPremiseSSHDbJobScript                   string                 `mapstructure:"onpremise-ssh-db-job-script" toml:"onpremise-ssh-db-job-script" json:"onpremiseSshDbJobScript"`
	ProvOpensvcP12Certificate                 string                 `mapstructure:"opensvc-p12-certificate" toml:"opensvc-p12-certificate" json:"opensvcP12Certificate"`
	ProvOpensvcP12Secret                      string                 `mapstructure:"opensvc-p12-secret" toml:"opensvc-p12-secret" json:"opensvcP12Secret"`
	ProvOpensvcUseCollectorAPI                bool                   `mapstructure:"opensvc-use-collector-api" toml:"opensvc-use-collector-api" json:"opensvcUseCollectorApi"`
	ProvOpensvcCollectorAccount               string                 `mapstructure:"opensvc-collector-account" toml:"opensvc-collector-account" json:"opensvcCollectorAccount"`
	ProvRegister                              bool                   `mapstructure:"opensvc-register" toml:"opensvc-register" json:"opensvcRegister"`
	ProvAdminUser                             string                 `mapstructure:"opensvc-admin-user" toml:"opensvc-admin-user" json:"opensvcAdminUser"`
	ProvUser                                  string                 `mapstructure:"opensvc-user" toml:"opensvc-user" json:"opensvcUser"`
	ProvCodeApp                               string                 `mapstructure:"opensvc-codeapp" toml:"opensvc-codeapp" json:"opensvcCodeapp"`
	ProvSerialized                            bool                   `mapstructure:"prov-serialized" toml:"prov-serialized" json:"provSerialized"`
	ProvOrchestrator                          string                 `mapstructure:"prov-orchestrator" toml:"prov-orchestrator" json:"provOrchestrator"`
	ProvOrchestratorEnable                    string                 `mapstructure:"prov-orchestrator-enable" toml:"prov-orchestrator-enable" json:"provOrchestratorEnable"`
	ProvOrchestratorCluster                   string                 `mapstructure:"prov-orchestrator-cluster" toml:"prov-orchestrator-cluster" json:"provOrchestratorCluster"`
	ProvDBApplyDynamicConfig                  bool                   `mapstructure:"prov-db-apply-dynamic-config" toml:"prov-db-apply-dynamic-config" json:"provDBApplyDynamicConfig"`
	ProvDBClientBasedir                       string                 `mapstructure:"prov-db-client-basedir" toml:"prov-db-client-basedir" json:"provDbClientBasedir"`
	ProvDBBinaryBasedir                       string                 `mapstructure:"prov-db-binary-basedir" toml:"prov-db-binary-basedir" json:"provDbBinaryBasedir"`
	ProvType                                  string                 `mapstructure:"prov-db-service-type" toml:"prov-db-service-type" json:"provDbServiceType"`
	ProvAgents                                string                 `mapstructure:"prov-db-agents" toml:"prov-db-agents" json:"provDbAgents"`
	ProvMem                                   string                 `mapstructure:"prov-db-memory" toml:"prov-db-memory" json:"provDbMemory"`
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
	ProvDisk                                  string                 `mapstructure:"prov-db-disk-size" toml:"prov-db-disk-size" json:"provDbDiskSize"`
	ProvDiskSystemSize                        string                 `mapstructure:"prov-db-disk-system-size" toml:"prov-db-disk-system-size" json:"provDbDiskSystemSize"`
	ProvDiskTempSize                          string                 `mapstructure:"prov-db-disk-temp-size" toml:"prov-db-disk-temp-size" json:"provDbDiskTempSize"`
	ProvDiskDockerSize                        string                 `mapstructure:"prov-db-disk-docker-size" toml:"prov-db-disk-docker-size" json:"provDbDiskDockerSize"`
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
	ProvDatadirVersion                        string                 `mapstructure:"prov-db-datadir-version" toml:"prov-db-datadir-version" json:"provDbDatadirVersion"`
	ProvDBLoadSQL                             string                 `mapstructure:"prov-db-load-sql" toml:"prov-db-load-sql" json:"provDbLoadSql"`
	ProvDBLoadCSV                             string                 `mapstructure:"prov-db-load-csv" toml:"prov-db-load-csv" json:"provDbLoadCsv"`
	ProvProxType                              string                 `mapstructure:"prov-proxy-service-type" toml:"prov-proxy-service-type" json:"provProxyServiceType"`
	ProvProxAgents                            string                 `mapstructure:"prov-proxy-agents" toml:"prov-proxy-agents" json:"provProxyAgents"`
	ProvProxAgentsFailover                    string                 `mapstructure:"prov-proxy-agents-failover" toml:"prov-proxy-agents-failover" json:"provProxyAgentsFailover"`
	ProvProxMem                               string                 `mapstructure:"prov-proxy-memory" toml:"prov-proxy-memory" json:"provProxyMemory"`
	ProvProxCores                             string                 `mapstructure:"prov-proxy-cpu-cores" toml:"prov-proxy-cpu-cores" json:"provProxyCpuCores"`
	ProvProxDisk                              string                 `mapstructure:"prov-proxy-disk-size" toml:"prov-proxy-disk-size" json:"provProxyDiskSize"`
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
	ProvProxTags                              string                 `mapstructure:"prov-proxy-tags" toml:"prov-proxy-tags" json:"provProxyTags"`
	ProvSphinxAgents                          string                 `mapstructure:"prov-sphinx-agents" toml:"prov-sphinx-agents" json:"provSphinxAgents"`
	ProvSphinxImg                             string                 `mapstructure:"prov-sphinx-docker-img" toml:"prov-sphinx-docker-img" json:"provSphinxDockerImg"`
	ProvSphinxMem                             string                 `mapstructure:"prov-sphinx-memory" toml:"prov-sphinx-memory" json:"provSphinxMemory"`
	ProvSphinxDisk                            string                 `mapstructure:"prov-sphinx-disk-size" toml:"prov-sphinx-disk-size" json:"provSphinxDiskSize"`
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
	ProvDockerDaemonPrivate                   bool                   `mapstructure:"prov-docker-daemon-private" toml:"prov-docker-daemon-private" json:"provDockerDaemonPrivate"`
	ProvServicePlan                           string                 `mapstructure:"prov-service-plan" toml:"prov-service-plan" json:"provServicePlan"`
	ProvServicePlanRegistry                   string                 `mapstructure:"prov-service-plan-registry" toml:"prov-service-plan-registry" json:"provServicePlanRegistry"`
	ProvDbBootstrapScript                     string                 `mapstructure:"prov-db-bootstrap-script" toml:"prov-db-bootstrap-script" json:"provDbBootstrapScript"`
	ProvProxyBootstrapScript                  string                 `mapstructure:"prov-proxy-bootstrap-script" toml:"prov-proxy-bootstrap-script" json:"provProxyBootstrapScript"`
	ProvDbCleanupScript                       string                 `mapstructure:"prov-db-cleanup-script" toml:"prov-db-cleanup-script" json:"provDbCleanupScript"`
	ProvProxyCleanupScript                    string                 `mapstructure:"prov-proxy-cleanup-script" toml:"prov-proxy-cleanup-script" json:"provProxyCleanupScript"`
	ProvDbStartScript                         string                 `mapstructure:"prov-db-start-script" toml:"prov-db-start-script" json:"provDbStartScript"`
	ProvProxyStartScript                      string                 `mapstructure:"prov-proxy-start-script" toml:"prov-proxy-start-script" json:"provProxyStartScript"`
	ProvDbStopScript                          string                 `mapstructure:"prov-db-stop-script" toml:"prov-db-stop-script" json:"provDbStopScript"`
	ProvProxyStopScript                       string                 `mapstructure:"prov-proxy-stop-script" toml:"prov-proxy-stop-script" json:"provProxyStopScript"`
	ProvDBCompliance                          string                 `mapstructure:"prov-db-compliance" toml:"prov-db-compliance" json:"provDBCompliance"`
	ProvProxyCompliance                       string                 `mapstructure:"prov-proxy-compliance" toml:"prov-proxy-compliance" json:"provProxyCompliance"`
	APIUsers                                  string                 `mapstructure:"api-credentials" toml:"api-credentials" json:"apiCredentials"`
	APIUsersExternal                          string                 `mapstructure:"api-credentials-external" toml:"api-credentials-external" json:"apiCredentialsExternal"`
	APIUsersACLAllow                          string                 `mapstructure:"api-credentials-acl-allow" toml:"api-credentials-acl-allow" json:"apiCredentialsACLAllow"`
	APIUsersACLDiscard                        string                 `mapstructure:"api-credentials-acl-discard" toml:"api-credentials-acl-discard" json:"apiCredentialsACLDiscard"`
	APISecureConfig                           bool                   `mapstructure:"api-credentials-secure-config" toml:"api-credentials-secure-config" json:"apiCredentialsSecureConfig"`
	APIPort                                   string                 `mapstructure:"api-port" toml:"api-port" json:"apiPort"`
	APIBind                                   string                 `mapstructure:"api-bind" toml:"api-bind" json:"apiBind"`
	APIPublicURL                              string                 `mapstructure:"api-public-url" toml:"api-public-url" json:"apiPublicUrl"`
	APIHttpsBind                              bool                   `mapstructure:"api-https-bind" toml:"api-secure" json:"apiHttpsBind"`
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
	BackupLogicalCron                         string                 `mapstructure:"scheduler-db-servers-logical-backup-cron" toml:"scheduler-db-servers-logical-backup-cron" json:"schedulerDbServersLogicalBackupCron"`
	BackupPhysicalCron                        string                 `mapstructure:"scheduler-db-servers-physical-backup-cron" toml:"scheduler-db-servers-physical-backup-cron" json:"schedulerDbServersPhysicalBackupCron"`
	BackupDatabaseLogCron                     string                 `mapstructure:"scheduler-db-servers-logs-cron" toml:"scheduler-db-servers-logs-cron" json:"schedulerDbServersLogsCron"`
	BackupDatabaseOptimizeCron                string                 `mapstructure:"scheduler-db-servers-optimize-cron" toml:"scheduler-db-servers-optimize-cron" json:"schedulerDbServersOptimizeCron"`
	BackupDatabaseAnalyzeCron                 string                 `mapstructure:"scheduler-db-servers-analyze-cron" toml:"scheduler-db-servers-analyze-cron" json:"schedulerDbServersAnalyzeCron"`
	BackupSaveScript                          string                 `mapstructure:"backup-save-script" toml:"backup-save-script" json:"backupSaveScript"`
	BackupLoadScript                          string                 `mapstructure:"backup-load-script" toml:"backup-load-script" json:"backupLoadScript"`
	CompressBackups                           bool                   `mapstructure:"compress-backups" toml:"compress-backups" json:"compressBackups"`
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
	BackupKeepHourly                          int                    `mapstructure:"backup-keep-hourly" toml:"backup-keep-hourly" json:"backupKeepHourly"`
	BackupKeepDaily                           int                    `mapstructure:"backup-keep-daily" toml:"backup-keep-daily" json:"backupKeepDaily"`
	BackupKeepWeekly                          int                    `mapstructure:"backup-keep-weekly" toml:"backup-keep-weekly" json:"backupKeepWeekly"`
	BackupKeepMonthly                         int                    `mapstructure:"backup-keep-monthly" toml:"backup-keep-monthly" json:"backupKeepMonthly"`
	BackupKeepYearly                          int                    `mapstructure:"backup-keep-yearly" toml:"backup-keep-yearly" json:"backupKeepYearly"`
	BackupRestic                              bool                   `mapstructure:"backup-restic" toml:"backup-restic" json:"backupRestic"`
	BackupResticBinaryPath                    string                 `mapstructure:"backup-restic-binary-path" toml:"backup-restic-binary-path" json:"backupResticBinaryPath"`
	BackupResticAwsAccessKeyId                string                 `mapstructure:"backup-restic-aws-access-key-id" toml:"backup-restic-aws-access-key-id" json:"-"`
	BackupResticAwsAccessSecret               string                 `mapstructure:"backup-restic-aws-access-secret"  toml:"backup-restic-aws-access-secret" json:"-"`
	BackupResticRepository                    string                 `mapstructure:"backup-restic-repository" toml:"backup-restic-repository" json:"backupResticRepository"`
	BackupResticPassword                      string                 `mapstructure:"backup-restic-password"  toml:"backup-restic-password" json:"-"`
	BackupResticAws                           bool                   `mapstructure:"backup-restic-aws"  toml:"backup-restic-aws" json:"backupResticAws"`
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
	BackupMyLoaderOptions                     string                 `mapstructure:"backup-myloader-options" toml:"backup-myloader-options" json:"backupMyloaderOptions"`
	BackupMyDumperOptions                     string                 `mapstructure:"backup-mydumper-options" toml:"backup-mydumper-options" json:"backupMyDumperOptions"`
	BackupMysqlbinlogPath                     string                 `mapstructure:"backup-mysqlbinlog-path" toml:"backup-mysqlbinlog-path" json:"backupMysqlbinlogPath"`
	BackupMysqlclientPath                     string                 `mapstructure:"backup-mysqlclient-path" toml:"backup-mysqlclient-path" json:"backupMysqlclientgPath"`
	BackupBinlogs                             bool                   `mapstructure:"backup-binlogs" toml:"backup-binlogs" json:"backupBinlogs"`
	BackupBinlogsKeep                         int                    `mapstructure:"backup-binlogs-keep" toml:"backup-binlogs-keep" json:"backupBinlogsKeep"`
	BinlogCopyMode                            string                 `mapstructure:"binlog-copy-mode" toml:"binlog-copy-mode" json:"binlogCopyMode"`
	BinlogCopyScript                          string                 `mapstructure:"binlog-copy-script" toml:"binlog-copy-script" json:"binlogCopyScript"`
	BinlogRotationScript                      string                 `mapstructure:"binlog-rotation-script" toml:"binlog-rotation-script" json:"binlogRotationScript"`
	BinlogParseMode                           string                 `mapstructure:"binlog-parse-mode" toml:"binlog-parse-mode" json:"binlogParseMode"`
	BackupLockDDL                             bool                   `mapstructure:"backup-lockddl" toml:"backup-lockddl" json:"backupLockDDL"`
	ClusterConfigPath                         string                 `mapstructure:"cluster-config-file" toml:"-" json:"-"`
	VaultServerAddr                           string                 `mapstructure:"vault-server-addr" toml:"vault-server-addr" json:"vaultServerAddr"`
	VaultRoleId                               string                 `mapstructure:"vault-role-id" toml:"vault-role-id" json:"vaultRoleId"`
	VaultSecretId                             string                 `mapstructure:"vault-secret-id" toml:"vault-secret-id" json:"vaultSecretId"`
	VaultMode                                 string                 `mapstructure:"vault-mode" toml:"vault-mode" json:"vaultMode"`
	VaultMount                                string                 `mapstructure:"vault-mount" toml:"vault-mount" json:"vaultMount"`
	VaultAuth                                 string                 `mapstructure:"vault-auth" toml:"vault-auth" json:"vaultAuth"`
	VaultToken                                string                 `mapstructure:"vault-token" toml:"vault-token" json:"vaultToken"`
	LogVault                                  bool                   `mapstructure:"log-vault" toml:"log-vault" json:"logVault"`
	LogVaultLevel                             int                    `mapstructure:"log-vault-level" toml:"log-vault-level" json:"logVaultLevel"`
	GitUrl                                    string                 `mapstructure:"git-url" toml:"git-url" json:"gitUrl"`
	GitUsername                               string                 `mapstructure:"git-username" toml:"git-username" json:"gitUsername"`
	GitAccesToken                             string                 `mapstructure:"git-acces-token" toml:"git-acces-token" json:"-"`
	GitMonitoringTicker                       int                    `mapstructure:"git-monitoring-ticker" toml:"git-monitoring-ticker" json:"gitMonitoringTicker"`
	Cloud18                                   bool                   `mapstructure:"cloud18"  toml:"cloud18" json:"cloud18"`
	Cloud18Domain                             string                 `mapstructure:"cloud18-domain" toml:"cloud18-domain" json:"cloud18Domain"`
	Cloud18SubDomain                          string                 `mapstructure:"cloud18-sub-domain" toml:"cloud18-sub-domain" json:"cloud18SubDomain"`
	Cloud18SubDomainZone                      string                 `mapstructure:"cloud18-sub-domain-zone" toml:"cloud18-sub-domain-zone" json:"cloud18SubDomainZone"`
	Cloud18Shared                             bool                   `mapstructure:"cloud18-shared"  toml:"cloud18-shared" json:"cloud18Shared"`
	Cloud18GitUser                            string                 `mapstructure:"cloud18-gitlab-user" toml:"cloud18-gitlab-user" json:"cloud18GitUser"`
	Cloud18GitPassword                        string                 `mapstructure:"cloud18-gitlab-password" toml:"cloud18-gitlab-password" json:"-"`
	Cloud18PlatformDescription                string                 `mapstructure:"cloud18-platform-description"  toml:"cloud18-platform-description" json:"cloud18PlatformDescription"`
	LogSecrets                                bool                   `mapstructure:"log-secrets"  toml:"log-secrets" json:"-"`
	Secrets                                   map[string]Secret      `json:"-"`
	SecretKey                                 []byte                 `json:"-"`
	ImmuableFlagMap                           map[string]interface{} `json:"-"`
	DynamicFlagMap                            map[string]interface{} `json:"-"`
	DefaultFlagMap                            map[string]interface{} `json:"-"`
	OAuthProvider                             string                 `mapstructure:"api-oauth-provider-url" toml:"api-oauth-provider-url" json:"apiOAuthProvider"`
	OAuthClientID                             string                 `mapstructure:"api-oauth-client-id" toml:"api-oauth-client-id" json:"apiOAuthClientID"`
	OAuthClientSecret                         string                 `mapstructure:"api-oauth-client-secret" toml:"api-oauth-client-secret" json:"apiOAuthClientSecret"`
	CacheStaticMaxAge                         int                    `mapstructure:"cache-static-max-age" toml:"cache-static-max-age" json:"-"`
	TokenTimeout                              int                    `scope:"server" mapstructure:"api-token-timeout" toml:"api-token-timeout" json:"apiTokenTimeout"`
	JobLogBatchSize                           int                    `mapstructure:"job-log-batch-size" toml:"job-log-batch-size" json:"jobLogBatchSize"`
	//OAuthRedirectURL                          string                 `mapstructure:"api-oauth-redirect-url" toml:"git-url" json:"-"`
	//	BackupResticStoragePolicy                  string `mapstructure:"backup-restic-storage-policy"  toml:"backup-restic-storage-policy" json:"backupResticStoragePolicy"`
	//ProvMode                           string `mapstructure:"prov-mode" toml:"prov-mode" json:"provMode"` //InitContainer vs API

}

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

// Compliance created in OpenSVC collector and exported as JSON
type Compliance struct {
	Filtersets []struct {
		ID    uint   `json:"id"`
		Stats bool   `json:"fset_stats"`
		Name  string `json:"fset_name"`
	} `json:"filtersets"`
	Rulesets []struct {
		ID        uint   `json:"id"`
		Name      string `json:"ruleset_name"`
		Filter    string `json:"fset_name"`
		Variables []struct {
			Value string `json:"var_value"`
			Class string `json:"var_class"`
			Name  string `json:"var_name"`
		} `json:"variables"`
	} `json:"rulesets"`
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
	EndTimestamp   time.Time `json:"start_timestamp" db:"start_timestamp"`
}

type ConfVersion struct {
	ConfInit     Config `json:"-"`
	ConfDecode   Config `json:"-"`
	ConfDynamic  Config `json:"-"`
	ConfImmuable Config `json:"-"`
}

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
	Id           int    `json:"id,string"`
	Plan         string `json:"plan"`
	DbMemory     int    `json:"dbmemory,string"`
	DbCores      int    `json:"dbcores,string"`
	DbDataSize   int    `json:"dbdatasize,string"`
	DbSystemSize int    `json:"dbSystemSize,string"`
	DbIops       int    `json:"dbiops,string"`
	PrxDataSize  int    `json:"prxdatasize,string"`
	PrxCores     int    `json:"prxcores,string"`
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

const (
	GrantDBStart                   string = "db-start"
	GrantDBStop                    string = "db-stop"
	GrantDBKill                    string = "db-kill"
	GrantDBOptimize                string = "db-optimize"
	GrantDBAnalyse                 string = "db-analyse"
	GrantDBReplication             string = "db-replication"
	GrantDBBackup                  string = "db-backup"
	GrantDBRestore                 string = "db-restore"
	GrantDBReadOnly                string = "db-readonly"
	GrantDBLogs                    string = "db-logs"
	GrantDBShowVariables           string = "db-show-variables"
	GrantDBShowStatus              string = "db-show-status"
	GrantDBShowSchema              string = "db-show-schema"
	GrantDBShowProcess             string = "db-show-process"
	GrantDBShowLogs                string = "db-show-logs"
	GrantDBCapture                 string = "db-capture"
	GrantDBMaintenance             string = "db-maintenance"
	GrantDBConfigCreate            string = "db-config-create"
	GrantDBConfigRessource         string = "db-config-ressource"
	GrantDBConfigFlag              string = "db-config-flag"
	GrantDBConfigGet               string = "db-config-get"
	GrantDBDebug                   string = "db-debug"
	GrantClusterCreate             string = "cluster-create"
	GrantClusterDelete             string = "cluster-delete"
	GrantClusterDrop               string = "cluster-drop"
	GrantClusterCreateMonitor      string = "cluster-create-monitor"
	GrantClusterDropMonitor        string = "cluster-drop-monitor"
	GrantClusterFailover           string = "cluster-failover"
	GrantClusterSwitchover         string = "cluster-switchover"
	GrantClusterRolling            string = "cluster-rolling"
	GrantClusterSettings           string = "cluster-settings"
	GrantClusterGrant              string = "cluster-grant"
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

	GrantProxyConfigCreate      string = "proxy-config-create"
	GrantProxyConfigGet         string = "proxy-config-get"
	GrantProxyConfigRessource   string = "proxy-config-ressource"
	GrantProxyConfigFlag        string = "proxy-config-flag"
	GrantProxyStart             string = "proxy-start"
	GrantProxyStop              string = "proxy-stop"
	GrantProvClusterProvision   string = "prov-cluster-provision"
	GrantProvClusterUnprovision string = "prov-cluster-unprovision"
	GrantProvProxyProvision     string = "prov-proxy-provision"
	GrantProvProxyUnprovision   string = "prov-proxy-unprovision"
	GrantProvDBProvision        string = "prov-db-provision"
	GrantProvDBUnprovision      string = "prov-db-unprovision"
	GrantProvSettings           string = "prov-settings"
	GrantProvCluster            string = "prov-cluster"
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
)

/*
This is the list of task to be used in SSH
*/
const (
	ConstTaskXB        string = "xtrabackup"
	ConstTaskMB        string = "mariabackup"
	ConstTaskError     string = "error"
	ConstTaskSlowQuery string = "slowquery"
	ConstTaskZFS       string = "zfssnapback"
	ConstTaskOptimize  string = "optimize"
	ConstTaskReseedXB  string = "reseedxtrabackup"
	ConstTaskReseedMB  string = "reseedmariabackup"
	ConstTaskDump      string = "reseedmysqldump"
	ConstTaskFlashXB   string = "flashbackxtrabackup"
	ConstTaskFlashMB   string = "flashbackmariadbackup"
	ConstTaskFlashDump string = "flashbackmysqldump"
	ConstTaskStop      string = "stop"
	ConstTaskRestart   string = "restart"
	ConstTaskStart     string = "start"
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
	TopoUnknown             string = "unknown"
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
		"vault-token":                           {"", ""},
		"api-oauth-client-secret":               {"", ""}}

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
		if conf.LogConfigLoad {
			log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("GetDecryptedPassword: key(%s) value(%s) %s", key, value, conf.SecretKey)
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
			line := fmt.Sprintf("Key: %s, Decrypted Value: %s\n", key, decryptedValue)
			fmt.Fprintf(file, line)
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

func (conf *Config) LoadEncrytionKey() ([]byte, error) {
	sec, err := crypto.ReadKey(conf.MonitoringKeyPath)
	if err != nil {
		conf.SecretKey = nil
	}
	conf.SecretKey = sec
	return conf.SecretKey, err
}

func (conf *Config) GetEncryptedString(str string) string {
	p := crypto.Password{PlainText: str}
	var err error
	if conf.SecretKey != nil {
		p.Key, err = crypto.ReadKey(fmt.Sprintf("%v", conf.MonitoringKeyPath))
		if err != nil {
			return str
		}
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

func (conf *Config) CloneConfigFromGit(url string, user string, tok string, dir string) {

	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}
	if conf.LogGit {
		log.Printf("Clone from git : url %s, tok %s, dir %s\n", url, conf.PrintSecret(tok), dir)
	}

	path := dir
	if _, err := os.Stat(path + "/.git"); err == nil {

		// We instantiate a new repository targeting the given path (the .git folder)
		r, err := git.PlainOpen(path)
		if err != nil && conf.LogGit {
			log.Errorf("Git error : cannot PlainOpen : %s", err)
			return
		}

		// Get the working directory for the repository
		w, err := r.Worktree()
		if err != nil && conf.LogGit {
			log.Errorf("Git error : cannot Worktree : %s", err)
			return
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
		if err != nil && fmt.Sprintf("%v", err) != "already up-to-date" && conf.LogGit {
			log.Errorf("Git error : cannot Pull : %s", err)
		}

	} else {
		// Clone the given repository to the given directory
		//git_ex.Info("git clone %s %s --recursive", url, path)

		_, err := git.PlainClone(path, false, &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
		})

		if err != nil && conf.LogGit {
			log.Errorf("Git error : cannot Clone %s repository : %s", url, err)
		}
	}
}

func (conf *Config) PushConfigToGit(url string, tok string, user string, dir string, clusterList []string) {

	if conf.LogGit {
		log.Infof("Push to git : tok %s, dir %s, user %s, clustersList : %v\n", conf.PrintSecret(tok), dir, user, clusterList)
	}
	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}
	path := dir
	r, err := git.PlainOpen(path)
	if err != nil && conf.LogGit {
		log.Errorf("Git error : cannot PlainOpen : %s", err)
		return
	}

	w, err := r.Worktree()
	if err != nil && conf.LogGit {
		log.Errorf("Git error : cannot Worktree : %s", err)
		return
	}

	if len(clusterList) != 0 {
		for _, name := range clusterList {
			// Adds the new file to the staging area.
			err = w.AddGlob(name + "/*.toml")
			if err != nil && conf.LogGit {
				log.Errorf("Git error : cannot Add %s : %s", name+"/*.toml", err)
			}

			if _, err := os.Stat(conf.WorkingDir + "/" + name + "/agents.json"); !os.IsNotExist(err) {
				_, err = w.Add(name + "/agents.json")
				if err != nil && conf.LogGit {
					log.Errorf("Git error : cannot Add %s : %s", name+"/agents.json", err)
				}
				_, err = w.Add(name + "/queryrules.json")
				if err != nil && conf.LogGit {
					log.Errorf("Git error : cannot Add %s : %s", name+"/queryrules.json", err)
				}
			}
		}
	}

	if _, err := os.Stat(conf.WorkingDir + "/cloud18.toml"); !os.IsNotExist(err) {
		_, err = w.Add("cloud18.toml")
		if err != nil && conf.LogGit {
			log.Errorf("Git error : cannot Add cloud18.toml : %s", err)
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

	if err != nil && conf.LogGit {
		log.Errorf("Git error : cannot Commit : %s", err)
		return
	}

	err = w.Pull(&git.PullOptions{
		RemoteName: "origin",
		Auth:       auth,
		RemoteURL:  url,
		Force:      true,
	})

	if err != nil && fmt.Sprintf("%v", err) != "already up-to-date" && conf.LogGit {

		if err != nil && conf.LogGit {
			log.Errorf("Git error : cannot Pull %s repository : %s", url, err)
			//conf.PullByGitCli()
			//return
		}

	}

	// push using default options
	err = r.Push(&git.PushOptions{Auth: auth})
	if err != nil && conf.LogGit {
		log.Errorf("Git error : cannot Push : %s", err)

	}
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
func (conf *Config) GetBackupBinlogType() map[string]bool {
	return map[string]bool{
		ConstBackupBinlogTypeMysqlbinlog: true,
		ConstBackupBinlogTypeSSH:         true,
		ConstBackupBinlogTypeScript:      true,
	}
}

func (conf *Config) GetBinlogParseMode() map[string]bool {
	return map[string]bool{
		ConstBackupBinlogTypeMysqlbinlog: true,
		ConstBackupBinlogTypeGoMySQL:     true,
	}
}

func (conf *Config) GetBackupPhysicalType() map[string]bool {
	return map[string]bool{
		ConstBackupPhysicalTypeXtrabackup:  true,
		ConstBackupPhysicalTypeMariaBackup: true,
	}
}

func (conf *Config) GetBackupLogicalType() map[string]bool {
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

func (conf *Config) GetMonitorType() map[string]string {

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
	}
}

func (conf *Config) GetDiskType() map[string]string {

	return map[string]string{
		"loopback":  "loopback",
		"physical":  "physical",
		"pool":      "pool",
		"directory": "directory",
		"volume":    "volume",
	}
}

func (conf *Config) GetFSType() map[string]bool {

	return map[string]bool{
		"ext4": true,
		"zfs":  true,
		"xfs":  true,
		"aufs": true,
		"nfs":  false,
	}
}

func (conf *Config) GetSysbenchTests() map[string]bool {
	return map[string]bool{
		"oltp_read_write":       true,
		"oltp_read_only":        true,
		"oltp_update_non_index": true,
		"oltp_update_index":     true,
		"tpcc":                  true,
	}
}

func (conf *Config) GetVMType() map[string]bool {

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

func (conf *Config) GetPoolType() map[string]bool {

	return map[string]bool{
		"none":  true,
		"zpool": true,
		"lvm":   true,
	}
}

func (conf *Config) GetTopologyType() map[string]string {
	return map[string]string{
		"master-slave":            "master-slave",
		"binlog-server":           "binlog-server",
		"multi-tier-slave":        "multi-tier-slave",
		"multi-master":            "multi-master",
		"multi-master-ring":       "multi-master-ring",
		"multi-master-wsrep":      "multi-master-wsrep",
		"master-slave-pg-logical": "master-slave-pg-logical",
		"master-slave-pg-stream":  "master-slave-pg-stream",
		"unknown":                 "unknown",
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

func (conf *Config) GetGrantType() map[string]string {
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
		GrantDBDebug:                   GrantDBDebug,
		GrantClusterCreate:             GrantClusterCreate,
		GrantClusterDrop:               GrantClusterDrop,
		GrantClusterCreateMonitor:      GrantClusterCreateMonitor,
		GrantClusterDropMonitor:        GrantClusterDropMonitor,
		GrantClusterFailover:           GrantClusterFailover,
		GrantClusterSwitchover:         GrantClusterSwitchover,
		GrantClusterRolling:            GrantClusterRolling,
		GrantClusterSettings:           GrantClusterSettings,
		GrantClusterGrant:              GrantClusterGrant,
		GrantClusterReplication:        GrantClusterReplication,
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
	}
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

	if _, err := os.Stat(path + "/" + name + "/overwrite.toml"); os.IsNotExist(err) {
		log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("No monitoring saved config found %s", path+"/"+name+"/overwrite.toml")
		return err
	} else {
		log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "config"}).Infof("Parsing saved config from working directory %s", path+"/"+name+"/overwrite.toml")

		dynRead.AddConfigPath(path + "/" + name)
		err := dynRead.ReadInConfig()
		if err != nil {
			fmt.Printf("Could not read in config : " + path + "/" + name + "/overwrite.toml")
		}
		dynRead = dynRead.Sub("overwrite-" + name)
		//fmt.Printf("%v\n", dynRead.AllSettings())
		for _, f := range dynRead.AllKeys() {
			v := dynRead.Get(f)
			_, ok := ImmMap[f]
			if ok && v != nil && v != ImmMap[f] {
				_, ok := DefMap[f]
				if ok && v != DefMap[f] {
					dynMap[f] = dynRead.Get(f)
				}
				if !ok {
					dynMap[f] = dynRead.Get(f)
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

func IsScope(toml string, scope string) bool {
	tconfig := Config{}
	if tscope, ok := GetScope(tconfig, toml); ok {
		return tscope == scope
	}
	return false
}

func (conf *Config) ReadCloud18Config(viper *viper.Viper) {
	viper = viper.Sub("default")
	viper.SetConfigType("toml")

	if _, err := os.Stat(conf.WorkingDir + "/cloud18.toml"); os.IsNotExist(err) {
		//fmt.Printf("No monitoring saved config found " + conf.WorkingDir + "/cloud18.toml")
		return
	}
	fmt.Printf("Parsing saved config from working directory %s ", conf.WorkingDir+"/cloud18.toml")

	viper.SetConfigFile(conf.WorkingDir + "/cloud18.toml")
	err := viper.MergeInConfig()
	if err != nil {
		log.Error("Config error in " + conf.WorkingDir + "/cloud18.toml:" + err.Error())
	}

	viper.Unmarshal(&conf)

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
		}
	}

	return false
}

func (conf *Config) SetLogOutput(out io.Writer) {
	log.SetOutput(out)
}

func (conf *Config) ToLogrusLevel(l int) log.Level {
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
	Reseedmysqldump       bool `json:"reseedmysqldump"`
	Flashbackxtrabackup   bool `json:"flashbackxtrabackup"`
	Flashbackmariadbackup bool `json:"flashbackmariadbackup"`
	Flashbackmysqldump    bool `json:"flashbackmysqldump"`
	Stop                  bool `json:"stop"`
	Start                 bool `json:"start"`
	Restart               bool `json:"restart"`
}

type Task struct {
	Id     int64          `json:"id" db:"id"`
	Task   string         `json:"task" db:"task"`
	Port   int            `json:"port" db:"port"`
	Server string         `json:"server" db:"server"`
	Done   int            `json:"done" db:"done"`
	State  int            `json:"state" db:"state"`
	Result sql.NullString `json:"result,omitempty" db:"result"`
	Start  int64          `json:"start" db:"utc_start"`
	End    sql.NullInt64  `json:"end,omitempty" db:"utc_end"`
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

func (conf *Config) GetJobTypes() map[string]bool {
	var res = JobResult{}
	return GetLabelsAsMap(res)
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
	}
	return ""
}

// If task is about backup and reseed, it will use log backup stream else will use log task
func GetModuleNameForTask(task string) string {
	switch task {
	case ConstTaskXB, ConstTaskMB, ConstTaskReseedXB, ConstTaskReseedMB, ConstTaskDump, ConstTaskFlashXB, ConstTaskFlashMB, ConstTaskFlashDump:
		return ConstLogNameBackupStream
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
}
