// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"hash"
	"hash/crc64"
	"io"
	"log/syslog"
	"net"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"

	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"

	clog "github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/sirupsen/logrus/hooks/writer"

	termbox "github.com/nsf/termbox-go"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/etc"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/regtest"
	"github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/spf13/pflag"
)

var RepMan *ReplicationManager

// Global variables
type ReplicationManager struct {
	OpenSVC          opensvc.Collector                 `json:"-"`
	Version          string                            `json:"version"`
	Fullversion      string                            `json:"fullVersion"`
	Os               string                            `json:"os"`
	Arch             string                            `json:"arch"`
	MemProfile       string                            `json:"memprofile"`
	CpuProfile       string                            `json:"cpuprofile"`
	Clusters         map[string]*cluster.Cluster       `json:"-"`
	Agents           []opensvc.Host                    `json:"agents"`
	UUID             string                            `json:"uuid"`
	Hostname         string                            `json:"hostname"`
	Status           string                            `json:"status"`
	SplitBrain       bool                              `json:"spitBrain"`
	ClusterList      []string                          `json:"clusters"`
	Tests            []string                          `json:"tests"`
	Conf             config.Config                     `json:"config"`
	ImmuableFlagMaps map[string]map[string]interface{} `json:"-"`
	DynamicFlagMaps  map[string]map[string]interface{} `json:"-"`
	DefaultFlagMap   map[string]interface{}            `json:"-"`
	//Adding default flags from AddFlags
	CommandLineFlag                                  []string                    `json:"-"`
	ConfigPathList                                   []string                    `json:"-"`
	Logs                                             s18log.HttpLog              `json:"logs"`
	ServicePlans                                     []config.ServicePlan        `json:"servicePlans"`
	ServiceOrchestrators                             []config.ConfigVariableType `json:"serviceOrchestrators"`
	ServiceAcl                                       []config.Grant              `json:"serviceAcl"`
	ServiceRepos                                     []config.DockerRepo         `json:"serviceRepos"`
	ServiceTarballs                                  []config.Tarball            `json:"serviceTarballs"`
	ServiceFS                                        map[string]bool             `json:"serviceFS"`
	ServiceVM                                        map[string]bool             `json:"serviceVM"`
	ServiceDisk                                      map[string]string           `json:"serviceDisk"`
	ServicePool                                      map[string]bool             `json:"servicePool"`
	BackupLogicalList                                map[string]bool             `json:"backupLogicalList"`
	BackupPhysicalList                               map[string]bool             `json:"backupPhysicalList"`
	BackupBinlogList                                 map[string]bool             `json:"backupBinlogList"`
	BinlogParseList                                  map[string]bool             `json:"binlogParseList"`
	GraphiteTemplateList                             map[string]bool             `json:"graphiteTemplateList"`
	currentCluster                                   *cluster.Cluster            `json:"-"`
	UserAuthTry                                      sync.Map                    `json:"-"`
	OAuthAccessToken                                 *oauth2.Token               `json:"-"`
	ViperConfig                                      *viper.Viper                `json:"-"`
	tlog                                             s18log.TermLog
	termlength                                       int
	exitMsg                                          string
	exit                                             bool
	isStarted                                        bool
	Confs                                            map[string]config.Config
	VersionConfs                                     map[string]*config.ConfVersion `json:"-"`
	grpcServer                                       *grpc.Server                   `json:"-"`
	grpcWrapped                                      *grpcweb.WrappedGrpcServer     `json:"-"`
	V3Up                                             chan bool                      `json:"-"`
	v3Config                                         Repmanv3Config                 `json:"-"`
	cloud18CheckSum                                  hash.Hash                      `json:"-"`
	clog                                             *clog.Logger                   `json:"-"`
	repmanv3.UnimplementedClusterPublicServiceServer `json:"-"`
	repmanv3.UnimplementedClusterServiceServer       `json:"-"`
	sync.Mutex
}

const (
	ConstMonitorActif   string = "A"
	ConstMonitorStandby string = "S"
)

type authTry struct {
	User string    `json:"username"`
	Try  int       `json:"try"`
	Time time.Time `json:"time"`
}

// Unused in server still used in client cmd line
type Settings struct {
	Enterprise          string   `json:"enterprise"`
	Interactive         string   `json:"interactive"`
	FailoverCtr         string   `json:"failoverctr"`
	MaxDelay            string   `json:"maxdelay"`
	Faillimit           string   `json:"faillimit"`
	LastFailover        string   `json:"lastfailover"`
	MonHearbeats        string   `json:"monheartbeats"`
	Uptime              string   `json:"uptime"`
	UptimeFailable      string   `json:"uptimefailable"`
	UptimeSemiSync      string   `json:"uptimesemisync"`
	RplChecks           string   `json:"rplchecks"`
	FailSync            string   `json:"failsync"`
	SwitchSync          string   `json:"switchsync"`
	Verbose             string   `json:"verbose"`
	Rejoin              string   `json:"rejoin"`
	RejoinBackupBinlog  string   `json:"rejoinbackupbinlog"`
	RejoinSemiSync      string   `json:"rejoinsemisync"`
	RejoinFlashback     string   `json:"rejoinflashback"`
	RejoinUnsafe        string   `json:"rejoinunsafe"`
	RejoinDump          string   `json:"rejoindump"`
	RejoinPseudoGTID    string   `json:"rejoinpseudogtid"`
	Test                string   `json:"test"`
	Heartbeat           string   `json:"heartbeat"`
	Status              string   `json:"runstatus"`
	IsActive            string   `json:"isactive"`
	ConfGroup           string   `json:"confgroup"`
	MonitoringTicker    string   `json:"monitoringticker"`
	FailResetTime       string   `json:"failresettime"`
	ToSessionEnd        string   `json:"tosessionend"`
	HttpAuth            string   `json:"httpauth"`
	HttpBootstrapButton string   `json:"httpbootstrapbutton"`
	GraphiteMetrics     string   `json:"graphitemetrics"`
	Clusters            []string `json:"clusters"`
	RegTests            []string `json:"regtests"`
	Topology            string   `json:"topology"`
	Version             string   `json:"version"`
	DBTags              []string `json:"databasetags"`
	ProxyTags           []string `json:"proxytags"`
	//	Scheduler           []cron.Entry `json:"scheduler"`
}

// A Heartbeat returns a quick overview of the cluster status
//
// swagger:response heartbeat
type HeartbeatResponse struct {
	// Heartbeat message
	// in: body
	Body Heartbeat
}

type Heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
	Status  string `json:"status"`
	Hosts   int    `json:"hosts"`
	Failed  int    `json:"failed"`
}

var confs = make(map[string]config.Config)
var cfgGroup string
var cfgGroupIndex int

func (repman *ReplicationManager) SetDefaultFlags(v *viper.Viper) {

	repman.DefaultFlagMap = make(map[string]interface{})
	for _, f := range v.AllKeys() {
		repman.DefaultFlagMap[f] = v.Get(f)
		//	fmt.Printf("%s %v \n", f, v.Get(f))
	}

	/*flags.VisitAll(func(f *pflag.Flag) {
		fmt.Printf("%s,%v", f.Name, f.Value)
		repman.DefaultFlagMapBis[f.Name] = f.Value
	})*/

}

func (repman *ReplicationManager) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.IntVar(&conf.TokenTimeout, "api-token-timeout", 48, "Timespan of API Token before expired in hour")

	if WithDeprecate == "ON" {
		//	initDeprecated() // not needed used alias in main
	}

	if WithTarball == "ON" {
		flags.StringVar(&conf.BaseDir, "monitoring-basedir", "/usr/local/replication-manager", "Path to a basedir where data and share sub directory can be found")
		flags.StringVar(&conf.ConfDir, "monitoring-confdir", "/usr/local/replication-manager/etc", "Path to a config directory")
	} else {
		flags.StringVar(&conf.BaseDir, "monitoring-basedir", "system", "Path to a basedir where a data and share directory can be found")
		flags.StringVar(&conf.ConfDir, "monitoring-confdir", "/etc/replication-manager", "Path to a config directory")
	}

	if runtime.GOOS == "darwin" {
		flags.StringVar(&conf.ShareDir, "monitoring-sharedir", "/opt/replication-manager/share", "Path to share files")
	} else {
		flags.StringVar(&conf.ShareDir, "monitoring-sharedir", "/usr/share/replication-manager", "Path to share files")
	}

	// Important flags for monitoring
	flags.BoolVar(&conf.ConfRewrite, "monitoring-save-config", false, "Save configuration changes to <monitoring-datadir>/<cluster_name> ")
	flags.StringVar(&conf.WorkingDir, "monitoring-datadir", "/var/lib/replication-manager", "Path to write temporary and persistent files")
	flags.Int64Var(&conf.MonitoringTicker, "monitoring-ticker", 2, "Monitoring interval in seconds")

	//not working so far
	//flags.StringVar(&conf.TunnelHost, "monitoring-tunnel-host", "", "Bastion host to access to monitor topology via SSH tunnel host:22")
	//flags.StringVar(&conf.TunnelCredential, "monitoring-tunnel-credential", "root:", "Credential Access to bastion host topology via SSH tunnel")
	//flags.StringVar(&conf.TunnelKeyPath, "monitoring-tunnel-key-path", "/Users/apple/.ssh/id_rsa", "Tunnel private key path")
	flags.BoolVar(&conf.MonitorWriteHeartbeat, "monitoring-write-heartbeat", false, "Inject heartbeat into proxy or via external vip")
	flags.StringVar(&conf.MonitorWriteHeartbeatCredential, "monitoring-write-heartbeat-credential", "", "Database user:password to inject traffic into proxy or via external vip")
	flags.BoolVar(&conf.MonitorVariableDiff, "monitoring-variable-diff", true, "Monitor variable difference beetween nodes")
	flags.BoolVar(&conf.MonitorPFS, "monitoring-performance-schema", true, "Monitor performance schema")
	flags.BoolVar(&conf.MonitorInnoDBStatus, "monitoring-innodb-status", true, "Monitor innodb status")
	flags.StringVar(&conf.MonitorIgnoreError, "monitoring-ignore-errors", "", "Comma separated list of error or warning to ignore")
	flags.BoolVar(&conf.MonitorSchemaChange, "monitoring-schema-change", true, "Monitor schema change")
	flags.StringVar(&conf.MonitorSchemaChangeScript, "monitoring-schema-change-script", "", "Monitor schema change external script")
	flags.StringVar(&conf.MonitoringSSLCert, "monitoring-ssl-cert", "", "HTTPS & API TLS certificate")
	flags.StringVar(&conf.MonitoringSSLKey, "monitoring-ssl-key", "", "HTTPS & API TLS key")
	flags.StringVar(&conf.MonitoringKeyPath, "monitoring-key-path", "/etc/replication-manager/.replication-manager.key", "Encryption key file path")
	flags.BoolVar(&conf.MonitorQueries, "monitoring-queries", true, "Monitor long queries")
	flags.BoolVar(&conf.MonitorPlugins, "monitoring-plugins", true, "Monitor installed plugins")
	flags.IntVar(&conf.MonitorLongQueryTime, "monitoring-long-query-time", 10000, "Long query time in ms")
	flags.BoolVar(&conf.MonitorQueryRules, "monitoring-query-rules", true, "Monitor query routing from proxies")
	flags.StringVar(&conf.MonitorLongQueryScript, "monitoring-long-query-script", "", "long query time external script")
	flags.BoolVar(&conf.MonitorLongQueryWithTable, "monitoring-long-query-with-table", false, "Use log_type table to fetch slow queries")
	flags.BoolVar(&conf.MonitorLongQueryWithProcess, "monitoring-long-query-with-process", true, "Use processlist to fetch slow queries")
	flags.IntVar(&conf.MonitorLongQueryLogLength, "monitoring-long-query-log-length", 200, "Number of slow queries to keep in monitor")
	flags.IntVar(&conf.MonitorErrorLogLength, "monitoring-erreur-log-length", 20, "Number of error log line to keep in monitor")
	flags.BoolVar(&conf.MonitorScheduler, "monitoring-scheduler", false, "Enable internal scheduler")
	flags.BoolVar(&conf.MonitorCheckGrants, "monitoring-check-grants", true, "Check grants for replication and monitoring users, it use DNS Lookup")
	flags.BoolVar(&conf.MonitorPause, "monitoring-pause", false, "Disable monitoring")
	flags.BoolVar(&conf.MonitorProcessList, "monitoring-processlist", true, "Enable capture 50 longuest process via processlist")
	flags.StringVar(&conf.MonitorAddress, "monitoring-address", "localhost", "How to contact this monitoring")
	flags.StringVar(&conf.MonitorTenant, "monitoring-tenant", "default", "Can be use to store multi tenant identifier")
	flags.Int64Var(&conf.MonitorWaitRetry, "monitoring-wait-retry", 60, "Retry this number of time before giving up state transition <999999")
	flags.IntVar(&conf.MonitoringQueryTimeout, "monitoring-query-timeout", 2000, "Timeout for querying monitor in ms")
	flags.StringVar(&conf.MonitoringOpenStateScript, "monitoring-open-state-script", "", "Script trigger on open state")
	flags.StringVar(&conf.MonitoringCloseStateScript, "monitoring-close-state-script", "", "Script trigger on close state")
	flags.BoolVar(&conf.MonitorCapture, "monitoring-capture", true, "Enable capture on error for 5 monitor loops")
	flags.StringVar(&conf.MonitorCaptureTrigger, "monitoring-capture-trigger", "ERR00076,ERR00041", "List of errno triggering capture mode")
	flags.IntVar(&conf.MonitorCaptureFileKeep, "monitoring-capture-file-keep", 5, "Purge capture file keep that number of them")
	flags.StringVar(&conf.MonitoringAlertTrigger, "monitoring-alert-trigger", "ERR00027,ERR00042,ERR00087", "List of errno triggering an alert to be send")

	flags.BoolVar(&conf.LogSQLInMonitoring, "log-sql-in-monitoring", false, "Log SQL queries send to servers in monitoring")

	flags.BoolVar(&conf.LogHeartbeat, "log-heartbeat", false, "Log Heartbeat")
	flags.IntVar(&conf.LogHeartbeatLevel, "log-heartbeat-level", 1, "Log Heartbeat Level")

	flags.BoolVar(&conf.LogFailedElection, "log-failed-election", true, "Log failed election")
	flags.IntVar(&conf.LogFailedElectionLevel, "log-failed-election-level", 1, "Log failed election Level")

	flags.BoolVar(&conf.LogBinlogPurge, "log-binlog-purge", false, "Log Binlog Purge")
	flags.IntVar(&conf.LogBinlogPurgeLevel, "log-binlog-purge-level", 1, "Log Binlog Purge Level")

	flags.BoolVar(&conf.LogGraphite, "log-graphite", true, "Log Graphite")
	flags.IntVar(&conf.LogGraphiteLevel, "log-graphite-level", 2, "Log Graphite Level")

	// SST
	flags.IntVar(&conf.SSTSendBuffer, "sst-send-buffer", 16384, "SST send buffer size")
	flags.BoolVar(&conf.LogSST, "log-sst", true, "Log open and close SST transfert")
	flags.IntVar(&conf.LogSSTLevel, "log-sst-level", 1, "Log SST Level")

	// Backup Stream
	flags.BoolVar(&conf.LogBackupStream, "log-backup-stream", true, "To log backup stream process")
	flags.IntVar(&conf.LogBackupStreamLevel, "log-backup-stream-level", 2, "Log Backup Stream Level")

	// Log orchestrator
	flags.BoolVar(&conf.LogOrchestrator, "log-orchestrator", true, "To log orchestrator process")
	flags.IntVar(&conf.LogOrchestratorLevel, "log-orchestrator-level", 2, "Log orchestrator Level")

	// Log topology
	flags.BoolVar(&conf.LogTopology, "log-topology", true, "To log topology process")
	flags.IntVar(&conf.LogTopologyLevel, "log-topology-level", 2, "Log topology Level")

	// Log DB Jobs
	flags.BoolVar(&conf.LogTask, "log-task", true, "To log DB job process")
	flags.IntVar(&conf.LogTaskLevel, "log-task-level", 3, "Log Task Level")

	// DB Credentials
	flags.StringVar(&conf.User, "db-servers-credential", "root:mariadb", "Database login, specified in the [user]:[password] format")
	flags.StringVar(&conf.Hosts, "db-servers-hosts", "", "Database hosts list to monitor, IP and port (optional), specified in the host:[port] format and separated by commas")
	flags.BoolVar(&conf.DBServersTLSUseGeneratedCertificate, "db-servers-tls-use-generated-cert", false, "Use the auto generated certificates to connect to database backend")
	flags.StringVar(&conf.HostsTLSCA, "db-servers-tls-ca-cert", "", "Database TLS authority certificate")
	flags.StringVar(&conf.HostsTlsCliKey, "db-servers-tls-client-key", "", "Database TLS client key")
	flags.StringVar(&conf.HostsTlsCliCert, "db-servers-tls-client-cert", "", "Database TLS client certificate")
	flags.StringVar(&conf.HostsTlsSrvKey, "db-servers-tls-server-key", "", "Database TLS server key to push in config")
	flags.StringVar(&conf.HostsTlsSrvCert, "db-servers-tls-server-cert", "", "Database TLS server certificate to push in config")
	flags.IntVar(&conf.Timeout, "db-servers-connect-timeout", 5, "Database connection timeout in seconds")
	flags.IntVar(&conf.ReadTimeout, "db-servers-read-timeout", 3600, "Database read timeout in seconds")
	flags.StringVar(&conf.PrefMaster, "db-servers-prefered-master", "", "Database preferred candidate in election,  host:[port] format")
	flags.StringVar(&conf.IgnoreSrv, "db-servers-ignored-hosts", "", "Database list of hosts to ignore in election")
	flags.StringVar(&conf.IgnoreSrvRO, "db-servers-ignored-readonly", "", "Database list of hosts not changing read only status")
	flags.StringVar(&conf.BackupServers, "db-servers-backup-hosts", "", "Database list of hosts to backup when set can backup a slave")

	flags.Int64Var(&conf.SwitchWaitKill, "switchover-wait-kill", 5000, "Switchover wait this many milliseconds before killing threads on demoted master")
	flags.IntVar(&conf.SwitchWaitWrite, "switchover-wait-write-query", 10, "Switchover is canceled if a write query is running for this time")
	flags.Int64Var(&conf.SwitchWaitTrx, "switchover-wait-trx", 10, "Switchover is cancel after this timeout in second if can't aquire FTWRL")
	flags.BoolVar(&conf.SwitchSync, "switchover-at-sync", false, "Switchover Only  when state semisync is sync for last status")
	flags.BoolVar(&conf.SwitchGtidCheck, "switchover-at-equal-gtid", false, "Switchover only when slaves are fully in sync")
	flags.BoolVar(&conf.SwitchSlaveWaitCatch, "switchover-slave-wait-catch", true, "Switchover wait for slave to catch with replication, not needed in GTID mode but enable to detect possible issues like witing on old master")
	flags.BoolVar(&conf.SwitchDecreaseMaxConn, "switchover-decrease-max-conn", true, "Switchover decrease max connection on old master")
	flags.BoolVar(&conf.SwitchoverCopyOldLeaderGtid, "switchover-copy-old-leader-gtid", false, "Switchover copy old leader GTID")
	flags.Int64Var(&conf.SwitchDecreaseMaxConnValue, "switchover-decrease-max-conn-value", 10, "Switchover decrease max connection to this value different according to flavor")
	flags.IntVar(&conf.SwitchSlaveWaitRouteChange, "switchover-wait-route-change", 2, "Switchover wait for unmanged proxy monitor to dicoverd new state")
	flags.BoolVar(&conf.SwitchLowerRelease, "switchover-lower-release", false, "Allow switchover to lower release")

	flags.StringVar(&conf.MasterConn, "replication-source-name", "", "Replication channel name to use for multisource")
	flags.StringVar(&conf.ReplicationMultisourceHeadClusters, "replication-multisource-head-clusters", "", "Multi source link to parent cluster, autodiscoverd but can be materialized for bootstraping replication")
	flags.StringVar(&conf.HostsDelayed, "replication-delayed-hosts", "", "Database hosts list that need delayed replication separated by commas")
	flags.IntVar(&conf.HostsDelayedTime, "replication-delayed-time", 3600, "Delayed replication time")
	flags.IntVar(&conf.MasterConnectRetry, "replication-master-connect-retry", 10, "Replication is define using this connection retry timeout")
	flags.StringVar(&conf.RplUser, "replication-credential", "root:mariadb", "Replication user in the [user]:[password] format")
	flags.BoolVar(&conf.ReplicationSSL, "replication-use-ssl", false, "Replication use SSL encryption to replicate from master")
	flags.BoolVar(&conf.ActivePassive, "replication-active-passive", false, "Active Passive topology")
	flags.BoolVar(&conf.MultiMaster, "replication-multi-master", false, "Multi-master topology")
	flags.BoolVar(&conf.MultiMasterGrouprep, "replication-multi-master-grouprep", false, "Enable mysql group replication multi-master")
	flags.IntVar(&conf.MultiMasterGrouprepPort, "replication-multi-master-grouprep-port", 33061, "Group replication network port")
	flags.BoolVar(&conf.MultiMasterWsrep, "replication-multi-master-wsrep", false, "Enable Galera wsrep multi-master")
	flags.StringVar(&conf.MultiMasterWsrepSSTMethod, "replication-multi-master-wsrep-sst-method", "mariabackup", "mariabackup|xtrabackup-v2|rsync|mysqldump")
	flags.IntVar(&conf.MultiMasterWsrepPort, "replication-multi-master-wsrep-port", 4567, "wsrep network port")
	flags.StringVar(&conf.TopologyTarget, "topology-target", "master-slave", "Target topology for current cluster. Default 'master-slave'")
	flags.BoolVar(&conf.DynamicTopology, "replication-dynamic-topology", true, "Auto discover topology when changed") //Set to true to keep same behavior
	flags.BoolVar(&conf.MultiMasterRing, "replication-multi-master-ring", false, "Multi-master ring topology")
	flags.BoolVar(&conf.MultiMasterRingUnsafe, "replication-multi-master-ring-unsafe", true, "Allow multi-master ring topology without log slave updates") //Set to true to keep same behavior
	flags.BoolVar(&conf.MultiTierSlave, "replication-multi-tier-slave", false, "Relay slaves topology")
	flags.BoolVar(&conf.MasterSlavePgStream, "replication-master-slave-pg-stream", false, "Postgres streaming replication")
	flags.BoolVar(&conf.MasterSlavePgLogical, "replication-master-slave-pg-locgical", false, "Postgres logical replication")
	flags.BoolVar(&conf.ReplicationNoRelay, "replication-master-slave-never-relay", true, "Do not allow relay server MSS MXS XXM RSM")
	flags.StringVar(&conf.ReplicationErrorScript, "replication-error-script", "", "Replication error script")
	flags.StringVar(&conf.ReplicationRestartOnSQLErrorMatch, "replication-restart-on-sqlerror-match", "", "Auto restart replication on SQL Error regexep")

	flags.StringVar(&conf.PreScript, "failover-pre-script", "", "Path of pre-failover script")
	flags.StringVar(&conf.PostScript, "failover-post-script", "", "Path of post-failover script")
	flags.BoolVar(&conf.ReadOnly, "failover-readonly-state", true, "Failover Switchover set slaves as read-only")
	flags.BoolVar(&conf.FailoverSemiSyncState, "failover-semisync-state", false, "Failover Switchover set semisync slave master state")
	flags.BoolVar(&conf.SuperReadOnly, "failover-superreadonly-state", false, "Failover Switchover set slaves as super-read-only")
	flags.StringVar(&conf.FailMode, "failover-mode", "manual", "Failover is manual or automatic")
	flags.Int64Var(&conf.FailMaxDelay, "failover-max-slave-delay", 30, "Election ignore slave with replication delay over this time in sec")
	flags.BoolVar(&conf.FailRestartUnsafe, "failover-restart-unsafe", false, "Failover when cluster down if a slave is start first ")
	flags.IntVar(&conf.FailLimit, "failover-limit", 5, "Failover is canceld if already failover this number of time (0: unlimited)")
	flags.Int64Var(&conf.FailTime, "failover-time-limit", 0, "Failover is canceled if timer in sec is not passed with previous failover (0: do not wait)")
	flags.BoolVar(&conf.FailSync, "failover-at-sync", false, "Failover only when state semisync is sync for last status")
	flags.BoolVar(&conf.FailEventScheduler, "failover-event-scheduler", false, "Failover event scheduler")
	flags.BoolVar(&conf.FailoverSwitchToPrefered, "failover-switch-to-prefered", false, "Failover always pick most up to date slave following it with switchover to prefered leader")
	flags.BoolVar(&conf.FailEventStatus, "failover-event-status", false, "Failover event status ENABLE OR DISABLE ON SLAVE")
	flags.BoolVar(&conf.CheckFalsePositiveHeartbeat, "failover-falsepositive-heartbeat", true, "Failover checks that slaves do not receive heartbeat")
	flags.IntVar(&conf.CheckFalsePositiveHeartbeatTimeout, "failover-falsepositive-heartbeat-timeout", 3, "Failover checks that slaves do not receive heartbeat detection timeout ")
	flags.BoolVar(&conf.CheckFalsePositiveExternal, "failover-falsepositive-external", false, "Failover checks that http//master:80 does not reponse 200 OK header")
	flags.IntVar(&conf.CheckFalsePositiveExternalPort, "failover-falsepositive-external-port", 80, "Failover checks external port")
	flags.IntVar(&conf.MaxFail, "failover-falsepositive-ping-counter", 5, "Failover after this number of ping failures (interval 1s)")
	flags.IntVar(&conf.FailoverLogFileKeep, "failover-log-file-keep", 5, "Purge log files taken during failover")
	flags.BoolVar(&conf.FailoverCheckDelayStat, "failover-check-delay-stat", false, "Use delay avg statistic for failover decision")
	flags.BoolVar(&conf.DelayStatCapture, "delay-stat-capture", false, "Capture hourly statistic for delay average")
	flags.BoolVar(&conf.PrintDelayStat, "print-delay-stat", false, "Print captured delay statistic")
	flags.BoolVar(&conf.PrintDelayStatHistory, "print-delay-stat-history", false, "Print captured delay statistic history")
	flags.IntVar(&conf.PrintDelayStatInterval, "print-delay-stat-interval", 1, "Interval for printing delay stat (in minutes)")
	flags.IntVar(&conf.DelayStatRotate, "delay-stat-rotate", 72, "Number of hours before rotating the delay stat")

	flags.BoolVar(&conf.Autoseed, "autoseed", false, "Automatic join a standalone node")
	flags.BoolVar(&conf.Autorejoin, "autorejoin", true, "Automatic rejoin a failed master")
	flags.BoolVar(&conf.AutorejoinBackupBinlog, "autorejoin-backup-binlog", true, "backup ahead binlogs events when old master rejoin")
	flags.StringVar(&conf.RejoinScript, "autorejoin-script", "", "Path of failed leader rejoin script")
	flags.BoolVar(&conf.AutorejoinSemisync, "autorejoin-flashback-on-sync", true, "Automatic rejoin failed leader via flashback if semisync SYNC ")
	flags.BoolVar(&conf.AutorejoinNoSemisync, "autorejoin-flashback-on-unsync", false, "Automatic rejoin failed leader flashback if semisync NOT SYNC ")
	flags.BoolVar(&conf.AutorejoinFlashback, "autorejoin-flashback", false, "Automatic rejoin ahead failed leader via binlog flashback")
	flags.BoolVar(&conf.AutorejoinZFSFlashback, "autorejoin-zfs-flashback", false, "Automatic rejoin ahead failed leader via previous ZFS snapshot")
	flags.BoolVar(&conf.AutorejoinMysqldump, "autorejoin-mysqldump", false, "Automatic rejoin ahead failed leader via direct current master dump")
	flags.BoolVar(&conf.AutorejoinPhysicalBackup, "autorejoin-physical-backup", false, "Automatic rejoin ahead failed leader via reseed previous phyiscal backup")
	flags.BoolVar(&conf.AutorejoinLogicalBackup, "autorejoin-logical-backup", false, "Automatic rejoin ahead failed leader via reseed previous logical backup")
	flags.BoolVar(&conf.AutorejoinSlavePositionalHeartbeat, "autorejoin-slave-positional-heartbeat", false, "Automatic rejoin extra slaves via pseudo gtid heartbeat for positional replication")
	flags.BoolVar(&conf.AutorejoinForceRestore, "autorejoin-force-restore", false, "Automatic rejoin ahead force full new leader backup restore")

	flags.StringVar(&conf.AlertScript, "alert-script", "", "Path for alerting script server status change")
	flags.StringVar(&conf.SlackURL, "alert-slack-url", "", "Slack webhook URL to alert")
	flags.StringVar(&conf.SlackChannel, "alert-slack-channel", "#support", "Slack channel to alert")
	flags.StringVar(&conf.SlackUser, "alert-slack-user", "", "Slack user for alert")

	flags.StringVar(&conf.PushoverAppToken, "alert-pushover-app-token", "", "Pushover App Token for alerts")
	flags.StringVar(&conf.PushoverUserToken, "alert-pushover-user-token", "", "Pushover User Token for alerts")

	flags.StringVar(&conf.ProvOpensvcP12Secret, "opensvc-p12-secret", "", "OpenSVC Secret")

	flags.StringVar(&conf.TeamsUrl, "alert-teams-url", "", "Teams url channel for alerts")
	flags.StringVar(&conf.TeamsProxyUrl, "alert-teams-proxy-url", "", "Proxy url for Teams Webhook")
	flags.StringVar(&conf.TeamsAlertState, "alert-teams-state", "", "State Code for Teams Alert : ERR|WARN|INFO")

	conf.CheckType = "tcp"
	flags.BoolVar(&conf.CheckReplFilter, "check-replication-filters", true, "Check that possible master have equal replication filters")
	flags.BoolVar(&conf.CheckBinFilter, "check-binlog-filters", true, "Check that possible master have equal binlog filters")
	flags.BoolVar(&conf.CheckGrants, "check-grants", true, "Check that possible master have equal grants")
	flags.BoolVar(&conf.RplChecks, "check-replication-state", true, "Check replication status when electing master server")
	flags.BoolVar(&conf.RplCheckErrantTrx, "check-replication-errant-trx", true, "Check replication have no errant transaction in MySQL GTID")
	flags.IntVar(&conf.CheckBinServerId, "check-binlog-server-id", 10000, "Server ID for checking binlogs timestamps")

	flags.StringVar(&conf.APIPort, "api-port", "10005", "Rest API listen port")
	flags.StringVar(&conf.APIUsers, "api-credentials", "admin:repman", "Rest API user list user:password,..")
	flags.StringVar(&conf.APIUsersExternal, "api-credentials-external", "", "Rest API user list user:password,.. as dba:repman,foo:bar")
	flags.StringVar(&conf.APIUsersACLAllow, "api-credentials-acl-allow", "admin:cluster proxy db prov,dba:cluster proxy db,foo:", "User acl allow")
	flags.StringVar(&conf.APIUsersACLDiscard, "api-credentials-acl-discard", "", "User acl discard")
	flags.StringVar(&conf.APIBind, "api-bind", "0.0.0.0", "Rest API bind ip")
	flags.BoolVar(&conf.APIHttpsBind, "api-https-bind", false, "Bind API call to https Web UI will error with http")
	flags.BoolVar(&conf.APISecureConfig, "api-credentials-secure-config", false, "Need JWT token to download config tar.gz")
	flags.StringVar(&conf.APIPublicURL, "api-public-url", "https://127.0.0.1:10005", "Public address of monitoring API Used for cloud18 OAuth callback")
	flags.StringVar(&conf.OAuthProvider, "api-oauth-provider-url", "https://gitlab.signal18.io", "API OAuth Provider URL")
	flags.StringVar(&conf.OAuthClientID, "api-oauth-client-id", "", "API OAuth Client ID")
	flags.StringVar(&conf.OAuthClientSecret, "api-oauth-client-secret", "", "API OAuth Client Secret")

	//vault
	flags.StringVar(&conf.VaultServerAddr, "vault-server-addr", "", "Vault server address")
	flags.StringVar(&conf.VaultRoleId, "vault-role-id", "", "Vault role id")
	flags.StringVar(&conf.VaultSecretId, "vault-secret-id", "", "Vault secret id")
	flags.StringVar(&conf.VaultMode, "vault-mode", cluster.VaultConfigStoreV2, "Vault mode : config_store_v2|database_engine")
	flags.StringVar(&conf.VaultMount, "vault-mount", "kv", "Vault mount for the secret")
	flags.StringVar(&conf.VaultAuth, "vault-auth", "approle", "Vault auth method : approle|userpass|ldap|token|github|alicloud|aws|azure|gcp|kerberos|kubernetes|radius")
	flags.StringVar(&conf.VaultToken, "vault-token", "", "Vault Token")
	flags.BoolVar(&conf.LogVault, "log-vault", true, "Log vault debug")
	flags.IntVar(&conf.LogVaultLevel, "log-vault-level", 1, "Log level for vault")

	flags.StringVar(&conf.GitUrl, "git-url", "", "GitHub URL repository to store config file")
	flags.StringVar(&conf.GitUsername, "git-username", "", "GitHub username")
	flags.StringVar(&conf.GitAccesToken, "git-acces-token", "", "GitHub personnal acces token")
	flags.IntVar(&conf.GitMonitoringTicker, "git-monitoring-ticker", 60, "Git monitoring interval in seconds")
	flags.BoolVar(&conf.LogGit, "log-git", true, "To log clone/push/pull from git")
	flags.IntVar(&conf.LogGitLevel, "log-git-level", 1, "Log GIT Level")

	//flags.BoolVar(&conf.Daemon, "daemon", true, "Daemon mode. Do not start the Termbox console")
	conf.Daemon = true
	flags.IntVar(&conf.CacheStaticMaxAge, "cache-static-max-age", 18000, "Cache Max Age Duration for static files")

	if WithEnforce == "ON" {
		flags.BoolVar(&conf.ForceSlaveReadOnly, "force-slave-readonly", true, "Automatically activate read only on slave")
		flags.BoolVar(&conf.ForceSlaveHeartbeat, "force-slave-heartbeat", false, "Automatically activate heartbeat on slave")
		flags.IntVar(&conf.ForceSlaveHeartbeatRetry, "force-slave-heartbeat-retry", 5, "Replication heartbeat retry on slave")
		flags.IntVar(&conf.ForceSlaveHeartbeatTime, "force-slave-heartbeat-time", 3, "Replication heartbeat time")
		flags.BoolVar(&conf.ForceSlaveGtid, "force-slave-gtid-mode", false, "Automatically activate gtid mode on slave")
		flags.BoolVar(&conf.ForceSlaveGtidStrict, "force-slave-gtid-mode-strict", false, "Automatically activate GTID strict mode")
		flags.BoolVar(&conf.ForceSlaveNoGtid, "force-slave-no-gtid-mode", false, "Automatically activate no gtid mode on slave")
		flags.BoolVar(&conf.ForceSlaveSemisync, "force-slave-semisync", false, "Automatically activate semisync on slave")
		flags.BoolVar(&conf.ForceBinlogRow, "force-binlog-row", false, "Automatically activate binlog row format on master")
		flags.BoolVar(&conf.ForceBinlogAnnotate, "force-binlog-annotate", false, "Automatically activate annotate event")
		flags.BoolVar(&conf.ForceBinlogSlowqueries, "force-binlog-slowqueries", false, "Automatically activate long replication statement in slow log")
		flags.BoolVar(&conf.ForceBinlogChecksum, "force-binlog-checksum", false, "Automatically force  binlog checksum")
		flags.BoolVar(&conf.ForceBinlogCompress, "force-binlog-compress", false, "Automatically force binlog compression")
		flags.BoolVar(&conf.ForceBinlogPurge, "force-binlog-purge", false, "Automatically force binlog purge")
		flags.BoolVar(&conf.ForceBinlogPurgeReplicas, "force-binlog-purge-replicas", false, "Automatically force binlog purge replicas based on oldest master binlog when master purged")
		flags.BoolVar(&conf.ForceBinlogPurgeOnRestore, "force-binlog-purge-on-restore", false, "Automatically force binlog purge on restore")
		flags.IntVar(&conf.ForceBinlogPurgeTotalSize, "force-binlog-purge-total-size", 30, "Automatically force binlog purge more than total size")
		flags.IntVar(&conf.ForceBinlogPurgeMinReplica, "force-binlog-purge-min-replica", 1, "Minimum of replica(s) needed for purging binary log")
		flags.BoolVar(&conf.ForceDiskRelayLogSizeLimit, "force-disk-relaylog-size-limit", false, "Automatically limit the size of relay log on disk ")
		flags.Uint64Var(&conf.ForceDiskRelayLogSizeLimitSize, "force-disk-relaylog-size-limit-size", 1000000000, "Automatically limit the size of relay log on disk to 1G")
		flags.BoolVar(&conf.ForceInmemoryBinlogCacheSize, "force-inmemory-binlog-cache-size", false, "Automatically adapt binlog cache size based on monitoring")
		flags.BoolVar(&conf.ForceSlaveStrict, "force-slave-strict", false, "Slave mode to error when replica diverge")
		flags.BoolVar(&conf.ForceSlaveIdempotent, "force-slave-idempotent", false, "Slave mode to repair when replica diverge using full master row event")
		flags.StringVar(&conf.ForceSlaveParallelMode, "force-slave-parallel-mode", "", "serialized|minimal|conservative|optimistic|aggressive| empty for no enforcement")
		flags.BoolVar(&conf.ForceSyncBinlog, "force-sync-binlog", false, "Automatically force master crash safe")
		flags.BoolVar(&conf.ForceSyncInnoDB, "force-sync-innodb", false, "Automatically force master innodb crash safe")
		flags.BoolVar(&conf.ForceNoslaveBehind, "force-noslave-behind", false, "Automatically force no slave behing")
	}

	flags.BoolVar(&conf.HttpServ, "http-server", true, "Start the HTTP server")
	flags.BoolVar(&conf.ApiServ, "api-server", true, "Start the API HTTPS server")

	flags.StringVar(&conf.BindAddr, "http-bind-address", "localhost", "Bind HTTP monitor to this IP address")
	flags.StringVar(&conf.HttpPort, "http-port", "10001", "HTTP monitor to listen on this port")
	if runtime.GOOS == "darwin" {
		flags.StringVar(&conf.HttpRoot, "http-root", "/opt/replication-manager/share/dashboard", "Path to HTTP replication-monitor files")
	} else {
		flags.StringVar(&conf.HttpRoot, "http-root", "/usr/share/replication-manager/dashboard", "Path to HTTP replication-monitor files")
	}
	flags.IntVar(&conf.HttpRefreshInterval, "http-refresh-interval", 4000, "Http refresh interval in ms")
	flags.IntVar(&conf.SessionLifeTime, "http-session-lifetime", 3600, "Http Session life time ")

	if WithMail == "ON" {
		flags.StringVar(&conf.MailFrom, "mail-from", "mrm@localhost", "Alert email sender")
		flags.StringVar(&conf.MailTo, "mail-to", "", "Alert email recipients, separated by commas")
		flags.StringVar(&conf.MailSMTPAddr, "mail-smtp-addr", "localhost:25", "Alert email SMTP server address, in host:[port] format")
		flags.StringVar(&conf.MailSMTPUser, "mail-smtp-user", "", "SMTP user")
		flags.StringVar(&conf.MailSMTPPassword, "mail-smtp-password", "", "SMTP password")
		flags.BoolVar(&conf.MailSMTPTLSSkipVerify, "mail-smtp-tls-skip-verify", false, "Use TLS with skip verify")
	}

	flags.BoolVar(&conf.PRXServersReadOnMaster, "proxy-servers-read-on-master", false, "Should RO route via proxies point to master")
	flags.BoolVar(&conf.PRXServersReadOnMasterNoSlave, "proxy-servers-read-on-master-no-slave", true, "Should RO route via proxies point to master when no more replicats")
	flags.BoolVar(&conf.PRXServersBackendCompression, "proxy-servers-backend-compression", false, "Proxy communicate with backends with compression")
	flags.IntVar(&conf.PRXServersBackendMaxReplicationLag, "proxy-servers-backend-max-replication-lag", 30, "Max lag to send query to read  backends ")
	flags.IntVar(&conf.PRXServersBackendMaxConnections, "proxy-servers-backend-max-connections", 1000, "Max connections on backends ")

	externalprx := new(cluster.ExternalProxy)
	externalprx.AddFlags(flags, conf)

	if WithMaxscale == "ON" {
		maxscaleprx := new(cluster.MaxscaleProxy)
		maxscaleprx.AddFlags(flags, conf)
	}

	proxyjanitorprx := new(cluster.ProxyJanitor)
	proxyjanitorprx.AddFlags(flags, conf)

	// TODO: this seems dead code / unimplemented
	// start
	if WithMySQLRouter == "ON" {
		flags.BoolVar(&conf.MysqlRouterOn, "mysqlrouter", false, "MySQLRouter proxy server is query for backend status")
		flags.BoolVar(&conf.MysqlRouterDebug, "mysqlrouter-debug", true, "MySQLRouter log debug")
		flags.IntVar(&conf.MysqlRouterLogLevel, "mysqlrouter-log-level", 1, "MySQLRouter log debug level")
		flags.StringVar(&conf.MysqlRouterHosts, "mysqlrouter-servers", "127.0.0.1", "MaxScale hosts ")
		flags.StringVar(&conf.MysqlRouterPort, "mysqlrouter-port", "6603", "MySQLRouter admin port")
		flags.StringVar(&conf.MysqlRouterUser, "mysqlrouter-user", "admin", "MySQLRouter admin user")
		flags.StringVar(&conf.MysqlRouterPass, "mysqlrouter-pass", "mariadb", "MySQLRouter admin password")
		flags.IntVar(&conf.MysqlRouterWritePort, "mysqlrouter-write-port", 3306, "MySQLRouter read-write port to leader")
		flags.IntVar(&conf.MysqlRouterReadPort, "mysqlrouter-read-port", 3307, "MySQLRouter load balance read port to all nodes")
		flags.IntVar(&conf.MysqlRouterReadWritePort, "mysqlrouter-read-write-port", 3308, "MySQLRouter load balance read port to all nodes")
	}
	// end of dead code

	if WithMariadbshardproxy == "ON" {
		mdbsprx := new(cluster.MariadbShardProxy)
		mdbsprx.AddFlags(flags, conf)
	}
	if WithHaproxy == "ON" {
		haprx := new(cluster.HaproxyProxy)
		haprx.AddFlags(flags, conf)
	}
	if WithProxysql == "ON" {
		proxysqlprx := new(cluster.ProxySQLProxy)
		proxysqlprx.AddFlags(flags, conf)
	}
	if WithSphinx == "ON" {
		sphinxprx := new(cluster.SphinxProxy)
		sphinxprx.AddFlags(flags, conf)
	}

	myproxyprx := new(cluster.MyProxyProxy)
	myproxyprx.AddFlags(flags, conf)

	consulprx := new(cluster.ConsulProxy)
	consulprx.AddFlags(flags, conf)

	if WithSpider == "ON" {
		flags.BoolVar(&conf.Spider, "spider", false, "Turn on spider detection")
	}

	if WithMonitoring == "ON" {
		flags.IntVar(&conf.GraphiteCarbonPort, "graphite-carbon-port", 2003, "Graphite Carbon Metrics TCP & UDP port")
		flags.IntVar(&conf.GraphiteCarbonApiPort, "graphite-carbon-api-port", 10002, "Graphite Carbon API port")
		flags.IntVar(&conf.GraphiteCarbonServerPort, "graphite-carbon-server-port", 10003, "Graphite Carbon HTTP port")
		flags.IntVar(&conf.GraphiteCarbonLinkPort, "graphite-carbon-link-port", 7002, "Graphite Carbon Link port")
		flags.IntVar(&conf.GraphiteCarbonPicklePort, "graphite-carbon-pickle-port", 2004, "Graphite Carbon Pickle port")
		flags.IntVar(&conf.GraphiteCarbonPprofPort, "graphite-carbon-pprof-port", 7007, "Graphite Carbon Pickle port")
		flags.StringVar(&conf.GraphiteCarbonHost, "graphite-carbon-host", "127.0.0.1", "Graphite monitoring host")
		flags.BoolVar(&conf.GraphiteMetrics, "graphite-metrics", false, "Enable Graphite monitoring")
		flags.BoolVar(&conf.GraphiteEmbedded, "graphite-embedded", false, "Enable Internal Graphite Carbon Server")
		flags.BoolVar(&conf.GraphiteWhitelist, "graphite-whitelist", true, "Enable Whitelist")
		flags.BoolVar(&conf.GraphiteBlacklist, "graphite-blacklist", false, "Enable Blacklist")
		flags.StringVar(&conf.GraphiteWhitelistTemplate, "graphite-whitelist-template", "minimal", "Graphite default template for whitelist (none | minimal | grafana | all)")
	}
	//	flags.BoolVar(&conf.Heartbeat, "heartbeat-table", false, "Heartbeat for active/passive or multi mrm setup")
	if WithArbitrationClient == "ON" {
		flags.BoolVar(&conf.Arbitration, "arbitration-external", false, "Multi moninitor sas arbitration")
		flags.StringVar(&conf.ArbitrationSasSecret, "arbitration-external-secret", "", "Secret for arbitration")
		flags.StringVar(&conf.ArbitrationSasHosts, "arbitration-external-hosts", "88.191.151.84:80", "Arbitrator address")
		flags.IntVar(&conf.ArbitrationSasUniqueId, "arbitration-external-unique-id", 0, "Unique replication-manager instance idententifier")
		flags.StringVar(&conf.ArbitrationPeerHosts, "arbitration-peer-hosts", "127.0.0.1:10001", "Peer replication-manager hosts http port")
		flags.StringVar(&conf.DBServersLocality, "db-servers-locality", "127.0.0.1", "List database servers that are in same network locality")
		flags.StringVar(&conf.ArbitrationFailedMasterScript, "arbitration-failed-master-script", "", "External script when a master lost arbitration during split brain")
		flags.IntVar(&conf.ArbitrationReadTimout, "arbitration-read-timeout", 800, "Read timeout for arbotration response in millisec don't woveload monitoring ticker in second")
	}

	flags.StringVar(&conf.SchedulerReceiverPorts, "scheduler-db-servers-receiver-ports", "4444", "Scheduler TCP port to send data to db node, if list port affection is modulo db nodes")
	flags.StringVar(&conf.SchedulerSenderPorts, "scheduler-db-servers-sender-ports", "", "Scheduler TCP port to receive data from db node, consume one port per transfert if not set, pick one available port")
	flags.BoolVar(&conf.SchedulerReceiverUseSSL, "scheduler-db-servers-receiver-use-ssl", false, "Listner to send data to db node is use SSL")
	flags.BoolVar(&conf.SchedulerBackupLogical, "scheduler-db-servers-logical-backup", true, "Schedule logical backup")
	flags.BoolVar(&conf.SchedulerBackupPhysical, "scheduler-db-servers-physical-backup", false, "Schedule logical backup")
	flags.BoolVar(&conf.SchedulerDatabaseLogs, "scheduler-db-servers-logs", false, "Schedule database logs fetching")
	flags.BoolVar(&conf.SchedulerDatabaseOptimize, "scheduler-db-servers-optimize", true, "Schedule database optimize")
	flags.BoolVar(&conf.SchedulerDatabaseAnalyze, "scheduler-db-servers-analyze", true, "Schedule database analyze")

	flags.StringVar(&conf.BackupLogicalCron, "scheduler-db-servers-logical-backup-cron", "0 0 1 * * 6", "Logical backup cron expression represents a set of times, using 6 space-separated fields.")
	flags.StringVar(&conf.BackupPhysicalCron, "scheduler-db-servers-physical-backup-cron", "0 0 0 * * 0-4", "Physical backup cron expression represents a set of times, using 6 space-separated fields.")
	flags.StringVar(&conf.BackupDatabaseOptimizeCron, "scheduler-db-servers-optimize-cron", "0 0 3 1 * 5", "Optimize cron expression represents a set of times, using 6 space-separated fields.")
	flags.StringVar(&conf.BackupDatabaseAnalyzeCron, "scheduler-db-servers-analyze-cron", "0 0 4 2 * *", "Analyze cron expression represents a set of times, using 6 space-separated fields.")
	flags.StringVar(&conf.BackupDatabaseLogCron, "scheduler-db-servers-logs-cron", "0 0/10 * * * *", "Logs backup cron expression represents a set of times, using 6 space-separated fields.")
	flags.BoolVar(&conf.SchedulerDatabaseLogsTableRotate, "scheduler-db-servers-logs-table-rotate", true, "Schedule rotate database system table logs")
	flags.StringVar(&conf.SchedulerDatabaseLogsTableRotateCron, "scheduler-db-servers-logs-table-rotate-cron", "0 0 0/6 * * *", "Logs table rotate cron expression represents a set of times, using 6 space-separated fields.")
	flags.IntVar(&conf.SchedulerMaintenanceDatabaseLogsTableKeep, "scheduler-db-servers-logs-table-keep", 12, "Keep this number of system table logs")
	flags.StringVar(&conf.SchedulerSLARotateCron, "scheduler-sla-rotate-cron", "0 0 0 1 * *", "SLA rotate cron expression represents a set of times, using 6 space-separated fields.")
	flags.BoolVar(&conf.SchedulerRollingRestart, "scheduler-rolling-restart", false, "Schedule rolling restart")
	flags.StringVar(&conf.SchedulerRollingRestartCron, "scheduler-rolling-restart-cron", "0 30 11 * * *", "Rolling restart cron expression represents a set of times, using 6 space-separated fields.")
	flags.BoolVar(&conf.SchedulerRollingReprov, "scheduler-rolling-reprov", false, "Schedule rolling reprov")
	flags.StringVar(&conf.SchedulerRollingReprovCron, "scheduler-rolling-reprov-cron", "0 30 10 * * 5", "Rolling reprov cron expression represents a set of times, using 6 space-separated fields.")
	flags.BoolVar(&conf.SchedulerJobsSSH, "scheduler-jobs-ssh", false, "Schedule remote execution of dbjobs via ssh ")
	flags.StringVar(&conf.SchedulerJobsSSHCron, "scheduler-jobs-ssh-cron", "0 * * * * *", "Remote execution of dbjobs via ssh ")
	flags.BoolVar(&conf.SchedulerAlertDisable, "scheduler-alert-disable", false, "Schedule to disable alerting")
	flags.StringVar(&conf.SchedulerAlertDisableCron, "scheduler-alert-disable-cron", "0 0 0 * * 0-4", "Disabling alert cron expression represents a set of times, using 6 space-separated fields.")
	flags.IntVar(&conf.SchedulerAlertDisableTime, "scheduler-alert-disable-time", 3600, "Time in seconds to disable alerting")

	flags.BoolVar(&conf.Backup, "backup", false, "Turn on Backup")
	flags.BoolVar(&conf.BackupLockDDL, "backup-lockddl", true, "Use mariadb backup stage")
	flags.IntVar(&conf.BackupLogicalLoadThreads, "backup-logical-load-threads", 2, "Number of threads to load database")
	flags.IntVar(&conf.BackupLogicalDumpThreads, "backup-logical-dump-threads", 2, "Number of threads to dump database")
	flags.BoolVar(&conf.BackupLogicalDumpSystemTables, "backup-logical-dump-system-tables", false, "Backup restore the mysql database")
	flags.StringVar(&conf.BackupLogicalType, "backup-logical-type", "mysqldump", "type of logical backup: river|mysqldump|mydumper")
	flags.StringVar(&conf.BackupPhysicalType, "backup-physical-type", "xtrabackup", "type of physical backup: xtrabackup|mariabackup")
	flags.BoolVar(&conf.BackupRestic, "backup-restic", false, "Use restic to archive and restore backups")
	flags.StringVar(&conf.BackupResticBinaryPath, "backup-restic-binary-path", "/usr/bin/restic", "Path to restic binary")
	flags.StringVar(&conf.BackupResticAwsAccessKeyId, "backup-restic-aws-access-key-id", "admin", "Restic backup AWS key id")
	flags.StringVar(&conf.BackupResticAwsAccessSecret, "backup-restic-aws-access-secret", "secret", "Restic backup AWS key sercret")
	flags.StringVar(&conf.BackupResticRepository, "backup-restic-repository", "s3:https://s3.signal18.io/backups", "Restic backend repository")
	flags.StringVar(&conf.BackupResticPassword, "backup-restic-password", "secret", "Restic backend password")
	flags.BoolVar(&conf.BackupResticAws, "backup-restic-aws", false, "Restic will archive to s3 or to datadir/backups/archive")
	flags.BoolVar(&conf.BackupStreaming, "backup-streaming", false, "Backup streaming to cloud ")
	flags.BoolVar(&conf.BackupStreamingDebug, "backup-streaming-debug", false, "Debug mode for streaming to cloud ")
	flags.StringVar(&conf.BackupStreamingAwsAccessKeyId, "backup-streaming-aws-access-key-id", "admin", "Backup AWS key id")
	flags.StringVar(&conf.BackupStreamingAwsAccessSecret, "backup-streaming-aws-access-secret", "secret", "Backup AWS key secret")
	flags.StringVar(&conf.BackupStreamingEndpoint, "backup-streaming-endpoint", "https://s3.signal18.io/", "Backup AWS endpoint")
	flags.StringVar(&conf.BackupStreamingRegion, "backup-streaming-region", "fr-1", "Backup AWS region")
	flags.StringVar(&conf.BackupStreamingBucket, "backup-streaming-bucket", "repman", "Backup AWS bucket")

	//flags.StringVar(&conf.BackupResticStoragePolicy, "backup-restic-storage-policy", "--prune --keep-last 10 --keep-hourly 24 --keep-daily 7 --keep-weekly 52 --keep-monthly 120 --keep-yearly 102", "Restic keep backup policy")
	flags.IntVar(&conf.BackupKeepHourly, "backup-keep-hourly", 1, "Keep this number of hourly backup")
	flags.IntVar(&conf.BackupKeepDaily, "backup-keep-daily", 1, "Keep this number of daily backup")
	flags.IntVar(&conf.BackupKeepWeekly, "backup-keep-weekly", 4, "Keep this number of weekly backup")
	flags.IntVar(&conf.BackupKeepMonthly, "backup-keep-monthly", 12, "Keep this number of monthly backup")
	flags.IntVar(&conf.BackupKeepYearly, "backup-keep-yearly", 2, "Keep this number of yearly backup")

	flags.StringVar(&conf.BackupSaveScript, "backup-save-script", "", "Customized backup save script")
	flags.StringVar(&conf.BackupLoadScript, "backup-load-script", "", "Customized backup load script")
	flags.BoolVar(&conf.CompressBackups, "compress-backups", false, "To compress backups")

	flags.StringVar(&conf.BackupMyDumperPath, "backup-mydumper-path", "/usr/bin/mydumper", "Path to mydumper binary")
	flags.StringVar(&conf.BackupMyLoaderPath, "backup-myloader-path", "/usr/bin/myloader", "Path to myloader binary")
	flags.StringVar(&conf.BackupMyLoaderOptions, "backup-myloader-options", "--overwrite-tables --verbose=3", "Extra options")
	flags.StringVar(&conf.BackupMyDumperOptions, "backup-mydumper-options", "--chunk-filesize=1000 --compress --less-locking --verbose=3 --triggers --routines --events --trx-consistency-only --kill-long-queries", "Extra options")
	flags.StringVar(&conf.BackupMysqldumpPath, "backup-mysqldump-path", "", "Path to mysqldump binary")
	flags.StringVar(&conf.BackupMysqldumpOptions, "backup-mysqldump-options", "--hex-blob --single-transaction --verbose --all-databases --routines=true --triggers=true --system=all", "Extra options")
	flags.StringVar(&conf.BackupMysqlbinlogPath, "backup-mysqlbinlog-path", "", "Path to mysqlbinlog binary")
	flags.StringVar(&conf.BackupMysqlclientPath, "backup-mysqlclient-path", "", "Path to mysql client binary")

	flags.BoolVar(&conf.BackupBinlogs, "backup-binlogs", false, "Archive binlogs")
	flags.IntVar(&conf.BackupBinlogsKeep, "backup-binlogs-keep", 10, "Number of master binlog to keep")

	//Using mysqlbinlog for PRO since it's using opensvc and k8s
	if WithProvisioning == "ON" {
		flags.StringVar(&conf.BinlogCopyMode, "binlog-copy-mode", "mysqlbinlog", "Method for backing up binlogs: mysqlbinlog|ssh|gomysql|script (old value 'client' will be treated same as 'mysqlbinlog')")
	} else {
		flags.StringVar(&conf.BinlogCopyMode, "binlog-copy-mode", "ssh", "Method for backing up binlogs: mysqlbinlog|ssh|gomysql|script (old value 'client' will be treated same as 'mysqlbinlog')")
	}
	flags.StringVar(&conf.BinlogCopyScript, "binlog-copy-script", "", "Script filename for backing up binlogs")

	flags.StringVar(&conf.BinlogRotationScript, "binlog-rotation-script", "", "Script filename triggered by binlogs rotation")
	flags.StringVar(&conf.BinlogParseMode, "binlog-parse-mode", "gomysql", "Method for parsing binlogs: mysqlbinlog|gomysql")

	flags.BoolVar(&conf.ProvBinaryInTarball, "prov-db-binary-in-tarball", false, "Add prov-db-binary-tarball-name binaries to init tarball")
	flags.StringVar(&conf.ProvBinaryTarballName, "prov-db-binary-tarball-name", "mysql-8.0.17-macos10.14-x86_64.tar.gz", "Name of binary tarball to put in tarball")

	flags.BoolVar(&conf.OptimizeUseSQL, "optimize-use-sql", true, "Orchetrate optimize table via SQL not via database job using mysqlcheck")
	flags.BoolVar(&conf.AnalyzeUseSQL, "analyze-use-sql", true, "Orchetrate analyze table via SQL not via database job using mysqlcheck")

	flags.StringVar(&conf.ProvIops, "prov-db-disk-iops", "300", "Rnd IO/s in for micro service VM")
	flags.StringVar(&conf.ProvIopsLatency, "prov-db-disk-iops-latency", "0.002", "IO latency in s")
	flags.StringVar(&conf.ProvCores, "prov-db-cpu-cores", "1", "Number of cpu cores for the micro service VM")
	flags.BoolVar(&conf.ProvDBApplyDynamicConfig, "prov-db-apply-dynamic-config", false, "Dynamic database config change")
	flags.StringVar(&conf.ProvTags, "prov-db-tags", "semisync,row,innodb,noquerycache,threadpool,slow,pfs,docker,linux,readonly,diskmonitor,sqlerror,compressbinlog", "playbook configuration tags")
	flags.StringVar(&conf.ProvDomain, "prov-db-domain", "0", "Config domain id for the cluster")
	flags.StringVar(&conf.ProvMem, "prov-db-memory", "256", "Memory in M for micro service VM")
	flags.StringVar(&conf.ProvMemSharedPct, "prov-db-memory-shared-pct", "threads:16,innodb:60,myisam:10,aria:10,rocksdb:1,tokudb:1,s3:1,archive:1,querycache:0", "% memory shared per buffer")
	flags.StringVar(&conf.ProvMemThreadedPct, "prov-db-memory-threaded-pct", "tmp:70,join:20,sort:10", "% memory allocted per threads")
	flags.StringVar(&conf.ProvDisk, "prov-db-disk-size", "20", "Disk in g for micro service VM")
	flags.IntVar(&conf.ProvExpireLogDays, "prov-db-expire-log-days", 5, "Keep binlogs that nunmber of days")
	flags.IntVar(&conf.ProvMaxConnections, "prov-db-max-connections", 1000, "Max database connections")
	flags.StringVar(&conf.ProvProxTags, "prov-proxy-tags", "masterslave,docker,linux,noreadwritesplit", "playbook configuration tags wsrep,multimaster,masterslave")
	flags.StringVar(&conf.ProvProxDisk, "prov-proxy-disk-size", "20", "Disk in g for micro service VM")
	flags.StringVar(&conf.ProvProxCores, "prov-proxy-cpu-cores", "1", "Cpu cores ")
	flags.StringVar(&conf.ProvProxMem, "prov-proxy-memory", "1", "Memory usage in giga bytes")
	flags.StringVar(&conf.ProvServicePlanRegistry, "prov-service-plan-registry", "https://docs.google.com/spreadsheets/d/e/2PACX-1vQClXknRapJZ4bRSId_aa5zUrbFDZmmc6GiV3n7-tPyQJispqqnSJj6lMaJxoJv5pOC9Ktj8ywWdGX6/pub?gid=0&single=true&output=csv", "URL to csv service plan list")
	//	flags.StringVar(&conf.ProvServicePlanRegistry, "prov-service-plan-registry", "http://gsx2json.com/api?id=130326CF_SPaz-flQzCRPE-w7FjzqU1NqbsM7MpIQ_oU&sheet=1&columns=false", "URL to json service plan list")
	flags.StringVar(&conf.ProvServicePlan, "prov-service-plan", "", "Cluster plan")
	flags.BoolVar(&conf.ProvSerialized, "prov-serialized", false, "Disable concurrent provisionning")
	flags.StringVar(&conf.ProvDBClientBasedir, "prov-db-client-basedir", "/usr/bin", "Path to database client binary")
	flags.StringVar(&conf.ProvDBBinaryBasedir, "prov-db-binary-basedir", "/usr/local/mysql/bin", "Path to mysqld binary")

	flags.BoolVar(&conf.Test, "test", false, "Enable non regression tests")
	flags.BoolVar(&conf.TestInjectTraffic, "test-inject-traffic", false, "Inject some database traffic via proxy")
	flags.IntVar(&conf.SysbenchTime, "sysbench-time", 100, "Time to run benchmark")
	flags.IntVar(&conf.SysbenchThreads, "sysbench-threads", 4, "Number of threads to run benchmark")
	flags.StringVar(&conf.SysbenchTest, "sysbench-test", "oltp_read_write", "oltp_read_write|tpcc|oltp_read_only|oltp_update_index|oltp_update_non_index")
	flags.IntVar(&conf.SysbenchScale, "sysbench-scale", 1, "Number of warehouse")
	flags.IntVar(&conf.SysbenchTables, "sysbench-tables", 1, "Number of tables")
	flags.BoolVar(&conf.SysbenchV1, "sysbench-v1", false, "v1 get different syntax")
	flags.StringVar(&conf.SysbenchBinaryPath, "sysbench-binary-path", "/usr/bin/sysbench", "Sysbench Wrapper in test mode")

	if WithOpenSVC == "ON" {
		flags.StringVar(&conf.ProvOrchestratorEnable, "prov-orchestrator-enable", "opensvc,kube,onpremise,local", "seprated list of orchestrator ")
		flags.StringVar(&conf.ProvOrchestrator, "prov-orchestrator", "opensvc", "onpremise|opensvc|kube|slapos|local")
		flags.StringVar(&conf.ProvOrchestratorCluster, "prov-orchestrator-cluster", "local", "The orchestrated cluster used in FQDNS")
	} else {
		flags.StringVar(&conf.ProvOrchestrator, "prov-orchestrator", "onpremise", "onpremise|opensvc|kube|slapos|local")
		flags.StringVar(&conf.ProvOrchestratorEnable, "prov-orchestrator-enable", "onpremise,local", "seprated list of orchestrator ")
	}
	flags.StringVar(&conf.SlapOSDBPartitions, "slapos-db-partitions", "", "List databases slapos partitions path")
	flags.StringVar(&conf.SlapOSProxySQLPartitions, "slapos-proxysql-partitions", "", "List proxysql slapos partitions path")
	flags.StringVar(&conf.SlapOSHaProxyPartitions, "slapos-haproxy-partitions", "", "List haproxy slapos partitions path")
	flags.StringVar(&conf.SlapOSMaxscalePartitions, "slapos-maxscale-partitions", "", "List maxscale slapos partitions path")
	flags.StringVar(&conf.SlapOSShardProxyPartitions, "slapos-shardproxy-partitions", "", "List spider slapos partitions path")
	flags.StringVar(&conf.SlapOSSphinxPartitions, "slapos-sphinx-partitions", "", "List sphinx slapos partitions path")
	flags.StringVar(&conf.ProvDbBootstrapScript, "prov-db-bootstrap-script", "", "Database bootstrap script")
	flags.StringVar(&conf.ProvProxyBootstrapScript, "prov-proxy-bootstrap-script", "", "Proxy bootstrap script")
	flags.StringVar(&conf.ProvDbCleanupScript, "prov-db-cleanup-script", "", "Database cleanup script")
	flags.StringVar(&conf.ProvProxyCleanupScript, "prov-proxy-cleanup-script", "", "Proxy cleanup script")
	flags.StringVar(&conf.ProvDbStartScript, "prov-db-start-script", "", "Database start script")
	flags.StringVar(&conf.ProvProxyStartScript, "prov-proxy-start-script", "", "Proxy start script")
	flags.StringVar(&conf.ProvDbStopScript, "prov-db-stop-script", "", "Database stop script")
	flags.StringVar(&conf.ProvProxyStopScript, "prov-proxy-stop-script", "", "Proxy stop script")

	flags.BoolVar(&conf.OnPremiseSSH, "onpremise-ssh", false, "Connect to host via SSH using user private key")
	flags.StringVar(&conf.OnPremiseSSHPrivateKey, "onpremise-ssh-private-key", "", "Private key for ssh if none use the user HOME directory")
	flags.IntVar(&conf.OnPremiseSSHPort, "onpremise-ssh-port", 22, "Connect to host via SSH using ssh port")
	flags.StringVar(&conf.OnPremiseSSHCredential, "onpremise-ssh-credential", "root:", "User:password for ssh if no password using current user private key")
	flags.StringVar(&conf.OnPremiseSSHStartDbScript, "onpremise-ssh-start-db-script", "", "Run via ssh a custom script to start database")
	flags.StringVar(&conf.OnPremiseSSHStartProxyScript, "onpremise-ssh-start-proxy-script", "", "Run via ssh a custom script to start proxy")
	flags.StringVar(&conf.OnPremiseSSHDbJobScript, "onpremise-ssh-db-job-script", "", "Run via ssh a custom script to execute database jobs")

	if WithProvisioning == "ON" {
		flags.StringVar(&conf.ProvDatadirVersion, "prov-db-datadir-version", "10.2", "Empty datadir to deploy for localtest")
		flags.StringVar(&conf.ProvDiskSystemSize, "prov-db-disk-system-size", "2", "Disk in g for micro service VM")
		flags.StringVar(&conf.ProvDiskTempSize, "prov-db-disk-temp-size", "128", "Disk in m for micro service VM")
		flags.StringVar(&conf.ProvDiskDockerSize, "prov-db-disk-docker-size", "2", "Disk in g for Docker Private per micro service VM")
		flags.StringVar(&conf.ProvDbImg, "prov-db-docker-img", "mariadb:latest", "Docker image for database")
		flags.StringVar(&conf.ProvType, "prov-db-service-type ", "package", "[package|docker|podman|oci|kvm|zone|lxc]")
		flags.StringVar(&conf.ProvAgents, "prov-db-agents", "", "Comma seperated list of agents for micro services provisionning")
		flags.StringVar(&conf.ProvDiskFS, "prov-db-disk-fs", "ext4", "[zfs|xfs|ext4]")
		flags.StringVar(&conf.ProvDiskFSCompress, "prov-db-disk-fs-compress", "off", " ZFS supported compression [off|gzip|lz4]")
		flags.StringVar(&conf.ProvDiskPool, "prov-db-disk-pool", "none", "[none|zpool|lvm]")
		flags.StringVar(&conf.ProvDiskType, "prov-db-disk-type", "loopback", "[loopback|physical|pool|directory|volume]")
		flags.StringVar(&conf.ProvVolumeDocker, "prov-db-volume-docker", "", "Volume name in case of docker private")
		flags.StringVar(&conf.ProvVolumeData, "prov-db-volume-data", "", "Volume name for the datadir")
		flags.StringVar(&conf.ProvDiskDevice, "prov-db-disk-device", "/srv", "loopback:path-to-loopfile|physical:/dev/xx|pool:pool-name|directory:/srv")
		flags.BoolVar(&conf.ProvDiskSnapshot, "prov-db-disk-snapshot-prefered-master", false, "Take snapshoot of prefered master")
		flags.IntVar(&conf.ProvDiskSnapshotKeep, "prov-db-disk-snapshot-keep", 7, "Keek this number of snapshoot of prefered master")
		flags.StringVar(&conf.ProvNetIface, "prov-db-net-iface", "eth0", "HBA Device to hold Ips")
		flags.StringVar(&conf.ProvGateway, "prov-db-net-gateway", "192.168.0.254", "Micro Service network gateway")
		flags.StringVar(&conf.ProvNetmask, "prov-db-net-mask", "255.255.255.0", "Micro Service network mask")
		flags.StringVar(&conf.ProvDBLoadCSV, "prov-db-load-csv", "", "List of shema.table csv file to load a bootstrap")
		flags.StringVar(&conf.ProvDBLoadSQL, "prov-db-load-sql", "", "List of sql scripts file to load a bootstrap")
		flags.StringVar(&conf.ProvProxType, "prov-proxy-service-type", "package", "[package|docker|podman|oci|kvm|zone|lxc]")
		flags.StringVar(&conf.ProvProxAgents, "prov-proxy-agents", "", "Comma seperated list of agents for micro services provisionning")
		flags.StringVar(&conf.ProvProxAgentsFailover, "prov-proxy-agents-failover", "", "Service Failover Agents")
		flags.StringVar(&conf.ProvProxDiskFS, "prov-proxy-disk-fs", "ext4", "[zfs|xfs|ext4]")
		flags.StringVar(&conf.ProvProxDiskPool, "prov-proxy-disk-pool", "none", "[none|zpool|lvm]")
		flags.StringVar(&conf.ProvProxDiskType, "prov-proxy-disk-type", "loopback", "[loopback|physical|pool|directory|volume]")
		flags.StringVar(&conf.ProvProxDiskDevice, "prov-proxy-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
		flags.StringVar(&conf.ProvProxVolumeData, "prov-proxy-volume-data", "", "Volume name of the data files")
		flags.StringVar(&conf.ProvProxNetIface, "prov-proxy-net-iface", "eth0", "HBA Device to hold Ips")
		flags.StringVar(&conf.ProvProxGateway, "prov-proxy-net-gateway", "192.168.0.254", "Micro Service network gateway")
		flags.StringVar(&conf.ProvProxNetmask, "prov-proxy-net-mask", "255.255.255.0", "Micro Service network mask")
		flags.StringVar(&conf.ProvProxRouteAddr, "prov-proxy-route-addr", "", "Route adress to databases proxies")
		flags.StringVar(&conf.ProvProxRoutePort, "prov-proxy-route-port", "", "Route Port to databases proxies")
		flags.StringVar(&conf.ProvProxRouteMask, "prov-proxy-route-mask", "255.255.255.0", "Route Netmask to databases proxies")
		flags.StringVar(&conf.ProvProxRoutePolicy, "prov-proxy-route-policy", "failover", "Route policy failover or balance")
		flags.StringVar(&conf.ProvProxProxysqlImg, "prov-proxy-docker-proxysql-img", "signal18/proxysql:1.4", "Docker image for proxysql")
		flags.StringVar(&conf.ProvProxMaxscaleImg, "prov-proxy-docker-maxscale-img", "mariadb/maxscale:2.2", "Docker image for maxscale proxy")
		flags.StringVar(&conf.ProvProxHaproxyImg, "prov-proxy-docker-haproxy-img", "haproxytech/haproxy-alpine:2.4", "Docker image for haproxy")
		flags.StringVar(&conf.ProvProxMysqlRouterImg, "prov-proxy-docker-mysqlrouter-img", "pulsepointinc/mysql-router", "Docker image for MySQLRouter")
		flags.StringVar(&conf.ProvProxShardingImg, "prov-proxy-docker-shardproxy-img", "signal18/mariadb104-spider", "Docker image for sharding proxy")
		flags.StringVar(&conf.ProvSphinxImg, "prov-sphinx-docker-img", "leodido/sphinxsearch", "Docker image for SphinxSearch")
		flags.StringVar(&conf.ProvSphinxTags, "prov-sphinx-tags", "masterslave", "playbook configuration tags wsrep,multimaster,masterslave")
		flags.StringVar(&conf.ProvSphinxType, "prov-sphinx-service-type", "package", "[package|docker]")
		flags.StringVar(&conf.ProvSphinxAgents, "prov-sphinx-agents", "", "Comma seperated list of agents for micro services provisionning")
		flags.StringVar(&conf.ProvSphinxDiskFS, "prov-sphinx-disk-fs", "ext4", "[zfs|xfs|ext4]")
		flags.StringVar(&conf.ProvSphinxDiskPool, "prov-sphinx-disk-pool", "none", "[none|zpool|lvm]")
		flags.StringVar(&conf.ProvSphinxDiskType, "prov-sphinx-disk-type", "[loopback|physical]", "[none|zpool|lvm]")
		flags.StringVar(&conf.ProvSphinxDiskDevice, "prov-sphinx-disk-device", "[loopback|physical]", "[path-to-loopfile|/dev/xx]")
		flags.StringVar(&conf.ProvSphinxMem, "prov-sphinx-memory", "256", "Memory in M for micro service VM")
		flags.StringVar(&conf.ProvSphinxDisk, "prov-sphinx-disk-size", "20", "Disk in g for micro service VM")
		flags.StringVar(&conf.ProvSphinxCores, "prov-sphinx-cpu-cores", "1", "Number of cpu cores for the micro service VM")
		flags.StringVar(&conf.ProvSphinxCron, "prov-sphinx-reindex-schedule", "@5", "task time to 5 minutes for index rotation")
		flags.StringVar(&conf.ProvSSLCa, "prov-tls-server-ca", "", "server TLS ca")
		flags.StringVar(&conf.ProvSSLCert, "prov-tls-server-cert", "", "server TLS cert")
		flags.StringVar(&conf.ProvSSLKey, "prov-tls-server-key", "", "server TLS key")
		flags.BoolVar(&conf.ProvNetCNI, "prov-net-cni", false, "Networking use CNI")
		flags.StringVar(&conf.ProvNetCNICluster, "prov-net-cni-cluster", "default", "Name of of the OpenSVC network")
		flags.BoolVar(&conf.ProvDockerDaemonPrivate, "prov-docker-daemon-private", true, "Use global or private registry per service")
		flags.StringVar(&conf.ProvDBCompliance, "prov-db-compliance", "", "Path of compliance file for DB configuration")
		flags.StringVar(&conf.ProvProxyCompliance, "prov-proxy-compliance", "", "Path of compliance file for Proxy configuration")

		flags.BoolVar(&conf.Cloud18, "cloud18", false, "Enable Cloud 18 DBAAS")
		flags.StringVar(&conf.Cloud18Domain, "cloud18-domain", "signal18", "DNS sub domain per organisation")
		flags.StringVar(&conf.Cloud18SubDomain, "cloud18-sub-domain", "ovh-1", "DNS sub domain per replication-manger instance")
		flags.StringVar(&conf.Cloud18SubDomainZone, "cloud18-sub-domain-zone", "fr", "DNS sub domain per replication-manger instance")
		flags.BoolVar(&conf.Cloud18Shared, "cloud18-shared", false, "Enable cluster sharing for Cloud 18 DBAAS")
		flags.StringVar(&conf.Cloud18GitUser, "cloud18-gitlab-user", "", "Cloud 18 GitLab user")
		flags.StringVar(&conf.Cloud18GitPassword, "cloud18-gitlab-password", "", "Cloud 18 GitLab password")
		flags.StringVar(&conf.Cloud18PlatformDescription, "cloud18-platform-description", "", "Marketing banner display on the cloud18 portal describing the infrastucture")

		if WithOpenSVC == "ON" {

			flags.BoolVar(&conf.Enterprise, "opensvc", true, "Provisioning via opensvc")
			flags.StringVar(&conf.ProvHost, "opensvc-host", "collector.signal18.io:443", "OpenSVC collector API")
			flags.StringVar(&conf.ProvAdminUser, "opensvc-admin-user", "root@signal18.io:opensvc", "OpenSVC collector admin user")
			flags.BoolVar(&conf.ProvRegister, "opensvc-register", false, "Register user codeapp to collector, load compliance")
			flags.StringVar(&conf.ProvOpensvcP12Certificate, "opensvc-p12-certificate", "/etc/replication-manager/s18.p12", "Certicate used for socket vs collector API opensvc-host refer to a cluster VIP")
			flags.BoolVar(&conf.ProvOpensvcUseCollectorAPI, "opensvc-use-collector-api", false, "Use the collector API instead of cluster VIP")
			flags.StringVar(&conf.KubeConfig, "kube-config", "", "path to ks8 config file")
			flags.StringVar(&conf.ProvOpensvcCollectorAccount, "opensvc-collector-account", "/etc/replication-manager/account.yaml", "Openscv collector account")

			if conf.ProvOpensvcUseCollectorAPI {
				dbConfig := viper.New()
				dbConfig.SetConfigType("yaml")
				file, err := os.ReadFile(conf.ProvOpensvcCollectorAccount)
				if err != nil {
					log.Errorf("Provide OpenSVC account file : %s", err)

				}

				dbConfig.ReadConfig(bytes.NewBuffer(file))
				conf.ProvUser = dbConfig.Get("email").(string) + ":" + dbConfig.Get("hashed_password").(string)
				crcTable := crc64.MakeTable(crc64.ECMA)
				conf.ProvCodeApp = "ns" + strconv.FormatUint(crc64.Checksum([]byte(dbConfig.Get("email").(string)), crcTable), 10)
			}

		}
	}

}

// DicoverClusters from viper merged config send a sperated list of clusters
func (repman *ReplicationManager) DiscoverClusters(FirstRead *viper.Viper) string {
	m := FirstRead.AllKeys()

	var clusterDiscovery = map[string]string{}
	var discoveries []string
	for _, k := range m {

		if strings.Contains(k, ".") {
			mycluster := strings.Split(k, ".")[0]
			//	log.Infof("Evaluate key %s %s", mycluster, k)
			if strings.ToLower(mycluster) != "default" {
				if strings.HasPrefix(mycluster, "saved-") {
					mycluster = strings.TrimPrefix(mycluster, "saved-")
				}
				_, ok := clusterDiscovery[mycluster]
				if !ok {
					clusterDiscovery[mycluster] = mycluster
					discoveries = append(discoveries, mycluster)
					log.Infof("Cluster discover from config: %s", strings.Split(k, ".")[0])
				}
			}

		}
	}
	return strings.Join(discoveries, ",")

}

func (repman *ReplicationManager) OverwriteParameterFlags(destViper *viper.Viper) {
	m := viper.AllSettings()
	//m := viper.AllSettings()
	for k, v := range m {
		if destViper.Get(k) != viper.Get(k) {
			fmt.Printf("%s:%v\n", k, v)
		}

	}

}

func (repman *ReplicationManager) initEmbed() error {
	//test si y'a  un repertoire ./.replication-manager sinon on le créer
	//test si y'a  un repertoire ./.replication-manager/config.toml sinon on le créer depuis embed
	//test y'a  un repertoire ./.replication-manager/data sinon on le créer
	//test y'a  un repertoire ./.replication-manager/share sinon on le créer
	if _, err := os.Stat("./.replication-manager"); os.IsNotExist(err) {
		os.MkdirAll("./.replication-manager", os.ModePerm)
		os.MkdirAll("./.replication-manager/data", os.ModePerm)
		os.MkdirAll("./.replication-manager/share", os.ModePerm)
	}

	if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {

		file, err := etc.EmbededDbModuleFS.ReadFile("local/embed/config.toml")
		if err != nil {
			log.Errorf("failed opening file because: %s", err.Error())
			return err
		}
		err = os.WriteFile("./.replication-manager/config.toml", file, 0644) //remplacer nil par l'obj créer pour config.toml dans etc/local/embed
		if err != nil {
			log.Errorf("failed write file because: %s", err.Error())
			return err
		}
		if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {
			log.Errorf("failed create ./.replication-manager/config.toml file because: %s", err.Error())
			return err
		}
	}

	return nil
}

func (repman *ReplicationManager) InitConfig(conf config.Config) {
	repman.VersionConfs = make(map[string]*config.ConfVersion)
	repman.ImmuableFlagMaps = make(map[string]map[string]interface{})
	repman.DynamicFlagMaps = make(map[string]map[string]interface{})
	ImmuableMap := make(map[string]interface{})
	DynamicMap := make(map[string]interface{})
	// repman.UserAuthTry = make(map[string]authTry)
	repman.cloud18CheckSum = nil
	// call after init if configuration file is provide

	//if repman is embed, create folders and load missing embedded files
	if conf.WithEmbed == "ON" {
		repman.initEmbed()
	}

	//init viper to read config file .toml
	fistRead := viper.GetViper()
	fistRead.SetConfigType("toml")

	//DefaultFlagMap is a map that contain all default flag value, set in the server_monitor.go file
	//fmt.Printf("%s", repman.DefaultFlagMap)

	//if a config file is already define
	if conf.ConfigFile != "" {
		if _, err := os.Stat(conf.ConfigFile); os.IsNotExist(err) {
			//	log.Fatal("No config file " + conf.ConfigFile)
			log.Error("No config file " + conf.ConfigFile)
		}
		fistRead.SetConfigFile(conf.ConfigFile)

	} else {
		//adds config files by searching them in different folders
		fistRead.SetConfigName("config")
		if conf.WithEmbed == "OFF" {
			fistRead.AddConfigPath("/etc/replication-manager/")
		} else {
			fistRead.AddConfigPath("./.replication-manager")
		}
		fistRead.AddConfigPath(".")

		//if tarball, add config path
		if conf.WithTarball == "ON" {
			fistRead.AddConfigPath("/usr/local/replication-manager/etc")
			if _, err := os.Stat("/usr/local/replication-manager/etc/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file /usr/local/replication-manager/etc/config.toml")
			}
		}
		//if embed, add config path
		if conf.WithEmbed == "ON" {
			if _, err := os.Stat("./.replication-manager/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file ./.replication-manager/config.toml ")
			}
		} else {
			if _, err := os.Stat("/etc/replication-manager/config.toml"); os.IsNotExist(err) {
				log.Warning("No config file /etc/replication-manager/config.toml ")
			}
		}
	}
	//default path for cluster config
	conf.ClusterConfigPath = conf.WorkingDir + "/cluster.d"

	//search for default section in config file and read
	//setEnvPrefix is case insensitive
	fistRead.SetEnvPrefix("DEFAULT")
	err := fistRead.ReadInConfig()
	if err == nil {
		log.WithFields(log.Fields{
			"file": fistRead.ConfigFileUsed(),
		}).Debug("Using config file")
	} else {
		log.Errorf("Could not parse config file: %s", err)
	}

	//recup tous les param set dans le default (avec les lignes de commandes)
	//err = fistRead.MergeInConfig()
	if err != nil {
		log.Fatal("Config error in " + conf.ClusterConfigPath + ":" + err.Error())
	}
	secRead := fistRead.Sub("DEFAULT")
	//var test config.Config
	//secRead.UnmarshalKey("default", &test)

	//fmt.Printf("REPMAN DEFAULT SECTION : %s", secRead.AllSettings())
	if secRead != nil {
		for _, f := range secRead.AllKeys() {
			v := secRead.Get(f)
			if v != nil {
				ImmuableMap[f] = secRead.Get(f)
			}

		}
	}

	//Add immuatable flag from default section

	//test.PrintConf()

	//from here first read as the combination of default sections variables but not forced parameters

	// Proceed include files
	//if include is defined in a config file
	if fistRead.GetString("default.include") != "" {
		log.Info("Reading default section include directory: " + fistRead.GetString("default.include"))

		if _, err := os.Stat(fistRead.GetString("default.include")); os.IsNotExist(err) {
			log.Warning("Include config directory does not exist " + conf.Include)
		} else {
			//if this path exist, set cluster config path to it
			conf.ClusterConfigPath = fistRead.GetString("default.include")
		}

		//load files from the include path
		files, err := os.ReadDir(conf.ClusterConfigPath)
		if err != nil {
			log.Infof("No config include directory %s ", conf.ClusterConfigPath)
		}
		//read and set config from all files in the include path
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".toml") {
				//file_name := strings.Split(f.Name(), ".")
				//cluster_name := file_name[0]
				fistRead.SetConfigName(f.Name())
				fistRead.SetConfigFile(conf.ClusterConfigPath + "/" + f.Name())
				//	viper.Debug()
				fistRead.AutomaticEnv()
				fistRead.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))

				err := fistRead.MergeInConfig()
				if err != nil {
					log.Fatal("Config error in " + conf.ClusterConfigPath + "/" + f.Name() + ":" + err.Error())
				}

				//recup tous les param set dans le include
				//secRead = fistRead.Sub(cluster_name)
				//secRead.UnmarshalKey(cluster_name, &test)
			}
		}
	} else {
		log.Warning("No include directory in default section")
	}

	tmp_read := fistRead.Sub("Default")
	if tmp_read != nil {
		tmp_read.Unmarshal(&conf)
	}

	// Proceed dynamic config
	if fistRead.GetBool("default.monitoring-save-config") {
		//read working dir from config
		if fistRead.GetString("default.monitoring-datadir") != "" {
			conf.WorkingDir = fistRead.GetString("default.monitoring-datadir")
		}

		dynRead := viper.GetViper()
		dynRead.SetConfigType("toml")

		//load files from the working dir
		files, err := os.ReadDir(conf.WorkingDir)
		if err != nil {
			log.Infof("No working directory %s ", conf.WorkingDir)
		}

		//read and set config from all files in the working dir
		for _, f := range files {
			if f.IsDir() && f.Name() != "graphite" {
				fistRead.SetConfigName(f.Name())
				dynRead.SetConfigName("overwrite-" + f.Name())
				if _, err := os.Stat(conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml"); os.IsNotExist(err) || f.Name() == "overwrite" {
					if f.Name() != "overwrite" {
						log.Warning("No monitoring saved config found " + conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml")
					}

				} else {

					log.Infof("Parsing saved config from working directory %s ", conf.WorkingDir+"/"+f.Name()+"/"+f.Name()+".toml")
					fistRead.SetConfigFile(conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml")
					err := fistRead.MergeInConfig()
					if err != nil {
						log.Fatal("Config error in " + conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml" + ":" + err.Error())
					}
				}
			}
		}

		//to read and set cloud18.toml config file if exist
		if _, err := os.Stat(conf.WorkingDir + "/cloud18.toml"); os.IsNotExist(err) {
			log.Infof("No monitoring saved config found %s", conf.WorkingDir+"/cloud18.toml")
		} else {
			tmp_read.SetConfigFile(conf.WorkingDir + "/cloud18.toml")
			err := tmp_read.MergeInConfig()
			if err != nil {
				log.Error("Config error in " + conf.WorkingDir + "/cloud18.toml:" + err.Error())
			}
		}

		//fmt.Printf("%+v\n", dynRead.AllSettings())
		//fmt.Printf("%s\n", dynRead.AllKeys())

	} else {
		log.Warning("No monitoring-save-config variable in default section config change lost on restart")
	}

	//contain a list of cluster name
	var strClusters string
	strClusters = cfgGroup

	//if cluster name is empty, go discover cluster
	if strClusters == "" {
		// Discovering the clusters from all merged conf files build clusterDiscovery map
		strClusters = repman.DiscoverClusters(fistRead)
		log.WithField("clusters", strClusters).Infof("Clusters discovered: %s", strClusters)
	}

	cfgGroupIndex = 0
	//extract the default section of the config files
	cf1 := fistRead.Sub("Default")

	//cf1.Debug()
	if cf1 == nil {
		log.Warning("config.toml has no [Default] configuration group and config group has not been specified")
	} else {
		//save all default section in conf
		cf1.AutomaticEnv()
		cf1.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
		cf1.SetEnvPrefix("DEFAULT")
		repman.initAlias(cf1)
		cf1.Unmarshal(&conf)
		conf.LoadEncrytionKey()
		repman.Conf = conf

	}
	//	backupvipersave := viper.GetViper()

	//if clusters have been discovered
	if strClusters == "" {

		//add default to the clusterlist if no cluster discover
		log.WithField("cluster", "Default").Debug("No clusters dicoverd add Default Cluster")

		strClusters += "Default"

	}

	//set cluster list
	repman.ClusterList = strings.Split(strClusters, ",")
	repman.ImmuableFlagMaps["default"] = ImmuableMap
	conf.ImmuableFlagMap = ImmuableMap
	//load config file from git hub
	conf.DecryptSecretsFromConfig()

	if conf.GitUrl != "" && conf.GitAccesToken != "" && !conf.Cloud18 {
		var tok string

		if conf.IsVaultUsed() && conf.IsPath(conf.GitAccesToken) {
			conn, err := conf.GetVaultConnection()
			if err != nil {
				log.Printf("Error vault connection %v", err)
			}
			tok, err = conf.GetVaultCredentials(conn, conf.GitAccesToken, "git-acces-token")
			if err != nil {
				log.Printf("Error get vault git-acces-token value %v", err)
				tok = conf.GetDecryptedValue("git-acces-token")
			} else {
				var Secrets config.Secret
				Secrets.Value = tok
				conf.Secrets["git-acces-token"] = Secrets
			}

		} else {
			tok = conf.GetDecryptedValue("git-acces-token")
		}

		conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, tok, conf.WorkingDir)
	}

	if conf.Cloud18GitUser != "" && conf.Cloud18GitPassword != "" && conf.Cloud18 {
		acces_tok := githelper.GetGitLabTokenBasicAuth(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), conf.LogGit)
		personal_access_token, _ := githelper.GetGitLabTokenOAuth(acces_tok, conf.LogGit)
		if personal_access_token != "" {
			var Secrets config.Secret
			Secrets.Value = personal_access_token
			conf.Secrets["git-acces-token"] = Secrets
			conf.GitUrl = conf.OAuthProvider + "/" + conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + ".git"
			conf.GitUsername = conf.Cloud18GitUser
			conf.GitAccesToken = personal_access_token
			conf.ImmuableFlagMap["git-url"] = conf.GitUrl
			conf.ImmuableFlagMap["git-username"] = conf.GitUsername
			conf.ImmuableFlagMap["git-acces-token"] = personal_access_token
			conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, conf.GitAccesToken, conf.WorkingDir)
			conf.PushConfigToGit(conf.GitUrl, conf.GitAccesToken, conf.GitUsername, conf.WorkingDir, []string{})
			//conf.GitAddReadMe(conf.GitUrl, conf.GitAccesToken, conf.GitUsername, conf.WorkingDir)

		} else if conf.LogGit {
			log.WithField("group", repman.ClusterList[cfgGroupIndex]).Infof("Could not get personal access token from gitlab")
		}

	}

	//add config from cluster to the config map
	for _, cluster := range repman.ClusterList {
		//vipersave := backupvipersave
		confs[cluster] = repman.GetClusterConfig(fistRead, ImmuableMap, DynamicMap, cluster, conf)
		cfgGroupIndex++

	}

	cfgGroupIndex--
	log.WithField("cluster", repman.ClusterList[cfgGroupIndex]).Debug("Default Cluster set")

	//fmt.Printf("%+v\n", fistRead.AllSettings())
	repman.Confs = confs
	repman.Conf = conf
	repman.ViperConfig = fistRead
}

func (repman *ReplicationManager) GetClusterConfig(fistRead *viper.Viper, ImmuableMap map[string]interface{}, DynamicMap map[string]interface{}, cluster string, conf config.Config) config.Config {
	confs := new(config.ConfVersion)
	clustImmuableMap := make(map[string]interface{})
	clustDynamicMap := make(map[string]interface{})

	//to copy default immuable flag in the immuable flag cluster map
	for k, v := range ImmuableMap {
		clustImmuableMap[k] = v
	}

	//to copy default dynamic flag in the dynamic flag cluster map
	for k, v := range DynamicMap {
		clustDynamicMap[k] = v
	}

	//Add immuatable flag from command line
	for _, f := range repman.CommandLineFlag {
		v := fistRead.Get(f)
		if v != nil {
			clustImmuableMap[f] = v
		}

	}

	//set the default config
	clusterconf := conf

	//conf.PrintConf()

	//if name cluster is defined
	if cluster != "" {
		log.WithField("group", cluster).Debug("Reading configuration group")

		//extract the cluster config from the viper
		cf2 := fistRead.Sub(cluster)

		if cf2 == nil {
			log.WithField("group", cluster).Infof("Could not parse configuration group")
		} else {
			cf2.AutomaticEnv()
			cf2.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
			repman.initAlias(cf2)
			cf2.Unmarshal(&clusterconf)
			//fmt.Printf("saved conf :")
			//clusterconf.PrintConf()
			//Add immuatable flag from cluster section
			for _, f := range cf2.AllKeys() {
				v := cf2.Get(f)
				if v != nil {
					clustImmuableMap[f] = v
				}

			}
		}

		//clusterconf.PrintConf()

		//save the immuable map for the cluster
		//fmt.Printf("Immuatable map : %s\n", ImmuableMap)
		repman.ImmuableFlagMaps[cluster] = clustImmuableMap

		//store default cluster config in immutable config (all parameter set in default and cluster section, default value and command line)
		confs.ConfImmuable = clusterconf

		//fmt.Printf("%+v\n", cf2.AllSettings())
		repman.DynamicFlagMaps["default"] = clustDynamicMap
		//if dynamic config, load modified parameter from the saved config
		if clusterconf.ConfRewrite {

			cf3 := fistRead.Sub("saved-" + cluster)

			//cf4 := repman.CleanupDynamicConfig(clustImmuableMap, cf3)
			if cf3 == nil {
				log.WithField("group", cluster).Info("Could not parse saved configuration group")
			} else {
				for _, f := range cf3.AllKeys() {
					v, ok := clustImmuableMap[f]
					if ok {
						cf3.Set(f, v)
					}
				}
				repman.initAlias(cf3)
				cf3.Unmarshal(&clusterconf)
				//to add flag in cluster dynamic map only if not defined yet or if the flag value read is diff from immuable flag value
				for _, f := range cf3.AllKeys() {
					v := cf3.Get(f)
					if v != nil {
						imm_v, ok := clustImmuableMap[f]
						if ok && imm_v != v {
							clustDynamicMap[f] = v
						}
						if !ok {
							clustDynamicMap[f] = v
						}

					}

				}
			}
			confs.ConfDynamic = clusterconf

		}
		repman.DynamicFlagMaps[cluster] = clustDynamicMap

		confs.ConfInit = clusterconf
		//fmt.Printf("init conf : ")
		//clusterconf.PrintConf()

		repman.VersionConfs[cluster] = confs
	}
	return clusterconf
}

/*
func CleanupDynamicConfig(clustImmuableMap map[string]interface{}, cf viper.Viper, cluster string) viper.Viper {
	//if admin change immuable value that is already saved in dynamic config, we need to remove it before parse
	for _, f := range cf.AllKeys() {
		_, ok := clustImmuableMap[f]
		if ok {
			delete(cf.Get(f).(map[string]interface{}), f)
		}

	}

}*/

func (repman *ReplicationManager) initAlias(v *viper.Viper) {
	v.RegisterAlias("monitoring-config-rewrite", "monitoring-save-config")
	v.RegisterAlias("api-user", "api-credentials")
	v.RegisterAlias("replication-master-connection", "replication-source-name")
	v.RegisterAlias("logfile", "log-file")
	v.RegisterAlias("wait-kill", "switchover-wait-kill")
	v.RegisterAlias("user", "db-servers-credential")
	v.RegisterAlias("hosts", "db-servers-hosts")
	v.RegisterAlias("hosts-tls-ca-cert", "db-servers-tls-ca-cert")
	v.RegisterAlias("hosts-tls-client-key", "db-servers-tls-client-key")
	v.RegisterAlias("hosts-tls-client-cert", "db-servers-tls-client-cert")
	v.RegisterAlias("connect-timeout", "db-servers-connect-timeout")
	v.RegisterAlias("rpluser", "replication-credential")
	v.RegisterAlias("prefmaster", "db-servers-prefered-master")
	v.RegisterAlias("ignore-servers", "db-servers-ignored-hosts")
	v.RegisterAlias("master-connection", "replication-master-connection")
	v.RegisterAlias("master-connect-retry", "replication-master-connection-retry")
	//v.RegisterAlias("api-user", "api-credential")
	v.RegisterAlias("readonly", "failover-readonly-state")
	v.RegisterAlias("maxscale-host", "maxscale-servers")
	v.RegisterAlias("mdbshardproxy-hosts", "mdbshardproxy-servers")
	v.RegisterAlias("multimaster", "replication-multi-master")
	v.RegisterAlias("multi-tier-slave", "replication-multi-tier-slave")
	v.RegisterAlias("pre-failover-script", "failover-pre-script")
	v.RegisterAlias("post-failover-script", "failover-post-script")
	v.RegisterAlias("rejoin-script", "autorejoin-script")
	v.RegisterAlias("share-directory", "monitoring-sharedir")
	v.RegisterAlias("working-directory", "monitoring-datadir")
	v.RegisterAlias("interactive", "failover-mode")
	v.RegisterAlias("failcount", "failover-falsepositive-ping-counter")
	v.RegisterAlias("wait-write-query", "switchover-wait-write-query")
	v.RegisterAlias("wait-trx", "switchover-wait-trx")
	v.RegisterAlias("gtidcheck", "switchover-at-equal-gtid")
	v.RegisterAlias("maxdelay", "failover-max-slave-delay")
	v.RegisterAlias("maxscale-host", "maxscale-servers")
	v.RegisterAlias("maxscale-pass", "maxscale-password")
	v.RegisterAlias("api-credential", "api-credentials")
	v.RegisterAlias("backup-binlogs-method", "binlog-copy-mode")
	v.RegisterAlias("backup-binlogs-script", "binlog-copy-script")
}

func (repman *ReplicationManager) InitRestic() error {
	os.Setenv("AWS_ACCESS_KEY_ID", repman.Conf.BackupResticAwsAccessKeyId)
	os.Setenv("AWS_SECRET_ACCESS_KEY", repman.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
	os.Setenv("RESTIC_REPOSITORY", repman.Conf.BackupResticRepository)
	os.Setenv("RESTIC_PASSWORD", repman.Conf.GetDecryptedValue("backup-restic-password"))
	//os.Setenv("RESTIC_FORGET_ARGS", repman.Conf.BackupResticStoragePolicy)
	return nil
}

func (repman *ReplicationManager) Run() error {
	var err error

	repman.Version = Version
	repman.Fullversion = FullVersion
	repman.Arch = GoArch
	repman.Os = GoOS
	repman.MemProfile = memprofile
	repman.CpuProfile = cpuprofile
	repman.clog = clog.New()

	repman.clog.SetLevel(repman.Conf.ToLogrusLevel(repman.Conf.LogGraphiteLevel))
	if repman.CpuProfile != "" {
		fcpupprof, err := os.Create(repman.CpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(fcpupprof)

	}

	repman.Clusters = make(map[string]*cluster.Cluster)
	repman.UUID = misc.GetUUID()
	if repman.Conf.Arbitration {
		repman.Status = ConstMonitorStandby
	} else {
		repman.Status = ConstMonitorActif
	}
	repman.SplitBrain = false
	repman.Hostname, err = os.Hostname()
	regtest := new(regtest.RegTest)
	repman.Tests = regtest.GetTests()
	if err != nil {
		log.Fatalln("ERROR: replication-manager could not get hostname from system")
	}

	if repman.Conf.LogSyslog {
		hook, err := lSyslog.NewSyslogHook("udp", "localhost:514", syslog.LOG_INFO, "")
		if err == nil {
			log.AddHook(hook)
		}
	}

	if repman.Conf.LogLevel > 1 {
		log.SetLevel(log.DebugLevel)
	}

	if repman.Conf.LogFile != "" {
		log.WithField("version", repman.Version).Info("Log to file: " + repman.Conf.LogFile)
		hook, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
			Filename:   repman.Conf.LogFile,
			MaxSize:    repman.Conf.LogRotateMaxSize,
			MaxBackups: repman.Conf.LogRotateMaxBackup,
			MaxAge:     repman.Conf.LogRotateMaxAge,
			Level:      log.GetLevel(),
			Formatter: &log.TextFormatter{
				DisableColors:   true,
				TimestampFormat: "2006-01-02 15:04:05",
				FullTimestamp:   true,
			},
		})
		if err != nil {
			log.WithError(err).Error("Can't init log file")
		}
		log.AddHook(hook)
	}

	if !repman.Conf.Daemon {
		err := termbox.Init()
		if err != nil {
			log.WithError(err).Fatal("Termbox initialization error")
		}
	}
	repman.termlength = 40
	log.WithField("version", repman.Version).Info("Replication-Manager started in daemon mode")
	loglen := repman.termlength - 9 - (len(strings.Split(repman.Conf.Hosts, ",")) * 3)
	repman.tlog = s18log.NewTermLog(loglen)
	repman.Logs = s18log.NewHttpLog(80)
	repman.InitServicePlans()
	repman.ServiceOrchestrators = repman.Conf.GetOrchestratorsProv()
	repman.InitGrants()
	repman.ServiceRepos, err = repman.Conf.GetDockerRepos(repman.Conf.ShareDir+"/repo/repos.json", repman.Conf.Test)
	if err != nil {
		log.WithError(err).Errorf("Initialization docker repo failed: %s %s", repman.Conf.ShareDir+"/repo/repos.json", err)
	}
	repman.ServiceTarballs, err = repman.Conf.GetTarballs(repman.Conf.Test)
	if err != nil {
		log.WithError(err).Errorf("Initialization tarballs repo failed: %s %s", repman.Conf.ShareDir+"/repo/tarballs.json", err)
	}

	repman.ServiceVM = repman.Conf.GetVMType()
	repman.ServiceFS = repman.Conf.GetFSType()
	repman.ServiceDisk = repman.Conf.GetDiskType()
	repman.ServicePool = repman.Conf.GetPoolType()
	repman.BackupLogicalList = repman.Conf.GetBackupLogicalType()
	repman.BackupPhysicalList = repman.Conf.GetBackupPhysicalType()
	repman.BackupBinlogList = repman.Conf.GetBackupBinlogType()
	repman.BinlogParseList = repman.Conf.GetBinlogParseMode()
	repman.GraphiteTemplateList = repman.Conf.GetGraphiteTemplateList()

	if repman.Conf.ProvOrchestrator == "opensvc" {
		repman.Agents = []opensvc.Host{}
		repman.OpenSVC.Host, repman.OpenSVC.Port = misc.SplitHostPort(repman.Conf.ProvHost)
		repman.OpenSVC.User, repman.OpenSVC.Pass = misc.SplitPair(repman.Conf.ProvAdminUser)
		repman.OpenSVC.RplMgrUser, repman.OpenSVC.RplMgrPassword = misc.SplitPair(repman.Conf.ProvUser) //yaml licence
		repman.OpenSVC.RplMgrCodeApp = repman.Conf.ProvCodeApp
		if !repman.Conf.ProvOpensvcUseCollectorAPI {
			repman.OpenSVC.UseAPI = repman.Conf.ProvOpensvcUseCollectorAPI
			repman.OpenSVC.CertsDERSecret = repman.Conf.GetDecryptedValue("opensvc-p12-secret")
			err := repman.OpenSVC.LoadCert(repman.Conf.ProvOpensvcP12Certificate)
			if err != nil {
				log.Fatalf("Cannot load OpenSVC cluster certificate %s ", err)
			}
		}
		//don't Bootstrap opensvc to speedup test
		if repman.Conf.ProvRegister {
			err := repman.OpenSVC.Bootstrap(repman.Conf.ShareDir + "/opensvc/")
			if err != nil {
				log.Fatalf("%s", err)
			}
			log.Fatalf("Registration to %s collector done", repman.Conf.ProvHost)
		} else {
			repman.OpenSVC.User, repman.OpenSVC.Pass = misc.SplitPair(repman.Conf.ProvUser)
		}

	}

	// Initialize go-carbon
	if repman.Conf.GraphiteEmbedded {
		graphite.Log = repman.clog
		graphite.Log.AddHook(&writer.Hook{ // Send logs with level higher than warning to stderr
			Writer: os.Stderr,
			LogLevels: []log.Level{
				log.PanicLevel,
				log.FatalLevel,
				log.ErrorLevel,
				log.WarnLevel,
			},
		})

		go graphite.RunCarbon(&repman.Conf)
		log.WithFields(log.Fields{
			"metricport": repman.Conf.GraphiteCarbonPort,
			"httpport":   repman.Conf.GraphiteCarbonServerPort,
		}).Info("Carbon server started")
		time.Sleep(2 * time.Second)
		go graphite.RunCarbonApi(&repman.Conf)
		log.WithField("apiport", repman.Conf.GraphiteCarbonApiPort).Info("Carbon server API started")
	}

	go repman.MountS3()

	//repman.InitRestic()
	log.Infof("repman.Conf.WorkingDir : %s", repman.Conf.WorkingDir)
	log.Infof("repman.Conf.ShareDir : %s", repman.Conf.ShareDir)

	// If there's an existing encryption key, decrypt the passwords

	for _, gl := range repman.ClusterList {
		repman.StartCluster(gl)
	}
	for _, cluster := range repman.Clusters {
		cluster.SetClusterList(repman.Clusters)

		cluster.SetCarbonLogger(repman.clog)
	}

	//	repman.currentCluster.SetCfgGroupDisplay(strClusters)
	if repman.Conf.ApiServ {
		go repman.apiserver()
	}
	// HTTP server should start after Cluster Init or may lead to various nil pointer if clients still requesting
	if repman.Conf.HttpServ {
		go repman.httpserver()
	}

	if _, err := os.Stat(conf.WorkingDir + "/cloud18.toml"); os.IsNotExist(err) {
		repman.Conf.ReadCloud18Config(repman.ViperConfig)
	}

	//this ticker make pull to github and check if there are new cluster pull
	ticker_GitPull := time.NewTicker(time.Duration(repman.Conf.GitMonitoringTicker) * time.Second)
	quit_GitPull := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker_GitPull.C:
				//to do it only when using github
				if repman.Conf.GitUrl != "" {
					repman.Conf.CloneConfigFromGit(repman.Conf.GitUrl, repman.Conf.GitUsername, repman.Conf.Secrets["git-acces-token"].Value, repman.Conf.WorkingDir)
					repman.Conf.PushConfigToGit(repman.Conf.GitUrl, repman.Conf.Secrets["git-acces-token"].Value, repman.Conf.GitUsername, repman.Conf.WorkingDir, repman.ClusterList)
					for _, cluster := range repman.Clusters {
						cluster.IsGitPull = true
					}

					//to check cloud18.toml for the first time
					if repman.cloud18CheckSum == nil && repman.Conf.Cloud18 {
						new_h := md5.New()
						repman.Conf.ReadCloud18Config(repman.ViperConfig)
						file, err := os.Open(repman.Conf.WorkingDir + "/cloud18.toml")
						if err != nil {
							if os.IsPermission(err) {
								log.Infof("File permission denied: %s", repman.Conf.WorkingDir+"/cloud18.toml")
							}
						} else {
							if _, err := io.Copy(new_h, file); err != nil {
								log.Infof("Error during computing cloud18.toml hash: %s", err)
							} else {
								repman.cloud18CheckSum = new_h
							}
						}
						defer file.Close()

					} else if repman.Conf.Cloud18 {
						//to check whether new parameters have been injected into the cloud18.toml config file
						file, err := os.Open(repman.Conf.WorkingDir + "/cloud18.toml")
						if err != nil {
							if os.IsPermission(err) {
								log.Infof("File permission denied: %s", repman.Conf.WorkingDir+"/cloud18.toml")
							}
						} else {
							new_h := md5.New()
							if _, err := io.Copy(new_h, file); err != nil {
								log.Infof("Error during computing cloud18.toml hash: %s", err)
							} else if !bytes.Equal(repman.cloud18CheckSum.Sum(nil), new_h.Sum(nil)) {
								repman.Conf.ReadCloud18Config(repman.ViperConfig)
								repman.cloud18CheckSum = new_h
							}
						}
						defer file.Close()

					}
				}
				if repman.Conf.Cloud18 {
					//then to check new file pulled in working dir
					files, err := os.ReadDir(repman.Conf.WorkingDir)
					if err != nil {
						log.Infof("No working directory %s ", repman.Conf.WorkingDir)
					}
					//check all dir of the datadir to check if a new cluster has been pull by git
					for _, f := range files {
						new_cluster_discover := true
						if f.IsDir() && f.Name() != "graphite" && f.Name() != "backups" && f.Name() != ".git" && f.Name() != "cloud18.toml" && !strings.Contains(f.Name(), ".json") && !strings.Contains(f.Name(), ".csv") {
							for name, _ := range repman.Clusters {
								if name == f.Name() {
									new_cluster_discover = false

								}
							}
						} else {
							new_cluster_discover = false
						}
						//find a dir that is not in the cluster list (and diff from backups and graphite)
						//so add the to new cluster to the repman
						if new_cluster_discover {
							//check if this there is a config file in the dir
							if _, err := os.Stat(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml"); !os.IsNotExist(err) {
								//init config, start the cluster and add it to the cluster list
								repman.ViperConfig.SetConfigName(f.Name())
								repman.ViperConfig.SetConfigFile(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml")
								err := repman.ViperConfig.MergeInConfig()
								if err != nil {
									log.Errorf("Config error in " + repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml" + ":" + err.Error())
								}
								repman.Confs[f.Name()] = repman.GetClusterConfig(repman.ViperConfig, repman.Conf.ImmuableFlagMap, repman.Conf.DynamicFlagMap, f.Name(), repman.Conf)
								repman.StartCluster(f.Name())
								repman.Clusters[f.Name()].IsGitPull = true
								for _, cluster := range repman.Clusters {
									cluster.SetClusterList(repman.Clusters)
								}
								repman.ClusterList = append(repman.ClusterList, f.Name())
							}
						}
					}
				}
			case <-quit_GitPull:
				ticker_GitPull.Stop()
				return
			}
		}
	}()

	//this ticker generate a new app access token, using app refresh token
	//then it generate a new PAT gitlab to preserved a valid PAT in order to clone/push/pull on the distant gitlab
	ticker_PAT := time.NewTicker(86400 * time.Second)
	quit_PAT := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker_PAT.C:
				//to do it only when auth to gitlab
				if repman.Conf.GitUrl != "" && repman.OAuthAccessToken != nil && repman.Conf.Cloud18 {
					//to obtain new app access token
					repman.OAuthAccessToken.AccessToken, repman.OAuthAccessToken.RefreshToken, err = githelper.RefreshAccessToken(repman.OAuthAccessToken.RefreshToken, repman.Conf.OAuthClientID, repman.Conf.GetDecryptedPassword("api-oauth-client-secret", repman.Conf.OAuthClientSecret), repman.Conf.LogGit)
					//to obtain a new PAT
					new_tok, _ := githelper.GetGitLabTokenOAuth(repman.OAuthAccessToken.AccessToken, repman.Conf.LogGit)

					//save the new PAT
					newSecret := repman.Conf.Secrets["git-acces-token"]
					newSecret.OldValue = newSecret.Value
					newSecret.Value = new_tok
					for _, cluster := range repman.Clusters {
						cluster.Conf.Secrets["git-acces-token"] = newSecret
					}
				}
			case <-quit_PAT:
				ticker_PAT.Stop()
				return
			}
		}
	}()

	//	ticker := time.NewTicker(interval * time.Duration(repman.Conf.MonitoringTicker))
	repman.isStarted = true
	sigs := make(chan os.Signal, 1)
	// catch all signals since not explicitly listing
	//	signal.Notify(sigs)
	signal.Notify(sigs, os.Interrupt)
	// method invoked upon seeing signal
	go func() {
		s := <-sigs
		log.Printf("RECEIVED SIGNAL: %s", s)
		repman.UnMountS3()
		for _, cl := range repman.Clusters {
			cl.Stop()
		}

		repman.exit = true

	}()

	for repman.exit == false {
		if repman.Conf.Arbitration {
			repman.Heartbeat()
		}
		if repman.Conf.Enterprise {
			//			agents = svc.GetNodes()
		}
		time.Sleep(time.Second * time.Duration(repman.Conf.MonitoringTicker))
	}
	if repman.exitMsg != "" {
		log.Println(repman.exitMsg)
	}
	fmt.Println("Cleanup before leaving")
	if repman.CpuProfile != "" {
		pprof.StopCPUProfile()
	}
	repman.Stop()
	os.Exit(1)
	return nil

}

func (repman *ReplicationManager) StartCluster(clusterName string) (*cluster.Cluster, error) {

	repman.currentCluster = new(cluster.Cluster)

	myClusterConf := repman.Confs[clusterName]
	if myClusterConf.MonitorAddress == "localhost" {
		myClusterConf.MonitorAddress = repman.resolveHostIp()
	}
	if myClusterConf.FailMode == "manual" {
		myClusterConf.Interactive = true
	} else {
		myClusterConf.Interactive = false
	}
	if myClusterConf.BaseDir != "system" {
		myClusterConf.ShareDir = myClusterConf.BaseDir + "/share"
		myClusterConf.WorkingDir = myClusterConf.BaseDir + "/data"
	}

	myClusterConf.ImmuableFlagMap = repman.ImmuableFlagMaps[clusterName]
	myClusterConf.DynamicFlagMap = repman.DynamicFlagMaps[clusterName]
	myClusterConf.DefaultFlagMap = repman.DefaultFlagMap
	log.Infof("Starting cluster: %s workingdir %s", clusterName, myClusterConf.WorkingDir)

	repman.VersionConfs[clusterName].ConfInit = myClusterConf
	//log.Infof("Default config for %s workingdir:\n %v", clusterName, myClusterConf.DefaultFlagMap)

	repman.currentCluster.Init(repman.VersionConfs[clusterName], clusterName, &repman.tlog, &repman.Logs, repman.termlength, repman.UUID, repman.Version, repman.Hostname)
	repman.Clusters[clusterName] = repman.currentCluster
	repman.currentCluster.SetCertificate(repman.OpenSVC)
	go repman.currentCluster.Run()
	return repman.currentCluster, nil
}

func (repman *ReplicationManager) HeartbeatPeerSplitBrain(peer string, bcksplitbrain bool) bool {
	timeout := time.Duration(time.Duration(repman.Conf.MonitoringTicker) * time.Second * 4)
	/*	Host, _ := misc.SplitHostPort(peer)
		ha, err := net.LookupHost(Host)
		if err != nil {
			log.Errorf("Heartbeat: Resolv %s DNS err: %s", Host, err)
		} else {
			log.Errorf("Heartbeat: Resolv %s DNS say: %s", Host, ha[0])
		}
	*/

	url := "http://" + peer + "/api/heartbeat"
	client := &http.Client{
		Timeout: timeout,
	}
	if repman.Conf.LogHeartbeat {
		log.Debugf("Heartbeat: Sending peer request to node %s", peer)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Error building HTTP request: %s", err)
		}
		return true
	}
	resp, err := client.Do(req)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Could not reach peer node, might be down or incorrect address")
		}
		return true
	}
	defer resp.Body.Close()
	monjson, err := io.ReadAll(resp.Body)
	if err != nil {
		if bcksplitbrain == false {
			log.Debugf("Could not read body from peer response")
		}
		return true
	}
	if repman.Conf.LogHeartbeat {
		log.Debugf("splitbrain http call result: %s ", monjson)
	}
	// Use json.Decode for reading streams of JSON data
	var h Heartbeat
	if err := json.Unmarshal(monjson, &h); err != nil {
		if repman.Conf.LogHeartbeat {
			log.Debugf("Could not unmarshal JSON from peer response %s", err)
		}
		return true
	} else {

		if repman.Conf.LogHeartbeat {
			log.Debugf("RETURN: %v", h)
		}

		if repman.Conf.LogHeartbeat {
			log.Infof("No peer split brain setting status to %s", repman.Status)
		}

	}

	return false
}

func (repman *ReplicationManager) Heartbeat() {
	if cfgGroup == "arbitrator" {
		log.Debugf("Arbitrator cannot send heartbeat to itself. Exiting")
		return
	}

	var peerList []string
	// try to found an active peer replication-manager
	if repman.Conf.ArbitrationPeerHosts != "" {
		peerList = strings.Split(repman.Conf.ArbitrationPeerHosts, ",")
	} else {
		log.Debugf("Arbitration peer not specified. Disabling arbitration")
		repman.Conf.Arbitration = false
		return
	}

	bcksplitbrain := repman.SplitBrain

	for _, peer := range peerList {
		repman.Lock()
		repman.SplitBrain = repman.HeartbeatPeerSplitBrain(peer, bcksplitbrain)
		repman.Unlock()
		if repman.Conf.LogHeartbeat {
			log.Infof("SplitBrain set to %t on peer %s", repman.SplitBrain, peer)
		}
	} //end check all peers

	// propagate SplitBrain state to clusters after peer negotiation
	for _, cl := range repman.Clusters {
		cl.IsSplitBrain = repman.SplitBrain

		if repman.Conf.LogHeartbeat {
			log.Infof("SplitBrain set to %t on cluster %s", repman.SplitBrain, cl.Name)
		}
	}
}

func (repman *ReplicationManager) resolveHostIp() string {
	netInterfaceAddresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, netInterfaceAddress := range netInterfaceAddresses {
		networkIp, ok := netInterfaceAddress.(*net.IPNet)
		if ok && !networkIp.IP.IsLoopback() && networkIp.IP.To4() != nil {
			ip := networkIp.IP.String()
			return ip
		}
	}
	return ""
}

func (repman *ReplicationManager) Stop() {

	//termbox.Close()
	fmt.Println("Prof profile into file: " + repman.MemProfile)
	if repman.MemProfile != "" {
		f, err := os.Create(repman.MemProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}

}

func (repman *ReplicationManager) DownloadFile(url string, file string) error {
	client := http.Client{
		Timeout: 3 * time.Second,
	}
	response, err := client.Get(url)
	if err != nil {
		log.Errorf("Get File %s to %s : %s", url, file, err)
		return err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		log.Errorf("Read File %s to %s : %s", url, file, err)
		return err
	}

	err = os.WriteFile(file, contents, 0644)
	if err != nil {
		log.Errorf("Write File %s to %s : %s", url, file, err)
		return err
	}
	return nil
}

func (repman *ReplicationManager) InitServicePlans() error {
	var err error
	if !repman.Conf.Test {

		if _, err := os.Stat(repman.Conf.WorkingDir + "/serviceplan.csv"); os.IsNotExist(err) {
			misc.CopyFile(repman.Conf.ShareDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.csv")
		}
		err = misc.ConvertCSVtoJSON(repman.Conf.WorkingDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.json", ",", repman.Conf.Test)
	} else {
		err = repman.DownloadFile(repman.Conf.ProvServicePlanRegistry, repman.Conf.WorkingDir+"/serviceplan.csv")
		if err != nil {
			log.Errorf("GetServicePlans download csv  %s", err)
			// copy from share if not downloadable
			if _, err := os.Stat(repman.Conf.WorkingDir + "/serviceplan.csv"); os.IsNotExist(err) {
				misc.CopyFile(repman.Conf.ShareDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.csv")
			}
		}
		err = misc.ConvertCSVtoJSON(repman.Conf.WorkingDir+"/serviceplan.csv", repman.Conf.WorkingDir+"/serviceplan.json", ",", true)
		// copy from share if not downloadable

	}
	if err != nil {
		log.Errorf("GetServicePlans ConvertCSVtoJSON %s", err)
		return err
	}

	file, err := os.ReadFile(repman.Conf.WorkingDir + "/serviceplan.json")
	if err != nil {
		log.Errorf("failed opening file because: %s", err.Error())
		return err
	}

	type Message struct {
		Rows []config.ServicePlan `json:"rows"`
	}
	var m Message
	err = json.Unmarshal([]byte(file), &m.Rows)
	if err != nil {
		log.Errorf("GetServicePlans  %s", err)
		return err
	}
	repman.ServicePlans = m.Rows

	return nil
}

type GrantSorter []config.Grant

func (a GrantSorter) Len() int           { return len(a) }
func (a GrantSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a GrantSorter) Less(i, j int) bool { return a[i].Grant < a[j].Grant }

func (repman *ReplicationManager) InitGrants() error {

	acls := []config.Grant{}

	for _, value := range repman.Conf.GetGrantType() {
		var acl config.Grant
		acl.Grant = value
		acls = append(acls, acl)
	}
	repman.ServiceAcl = acls
	sort.Sort(GrantSorter(repman.ServiceAcl))
	return nil
}

func IsDefault(p string, v *viper.Viper) bool {

	return false
}
