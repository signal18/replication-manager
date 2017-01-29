// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package config

type Config struct {
	User                     string
	Hosts                    string
	Socket                   string
	RplUser                  string
	Interactive              bool
	Verbose                  bool
	PreScript                string `mapstructure:"pre-failover-script"`
	PostScript               string `mapstructure:"post-failover-script"`
	MaxDelay                 int64
	GtidCheck                bool
	PrefMaster               string
	IgnoreSrv                string `mapstructure:"ignore-servers"`
	WaitKill                 int64  `mapstructure:"wait-kill"`
	WaitTrx                  int64  `mapstructure:"wait-trx"`
	WaitWrite                int    `mapstructure:"wait-write-query"`
	ReadOnly                 bool
	MaxFail                  int   `mapstructure:"failcount"`
	FailResetTime            int64 `mapstructure:"failcount-reset-time"`
	Autorejoin               bool
	LogFile                  string
	MonitoringTicker         int64 `mapstructure:"monitoring-ticker"`
	Timeout                  int   `mapstructure:"connect-timeout"`
	FailLimit                int   `mapstructure:"failover-limit"`
	FailTime                 int64 `mapstructure:"failover-time-limit"`
	CheckType                string
	CheckReplFilter          bool
	CheckBinFilter           bool
	RplChecks                bool
	MasterConn               string `mapstructure:"master-connection"`
	MultiMaster              bool
	Spider                   bool
	BindAddr                 string `mapstructure:"http-bind-address"`
	HttpPort                 string `mapstructure:"http-port"`
	HttpServ                 bool   `mapstructure:"http-server"`
	HttpRoot                 string `mapstructure:"http-root"`
	HttpAuth                 bool   `mapstructure:"http-auth"`
	HttpBootstrapButton      bool   `mapstructure:"http-bootstrap-button"`
	SessionLifeTime          int    `mapstructure:"http-session-lifetime"`
	Daemon                   bool
	MailFrom                 string `mapstructure:"mail-from"`
	MailTo                   string `mapstructure:"mail-to"`
	MailSMTPAddr             string `mapstructure:"mail-smtp-addr"`
	MasterConnectRetry       int    `mapstructure:"master-connect-retry"`
	FailSync                 bool   `mapstructure:"failover-at-sync"`
	FailEventScheduler       bool   `mapstructure:"failover-event-scheduler"`
	FailEventStatus          bool   `mapstructure:"failover-event-status"`
	Heartbeat                bool   `mapstructure:"heartbeat-table"`
	MxsOn                    bool   `mapstructure:"maxscale"`
	MxsHost                  string `mapstructure:"maxscale-host"`
	MxsPort                  string `mapstructure:"maxscale-port"`
	MxsUser                  string `mapstructure:"maxscale-user"`
	MxsPass                  string `mapstructure:"maxscale-pass"`
	HaproxyOn                bool   `mapstructure:"haproxy"`
	HaproxyWritePort         int    `mapstructure:"haproxy-write-port"`
	HaproxyReadPort          int    `mapstructure:"haproxy-read-port"`
	HaproxyStatPort          int    `mapstructure:"haproxy-stat-port"`
	HaproxyWriteBindIp       string `mapstructure:"haproxy-ip-write-bind"`
	HaproxyReadBindIp        string `mapstructure:"haproxy-ip-read-bind"`
	HaproxyBinaryPath        string `mapstructure:"haproxy-binary-path"`
	KeyPath                  string
	LogLevel                 int `mapstructure:"log-level"`
	Test                     bool
	GraphiteMetrics          bool   `mapstructure:"graphite-metrics"`
	GraphiteEmbedded         bool   `mapstructure:"graphite-embedded"`
	GraphiteCarbonHost       string `mapstructure:"graphite-carbon-host"`
	GraphiteCarbonPort       int    `mapstructure:"graphite-carbon-port"`
	GraphiteCarbonApiPort    int    `mapstructure:"graphite-carbon-api-port"`
	GraphiteCarbonServerPort int    `mapstructure:"graphite-carbon-server-port"`
	GraphiteCarbonLinkPort   int    `mapstructure:"graphite-carbon-link-port"`
	GraphiteCarbonPicklePort int    `mapstructure:"graphite-carbon-pickle-port"`
	GraphiteCarbonPprofPort  int    `mapstructure:"graphite-carbon-pprof-port"`
	SysbenchBinaryPath       string `mapstructure:"sysbench-binary-path"`
	SysbenchTime             int    `mapstructure:"sysbench-time"`
	SysbenchThreads          int    `mapstructure:"sysbench-threads"`
}
