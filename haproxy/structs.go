// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"sync"
)

/*
  A Route is structured set of Haproxy frontends, backends and servers. The Route provides a convenient
  and higher level way of creating and managing this structure. You could create this structure by
  hand with separate API calls, but this is faster and easier in 9 out of 10 cases.

  The structure of a route is as follows:

                              -> [srv a] -> sock -> [fe a: be a] -> [*srv] -> host:port
                            /
    ->[fe (fltr)(qts) : be]-
                            \
                              -> [srv b] -> sock -> [fe b: be b] -> [*srv] -> host:port

    fe = frontend
    be = backend
    srv = server
    fltr = filter
    qts = quotas

  The above example has two services, a and b, but a route can have many services. The start of the
  route (the first frontend) has filters and quotas that influence the way traffic flows in a route,
  i.e. to which services the traffic goes.

  All items in a route map to actual Haproxy types from the vamp-loadbalancer/haproxy package.
*/
type Route struct {
	Name      string     `json:"name" binding:"required" valid:"routeName"`
	Port      int        `json:"port" binding:"required"`
	Protocol  string     `json:"protocol" binding:"required"`
	HttpQuota Quota      `json:"httpQuota"`
	TcpQuota  Quota      `json:"tcpQuota"`
	Filters   []*Filter  `json:"filters"`
	Services  []*Service `json:"services"`
}

type Filter struct {
	Name        string `json:"name" binding:"required" valid:"filterName"`
	Condition   string `json:"condition" binding:"required"`
	Destination string `json:"destination" binding:"required"`
	Negate      bool   `json:"negate,omitempty"`
}

type Quota struct {
	SampleWindow string `json:"sampleWindow,omitempty" binding:"required"`
	Rate         int    `json:"rate,omitempty" binding:"required"`
	ExpiryTime   string `json:"expiryTime,omitempty" binding:"required"`
}

type Service struct {
	Name    string    `json:"name" binding:"required"`
	Weight  int       `json:"weight" binding:"required"`
	Servers []*Server `json:"servers"`
}

type Server struct {
	Name string `json:"name" binding:"required"`
	Host string `json:"host" binding:"required"`
	Port int    `json:"port" binding:"required"`
}

type ServerDetail struct {
	Name          string `json:"name" binding:"required"`
	Host          string `json:"host" binding:"required"`
	Port          int    `json:"port" binding:"required"`
	UnixSock      string `json:"unixSock" valid:"socketPath"`
	Weight        int    `json:"weight" binding:"required"`
	MaxConn       int    `json:"maxconn"`
	Check         bool   `json:"check"`
	CheckInterval int    `json:"checkInterval"`
}

type Runtime struct {
	Binary   string
	SockFile string
}

// Main configuration object for load balancers. This contains all variables and is passed to
// the templating engine.
type Config struct {
	Frontends     []*Frontend   `json:"frontends" binding:"required"`
	Backends      []*Backend    `json:"backends" binding:"required"`
	Routes        []Route       `json:"routes" binding:"required"`
	PidFile       string        `json:"-"`
	SockFile      string        `json:"-"`
	Mutex         *sync.RWMutex `json:"-"`
	TemplateFile  string        `json:"-"`
	ConfigFile    string        `json:"-"`
	JsonFile      string        `json:"-"`
	WorkingDir    string        `json:"-"`
	ErrorPagesDir string        `json:"-"`
}

// Defines a single haproxy "backend".
type Backend struct {
	Name      string          `json:"name" binding:"required"`
	Mode      string          `json:"mode" binding:"required"`
	Servers   []*ServerDetail `json:"servers" binding:"required"`
	Options   ProxyOptions    `json:"options"`
	ProxyMode bool            `json:"proxyMode" binding:"required"`
}

// Defines a single haproxy "frontend".
type Frontend struct {
	Name           string       `json:"name" binding:"required"`
	Mode           string       `json:"mode" binding:"required"`
	BindPort       int          `json:"bindPort"`
	BindIp         string       `json:"bindIp"`
	UnixSock       string       `json:"unixSock"`
	SockProtocol   string       `json:"sockProtocol"`
	Options        ProxyOptions `json:"options"`
	DefaultBackend string       `json:"defaultBackend" binding:"required"`
	Filters        []*Filter    `json:"filters,omitempty"`
	HttpQuota      Quota        `json:"httpQuota,omitempty"`
	TcpQuota       Quota        `json:"tcpQuota,omitempty"`
}

type ProxyOptions struct {
	AbortOnClose    bool `json:"abortOnClose"`
	AllBackups      bool `json:"allBackups"`
	CheckCache      bool `json:"checkCache"`
	ForwardFor      bool `json:"forwardFor"`
	HttpClose       bool `json:"httpClose"`
	HttpCheck       bool `json:"httpCheck"`
	SslHelloCheck   bool `json:"sslHelloCheck"`
	TcpKeepAlive    bool `json:"tcpKeepAlive"`
	TcpLog          bool `json:"tcpLog"`
	TcpSmartAccept  bool `json:"tcpSmartAccept"`
	TcpSmartConnect bool `json:"tcpSmartConnect"`
}

// Struct to hold the output from the /stats endpoint
type Stats struct {
	Pxname         string `json:"pxname"`
	Svname         string `json:"svname"`
	Qcur           string `json:"qcur"`
	Qmax           string `json:"qmax"`
	Scur           string `json:"scur"`
	Smax           string `json:"smax"`
	Slim           string `json:"slim"`
	Stot           string `json:"stot"`
	Bin            string `json:"bin"`
	Bout           string `json:"bout"`
	Dreq           string `json:"dreq"`
	Dresp          string `json:"dresp"`
	Ereq           string `json:"ereq"`
	Econ           string `json:"econ"`
	Eresp          string `json:"eresp"`
	Wretr          string `json:"wretr"`
	Wredis         string `json:"wredis"`
	Status         string `json:"status"`
	Weight         string `json:"weight"`
	Act            string `json:"act"`
	Bck            string `json:"bck"`
	Chkfail        string `json:"chkfail"`
	Chkdown        string `json:"chkdown"`
	Lastchg        string `json:"lastchg"`
	Downtime       string `json:"downtime"`
	Qlimit         string `json:"qlimit"`
	Pid            string `json:"pid"`
	Iid            string `json:"iid"`
	Sid            string `json:"sid"`
	Throttle       string `json:"throttle"`
	Lbtot          string `json:"lbtot"`
	Tracked        string `json:"tracked"`
	_Type          string `json:"type"`
	Rate           string `json:"rate"`
	Rate_lim       string `json:"rate_lim"`
	Rate_max       string `json:"rate_max"`
	Check_status   string `json:"check_status"`
	Check_code     string `json:"check_code"`
	Check_duration string `json:"check_duration"`
	Hrsp_1xx       string `json:"hrsp_1xx"`
	Hrsp_2xx       string `json:"hrsp_2xx"`
	Hrsp_3xx       string `json:"hrsp_3xx"`
	Hrsp_4xx       string `json:"hrsp_4xx"`
	Hrsp_5xx       string `json:"hrsp_5xx"`
	Hrsp_other     string `json:"hrsp_other"`
	Hanafail       string `json:"hanafail"`
	Req_rate       string `json:"req_rate"`
	Req_rate_max   string `json:"req_rate_max"`
	Req_tot        string `json:"req_tot"`
	Cli_abrt       string `json:"cli_abrt"`
	Srv_abrt       string `json:"srv_abrt"`
	Comp_in        string `json:"comp_in"`
	Comp_out       string `json:"comp_out"`
	Comp_byp       string `json:"comp_byp"`
	Comp_rsp       string `json:"comp_rsp"`
	Lastsess       string `json:"lastsess"`
	Last_chk       string `json:"last_chk"`
	Last_agt       string `json:"last_agt"`
	Qtime          string `json:"qtime"`
	Ctime          string `json:"ctime"`
	Rtime          string `json:"rtime"`
	Ttime          string `json:"ttime"`
}

// struct to hold the output from the /info endpoint
type Info struct {
	Name                        string `json:"Name"`
	Version                     string `json:"Version"`
	Release_date                string `json:"Release_date"`
	Nbproc                      string `json:"Nbproc"`
	Process_num                 string `json:"Process_num"`
	Pid                         string `json:"Pid"`
	Uptime                      string `json:"Uptime"`
	Uptime_sec                  string `json:"Uptime_sec"`
	Memmax_MB                   string `json:"Memmax_MB"`
	Ulimitn                     string `json:"Ulimit-n"`
	Maxsock                     string `json:"Maxsock"`
	Maxconn                     string `json:"Maxconn"`
	Hard_maxconn                string `json:"Hard_maxconn"`
	CurrConns                   string `json:"CurrConns"`
	CumConns                    string `json:"CumConns"`
	CumReq                      string `json:"CumReq"`
	MaxSslConns                 string `json:"MaxSslConns"`
	CurrSslConns                string `json:"CurrSslConns"`
	CumSslConns                 string `json:"CumSslConns"`
	Maxpipes                    string `json:"Maxpipes"`
	PipesUsed                   string `json:"PipesUsed"`
	PipesFree                   string `json:"PipesFree"`
	ConnRate                    string `json:"ConnRate"`
	ConnRateLimit               string `json:"ConnRateLimit"`
	MaxConnRate                 string `json:"MaxConnRate"`
	SessRate                    string `json:"SessRate"`
	SessRateLimit               string `json:"SessRateLimit"`
	MaxSessRate                 string `json:"MaxSessRate"`
	SslRate                     string `json:"SslRate"`
	SslRateLimit                string `json:"SslRateLimit"`
	MaxSslRate                  string `json:"MaxSslRate"`
	SslFrontendKeyRate          string `json:"SslFrontendKeyRate"`
	SslFrontendMaxKeyRate       string `json:"SslFrontendMaxKeyRate"`
	SslFrontendSessionReuse_pct string `json:"SslFrontendSessionReuse_pct"`
	SslBackendKeyRate           string `json:"SslBackendKeyRate"`
	SslBackendMaxKeyRate        string `json:"SslBackendMaxKeyRate"`
	SslCacheLookups             string `json:"SslCacheLookups"`
	SslCacheMisses              string `json:"SslCacheMisses"`
	CompressBpsIn               string `json:"CompressBpsIn"`
	CompressBpsOut              string `json:"CompressBpsOut"`
	CompressBpsRateLim          string `json:"CompressBpsRateLim"`
	ZlibMemUsage                string `json:"ZlibMemUsage"`
	MaxZlibMemUsage             string `json:"MaxZlibMemUsage"`
	Tasks                       string `json:"Tasks"`
	Run_queue                   string `json:"Run_queue"`
	Idle_pct                    string `json:"Idle_pct"`
	node                        string `json:"node"`
	description                 string `json:"description"`
}

// custom error that allows us to define the HTTP return code that should be used in different
// error situations
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	return e.Err.Error()
}
