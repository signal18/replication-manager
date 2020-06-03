// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package config

import (
	"database/sql"
	"encoding/json"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Version                                   string `mapstructure:"-" toml:"-" json:"-"`
	FullVersion                               string `mapstructure:"-" toml:"-" json:"-"`
	GoOS                                      string `mapstructure:"goos" toml:"-" json:"-"`
	GoArch                                    string `mapstructure:"goarch" toml:"-" json:"-"`
	WithTarball                               string `mapstructure:"-" toml:"-" json:"-"`
	MemProfile                                string `mapstructure:"-" toml:"-" json:"-"`
	Include                                   string `mapstructure:"include" toml:"-" json:"-"`
	BaseDir                                   string `mapstructure:"monitoring-basedir" toml:"monitoring-basedir" json:"monitoringBasedir"`
	WorkingDir                                string `mapstructure:"monitoring-datadir" toml:"monitoring-datadir" json:"monitoringDatadir"`
	ShareDir                                  string `mapstructure:"monitoring-sharedir" toml:"monitoring-sharedir" json:"monitoringSharedir"`
	ConfDir                                   string `mapstructure:"monitoring-confdir" toml:"monitoring-confdir" json:"monitoringConfdir"`
	ConfRewrite                               bool   `mapstructure:"monitoring-save-config" toml:"monitoring-save-config" json:"monitoringSaveConfig"`
	MonitoringSSLCert                         string `mapstructure:"monitoring-ssl-cert" toml:"monitoring-ssl-cert" json:"monitoringSSLCert"`
	MonitoringSSLKey                          string `mapstructure:"monitoring-ssl-key" toml:"monitoring-ssl-key" json:"monitoringSSLKey"`
	MonitoringKeyPath                         string `mapstructure:"monitoring-key-path" toml:"monitoring-key-path" json:"monitoringKeyPath"`
	MonitoringTicker                          int64  `mapstructure:"monitoring-ticker" toml:"monitoring-ticker" json:"monitoringTicker"`
	MonitorWaitRetry                          int64  `mapstructure:"monitoring-wait-retry" toml:"monitoring-wait-retry" json:"monitoringWaitRetry"`
	Socket                                    string `mapstructure:"monitoring-socket" toml:"monitoring-socket" json:"monitoringSocket"`
	TunnelHost                                string `mapstructure:"monitoring-tunnel-host" toml:"monitoring-tunnel-host" json:"monitoringTunnelHost"`
	TunnelCredential                          string `mapstructure:"monitoring-tunnel-credential" toml:"monitoring-tunnel-credential" json:"monitoringTunnelCredential"`
	TunnelKeyPath                             string `mapstructure:"monitoring-tunnel-key-path" toml:"monitoring-tunnel-key-path" json:"monitoringTunnelKeyPath"`
	MonitorAddress                            string `mapstructure:"monitoring-address" toml:"monitoring-address" json:"monitoringAddress"`
	MonitorWriteHeartbeat                     bool   `mapstructure:"monitoring-write-heartbeat" toml:"monitoring-write-heartbeat" json:"monitoringWriteHeartbeat"`
	MonitorWriteHeartbeatCredential           string `mapstructure:"monitoring-write-heartbeat-credential" toml:"monitoring-write-heartbeat-credential" json:"monitoringWriteHeartbeatCredential"`
	MonitorVariableDiff                       bool   `mapstructure:"monitoring-variable-diff" toml:"monitoring-variable-diff" json:"monitoringVariableDiff"`
	MonitorSchemaChange                       bool   `mapstructure:"monitoring-schema-change" toml:"monitoring-schema-change" json:"monitoringSchemaChange"`
	MonitorQueryRules                         bool   `mapstructure:"monitoring-query-rules" toml:"monitoring-query-rules" json:"monitoringQueryRules"`
	MonitorSchemaChangeScript                 string `mapstructure:"monitoring-schema-change-script" toml:"monitoring-schema-change-script" json:"monitoringSchemaChangeScript"`
	MonitorProcessList                        bool   `mapstructure:"monitoring-processlist" toml:"monitoring-processlist" json:"monitoringProcesslist"`
	MonitorQueries                            bool   `mapstructure:"monitoring-queries" toml:"monitoring-queries" json:"monitoringQueries"`
	MonitorPFS                                bool   `mapstructure:"monitoring-performance-schema" toml:"monitoring-performance-schema" json:"monitoringPerformanceSchema"`
	MonitorInnoDBStatus                       bool   `mapstructure:"monitoring-innodb-status" toml:"monitoring-innodb-status" json:"monitoringInnoDBStatus"`
	MonitorLongQueryWithProcess               bool   `mapstructure:"monitoring-long-query-with-process" toml:"monitoring-long-query-with-process" json:"monitoringLongQueryWithProcess"`
	MonitorLongQueryTime                      int    `mapstructure:"monitoring-long-query-time" toml:"monitoring-long-query-time" json:"monitoringLongQueryTime"`
	MonitorLongQueryScript                    string `mapstructure:"monitoring-long-query-script" toml:"monitoring-long-query-script" json:"monitoringLongQueryScript"`
	MonitorLongQueryWithTable                 bool   `mapstructure:"monitoring-long-query-with-table" toml:"monitoring-long-query-with-table" json:"monitoringLongQueryWithTable"`
	MonitorLongQueryLogLength                 int    `mapstructure:"monitoring-long-query-log-length" toml:"monitoring-long-query-log-length" json:"monitoringLongQueryLogLength"`
	MonitorErrorLogLength                     int    `mapstructure:"monitoring-erreur-log-length" toml:"monitoring-erreur-log-length" json:"monitoringErreurLogLength"`
	MonitorCapture                            bool   `mapstructure:"monitoring-capture" toml:"monitoring-capture" json:"monitoringCapture"`
	MonitorCaptureFileKeep                    int    `mapstructure:"monitoring-capture-file-keep" toml:"monitoring-capture-file-keep" json:"monitoringCaptureFileKeep"`
	MonitorDiskUsage                          bool   `mapstructure:"monitoring-disk-usage" toml:"monitoring-disk-usage" json:"monitoringDiskUsage"`
	MonitorDiskUsagePct                       int    `mapstructure:"monitoring-disk-usage-pct" toml:"monitoring-disk-usage-pct" json:"monitoringDiskUsagePct"`
	MonitorCaptureTrigger                     string `mapstructure:"monitoring-capture-trigger" toml:"monitoring-capture-trigger" json:"monitoringCaptureTrigger"`
	MonitorIgnoreError                        string `mapstructure:"monitoring-ignore-errors" toml:"monitoring-ignore-errors" json:"monitoringIgnoreErrors"`
	MonitorTenant                             string `mapstructure:"monitoring-tenant" toml:"monitoring-tenant" json:"monitoringTenant"`
	Interactive                               bool   `mapstructure:"interactive" toml:"-" json:"interactive"`
	Verbose                                   bool   `mapstructure:"verbose" toml:"verbose" json:"verbose"`
	LogFile                                   string `mapstructure:"log-file" toml:"log-file" json:"logFile"`
	LogSyslog                                 bool   `mapstructure:"log-syslog" toml:"log-syslog" json:"logSyslog"`
	LogLevel                                  int    `mapstructure:"log-level" toml:"log-level" json:"logLevel"`
	LogRotateMaxSize                          int    `mapstructure:"log-rotate-max-size" toml:"log-rotate-max-size" json:"logRotateMaxSize"`
	LogRotateMaxBackup                        int    `mapstructure:"log-rotate-max-backup" toml:"log-rotate-max-backup" json:"logRotateMaxBackup"`
	LogRotateMaxAge                           int    `mapstructure:"log-rotate-max-age" toml:"log-rotate-max-age" json:"logRotateMaxAge"`
	LogSST                                    bool   `mapstructure:"log-sst" toml:"log-sst" json:"logSst"` // internal replication-manager sst
	LogHeartbeat                              bool   `mapstructure:"log-heartbeat" toml:"log-heartbeat" json:"logHeartbeat"`
	LogSQLInMonitoring                        bool   `mapstructure:"log-sql-in-monitoring"  toml:"log-sql-in-monitoring" json:"logSqlInMonitoring"`
	LogFailedElection                         bool   `mapstructure:"log-failed-election"  toml:"log-failed-election" json:"logFailedElection"`
	User                                      string `mapstructure:"db-servers-credential" toml:"db-servers-credential" json:"dbServersCredential"`
	Hosts                                     string `mapstructure:"db-servers-hosts" toml:"db-servers-hosts" json:"dbServersHosts"`
	HostsDelayed                              string `mapstructure:"replication-delayed-hosts", toml:"replication-delayed-hosts" json:"replicationDelayedHosts"`
	HostsDelayedTime                          int    `mapstructure:"replication-delayed-time", toml:"replication-delayed-time" json:"replicationDelayedTime"`
	DBServersTLSUseGeneratedCertificate       bool   `mapstructure:"db-servers-tls-use-generated-cert" toml:"db-servers-tls-use-generated-cert" json:"dbServersUseGeneratedCert"`
	HostsTLSCA                                string `mapstructure:"db-servers-tls-ca-cert" toml:"db-servers-tls-ca-cert" json:"dbServersTlsCaCert"`
	HostsTLSKEY                               string `mapstructure:"db-servers-tls-client-key" toml:"db-servers-tls-client-key" json:"dbServersTlsClientKey"`
	HostsTLSCLI                               string `mapstructure:"db-servers-tls-client-cert" toml:"db-servers-tls-client-cert" json:"dbServersTlsClientCert"`
	PrefMaster                                string `mapstructure:"db-servers-prefered-master" toml:"db-servers-prefered-master" json:"dbServersPreferedMaster"`
	IgnoreSrv                                 string `mapstructure:"db-servers-ignored-hosts" toml:"db-servers-ignored-hosts" json:"dbServersIgnoredHosts"`
	IgnoreSrvRO                               string `mapstructure:"db-servers-ignored-readonly" toml:"db-servers-ignored-readonly" json:"dbServersIgnoredReadonly"`
	Timeout                                   int    `mapstructure:"db-servers-connect-timeout" toml:"db-servers-connect-timeout" json:"dbServersConnectTimeout"`
	ReadTimeout                               int    `mapstructure:"db-servers-read-timeout" toml:"db-servers-read-timeout" json:"dbServersReadTimeout"`
	DBServersLocality                         string `mapstructure:"db-servers-locality" toml:"db-servers-locality" json:"dbServersLocality"`
	PRXServersReadOnMaster                    bool   `mapstructure:"proxy-servers-read-on-master" toml:"proxy-servers-read-on-master" json:"proxyServersReadOnMaster"`
	PRXServersBackendCompression              bool   `mapstructure:"proxy-servers-backend-compression" toml:"proxy-servers-backend-compression" json:"proxyServersBackendCompression"`
	PRXServersBackendMaxReplicationLag        int    `mapstructure:"proxy-servers-backend-max-replication-lag" toml:"proxy-servers-backend--max-replication-lag" json:"proxyServersBackendMaxReplicationLag"`
	PRXServersBackendMaxConnections           int    `mapstructure:"proxy-servers-backend-max-connections" toml:"proxy-servers-backend--max-connections" json:"proxyServersBackendMaxConnections"`
	ClusterHead                               string `mapstructure:"cluster-head" toml:"cluster-head" json:"clusterHead"`
	MasterConnectRetry                        int    `mapstructure:"replication-master-connect-retry" toml:"replication-master-connect-retry" json:"replicationMasterConnectRetry"`
	RplUser                                   string `mapstructure:"replication-credential" toml:"replication-credential" json:"replicationCredential"`
	ReplicationErrorScript                    string `mapstructure:"replication-error-script" toml:"replication-error-script" json:"replicationErrorScript"`
	MasterConn                                string `mapstructure:"replication-source-name" toml:"replication-source-name" json:"replicationSourceName"`
	ReplicationSSL                            bool   `mapstructure:"replication-use-ssl" toml:"replication-use-ssl" json:"replicationUseSsl"`
	MultiMasterRing                           bool   `mapstructure:"replication-multi-master-ring" toml:"replication-multi-master-ring" json:"replicationMultiMasterRing"`
	MultiMasterWsrep                          bool   `mapstructure:"replication-multi-master-wsrep" toml:"replication-multi-master-wsrep" json:"replicationMultiMasterWsrep"`
	MultiMasterWsrepSSTMethod                 string `mapstructure:"replication-multi-master-wsrep-sst-method" toml:"replication-multi-master-wsrep-sst-method" json:"replicationMultiMasterWsrepSSTMethod"`
	MultiMaster                               bool   `mapstructure:"replication-multi-master" toml:"replication-multi-master" json:"replicationMultiMaster"`
	MultiTierSlave                            bool   `mapstructure:"replication-multi-tier-slave" toml:"replication-multi-tier-slave" json:"replicationMultiTierSlave"`
	MasterSlavePgStream                       bool   `mapstructure:"replication-master-slave-pg-stream" toml:"replication-master-slave-pg-stream" json:"replicationMasterSlavePgStream"`
	MasterSlavePgLogical                      bool   `mapstructure:"replication-master-slave-pg-logical" toml:"replication-master-slave-pg-logical" json:"replicationMasterSlavePgLogical"`
	ReplicationNoRelay                        bool   `mapstructure:"replication-master-slave-never-relay" toml:"replication-master-slave-never-relay" json:"replicationMasterSlaveNeverRelay"`
	ReplicationRestartOnSQLErrorMatch         string `mapstructure:"replication-restart-on-sqlerror-match" toml:"replication-restart-on-sqlerror-match" json:"eeplicationRestartOnSqlLErrorMatch"`
	SwitchWaitKill                            int64  `mapstructure:"switchover-wait-kill" toml:"switchover-wait-kill" json:"switchoverWaitKill"`
	SwitchWaitTrx                             int64  `mapstructure:"switchover-wait-trx" toml:"switchover-wait-trx" json:"switchoverWaitTrx"`
	SwitchWaitWrite                           int    `mapstructure:"switchover-wait-write-query" toml:"switchover-wait-write-query" json:"switchoverWaitWriteQuery"`
	SwitchGtidCheck                           bool   `mapstructure:"switchover-at-equal-gtid" toml:"switchover-at-equal-gtid" json:"switchoverAtEqualGtid"`
	SwitchSync                                bool   `mapstructure:"switchover-at-sync" toml:"switchover-at-sync" json:"switchoverAtSync"`
	SwitchMaxDelay                            int64  `mapstructure:"switchover-max-slave-delay" toml:"switchover-max-slave-delay" json:"switchoverMaxSlaveDelay"`
	SwitchSlaveWaitCatch                      bool   `mapstructure:"switchover-slave-wait-catch" toml:"switchover-slave-wait-catch" json:"switchoverSlaveWaitCatch"`
	SwitchSlaveWaitRouteChange                int    `mapstructure:"switchover-wait-route-change" toml:"switchover-wait-route-change" json:"switchoverWaitRouteChange"`
	SwitchDecreaseMaxConn                     bool   `mapstructure:"switchover-decrease-max-conn" toml:"switchover-decrease-max-conn" json:"switchoverDecreaseMaxConn"`
	SwitchDecreaseMaxConnValue                int64  `mapstructure:"switchover-decrease-max-conn-value" toml:"switchover-decrease-max-conn-value" json:"switchoverDecreaseMaxConnValue"`
	FailLimit                                 int    `mapstructure:"failover-limit" toml:"failover-limit" json:"failoverLimit"`
	PreScript                                 string `mapstructure:"failover-pre-script" toml:"failover-pre-script" json:"failoverPreScript"`
	PostScript                                string `mapstructure:"failover-post-script" toml:"failover-post-script" json:"failoverPostScript"`
	ReadOnly                                  bool   `mapstructure:"failover-readonly-state" toml:"failover-readonly-state" json:"failoverReadOnlyState"`
	SuperReadOnly                             bool   `mapstructure:"failover-superreadonly-state" toml:"failover-superreadonly-state" json:"failoverSuperReadOnlyState"`
	FailTime                                  int64  `mapstructure:"failover-time-limit" toml:"failover-time-limit" json:"failoverTimeLimit"`
	FailSync                                  bool   `mapstructure:"failover-at-sync" toml:"failover-at-sync" json:"failoverAtSync"`
	FailEventScheduler                        bool   `mapstructure:"failover-event-scheduler" toml:"failover-event-scheduler" json:"failoverEventScheduler"`
	FailEventStatus                           bool   `mapstructure:"failover-event-status" toml:"failover-event-status" json:"failoverEventStatus"`
	FailRestartUnsafe                         bool   `mapstructure:"failover-restart-unsafe" toml:"failover-restart-unsafe" json:"failoverRestartUnsafe"`
	FailResetTime                             int64  `mapstructure:"failcount-reset-time" toml:"failover-reset-time" json:"failoverResetTime"`
	FailMode                                  string `mapstructure:"failover-mode" toml:"failover-mode" json:"failoverMode"`
	FailMaxDelay                              int64  `mapstructure:"failover-max-slave-delay" toml:"failover-max-slave-delay" json:"failoverMaxSlaveDelay"`
	MaxFail                                   int    `mapstructure:"failover-falsepositive-ping-counter" toml:"failover-falsepositive-ping-counter" json:"failoverFalsePositivePingCounter"`
	CheckFalsePositiveHeartbeat               bool   `mapstructure:"failover-falsepositive-heartbeat" toml:"failover-falsepositive-heartbeat" json:"failoverFalsePositiveHeartbeat"`
	CheckFalsePositiveMaxscale                bool   `mapstructure:"failover-falsepositive-maxscale" toml:"failover-falsepositive-maxscale" json:"failoverFalsePositiveMaxscale"`
	CheckFalsePositiveHeartbeatTimeout        int    `mapstructure:"failover-falsepositive-heartbeat-timeout" toml:"failover-falsepositive-heartbeat-timeout" json:"failoverFalsePositiveHeartbeatTimeout"`
	CheckFalsePositiveMaxscaleTimeout         int    `mapstructure:"failover-falsepositive-maxscale-timeout" toml:"failover-falsepositive-maxscale-timeout" json:"failoverFalsePositiveMaxscaleTimeout"`
	CheckFalsePositiveExternal                bool   `mapstructure:"failover-falsepositive-external" toml:"failover-falsepositive-external" json:"failoverFalsePositiveExternal"`
	CheckFalsePositiveExternalPort            int    `mapstructure:"failover-falsepositive-external-port" toml:"failover-falsepositive-external-port" json:"failoverFalsePositiveExternalPort"`
	FailoverLogFileKeep                       int    `mapstructure:"failover-log-file-keep" toml:"failover-log-file-keep" json:"failoverLogFileKeep"`
	Autorejoin                                bool   `mapstructure:"autorejoin" toml:"autorejoin" json:"autorejoin"`
	Autoseed                                  bool   `mapstructure:"autoseed" toml:"autoseed" json:"autoseed"`
	AutorejoinFlashback                       bool   `mapstructure:"autorejoin-flashback" toml:"autorejoin-flashback" json:"autorejoinFlashback"`
	AutorejoinMysqldump                       bool   `mapstructure:"autorejoin-mysqldump" toml:"autorejoin-mysqldump" json:"autorejoinMysqldump"`
	AutorejoinZFSFlashback                    bool   `mapstructure:"autorejoin-zfs-flashback" toml:"autorejoin-zfs-flashback" json:"autorejoinZfsFlashback"`
	AutorejoinPhysicalBackup                  bool   `mapstructure:"autorejoin-physical-backup" toml:"autorejoin-physical-backup" json:"autorejoinPhysicalBackup"`
	AutorejoinLogicalBackup                   bool   `mapstructure:"autorejoin-logical-backup" toml:"autorejoin-logical-backup" json:"autorejoinLogicalBackup"`
	RejoinScript                              string `mapstructure:"autorejoin-script" toml:"autorejoin-script" json:"autorejoinScript"`
	AutorejoinBackupBinlog                    bool   `mapstructure:"autorejoin-backup-binlog" toml:"autorejoin-backup-binlog" json:"autorejoinBackupBinlog"`
	AutorejoinSemisync                        bool   `mapstructure:"autorejoin-flashback-on-sync" toml:"autorejoin-flashback-on-sync" json:"autorejoinFlashbackOnSync"`
	AutorejoinNoSemisync                      bool   `mapstructure:"autorejoin-flashback-on-unsync" toml:"autorejoin-flashback-on-unsync" json:"autorejoinFlashbackOnUnsync"`
	AutorejoinSlavePositionalHeartbeat        bool   `mapstructure:"autorejoin-slave-positional-heartbeat" toml:"autorejoin-slave-positional-heartbeat" json:"autorejoinSlavePositionalHeartbeat"`
	CheckType                                 string `mapstructure:"check-type" toml:"check-type" json:"checkType"`
	CheckReplFilter                           bool   `mapstructure:"check-replication-filters" toml:"check-replication-filters" json:"checkReplicationFilters"`
	CheckBinFilter                            bool   `mapstructure:"check-binlog-filters" toml:"check-binlog-filters" json:"checkBinlogFilters"`
	CheckGrants                               bool   `mapstructure:"check-grants" toml:"check-grants" json:"checkGrants"`
	RplChecks                                 bool   `mapstructure:"check-replication-state" toml:"check-replication-state" json:"checkReplicationState"`
	ForceSlaveHeartbeat                       bool   `mapstructure:"force-slave-heartbeat" toml:"force-slave-heartbeat" json:"forceSlaveHeartbeat"`
	ForceSlaveHeartbeatTime                   int    `mapstructure:"force-slave-heartbeat-time" toml:"force-slave-heartbeat-time" json:"forceSlaveHeartbeatTime"`
	ForceSlaveHeartbeatRetry                  int    `mapstructure:"force-slave-heartbeat-retry" toml:"force-slave-heartbeat-retry" json:"forceSlaveHeartbeatRetry"`
	ForceSlaveGtid                            bool   `mapstructure:"force-slave-gtid-mode" toml:"force-slave-gtid-mode" json:"forceSlaveGtidMode"`
	ForceSlaveGtidStrict                      bool   `mapstructure:"force-slave-gtid-mode-strict" toml:"force-slave-gtid-mode-strict" json:"forceSlaveGtidModeStrict"`
	ForceSlaveNoGtid                          bool   `mapstructure:"force-slave-no-gtid-mode" toml:"force-slave-no-gtid-mode" json:"forceSlaveNoGtidMode"`
	ForceSlaveSemisync                        bool   `mapstructure:"force-slave-semisync" toml:"force-slave-semisync" json:"forceSlaveSemisync"`
	ForceSlaveReadOnly                        bool   `mapstructure:"force-slave-readonly" toml:"force-slave-readonly" json:"forceSlaveReadonly"`
	ForceBinlogRow                            bool   `mapstructure:"force-binlog-row" toml:"force-binlog-row" json:"forceBinlogRow"`
	ForceBinlogAnnotate                       bool   `mapstructure:"force-binlog-annotate" toml:"force-binlog-annotate" json:"forceBinlogAnnotate"`
	ForceBinlogCompress                       bool   `mapstructure:"force-binlog-compress" toml:"force-binlog-compress" json:"forceBinlogCompress"`
	ForceBinlogSlowqueries                    bool   `mapstructure:"force-binlog-slowqueries" toml:"force-binlog-slowqueries" json:"forceBinlogSlowqueries"`
	ForceBinlogChecksum                       bool   `mapstructure:"force-binlog-checksum" toml:"force-binlog-checksum" json:"forceBinlogChecksum"`
	ForceInmemoryBinlogCacheSize              bool   `mapstructure:"force-inmemory-binlog-cache-size" toml:"force-inmemory-binlog-cache-size" json:"forceInmemoryBinlogCacheSize"`
	ForceDiskRelayLogSizeLimit                bool   `mapstructure:"force-disk-relaylog-size-limit" toml:"force-disk-relaylog-size-limit" json:"forceDiskRelaylogSizeLimit"`
	ForceDiskRelayLogSizeLimitSize            uint64 `mapstructure:"force-disk-relaylog-size-limit-size"  toml:"force-disk-relaylog-size-limit-size" json:"forceDiskRelaylogSizeLimitSize"`
	ForceSyncBinlog                           bool   `mapstructure:"force-sync-binlog" toml:"force-sync-binlog" json:"forceSyncBinlog"`
	ForceSyncInnoDB                           bool   `mapstructure:"force-sync-innodb" toml:"force-sync-innodb" json:"forceSyncInnodb"`
	ForceNoslaveBehind                        bool   `mapstructure:"force-noslave-behind" toml:"force-noslave-behind" json:"forceNoslaveBehind"`
	Spider                                    bool   `mapstructure:"spider" toml:"-" json:"-"`
	BindAddr                                  string `mapstructure:"http-bind-address" toml:"http-bind-address" json:"httpBindAdress"`
	HttpPort                                  string `mapstructure:"http-port" toml:"http-port" json:"httpPort"`
	HttpServ                                  bool   `mapstructure:"http-server" toml:"http-server" json:"httpServer"`
	HttpRoot                                  string `mapstructure:"http-root" toml:"http-root" json:"httpRoot"`
	HttpAuth                                  bool   `mapstructure:"http-auth" toml:"http-auth" json:"httpAuth"`
	HttpBootstrapButton                       bool   `mapstructure:"http-bootstrap-button" toml:"http-bootstrap-button" json:"httpBootstrapButton"`
	SessionLifeTime                           int    `mapstructure:"http-session-lifetime" toml:"http-session-lifetime" json:"httpSessionLifetime"`
	HttpRefreshInterval                       int    `mapstructure:"http-refresh-interval" toml:"http-refresh-interval" json:"httpRefreshInterval"`
	Daemon                                    bool   `mapstructure:"daemon" toml:"-" json:"-"`
	MailFrom                                  string `mapstructure:"mail-from" toml:"mail-from" json:"mailFrom"`
	MailTo                                    string `mapstructure:"mail-to" toml:"mail-to" json:"mailTo"`
	MailSMTPAddr                              string `mapstructure:"mail-smtp-addr" toml:"mail-smtp-addr" json:"mailSmtpAddr"`
	MailSMTPUser                              string `mapstructure:"mail-smtp-user" toml:"mail-smtp-user" json:"mailSmtpUser"`
	MailSMTPPassword                          string `mapstructure:"mail-smtp-password" toml:"mail-smtp-password" json:"mailSmtpPassword"`
	SlackURL                                  string `mapstructure:"alert-slack-url" toml:"alert-slack-url" json:"alertSlackUrl"`
	SlackChannel                              string `mapstructure:"alert-slack-channel" toml:"alert-slack-channel" json:"alertSlackChannel"`
	SlackUser                                 string `mapstructure:"alert-slack-user" toml:"alert-slack-user" json:"alertSlackUser"`
	Heartbeat                                 bool   `mapstructure:"heartbeat-table" toml:"heartbeat-table" json:"heartbeatTable"`
	ExtProxyOn                                bool   `mapstructure:"extproxy" toml:"extproxy" json:"extproxy"`
	ExtProxyVIP                               string `mapstructure:"extproxy-address" toml:"extproxy-address" json:"extproxyAddress"`
	MdbsProxyOn                               bool   `mapstructure:"shardproxy" toml:"shardproxy" json:"shardproxy"`
	MdbsProxyHosts                            string `mapstructure:"shardproxy-servers" toml:"shardproxy-servers" json:"shardproxyServers"`
	MdbsProxyUser                             string `mapstructure:"shardproxy-user" toml:"shardproxy-user" json:"shardproxyUser"`
	MdbsProxyCopyGrants                       bool   `mapstructure:"shardproxy-copy-grants" toml:"shardproxy-copy-grants" json:"shardproxyCopyGrants"`
	MdbsProxyLoadSystem                       bool   `mapstructure:"shardproxy-load-system" toml:"shardproxy-load-system" json:"shardproxyLoadSystem"`
	MdbsUniversalTables                       string `mapstructure:"shardproxy-universal-tables" toml:"shardproxy-universal-tables" json:"shardproxyUniversalTables"`
	MdbsIgnoreTables                          string `mapstructure:"shardproxy-ignore-tables" toml:"shardproxy-ignore-tables" json:"shardproxyIgnoreTables"`
	MxsOn                                     bool   `mapstructure:"maxscale" toml:"maxscale" json:"maxscale"`
	MxsHost                                   string `mapstructure:"maxscale-servers" toml:"maxscale-servers" json:"maxscaleServers"`
	MxsPort                                   string `mapstructure:"maxscale-port" toml:"maxscale-port" json:"maxscalePort"`
	MxsUser                                   string `mapstructure:"maxscale-user" toml:"maxscale-user" json:"maxscaleUser"`
	MxsPass                                   string `mapstructure:"maxscale-pass" toml:"maxscale-pass" json:"maxscalePass"`
	MxsWritePort                              int    `mapstructure:"maxscale-write-port" toml:"maxscale-write-port" json:"maxscaleWritePort"`
	MxsReadPort                               int    `mapstructure:"maxscale-read-port" toml:"maxscale-read-port" json:"maxscaleReadPort"`
	MxsReadWritePort                          int    `mapstructure:"maxscale-read-write-port" toml:"maxscale-read-write-port" json:"maxscaleReadWritePort"`
	MxsMaxinfoPort                            int    `mapstructure:"maxscale-maxinfo-port" toml:"maxscale-maxinfo-port" json:"maxscaleMaxinfoPort"`
	MxsBinlogOn                               bool   `mapstructure:"maxscale-binlog" toml:"maxscale-binlog" json:"maxscaleBinlog"`
	MxsBinlogPort                             int    `mapstructure:"maxscale-binlog-port" toml:"maxscale-binlog-port" json:"maxscaleBinlogPort"`
	MxsDisableMonitor                         bool   `mapstructure:"maxscale-disable-monitor" toml:"maxscale-disable-monitor" json:"maxscaleDisableMonitor"`
	MxsGetInfoMethod                          string `mapstructure:"maxscale-get-info-method" toml:"maxscale-get-info-method" json:"maxscaleGetInfoMethod"`
	MxsServerMatchPort                        bool   `mapstructure:"maxscale-server-match-port" toml:"maxscale-server-match-port" json:"maxscaleServerMatchPort"`
	MxsBinaryPath                             string `mapstructure:"maxscale-binary-path" toml:"maxscale-binary-path" json:"maxscalemBinaryPath"`
	MyproxyOn                                 bool   `mapstructure:"myproxy" toml:"myproxy" json:"myproxy"`
	MyproxyPort                               int    `mapstructure:"myproxy-port" toml:"myproxy-port" json:"myproxyPort"`
	MyproxyUser                               string `mapstructure:"myproxy-user" toml:"myproxy-user" json:"myproxyUser"`
	MyproxyPassword                           string `mapstructure:"myproxy-password" toml:"myproxy-password" json:"myproxyPassword"`
	HaproxyOn                                 bool   `mapstructure:"haproxy" toml:"haproxy" json:"haproxy"`
	HaproxyHosts                              string `mapstructure:"haproxy-servers" toml:"haproxy-servers" json:"haproxyServers"`
	HaproxyWritePort                          int    `mapstructure:"haproxy-write-port" toml:"haproxy-write-port" json:"haproxyWritePort"`
	HaproxyReadPort                           int    `mapstructure:"haproxy-read-port" toml:"haproxy-read-port" json:"haproxyReadPort"`
	HaproxyStatPort                           int    `mapstructure:"haproxy-stat-port" toml:"haproxy-stat-port" json:"haproxyStatPort"`
	HaproxyWriteBindIp                        string `mapstructure:"haproxy-ip-write-bind" toml:"haproxy-ip-write-bind" json:"haproxyIpWriteBind"`
	HaproxyReadBindIp                         string `mapstructure:"haproxy-ip-read-bind" toml:"haproxy-ip-read-bind" json:"haproxyIpReadBind"`
	HaproxyBinaryPath                         string `mapstructure:"haproxy-binary-path" toml:"haproxy-binary-path" json:"haproxyBinaryPath"`
	ProxysqlOn                                bool   `mapstructure:"proxysql" toml:"proxysql" json:"proxysql"`
	ProxysqlSaveToDisk                        bool   `mapstructure:"proxysql-save-to-disk" toml:"proxysql-save-to-disk" json:"proxysqlSaveToDisk"`
	ProxysqlHosts                             string `mapstructure:"proxysql-servers" toml:"proxysql-servers" json:"proxysqlServers"`
	ProxysqlHostsIPV6                         string `mapstructure:"proxysql-servers-ipv6" toml:"proxysql-servers-ipv6" json:"proxysqlServers-ipv6"`
	ProxysqlPort                              string `mapstructure:"proxysql-port" toml:"proxysql-port" json:"proxysqlPort"`
	ProxysqlAdminPort                         string `mapstructure:"proxysql-admin-port" toml:"proxysql-admin-port" json:"proxysqlAdminPort"`
	ProxysqlUser                              string `mapstructure:"proxysql-user" toml:"proxysql-user" json:"proxysqlUser"`
	ProxysqlPassword                          string `mapstructure:"proxysql-password" toml:"proxysql-password" json:"proxysqlPassword"`
	ProxysqlWriterHostgroup                   string `mapstructure:"proxysql-writer-hostgroup" toml:"proxysql-writer-hostgroup" json:"proxysqlWriterHostgroup"`
	ProxysqlReaderHostgroup                   string `mapstructure:"proxysql-reader-hostgroup" toml:"proxysql-reader-hostgroup" json:"proxysqlReaderHostgroup"`
	ProxysqlCopyGrants                        bool   `mapstructure:"proxysql-bootstrap-users" toml:"proxysql-bootstarp-users" json:"proxysqlBootstrapyUsers"`
	ProxysqlBootstrap                         bool   `mapstructure:"proxysql-bootstrap" toml:"proxysql-bootstrap" json:"proxysqlBootstrap"`
	ProxysqlBootstrapVariables                bool   `mapstructure:"proxysql-bootstrap-variables" toml:"proxysql-bootstrap-variables" json:"proxysqlBootstrapVariables"`
	ProxysqlBootstrapHG                       bool   `mapstructure:"proxysql-bootstrap-hostgroups" toml:"proxysql-bootstrap-hostgroups" json:"proxysqlBootstrapHostgroups"`
	ProxysqlBootstrapQueryRules               bool   `mapstructure:"proxysql-bootstrap-query-rules" toml:"proxysql-bootstrap-query-rules" json:"proxysqlBootstrapQueryRules"`
	ProxysqlMasterIsReader                    bool   `mapstructure:"proxysql-master-is-reader" toml:"proxysql-master-is-reader" json:"proxysqlMasterIsReader"`
	ProxysqlMultiplexing                      bool   `mapstructure:"proxysql-multiplexing" toml:"proxysql-multiplexing" json:"proxysqlMultiplexing"`
	ProxysqlBinaryPath                        string `mapstructure:"proxysql-binary-path" toml:"proxysql-binary-path" json:"proxysqlBinaryPath"`
	MysqlRouterOn                             bool   `mapstructure:"mysqlrouter" toml:"mysqlrouter" json:"mysqlrouter"`
	MysqlRouterHosts                          string `mapstructure:"mysqlrouter-servers" toml:"mysqlrouter-servers" json:"mysqlrouterServers"`
	MysqlRouterPort                           string `mapstructure:"mysqlrouter-port" toml:"mysqlrouter-port" json:"mysqlrouterPort"`
	MysqlRouterUser                           string `mapstructure:"mysqlrouter-user" toml:"mysqlrouter-user" json:"mysqlrouterUser"`
	MysqlRouterPass                           string `mapstructure:"mysqlrouter-pass" toml:"mysqlrouter-pass" json:"mysqlrouterPass"`
	MysqlRouterWritePort                      int    `mapstructure:"mysqlrouter-write-port" toml:"mysqlrouter-write-port" json:"mysqlrouterWritePort"`
	MysqlRouterReadPort                       int    `mapstructure:"mysqlrouter-read-port" toml:"mysqlrouter-read-port" json:"mysqlrouterReadPort"`
	MysqlRouterReadWritePort                  int    `mapstructure:"mysqlrouter-read-write-port" toml:"mysqlrouter-read-write-port" json:"mysqlrouterReadWritePort"`
	SphinxOn                                  bool   `mapstructure:"sphinx" toml:"sphinx" json:"sphinx"`
	SphinxHosts                               string `mapstructure:"sphinx-servers" toml:"sphinx-servers" json:"sphinxServers"`
	SphinxConfig                              string `mapstructure:"sphinx-config" toml:"sphinx-config" json:"sphinxConfig"`
	SphinxQLPort                              string `mapstructure:"sphinx-sql-port" toml:"sphinx-sql-port" json:"sphinxSqlPort"`
	SphinxPort                                string `mapstructure:"sphinx-port" toml:"sphinx-port" json:"sphinxPort"`
	RegistryConsul                            bool   `mapstructure:"registry-consul" toml:"registry-consul" json:"registryConsul"`
	RegistryHosts                             string `mapstructure:"registry-servers" toml:"registry-servers" json:"registryServers"`
	KeyPath                                   string `mapstructure:"keypath" toml:"-" json:"-"`
	Topology                                  string `mapstructure:"topology" toml:"-" json:"-"` // use by bootstrap
	GraphiteMetrics                           bool   `mapstructure:"graphite-metrics" toml:"graphite-metrics" json:"graphiteMetrics"`
	GraphiteEmbedded                          bool   `mapstructure:"graphite-embedded" toml:"graphite-embedded" json:"graphiteEmbedded"`
	GraphiteCarbonHost                        string `mapstructure:"graphite-carbon-host" toml:"graphite-carbon-host" json:"graphiteCarbonHost"`
	GraphiteCarbonPort                        int    `mapstructure:"graphite-carbon-port" toml:"graphite-carbon-port" json:"graphiteCarbonPort"`
	GraphiteCarbonApiPort                     int    `mapstructure:"graphite-carbon-api-port" toml:"graphite-carbon-api-port" json:"graphiteCarbonApiPort"`
	GraphiteCarbonServerPort                  int    `mapstructure:"graphite-carbon-server-port" toml:"graphite-carbon-server-port" json:"graphiteCarbonServerPort"`
	GraphiteCarbonLinkPort                    int    `mapstructure:"graphite-carbon-link-port" toml:"graphite-carbon-link-port" json:"graphiteCarbonLinkPort"`
	GraphiteCarbonPicklePort                  int    `mapstructure:"graphite-carbon-pickle-port" toml:"graphite-carbon-pickle-port" json:"graphiteCarbonPicklePort"`
	GraphiteCarbonPprofPort                   int    `mapstructure:"graphite-carbon-pprof-port" toml:"graphite-carbon-pprof-port" json:"graphiteCarbonPprofPort"`
	SysbenchBinaryPath                        string `mapstructure:"sysbench-binary-path" toml:"sysbench-binary-path" json:"sysbenchBinaryPath"`
	SysbenchV1                                bool   `mapstructure:"sysbench-v1" toml:"sysbench-v1" json:"sysbenchV1"`
	SysbenchTime                              int    `mapstructure:"sysbench-time" toml:"sysbench-time" json:"sysbenchTime"`
	SysbenchThreads                           int    `mapstructure:"sysbench-threads" toml:"sysbench-threads" json:"sysbenchThreads"`
	Arbitration                               bool   `mapstructure:"arbitration-external" toml:"arbitration-external" json:"arbitrationExternal"`
	ArbitrationSasSecret                      string `mapstructure:"arbitration-external-secret" toml:"arbitration-external-secret" json:"arbitrationExternalSecret"`
	ArbitrationSasHosts                       string `mapstructure:"arbitration-external-hosts" toml:"arbitration-external-hosts" json:"arbitrationExternalHosts"`
	ArbitrationSasUniqueId                    int    `mapstructure:"arbitration-external-unique-id" toml:"arbitration-external-unique-id" json:"arbitrationExternalUniqueId"`
	ArbitrationPeerHosts                      string `mapstructure:"arbitration-peer-hosts" toml:"arbitration-peer-hosts" json:"arbitrationPeerHosts"`
	ArbitrationFailedMasterScript             string `mapstructure:"arbitration-failed-master-script" toml:"arbitration-failed-master-script" json:"arbitrationFailedMasterScript"`
	ArbitratorAddress                         string `mapstructure:"arbitrator-bind-address" toml:"arbitrator-bind-address" json:"arbitratorBindAddress"`
	ArbitratorDriver                          string `mapstructure:"arbitrator-driver" toml:"arbitrator-driver" json:"arbitratorDriver"`
	FailForceGtid                             bool   `toml:"-" json:"-"` //suspicious code
	Test                                      bool   `mapstructure:"test" toml:"test" json:"test"`
	TestInjectTraffic                         bool   `mapstructure:"test-inject-traffic" toml:"test-inject-traffic" json:"testInjectTraffic"`
	Enterprise                                bool   `toml:"enterprise" json:"enterprise"` //used to talk to opensvc collector
	KubeConfig                                string `mapstructure:"kube-config" toml:"kube-config" json:"kubeConfig"`
	SlapOSConfig                              string `mapstructure:"slapos-config" toml:"slapos-config" json:"slaposConfig"`
	SlapOSDBPartitions                        string `mapstructure:"slapos-db-partitions" toml:"slapos-db-partitions" json:"slaposDbPartitions"`
	SlapOSProxySQLPartitions                  string `mapstructure:"slapos-proxysql-partitions" toml:"slapos-proxysql-partitions" json:"slaposProxysqlPartitions"`
	SlapOSHaProxyPartitions                   string `mapstructure:"slapos-haproxy-partitions" toml:"slapos-haproxy-partitions" json:"slaposHaproxyPartitions"`
	SlapOSMaxscalePartitions                  string `mapstructure:"slapos-maxscale-partitions" toml:"slapos-maxscale-partitions" json:"slaposMaxscalePartitions"`
	ProvHost                                  string `mapstructure:"opensvc-host" toml:"opensvc-host" json:"opensvcHost"`
	ProvOpensvcP12Certificate                 string `mapstructure:"opensvc-p12-certificate" toml:"opensvc-p12-certificat" json:"opensvcP12Certificate"`
	ProvOpensvcP12Secret                      string `mapstructure:"opensvc-p12-secret" toml:"opensvc-p12-secret" json:"opensvcP12Secret"`
	ProvOpensvcUseCollectorAPI                bool   `mapstructure:"opensvc-use-collector-api" toml:"opensvc-use-collector-api" json:"opensvcUseCollectorApi"`
	ProvRegister                              bool   `mapstructure:"opensvc-register" toml:"opensvc-register" json:"opensvcRegister"`
	ProvAdminUser                             string `mapstructure:"opensvc-admin-user" toml:"opensvc-admin-user" json:"opensvcAdminUser"`
	ProvUser                                  string `mapstructure:"opensvc-user" toml:"opensvc-user" json:"opensvcUser"`
	ProvCodeApp                               string `mapstructure:"opensvc-codeapp" toml:"opensvc-codeapp" json:"opensvcCodeapp"`
	ProvOrchestrator                          string `mapstructure:"prov-orchestrator" toml:"prov-orchestrator" json:"provOrchestrator"`
	ProvOrchestratorEnable                    string `mapstructure:"prov-orchestrator-enable" toml:"prov-orchestrator-enable" json:"provOrchestratorEnable"`
	ProvDBClientBasedir                       string `mapstructure:"prov-db-client-basedir" toml:"prov-db-client-basedir" json:"provDbClientBasedir"`
	ProvDBBinaryBasedir                       string `mapstructure:"prov-db-binary-basedir" toml:"prov-db-binary-basedir" json:"provDbBinaryBasedir"`
	ProvType                                  string `mapstructure:"prov-db-service-type" toml:"prov-db-service-type" json:"provDbServiceType"`
	ProvAgents                                string `mapstructure:"prov-db-agents" toml:"prov-db-agents" json:"provDbAgents"`
	ProvMem                                   string `mapstructure:"prov-db-memory" toml:"prov-db-memory" json:"provDbMemory"`
	ProvMemSharedPct                          string `mapstructure:"prov-db-memory-shared-pct" toml:"prov-db-memory-shared-pct" json:"provDbMemorySharedPct"`
	ProvMemThreadedPct                        string `mapstructure:"prov-db-memory-threaded-pct" toml:"prov-db-memory-threaded-pct" json:"provDbMemoryThreadedPct"`
	ProvIops                                  string `mapstructure:"prov-db-disk-iops" toml:"prov-db-disk-iops" json:"provDbDiskIops"`
	ProvMaxConnections                        int    `mapstructure:"prov-db-max-connections" toml:"prov-db-max-connections" json:"provDbMaxConnections"`
	ProvCores                                 string `mapstructure:"prov-db-cpu-cores" toml:"prov-db-cpu-cores" json:"provDbCpuCores"`
	ProvTags                                  string `mapstructure:"prov-db-tags" toml:"prov-db-tags" json:"provDbTags"`
	ProvDomain                                string `mapstructure:"prov-db-domain" toml:"prov-db-domain" json:"provDbDomain"`
	ProvDisk                                  string `mapstructure:"prov-db-disk-size" toml:"prov-db-disk-size" json:"provDbDiskSize"`
	ProvDiskSystemSize                        string `mapstructure:"prov-db-disk-system-size" toml:"prov-db-disk-system-size" json:"provDbDiskSystemSize"`
	ProvDiskTempSize                          string `mapstructure:"prov-db-disk-temp-size" toml:"prov-db-disk-temp-size" json:"provDbDiskTempSize"`
	ProvDiskDockerSize                        string `mapstructure:"prov-db-disk-docker-size" toml:"prov-db-disk-docker-size" json:"provDbDiskDockerSize"`
	ProvVolumeDocker                          string `mapstructure:"prov-db-volume-docker" toml:"prov-db-volume-docker" json:"provDbVolumeDocker"`
	ProvVolumeData                            string `mapstructure:"prov-db-volume-data" toml:"prov-db-volume-data" json:"provDbVolumeData"`
	ProvVolumeSystem                          string `mapstructure:"prov-db-volume-system" toml:"prov-db-volume-system" json:"provDbVolumeSystem"`
	ProvVolumeTemp                            string `mapstructure:"prov-db-volume-temp" toml:"prov-db-volume-temp" json:"provDbVolumeTemp"`
	ProvDiskFS                                string `mapstructure:"prov-db-disk-fs" toml:"prov-db-disk-fs" json:"provDbDiskFs"`
	ProvDiskFSCompress                        string `mapstructure:"prov-db-disk-fs-compress" toml:"prov-db-disk-fs-compress" json:"provDbDiskFsCompress"`
	ProvDiskPool                              string `mapstructure:"prov-db-disk-pool" toml:"prov-db-disk-pool" json:"provDbDiskPool"`
	ProvDiskDevice                            string `mapstructure:"prov-db-disk-device" toml:"prov-db-disk-device" json:"provDbDiskDevice"`
	ProvDiskType                              string `mapstructure:"prov-db-disk-type" toml:"prov-db-disk-type" json:"provDbDiskType"`
	ProvDiskSnapshot                          bool   `mapstructure:"prov-db-disk-snapshot-prefered-master" toml:"prov-db-disk-snapshot-prefered-master" json:"provDbDiskSnapshotPreferedMaster"`
	ProvDiskSnapshotKeep                      int    `mapstructure:"prov-db-disk-snapshot-keep" toml:"prov-db-disk-snapshot-keep" json:"provDbDiskSnapshotKeep"`
	ProvNetIface                              string `mapstructure:"prov-db-net-iface" toml:"prov-db-net-iface" json:"provDbNetIface"`
	ProvNetmask                               string `mapstructure:"prov-db-net-mask" toml:"prov-db-net-mask" json:"provDbNetMask"`
	ProvGateway                               string `mapstructure:"prov-db-net-gateway" toml:"prov-db-net-gateway" json:"provDbNetGateway"`
	ProvDbImg                                 string `mapstructure:"prov-db-docker-img" toml:"prov-db-docker-img" json:"provDbDockerImg"`
	ProvDatadirVersion                        string `mapstructure:"prov-db-datadir-version" toml:"prov-db-datadir-version" json:"provDbDatadirVersion"`
	ProvDBLoadSQL                             string `mapstructure:"prov-db-load-sql" toml:"prov-db-load-sql" json:"provDbLoadSql"`
	ProvDBLoadCSV                             string `mapstructure:"prov-db-load-csv" toml:"prov-db-load-csv" json:"provDbLoadCsv"`
	ProvProxType                              string `mapstructure:"prov-proxy-service-type" toml:"prov-proxy-service-type" json:"provProxyServiceType"`
	ProvProxAgents                            string `mapstructure:"prov-proxy-agents" toml:"prov-proxy-agents" json:"provProxyAgents"`
	ProvProxAgentsFailover                    string `mapstructure:"prov-proxy-agents-failover" toml:"prov-proxy-agents-failover" json:"provProxyAgentsFailover"`
	ProvProxMem                               string `mapstructure:"prov-proxy-memory" toml:"prov-proxy-memory" json:"provProxyMemory"`
	ProvProxCores                             string `mapstructure:"prov-proxy-cpu-cores" toml:"prov-proxy-cpu-cores" json:"provProxyCpuCores"`
	ProvProxDisk                              string `mapstructure:"prov-proxy-disk-size" toml:"prov-proxy-disk-size" json:"provProxyDiskSize"`
	ProvProxDiskFS                            string `mapstructure:"prov-proxy-disk-fs" toml:"prov-proxy-disk-fs" json:"provProxyDiskFs"`
	ProvProxDiskPool                          string `mapstructure:"prov-proxy-disk-pool" toml:"prov-proxy-disk-pool" json:"provProxyDiskPool"`
	ProvProxDiskDevice                        string `mapstructure:"prov-proxy-disk-device" toml:"prov-proxy-disk-device" json:"provProxyDiskDevice"`
	ProvProxDiskType                          string `mapstructure:"prov-proxy-disk-type" toml:"prov-proxy-disk-type" json:"provProxyDiskType"`
	ProvProxVolumeData                        string `mapstructure:"prov-proxy-volume-data" toml:"prov-proxy-volume-data" json:"provProxyVolumeData"`
	ProvProxNetIface                          string `mapstructure:"prov-proxy-net-iface" toml:"prov-proxy-net-iface" json:"provProxyNetIface"`
	ProvProxNetmask                           string `mapstructure:"prov-proxy-net-mask" toml:"prov-proxy-net-mask" json:"provProxyNetMask"`
	ProvProxGateway                           string `mapstructure:"prov-proxy-net-gateway" toml:"prov-proxy-net-gateway" json:"provProxyNetGateway"`
	ProvProxRouteAddr                         string `mapstructure:"prov-proxy-route-addr" toml:"prov-proxy-route-addr" json:"provProxyRouteAddr"`
	ProvProxRoutePort                         string `mapstructure:"prov-proxy-route-port" toml:"prov-proxy-route-port" json:"provProxyRoutePort"`
	ProvProxRouteMask                         string `mapstructure:"prov-proxy-route-mask" toml:"prov-proxy-route-mask" json:"provProxyRouteMask"`
	ProvProxRoutePolicy                       string `mapstructure:"prov-proxy-route-policy" toml:"prov-proxy-route-policy" json:"provProxyRoutePolicy"`
	ProvProxShardingImg                       string `mapstructure:"prov-proxy-docker-shardproxy-img" toml:"prov-proxy-docker-shardproxy-img" json:"provProxyDockerShardproxyImg"`
	ProvProxMaxscaleImg                       string `mapstructure:"prov-proxy-docker-maxscale-img" toml:"prov-proxy-docker-maxscale-img" json:"provProxyDockerMaxscaleImg"`
	ProvProxHaproxyImg                        string `mapstructure:"prov-proxy-docker-haproxy-img" toml:"prov-proxy-docker-haproxy-img" json:"provProxyDockerHaproxyImg"`
	ProvProxProxysqlImg                       string `mapstructure:"prov-proxy-docker-proxysql-img" toml:"prov-proxy-docker-proxysql-img" json:"provProxyDockerProxysqlImg"`
	ProvProxMysqlRouterImg                    string `mapstructure:"prov-proxy-docker-mysqlrouter-img" toml:"prov-proxy-docker-mysqlrouter-img" json:"provProxyDockerMysqlrouterImg"`
	ProvProxTags                              string `mapstructure:"prov-proxy-tags" toml:"prov-proxy-tags" json:"provProxyTags"`
	ProvSphinxAgents                          string `mapstructure:"prov-sphinx-agents" toml:"prov-sphinx-agents" json:"provSphinxAgents"`
	ProvSphinxImg                             string `mapstructure:"prov-sphinx-docker-img" toml:"prov-sphinx-docker-img" json:"provSphinxDockerImg"`
	ProvSphinxMem                             string `mapstructure:"prov-sphinx-memory" toml:"prov-sphinx-memory" json:"provSphinxMemory"`
	ProvSphinxDisk                            string `mapstructure:"prov-sphinx-disk-size" toml:"prov-sphinx-disk-size" json:"provSphinxDiskSize"`
	ProvSphinxCores                           string `mapstructure:"prov-sphinx-cpu-cores" toml:"prov-sphinx-cpu-cores" json:"provSphinxCpuCores"`
	ProvSphinxMaxChildren                     string `mapstructure:"prov-sphinx-max-childrens" toml:"prov-sphinx-max-childrens" json:"provSphinxMaxChildrens"`
	ProvSphinxDiskPool                        string `mapstructure:"prov-sphinx-disk-pool" toml:"prov-sphinx-disk-pool" json:"provSphinxDiskPool"`
	ProvSphinxDiskFS                          string `mapstructure:"prov-sphinx-disk-fs" toml:"prov-sphinx-disk-fs" json:"provSphinxDiskFs"`
	ProvSphinxDiskDevice                      string `mapstructure:"prov-sphinx-disk-device" toml:"prov-sphinx-disk-device" json:"provSphinxDiskDevice"`
	ProvSphinxDiskType                        string `mapstructure:"prov-sphinx-disk-type" toml:"prov-sphinx-disk-type" json:"provSphinxDiskType"`
	ProvSphinxTags                            string `mapstructure:"prov-sphinx-tags" toml:"prov-sphinx-tags" json:"provSphinxTags"`
	ProvSphinxCron                            string `mapstructure:"prov-sphinx-reindex-schedule" toml:"prov-sphinx-reindex-schedule" json:"provSphinxReindexSchedule"`
	ProvSphinxType                            string `mapstructure:"prov-sphinx-service-type" toml:"prov-sphinx-service-type" json:"provSphinxServiceType"`
	ProvSSLCa                                 string `mapstructure:"prov-tls-server-ca" toml:"prov-tls-server-ca" json:"provTlsServerCa"`
	ProvSSLCert                               string `mapstructure:"prov-tls-server-cert" toml:"prov-tls-server-cert" json:"provTlsServerCert"`
	ProvSSLKey                                string `mapstructure:"prov-tls-server-key" toml:"prov-tls-server-key" json:"provTlsServerKey"`
	ProvSSLCaUUID                             string `mapstructure:"prov-tls-server-ca-uuid" toml:"-" json:"-"`
	ProvSSLCertUUID                           string `mapstructure:"prov-tls-server-cert-uuid" toml:"-" json:"-"`
	ProvSSLKeyUUID                            string `mapstructure:"prov-tls-server-key-uuid" toml:"-" json:"-"`
	ProvNetCNI                                bool   `mapstructure:"prov-net-cni" toml:"prov-net-cni" json:"provNetCni"`
	ProvNetCNICluster                         string `mapstructure:"prov-net-cni-cluster" toml:"prov-net-cni-cluster" json:"provNetCniCluster"`
	ProvDockerDaemonPrivate                   bool   `mapstructure:"prov-docker-daemon-private" toml:"prov-docker-daemon-private" json:"provDockerDaemonPrivate"`
	ProvServicePlan                           string `mapstructure:"prov-service-plan" toml:"prov-service-plan" json:"provServicePlan"`
	ProvServicePlanRegistry                   string `mapstructure:"prov-service-plan-registry" toml:"prov-service-plan-registry" json:"provServicePlanRegistry"`
	APIUsers                                  string `mapstructure:"api-credentials" toml:"api-credentials" json:"apiCredentials"`
	APIUsersExternal                          string `mapstructure:"api-credentials-external" toml:"api-credentials-external" json:"apiCredentialsExternal"`
	APIUsersACLAllow                          string `mapstructure:"api-credentials-acl-allow" toml:"api-credentials-acl-allow" json:"apiCredentialsACLAllow"`
	APIUsersACLDiscard                        string `mapstructure:"api-credentials-acl-discard" toml:"api-credentials-acl-discard" json:"apiCredentialsACLDiscard"`
	APIPort                                   string `mapstructure:"api-port" toml:"api-port" json:"apiPort"`
	APIBind                                   string `mapstructure:"api-bind" toml:"api-bind" json:"apiBind"`
	APIHttpsBind                              bool   `mapstructure:"api-https-bind" toml:"api-secure" json:"apiHttpsBind"`
	AlertScript                               string `mapstructure:"alert-script" toml:"alert-script" json:"alertScript"`
	ConfigFile                                string `mapstructure:"config" toml:"-" json:"-"`
	MonitorScheduler                          bool   `mapstructure:"monitoring-scheduler" toml:"monitoring-scheduler" json:"monitoringScheduler"`
	SchedulerReceiverPorts                    string `mapstructure:"scheduler-db-servers-receiver-ports" toml:"scheduler--db-servers-receiver-ports" json:"schedulerDbServersReceiverPorts"`
	SchedulerBackupLogical                    bool   `mapstructure:"scheduler-db-servers-logical-backup" toml:"scheduler-db-servers-logical-backup" json:"schedulerDbServersLogicalBackup"`
	SchedulerBackupPhysical                   bool   `mapstructure:"scheduler-db-servers-physical-backup" toml:"scheduler-db-servers-physical-backup" json:"schedulerDbServersPhysicalBackup"`
	SchedulerDatabaseLogs                     bool   `mapstructure:"scheduler-db-servers-logs" toml:"scheduler-db-servers-logs" json:"schedulerDbServersLogs"`
	SchedulerDatabaseOptimize                 bool   `mapstructure:"scheduler-db-servers-optimize" toml:"scheduler-db-servers-optimize" json:"schedulerDbServersOptimize"`
	BackupLogicalCron                         string `mapstructure:"scheduler-db-servers-logical-backup-cron" toml:"scheduler-db-servers-logical-backup-cron" json:"schedulerDbServersLogicalBackupCron"`
	BackupPhysicalCron                        string `mapstructure:"scheduler-db-servers-physical-backup-cron" toml:"scheduler-db-servers-physical-backup-cron" json:"schedulerDbServersPhysicalBackupCron"`
	BackupDatabaseLogCron                     string `mapstructure:"scheduler-db-servers-logs-cron" toml:"scheduler-db-servers-logs-cron" json:"schedulerDbServersLogsCron"`
	BackupDatabaseOptimizeCron                string `mapstructure:"scheduler-db-servers-optimize-cron" toml:"scheduler-db-servers-optimize-cron" json:"schedulerDbServersOptimizeCron"`
	SchedulerDatabaseLogsTableRotate          bool   `mapstructure:"scheduler-db-servers-logs-table-rotate" toml:"scheduler-db-servers-logs-table-rotate" json:"schedulerDbServersLogsTableRotate"`
	SchedulerDatabaseLogsTableRotateCron      string `mapstructure:"scheduler-db-servers-logs-table-rotate-cron" toml:"scheduler-db-servers-logs-table-rotate-cron" json:"schedulerDbServersLogsTableRotateCron"`
	SchedulerMaintenanceDatabaseLogsTableKeep int    `mapstructure:"scheduler-db-servers-logs-table-keep" toml:"scheduler-db-servers-logs-table-keep" json:"schedulerDatabaseLogsTableKeep"`
	SchedulerSLARotateCron                    string `mapstructure:"scheduler-sla-rotate-cron" toml:"scheduler-sla-rotate-cron" json:"schedulerSlaRotateCron"`
	SchedulerRollingRestart                   bool   `mapstructure:"scheduler-rolling-restart" toml:"scheduler-rolling-restart" json:"schedulerRollingRestart"`
	SchedulerRollingRestartCron               string `mapstructure:"scheduler-rolling-restart-cron" toml:"scheduler-rolling-restart-cron" json:"schedulerRollingRestartCron"`
	SchedulerRollingReprov                    bool   `mapstructure:"scheduler-rolling-reprov" toml:"scheduler-rolling-reprov" json:"schedulerRollingReprov"`
	SchedulerRollingReprovCron                string `mapstructure:"scheduler-rolling-reprov-cron" toml:"scheduler-rolling-reprov-cron" json:"schedulerRollingReprovCron"`
	SchedulerJobsSSH                          bool   `mapstructure:"scheduler-jobs-ssh" toml:"scheduler-jobs-ssh" json:"schedulerJobsSsh"`
	SchedulerJobsSSHCron                      string `mapstructure:"scheduler-jobs-ssh-cron" toml:"scheduler-jobs-ssh-cron" json:"schedulerJobsSshCron"`
	Backup                                    bool   `mapstructure:"backup" toml:"backup" json:"backup"`
	BackupLogicalType                         string `mapstructure:"backup-logical-type" toml:"backup-logical-type" json:"backupLogicalType"`
	BackupLogicalLoadThreads                  int    `mapstructure:"backup-logical-load-threads" toml:"backup-logical-load-threads" json:"backupLogicalLoadThreads"`
	BackupLogicalDumpThreads                  int    `mapstructure:"backup-logical-dump-threads" toml:"backup-logical-dump-threads" json:"backupLogicalDumpThreads"`
	BackupLogicalDumpSystemTables             bool   `mapstructure:"backup-logical-dump-system-tables" toml:"backup-logical-dump-system-tables" json:"backupLogicalDumpSystemTables"`
	BackupPhysicalType                        string `mapstructure:"backup-physical-type" toml:"backup-physical-type" json:"backupPhysicalType"`
	BackupKeepHourly                          int    `mapstructure:"backup-keep-hourly" toml:"backup-keep-hourly" json:"backupKeepHourly"`
	BackupKeepDaily                           int    `mapstructure:"backup-keep-daily" toml:"backup-keep-daily" json:"backupKeepDaily"`
	BackupKeepWeekly                          int    `mapstructure:"backup-keep-weekly" toml:"backup-keep-weekly" json:"backupKeepWeekly"`
	BackupKeepMonthly                         int    `mapstructure:"backup-keep-monthly" toml:"backup-keep-monthly" json:"backupKeepMonthly"`
	BackupKeepYearly                          int    `mapstructure:"backup-keep-yearly" toml:"backup-keep-yearly" json:"backupKeepYearly"`
	BackupRestic                              bool   `mapstructure:"backup-restic" toml:"backup-restic" json:"backupRestic"`
	BackupResticBinaryPath                    string `mapstructure:"backup-restic-binary-path" toml:"backup-restic-binary-path" json:"backupResticBinaryPath"`
	BackupResticAwsAccessKeyId                string `mapstructure:"backup-restic-aws-access-key-id" toml:"backup-restic-aws-access-key-id" json:"-"`
	BackupResticAwsAccessSecret               string `mapstructure:"backup-restic-aws-access-secret"  toml:"backup-restic-aws-access-secret" json:"-"`
	BackupResticRepository                    string `mapstructure:"backup-restic-repository" toml:"backup-restic-repository" json:"backupResticRepository"`
	BackupResticPassword                      string `mapstructure:"backup-restic-password"  toml:"backup-restic-password" json:"-"`
	BackupResticAws                           bool   `mapstructure:"backup-restic-aws"  toml:"backup-restic-aws" json:"backupResticAws"`
	BackupStreaming                           bool   `mapstructure:"backup-streaming" toml:"backup-streaming" json:"backupStreaming"`
	BackupStreamingDebug                      bool   `mapstructure:"backup-streaming-debug" toml:"backup-streaming-debug" json:"backupStreamingDebug"`
	BackupStreamingAwsAccessKeyId             string `mapstructure:"backup-streaming-aws-access-key-id" toml:"backup-streaming-aws-access-key-id" json:"-"`
	BackupStreamingAwsAccessSecret            string `mapstructure:"backup-streaming-aws-access-secret"  toml:"backup-streaming-aws-access-secret" json:"-"`
	BackupStreamingEndpoint                   string `mapstructure:"backup-streaming-endpoint" toml:"backup-streaming-endpoint" json:"backupStreamingEndpoint"`
	BackupStreamingRegion                     string `mapstructure:"backup-streaming-region" toml:"backup-streaming-region" json:"backupStreamingRegion"`
	BackupStreamingBucket                     string `mapstructure:"backup-streaming-bucket" toml:"backup-streaming-bucket" json:"backupStreamingBucket"`
	BackupMysqldumpPath                       string `mapstructure:"backup-mysqldump-path" toml:"backup-mysqldump-path" json:"backupMysqldumpPath"`
	BackupMyDumperPath                        string `mapstructure:"backup-mydumper-path" toml:"backup-mydumper-path" json:"backupMydumperPath"`
	BackupMyLoaderPath                        string `mapstructure:"backup-myloader-path" toml:"backup-myloader-path" json:"backupMyloaderPath"`
	BackupMysqlbinlogPath                     string `mapstructure:"backup-mysqlbinlog-path" toml:"backup-mysqlbinlog-path" json:"backupMysqlbinlogPath"`
	BackupMysqlclientPath                     string `mapstructure:"backup-mysqlclient-path" toml:"backup-mysqlclient-path" json:"backupMysqlclientgPath"`
	BackupBinlogs                             bool   `mapstructure:"backup-binlogs" toml:"backup-binlogs" json:"backupBinlogs"`
	BackupBinlogsKeep                         int    `mapstructure:"backup-binlogs-keep" toml:"backup-binlogs-keep" json:"backupBinlogsKeep"`
	ClusterConfigPath                         string `mapstructure:"cluster-config-file" toml:"-" json:"-"`

	//	BackupResticStoragePolicy                 string `mapstructure:"backup-restic-storage-policy"  toml:"backup-restic-storage-policy" json:"backupResticStoragePolicy"`
	//ProvMode                           string `mapstructure:"prov-mode" toml:"prov-mode" json:"provMode"` //InitContainer vs API

}

type ConfigVariableType struct {
	Id        int    `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Label     string `json:"label"`
}

//Compliance created in OpenSVC collector and exported as JSON
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

const (
	ConstStreamingSubDir string = "backups"
)
const (
	ConstProxyMaxscale    string = "maxscale"
	ConstProxyHaproxy     string = "haproxy"
	ConstProxySqlproxy    string = "proxysql"
	ConstProxySpider      string = "shardproxy"
	ConstProxyExternal    string = "extproxy"
	ConstProxyMysqlrouter string = "mysqlrouter"
	ConstProxySphinx      string = "sphinx"
	ConstProxyMyProxy     string = "myproxy"
)

type ServicePlan struct {
	Id           int    `json:"id"`
	Plan         string `json:"plan"`
	DbMemory     int    `json:"dbmemory"`
	DbCores      int    `json:"dbcores"`
	DbDataSize   int    `json:"dbdatasize"`
	DbSystemSize int    `json:"dbSystemSize"`
	DbIops       int    `json:"dbiops"`
	PrxDataSize  int    `json:"prxdatasize"`
	PrxCores     int    `json:"prxcores"`
}

type DockerTag struct {
	Layer string `json:"layer"`
	Name  string `json:"name"`
}

type DockerRepo struct {
	Name  string      `json:"name"`
	Image string      `json:"image"`
	Tags  []DockerTag `json:"tags"`
}

type DockerRepos struct {
	Repos []DockerRepo `json:"repos"`
}

type Grant struct {
	Grant  string `json:"grant"`
	Enable bool   `json:"enable"`
}

const (
	GrantDBStart                 string = "db-start"
	GrantDBStop                  string = "db-stop"
	GrantDBKill                  string = "db-kill"
	GrantDBOptimize              string = "db-optimize"
	GrantDBAnalyse               string = "db-analyse"
	GrantDBReplication           string = "db-replication"
	GrantDBBackup                string = "db-backup"
	GrantDBRestore               string = "db-restore"
	GrantDBReadOnly              string = "db-readonly"
	GrantDBLogs                  string = "db-logs"
	GrantDBShowVariables         string = "db-show-variables"
	GrantDBShowStatus            string = "db-show-status"
	GrantDBShowSchema            string = "db-show-schema"
	GrantDBShowProcess           string = "db-show-process"
	GrantDBShowLogs              string = "db-show-logs"
	GrantDBCapture               string = "db-capture"
	GrantDBMaintenance           string = "db-maintenance"
	GrantDBConfigCreate          string = "db-config-create"
	GrantDBConfigRessource       string = "db-config-ressource"
	GrantDBConfigFlag            string = "db-config-flag"
	GrantDBConfigGet             string = "db-config-get"
	GrantDBDebug                 string = "db-debug"
	GrantClusterCreate           string = "cluster-create"
	GrantClusterDrop             string = "cluster-drop"
	GrantClusterCreateMonitor    string = "cluster-create-monitor"
	GrantClusterDropMonitor      string = "cluster-drop-monitor"
	GrantClusterFailover         string = "cluster-failover"
	GrantClusterSwitchover       string = "cluster-switchover"
	GrantClusterRolling          string = "cluster-rolling"
	GrantClusterSettings         string = "cluster-settings"
	GrantClusterGrant            string = "cluster-grant"
	GrantClusterChecksum         string = "cluster-checksum"
	GrantClusterSharding         string = "cluster-sharding"
	GrantClusterReplication      string = "cluster-replication"
	GrantClusterRotateKey        string = "cluster-rotate-keys"
	GrantClusterBench            string = "cluster-bench"
	GrantClusterProcess          string = "cluster-process" //Can ssh for jobs
	GrantClusterTest             string = "cluster-test"
	GrantClusterTraffic          string = "cluster-traffic"
	GrantClusterShowBackups      string = "cluster-show-backups"
	GrantClusterShowRoutes       string = "cluster-show-routes"
	GrantClusterShowGraphs       string = "cluster-show-graphs"
	GrantClusterShowAgents       string = "cluster-show-agents"
	GrantClusterShowCertificates string = "cluster-show-certificates"
	GrantClusterResetSLA         string = "cluster-reset-sla"
	GrantClusterDebug            string = "cluster-debug"
	GrantProxyConfigCreate       string = "proxy-config-create"
	GrantProxyConfigGet          string = "proxy-config-get"
	GrantProxyConfigRessource    string = "proxy-config-ressource"
	GrantProxyConfigFlag         string = "proxy-config-flag"
	GrantProxyStart              string = "proxy-start"
	GrantProxyStop               string = "proxy-stop"
	GrantProvClusterProvision    string = "prov-cluster-provision"
	GrantProvClusterUnprovision  string = "prov-cluster-unprovision"
	GrantProvProxyProvision      string = "prov-proxy-provision"
	GrantProvProxyUnprovision    string = "prov-proxy-unprovision"
	GrantProvDBProvision         string = "prov-db-provision"
	GrantProvDBUnprovision       string = "prov-db-unprovision"
	GrantProvSettings            string = "prov-settings"
	GrantProvCluster             string = "prov-cluster"
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
		GrantDBStart:                 GrantDBStart,
		GrantDBStop:                  GrantDBStop,
		GrantDBKill:                  GrantDBKill,
		GrantDBOptimize:              GrantDBOptimize,
		GrantDBAnalyse:               GrantDBAnalyse,
		GrantDBReplication:           GrantDBReplication,
		GrantDBBackup:                GrantDBBackup,
		GrantDBRestore:               GrantDBRestore,
		GrantDBReadOnly:              GrantDBReadOnly,
		GrantDBLogs:                  GrantDBLogs,
		GrantDBCapture:               GrantDBCapture,
		GrantDBMaintenance:           GrantDBMaintenance,
		GrantDBConfigCreate:          GrantDBConfigCreate,
		GrantDBConfigRessource:       GrantDBConfigRessource,
		GrantDBConfigFlag:            GrantDBConfigFlag,
		GrantDBConfigGet:             GrantDBConfigGet,
		GrantDBShowVariables:         GrantDBShowVariables,
		GrantDBShowStatus:            GrantDBShowStatus,
		GrantDBShowSchema:            GrantDBShowSchema,
		GrantDBShowProcess:           GrantDBShowProcess,
		GrantDBShowLogs:              GrantDBShowLogs,
		GrantDBDebug:                 GrantDBDebug,
		GrantClusterCreate:           GrantClusterCreate,
		GrantClusterDrop:             GrantClusterDrop,
		GrantClusterCreateMonitor:    GrantClusterCreateMonitor,
		GrantClusterDropMonitor:      GrantClusterDropMonitor,
		GrantClusterFailover:         GrantClusterFailover,
		GrantClusterSwitchover:       GrantClusterSwitchover,
		GrantClusterRolling:          GrantClusterRolling,
		GrantClusterSettings:         GrantClusterSettings,
		GrantClusterGrant:            GrantClusterGrant,
		GrantClusterReplication:      GrantClusterReplication,
		GrantClusterChecksum:         GrantClusterChecksum,
		GrantClusterSharding:         GrantClusterSharding,
		GrantClusterRotateKey:        GrantClusterRotateKey,
		GrantClusterBench:            GrantClusterBench,
		GrantClusterTest:             GrantClusterTest,
		GrantClusterTraffic:          GrantClusterTraffic,
		GrantClusterProcess:          GrantClusterProcess,
		GrantClusterDebug:            GrantClusterDebug,
		GrantClusterShowBackups:      GrantClusterShowBackups,
		GrantClusterShowAgents:       GrantClusterShowAgents,
		GrantClusterShowGraphs:       GrantClusterShowGraphs,
		GrantClusterShowRoutes:       GrantClusterShowRoutes,
		GrantClusterShowCertificates: GrantClusterShowCertificates,
		GrantClusterResetSLA:         GrantClusterResetSLA,
		GrantProxyConfigCreate:       GrantProxyConfigCreate,
		GrantProxyConfigGet:          GrantProxyConfigGet,
		GrantProxyConfigRessource:    GrantProxyConfigRessource,
		GrantProxyConfigFlag:         GrantProxyConfigFlag,
		GrantProxyStart:              GrantProxyStart,
		GrantProxyStop:               GrantProxyStop,
		GrantProvSettings:            GrantProvSettings,
		GrantProvCluster:             GrantProvCluster,
		GrantProvClusterProvision:    GrantProvClusterProvision,
		GrantProvClusterUnprovision:  GrantProvClusterUnprovision,
		GrantProvDBUnprovision:       GrantProvDBUnprovision,
		GrantProvDBProvision:         GrantProvDBProvision,
		GrantProvProxyProvision:      GrantProvProxyProvision,
		GrantProvProxyUnprovision:    GrantProvProxyUnprovision,
	}
}

func (conf *Config) GetDockerRepos(file string) ([]DockerRepo, error) {
	var repos DockerRepos
	jsonFile, err := os.Open(file)
	if err != nil {
		return repos.Repos, err
	}

	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	err = json.Unmarshal([]byte(byteValue), &repos)
	if err != nil {
		return repos.Repos, err
	}

	return repos.Repos, nil
}
