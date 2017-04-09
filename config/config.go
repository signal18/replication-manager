// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package config

type Config struct {
	WorkingDir                         string `mapstructure:"working-directory"`
	ShareDir                           string `mapstructure:"share-directory"`
	User                               string `mapstructure:"user"`
	Hosts                              string `mapstructure:"hosts"`
	Socket                             string `mapstructure:"socket"`
	RplUser                            string `mapstructure:"rpluser"`
	Interactive                        bool   `mapstructure:"interactive"`
	Verbose                            bool   `mapstructure:"verbose"`
	PreScript                          string `mapstructure:"pre-failover-script"`
	PostScript                         string `mapstructure:"post-failover-script"`
	RejoinScript                       string `mapstructure:"rejoin-script"`
	PrefMaster                         string `mapstructure:"prefmaster"`
	IgnoreSrv                          string `mapstructure:"ignore-servers"`
	SwitchWaitKill                     int64  `mapstructure:"switchover-wait-kill"`
	SwitchWaitTrx                      int64  `mapstructure:"switchover-wait-trx"`
	SwitchWaitWrite                    int    `mapstructure:"switchover-wait-write-query"`
	SwitchGtidCheck                    bool   `mapstructure:"switchover-at-equal-gtid"`
	SwitchSync                         bool   `mapstructure:"switchover-at-sync"`
	SwitchMaxDelay                     int64  `mapstructure:"switchover-max-slave-delay"`
	ReadOnly                           bool   `mapstructure:"readonly"`
	Autorejoin                         bool   `mapstructure:"autorejoin"`
	AutorejoinFlashback                bool   `mapstructure:"autorejoin-flashback"`
	AutorejoinMysqldump                bool   `mapstructure:"autorejoin-mysqldump"`
	AutorejoinBackupBinlog             bool   `mapstructure:"autorejoin-backup-binlog"`
	AutorejoinSemisync                 bool   `mapstructure:"autorejoin-semisync"`
	LogFile                            string `mapstructure:"logfile"`
	MonitoringTicker                   int64  `mapstructure:"monitoring-ticker"`
	Timeout                            int    `mapstructure:"connect-timeout"`
	CheckType                          string `mapstructure:"check-type"`
	CheckReplFilter                    bool   `mapstructure:"check-replication-filters"`
	CheckBinFilter                     bool   `mapstructure:"check-binlog-filters"`
	ForceSlaveHeartbeat                bool   `mapstructure:"force-slave-heartbeat"`
	ForceSlaveHeartbeatTime            int    `mapstructure:"force-slave-heartbeat-time"`
	ForceSlaveHeartbeatRetry           int    `mapstructure:"force-slave-heartbeat-retry"`
	ForceSlaveGtid                     bool   `mapstructure:"force-slave-gtid-mode"`
	ForceSlaveNoGtid                   bool   `mapstructure:"force-slave-no-gtid-mode"`
	ForceSlaveSemisync                 bool   `mapstructure:"force-slave-semisync"`
	ForceSlaveReadOnly                 bool   `mapstructure:"force-slave-readonly"`
	ForceBinlogRow                     bool   `mapstructure:"force-binlog-row"`
	ForceBinlogAnnotate                bool   `mapstructure:"force-binlog-annotate"`
	ForceBinlogCompress                bool   `mapstructure:"force-binlog-compress"`
	ForceBinlogSlowqueries             bool   `mapstructure:"force-binlog-slowqueries"`
	ForceBinlogChecksum                bool   `mapstructure:"force-binlog-checksum"`
	ForceInmemoryBinlogCacheSize       bool   `mapstructure:"force-inmemory-binlog-cache-size"`
	ForceDiskRelayLogSizeLimit         bool   `mapstructure:"force-disk-relaylog-size-limit"`
	ForceDiskRelayLogSizeLimitSize     uint64 `mapstructure:"force-disk-relaylog-size-limit-size"`
	ForceSyncBinlog                    bool   `mapstructure:"force-sync-binlog"`
	ForceSyncInnoDB                    bool   `mapstructure:"force-sync-innodb"`
	ForceNoslaveBehind                 bool   `mapstructure:"force-noslave-behind"`
	RplChecks                          bool
	MasterConn                         string `mapstructure:"master-connection"`
	MultiMaster                        bool   `mapstructure:"multimaster"`
	MultiTierSlave                     bool   `mapstructure:"multi-tier-slave"`
	Spider                             bool   `mapstructure:"spider"`
	BindAddr                           string `mapstructure:"http-bind-address"`
	HttpPort                           string `mapstructure:"http-port"`
	HttpServ                           bool   `mapstructure:"http-server"`
	HttpRoot                           string `mapstructure:"http-root"`
	HttpAuth                           bool   `mapstructure:"http-auth"`
	HttpBootstrapButton                bool   `mapstructure:"http-bootstrap-button"`
	SessionLifeTime                    int    `mapstructure:"http-session-lifetime"`
	Daemon                             bool   `mapstructure:"daemon"`
	MailFrom                           string `mapstructure:"mail-from"`
	MailTo                             string `mapstructure:"mail-to"`
	MailSMTPAddr                       string `mapstructure:"mail-smtp-addr"`
	MasterConnectRetry                 int    `mapstructure:"master-connect-retry"`
	FailLimit                          int    `mapstructure:"failover-limit"`
	FailTime                           int64  `mapstructure:"failover-time-limit"`
	FailSync                           bool   `mapstructure:"failover-at-sync"`
	FailEventScheduler                 bool   `mapstructure:"failover-event-scheduler"`
	FailEventStatus                    bool   `mapstructure:"failover-event-status"`
	MaxFail                            int    `mapstructure:"failcount"`
	FailResetTime                      int64  `mapstructure:"failcount-reset-time"`
	FailMaxDelay                       int64  `mapstructure:"failover-max-slave-delay"`
	CheckFalsePositiveHeartbeat        bool   `mapstructure:"failover-falsepositive-heartbeat"`
	CheckFalsePositiveMaxscale         bool   `mapstructure:"failover-falsepositive-maxscale"`
	CheckFalsePositiveHeartbeatTimeout int    `mapstructure:"failover-falsepositive-heartbeat-timeout"`
	CheckFalsePositiveMaxscaleTimeout  int    `mapstructure:"failover-falsepositive-maxscale-timeout"`
	CheckFalsePositiveExternal         bool   `mapstructure:"failover-falsepositive-external"`
	CheckFalsePositiveExternalPort     int    `mapstructure:"failover-falsepositive-external-port"`
	Heartbeat                          bool   `mapstructure:"heartbeat-table"`
	MxsOn                              bool   `mapstructure:"maxscale"`
	MxsHost                            string `mapstructure:"maxscale-host"`
	MxsPort                            string `mapstructure:"maxscale-port"`
	MxsUser                            string `mapstructure:"maxscale-user"`
	MxsPass                            string `mapstructure:"maxscale-pass"`
	MxsWritePort                       int    `mapstructure:"maxscale-write-port"`
	MxsReadPort                        int    `mapstructure:"maxscale-read-port"`
	MxsReadWritePort                   int    `mapstructure:"maxscale-read-write-port"`
	MxsMaxinfoPort                     int    `mapstructure:"maxscale-maxinfo-port"`
	MxsBinlogOn                        bool   `mapstructure:"maxscale-binlog"`
	MxsBinlogPort                      int    `mapstructure:"maxscale-binlog-port"`
	MxsMonitor                         bool   `mapstructure:"maxscale-monitor"`
	MxsGetInfoMethod                   string `mapstructure:"maxscale-get-info-method"`
	HaproxyOn                          bool   `mapstructure:"haproxy"`
	HaproxyWritePort                   int    `mapstructure:"haproxy-write-port"`
	HaproxyReadPort                    int    `mapstructure:"haproxy-read-port"`
	HaproxyStatPort                    int    `mapstructure:"haproxy-stat-port"`
	HaproxyWriteBindIp                 string `mapstructure:"haproxy-ip-write-bind"`
	HaproxyReadBindIp                  string `mapstructure:"haproxy-ip-read-bind"`
	HaproxyBinaryPath                  string `mapstructure:"haproxy-binary-path"`
	KeyPath                            string `mapstructure:"keypath"`
	LogLevel                           int    `mapstructure:"log-level"`
	Test                               bool   `mapstructure:"test"`
	Topology                           string `mapstructure:"topology"` // use by bootstrap
	GraphiteMetrics                    bool   `mapstructure:"graphite-metrics"`
	GraphiteEmbedded                   bool   `mapstructure:"graphite-embedded"`
	GraphiteCarbonHost                 string `mapstructure:"graphite-carbon-host"`
	GraphiteCarbonPort                 int    `mapstructure:"graphite-carbon-port"`
	GraphiteCarbonApiPort              int    `mapstructure:"graphite-carbon-api-port"`
	GraphiteCarbonServerPort           int    `mapstructure:"graphite-carbon-server-port"`
	GraphiteCarbonLinkPort             int    `mapstructure:"graphite-carbon-link-port"`
	GraphiteCarbonPicklePort           int    `mapstructure:"graphite-carbon-pickle-port"`
	GraphiteCarbonPprofPort            int    `mapstructure:"graphite-carbon-pprof-port"`
	SysbenchBinaryPath                 string `mapstructure:"sysbench-binary-path"`
	SysbenchTime                       int    `mapstructure:"sysbench-time"`
	SysbenchThreads                    int    `mapstructure:"sysbench-threads"`
	MariaDBBinaryPath                  string `mapstructure:"mariadb-binary-path"`
	Arbitration                        bool   `mapstructure:"arbitration-external"`
	ArbitrationSasSecret               string `mapstructure:"arbitration-external-secret"`
	ArbitrationSasHosts                string `mapstructure:"arbitration-external-hosts"`
	ArbitrationSasUniqueId             int    `mapstructure:"arbitration-external-unique-id"`
	ArbitrationPeerHosts               string `mapstructure:"arbitration-peer-hosts"`
	ReplicationSSL                     bool   `mapstructure:"replication-use-ssl"`
	FailForceGtid                      bool   //suspicious code
	RegTestStopCluster                 bool   //used by regtest to stop cluster
}
