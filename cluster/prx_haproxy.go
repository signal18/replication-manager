// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/spf13/pflag"
)

// haproxyDynDefaultsSection is the name of the defaults section that
// share/haproxy_config.template and the OpenSVC haproxy.cfg module both
// declare, so that runtime-created backends (AddBackend) have a named
// defaults proxy to inherit from -- the HAProxy runtime API rejects "add
// backend" without "from <defaults>".
const haproxyDynDefaultsSection = "dyn_defaults"

// haproxyMinDynamicBackendMajor/Minor is the first HAProxy release where
// "publish backend" (and therefore the AddBackend/AddServer self-heal path)
// is available.
const haproxyMinDynamicBackendMajor = 3
const haproxyMinDynamicBackendMinor = 4

type HaproxyProxy struct {
	Proxy
}

func NewHaproxyProxy(placement int, cluster *Cluster, proxyHost string) *HaproxyProxy {
	conf := cluster.Conf
	prx := new(HaproxyProxy)
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSHaProxyPartitions, conf.HaproxyHostsIPV6, conf.HaproxyJanitorWeights)
	prx.Type = config.ConstProxyHaproxy
	prx.Port = strconv.Itoa(conf.HaproxyAPIPort)
	prx.ReadPort = conf.HaproxyReadPort
	prx.WritePort = conf.HaproxyWritePort
	prx.ReadWritePort = conf.HaproxyWritePort
	prx.Name = proxyHost
	prx.Host = proxyHost
	if conf.ProvNetCNI {
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}
	prx.User = conf.HaproxyUser
	prx.Pass = cluster.Conf.GetDecryptedValue("haproxy-password")

	return prx
}

func (proxy *HaproxyProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper to use HAProxy on same host")
	flags.StringVar(&conf.HaproxyMode, "haproxy-mode", "runtimeapi", "HAProxy mode [standby|runtimeapi|dataplaneapi]")
	flags.BoolVar(&conf.HaproxyDebug, "haproxy-debug", true, "Extra info on monitoring backend")
	flags.IntVar(&conf.HaproxyLogLevel, "log-level-haproxy", 1, "Log level for debug")
	flags.StringVar(&conf.HaproxyUser, "haproxy-user", "admin", "HAProxy API user")
	flags.StringVar(&conf.HaproxyPassword, "haproxy-password", "admin", "HAProxy API password")
	flags.StringVar(&conf.HaproxyHosts, "haproxy-servers", "127.0.0.1", "HAProxy hosts")
	flags.StringVar(&conf.HaproxyJanitorWeights, "haproxy-janitor-weights", "100", "Weight of each HAProxy host inside janitor proxy")
	flags.IntVar(&conf.HaproxyAPIPort, "haproxy-api-port", 1999, "HAProxy runtime api port")
	flags.IntVar(&conf.HaproxyWritePort, "haproxy-write-port", 3306, "HAProxy read-write port to leader")
	flags.IntVar(&conf.HaproxyReadPort, "haproxy-read-port", 3307, "HAProxy load balancer read port to all nodes")
	flags.IntVar(&conf.HaproxyStatPort, "haproxy-stat-port", 1988, "HAProxy statistics port")
	flags.StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "HAProxy binary location")
	flags.StringVar(&conf.HaproxyReadBindIp, "haproxy-ip-read-bind", "0.0.0.0", "HAProxy input bind address for read")
	flags.StringVar(&conf.HaproxyWriteBindIp, "haproxy-ip-write-bind", "0.0.0.0", "HAProxy input bind address for write")
	flags.StringVar(&conf.HaproxyAPIReadBackend, "haproxy-api-read-backend", "service_read", "HAProxy API backend name used for read")
	flags.StringVar(&conf.HaproxyAPIWriteBackend, "haproxy-api-write-backend", "service_write", "HAProxy API backend name used for write")
	flags.StringVar(&conf.HaproxyHostsIPV6, "haproxy-servers-ipv6", "", "HAProxy IPv6 bind address ")
	flags.BoolVar(&conf.HaproxyRuntimeDynamicBackends, "haproxy-runtime-dynamic-backends", false, "Self-heal missing HAProxy runtime backends/servers via the runtime API (requires HAProxy 3.4+ and haproxy-mode=runtimeapi)")
}

func (proxy *HaproxyProxy) Init() {
	cluster := proxy.ClusterGroup
	haproxydatadir := proxy.Datadir + "/var"

	if _, err := os.Stat(haproxydatadir); os.IsNotExist(err) {
		proxy.GetProxyConfig()
		os.Symlink(proxy.Datadir+"/init/data", haproxydatadir)
	}
	//haproxysockFile := "haproxy.stats.sock"

	haproxytemplateFile := "haproxy_config.template"
	haproxyconfigFile := "haproxy.cfg"
	haproxyjsonFile := "vamp_router.json"
	haproxypidFile := "haproxy.pid"
	haproxyerrorPagesDir := "error_pages"
	//	haproxymaxWorkDirSize := 50 // this value is based on (max socket path size - md5 hash length - pre and postfixes)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(proxy.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	haConfig := haproxy.Config{
		TemplateFile:  filepath.Join(cluster.Conf.ShareDir, haproxytemplateFile),
		ConfigFile:    filepath.Join(haproxydatadir, "/", haproxyconfigFile),
		JsonFile:      filepath.Join(haproxydatadir, "/", haproxyjsonFile),
		ErrorPagesDir: filepath.Join(haproxydatadir, "/", haproxyerrorPagesDir, "/"),
		PidFile:       filepath.Join(haproxydatadir, "/", haproxypidFile),
		//	SockFile:      filepath.Join(haproxydatadir, "/", haproxysockFile),
		SockFile:   "/tmp/haproxy" + proxy.Id + ".sock",
		ApiPort:    proxy.Port,
		StatPort:   strconv.Itoa(proxy.ClusterGroup.Conf.HaproxyStatPort),
		Host:       proxy.Host,
		WorkingDir: filepath.Join(haproxydatadir + "/"),
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy loading haproxy config at %s", haproxydatadir)
	err := haConfig.GetConfigFromDisk()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy did not find an haproxy config...initializing new config")
		haConfig.InitializeConfig()
	}
	few := haproxy.Frontend{Name: "my_write_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIWriteBackend, BindPort: cluster.Conf.HaproxyWritePort, BindIp: cluster.Conf.HaproxyWriteBindIp}
	if err := haConfig.AddFrontend(&few); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add frontend write ")
	} else {
		if err := haConfig.AddFrontend(&few); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy should return nil on already existing frontend")
		}

	}
	if result, _ := haConfig.GetFrontend("my_write_frontend"); result.Name != "my_write_frontend" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add frontend write")
	}
	bew := haproxy.Backend{Name: cluster.Conf.HaproxyAPIWriteBackend, Mode: "tcp"}
	haConfig.AddBackend(&bew)

	fer := haproxy.Frontend{Name: "my_read_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIReadBackend, BindPort: cluster.Conf.HaproxyReadPort, BindIp: cluster.Conf.HaproxyReadBindIp}
	if err := haConfig.AddFrontend(&fer); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add frontend read")
	} else {
		if err := haConfig.AddFrontend(&fer); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy should return nil on already existing frontend")
		}
	}
	if result, _ := haConfig.GetFrontend("my_read_frontend"); result.Name != "my_read_frontend" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to get frontend")
	}
	/* End add front end */

	ber := haproxy.Backend{Name: cluster.Conf.HaproxyAPIReadBackend, Mode: "tcp"}
	if err := haConfig.AddBackend(&ber); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add backend for "+cluster.Conf.HaproxyAPIReadBackend)
	}

	//var checksum64 string
	//	crcHost := crc64.MakeTable(crc64.ECMA)
	for _, server := range cluster.Servers {
		if !server.IsMaintenance {
			p, _ := strconv.Atoi(server.Port)
			//		checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(server.Host+":"+server.Port), crcHost))
			s := haproxy.ServerDetail{Name: server.Id, Host: server.Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
			if err := haConfig.AddServer(cluster.Conf.HaproxyAPIReadBackend, &s); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server in HAProxy for "+cluster.Conf.HaproxyAPIReadBackend)
			}
		}
	}

	err = haConfig.Render()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not create haproxy config %s", err)
	}
	if cluster.Conf.HaproxyMode == "standby" {
		if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set pid %s", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy reload config on pid %s", haConfig.PidFile)
		}

		err = haRuntime.Reload(&haConfig)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Can't reload haproxy config %s", err)
		}
	}
}

func (proxy *HaproxyProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	stagingsrv := cluster.StagingServer
	if stagingsrv == nil {
		stagingsrv = cluster.SetStandaloneAsStaging()
	}
	// if proxy.ClusterGroup.Conf.HaproxyStatHttp {

	/*
		url := "http://" + proxy.Host + ":" + proxy.Port + "/stats;csv"
		client := &http.Client{
			Timeout: time.Duration(2 * time.Second),
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		defer resp.Body.Close()
		reader := csv.NewReader(resp.Body)

	*/
	//tcpAddr, err := net.ResolveTCPAddr("tcp4", proxy.Host+":"+proxy.Port)
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModHAProxy,config.LvlErr, "haproxy entering  refresh: ")

	haproxydatadir := proxy.Datadir + "/var"
	haproxysockFile := "haproxy.stats.sock"

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(haproxydatadir, "/", haproxysockFile),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	backend_ip_host := make(map[string]string)
	backend_svname_host := make(map[string]string) // svname → FQDN for DNS failure fallback
	if proxy.HasDNS() {
		// When using FQDN map server state host->IP to locate in show stats where it's only IPs
		cmd := "show servers state"

		showleaderstate, err := haRuntime.ApiCmd(cmd)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}

		// API return a first row with return code make it as comment
		showleaderstate = "# " + showleaderstate

		// API return space sparator conveting to csv
		showleaderstate = strings.ReplaceAll(showleaderstate, " ", ",")

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "haproxy show servers state response :%s", showleaderstate)

		showleaderstatereader := io.NopCloser(bytes.NewReader([]byte(showleaderstate)))

		defer showleaderstatereader.Close()
		reader := csv.NewReader(showleaderstatereader)
		reader.Comment = '#'
		for {
			line, error := reader.Read()
			if error == io.EOF {
				break
			} else if error != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not read csv from haproxy response")
				return err
			}
			if len(line) > 17 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy adding IP map %s %s", line[4], line[17])
				backend_ip_host[line[4]] = line[17]
				if line[3] != "" && line[17] != "" {
					backend_svname_host[line[3]] = line[17]
				}
			}
		}

	}

	if proxy.Version == "" {
		vstring, err := haRuntime.GetVersion()
		if err == nil {
			if vstring != "" {
				proxy.SetVersion(vstring)
			}
		}
	}

	result, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy show stat result: %s", result)

	r := io.NopCloser(bytes.NewReader([]byte(result)))
	defer r.Close()
	reader := csv.NewReader(r)

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil
	foundMasterInStat := false
	masterReadFound := false
	masterReadSvname := ""
	masterReadStatus := ""
	// Dynamic-backend self-heal bookkeeping (see selfHealDynamicBackends):
	// whether each backend's BACKEND summary row was present, whether the
	// write backend's "leader" row was present, and the read-backend
	// svnames seen (so missing/stale cluster servers can be detected).
	// readSvnamesUnhealthy separately flags a seen svname that's stuck at a
	// status/address nothing else would ever recover (e.g. "no check" from
	// a rejected EnableHealth, or a stale address) -- the read loop retries
	// those instead of treating presence as done. Kept as its own map,
	// not folded into readSvnamesSeen: the prune loop below still needs
	// every seen svname regardless of health, to tell "unhealthy but still
	// a real server" (retry in place) from "stale" (drain and delete).
	sawWriteBackend := false
	sawReadBackend := false
	sawWriteLeader := false
	readSvnamesSeen := make(map[string]bool)
	readSvnamesUnhealthy := make(map[string]bool)
	for {
		line, error := reader.Read()
		if error == io.EOF {
			break
		} else if error != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not read csv from haproxy response")
			return err
		}
		if len(line) < 73 {
			cluster.SetState("WARN0078", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0078"], err), ErrFrom: "MON"})
			return errors.New(clusterError["WARN0078"])
		}
		// A BACKEND summary row proves the backend exists even with zero
		// servers -- capture that before the skip below discards the row.
		// sawWriteBackend/sawReadBackend also require it to be published
		// (not "UP (UNPUB)"), same as addDynamicBackend's own
		// backendPublishedAtRuntime check: otherwise a row left behind by
		// "add backend" with "publish backend" never taking effect would
		// look "already there" forever and never get retried.
		if line[1] == "BACKEND" {
			// Exact match, not Contains: HaproxyAPIWriteBackend/ReadBackend
			// are configurable and one can be a substring of the other
			// (e.g. "service" vs "service_read"), which would let the read
			// backend's summary row falsely satisfy the write backend
			// check (or vice versa) and mask a genuinely missing backend.
			if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIWriteBackend) {
				sawWriteBackend = backendRowPublished(line[17])
			}
			if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIReadBackend) {
				sawReadBackend = backendRowPublished(line[17])
			}
		}
		// Skip FRONTEND/BACKEND summary lines — only process actual server entries
		if line[1] == "FRONTEND" || line[1] == "BACKEND" {
			continue
		}
		// Exact match, not Contains: see the BACKEND-summary match above --
		// self-heal bookkeeping (sawWriteLeader here, readSvnamesSeen below)
		// now depends on this guard too, so a substring collision between
		// the configured write/read backend names would misclassify rows
		// and cause self-heal to skip a re-add or track the wrong server.
		if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIWriteBackend) {
			if line[1] == "leader" {
				// Require the row to be usable ("UP"-prefixed), not merely
				// present: self-heal only ever (re)creates "leader" while a
				// non-maintenance master is known, so "MAINT"/"no check"
				// here is never legitimate -- only a partial heal, or a
				// failed cleanupFailedDynamicServer delete, left behind.
				// Without this, sawWriteLeader would see the leftover row
				// as "already handled" and never retry it.
				sawWriteLeader = writeLeaderRowHealthy(line[17])
			}
			host := line[73]
			if proxy.HasDNS() {
				// After provisioning the stats may arrive with IP:Port while sometime not
				if strings.Count(host, ":") >= 2 {
					// IPV6
					host, _, _ = net.SplitHostPort(host)
					host = misc.Unbracket(host)
				} else {
					host = strings.Split(line[73], ":")[0]
				}
				host = backend_ip_host[host]
			}

			srv := cluster.GetServerFromURL(host)

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy stat lookup writer: host %s translated to %s", line[73], host)

			if srv != nil {
				bkw := Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61], //ttime: average session time in ms over the 1024 last requests
				}

				if bkw.PrxName != "" {
					foundMasterInStat = true
					proxy.BackendsWrite = append(proxy.BackendsWrite, bkw)

					if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
						if !srv.IsStandAlone() {
							if stagingsrv != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "[Staging] Detecting wrong master server in haproxy %s fixing it to standalone %s %s", proxy.Host+":"+proxy.Port, stagingsrv.Host, stagingsrv.Port)
								msg, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.Host, stagingsrv.Port)
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
								}
							}
						}
					} else {
						if !srv.IsMaster() {
							master := cluster.GetMaster()
							if master != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "Detecting wrong master server in haproxy %s fixing it to master %s %s", proxy.Host+":"+proxy.Port, master.Host, master.Port)
								msg, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.Host, master.Port)
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (master: %s)", proxy.Host+":"+proxy.Port, msg, master.Host+":"+master.Port)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (master: %s)", proxy.Host+":"+proxy.Port, msg, master.Host+":"+master.Port)
								}
							}
						}
					}
				}
			}
		}
		// Exact match -- see the write-backend guard above for why.
		if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIReadBackend) {
			readSvnamesSeen[line[1]] = true
			if !readServerRowHealthy(line[17]) {
				readSvnamesUnhealthy[line[1]] = true
			}
			// A row can be healthy (UP/DRAIN/MAINT) at a stale address --
			// e.g. the server's IP changed since this row was provisioned --
			// which readServerRowHealthy can't catch on its own. Look the
			// server up by Id (svname convention) rather than trust the
			// `srv` lookup below, which matches by the row's own possibly-
			// stale address and could silently resolve to a different
			// server. Literal IPs only: a hostname-backed server legitimately
			// shows a DNS-resolved IP here, and addDynamicServer refuses to
			// touch those anyway, so flagging it would just spam refusals.
			if byId := cluster.GetServerFromName(line[1]); byId != nil && net.ParseIP(byId.Host) != nil {
				if !strings.EqualFold(line[73], net.JoinHostPort(byId.Host, byId.Port)) {
					readSvnamesUnhealthy[line[1]] = true
				}
			}
			host := line[73]
			if proxy.HasDNS() {
				// After provisioning the stats may arrive with  IP:Port while sometime not
				if strings.Count(host, ":") >= 2 {
					// IPV6
					host, _, _ = net.SplitHostPort(host)
					host = misc.Unbracket(host)
				} else {
					host = strings.Split(line[73], ":")[0]
				}
				host = backend_ip_host[host]
			}
			srv := cluster.GetServerFromURL(host)
			if srv == nil && proxy.HasDNS() {
				// DNS resolution may have failed (server DOWN/MAINT) — use the
				// FQDN from show servers state via the svname→FQDN map.
				if fqdn, ok := backend_svname_host[line[1]]; ok {
					srv = cluster.GetServerFromURL(fqdn)
				}
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy stat lookup reader: host %s translated to %s", line[73], host)

			if srv != nil {
				bkr := Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					Svname:         line[1],
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61],
				}

				proxy.BackendsRead = append(proxy.BackendsRead, bkr)

				if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
					if stagingsrv != nil {
						if srv.Id == stagingsrv.Id { // Only activate staging server for read
							if line[17] == "DRAIN" {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy staging is DRAIN in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
								msg, err := haRuntime.SetReady(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
								}
							}
						} else { // Deactivate other servers
							if line[17] == "UP" {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy non-staging backend state is UP in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
								msg, err := haRuntime.SetDrain(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
								if err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
								}
							}
						}
					}
				} else {
					if (srv.State == stateSlaveErr || srv.State == stateRelayErr || srv.State == stateSlaveLate || srv.State == stateRelayLate || srv.IsIgnored()) && line[17] == "UP" || srv.State == stateWsrepLate || srv.State == stateWsrepDonor {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting broken replication and UP state in haproxy %s drain server %s (%s)", proxy.Host+":"+proxy.Port, srv.Id, srv.URL)
						msg, err := haRuntime.SetDrain(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
						if err != nil {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
						} else {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
							proxy.setLastReadBackendStatus("DRAIN")
						}
					}
					if (srv.State == stateSlave || srv.State == stateRelay || (srv.State == stateWsrep && !srv.IsLeader())) && line[17] == "DRAIN" && !srv.IsIgnored() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy valid replication and DRAIN state in haproxy %s enable traffic on server %s", proxy.Host+":"+proxy.Port, srv.URL)
						msg, err := haRuntime.SetReady(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
						if err != nil {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
						} else {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
							proxy.setLastReadBackendStatus("UP")
						}
					}
					if srv.IsMaster() {
						masterReadFound = true
						masterReadSvname = bkr.Svname
						masterReadStatus = line[17]
					}
				}

				if srv.IsMaintenance && line[17] == "UP" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting server %s in maintenance but proxy %s reports UP  ", srv.URL, proxy.Host+":"+proxy.Port)
					proxy.SetMaintenance(srv)
					proxy.setLastReadBackendStatus("MAINT")
					if srv.IsMaster() {
						masterReadStatus = "MAINT"
					}
				}
				if !srv.IsMaintenance && line[17] == "MAINT" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting server %s UP but proxy %s reports in maintenance ", srv.URL, proxy.Host+":"+proxy.Port)
					proxy.SetMaintenance(srv)
					proxy.setLastReadBackendStatus("UP")
					if srv.IsMaster() {
						masterReadStatus = "UP"
					}
				}
			}
		}
	}

	if cluster.Conf.HaproxyRuntimeDynamicBackends && proxy.hasDynamicBackendSupport(&haRuntime) {
		proxy.selfHealDynamicBackends(&haRuntime, sawWriteBackend, sawReadBackend, sawWriteLeader, readSvnamesSeen, readSvnamesUnhealthy)
	}

	if masterReadFound {
		shouldRead := proxy.masterShouldBeReader()
		if !shouldRead && masterReadStatus == "UP" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy master is not configured as reader but state is UP in haproxy %s", proxy.Host+":"+proxy.Port)
			msg, err := haRuntime.SetDrain(masterReadSvname, cluster.Conf.HaproxyAPIReadBackend)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			}
		}
		if shouldRead && masterReadStatus == "DRAIN" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy master is configured as reader but state is DRAIN in haproxy %s", proxy.Host+":"+proxy.Port)
			msg, err := haRuntime.SetReady(masterReadSvname, cluster.Conf.HaproxyAPIReadBackend)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			}
		}
	}
	if !foundMasterInStat {
		if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
			if stagingsrv != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "[Staging] HAProxy has standalone in cluster but not in haproxy %s fixing it to standalone %s %s", proxy.Host+":"+proxy.Port, stagingsrv.Host, stagingsrv.Port)
				msg, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.Host, stagingsrv.Port)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
				}
			}
		} else {
			master := cluster.GetMaster()
			if master != nil && master.IsLeader() {
				res, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.Host, master.Port)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy has leader in cluster but not in %s fixing it to master %s return %s", proxy.Host+":"+proxy.Port, master.URL, res)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy cannot add leader %s in cluster but not in %s : %s", master.URL, proxy.Host+":"+proxy.Port, err)
				}
			}
		}

	}
	return nil
}

// hasDynamicBackendSupport reports whether this HAProxy instance is new
// enough to serve "publish backend" (HAProxy 3.4+). Always fetches fresh
// rather than trusting Refresh()'s own proxy.Version cache (populated once,
// purely for display there): a stale cache here would leave self-heal
// disabled forever after an upgrade from pre-3.4, until repman restarts.
// Matches srv.go's DB-version tracking, which also never caches.
func (proxy *HaproxyProxy) hasDynamicBackendSupport(haRuntime *haproxy.Runtime) bool {
	vstring, err := haRuntime.GetVersion()
	if err != nil || vstring == "" {
		return false
	}
	proxy.SetVersion(vstring)
	return versionSupportsDynamicBackends(vstring)
}

func versionSupportsDynamicBackends(v string) bool {
	if v == "" {
		return false
	}
	ver, tokens := version.NewVersionFromString("HAProxy", v)
	if tokens == 0 {
		return false
	}
	return ver.Major > haproxyMinDynamicBackendMajor ||
		(ver.Major == haproxyMinDynamicBackendMajor && ver.Minor >= haproxyMinDynamicBackendMinor)
}

// selfHealDynamicBackends performs best-effort recovery of HAProxy runtime
// state on HAProxy 3.4+, where backends and servers can be created without a
// config reload. Every step is best-effort and independently logged: a
// failure (e.g. HAProxy rejects a command because it's actually older, or a
// dynamic backend was already deleted by something else) does not abort the
// remaining steps, and the next Refresh() pass simply retries whatever is
// still missing.
func (proxy *HaproxyProxy) selfHealDynamicBackends(haRuntime *haproxy.Runtime, sawWriteBackend, sawReadBackend, sawWriteLeader bool, readSvnamesSeen, readSvnamesUnhealthy map[string]bool) {
	cluster := proxy.ClusterGroup

	// This whole feature assumes runtimeapi's server-naming convention:
	// "leader" for the write server, cluster server Id for read servers
	// (see GetConfigProxyModule). "standby" mode names servers "server1",
	// "server2", ... instead -- self-heal here would misread every real row
	// as missing, add duplicates under the wrong names, and prune the real
	// ones as stale. Init() already reconciles standby by fully
	// re-rendering and reloading config, not incrementally.
	if cluster.Conf.HaproxyMode != "runtimeapi" {
		return
	}

	// Staging proxies use a different model entirely: writes target
	// HaproxyStagingBackend pointed at the standalone staging server, not
	// GetMaster(), and only that one server should ever be UP for reads.
	// Self-heal doesn't model any of that -- wrong write backend name, and
	// isReadEligible() would add ordinary replicas staging mode drains on
	// purpose -- so it must not run at all for a staging proxy.
	if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
		return
	}

	writeBackend := cluster.Conf.HaproxyAPIWriteBackend
	readBackend := cluster.Conf.HaproxyAPIReadBackend

	// The write and read sides are repaired independently: one side's
	// backend failing to recreate must not skip the other side's repair,
	// since each side retries independently on the next Refresh() pass
	// regardless of the other's outcome.
	writeBackendReady := sawWriteBackend
	if !sawWriteBackend {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy write backend %s missing at runtime on %s, recreating", writeBackend, proxy.Host+":"+proxy.Port)
		writeBackendReady = proxy.addDynamicBackend(haRuntime, writeBackend)
		if writeBackendReady {
			sawWriteLeader = false
		}
	}
	if writeBackendReady && !sawWriteLeader {
		// Only create the leader row once there's an actual, ready master to
		// point it at. Nothing else in this codebase would bring a
		// dynamically-created leader out of whatever state it started in
		// except this same check on a later pass: SetMaster/SetMasterFQDN
		// only update an existing row's address, never its enabled state.
		// So leave the row absent (retried every pass via sawWriteLeader
		// staying false) rather than present-but-wrong, for either
		// master == nil (a placeholder could route writes to the wrong
		// target) or master.IsMaintenance (no same-pass write-side
		// maintenance reconciliation exists to un-enable it later).
		if master := cluster.GetMaster(); master != nil && !master.IsMaintenance {
			if net.ParseIP(master.Host) == nil {
				// Same limitation addDynamicServer refuses hostnames for,
				// called out explicitly here since an unpopulated leader
				// means the write backend has zero servers -- every write
				// fails, not just one reader among several.
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy cannot self-heal the write backend's leader server for hostname-backed master %s: the runtime API has no way to attach DNS resolution to a dynamically-created server, so writes through %s will remain unavailable until HAProxy is reloaded with the current config", master.URL, writeBackend)
			} else if !proxy.addDynamicServer(haRuntime, writeBackend, "leader", master.Host, master.Port) {
				// A partial failure leaves "leader" existing but disabled,
				// and (unlike the read backend) nothing else would ever
				// notice and fix a stuck write-side row. Clean it back up so
				// the whole add+enable sequence retries from scratch next
				// pass, as if AddServer itself had failed outright.
				proxy.cleanupFailedDynamicServer(haRuntime, writeBackend, "leader")
			}
		}
	}

	readBackendReady := sawReadBackend
	if !sawReadBackend {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy read backend %s missing at runtime on %s, recreating", readBackend, proxy.Host+":"+proxy.Port)
		readBackendReady = proxy.addDynamicBackend(haRuntime, readBackend)
		if readBackendReady {
			readSvnamesSeen = nil
			readSvnamesUnhealthy = nil
		}
	}
	if !readBackendReady {
		return
	}

	// The master/leader is handled separately below, not by the generic
	// eligibility check here: its read-backend membership is a policy
	// decision (masterShouldBeReader), not a replication-state one.
	for _, server := range cluster.Servers {
		if server == nil || server.IsMaintenance || server.IsMaster() {
			continue
		}
		// A seen-but-unhealthy row (readServerRowHealthy said no -- e.g.
		// stuck at "no check" or "DOWN ..." because a previous pass's
		// EnableServer/EnableHealth was rejected) must not be skipped like a
		// genuinely healthy one: nothing else in this codebase would ever
		// retry it otherwise, since readSvnamesSeen alone can't tell "already
		// fixed" apart from "exists but still broken."
		if readSvnamesSeen[server.Id] && !readSvnamesUnhealthy[server.Id] {
			continue
		}
		// Only self-heal servers that Refresh()'s own eligibility checks
		// (the stateSlaveErr/.../IsIgnored() drain branch and the
		// stateSlave/stateRelay/stateWsrep ready branch above) would
		// themselves bring into service. Otherwise a lagged/ignored/wsrep
		// donor server would be added and enabled immediately -- serving
		// live read traffic for a full Refresh() cycle -- before the next
		// pass drains it back out.
		if !proxy.isReadEligible(server) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy self-heal skipping ineligible read server %s (state %s)", server.URL, server.State)
			continue
		}
		if proxy.addDynamicServer(haRuntime, readBackend, server.Id, server.Host, server.Port) {
			// Reflect the just-healed server in this same pass's read-backend
			// snapshot immediately: the master-row check right below calls
			// masterShouldBeReader() -> HasAvailableReader(), which reads
			// proxy.BackendsRead. Without this, a slave restored by this very
			// loop wouldn't count as "available" yet, and the no-slave
			// fallback could wrongly add the master as a reader in the same
			// pass the slave was actually fixed. See upsertHealedReadRow for
			// why this replaces rather than appends on a retry, and why the
			// placeholder metric fields matter.
			proxy.upsertHealedReadRow(server.Id, server.Host, server.Port, server.State)
		}
	}

	// The master/leader's read-backend row (named by its Id) is only ever
	// brought to ready/drain by the masterReadFound block below once that
	// row exists. If it was never provisioned (or service_read was just
	// recreated), reads for a should-be-reader master stay blackholed
	// forever without this. Self-heal it like any other missing server, but
	// only when the no-slave/read-on-master policy currently calls for it
	// (else it'd just have to be drained back out), and never while the
	// master is in maintenance -- unlike the write leader, a maintenance
	// server's read row is expected to be absent, not present-but-drained.
	if master := cluster.GetMaster(); master != nil && !master.IsMaintenance && (!readSvnamesSeen[master.Id] || readSvnamesUnhealthy[master.Id]) {
		if proxy.masterShouldBeReader() {
			if proxy.addDynamicServer(haRuntime, readBackend, master.Id, master.Host, master.Port) {
				// Same-pass visibility as the ordinary replica loop above:
				// without this, a should-be-reader master healed this pass
				// wouldn't show up in proxy.BackendsRead (and so wouldn't be
				// counted by HasAvailableReader() or reported by
				// FetchStats()/the status API) until the next Refresh() pass,
				// even though HAProxy is already routing to it correctly.
				proxy.upsertHealedReadRow(master.Id, master.Host, master.Port, master.State)
			}
		}
	}

	// Best-effort prune of read servers that no longer correspond to any
	// current cluster server (e.g. a node was removed from the cluster
	// after the backend was provisioned).
	clusterIds := make(map[string]bool, len(cluster.Servers))
	for _, server := range cluster.Servers {
		if server != nil {
			clusterIds[server.Id] = true
		}
	}
	for svname := range readSvnamesSeen {
		if clusterIds[svname] {
			continue
		}
		res, err := haRuntime.SetMaintenance(svname, readBackend)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not set maintenance on stale read server %s/%s: %s", readBackend, svname, err)
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy maintenance on stale read server %s/%s: %s", readBackend, svname, res)
		if _, err := haRuntime.DelServer(svname, readBackend); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy could not delete stale read server %s/%s: %s", readBackend, svname, err)
			continue
		}
		// DelServer rejections (e.g. the server still holds active/idle
		// connections) come back as plain CLI text with err == nil, the
		// same ApiCmd limitation addDynamicBackend already works around
		// for "add backend". Confirm the server is actually gone via
		// "show servers state" before logging/trusting the deletion.
		if proxy.serverExistsAtRuntime(haRuntime, readBackend, svname) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy could not delete stale read server %s/%s: still present after del server -- will retry next pass", readBackend, svname)
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy deleted stale read server %s/%s", readBackend, svname)
	}
}

// serverExistsAtRuntime confirms a specific server still exists in a given
// backend via "show servers state" -- see the stale-server prune loop above
// for why this, not err, is what a DelServer call's success is judged by.
func (proxy *HaproxyProxy) serverExistsAtRuntime(haRuntime *haproxy.Runtime, pool, name string) bool {
	res, err := haRuntime.ShowServersState()
	if err != nil {
		// Can't confirm either way -- assume it might still be there rather
		// than risk falsely reporting a deletion that didn't happen.
		return true
	}
	reader := csv.NewReader(strings.NewReader("# " + strings.ReplaceAll(res, " ", ",")))
	reader.Comment = '#'
	reader.FieldsPerRecord = -1
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return true
		}
		if len(line) > 3 && strings.EqualFold(line[1], pool) && strings.EqualFold(line[3], name) {
			return true
		}
	}
	return false
}

// upsertHealedReadRow reflects a just-healed read-backend server in this
// same pass's proxy.BackendsRead snapshot immediately, so same-pass
// consumers (masterShouldBeReader -> HasAvailableReader, FetchStats(), the
// status API) see it without a one-pass lag.
//
// If svname already has an entry (a retry of a row that existed but was
// unhealthy), it's replaced in place rather than duplicated -- the main
// "show stat" parse loop above already appended one with the stale
// pre-heal status, and a duplicate would double FetchStats()'s metrics.
//
// PrxName/PrxConnections/PrxByteIn/PrxByteOut/PrxLatency get real-shaped
// placeholder values, not the Go zero value: FetchStats() reads them
// unconditionally this same pass, and an empty PrxName/numeric field would
// emit a blank-identity, malformed Graphite line. The next real "show stat"
// parse overwrites these with the row's actual figures.
func (proxy *HaproxyProxy) upsertHealedReadRow(svname, host, port, clusterState string) {
	healed := Backend{
		Host:           host,
		Port:           port,
		Status:         clusterState,
		Svname:         svname,
		// net.JoinHostPort, not a bare "+":"+" concatenation: matches the
		// bracketed form real "show stat" rows report an IPv6 address in
		// (line[73], used verbatim as PrxName elsewhere in this file), so
		// this synthetic entry's PrxName stays consistent with what the very
		// next Refresh() pass's real parse would show for the same server.
		PrxName: net.JoinHostPort(host, port),
		PrxStatus:      "UP",
		PrxConnections: "0",
		PrxByteIn:      "0",
		PrxByteOut:     "0",
		PrxLatency:     "0",
	}
	for i := range proxy.BackendsRead {
		if proxy.BackendsRead[i].Svname == svname {
			proxy.BackendsRead[i] = healed
			return
		}
	}
	proxy.BackendsRead = append(proxy.BackendsRead, healed)
}

// isReadEligible mirrors the same per-state checks Refresh() applies via
// SetDrain/SetReady just above (the stateSlaveErr/stateRelayErr/
// stateSlaveLate/stateRelayLate/IsIgnored()/stateWsrepLate/stateWsrepDonor
// drain conditions and the stateSlave/stateRelay/stateWsrep-non-leader ready
// condition), so a server self-heal adds starts in the same eligibility
// state Refresh() would otherwise converge it to.
func (proxy *HaproxyProxy) isReadEligible(srv *ServerMonitor) bool {
	if srv.IsIgnored() {
		return false
	}
	switch srv.State {
	case stateSlave, stateRelay:
		return true
	case stateWsrep:
		return !srv.IsLeader()
	default:
		return false
	}
}

// addDynamicBackend creates and publishes a runtime backend so it can start
// receiving traffic. Returns false if either step fails.
func (proxy *HaproxyProxy) addDynamicBackend(haRuntime *haproxy.Runtime, name string) bool {
	cluster := proxy.ClusterGroup
	res, err := haRuntime.AddBackend(name, "tcp", haproxyDynDefaultsSection)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not add dynamic backend %s: %s", name, err)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy add backend %s: %s", name, res)
	res, err = haRuntime.PublishBackend(name)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not publish dynamic backend %s: %s", name, err)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy publish backend %s: %s", name, res)

	// AddBackend/PublishBackend rejections come back as plain CLI text with
	// err == nil (ApiCmd only ever errors on a transport failure, not on
	// HAProxy refusing the command) -- most notably when the already-running
	// HAProxy process predates the "dyn_defaults" defaults section this
	// backend is created "from" (a process only picks up a config change
	// like that on its next reload/restart, which this self-heal pass
	// cannot trigger on its own). So don't trust a nil err here: confirm the
	// backend is actually published, not merely present -- a backend that
	// only got as far as AddBackend, with PublishBackend never taking
	// effect, still shows up in a plain existence check.
	if !proxy.backendPublishedAtRuntime(haRuntime, name) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy dynamic backend %s not published after add+publish -- if this HAProxy process was started/reloaded before dyn_defaults was added to its config, reload it once with the current config to enable self-heal", name)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy published dynamic backend %s", name)
	return true
}

// backendRowPublished reports whether a "show stat" BACKEND summary row's
// status column shows the backend actually publishing traffic, not just
// present -- "UP (UNPUB)" right after "add backend", before "publish
// backend" drops the "(UNPUB)" suffix.
func backendRowPublished(status string) bool {
	return !strings.Contains(status, "UNPUB")
}

// readServerRowHealthy reports whether a read-backend row's "show stat"
// status is one this file already manages: "UP" (any "UP ..."-prefixed
// transient, e.g. "UP -1/3" right after EnableHealth), "DRAIN" (toggled by
// the main parse loop's SetReady/SetDrain), or "MAINT" (SetMaintenance's
// target, and a dynamic server's starting state). Anything else -- "no
// check" (EnableServer succeeded, EnableHealth didn't) or "DOWN ..." -- is
// a row nothing else would ever recover; selfHealDynamicBackends' read loop
// keeps retrying it instead of treating presence as done.
func readServerRowHealthy(status string) bool {
	return strings.HasPrefix(status, "UP") || status == "DRAIN" || status == "MAINT"
}

// writeLeaderRowHealthy reports whether the write backend's "leader" row is
// actually usable, not just present. Unlike a read-backend row,
// "MAINT"/"DRAIN" are never a legitimate resting state for it under
// self-heal's own gate (only attempted while the master is known and not in
// maintenance -- see selfHealDynamicBackends): only an "UP"-prefixed status
// (including the transient "UP -1/3"-style string right after EnableHealth,
// matching addDynamicServer's own post-enable allowlist) counts.
func writeLeaderRowHealthy(status string) bool {
	return strings.HasPrefix(status, "UP")
}

// backendPublishedAtRuntime confirms a backend is not merely present but
// actually published (routable) via "show stat"'s BACKEND summary row's
// status field -- see addDynamicBackend for why this, not just existence,
// is what its success is judged by.
func (proxy *HaproxyProxy) backendPublishedAtRuntime(haRuntime *haproxy.Runtime, name string) bool {
	res, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		return false
	}
	reader := csv.NewReader(strings.NewReader(res))
	reader.FieldsPerRecord = -1
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
		if len(line) > 17 && line[1] == "BACKEND" && strings.EqualFold(line[0], name) {
			return backendRowPublished(line[17])
		}
	}
	return false
}

// addDynamicServer adds a server to an existing backend and brings it into
// service. A dynamically-added server starts in maintenance with health
// checks disabled, so EnableServer/EnableHealth always follow.
//
// Returns true only once AddServer, EnableServer, and EnableHealth have all
// actually succeeded, not merely that none errored. Callers rely on this to
// gate their own same-pass proxy.BackendsRead bookkeeping: a
// partially-healed server (e.g. still in maintenance) must not count as an
// available reader, or masterShouldBeReader() could wrongly skip the
// master's own re-add in the same pass, blackholing reads on both fronts.
//
// host must already be a literal IP -- a hostname is refused outright. The
// runtime API's "set server ... fqdn" only works on a server that already
// has a static "resolvers" section; there's no way to attach one to a
// server created dynamically, so it would accept the command and sit
// permanently unresolved while self-heal's own log claimed success.
// Callers needing FQDN support (the write-leader call site) check
// net.ParseIP themselves and skip calling this, for a clearer message.
func (proxy *HaproxyProxy) addDynamicServer(haRuntime *haproxy.Runtime, pool, name, host, port string) bool {
	cluster := proxy.ClusterGroup
	if net.ParseIP(host) == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy cannot self-heal hostname-backed server %s/%s (%s): the runtime API has no way to attach DNS resolution to a dynamically-created server, so it would stay unresolved -- reload HAProxy with the current config to add it statically instead", pool, name, host)
		return false
	}
	res, err := haRuntime.AddServer(pool, name, host, port)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not add dynamic server %s/%s: %s", pool, name, err)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy add server %s/%s: %s", pool, name, res)
	// AddServer rejections (e.g. the backend's balance algorithm isn't
	// dynamic-compatible) come back as plain CLI text with err == nil --
	// confirm the row actually exists (any status, even "MAINT", counts --
	// this only checks the add itself took) before trusting it, the same
	// way addDynamicBackend does for "add backend".
	if status, _ := proxy.serverRowAtRuntime(haRuntime, pool, name); status == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy dynamic server %s/%s does not exist after add server", pool, name)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy added dynamic server %s/%s", pool, name)

	ok := true
	if res, err := haRuntime.EnableServer(name, pool); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not enable dynamic server %s/%s: %s", pool, name, err)
		ok = false
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy enable server %s/%s: %s", pool, name, res)
	}
	if res, err := haRuntime.EnableHealth(name, pool); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not enable health on dynamic server %s/%s: %s", pool, name, err)
		ok = false
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy enable health %s/%s: %s", pool, name, res)
	}
	if !ok {
		return false
	}
	// EnableServer/EnableHealth rejections also come back as CLI text with
	// err == nil -- confirm the server actually left maintenance and has
	// health checking running before trusting it's in service. This is an
	// allowlist (status must start with "UP"), not a denylist of known-bad
	// strings: right after a successful enable, status is a transient
	// "no check" (EnableServer alone, checks not yet resumed) or
	// "UP -1/3"-style string (EnableHealth, before checks stabilize) rather
	// than the literal "UP" -- both still start with "UP". A denylist would
	// let an actual "DOWN"/"NOLB" status through as success too, letting the
	// read-eligible loop append a synthetic "UP" BackendsRead entry for a
	// server that isn't usable and wrongly suppress the master-row
	// fallback.
	status, addr := proxy.serverRowAtRuntime(haRuntime, pool, name)
	if !strings.HasPrefix(status, "UP") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy dynamic server %s/%s status %q not confirmed up after enable", pool, name, status)
		return false
	}
	// A row can pre-exist under this name at a stale address (the server's
	// host/IP changed since it was provisioned): "add server" against an
	// existing name is rejected with CLI text too (confirmed live, address
	// left untouched), so the existence and status checks above would both
	// pass while EnableServer/EnableHealth just brought the OLD address
	// into service. There's no runtime command to change an address
	// without risking a live cutover, so a mismatch is only ever detected
	// and refused here, never auto-corrected -- reload HAProxy to fix it.
	// net.JoinHostPort (not bare "+":"+" concatenation) since HAProxy
	// reports IPv6 addresses bracketed ("[::1]:3307", confirmed live).
	wantAddr := net.JoinHostPort(host, port)
	if !strings.EqualFold(addr, wantAddr) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy dynamic server %s/%s exists at address %s, want %s -- a stale row under this name was left behind by a previous host/IP change and \"add server\" cannot update it; reload HAProxy with the current config to fix it", pool, name, addr, wantAddr)
		return false
	}
	return true
}

// serverRowAtRuntime returns this server's current HAProxy status and
// address ("show stat" line[17] and line[73]), or ("", "") if it has no row
// at all. Uses "show stat" rather than "show servers state" for consistency
// with Refresh()'s own main parse loop above, which already relies on this
// exact command and status-string convention throughout this file, rather
// than srv_admin_state's undocumented bitmask.
func (proxy *HaproxyProxy) serverRowAtRuntime(haRuntime *haproxy.Runtime, pool, name string) (status, addr string) {
	res, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		return "", ""
	}
	reader := csv.NewReader(strings.NewReader(res))
	reader.FieldsPerRecord = -1
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", ""
		}
		if len(line) > 73 && line[1] != "FRONTEND" && line[1] != "BACKEND" && strings.EqualFold(line[0], pool) && strings.EqualFold(line[1], name) {
			return line[17], line[73]
		}
	}
	return "", ""
}

// cleanupFailedDynamicServer best-effort removes a server addDynamicServer
// couldn't fully bring into service, so the next pass sees it as genuinely
// missing and retries add+enable rather than treating a stuck row as
// "already handled." Only used by the write-leader call site: the read
// side doesn't need this, since a stuck-in-maintenance read row is already
// caught by Refresh()'s own main parse loop, which has no write-side
// equivalent. SetMaintenance first since DelServer requires it -- harmless
// if already there (e.g. AddServer itself was what failed).
func (proxy *HaproxyProxy) cleanupFailedDynamicServer(haRuntime *haproxy.Runtime, pool, name string) {
	cluster := proxy.ClusterGroup
	if _, err := haRuntime.SetMaintenance(name, pool); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy could not set maintenance while cleaning up partially-healed server %s/%s: %s", pool, name, err)
	}
	if _, err := haRuntime.DelServer(name, pool); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy could not delete partially-healed server %s/%s: %s", pool, name, err)
		return
	}
	if proxy.serverExistsAtRuntime(haRuntime, pool, name) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy partially-healed server %s/%s still present after cleanup delete -- will keep retrying", pool, name)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy cleaned up partially-healed server %s/%s", pool, name)
}

// setLastReadBackendStatus overrides the PrxStatus of the most recently
// appended BackendsRead entry to reflect the effective state after this
// pass's own SetDrain/SetReady/SetMaintenance actions, so that
// HasAvailableReader() (called later in this same Refresh() pass) does not
// act on the stale pre-action "show stat" snapshot.
func (proxy *HaproxyProxy) setLastReadBackendStatus(status string) {
	if n := len(proxy.BackendsRead); n > 0 {
		proxy.BackendsRead[n-1].PrxStatus = status
	}
}

// HasAvailableReader returns true if the read backend currently has at least
// one entry that is not the current master/leader's own row and whose
// effective HAProxy status for this pass is "UP". The master/leader's row is
// identified by host/port identity against cluster.GetMaster() rather than by
// b.Status == stateMaster, because a Galera/Wsrep leader's repman state is
// stateWsrep, not stateMaster (cluster.GetMaster() returns cluster.vmaster for
// Wsrep topologies, where cluster.master == cluster.vmaster == leader).
func (proxy *HaproxyProxy) HasAvailableReader() bool {
	cluster := proxy.ClusterGroup
	master := cluster.GetMaster()
	for _, b := range proxy.BackendsRead {
		isMasterEntry := master != nil && b.Host == master.Host && b.Port == master.Port
		if !isMasterEntry && b.PrxStatus == "UP" {
			return true
		}
	}
	return false
}

// masterShouldBeReader reports whether the master/leader should be a member
// of the HAProxy read backend: always when proxy-servers-read-on-master is
// set, or as a fallback (proxy-servers-read-on-master-no-slave, default true)
// when there is no other valid/available slave reader.
func (proxy *HaproxyProxy) masterShouldBeReader() bool {
	cluster := proxy.ClusterGroup
	if cluster.Configurator.HasProxyReadLeader() {
		return true
	}
	return cluster.Configurator.HasProxyReadLeaderNoSlave() &&
		(cluster.HasNoValidSlave() || !proxy.HasAvailableReader())
}

func (cluster *Cluster) setMaintenanceHaproxy(pr *Proxy, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

func (proxy *HaproxyProxy) SetMaintenance(server *ServerMonitor) {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.HaproxyOn {
		return
	}
	if cluster.Conf.HaproxyMode == "standby" {
		proxy.Init()
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set maintenance for server %s ", server.URL)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(proxy.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	svname := server.Id
	bkr := proxy.GetReadBackendDetail(server)
	if bkr != nil {
		svname = bkr.Svname
	}

	if server.IsMaintenance {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set server %s/%s state maint ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetMaintenance(svname, cluster.Conf.HaproxyAPIReadBackend)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set maintenance %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set maintenance %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set server %s/%s state ready ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetReady(svname, cluster.Conf.HaproxyAPIReadBackend)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set ready %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
	}

	if server.IsMaster() {
		if server.IsMaintenance {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set maintenance for server %s ", server.URL)

			res, err := haRuntime.SetMaintenance("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set maintenance %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set maintenance result: %s", res)

		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set ready for server %s ", server.URL)

			res, err := haRuntime.SetReady("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set ready %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, err)
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, res)
		}
	}
}

func (proxy *HaproxyProxy) Failover() {
	cluster := proxy.ClusterGroup
	if cluster.Conf.HaproxyMode == "runtimeapi" {
		proxy.Refresh()
	}
	if cluster.Conf.HaproxyMode == "standby" {
		proxy.Init()
	}
}

func (proxy *HaproxyProxy) BackendsStateChange() {
	proxy.Refresh()
}

func (proxy *HaproxyProxy) CertificatesReload() error {
	return nil
}
