// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"bytes"
	"fmt"
	"hash/crc64"
	"io/ioutil"
	"os"
	"strconv"

	mysqllog "log"

	"github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/server"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	memprofile string
	// Version is the semantic version number, e.g. 1.0.1
	Version string
	// Provisoning to add flags for compile
	WithProvisioning      string = "ON"
	WithArbitration       string = "OFF"
	WithArbitrationClient string = "ON"
	WithProxysql          string = "ON"
	WithHaproxy           string = "ON"
	WithMaxscale          string = "ON"
	WithMariadbshardproxy string = "ON"
	WithMonitoring        string = "ON"
	WithMail              string = "ON"
	WithHttp              string = "ON"
	WithSpider            string
	WithEnforce           string = "ON"
	WithDeprecate         string = "ON"
	WithOpenSVC           string = "OFF"
	WithTarball           string
	WithMySQLRouter       string
	WithSphinx            string = "ON"
	WithBackup            string = "ON"
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build    string
	GoOS     string = "linux"
	GoArch   string = "amd64"
	conf     config.Config
	cfgGroup string
)

var RepMan *server.ReplicationManager

func init() {

	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().StringVar(&conf.ConfigFile, "config", "", "Configuration file (default is config.toml)")
	rootCmd.PersistentFlags().StringVar(&cfgGroup, "cluster", "", "Configuration group (default is none)")
	rootCmd.Flags().StringVar(&conf.KeyPath, "keypath", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	rootCmd.PersistentFlags().BoolVar(&conf.Verbose, "verbose", false, "Print detailed execution info")
	rootCmd.PersistentFlags().StringVar(&memprofile, "memprofile", "/tmp/repmgr.mprof", "Write a memory profile to a file readable by pprof")

	viper.BindPFlags(rootCmd.PersistentFlags())
	if conf.Verbose == true && conf.LogLevel == 0 {
		conf.LogLevel = 1
	}
	if conf.Verbose == false && conf.LogLevel > 0 {
		conf.Verbose = true
	}

}

func main() {

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "replication-manager",
	Short: "Replication Manager tool for MariaDB and MySQL",
	// Copyright 2017-2021 SIGNAL18 CLOUD SAS
	Long: `replication-manager allows users to monitor interactively MariaDB 10.x and MySQL GTID replication health
and trigger slave to master promotion (aka switchover), or elect a new master in case of failure (aka failover).`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the replication manager version number",
	Long:  `All software has versions. This is ours`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Replication Manager " + Version + " for MariaDB 10.x and MySQL 5.7 Series")
		fmt.Println("Full Version: ", FullVersion)
		fmt.Println("Build Time: ", Build)
	},
}

func init() {

	conf.GoArch = GoArch
	conf.GoOS = GoOS
	conf.Version = Version
	conf.FullVersion = FullVersion
	conf.MemProfile = memprofile
	conf.WithTarball = WithTarball
	conf.ProvOrchestrator = "local"
	var errLog = mysql.Logger(mysqllog.New(ioutil.Discard, "", 0))
	mysql.SetLogger(errLog)

	rootCmd.AddCommand(monitorCmd)
	if WithDeprecate == "ON" {
		//	initDeprecated() // not needed used alias in main
	}
	initRepmgrFlags(monitorCmd)
	if WithTarball == "ON" {
		monitorCmd.Flags().StringVar(&conf.BaseDir, "monitoring-basedir", "/usr/local/replication-manager", "Path to a basedir where data and share sub directory can be found")
		monitorCmd.Flags().StringVar(&conf.ConfDir, "monitoring-confdir", "/usr/local/replication-manager/etc", "Path to a config directory")
	} else {
		monitorCmd.Flags().StringVar(&conf.BaseDir, "monitoring-basedir", "system", "Path to a basedir where a data and share directory can be found")
		monitorCmd.Flags().StringVar(&conf.ConfDir, "monitoring-confdir", "/etc/replication-manager", "Path to a config directory")
	}

	if GoOS == "darwin" {
		monitorCmd.Flags().StringVar(&conf.ShareDir, "monitoring-sharedir", "/opt/replication-manager/share", "Path to share files")
	} else {
		monitorCmd.Flags().StringVar(&conf.ShareDir, "monitoring-sharedir", "/usr/share/replication-manager", "Path to share files")
	}
	monitorCmd.Flags().StringVar(&conf.WorkingDir, "monitoring-datadir", "/var/lib/replication-manager", "Path to write temporary and persistent files")
	monitorCmd.Flags().Int64Var(&conf.MonitoringTicker, "monitoring-ticker", 2, "Monitoring interval in seconds")
	monitorCmd.Flags().StringVar(&conf.TunnelHost, "monitoring-tunnel-host", "", "Bastion host to access to monitor topology via SSH tunnel host:22")
	monitorCmd.Flags().StringVar(&conf.TunnelCredential, "monitoring-tunnel-credential", "root:", "Credential Access to bastion host topology via SSH tunnel")
	monitorCmd.Flags().StringVar(&conf.TunnelKeyPath, "monitoring-tunnel-key-path", "/Users/apple/.ssh/id_rsa", "Tunnel private key path")
	monitorCmd.Flags().BoolVar(&conf.MonitorWriteHeartbeat, "monitoring-write-heartbeat", false, "Inject heartbeat into proxy or via external vip")
	monitorCmd.Flags().BoolVar(&conf.ConfRewrite, "monitoring-save-config", false, "Save configuration changes to <monitoring-datadir>/<cluster_name> ")
	monitorCmd.Flags().StringVar(&conf.MonitorWriteHeartbeatCredential, "monitoring-write-heartbeat-credential", "", "Database user:password to inject traffic into proxy or via external vip")
	monitorCmd.Flags().BoolVar(&conf.MonitorVariableDiff, "monitoring-variable-diff", true, "Monitor variable difference beetween nodes")
	monitorCmd.Flags().BoolVar(&conf.MonitorPFS, "monitoring-performance-schema", true, "Monitor performance schema")
	monitorCmd.Flags().BoolVar(&conf.MonitorInnoDBStatus, "monitoring-innodb-status", true, "Monitor innodb status")
	monitorCmd.Flags().StringVar(&conf.MonitorIgnoreError, "monitoring-ignore-errors", "", "Comma separated list of error or warning to ignore")
	monitorCmd.Flags().BoolVar(&conf.MonitorSchemaChange, "monitoring-schema-change", true, "Monitor schema change")
	monitorCmd.Flags().StringVar(&conf.MonitorSchemaChangeScript, "monitoring-schema-change-script", "", "Monitor schema change external script")
	monitorCmd.Flags().StringVar(&conf.MonitoringSSLCert, "monitoring-ssl-cert", "", "HTTPS & API TLS certificate")
	monitorCmd.Flags().StringVar(&conf.MonitoringSSLKey, "monitoring-ssl-key", "", "HTTPS & API TLS key")
	monitorCmd.Flags().StringVar(&conf.MonitoringKeyPath, "monitoring-key-path", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	monitorCmd.Flags().BoolVar(&conf.MonitorQueries, "monitoring-queries", true, "Monitor long queries")
	monitorCmd.Flags().BoolVar(&conf.MonitorPlugins, "monitoring-plugins", true, "Monitor installed plugins")
	monitorCmd.Flags().IntVar(&conf.MonitorLongQueryTime, "monitoring-long-query-time", 10000, "Long query time in ms")
	monitorCmd.Flags().BoolVar(&conf.MonitorQueryRules, "monitoring-query-rules", true, "Monitor query routing from proxies")
	monitorCmd.Flags().StringVar(&conf.MonitorLongQueryScript, "monitoring-long-query-script", "", "long query time external script")
	monitorCmd.Flags().BoolVar(&conf.MonitorLongQueryWithTable, "monitoring-long-query-with-table", false, "Use log_type table to fetch slow queries")
	monitorCmd.Flags().BoolVar(&conf.MonitorLongQueryWithProcess, "monitoring-long-query-with-process", true, "Use processlist to fetch slow queries")
	monitorCmd.Flags().IntVar(&conf.MonitorLongQueryLogLength, "monitoring-long-query-log-length", 200, "Number of slow queries to keep in monitor")
	monitorCmd.Flags().IntVar(&conf.MonitorErrorLogLength, "monitoring-erreur-log-length", 20, "Number of error log line to keep in monitor")
	monitorCmd.Flags().BoolVar(&conf.MonitorScheduler, "monitoring-scheduler", false, "Enable internal scheduler")
	monitorCmd.Flags().BoolVar(&conf.MonitorPause, "monitoring-pause", false, "Disable monitoring")
	monitorCmd.Flags().BoolVar(&conf.MonitorProcessList, "monitoring-processlist", true, "Enable capture 50 longuest process via processlist")
	monitorCmd.Flags().StringVar(&conf.MonitorAddress, "monitoring-address", "localhost", "How to contact this monitoring")
	monitorCmd.Flags().StringVar(&conf.MonitorTenant, "monitoring-tenant", "default", "Can be use to store multi tenant identifier")
	monitorCmd.Flags().Int64Var(&conf.MonitorWaitRetry, "monitoring-wait-retry", 30, "Retry this number of time before giving up state transition <999999")
	monitorCmd.Flags().BoolVar(&conf.LogSST, "log-sst", false, "Log open and close SST transfert")
	monitorCmd.Flags().BoolVar(&conf.LogHeartbeat, "log-heartbeat", false, "Log Heartbeat")
	monitorCmd.Flags().BoolVar(&conf.LogFailedElection, "log-failed-election", false, "Log failed election")
	monitorCmd.Flags().BoolVar(&conf.LogSQLInMonitoring, "log-sql-in-monitoring", false, "Log SQL queries send to servers in monitoring")
	monitorCmd.Flags().BoolVar(&conf.MonitorCapture, "monitoring-capture", true, "Enable capture on error for 5 monitor loops")
	monitorCmd.Flags().StringVar(&conf.MonitorCaptureTrigger, "monitoring-capture-trigger", "ERR00076,ERR00041", "List of errno triggering capture mode")
	monitorCmd.Flags().IntVar(&conf.MonitorCaptureFileKeep, "monitoring-capture-file-keep", 5, "Purge capture file keep that number of them")
	monitorCmd.Flags().StringVar(&conf.MonitoringAlertTrigger, "monitoring-alert-trigger", "ERR00027,ERR00042", "List of errno triggering an alert to be send")
	monitorCmd.Flags().StringVar(&conf.User, "db-servers-credential", "root:mariadb", "Database login, specified in the [user]:[password] format")
	monitorCmd.Flags().StringVar(&conf.Hosts, "db-servers-hosts", "", "Database hosts list to monitor, IP and port (optional), specified in the host:[port] format and separated by commas")
	monitorCmd.Flags().BoolVar(&conf.DBServersTLSUseGeneratedCertificate, "db-servers-tls-use-generated-cert", false, "Use the auto generated certificates to connect to database backend")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCA, "db-servers-tls-ca-cert", "", "Database TLS authority certificate")
	monitorCmd.Flags().StringVar(&conf.HostsTlsCliKey, "db-servers-tls-client-key", "", "Database TLS client key")
	monitorCmd.Flags().StringVar(&conf.HostsTlsCliCert, "db-servers-tls-client-cert", "", "Database TLS client certificate")
	monitorCmd.Flags().StringVar(&conf.HostsTlsSrvKey, "db-servers-tls-server-key", "", "Database TLS server key to push in config")
	monitorCmd.Flags().StringVar(&conf.HostsTlsSrvCert, "db-servers-tls-server-cert", "", "Database TLS server certificate to push in config")

	monitorCmd.Flags().IntVar(&conf.Timeout, "db-servers-connect-timeout", 5, "Database connection timeout in seconds")
	monitorCmd.Flags().IntVar(&conf.ReadTimeout, "db-servers-read-timeout", 3600, "Database read timeout in seconds")
	monitorCmd.Flags().StringVar(&conf.PrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")
	monitorCmd.Flags().StringVar(&conf.IgnoreSrv, "db-servers-ignored-hosts", "", "Database list of hosts to ignore in election")
	monitorCmd.Flags().StringVar(&conf.IgnoreSrvRO, "db-servers-ignored-readonly", "", "Database list of hosts not changing read only status")
	monitorCmd.Flags().StringVar(&conf.BackupServers, "db-servers-backup-hosts", "", "Database list of hosts to backup when set can backup a slave")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitKill, "switchover-wait-kill", 5000, "Switchover wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().IntVar(&conf.SwitchWaitWrite, "switchover-wait-write-query", 10, "Switchover is canceled if a write query is running for this time")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitTrx, "switchover-wait-trx", 10, "Switchover is cancel after this timeout in second if can't aquire FTWRL")
	monitorCmd.Flags().BoolVar(&conf.SwitchSync, "switchover-at-sync", false, "Switchover Only  when state semisync is sync for last status")
	monitorCmd.Flags().BoolVar(&conf.SwitchGtidCheck, "switchover-at-equal-gtid", false, "Switchover only when slaves are fully in sync")
	monitorCmd.Flags().BoolVar(&conf.SwitchSlaveWaitCatch, "switchover-slave-wait-catch", true, "Switchover wait for slave to catch with replication, not needed in GTID mode but enable to detect possible issues like witing on old master")
	monitorCmd.Flags().BoolVar(&conf.SwitchDecreaseMaxConn, "switchover-decrease-max-conn", true, "Switchover decrease max connection on old master")
	monitorCmd.Flags().BoolVar(&conf.SwitchoverCopyOldLeaderGtid, "switchover-copy-old-leader-gtid", false, "Switchover copy old leader GTID")
	monitorCmd.Flags().Int64Var(&conf.SwitchDecreaseMaxConnValue, "switchover-decrease-max-conn-value", 10, "Switchover decrease max connection to this value different according to flavor")
	monitorCmd.Flags().IntVar(&conf.SwitchSlaveWaitRouteChange, "switchover-wait-route-change", 2, "Switchover wait for unmanged proxy monitor to dicoverd new state")
	monitorCmd.Flags().StringVar(&conf.MasterConn, "replication-source-name", "", "Replication channel name to use for multisource")
	monitorCmd.Flags().StringVar(&conf.ReplicationMultisourceHeadClusters, "replication-multisource-head-clusters", "", "Multi source link to parent cluster, autodiscoverd but can be materialized for bootstraping replication")
	monitorCmd.Flags().StringVar(&conf.HostsDelayed, "replication-delayed-hosts", "", "Database hosts list that need delayed replication separated by commas")
	monitorCmd.Flags().IntVar(&conf.HostsDelayedTime, "replication-delayed-time", 3600, "Delayed replication time")

	monitorCmd.Flags().IntVar(&conf.MasterConnectRetry, "replication-master-connect-retry", 10, "Replication is define using this connection retry timeout")
	monitorCmd.Flags().StringVar(&conf.RplUser, "replication-credential", "", "Replication user in the [user]:[password] format")
	monitorCmd.Flags().BoolVar(&conf.ReplicationSSL, "replication-use-ssl", false, "Replication use SSL encryption to replicate from master")
	monitorCmd.Flags().BoolVar(&conf.MultiMaster, "replication-multi-master", false, "Multi-master topology")
	monitorCmd.Flags().BoolVar(&conf.MultiMasterWsrep, "replication-multi-master-wsrep", false, "Enable Galera multi-master")
	monitorCmd.Flags().StringVar(&conf.MultiMasterWsrepSSTMethod, "replication-multi-master-wsrep-sst-method", "mariabackup", "mariabackup|xtrabackup-v2|rsync|mysqldump")
	monitorCmd.Flags().BoolVar(&conf.MultiMasterRing, "replication-multi-master-ring", false, "Multi-master ring topology")
	monitorCmd.Flags().BoolVar(&conf.MultiTierSlave, "replication-multi-tier-slave", false, "Relay slaves topology")
	monitorCmd.Flags().BoolVar(&conf.MasterSlavePgStream, "replication-master-slave-pg-stream", false, "Postgres streaming replication")
	monitorCmd.Flags().BoolVar(&conf.MasterSlavePgLogical, "replication-master-slave-pg-locgical", false, "Postgres logical replication")
	monitorCmd.Flags().BoolVar(&conf.ReplicationNoRelay, "replication-master-slave-never-relay", true, "Do not allow relay server MSS MXS XXM RSM")
	monitorCmd.Flags().StringVar(&conf.ReplicationErrorScript, "replication-error-script", "", "Replication error script")
	monitorCmd.Flags().StringVar(&conf.ReplicationRestartOnSQLErrorMatch, "replication-restart-on-sqlerror-match", "", "Auto restart replication on SQL Error regexep")
	monitorCmd.Flags().StringVar(&conf.PreScript, "failover-pre-script", "", "Path of pre-failover script")
	monitorCmd.Flags().StringVar(&conf.PostScript, "failover-post-script", "", "Path of post-failover script")
	monitorCmd.Flags().BoolVar(&conf.ReadOnly, "failover-readonly-state", true, "Failover Switchover set slaves as read-only")
	monitorCmd.Flags().BoolVar(&conf.SuperReadOnly, "failover-superreadonly-state", false, "Failover Switchover set slaves as super-read-only")
	monitorCmd.Flags().StringVar(&conf.FailMode, "failover-mode", "manual", "Failover is manual or automatic")
	monitorCmd.Flags().Int64Var(&conf.FailMaxDelay, "failover-max-slave-delay", 30, "Election ignore slave with replication delay over this time in sec")
	monitorCmd.Flags().BoolVar(&conf.FailRestartUnsafe, "failover-restart-unsafe", false, "Failover when cluster down if a slave is start first ")
	monitorCmd.Flags().IntVar(&conf.FailLimit, "failover-limit", 5, "Failover is canceld if already failover this number of time (0: unlimited)")
	monitorCmd.Flags().Int64Var(&conf.FailTime, "failover-time-limit", 0, "Failover is canceled if timer in sec is not passed with previous failover (0: do not wait)")
	monitorCmd.Flags().BoolVar(&conf.FailSync, "failover-at-sync", false, "Failover only when state semisync is sync for last status")
	monitorCmd.Flags().BoolVar(&conf.FailEventScheduler, "failover-event-scheduler", false, "Failover event scheduler")
	monitorCmd.Flags().BoolVar(&conf.FailoverSwitchToPrefered, "failover-switch-to-prefered", false, "Failover always pick most up to date slave following it with switchover to prefered leader")
	monitorCmd.Flags().BoolVar(&conf.FailEventStatus, "failover-event-status", false, "Failover event status ENABLE OR DISABLE ON SLAVE")
	monitorCmd.Flags().BoolVar(&conf.CheckFalsePositiveHeartbeat, "failover-falsepositive-heartbeat", true, "Failover checks that slaves do not receive heartbeat")
	monitorCmd.Flags().IntVar(&conf.CheckFalsePositiveHeartbeatTimeout, "failover-falsepositive-heartbeat-timeout", 3, "Failover checks that slaves do not receive heartbeat detection timeout ")
	monitorCmd.Flags().BoolVar(&conf.CheckFalsePositiveExternal, "failover-falsepositive-external", false, "Failover checks that http//master:80 does not reponse 200 OK header")
	monitorCmd.Flags().IntVar(&conf.CheckFalsePositiveExternalPort, "failover-falsepositive-external-port", 80, "Failover checks external port")
	monitorCmd.Flags().IntVar(&conf.MaxFail, "failover-falsepositive-ping-counter", 5, "Failover after this number of ping failures (interval 1s)")
	monitorCmd.Flags().IntVar(&conf.FailoverLogFileKeep, "failover-log-file-keep", 5, "Purge log files taken during failover")
	monitorCmd.Flags().BoolVar(&conf.Autoseed, "autoseed", false, "Automatic join a standalone node")
	monitorCmd.Flags().BoolVar(&conf.Autorejoin, "autorejoin", true, "Automatic rejoin a failed master")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinBackupBinlog, "autorejoin-backup-binlog", true, "backup ahead binlogs events when old master rejoin")
	monitorCmd.Flags().StringVar(&conf.RejoinScript, "autorejoin-script", "", "Path of old master rejoin script")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinSemisync, "autorejoin-flashback-on-sync", true, "Automatic rejoin flashback if election status is semisync SYNC ")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinNoSemisync, "autorejoin-flashback-on-unsync", false, "Automatic rejoin flashback if election status is semisync NOT SYNC ")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinFlashback, "autorejoin-flashback", false, "Automatic rejoin ahead failed master via binlog flashback")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinZFSFlashback, "autorejoin-zfs-flashback", false, "Automatic rejoin ahead failed master via previous ZFS snapshot")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinMysqldump, "autorejoin-mysqldump", false, "Automatic rejoin ahead failed master via direct current master dump")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinPhysicalBackup, "autorejoin-physical-backup", false, "Automatic rejoin ahead failed master via reseed previous phyiscal backup")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinLogicalBackup, "autorejoin-logical-backup", false, "Automatic rejoin ahead failed master via reseed previous logical backup")
	monitorCmd.Flags().BoolVar(&conf.AutorejoinSlavePositionalHeartbeat, "autorejoin-slave-positional-heartbeat", false, "Automatically rejoin extra slaves via pseudo gtid heartbeat for positional replication")

	monitorCmd.Flags().StringVar(&conf.AlertScript, "alert-script", "", "Path for alerting script server status change")
	monitorCmd.Flags().StringVar(&conf.SlackURL, "alert-slack-url", "", "Slack webhook URL to alert")
	monitorCmd.Flags().StringVar(&conf.SlackChannel, "alert-slack-channel", "#support", "Slack channel to alert")
	monitorCmd.Flags().StringVar(&conf.SlackUser, "alert-slack-user", "", "Slack user for alert")

	monitorCmd.Flags().BoolVar(&conf.RegistryConsul, "registry-consul", false, "Register write and read SRV DNS to consul")
	monitorCmd.Flags().StringVar(&conf.RegistryHosts, "registry-servers", "127.0.0.1", "Comma-separated list of registry addresses")

	conf.CheckType = "tcp"
	monitorCmd.Flags().BoolVar(&conf.CheckReplFilter, "check-replication-filters", true, "Check that possible master have equal replication filters")
	monitorCmd.Flags().BoolVar(&conf.CheckBinFilter, "check-binlog-filters", true, "Check that possible master have equal binlog filters")
	monitorCmd.Flags().BoolVar(&conf.CheckGrants, "check-grants", true, "Check that possible master have equal grants")
	monitorCmd.Flags().BoolVar(&conf.RplChecks, "check-replication-state", true, "Check replication status when electing master server")

	monitorCmd.Flags().StringVar(&conf.APIPort, "api-port", "10005", "Rest API listen port")
	monitorCmd.Flags().StringVar(&conf.APIUsers, "api-credentials", "admin:repman", "Rest API user list user:password,..")
	monitorCmd.Flags().StringVar(&conf.APIUsersExternal, "api-credentials-external", "dba:repman,foo:bar", "Rest API user list user:password,..")
	monitorCmd.Flags().StringVar(&conf.APIUsersACLAllow, "api-credentials-acl-allow", "admin:cluster proxy db prov,dba:cluster proxy db,foo:", "User acl allow")
	monitorCmd.Flags().StringVar(&conf.APIUsersACLDiscard, "api-credentials-acl-discard", "", "User acl discard")
	monitorCmd.Flags().StringVar(&conf.APIBind, "api-bind", "0.0.0.0", "Rest API bind ip")
	monitorCmd.Flags().BoolVar(&conf.APIHttpsBind, "api-https-bind", false, "Bind API call to https Web UI will error with http")
	monitorCmd.Flags().BoolVar(&conf.APISecureConfig, "api-credentials-secure-config", false, "Need JWT token to download config tar.gz")

	//monitorCmd.Flags().BoolVar(&conf.Daemon, "daemon", true, "Daemon mode. Do not start the Termbox console")
	conf.Daemon = true

	if WithEnforce == "ON" {
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveReadOnly, "force-slave-readonly", true, "Automatically activate read only on slave")
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveHeartbeat, "force-slave-heartbeat", false, "Automatically activate heartbeat on slave")
		monitorCmd.Flags().IntVar(&conf.ForceSlaveHeartbeatRetry, "force-slave-heartbeat-retry", 5, "Replication heartbeat retry on slave")
		monitorCmd.Flags().IntVar(&conf.ForceSlaveHeartbeatTime, "force-slave-heartbeat-time", 3, "Replication heartbeat time")
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveGtid, "force-slave-gtid-mode", false, "Automatically activate gtid mode on slave")
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveGtidStrict, "force-slave-gtid-mode-strict", false, "Automatically activate GTID strict mode")
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveNoGtid, "force-slave-no-gtid-mode", false, "Automatically activate no gtid mode on slave")
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveSemisync, "force-slave-semisync", false, "Automatically activate semisync on slave")
		monitorCmd.Flags().BoolVar(&conf.ForceBinlogRow, "force-binlog-row", false, "Automatically activate binlog row format on master")
		monitorCmd.Flags().BoolVar(&conf.ForceBinlogAnnotate, "force-binlog-annotate", false, "Automatically activate annotate event")
		monitorCmd.Flags().BoolVar(&conf.ForceBinlogSlowqueries, "force-binlog-slowqueries", false, "Automatically activate long replication statement in slow log")
		monitorCmd.Flags().BoolVar(&conf.ForceBinlogChecksum, "force-binlog-checksum", false, "Automatically force  binlog checksum")
		monitorCmd.Flags().BoolVar(&conf.ForceBinlogCompress, "force-binlog-compress", false, "Automatically force binlog compression")
		monitorCmd.Flags().BoolVar(&conf.ForceDiskRelayLogSizeLimit, "force-disk-relaylog-size-limit", false, "Automatically limit the size of relay log on disk ")
		monitorCmd.Flags().Uint64Var(&conf.ForceDiskRelayLogSizeLimitSize, "force-disk-relaylog-size-limit-size", 1000000000, "Automatically limit the size of relay log on disk to 1G")
		monitorCmd.Flags().BoolVar(&conf.ForceInmemoryBinlogCacheSize, "force-inmemory-binlog-cache-size", false, "Automatically adapt binlog cache size based on monitoring")
		monitorCmd.Flags().BoolVar(&conf.ForceSyncBinlog, "force-sync-binlog", false, "Automatically force master crash safe")
		monitorCmd.Flags().BoolVar(&conf.ForceSyncInnoDB, "force-sync-innodb", false, "Automatically force master innodb crash safe")
		monitorCmd.Flags().BoolVar(&conf.ForceNoslaveBehind, "force-noslave-behind", false, "Automatically force no slave behing")
	}

	monitorCmd.Flags().BoolVar(&conf.HttpServ, "http-server", true, "Start the HTTP monitor")
	monitorCmd.Flags().StringVar(&conf.BindAddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
	monitorCmd.Flags().StringVar(&conf.HttpPort, "http-port", "10001", "HTTP monitor to listen on this port")
	if GoOS == "darwin" {
		monitorCmd.Flags().StringVar(&conf.HttpRoot, "http-root", "/opt/replication-manager/share/dashboard", "Path to HTTP replication-monitor files")
	} else {
		monitorCmd.Flags().StringVar(&conf.HttpRoot, "http-root", "/usr/share/replication-manager/dashboard", "Path to HTTP replication-monitor files")
	}
	monitorCmd.Flags().IntVar(&conf.HttpRefreshInterval, "http-refresh-interval", 4000, "Http refresh interval in ms")
	monitorCmd.Flags().IntVar(&conf.SessionLifeTime, "http-session-lifetime", 3600, "Http Session life time ")

	if WithMail == "ON" {
		monitorCmd.Flags().StringVar(&conf.MailFrom, "mail-from", "mrm@localhost", "Alert email sender")
		monitorCmd.Flags().StringVar(&conf.MailTo, "mail-to", "", "Alert email recipients, separated by commas")
		monitorCmd.Flags().StringVar(&conf.MailSMTPAddr, "mail-smtp-addr", "localhost:25", "Alert email SMTP server address, in host:[port] format")
		monitorCmd.Flags().StringVar(&conf.MailSMTPUser, "mail-smtp-user", "", "SMTP user")
		monitorCmd.Flags().StringVar(&conf.MailSMTPPassword, "mail-smtp-password", "", "SMTP password")
		monitorCmd.Flags().BoolVar(&conf.MailSMTPTLSSkipVerify, "mail-smtp-tls-skip-verify", false, "Use TLS with skip verify")
	}

	monitorCmd.Flags().BoolVar(&conf.PRXServersReadOnMaster, "proxy-servers-read-on-master", false, "Should RO route via proxies point to master")
	monitorCmd.Flags().BoolVar(&conf.PRXServersBackendCompression, "proxy-servers-backend-compression", false, "Proxy communicate with backends with compression")
	monitorCmd.Flags().IntVar(&conf.PRXServersBackendMaxReplicationLag, "proxy-servers-backend-max-replication-lag", 30, "Max lag to send query to read  backends ")
	monitorCmd.Flags().IntVar(&conf.PRXServersBackendMaxConnections, "proxy-servers-backend-max-connections", 1000, "Max connections on backends ")

	monitorCmd.Flags().BoolVar(&conf.ExtProxyOn, "extproxy", false, "External proxy can be used to specify a route manage with external scripts")
	monitorCmd.Flags().StringVar(&conf.ExtProxyVIP, "extproxy-address", "", "Network address when route is manage via external script,  host:[port] format")

	if WithMaxscale == "ON" {
		maxscaleprx := new(cluster.MaxscaleProxy)
		maxscaleprx.AddFlags(monitorCmd.Flags(), &conf)
	}

	// TODO: this seems dead code / unimplemented
	// start
	if WithMySQLRouter == "ON" {
		monitorCmd.Flags().BoolVar(&conf.MysqlRouterOn, "mysqlrouter", false, "MySQLRouter proxy server is query for backend status")
		monitorCmd.Flags().StringVar(&conf.MysqlRouterHosts, "mysqlrouter-servers", "127.0.0.1", "MaxScale hosts ")
		monitorCmd.Flags().StringVar(&conf.MysqlRouterPort, "mysqlrouter-port", "6603", "MySQLRouter admin port")
		monitorCmd.Flags().StringVar(&conf.MysqlRouterUser, "mysqlrouter-user", "admin", "MySQLRouter admin user")
		monitorCmd.Flags().StringVar(&conf.MysqlRouterPass, "mysqlrouter-pass", "mariadb", "MySQLRouter admin password")
		monitorCmd.Flags().IntVar(&conf.MysqlRouterWritePort, "mysqlrouter-write-port", 3306, "MySQLRouter read-write port to leader")
		monitorCmd.Flags().IntVar(&conf.MysqlRouterReadPort, "mysqlrouter-read-port", 3307, "MySQLRouter load balance read port to all nodes")
		monitorCmd.Flags().IntVar(&conf.MysqlRouterReadWritePort, "mysqlrouter-read-write-port", 3308, "MySQLRouter load balance read port to all nodes")
	}
	// end of dead code

	if WithMariadbshardproxy == "ON" {
		mdbsprx := new(cluster.MariadbShardProxy)
		mdbsprx.AddFlags(monitorCmd.Flags(), &conf)
	}
	if WithHaproxy == "ON" {
		haprx := new(cluster.HaproxyProxy)
		haprx.AddFlags(monitorCmd.Flags(), &conf)
	}
	if WithProxysql == "ON" {
		proxysqlprx := new(cluster.ProxySQLProxy)
		proxysqlprx.AddFlags(monitorCmd.Flags(), &conf)
	}
	if WithSphinx == "ON" {
		sphinxprx := new(cluster.SphinxProxy)
		sphinxprx.AddFlags(monitorCmd.Flags(), &conf)
	}

	myproxyprx := new(cluster.MyProxyProxy)
	myproxyprx.AddFlags(monitorCmd.Flags(), &conf)

	if WithSpider == "ON" {
		monitorCmd.Flags().BoolVar(&conf.Spider, "spider", false, "Turn on spider detection")
	}

	if WithMonitoring == "ON" {
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonPort, "graphite-carbon-port", 2003, "Graphite Carbon Metrics TCP & UDP port")
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonApiPort, "graphite-carbon-api-port", 10002, "Graphite Carbon API port")
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonServerPort, "graphite-carbon-server-port", 10003, "Graphite Carbon HTTP port")
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonLinkPort, "graphite-carbon-link-port", 7002, "Graphite Carbon Link port")
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonPicklePort, "graphite-carbon-pickle-port", 2004, "Graphite Carbon Pickle port")
		monitorCmd.Flags().IntVar(&conf.GraphiteCarbonPprofPort, "graphite-carbon-pprof-port", 7007, "Graphite Carbon Pickle port")
		monitorCmd.Flags().StringVar(&conf.GraphiteCarbonHost, "graphite-carbon-host", "127.0.0.1", "Graphite monitoring host")
		monitorCmd.Flags().BoolVar(&conf.GraphiteMetrics, "graphite-metrics", false, "Enable Graphite monitoring")
		monitorCmd.Flags().BoolVar(&conf.GraphiteEmbedded, "graphite-embedded", false, "Enable Internal Graphite Carbon Server")
	}
	//	monitorCmd.Flags().BoolVar(&conf.Heartbeat, "heartbeat-table", false, "Heartbeat for active/passive or multi mrm setup")
	if WithArbitrationClient == "ON" {
		monitorCmd.Flags().BoolVar(&conf.Arbitration, "arbitration-external", false, "Multi moninitor sas arbitration")
		monitorCmd.Flags().StringVar(&conf.ArbitrationSasSecret, "arbitration-external-secret", "", "Secret for arbitration")
		monitorCmd.Flags().StringVar(&conf.ArbitrationSasHosts, "arbitration-external-hosts", "88.191.151.84:80", "Arbitrator address")
		monitorCmd.Flags().IntVar(&conf.ArbitrationSasUniqueId, "arbitration-external-unique-id", 0, "Unique replication-manager instance idententifier")
		monitorCmd.Flags().StringVar(&conf.ArbitrationPeerHosts, "arbitration-peer-hosts", "127.0.0.1:10001", "Peer replication-manager hosts http port")
		monitorCmd.Flags().StringVar(&conf.DBServersLocality, "db-servers-locality", "127.0.0.1", "List database servers that are in same network locality")
		monitorCmd.Flags().StringVar(&conf.ArbitrationFailedMasterScript, "arbitration-failed-master-script", "", "External script when a master lost arbitration during split brain")
		monitorCmd.Flags().IntVar(&conf.ArbitrationReadTimout, "arbitration-read-timeout", 800, "Read timeout for arbotration response in millisec don't woveload monitoring ticker in second")
	}

	monitorCmd.Flags().StringVar(&conf.SchedulerReceiverPorts, "scheduler-db-servers-receiver-ports", "4444", "Scheduler TCP port to send data to db node, if list port affection is modulo db nodes")
	monitorCmd.Flags().BoolVar(&conf.SchedulerReceiverUseSSL, "scheduler-db-servers-receiver-use-ssl", false, "Listner to send data to db node is use SSL")
	monitorCmd.Flags().BoolVar(&conf.SchedulerBackupLogical, "scheduler-db-servers-logical-backup", true, "Schedule logical backup")
	monitorCmd.Flags().BoolVar(&conf.SchedulerBackupPhysical, "scheduler-db-servers-physical-backup", false, "Schedule logical backup")
	monitorCmd.Flags().BoolVar(&conf.SchedulerDatabaseLogs, "scheduler-db-servers-logs", false, "Schedule database logs fetching")
	monitorCmd.Flags().BoolVar(&conf.SchedulerDatabaseOptimize, "scheduler-db-servers-optimize", true, "Schedule database optimize")
	monitorCmd.Flags().StringVar(&conf.BackupLogicalCron, "scheduler-db-servers-logical-backup-cron", "0 0 1 * * 6", "Logical backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupPhysicalCron, "scheduler-db-servers-physical-backup-cron", "0 0 0 * * 0-4", "Physical backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupDatabaseOptimizeCron, "scheduler-db-servers-optimize-cron", "0 0 3 1 * 5", "Optimize cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupDatabaseLogCron, "scheduler-db-servers-logs-cron", "0 0/10 * * * *", "Logs backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().BoolVar(&conf.SchedulerDatabaseLogsTableRotate, "scheduler-db-servers-logs-table-rotate", true, "Schedule rotate database system table logs")
	monitorCmd.Flags().StringVar(&conf.SchedulerDatabaseLogsTableRotateCron, "scheduler-db-servers-logs-table-rotate-cron", "0 0 0/6 * * *", "Logs table rotate cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().IntVar(&conf.SchedulerMaintenanceDatabaseLogsTableKeep, "scheduler-db-servers-logs-table-keep", 12, "Keep this number of system table logs")
	monitorCmd.Flags().StringVar(&conf.SchedulerSLARotateCron, "scheduler-sla-rotate-cron", "0 0 0 1 * *", "SLA rotate cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().BoolVar(&conf.SchedulerRollingRestart, "scheduler-rolling-restart", false, "Schedule rolling restart")
	monitorCmd.Flags().StringVar(&conf.SchedulerRollingRestartCron, "scheduler-rolling-restart-cron", "0 30 11 * * *", "Rolling restart cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().BoolVar(&conf.SchedulerRollingReprov, "scheduler-rolling-reprov", false, "Schedule rolling reprov")
	monitorCmd.Flags().StringVar(&conf.SchedulerRollingReprovCron, "scheduler-rolling-reprov-cron", "0 30 10 * * 5", "Rolling reprov cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().BoolVar(&conf.SchedulerJobsSSH, "scheduler-jobs-ssh", false, "Schedule remote execution of dbjobs via ssh ")
	monitorCmd.Flags().StringVar(&conf.SchedulerJobsSSHCron, "scheduler-jobs-ssh-cron", "0 * * * * *", "Remote execution of dbjobs via ssh ")

	monitorCmd.Flags().BoolVar(&conf.Backup, "backup", false, "Turn on Backup")
	monitorCmd.Flags().BoolVar(&conf.BackupLockDDL, "backup-lockddl", true, "Use mariadb backup stage")
	monitorCmd.Flags().IntVar(&conf.BackupLogicalLoadThreads, "backup-logical-load-threads", 2, "Number of threads to load database")
	monitorCmd.Flags().IntVar(&conf.BackupLogicalDumpThreads, "backup-logical-dump-threads", 2, "Number of threads to dump database")
	monitorCmd.Flags().BoolVar(&conf.BackupLogicalDumpSystemTables, "backup-logical-dump-system-tables", false, "Backup restore the mysql database")
	monitorCmd.Flags().StringVar(&conf.BackupLogicalType, "backup-logical-type", "mysqldump", "type of logical backup: river|mysqldump|mydumper")
	monitorCmd.Flags().StringVar(&conf.BackupPhysicalType, "backup-physical-type", "xtrabackup", "type of physical backup: xtrabackup|mariabackup")
	monitorCmd.Flags().BoolVar(&conf.BackupRestic, "backup-restic", false, "Use restic to archive and restore backups")
	monitorCmd.Flags().StringVar(&conf.BackupResticBinaryPath, "backup-restic-binary-path", "/usr/bin/restic", "Path to restic binary")
	monitorCmd.Flags().StringVar(&conf.BackupResticAwsAccessKeyId, "backup-restic-aws-access-key-id", "admin", "Restic backup AWS key id")
	monitorCmd.Flags().StringVar(&conf.BackupResticAwsAccessSecret, "backup-restic-aws-access-secret", "secret", "Restic backup AWS key sercret")
	monitorCmd.Flags().StringVar(&conf.BackupResticRepository, "backup-restic-repository", "s3:https://s3.signal18.io/backups", "Restic backend repository")
	monitorCmd.Flags().StringVar(&conf.BackupResticPassword, "backup-restic-password", "secret", "Restic backend password")
	monitorCmd.Flags().BoolVar(&conf.BackupResticAws, "backup-restic-aws", false, "Restic will archive to s3 or to datadir/backups/archive")
	monitorCmd.Flags().BoolVar(&conf.BackupStreaming, "backup-streaming", false, "Backup streaming to cloud ")
	monitorCmd.Flags().BoolVar(&conf.BackupStreamingDebug, "backup-streaming-debug", false, "Debug mode for streaming to cloud ")
	monitorCmd.Flags().StringVar(&conf.BackupStreamingAwsAccessKeyId, "backup-streaming-aws-access-key-id", "admin", "Backup AWS key id")
	monitorCmd.Flags().StringVar(&conf.BackupStreamingAwsAccessSecret, "backup-streaming-aws-access-secret", "secret", "Backup AWS key sercret")
	monitorCmd.Flags().StringVar(&conf.BackupStreamingEndpoint, "backup-streaming-endpoint", "https://s3.signal18.io/", "Backup AWS endpoint")
	monitorCmd.Flags().StringVar(&conf.BackupStreamingRegion, "backup-streaming-region", "fr-1", "Backup AWS region")
	monitorCmd.Flags().StringVar(&conf.BackupStreamingBucket, "backup-streaming-bucket", "repman", "Backup AWS bucket")

	//monitorCmd.Flags().StringVar(&conf.BackupResticStoragePolicy, "backup-restic-storage-policy", "--prune --keep-last 10 --keep-hourly 24 --keep-daily 7 --keep-weekly 52 --keep-monthly 120 --keep-yearly 102", "Restic keep backup policy")
	monitorCmd.Flags().IntVar(&conf.BackupKeepHourly, "backup-keep-hourly", 1, "Keep this number of hourly backup")
	monitorCmd.Flags().IntVar(&conf.BackupKeepDaily, "backup-keep-daily", 1, "Keep this number of daily backup")
	monitorCmd.Flags().IntVar(&conf.BackupKeepWeekly, "backup-keep-weekly", 4, "Keep this number of weekly backup")
	monitorCmd.Flags().IntVar(&conf.BackupKeepMonthly, "backup-keep-monthly", 12, "Keep this number of monthly backup")
	monitorCmd.Flags().IntVar(&conf.BackupKeepYearly, "backup-keep-yearly", 2, "Keep this number of yearly backup")

	monitorCmd.Flags().StringVar(&conf.BackupMyDumperPath, "backup-mydumper-path", "/usr/bin/mydumper", "Path to mydumper binary")
	monitorCmd.Flags().StringVar(&conf.BackupMyLoaderPath, "backup-myloader-path", "/usr/bin/myloader", "Path to myloader binary")
	monitorCmd.Flags().StringVar(&conf.BackupMysqldumpPath, "backup-mysqldump-path", "", "Path to mysqldump binary")
	monitorCmd.Flags().StringVar(&conf.BackupMysqldumpOptions, "backup-mysqldump-options", "--hex-blob --single-transaction --verbose --all-databases --system=all", "Extra options")
	monitorCmd.Flags().StringVar(&conf.BackupMysqlbinlogPath, "backup-mysqlbinlog-path", "", "Path to mysqlbinlog binary")
	monitorCmd.Flags().StringVar(&conf.BackupMysqlclientPath, "backup-mysqlclient-path", "", "Path to mysql client binary")
	monitorCmd.Flags().BoolVar(&conf.BackupBinlogs, "backup-binlogs", false, "Archive binlogs")
	monitorCmd.Flags().IntVar(&conf.BackupBinlogsKeep, "backup-binlogs-keep", 10, "Number of master binlog to keep")
	monitorCmd.Flags().BoolVar(&conf.ProvBinaryInTarball, "prov-db-binary-in-tarball", false, "Add prov-db-binary-tarball-name binaries to init tarball")
	monitorCmd.Flags().StringVar(&conf.ProvBinaryTarballName, "prov-db-binary-tarball-name", "mysql-8.0.17-macos10.14-x86_64.tar.gz", "Name of binary tarball to put in tarball")

	monitorCmd.Flags().StringVar(&conf.ProvIops, "prov-db-disk-iops", "300", "Rnd IO/s in for micro service VM")
	monitorCmd.Flags().StringVar(&conf.ProvIopsLatency, "prov-db-disk-iops-latency", "0.002", "IO latency in s")
	monitorCmd.Flags().StringVar(&conf.ProvCores, "prov-db-cpu-cores", "1", "Number of cpu cores for the micro service VM")
	monitorCmd.Flags().BoolVar(&conf.ProvDBApplyDynamicConfig, "prov-db-apply-dynamic-config", false, "Dynamic database config change")
	monitorCmd.Flags().StringVar(&conf.ProvTags, "prov-db-tags", "semisync,row,innodb,noquerycache,threadpool,slow,pfs,docker,linux,readonly,diskmonitor,sqlerror,compressbinlog", "playbook configuration tags")
	monitorCmd.Flags().StringVar(&conf.ProvDomain, "prov-db-domain", "0", "Config domain id for the cluster")
	monitorCmd.Flags().StringVar(&conf.ProvMem, "prov-db-memory", "256", "Memory in M for micro service VM")
	monitorCmd.Flags().StringVar(&conf.ProvMemSharedPct, "prov-db-memory-shared-pct", "threads:16,innodb:60,myisam:10,aria:10,rocksdb:1,tokudb:1,s3:1,archive:1,querycache:0", "% memory shared per buffer")
	monitorCmd.Flags().StringVar(&conf.ProvMemThreadedPct, "prov-db-memory-threaded-pct", "tmp:70,join:20,sort:10", "% memory allocted per threads")
	monitorCmd.Flags().StringVar(&conf.ProvDisk, "prov-db-disk-size", "20", "Disk in g for micro service VM")
	monitorCmd.Flags().IntVar(&conf.ProvExpireLogDays, "prov-db-expire-log-days", 5, "Keep binlogs that nunmber of days")
	monitorCmd.Flags().IntVar(&conf.ProvMaxConnections, "prov-db-max-connections", 1000, "Max database connections")
	monitorCmd.Flags().StringVar(&conf.ProvProxTags, "prov-proxy-tags", "masterslave,docker,linux,noreadwritesplit", "playbook configuration tags wsrep,multimaster,masterslave")
	monitorCmd.Flags().StringVar(&conf.ProvProxDisk, "prov-proxy-disk-size", "20", "Disk in g for micro service VM")
	monitorCmd.Flags().StringVar(&conf.ProvProxCores, "prov-proxy-cpu-cores", "1", "Cpu cores ")
	monitorCmd.Flags().StringVar(&conf.ProvProxMem, "prov-proxy-memory", "1", "Memory usage in giga bytes")
	monitorCmd.Flags().StringVar(&conf.ProvServicePlanRegistry, "prov-service-plan-registry", "https://docs.google.com/spreadsheets/d/e/2PACX-1vQClXknRapJZ4bRSId_aa5zUrbFDZmmc6GiV3n7-tPyQJispqqnSJj6lMaJxoJv5pOC9Ktj8ywWdGX6/pub?gid=0&single=true&output=csv", "URL to csv service plan list")
	//	monitorCmd.Flags().StringVar(&conf.ProvServicePlanRegistry, "prov-service-plan-registry", "http://gsx2json.com/api?id=130326CF_SPaz-flQzCRPE-w7FjzqU1NqbsM7MpIQ_oU&sheet=1&columns=false", "URL to json service plan list")
	monitorCmd.Flags().StringVar(&conf.ProvServicePlan, "prov-service-plan", "", "Cluster plan")
	monitorCmd.Flags().BoolVar(&conf.Test, "test", true, "Enable non regression tests")
	monitorCmd.Flags().BoolVar(&conf.TestInjectTraffic, "test-inject-traffic", false, "Inject some database traffic via proxy")
	monitorCmd.Flags().IntVar(&conf.SysbenchTime, "sysbench-time", 100, "Time to run benchmark")
	monitorCmd.Flags().IntVar(&conf.SysbenchThreads, "sysbench-threads", 4, "Number of threads to run benchmark")
	monitorCmd.Flags().StringVar(&conf.SysbenchTest, "sysbench-test", "oltp_read_write", "oltp_read_write|tpcc|oltp_read_only|oltp_update_index|oltp_update_non_index")
	monitorCmd.Flags().IntVar(&conf.SysbenchScale, "sysbench-scale", 1, "Number of warehouse")
	monitorCmd.Flags().IntVar(&conf.SysbenchTables, "sysbench-tables", 1, "Number of tables")
	monitorCmd.Flags().BoolVar(&conf.SysbenchV1, "sysbench-v1", false, "v1 get different syntax")
	monitorCmd.Flags().StringVar(&conf.SysbenchBinaryPath, "sysbench-binary-path", "/usr/bin/sysbench", "Sysbench Wrapper in test mode")
	monitorCmd.Flags().StringVar(&conf.ProvDBBinaryBasedir, "prov-db-binary-basedir", "/usr/local/mysql/bin", "Path to mysqld binary")
	monitorCmd.Flags().StringVar(&conf.ProvDBClientBasedir, "prov-db-client-basedir", "/usr/bin", "Path to database client binary")

	if WithOpenSVC == "ON" {
		monitorCmd.Flags().StringVar(&conf.ProvOrchestratorEnable, "prov-orchestrator-enable", "opensvc,kube,onpremise,local", "seprated list of orchestrator ")
		monitorCmd.Flags().StringVar(&conf.ProvOrchestrator, "prov-orchestrator", "opensvc", "onpremise|opensvc|kube|slapos|local")
		monitorCmd.Flags().StringVar(&conf.ProvOrchestratorCluster, "prov-orchestrator-cluster", "local", "The orchestrated cluster used in FQDNS")
	} else {
		monitorCmd.Flags().StringVar(&conf.ProvOrchestrator, "prov-orchestrator", "onpremise", "onpremise|opensvc|kube|slapos|local")
		monitorCmd.Flags().StringVar(&conf.ProvOrchestratorEnable, "prov-orchestrator-enable", "onpremise,local", "seprated list of orchestrator ")
	}
	monitorCmd.Flags().StringVar(&conf.SlapOSDBPartitions, "slapos-db-partitions", "", "List databases slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.SlapOSProxySQLPartitions, "slapos-proxysql-partitions", "", "List proxysql slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.SlapOSHaProxyPartitions, "slapos-haproxy-partitions", "", "List haproxy slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.SlapOSMaxscalePartitions, "slapos-maxscale-partitions", "", "List maxscale slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.SlapOSShardProxyPartitions, "slapos-shardproxy-partitions", "", "List spider slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.SlapOSSphinxPartitions, "slapos-sphinx-partitions", "", "List sphinx slapos partitions path")
	monitorCmd.Flags().StringVar(&conf.ProvDbBootstrapScript, "prov-db-bootstrap-script", "", "Database bootstrap script")
	monitorCmd.Flags().StringVar(&conf.ProvProxyBootstrapScript, "prov-proxy-bootstrap-script", "", "Proxy bootstrap script")
	monitorCmd.Flags().StringVar(&conf.ProvDbCleanupScript, "prov-db-cleanup-script", "", "Database cleanup script")
	monitorCmd.Flags().StringVar(&conf.ProvProxyCleanupScript, "prov-proxy-cleanup-script", "", "Proxy cleanup script")
	monitorCmd.Flags().StringVar(&conf.ProvDbStartScript, "prov-db-start-script", "", "Database start script")
	monitorCmd.Flags().StringVar(&conf.ProvProxyStartScript, "prov-proxy-start-script", "", "Proxy start script")
	monitorCmd.Flags().StringVar(&conf.ProvDbStopScript, "prov-db-stop-script", "", "Database stop script")
	monitorCmd.Flags().StringVar(&conf.ProvProxyStopScript, "prov-proxy-stop-script", "", "Proxy stop script")

	monitorCmd.Flags().BoolVar(&conf.OnPremiseSSH, "onpremise-ssh", false, "Connect to host via SSH using user private key")
	monitorCmd.Flags().StringVar(&conf.OnPremiseSSHPrivateKey, "onpremise-ssh-private-key", "", "Private key for ssh if none use the user HOME directory")
	monitorCmd.Flags().IntVar(&conf.OnPremiseSSHPort, "onpremise-ssh-port", 22, "Connect to host via SSH using ssh port")
	monitorCmd.Flags().StringVar(&conf.OnPremiseSSHCredential, "onpremise-ssh-credential", "root:", "User:password for ssh if no password using current user private key")
	if WithProvisioning == "ON" {
		monitorCmd.Flags().StringVar(&conf.ProvDatadirVersion, "prov-db-datadir-version", "10.2", "Empty datadir to deploy for localtest")
		monitorCmd.Flags().StringVar(&conf.ProvDiskSystemSize, "prov-db-disk-system-size", "2", "Disk in g for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvDiskTempSize, "prov-db-disk-temp-size", "128", "Disk in m for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvDiskDockerSize, "prov-db-disk-docker-size", "2", "Disk in g for Docker Private per micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvDbImg, "prov-db-docker-img", "mariadb:latest", "Docker image for database")
		monitorCmd.Flags().StringVar(&conf.ProvType, "prov-db-service-type ", "package", "[package|docker|podman|oci|kvm|zone|lxc]")
		monitorCmd.Flags().StringVar(&conf.ProvAgents, "prov-db-agents", "", "Comma seperated list of agents for micro services provisionning")
		monitorCmd.Flags().StringVar(&conf.ProvDiskFS, "prov-db-disk-fs", "ext4", "[zfs|xfs|ext4]")
		monitorCmd.Flags().StringVar(&conf.ProvDiskFSCompress, "prov-db-disk-fs-compress", "off", " ZFS supported compression [off|gzip|lz4]")
		monitorCmd.Flags().StringVar(&conf.ProvDiskPool, "prov-db-disk-pool", "none", "[none|zpool|lvm]")
		monitorCmd.Flags().StringVar(&conf.ProvDiskType, "prov-db-disk-type", "loopback", "[loopback|physical|pool|directory|volume]")
		monitorCmd.Flags().StringVar(&conf.ProvVolumeDocker, "prov-db-volume-docker", "", "Volume name in case of docker private")
		monitorCmd.Flags().StringVar(&conf.ProvVolumeData, "prov-db-volume-data", "", "Volume name for the datadir")
		monitorCmd.Flags().StringVar(&conf.ProvDiskDevice, "prov-db-disk-device", "/srv", "loopback:path-to-loopfile|physical:/dev/xx|pool:pool-name|directory:/srv")
		monitorCmd.Flags().BoolVar(&conf.ProvDiskSnapshot, "prov-db-disk-snapshot-prefered-master", false, "Take snapshoot of prefered master")
		monitorCmd.Flags().IntVar(&conf.ProvDiskSnapshotKeep, "prov-db-disk-snapshot-keep", 7, "Keek this number of snapshoot of prefered master")
		monitorCmd.Flags().StringVar(&conf.ProvNetIface, "prov-db-net-iface", "eth0", "HBA Device to hold Ips")
		monitorCmd.Flags().StringVar(&conf.ProvGateway, "prov-db-net-gateway", "192.168.0.254", "Micro Service network gateway")
		monitorCmd.Flags().StringVar(&conf.ProvNetmask, "prov-db-net-mask", "255.255.255.0", "Micro Service network mask")
		monitorCmd.Flags().StringVar(&conf.ProvDBLoadCSV, "prov-db-load-csv", "", "List of shema.table csv file to load a bootstrap")
		monitorCmd.Flags().StringVar(&conf.ProvDBLoadSQL, "prov-db-load-sql", "", "List of sql scripts file to load a bootstrap")
		monitorCmd.Flags().StringVar(&conf.ProvProxType, "prov-proxy-service-type", "package", "[package|docker|podman|oci|kvm|zone|lxc]")
		monitorCmd.Flags().StringVar(&conf.ProvProxAgents, "prov-proxy-agents", "", "Comma seperated list of agents for micro services provisionning")
		monitorCmd.Flags().StringVar(&conf.ProvProxAgentsFailover, "prov-proxy-agents-failover", "", "Service Failover Agents")
		monitorCmd.Flags().StringVar(&conf.ProvProxDiskFS, "prov-proxy-disk-fs", "ext4", "[zfs|xfs|ext4]")
		monitorCmd.Flags().StringVar(&conf.ProvProxDiskPool, "prov-proxy-disk-pool", "none", "[none|zpool|lvm]")
		monitorCmd.Flags().StringVar(&conf.ProvProxDiskType, "prov-proxy-disk-type", "loopback", "[loopback|physical|pool|directory|volume]")
		monitorCmd.Flags().StringVar(&conf.ProvProxDiskDevice, "prov-proxy-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
		monitorCmd.Flags().StringVar(&conf.ProvProxVolumeData, "prov-proxy-volume-data", "", "Volume name of the data files")
		monitorCmd.Flags().StringVar(&conf.ProvProxNetIface, "prov-proxy-net-iface", "eth0", "HBA Device to hold Ips")
		monitorCmd.Flags().StringVar(&conf.ProvProxGateway, "prov-proxy-net-gateway", "192.168.0.254", "Micro Service network gateway")
		monitorCmd.Flags().StringVar(&conf.ProvProxNetmask, "prov-proxy-net-mask", "255.255.255.0", "Micro Service network mask")
		monitorCmd.Flags().StringVar(&conf.ProvProxRouteAddr, "prov-proxy-route-addr", "", "Route adress to databases proxies")
		monitorCmd.Flags().StringVar(&conf.ProvProxRoutePort, "prov-proxy-route-port", "", "Route Port to databases proxies")
		monitorCmd.Flags().StringVar(&conf.ProvProxRouteMask, "prov-proxy-route-mask", "255.255.255.0", "Route Netmask to databases proxies")
		monitorCmd.Flags().StringVar(&conf.ProvProxRoutePolicy, "prov-proxy-route-policy", "failover", "Route policy failover or balance")
		monitorCmd.Flags().StringVar(&conf.ProvProxProxysqlImg, "prov-proxy-docker-proxysql-img", "signal18/proxysql:1.4", "Docker image for proxysql")
		monitorCmd.Flags().StringVar(&conf.ProvProxMaxscaleImg, "prov-proxy-docker-maxscale-img", "mariadb/maxscale:2.2", "Docker image for maxscale proxy")
		monitorCmd.Flags().StringVar(&conf.ProvProxHaproxyImg, "prov-proxy-docker-haproxy-img", "haproxytech/haproxy-alpine:2.4", "Docker image for haproxy")
		monitorCmd.Flags().StringVar(&conf.ProvProxMysqlRouterImg, "prov-proxy-docker-mysqlrouter-img", "pulsepointinc/mysql-router", "Docker image for MySQLRouter")
		monitorCmd.Flags().StringVar(&conf.ProvProxShardingImg, "prov-proxy-docker-shardproxy-img", "signal18/mariadb104-spider", "Docker image for sharding proxy")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxImg, "prov-sphinx-docker-img", "leodido/sphinxsearch", "Docker image for SphinxSearch")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxTags, "prov-sphinx-tags", "masterslave", "playbook configuration tags wsrep,multimaster,masterslave")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxType, "prov-sphinx-service-type", "package", "[package|docker]")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxAgents, "prov-sphinx-agents", "", "Comma seperated list of agents for micro services provisionning")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskFS, "prov-sphinx-disk-fs", "ext4", "[zfs|xfs|ext4]")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskPool, "prov-sphinx-disk-pool", "none", "[none|zpool|lvm]")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskType, "prov-sphinx-disk-type", "[loopback|physical]", "[none|zpool|lvm]")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskDevice, "prov-sphinx-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxMem, "prov-sphinx-memory", "256", "Memory in M for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxDisk, "prov-sphinx-disk-size", "20", "Disk in g for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxCores, "prov-sphinx-cpu-cores", "1", "Number of cpu cores for the micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvSphinxCron, "prov-sphinx-reindex-schedule", "@5", "task time to 5 minutes for index rotation")
		monitorCmd.Flags().StringVar(&conf.ProvSSLCa, "prov-tls-server-ca", "", "server TLS ca")
		monitorCmd.Flags().StringVar(&conf.ProvSSLCert, "prov-tls-server-cert", "", "server TLS cert")
		monitorCmd.Flags().StringVar(&conf.ProvSSLKey, "prov-tls-server-key", "", "server TLS key")
		monitorCmd.Flags().BoolVar(&conf.ProvNetCNI, "prov-net-cni", false, "Networking use CNI")
		monitorCmd.Flags().StringVar(&conf.ProvNetCNICluster, "prov-net-cni-cluster", "default", "Name of of the OpenSVC network")
		monitorCmd.Flags().BoolVar(&conf.ProvDockerDaemonPrivate, "prov-docker-daemon-private", true, "Use global or private registry per service")

		if WithOpenSVC == "ON" {

			monitorCmd.Flags().BoolVar(&conf.Enterprise, "opensvc", true, "Provisioning via opensvc")
			monitorCmd.Flags().StringVar(&conf.ProvHost, "opensvc-host", "collector.signal18.io:443", "OpenSVC collector API")
			monitorCmd.Flags().StringVar(&conf.ProvAdminUser, "opensvc-admin-user", "root@signal18.io:opensvc", "OpenSVC collector admin user")
			monitorCmd.Flags().BoolVar(&conf.ProvRegister, "opensvc-register", false, "Register user codeapp to collector, load compliance")
			monitorCmd.Flags().StringVar(&conf.ProvOpensvcP12Certificate, "opensvc-p12-certificate", "/etc/replication-manager/s18.p12", "Certicate used for socket vs collector API opensvc-host refer to a cluster VIP")
			monitorCmd.Flags().BoolVar(&conf.ProvOpensvcUseCollectorAPI, "opensvc-use-collector-api", false, "Use the collector API instead of cluster VIP")
			monitorCmd.Flags().StringVar(&conf.KubeConfig, "kube-config", "", "path to ks8 config file")

			dbConfig := viper.New()
			dbConfig.SetConfigType("yaml")
			file, err := ioutil.ReadFile(conf.ConfDir + "/account.yaml")
			if err != nil {
				file, err = ioutil.ReadFile(conf.ShareDir + "/opensvc/account.yaml")
				if err != nil {
					log.Errorf("%s", err)
				}
			}
			dbConfig.ReadConfig(bytes.NewBuffer(file))
			//	log.Printf("OpenSVC user account: %s", dbConfig.Get("email").(string))
			conf.ProvUser = dbConfig.Get("email").(string) + ":" + dbConfig.Get("hashed_password").(string)
			crcTable := crc64.MakeTable(crc64.ECMA)
			conf.ProvCodeApp = "ns" + strconv.FormatUint(crc64.Checksum([]byte(dbConfig.Get("email").(string)), crcTable), 10)
			//	log.Printf("OpenSVC code application: %s", conf.ProvCodeApp)
			//	} else {
			//		monitorCmd.Flags().StringVar(&conf.ProvUser, "opensvc-user", "replication-manager@localhost.localdomain:mariadb", "OpenSVC collector provisioning user")
			//		monitorCmd.Flags().StringVar(&conf.ProvCodeApp, "opensvc-codeapp", "MariaDB", "OpenSVC collector applicative code")
			//	}

		}
	}
	//cobra.OnInitialize()
	viper.BindPFlags(monitorCmd.Flags())

}

// initRepmgrFlags function is used to initialize flags that are common to several subcommands
// e.g. monitor, failover, switchover.
// If you add a subcommand that shares flags with other subcommand scenarios please call this function.
// If you add flags that impact all the possible scenarios please do it here.
func initRepmgrFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&conf.LogFile, "log-file", "", "Write output messages to log file")
	cmd.Flags().BoolVar(&conf.LogSyslog, "log-syslog", false, "Enable logging to syslog")
	cmd.Flags().IntVar(&conf.LogLevel, "log-level", 0, "Log verbosity level")
	cmd.Flags().IntVar(&conf.LogRotateMaxSize, "log-rotate-max-size", 5, "Log rotate max size")
	cmd.Flags().IntVar(&conf.LogRotateMaxBackup, "log-rotate-max-backup", 7, "Log rotate max backup")
	cmd.Flags().IntVar(&conf.LogRotateMaxAge, "log-rotate-max-age", 7, "Log rotate max age")

	viper.BindPFlags(cmd.Flags())

}

func initDeprecated() {
	//not needed use Alias in server.go

	monitorCmd.Flags().BoolVar(&conf.ProxysqlCopyGrants, "proxysql-copy-grants", true, "Deprecate copy grants from master")
	monitorCmd.Flags().MarkDeprecated("proxysql-copy-grants", "Deprecated for proxysql-bootstrap-users")
	monitorCmd.Flags().StringVar(&conf.BackupMyDumperPath, "mydumper-path", "/usr/bin/mydumper", "Deprecate Path to mydumper binary")
	monitorCmd.Flags().MarkDeprecated("mydumper-path", "Deprecated for backup-mydumper-path")
	monitorCmd.Flags().StringVar(&conf.BackupMyLoaderPath, "myloader-path", "/usr/bin/myloader", "Deprecate Path to myloader binary")
	monitorCmd.Flags().MarkDeprecated("myloader-path", "Deprecated for backup-myloader-path")
	monitorCmd.Flags().StringVar(&conf.BackupMysqldumpPath, "mysqldump-path", "", "Deprecate Path to mysqldump binary")

	monitorCmd.Flags().MarkDeprecated("mysqldump-path", "Deprecated for backup-mysqldump-path")
	monitorCmd.Flags().StringVar(&conf.BackupMysqlbinlogPath, "mysqlbinlog-path", "", "Deprecate Path to mysqlbinlog binary")
	monitorCmd.Flags().MarkDeprecated("mysqlbinlog-path", "Deprecated for backup-mysqlbinlog-path")
	monitorCmd.Flags().StringVar(&conf.BackupMysqlclientPath, "mysqlclient-path", "", "Deprecate Path to mysql client binary")
	monitorCmd.Flags().MarkDeprecated("mysqlclient-path", "Deprecated for backup-mysqlclient-path")
	monitorCmd.Flags().StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "HaProxy binary location")
	monitorCmd.Flags().StringVar(&conf.MasterConn, "replication-master-connection", "", "Connection name to use for multisource replication")
	monitorCmd.Flags().MarkDeprecated("replication-master-connection", "Depecrate for replication-source-name")
	monitorCmd.Flags().StringVar(&conf.LogFile, "logfile", "", "Write output messages to log file")
	monitorCmd.Flags().MarkDeprecated("logfile", "Deprecate for log-file")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitKill, "wait-kill", 5000, "Deprecate for switchover-wait-kill Wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().MarkDeprecated("wait-kill", "Deprecate for switchover-wait-kill Wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().StringVar(&conf.User, "user", "", "User for database login, specified in the [user]:[password] format")
	monitorCmd.Flags().MarkDeprecated("user", "Deprecate for db-servers-credential")

	monitorCmd.Flags().StringVar(&conf.ProvDBBinaryBasedir, "db-servers-binary-path", "/usr/local/mysql/bin", "Deprecate Path to mysqld binary for testing")
	monitorCmd.Flags().MarkDeprecated("db-servers-binary-path", "Deprecate for prov-db-binary-basedir")

	monitorCmd.Flags().StringVar(&conf.Hosts, "hosts", "", "List of database hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	monitorCmd.Flags().MarkDeprecated("hosts", "Deprecate for db-servers-hosts")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCA, "hosts-tls-ca-cert", "", "TLS authority certificate")
	monitorCmd.Flags().MarkDeprecated("hosts-tls-ca-cert", "Deprecate for db-servers-tls-ca-cert")
	monitorCmd.Flags().StringVar(&conf.HostsTlsCliKey, "hosts-tls-client-key", "", "TLS client key")
	monitorCmd.Flags().MarkDeprecated("hosts-tls-client-key", "Deprecate for db-servers-tls-client-key")
	monitorCmd.Flags().StringVar(&conf.HostsTlsCliCert, "hosts-tls-client-cert", "", "TLS client certificate")
	monitorCmd.Flags().MarkDeprecated("hosts-tls-client-cert", "Deprecate for db-servers-tls-client-cert")
	monitorCmd.Flags().IntVar(&conf.Timeout, "connect-timeout", 5, "Database connection timeout in seconds")
	monitorCmd.Flags().MarkDeprecated("connect-timeout", "Deprecate for db-servers-connect-timeout")
	monitorCmd.Flags().StringVar(&conf.RplUser, "rpluser", "", "Replication user in the [user]:[password] format")
	monitorCmd.Flags().MarkDeprecated("rpluser", "Deprecate for replication-credential")
	monitorCmd.Flags().StringVar(&conf.PrefMaster, "prefmaster", "", "Preferred candidate server for master failover, in host:[port] format")
	monitorCmd.Flags().MarkDeprecated("prefmaster", "Deprecate for db-servers-prefered-master")
	monitorCmd.Flags().StringVar(&conf.IgnoreSrv, "ignore-servers", "", "List of servers to ignore in slave promotion operations")
	monitorCmd.Flags().MarkDeprecated("ignore-servers", "Deprecate for db-servers-ignored-hosts")
	monitorCmd.Flags().StringVar(&conf.MasterConn, "master-connection", "", "Connection name to use for multisource replication")
	monitorCmd.Flags().MarkDeprecated("master-connection", "Deprecate for replication-master-connection")
	monitorCmd.Flags().IntVar(&conf.MasterConnectRetry, "master-connect-retry", 10, "Specifies how many seconds to wait between slave connect retries to master")
	monitorCmd.Flags().MarkDeprecated("master-connect-retry", "Deprecate for replication-master-connection-retry")
	monitorCmd.Flags().MarkDeprecated("api-user", "Deprecate for 	api-credential")
	monitorCmd.Flags().BoolVar(&conf.ReadOnly, "readonly", true, "Set slaves as read-only after switchover failover")
	monitorCmd.Flags().MarkDeprecated("readonly", "Deprecate for failover-readonly-state")
	monitorCmd.Flags().StringVar(&conf.MxsHost, "maxscale-host", "", "MaxScale host IP")
	monitorCmd.Flags().MarkDeprecated("maxscale-host", "Deprecate for maxscale-servers")
	monitorCmd.Flags().StringVar(&conf.MdbsProxyHosts, "mdbshardproxy-hosts", "127.0.0.1:3307", "MariaDB spider proxy hosts IP:Port,IP:Port")
	monitorCmd.Flags().MarkDeprecated("mdbshardproxy-hosts", "Deprecate for mdbshardproxy-servers")
	monitorCmd.Flags().BoolVar(&conf.MultiMaster, "multimaster", false, "Turn on multi-master detection")
	monitorCmd.Flags().MarkDeprecated("multimaster", "Deprecate for replication-multi-master")
	monitorCmd.Flags().BoolVar(&conf.MultiTierSlave, "multi-tier-slave", false, "Turn on to enable relay slaves in the topology")
	monitorCmd.Flags().MarkDeprecated("multi-tier-slaver", "Deprecate for replication-multi-tier-slave")
	monitorCmd.Flags().StringVar(&conf.PreScript, "pre-failover-script", "", "Path of pre-failover script")
	monitorCmd.Flags().MarkDeprecated("pre-failover-script", "Deprecate for failover-pre-script")
	monitorCmd.Flags().StringVar(&conf.PostScript, "post-failover-script", "", "Path of post-failover script")
	monitorCmd.Flags().MarkDeprecated("post-failover-script", "Deprecate for failover-post-script")
	monitorCmd.Flags().StringVar(&conf.RejoinScript, "rejoin-script", "", "Path of old master rejoin script")
	monitorCmd.Flags().MarkDeprecated("rejoin-script", "Deprecate for autorejoin-script")
	monitorCmd.Flags().StringVar(&conf.ShareDir, "share-directory", "/usr/share/replication-manager", "Path to HTTP monitor share files")
	monitorCmd.Flags().MarkDeprecated("share-directory", "Deprecate for monitoring-sharedir")
	monitorCmd.Flags().StringVar(&conf.WorkingDir, "working-directory", "/var/lib/replication-manager", "Path to HTTP monitor working directory")
	monitorCmd.Flags().MarkDeprecated("working-directory", "Deprecate for monitoring-datadir")
	monitorCmd.Flags().BoolVar(&conf.Interactive, "interactive", true, "Ask for user interaction when failures are detected")
	monitorCmd.Flags().MarkDeprecated("interactive", "Deprecate for failover-mode")
	monitorCmd.Flags().IntVar(&conf.MaxFail, "failcount", 5, "Trigger failover after N failures (interval 1s)")
	monitorCmd.Flags().MarkDeprecated("failcount", "Deprecate for failover-falsepositive-ping-counter")
	monitorCmd.Flags().IntVar(&conf.SwitchWaitWrite, "wait-write-query", 10, "Deprecate  Wait this many seconds before write query end to cancel switchover")
	monitorCmd.Flags().MarkDeprecated("wait-write-query", "Deprecate for switchover-wait-write-query")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitTrx, "wait-trx", 10, "Depecrate for switchover-wait-trx Wait this many seconds before transactions end to cancel switchover")
	monitorCmd.Flags().MarkDeprecated("wait-trx", "Deprecate for switchover-wait-trx")
	monitorCmd.Flags().BoolVar(&conf.SwitchGtidCheck, "gtidcheck", false, "Depecrate for failover-at-equal-gtid do not initiate failover unless slaves are fully in sync")
	monitorCmd.Flags().MarkDeprecated("gtidcheck", "Deprecate for switchover-at-equal-gtid")
	monitorCmd.Flags().Int64Var(&conf.FailMaxDelay, "maxdelay", 0, "Deprecate Maximum replication delay before initiating failover")
	monitorCmd.Flags().MarkDeprecated("maxdelay", "Deprecate for failover-max-slave-delay")

}

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Starts monitoring server",
	Long: `Starts replication-manager server in stateful monitor daemon mode.

For interacting with this daemon use,
- Interactive console client: "replication-manager client".
- Command line clients: "replication-manager switchover|failover|topology|test".
- HTTP dashboards on port 10001

`,
	Run: func(cmd *cobra.Command, args []string) {

		RepMan = new(server.ReplicationManager)
		RepMan.InitConfig(conf)
		RepMan.Run()
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
		RepMan.Stop()
	},
}
