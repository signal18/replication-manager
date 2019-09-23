// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) GetPersitentState() error {

	type Save struct {
		Servers string    `json:"servers"`
		Crashes crashList `json:"crashes"`
		SLA     state.Sla `json:"sla"`
	}

	var clsave Save
	file, err := ioutil.ReadFile(cluster.Conf.WorkingDir + "/" + cluster.Name + "/clusterstate.json")
	if err != nil {
		cluster.LogPrintf(LvlInfo, "No file found: %v\n", err)
		return err
	}
	err = json.Unmarshal(file, &clsave)
	if err != nil {
		cluster.LogPrintf(LvlErr, "File error: %v\n", err)
		return err
	}
	if len(clsave.Crashes) > 0 {
		cluster.LogPrintf(LvlInfo, "Restoring %d crashes from file: %s\n", len(clsave.Crashes), cluster.Conf.WorkingDir+"/"+cluster.Name+"/clusterstate.json")
	}
	cluster.Crashes = clsave.Crashes
	cluster.sme.SetSla(clsave.SLA)

	return nil
}

func (cluster *Cluster) GetMaster() *ServerMonitor {
	if cluster.master == nil {
		return cluster.vmaster
	} else {
		return cluster.master
	}
}

func (cluster *Cluster) GetErrorList() map[string]string {
	return clusterError
}
func (cluster *Cluster) GetTraffic() bool {
	return cluster.Conf.TestInjectTraffic
}

func (cluster *Cluster) GetClusterName() string {
	return cluster.Name
}

func (cluster *Cluster) GetServers() serverList {
	return cluster.Servers
}

func (cluster *Cluster) GetSlaves() serverList {
	return cluster.slaves
}

func (cluster *Cluster) GetProxies() proxyList {
	return cluster.Proxies
}

func (cluster *Cluster) GetConf() config.Config {
	return cluster.Conf
}

func (cluster *Cluster) GetWaitTrx() int64 {
	return cluster.Conf.SwitchWaitTrx
}

func (cluster *Cluster) GetStateMachine() *state.StateMachine {
	return cluster.sme
}

func (cluster *Cluster) GetMasterFailCount() int {
	return cluster.master.FailCount
}

func (cluster *Cluster) GetFailoverCtr() int {
	return cluster.FailoverCtr
}

func (cluster *Cluster) GetFailoverTs() int64 {
	return cluster.FailoverTs
}

func (cluster *Cluster) GetRunStatus() string {
	return cluster.Status
}
func (cluster *Cluster) GetFailSync() bool {
	return cluster.Conf.FailSync
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.Conf.RplChecks
}

func (cluster *Cluster) GetMaxFail() int {
	return cluster.Conf.MaxFail
}

func (cluster *Cluster) GetLogLevel() int {
	return cluster.Conf.LogLevel
}
func (cluster *Cluster) GetSwitchSync() bool {
	return cluster.Conf.SwitchSync
}

func (cluster *Cluster) GetRejoin() bool {
	return cluster.Conf.Autorejoin
}

func (cluster *Cluster) GetRejoinDump() bool {
	return cluster.Conf.AutorejoinMysqldump
}

func (cluster *Cluster) GetRejoinBackupBinlog() bool {
	return cluster.Conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) GetRejoinSemisync() bool {
	return cluster.Conf.AutorejoinSemisync
}

func (cluster *Cluster) GetRejoinFlashback() bool {
	return cluster.Conf.AutorejoinFlashback
}

func (cluster *Cluster) GetName() string {
	return cluster.Name
}

func (cluster *Cluster) GetTestMode() bool {
	return cluster.Conf.Test
}

func (cluster *Cluster) GetDbUser() string {
	return cluster.dbUser
}

func (cluster *Cluster) GetDbPass() string {
	return cluster.dbPass
}

func (cluster *Cluster) GetStatus() bool {
	return cluster.sme.IsFailable()
}

func (cluster *Cluster) GetGComm() string {
	var gcomms []string
	for _, server := range cluster.Servers {
		gcomms = append(gcomms, server.Host+":4567")
	}
	return strings.Join(gcomms, ",")
}

func (cluster *Cluster) getPreferedMaster() *ServerMonitor {
	if cluster.Conf.PrefMaster == "" {
		return nil
	}
	for _, server := range cluster.Servers {
		if cluster.Conf.LogLevel > 2 {
			cluster.LogPrintf(LvlDbg, "Lookup server %s if preferred master: %s", server.URL, cluster.Conf.PrefMaster)
		}
		if server.URL == cluster.Conf.PrefMaster {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetRelayServer() *ServerMonitor {
	for _, server := range cluster.Servers {
		if cluster.Conf.LogLevel > 2 {
			cluster.LogPrintf(LvlDbg, "Lookup server %s if maxscale binlog server: %s", server.URL, cluster.Conf.PrefMaster)
		}
		if server.IsRelay {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetIndiceServerFromId(Id string) int {
	i := 0
	for _, server := range cluster.Servers {

		if server.Id == Id {
			return i
		}
		i = i + 1
	}
	return 0
}

func (cluster *Cluster) GetServerFromId(serverid uint64) *ServerMonitor {
	for _, server := range cluster.Servers {
		if server.ServerID == serverid {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetServerFromName(name string) *ServerMonitor {
	for _, server := range cluster.Servers {
		if server.Id == name {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetServerFromURL(url string) *ServerMonitor {
	if strings.Contains(url, ":") {
		for _, server := range cluster.Servers {
			if server.Host+":"+server.Port == url {
				return server
			}
			if server.IP+":"+server.Port == url {
				return server
			}
		}
	} else {
		for _, server := range cluster.Servers {
			if server.Host == url {
				return server
			}
			if server.IP == url {
				return server
			}
		}
	}

	return nil
}

func (cluster *Cluster) GetMasterFromReplication(s *ServerMonitor) (*ServerMonitor, error) {

	for _, server := range cluster.Servers {
		if server.ServerID == s.ServerID {
			//Ignoring same ServerID
			continue
		}
		if len(s.Replications) > 0 {

			if cluster.Conf.LogLevel > 2 {
				cluster.LogPrintf(LvlDbg, "GetMasterFromReplication server  %d  lookup if server %s is the one : %d", s.GetReplicationServerID(), server.URL, server.ServerID)
			}
			if s.IsIOThreadRunning() && s.IsSQLThreadRunning() {
				if s.GetReplicationServerID() == server.ServerID {
					return server, nil
				}
			} else {
				if s.GetReplicationMasterHost() == server.Host && s.GetReplicationMasterPort() == server.Port {
					return server, nil
				}
			}
		}

	}

	return nil, nil
}

func (cluster *Cluster) GetFailedServer() *ServerMonitor {
	for _, server := range cluster.Servers {
		if server.State == stateFailed {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetFirstWorkingSlave() *ServerMonitor {
	for _, server := range cluster.slaves {
		if !server.IsDown() && !server.IsReplicationBroken() {
			return server
		}
	}
	return nil
}

func (cluster *Cluster) GetDBServerIdList() []string {
	cluster.Lock()
	ret := make([]string, len(cluster.Servers))
	for i, server := range cluster.Servers {
		ret[i] = server.Id
	}
	cluster.Unlock()
	return ret
}

func (cluster *Cluster) GetProxyServerIdList() []string {
	ret := make([]string, len(cluster.Proxies))
	for i, server := range cluster.Proxies {
		ret[i] = server.Id
	}
	return ret
}

func (cluster *Cluster) GetTopology() string {
	cluster.Conf.Topology = topoUnknown
	if cluster.Conf.MultiMaster {
		cluster.Conf.Topology = topoMultiMaster
	} else if cluster.Conf.MultiMasterRing {
		cluster.Conf.Topology = topoMultiMasterRing
	} else if cluster.Conf.MultiMasterWsrep {
		cluster.Conf.Topology = topoMultiMasterWsrep
	} else if cluster.Conf.MxsBinlogOn {
		cluster.Conf.Topology = topoBinlogServer
	} else if cluster.Conf.MultiTierSlave {
		cluster.Conf.Topology = topoMultiTierSlave
	} else if cluster.Conf.MasterSlavePgStream {
		cluster.Conf.Topology = topoMasterSlavePgStream
		cluster.IsPostgres = true
	} else if cluster.Conf.MasterSlavePgLogical {
		cluster.Conf.Topology = topoMasterSlavePgLog
		cluster.IsPostgres = true
	} else {
		relay := cluster.GetRelayServer()
		if relay != nil && cluster.Conf.ReplicationNoRelay == false {
			cluster.Conf.Topology = topoMultiTierSlave
		} else if cluster.master != nil {
			cluster.Conf.Topology = topoMasterSlave
		}
	}
	return cluster.Conf.Topology
}

func (cluster *Cluster) GetDatabaseTags() []string {
	return strings.Split(cluster.Conf.ProvTags, ",")
}

func (cluster *Cluster) GetProxyTags() []string {
	return strings.Split(cluster.Conf.ProvProxTags, ",")
}

func (cluster *Cluster) GetLocalProxy(this *Proxy) Proxy {
	// dirty: need to point LB to all DB  proxies, just pick the first one so far
	var prx Proxy
	for _, p := range cluster.Proxies {
		if p != this && p.Type != proxySphinx {
			return *p
		}
	}
	return prx
}

func (cluster *Cluster) GetCron() []CronEntry {
	var entries []CronEntry

	for _, e := range cluster.scheduler.Entries() {
		var entry CronEntry
		entry.Next = e.Next
		entry.Prev = e.Prev
		entry.Id = strconv.Itoa(int(e.ID))
		entry.Schedule = e.Spec
		entries = append(entries, entry)
	}
	return entries
}

func (cl *Cluster) GetArbitratorElection() error {
	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker) * time.Second * 4)
	url := "http://" + cl.Conf.ArbitrationSasHosts + "/arbitrator"
	if cl.IsSplitBrainBck != cl.IsSplitBrain {
		cl.LogPrintf("INFO", "Arbitrator: External check requested")
	} else {
		// don't need arbitration if split brain status did not change
		return nil
	}
	var mst string
	if cl.GetMaster() != nil {
		mst = cl.GetMaster().URL
	}

	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + cl.Status + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		cl.LogPrintf("ERROR", "Could not create http request to arbitrator: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cl.LogPrintf("ERROR", "Could not receive http response from arbitration: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Arbitration string `json:"arbitration"`
		Master      string `json:"master"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		cl.LogPrintf("ERROR", "Arbitrator sent back invalid JSON, %s", body)
		cl.IsFailedArbitrator = true
		return err
	}

	cl.IsFailedArbitrator = false
	if r.Arbitration == "winner" {
		cl.SetActiveStatus(ConstMonitorActif)
		cl.SetState("WARN0083", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0083"]), ErrFrom: "ARB"})
	} else {
		cl.SetActiveStatus(ConstMonitorStandby)
		cl.SetState("ERR00068", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00068"]), ErrFrom: "ARB"})
		if cl.GetMaster() != nil {
			mst = cl.GetMaster().URL
			if r.Master != mst {
				cl.LostArbitration(r.Master)
				cl.LogPrintf("INFO", "Election Lost - Current master %s different from winner master %s, %s is split brain victim. ", mst, r.Master, mst)
			}
		}
	}
	return nil
}

func (cluster *Cluster) getClusterByName(clname string) *Cluster {

	for _, c := range cluster.clusterList {
		if clname == c.GetName() {
			return c
		}
	}
	return nil
}

//GetClusterFromShardProxy return all clusters sharing same proxy
func (cluster *Cluster) GetClusterListFromShardProxy(shardproxy string) map[string]*Cluster {
	var clusters = make(map[string]*(Cluster))
	for _, c := range cluster.clusterList {
		if c.Conf.MdbsProxyHosts == shardproxy && cluster.Conf.MdbsProxyOn {
			clusters[c.GetName()] = c
		}
	}
	return clusters
}
func (cluster *Cluster) GetClusterListFromName(name string) map[string]*Cluster {
	var clusters = make(map[string]*(Cluster))
	for _, c := range cluster.clusterList {
		if cluster.Name == name {
			clusters[c.GetName()] = c
		}
	}
	return clusters
}

func (cluster *Cluster) GetTableDLL(schema string, table string, srv *ServerMonitor) (string, error) {
	query := "SHOW CREATE TABLE `" + schema + "`.`" + table + "`"
	var tbl, ddl string
	err := srv.Conn.QueryRowx(query).Scan(&tbl, &ddl)
	if err != nil {
		return "", err
	}
	pos := strings.Index(ddl, "ENGINE=")
	ddl = ddl[12:pos]
	return ddl, err
}

func (cluster *Cluster) GetTableDLLNoFK(schema string, table string, srv *ServerMonitor) (string, error) {

	ddl, err := cluster.GetTableDLL(schema, table, cluster.master)
	if err != nil {
		return "", err
	}
	ddl = strings.TrimPrefix(ddl, " `"+table+"`")

	cluster.RunQueryWithLog(srv, "CREATE OR REPLACE TABLE replication_manager_schema.`"+table+"`"+ddl+" engine=MEMORY")
	//Loop over foreign keys
	query := "SELECT CONSTRAINT_NAME from information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA='" + schema + "' AND TABLE_NAME='" + table + "' AND CONSTRAINT_TYPE='FOREIGN KEY'"
	rows, err := srv.Conn.Query(query)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Contraint fetch failed %s %s", query, err)
		return "", err
	}
	var fk string
	for rows.Next() {

		err = rows.Scan(&fk)
		if err != nil {
			return "", err
		}
		cluster.RunQueryWithLog(srv, "ALTER TABLE replication_manager_schema.`"+table+"` DROP FOREIGN KEY "+fk)
	}

	query = "SHOW CREATE TABLE replication_manager_schema.`" + table + "`"
	var tbl string
	err = srv.Conn.QueryRowx(query).Scan(&tbl, &ddl)
	if err != nil {
		return "", err
	}
	pos := strings.Index(ddl, "ENGINE=")
	ddl = ddl[12:pos]
	cluster.RunQueryWithLog(srv, "DROP TABLE IF EXISTS replication_manager_schema.`"+table+"`")
	return ddl, err
}

func (cluster *Cluster) GetDBModuleTags() []Tag {
	var tags []Tag
	for _, value := range cluster.DBModule.Filtersets {
		var t Tag
		t.Id = value.ID
		s := strings.SplitAfter(value.Name, ".")
		t.Name = s[len(s)-1]
		tags = append(tags, t)
	}
	return tags
}

// GetConfigInnoDBBPSize configure 80% of the ConfigMemory in Megabyte
func (cluster *Cluster) GetConfigInnoDBBPSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	containermem = containermem * 80 / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

// GetConfigInnoDBBPSize configure 20% of the ConfigMemory in Megabyte
func (cluster *Cluster) GetConfigInnoDBLogFileSize() string {
	if cluster.HaveTag("smallredolog") {
		return "16"
	}
	value, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	value = value * 20 / 100
	s10 := strconv.FormatInt(value, 10)
	return s10
}

// GetConfigInnoDBBPInstances configure BP/8G of the ConfigMemory in Megabyte
func (cluster *Cluster) GetConfigInnoDBBPInstances() string {
	value, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "1"
	}
	value = value/8000 + 1
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBIOCapacity() string {
	value, err := strconv.ParseInt(cluster.Conf.ProvIops, 10, 64)
	if err != nil {
		return "1"
	}
	value = value / 3
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBIOCapacityMax() string {
	value, err := strconv.ParseInt(cluster.Conf.ProvIops, 10, 64)
	if err != nil {
		return "1"
	}
	s10 := strconv.FormatInt(value, 10)
	return s10
}
