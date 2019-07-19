// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
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
	WithProvisioning      string
	WithArbitration       string
	WithArbitrationClient string
	WithProxysql          string
	WithHaproxy           string
	WithMaxscale          string
	WithMariadbshardproxy string
	WithMonitoring        string
	WithMail              string
	WithHttp              string
	WithSpider            string
	WithEnforce           string
	WithDeprecate         string
	WithOpenSVC           string
	WithMultiTiers        string
	WithTarball           string
	WithMySQLRouter       string
	WithSphinx            string
	WithBackup            string
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build    string
	GoOS     string
	GoArch   string
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
	// Copyright 2017 Signal 18 SARL
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

	//conf.FailForceGtid = true
	conf.GoArch = GoArch
	conf.GoOS = GoOS
	conf.Version = Version
	conf.FullVersion = FullVersion
	conf.MemProfile = memprofile
	conf.WithTarball = WithTarball
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

	}
	if GoOS == "linux" {
		monitorCmd.Flags().StringVar(&conf.ShareDir, "monitoring-sharedir", "/usr/share/replication-manager", "Path to share files")
		monitorCmd.Flags().StringVar(&conf.ConfDir, "monitoring-confdir", "/etc/replication-manager", "Path to a config directory")
	}
	if GoOS == "darwin" {
		monitorCmd.Flags().StringVar(&conf.ShareDir, "monitoring-sharedir", "/opt/replication-manager/share", "Path to share files")
		monitorCmd.Flags().StringVar(&conf.ConfDir, "monitoring-confdir", "/etc/replication-manager", "Path to a config directory")
	}

	monitorCmd.Flags().StringVar(&conf.WorkingDir, "monitoring-datadir", "/var/lib/replication-manager", "Path to write temporary and persistent files")
	monitorCmd.Flags().Int64Var(&conf.MonitoringTicker, "monitoring-ticker", 2, "Monitoring interval in seconds")
	monitorCmd.Flags().StringVar(&conf.TunnelHost, "monitoring-tunnel-host", "", "Bastion host to access to monitor topology via SSH tunnel host:22")
	monitorCmd.Flags().StringVar(&conf.TunnelCredential, "monitoring-tunnel-credential", "root:", "Credential Access to bastion host topology via SSH tunnel")
	monitorCmd.Flags().StringVar(&conf.TunnelKeyPath, "monitoring-tunnel-key-path", "/Users/apple/.ssh/id_rsa", "Tunnel private key path")
	monitorCmd.Flags().BoolVar(&conf.MonitorWriteHeartbeat, "monitoring-write-heartbeat", false, "Inject heartbeat into proxy or via external vip")
	monitorCmd.Flags().BoolVar(&conf.ConfRewrite, "monitoring-config-rewrite", false, "Save configuration changes to monitoring-datadir/clusterd")
	monitorCmd.Flags().StringVar(&conf.MonitorWriteHeartbeatCredential, "monitoring-write-heartbeat-credential", "", "Database user:password to inject traffic into proxy or via external vip")
	monitorCmd.Flags().BoolVar(&conf.MonitorVariableDiff, "monitoring-variable-diff", true, "Monitor variable difference beetween nodes")
	monitorCmd.Flags().BoolVar(&conf.MonitorPFS, "monitoring-performance-schema", true, "Monitor performance schema")
	monitorCmd.Flags().BoolVar(&conf.MonitorInnoDBStatus, "monitoring-innodb-status", true, "Monitor innodb status")
	monitorCmd.Flags().StringVar(&conf.MonitorIgnoreError, "monitoring-ignore-errors", "", "Comma separated list of error or warning to ignore")
	monitorCmd.Flags().BoolVar(&conf.MonitorSchemaChange, "monitoring-schema-change", true, "Monitor schema change")
	monitorCmd.Flags().StringVar(&conf.MonitorSchemaChangeScript, "monitoring-schema-change-script", "", "Monitor schema change external script")
	monitorCmd.Flags().StringVar(&conf.MonitoringSSLCert, "monitoring-ssl-cert", "", "HTTPS & API TLS certificate")
	monitorCmd.Flags().StringVar(&conf.MonitoringSSLKey, "monitoring-ssl-key", "", "HTTPS & API TLS key")
	monitorCmd.Flags().StringVar(&conf.MonitoringKeyPath, "monitprting-key-path", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	monitorCmd.Flags().BoolVar(&conf.MonitorQueries, "monitoring-queries", true, "Monitor long queries")
	monitorCmd.Flags().IntVar(&conf.MonitorLongQueryTime, "monitoring-long-query-time", 10000, "Long query time in ms")
	monitorCmd.Flags().StringVar(&conf.MonitorLongQueryScript, "monitoring-long-query-script", "", "long query time external script")
	monitorCmd.Flags().BoolVar(&conf.MonitorLongQueryWithTable, "monitoring-long-query-with-table", false, "Use log_type table to fetch slow queries")
	monitorCmd.Flags().BoolVar(&conf.MonitorLongQueryWithProcess, "monitoring-long-query-with-process", true, "Use processlist to fetch slow queries")
	monitorCmd.Flags().IntVar(&conf.MonitorLongQueryLogLength, "monitoring-long-query-log-length", 200, "Number of slow queries to keep in monitor")
	monitorCmd.Flags().IntVar(&conf.MonitorErrorLogLength, "monitoring-erreur-log-length", 20, "Number of error log line to keep in monitor")
	monitorCmd.Flags().BoolVar(&conf.MonitorScheduler, "monitoring-scheduler", false, "Enable internal scheduler")
	monitorCmd.Flags().BoolVar(&conf.MonitorProcessList, "monitoring-processlist", true, "Enable capture 50 longuest process via processlist")
	monitorCmd.Flags().StringVar(&conf.MonitorAddress, "monitoring-address", "localhost", "How to contact this monitoring")
	monitorCmd.Flags().BoolVar(&conf.LogSST, "log-sst", false, "Log open and close SST transfert")
	monitorCmd.Flags().BoolVar(&conf.LogHeartbeat, "log-heartbeat", false, "Log Heartbeat")
	monitorCmd.Flags().BoolVar(&conf.MonitorCapture, "monitoring-capture", true, "Enable capture on error for 5 monitor loops")
	monitorCmd.Flags().StringVar(&conf.MonitorCaptureTrigger, "monitoring-capture-trigger", "ERR00076,ERR00041", "List of errno triggering capturemode")

	monitorCmd.Flags().StringVar(&conf.User, "db-servers-credential", "", "Database login, specified in the [user]:[password] format")
	monitorCmd.Flags().StringVar(&conf.Hosts, "db-servers-hosts", "", "Database hosts list to monitor, IP and port (optional), specified in the host:[port] format and separated by commas")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCA, "db-servers-tls-ca-cert", "", "Database TLS authority certificate")
	monitorCmd.Flags().StringVar(&conf.HostsTLSKEY, "db-servers-tls-client-key", "", "Database TLS client key")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCLI, "db-servers-tls-client-cert", "", "Database TLS client certificate")
	monitorCmd.Flags().IntVar(&conf.Timeout, "db-servers-connect-timeout", 5, "Database connection timeout in seconds")
	monitorCmd.Flags().IntVar(&conf.ReadTimeout, "db-servers-read-timeout", 15, "Database read timeout in seconds")
	monitorCmd.Flags().StringVar(&conf.PrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")
	monitorCmd.Flags().StringVar(&conf.IgnoreSrv, "db-servers-ignored-hosts", "", "Database list of hosts to ignore in election")

	monitorCmd.Flags().BoolVar(&conf.PRXReadOnMaster, "proxy-servers-read-on-master", false, "Should RO route via proxies point to master")

	monitorCmd.Flags().Int64Var(&conf.SwitchWaitKill, "switchover-wait-kill", 5000, "Switchover wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().IntVar(&conf.SwitchWaitWrite, "switchover-wait-write-query", 10, "Switchover is canceled if a write query is running for this time")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitTrx, "switchover-wait-trx", 10, "Switchover is cancel after this timeout in second if can't aquire FTWRL")
	monitorCmd.Flags().BoolVar(&conf.SwitchSync, "switchover-at-sync", false, "Switchover Only  when state semisync is sync for last status")
	monitorCmd.Flags().BoolVar(&conf.SwitchGtidCheck, "switchover-at-equal-gtid", false, "Switchover only when slaves are fully in sync")
	monitorCmd.Flags().BoolVar(&conf.SwitchSlaveWaitCatch, "switchover-slave-wait-catch", true, "Switchover wait for slave to catch with replication, not needed in GTID mode but enable to detect possible issues like witing on old master")
	monitorCmd.Flags().BoolVar(&conf.SwitchDecreaseMaxConn, "switchover-decrease-max-conn", true, "Switchover decrease max connection on old master")
	monitorCmd.Flags().Int64Var(&conf.SwitchDecreaseMaxConnValue, "switchover-decrease-max-conn-value", 10, "Switchoverd decrease max connection to this value different according to flavor")

	monitorCmd.Flags().StringVar(&conf.MasterConn, "replication-source-name", "", "Replication channel name to use for multisource")
	monitorCmd.Flags().IntVar(&conf.MasterConnectRetry, "replication-master-connect-retry", 10, "Replication is define using this connection retry timeout")
	monitorCmd.Flags().StringVar(&conf.RplUser, "replication-credential", "", "Replication user in the [user]:[password] format")
	monitorCmd.Flags().BoolVar(&conf.ReplicationSSL, "replication-use-ssl", false, "Replication use SSL encryption to replicate from master")
	monitorCmd.Flags().BoolVar(&conf.MultiMaster, "replication-multi-master", false, "Multi-master topology")
	monitorCmd.Flags().BoolVar(&conf.MultiMasterWsrep, "replication-multi-master-wsrep", false, "Enable Galera multi-master")
	monitorCmd.Flags().BoolVar(&conf.MultiMasterRing, "replication-multi-master-ring", false, "Multi-master ring topology")
	monitorCmd.Flags().BoolVar(&conf.MultiTierSlave, "replication-multi-tier-slave", false, "Relay slaves topology")
	monitorCmd.Flags().BoolVar(&conf.ReplicationNoRelay, "replication-master-slave-never-relay", true, "Do not allow relay server MSS MXS XXM RSM")
	monitorCmd.Flags().StringVar(&conf.ReplicationErrorScript, "replication-error-script", "", "Replication error script")

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
	monitorCmd.Flags().BoolVar(&conf.FailEventStatus, "failover-event-status", false, "Failover event status ENABLE OR DISABLE ON SLAVE")
	monitorCmd.Flags().BoolVar(&conf.CheckFalsePositiveHeartbeat, "failover-falsepositive-heartbeat", true, "Failover checks that slaves do not receive heartbeat")
	monitorCmd.Flags().IntVar(&conf.CheckFalsePositiveHeartbeatTimeout, "failover-falsepositive-heartbeat-timeout", 3, "Failover checks that slaves do not receive heartbeat detection timeout ")
	monitorCmd.Flags().BoolVar(&conf.CheckFalsePositiveExternal, "failover-falsepositive-external", false, "Failover checks that http//master:80 does not reponse 200 OK header")
	monitorCmd.Flags().IntVar(&conf.CheckFalsePositiveExternalPort, "failover-falsepositive-external-port", 80, "Failover checks external port")
	monitorCmd.Flags().IntVar(&conf.MaxFail, "failover-falsepositive-ping-counter", 5, "Failover after this number of ping failures (interval 1s)")

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
	monitorCmd.Flags().StringVar(&conf.APIUser, "api-credential", "admin:repman", "Rest API user:password")
	monitorCmd.Flags().StringVar(&conf.APIBind, "api-bind", "0.0.0.0", "Rest API bind ip")
	monitorCmd.Flags().BoolVar(&conf.APIHttpsBind, "api-https-bind", false, "Bind API call to https Web UI will error with http")

	//monitorCmd.Flags().BoolVar(&conf.Daemon, "daemon", true, "Daemon mode. Do not start the Termbox console")
	conf.Daemon = true

	if WithEnforce == "ON" {
		monitorCmd.Flags().BoolVar(&conf.ForceSlaveReadOnly, "force-slave-readonly", false, "Automatically activate read only on slave")
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

	if WithHttp == "ON" {
		monitorCmd.Flags().BoolVar(&conf.HttpServ, "http-server", true, "Start the HTTP monitor")
		monitorCmd.Flags().StringVar(&conf.BindAddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
		monitorCmd.Flags().StringVar(&conf.HttpPort, "http-port", "10001", "HTTP monitor to listen on this port")
		if GoOS == "linux" {
			monitorCmd.Flags().StringVar(&conf.HttpRoot, "http-root", "/usr/share/replication-manager/dashboard", "Path to HTTP replication-monitor files")
		}
		if GoOS == "darwin" {
			monitorCmd.Flags().StringVar(&conf.HttpRoot, "http-root", "/opt/replication-manager/share/dashboard", "Path to HTTP replication-monitor files")
		}
		monitorCmd.Flags().IntVar(&conf.SessionLifeTime, "http-session-lifetime", 3600, "Http Session life time ")
	}
	if WithMail == "ON" {
		monitorCmd.Flags().StringVar(&conf.MailFrom, "mail-from", "mrm@localhost", "Alert email sender")
		monitorCmd.Flags().StringVar(&conf.MailTo, "mail-to", "", "Alert email recipients, separated by commas")
		monitorCmd.Flags().StringVar(&conf.MailSMTPAddr, "mail-smtp-addr", "localhost:25", "Alert email SMTP server address, in host:[port] format")
		monitorCmd.Flags().StringVar(&conf.MailSMTPUser, "mail-smtp-user", "", "SMTP user")
		monitorCmd.Flags().StringVar(&conf.MailSMTPPassword, "mail-smtp-password", "", "SMTP password")
	}

	monitorCmd.Flags().BoolVar(&conf.ExtProxyOn, "extproxy", false, "External proxy can be used to specify a route manage with external scripts")
	monitorCmd.Flags().StringVar(&conf.ExtProxyVIP, "extproxy-address", "", "Network address when route is manage via external script,  host:[port] format")

	if WithMaxscale == "ON" {
		monitorCmd.Flags().BoolVar(&conf.MxsOn, "maxscale", false, "MaxScale proxy server is query for backend status")
		monitorCmd.Flags().BoolVar(&conf.CheckFalsePositiveMaxscale, "failover-falsepositive-maxscale", false, "Failover checks that maxscale detect failed master")
		monitorCmd.Flags().IntVar(&conf.CheckFalsePositiveMaxscaleTimeout, "failover-falsepositive-maxscale-timeout", 14, "Failover checks that maxscale detect failed master")
		monitorCmd.Flags().BoolVar(&conf.MxsBinlogOn, "maxscale-binlog", false, "Maxscale binlog server topolgy")
		monitorCmd.Flags().MarkDeprecated("maxscale-monitor", "Deprecate disable maxscale monitoring for 2 nodes cluster")
		monitorCmd.Flags().BoolVar(&conf.MxsDisableMonitor, "maxscale-disable-monitor", false, "Disable maxscale monitoring and fully drive server state")
		monitorCmd.Flags().StringVar(&conf.MxsGetInfoMethod, "maxscale-get-info-method", "maxadmin", "How to get infos from Maxscale maxinfo|maxadmin")
		monitorCmd.Flags().StringVar(&conf.MxsHost, "maxscale-servers", "", "MaxScale hosts ")
		monitorCmd.Flags().StringVar(&conf.MxsPort, "maxscale-port", "6603", "MaxScale admin port")
		monitorCmd.Flags().StringVar(&conf.MxsUser, "maxscale-user", "admin", "MaxScale admin user")
		monitorCmd.Flags().StringVar(&conf.MxsPass, "maxscale-pass", "mariadb", "MaxScale admin password")
		monitorCmd.Flags().IntVar(&conf.MxsWritePort, "maxscale-write-port", 3306, "MaxScale read-write port to leader")
		monitorCmd.Flags().IntVar(&conf.MxsReadPort, "maxscale-read-port", 3307, "MaxScale load balance read port to all nodes")
		monitorCmd.Flags().IntVar(&conf.MxsReadWritePort, "maxscale-read-write-port", 3308, "MaxScale load balance read port to all nodes")
		monitorCmd.Flags().IntVar(&conf.MxsMaxinfoPort, "maxscale-maxinfo-port", 3309, "MaxScale maxinfo plugin http port")
		monitorCmd.Flags().IntVar(&conf.MxsBinlogPort, "maxscale-binlog-port", 3309, "MaxScale maxinfo plugin http port")
		monitorCmd.Flags().BoolVar(&conf.MxsServerMatchPort, "maxscale-server-match-port", false, "Match servers running on same host with different port")
	}

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

	if WithMariadbshardproxy == "ON" {
		monitorCmd.Flags().BoolVar(&conf.MdbsProxyOn, "shardproxy", false, "MariaDB Spider proxy")
		monitorCmd.Flags().StringVar(&conf.MdbsProxyHosts, "shardproxy-servers", "127.0.0.1:3307", "MariaDB spider proxy hosts IP:Port,IP:Port")
		monitorCmd.Flags().StringVar(&conf.MdbsProxyUser, "shardproxy-credential", "root:mariadb", "MariaDB spider proxy credential")
		monitorCmd.Flags().BoolVar(&conf.MdbsProxyCopyGrants, "shardproxy-copy-grants", true, "Copy grants from shards master")
		monitorCmd.Flags().BoolVar(&conf.MdbsProxyLoadSystem, "shardproxy-load-system", true, "Load Spider system tables")
		monitorCmd.Flags().StringVar(&conf.MdbsUniversalTables, "shardproxy-universal-tables", "replication_manager_schema.bench", "MariaDB spider proxy table list that are federarated to all master")
		monitorCmd.Flags().StringVar(&conf.MdbsIngoreTables, "shardproxy-ignore-tables", "", "MariaDB spider proxy master table list that are ignored")
	}
	if WithHaproxy == "ON" {
		monitorCmd.Flags().BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper to use HaProxy on same host")
		monitorCmd.Flags().StringVar(&conf.HaproxyHosts, "haproxy-servers", "127.0.0.1", "HaProxy hosts")
		monitorCmd.Flags().IntVar(&conf.HaproxyWritePort, "haproxy-write-port", 3306, "HaProxy read-write port to leader")
		monitorCmd.Flags().IntVar(&conf.HaproxyReadPort, "haproxy-read-port", 3307, "HaProxy load balance read port to all nodes")
		monitorCmd.Flags().IntVar(&conf.HaproxyStatPort, "haproxy-stat-port", 1988, "HaProxy statistics port")
		monitorCmd.Flags().StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "HaProxy binary location")
		monitorCmd.Flags().StringVar(&conf.HaproxyReadBindIp, "haproxy-ip-read-bind", "0.0.0.0", "HaProxy input bind address for read")
		monitorCmd.Flags().StringVar(&conf.HaproxyWriteBindIp, "haproxy-ip-write-bind", "0.0.0.0", "HaProxy input bind address for write")
	}
	monitorCmd.Flags().BoolVar(&conf.MyproxyOn, "myproxy", false, "Use Internal Proxy")
	monitorCmd.Flags().IntVar(&conf.MyproxyPort, "myproxy-port", 4000, "Internal proxy read/write port")
	monitorCmd.Flags().StringVar(&conf.MyproxyUser, "myproxy-user", "admin", "Myproxy user")
	monitorCmd.Flags().StringVar(&conf.MyproxyPassword, "myproxy-password", "repman", "Myproxy password")

	if WithProxysql == "ON" {
		monitorCmd.Flags().BoolVar(&conf.ProxysqlOn, "proxysql", false, "Use ProxySQL")
		monitorCmd.Flags().StringVar(&conf.ProxysqlHosts, "proxysql-servers", "", "ProxySQL hosts")
		monitorCmd.Flags().StringVar(&conf.ProxysqlPort, "proxysql-port", "6033", "ProxySQL read/write proxy port")
		monitorCmd.Flags().StringVar(&conf.ProxysqlAdminPort, "proxysql-admin-port", "6032", "ProxySQL admin interface port")
		monitorCmd.Flags().StringVar(&conf.ProxysqlReaderHostgroup, "proxysql-reader-hostgroup", "1", "ProxySQL reader hostgroup")
		monitorCmd.Flags().StringVar(&conf.ProxysqlWriterHostgroup, "proxysql-writer-hostgroup", "0", "ProxySQL writer hostgroup")
		monitorCmd.Flags().StringVar(&conf.ProxysqlUser, "proxysql-user", "admin", "ProxySQL admin user")
		monitorCmd.Flags().StringVar(&conf.ProxysqlPassword, "proxysql-password", "admin", "ProxySQL admin password")
		monitorCmd.Flags().BoolVar(&conf.ProxysqlCopyGrants, "proxysql-copy-grants", true, "Copy grants from master")
		monitorCmd.Flags().BoolVar(&conf.ProxysqlBootstrap, "proxysql-bootstrap", false, "Bootstrap ProxySQL config from replication-manager config")
	}
	if WithSphinx == "ON" {
		monitorCmd.Flags().BoolVar(&conf.SphinxOn, "sphinx", false, "Turn on SphinxSearch detection")
		monitorCmd.Flags().StringVar(&conf.SphinxHosts, "sphinx-servers", "127.0.0.1", "SphinxSearch hosts")
		monitorCmd.Flags().StringVar(&conf.SphinxPort, "sphinx-port", "9312", "SphinxSearch API port")
		monitorCmd.Flags().StringVar(&conf.SphinxQLPort, "sphinx-sql-port", "9306", "SphinxSearch SQL port")
		if GoOS == "linux" {
			monitorCmd.Flags().StringVar(&conf.SphinxConfig, "sphinx-config", "/usr/share/replication-manager/shinx/sphinx.conf", "Path to sphinx config")
		}
		if GoOS == "darwin" {
			monitorCmd.Flags().StringVar(&conf.SphinxConfig, "sphinx-config", "/opt/replication-manager/share/sphinx/sphinx.conf", "Path to sphinx config")
		}
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
	}

	if WithSpider == "ON" {
		monitorCmd.Flags().BoolVar(&conf.Spider, "spider", false, "Turn on spider detection")
	}

	monitorCmd.Flags().BoolVar(&conf.SchedulerBackupLogical, "scheduler-db-servers-logical-backup", true, "Schedule logical backup")
	monitorCmd.Flags().BoolVar(&conf.SchedulerBackupPhysical, "scheduler-db-servers-physical-backup", true, "Schedule logical backup")
	monitorCmd.Flags().BoolVar(&conf.SchedulerDatabaseLogs, "scheduler-db-servers-logs", true, "Schedule database logs fetching")
	monitorCmd.Flags().BoolVar(&conf.SchedulerDatabaseOptimize, "scheduler-db-servers-optimize", true, "Schedule database optimize")

	monitorCmd.Flags().StringVar(&conf.BackupLogicalCron, "scheduler-db-servers-logical-backup-cron", "0 0 1 * * 6", "Logical backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupPhysicalCron, "scheduler-db-servers-physical-backup-cron", "0 0 0 * * *", "Physical backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupDatabaseLogCron, "scheduler-db-servers-logs-cron", "0 0/10 * * * *", "Logs backup cron expression represents a set of times, using 6 space-separated fields.")
	monitorCmd.Flags().StringVar(&conf.BackupDatabaseOptimizeCron, "scheduler-db-servers-optimize-cron", "0 0 3 1 * 5", "Optimize cron expression represents a set of times, using 6 space-separated fields.")

	if WithBackup == "ON" {
		monitorCmd.Flags().BoolVar(&conf.Backup, "backup", false, "Turn on Backup")
		monitorCmd.Flags().IntVar(&conf.BackupKeepHourly, "backup-keep-hourly", 1, "Keep this number of hourly backup")
		monitorCmd.Flags().IntVar(&conf.BackupKeepDaily, "backup-keep-daily", 1, "Keep this number of daily backup")
		monitorCmd.Flags().IntVar(&conf.BackupKeepWeekly, "backup-keep-weekly", 1, "Keep this number of weekly backup")
		monitorCmd.Flags().IntVar(&conf.BackupKeepMonthly, "backup-keep-monthly", 1, "Keep this number of monthly backup")
		monitorCmd.Flags().IntVar(&conf.BackupKeepYearly, "backup-keep-yearly", 1, "Keep this number of yearly backup")

		monitorCmd.Flags().StringVar(&conf.BackupLogicalType, "backup-logical-type", "mysqldump", "type of logical backup: river|mysqldump|mydumper")
		monitorCmd.Flags().StringVar(&conf.BackupPhysicalType, "backup-physical-type", "xtrabackup", "type of physical backup: xtrabackup|mariabackup")
		monitorCmd.Flags().StringVar(&conf.BackupRepo, "backup-repo", "directory", "type of directory: directory|aws|rest")
		monitorCmd.Flags().StringVar(&conf.BackupRepoAwsURI, "backup-repo-aws-uri", "", "Repo address")
		monitorCmd.Flags().StringVar(&conf.BackupRepoAwsKey, "backup-repo-aws-key", "", "AWS key ")
		monitorCmd.Flags().StringVar(&conf.BackupRepoAwsSecret, "backup-repo-aws-key-secret", "", "AWS key secret")
	}
	if WithProvisioning == "ON" {
		monitorCmd.Flags().BoolVar(&conf.Test, "test", true, "Enable non regression tests")
		monitorCmd.Flags().BoolVar(&conf.TestInjectTraffic, "test-inject-traffic", false, "Inject some database traffic via proxy")
		monitorCmd.Flags().IntVar(&conf.SysbenchTime, "sysbench-time", 100, "Time to run benchmark")
		monitorCmd.Flags().IntVar(&conf.SysbenchThreads, "sysbench-threads", 4, "Number of threads to run benchmark")
		monitorCmd.Flags().StringVar(&conf.SysbenchBinaryPath, "sysbench-binary-path", "/usr/bin/sysbench", "Sysbench Wrapper in test mode")
		monitorCmd.Flags().StringVar(&conf.MariaDBBinaryPath, "db-servers-binary-path", "/usr/local/mysql/bin", "Path to mysqld binary for testing")
		monitorCmd.Flags().StringVar(&conf.ProvDatadirVersion, "prov-db-datadir-version", "10.2", "Empty datadir to deploy for localtest")
		monitorCmd.Flags().StringVar(&conf.ProvMem, "prov-db-memory", "256", "Memory in M for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvDisk, "prov-db-disk-size", "20", "Disk in g for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvIops, "prov-db-disk-iops", "300", "Rnd IO/s in for micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvCores, "prov-db-cpu-cores", "1", "Number of cpu cores for the micro service VM")
		monitorCmd.Flags().StringVar(&conf.ProvDbImg, "prov-db-docker-img", "mariadb:latest", "Docker image for database")
		monitorCmd.Flags().StringVar(&conf.ProvTags, "prov-db-tags", "semisync,innodb,noquerycache,threadpool,slow,pfs,compressbinlog,docker,linux,readonly", "playbook configuration tags")

		if WithOpenSVC == "ON" {

			monitorCmd.Flags().BoolVar(&conf.Enterprise, "opensvc", true, "Provisioning via opensvc")
			monitorCmd.Flags().StringVar(&conf.ProvHost, "opensvc-host", "collector.signal18.io:443", "OpenSVC collector API")
			monitorCmd.Flags().StringVar(&conf.ProvAdminUser, "opensvc-admin-user", "root@signal18.io:opensvc", "OpenSVC collector admin user")
			monitorCmd.Flags().BoolVar(&conf.ProvRegister, "opensvc-register", false, "Register user codeapp to collector, load compliance")

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
			monitorCmd.Flags().StringVar(&conf.ProvType, "prov-db-service-type ", "package", "[package|docker]")
			monitorCmd.Flags().StringVar(&conf.ProvAgents, "prov-db-agents", "", "Comma seperated list of agents for micro services provisionning")
			monitorCmd.Flags().StringVar(&conf.ProvDiskFS, "prov-db-disk-fs", "ext4", "[zfs|xfs|ext4]")
			monitorCmd.Flags().StringVar(&conf.ProvDiskPool, "prov-db-disk-pool", "none", "[none|zpool|lvm]")
			monitorCmd.Flags().StringVar(&conf.ProvDiskType, "prov-db-disk-type", "loopback", "[loopback|physical|pool|directory]")
			monitorCmd.Flags().StringVar(&conf.ProvDiskDevice, "prov-db-disk-device", "/srv", "loopback:path-to-loopfile|physical:/dev/xx|pool:pool-name|directory:/srv")
			monitorCmd.Flags().BoolVar(&conf.ProvDiskSnapshot, "prov-db-disk-snapshot-prefered-master", false, "Take snapshoot of prefered master")
			monitorCmd.Flags().IntVar(&conf.ProvDiskSnapshotKeep, "prov-db-disk-snapshot-keep", 7, "Keek this number of snapshoot of prefered master")
			monitorCmd.Flags().StringVar(&conf.ProvNetIface, "prov-db-net-iface", "eth0", "HBA Device to hold Ips")
			monitorCmd.Flags().StringVar(&conf.ProvGateway, "prov-db-net-gateway", "192.168.0.254", "Micro Service network gateway")
			monitorCmd.Flags().StringVar(&conf.ProvNetmask, "prov-db-net-mask", "255.255.255.0", "Micro Service network mask")
			monitorCmd.Flags().StringVar(&conf.ProvDBLoadCSV, "prov-db-load-csv", "", "List of shema.table csv file to load a bootstrap")
			monitorCmd.Flags().StringVar(&conf.ProvDBLoadSQL, "prov-db-load-sql", "", "List of sql scripts file to load a bootstrap")
			monitorCmd.Flags().StringVar(&conf.ProvProxTags, "prov-proxy-tags", "masterslave", "playbook configuration tags wsrep,multimaster,masterslave")
			monitorCmd.Flags().StringVar(&conf.ProvProxType, "prov-proxy-service-type", "package", "[package|docker]")
			monitorCmd.Flags().StringVar(&conf.ProvProxAgents, "prov-proxy-agents", "", "Comma seperated list of agents for micro services provisionning")
			monitorCmd.Flags().StringVar(&conf.ProvProxDisk, "prov-proxy-disk-size", "20g", "Disk in g for micro service VM")
			monitorCmd.Flags().StringVar(&conf.ProvProxDiskFS, "prov-proxy-disk-fs", "ext4", "[zfs|xfs|ext4]")
			monitorCmd.Flags().StringVar(&conf.ProvProxDiskPool, "prov-proxy-disk-pool", "none", "[none|zpool|lvm]")
			monitorCmd.Flags().StringVar(&conf.ProvProxDiskType, "prov-proxy-disk-type", "[loopback|physical]", "[none|zpool|lvm]")
			monitorCmd.Flags().StringVar(&conf.ProvProxDiskDevice, "prov-proxy-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
			monitorCmd.Flags().StringVar(&conf.ProvProxNetIface, "prov-proxy-net-iface", "eth0", "HBA Device to hold Ips")
			monitorCmd.Flags().StringVar(&conf.ProvProxGateway, "prov-proxy-net-gateway", "192.168.0.254", "Micro Service network gateway")
			monitorCmd.Flags().StringVar(&conf.ProvProxNetmask, "prov-proxy-net-mask", "255.255.255.0", "Micro Service network mask")
			monitorCmd.Flags().StringVar(&conf.ProvProxRouteAddr, "prov-proxy-route-addr", "", "Route adress to databases proxies")
			monitorCmd.Flags().StringVar(&conf.ProvProxRoutePort, "prov-proxy-route-port", "", "Route Port to databases proxies")
			monitorCmd.Flags().StringVar(&conf.ProvProxRouteMask, "prov-proxy-route-mask", "255.255.255.0", "Route Netmask to databases proxies")
			monitorCmd.Flags().StringVar(&conf.ProvProxRoutePolicy, "prov-proxy-route-policy", "failover", "Route policy failover or balance")
			monitorCmd.Flags().StringVar(&conf.ProvProxProxysqlImg, "prov-proxy-docker-proxysql-img", "signal18/proxysql:1.4", "Docker image for proxysql")
			monitorCmd.Flags().StringVar(&conf.ProvProxMaxscaleImg, "prov-proxy-docker-maxscale-img", "asosso/maxscale:latest", "Docker image for maxscale proxy")
			monitorCmd.Flags().StringVar(&conf.ProvProxHaproxyImg, "prov-proxy-docker-haproxy-img", "haproxy:alpine", "Docker image for haproxy")
			monitorCmd.Flags().StringVar(&conf.ProvProxMysqlRouterImg, "prov-proxy-docker-mysqlrouter-img", "pulsepointinc/mysql-router", "Docker image for MySQLRouter")
			monitorCmd.Flags().StringVar(&conf.ProvProxShardingImg, "prov-proxy-docker-shardproxy-img", "signal18/shardproxy", "Docker image for sharding proxy")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxImg, "prov-sphinx-docker-img", "leodido/sphinxsearch", "Docker image for SphinxSearch")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxTags, "prov-sphinx-tags", "masterslave", "playbook configuration tags wsrep,multimaster,masterslave")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxType, "prov-sphinx-service-type", "package", "[package|docker]")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxAgents, "prov-sphinx-agents", "", "Comma seperated list of agents for micro services provisionning")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskFS, "prov-sphinx-disk-fs", "ext4", "[zfs|xfs|ext4]")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskPool, "prov-sphinx-disk-pool", "none", "[none|zpool|lvm]")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskType, "prov-sphinx-disk-type", "[loopback|physical]", "[none|zpool|lvm]")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxDiskDevice, "prov-sphinx-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxMem, "prov-sphinx-memory", "256", "Memory in M for micro service VM")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxDisk, "prov-sphinx-disk-size", "20g", "Disk in g for micro service VM")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxCores, "prov-sphinx-cpu-cores", "1", "Number of cpu cores for the micro service VM")
			monitorCmd.Flags().StringVar(&conf.ProvSphinxCron, "prov-sphinx-reindex-schedule", "@5", "task time to 5 minutes for index rotation")
			monitorCmd.Flags().StringVar(&conf.ProvSSLCa, "prov-tls-server-ca", "", "server TLS ca")
			monitorCmd.Flags().StringVar(&conf.ProvSSLCert, "prov-tls-server-cert", "", "server TLS cert")
			monitorCmd.Flags().StringVar(&conf.ProvSSLKey, "prov-tls-server-key", "", "server TLS key")
			monitorCmd.Flags().BoolVar(&conf.ProvNetCNI, "prov-net-cni", false, "Networking use CNI")
			monitorCmd.Flags().StringVar(&conf.ProvNetCNICluster, "prov-net-cni-cluster", "default", "Name of OpenSVC agent cluster")
			monitorCmd.Flags().BoolVar(&conf.ProvDockerDaemonPrivate, "prov-docker-daemon-private", true, "Use global or private registry per service")
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

	viper.BindPFlags(cmd.Flags())

}

func initDeprecated() {

	monitorCmd.Flags().StringVar(&conf.MasterConn, "replication-master-connection", "", "Connection name to use for multisource replication")
	monitorCmd.Flags().MarkDeprecated("replication-master-connection", "Depecrate for replication-source-name")
	monitorCmd.Flags().StringVar(&conf.LogFile, "logfile", "", "Write output messages to log file")
	monitorCmd.Flags().MarkDeprecated("logfile", "Deprecate for log-file")
	monitorCmd.Flags().Int64Var(&conf.SwitchWaitKill, "wait-kill", 5000, "Deprecate for switchover-wait-kill Wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().MarkDeprecated("wait-kill", "Deprecate for switchover-wait-kill Wait this many milliseconds before killing threads on demoted master")
	monitorCmd.Flags().StringVar(&conf.User, "user", "", "User for database login, specified in the [user]:[password] format")
	monitorCmd.Flags().MarkDeprecated("user", "Deprecate for db-servers-credential")
	monitorCmd.Flags().StringVar(&conf.Hosts, "hosts", "", "List of database hosts IP and port (optional), specified in the host:[port] format and separated by commas")
	monitorCmd.Flags().MarkDeprecated("hosts", "Deprecate for db-servers-hosts")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCA, "hosts-tls-ca-cert", "", "TLS authority certificate")
	monitorCmd.Flags().MarkDeprecated("hosts-tls-ca-cert", "Deprecate for db-servers-tls-ca-cert")
	monitorCmd.Flags().StringVar(&conf.HostsTLSKEY, "hosts-tls-client-key", "", "TLS client key")
	monitorCmd.Flags().MarkDeprecated("hosts-tls-client-key", "Deprecate for db-servers-tls-client-key")
	monitorCmd.Flags().StringVar(&conf.HostsTLSCLI, "hosts-tls-client-cert", "", "TLS client certificate")
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
	monitorCmd.Flags().StringVar(&conf.APIUser, "api-user", "admin:repman", "Rest API user:password")
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
