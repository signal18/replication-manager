// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) GetMysqlDumpPath() string {
	if cluster.Conf.BackupMysqldumpPath == "" {
		return cluster.Conf.ShareDir + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysqldump"
	}
	return cluster.Conf.BackupMysqldumpPath
}

func (cluster *Cluster) GetMyDumperPath() string {
	if cluster.Conf.BackupMyDumperPath == "" {
		return cluster.Conf.ShareDir + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mydumper"
	}
	return cluster.Conf.BackupMyDumperPath
}

func (cluster *Cluster) GetMyLoaderPath() string {
	if cluster.Conf.BackupMyDumperPath == "" {
		return cluster.Conf.ShareDir + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/myloader"
	}
	return cluster.Conf.BackupMyLoaderPath
}

func (cluster *Cluster) GetMysqlBinlogPath() string {
	if cluster.Conf.BackupMysqlbinlogPath == "" {
		return cluster.Conf.ShareDir + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysqlbinlog"
	}
	return cluster.Conf.BackupMysqlbinlogPath
}

func (cluster *Cluster) GetMysqlclientPath() string {
	if cluster.Conf.BackupMysqlclientPath == "" {
		return cluster.Conf.ShareDir + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysql"
	}
	return cluster.Conf.BackupMysqlclientPath
}

func (cluster *Cluster) GetDomain() string {
	if cluster.Conf.ProvNetCNI {
		return "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
	}
	return ""
}

func (cluster *Cluster) GetDomainHeadCluster() string {
	if cluster.Conf.ProvNetCNI {
		return "." + cluster.Conf.ClusterHead + ".svc." + cluster.Conf.ProvOrchestratorCluster
	}
	return ""
}

func (cluster *Cluster) GetPersitentState() error {

	type Save struct {
		Servers    string      `json:"servers"`
		Crashes    crashList   `json:"crashes"`
		SLA        state.Sla   `json:"sla"`
		SLAHistory []state.Sla `json:"slaHistory"`
	}

	var clsave Save
	file, err := ioutil.ReadFile(cluster.WorkingDir + "/clusterstate.json")
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
	cluster.SLAHistory = clsave.SLAHistory
	cluster.Crashes = clsave.Crashes
	cluster.sme.SetSla(clsave.SLA)
	cluster.sme.SetMasterUpAndSyncRestart()

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

func (cluster *Cluster) GetQps() int64 {
	qps := int64(0)
	for _, server := range cluster.Servers {
		qps += server.QPS
	}
	return qps
}

func (cluster *Cluster) GetConnections() int {
	allconns := 0
	for _, server := range cluster.Servers {
		if conns, ok := server.Status["THREADS_RUNNING"]; ok {
			numconns, _ := strconv.Atoi(conns)
			allconns += numconns
		}
	}
	return allconns
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
	if cluster.Conf.Hosts == "" {
		return nil
	}
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

func (cluster *Cluster) GetProxyFromURL(url string) *Proxy {
	if strings.Contains(url, ":") {
		for _, proxy := range cluster.Proxies {
			//	cluster.LogPrintf(LvlInfo, " search prx %s %s for url %s", proxy.Host, proxy.Port, url)
			if proxy.Host+":"+proxy.Port == url {
				return proxy
			}
		}
	} else {
		for _, proxy := range cluster.Proxies {
			if proxy.Host == url {
				return proxy
			}
		}
	}

	return nil
}

func (cluster *Cluster) GetMasterFromReplication(slave *ServerMonitor) (*ServerMonitor, error) {

	for _, server := range cluster.Servers {
		if server.ServerID == slave.ServerID {
			//Ignoring same ServerID
			continue
		}
		if len(slave.Replications) > 0 {

			if cluster.Conf.LogLevel > 2 {
				cluster.LogPrintf(LvlDbg, "GetMasterFromReplication server  %d  lookup if server %s is the one : %d", slave.GetReplicationServerID(), server.URL, server.ServerID)
			}
			if slave.IsIOThreadRunning() && slave.IsSQLThreadRunning() {
				if slave.GetReplicationServerID() == server.ServerID {
					return server, nil
				}
			} else {
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "GetMasterFromReplication slave host  %s:%s if  equal server  %s:%s", slave.GetReplicationMasterHost(), slave.GetReplicationMasterPort(), server.Host, server.Port)
				}
				if slave.GetReplicationMasterHost() == server.Host && slave.GetReplicationMasterPort() == server.Port {
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

func (cluster *Cluster) GetBackupServer() *ServerMonitor {
	if !cluster.IsDiscovered() || len(cluster.Servers) < 1 {
		return nil
	}
	for _, server := range cluster.Servers {
		if server.State != stateFailed && server.PreferedBackup {
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
	if cluster.Conf.Hosts == "" {
		cluster.Unlock()
		return ret
	}
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
		if p != this && p.Type != config.ConstProxySphinx {
			return *p
		}
	}
	return prx
}

func (cluster *Cluster) GetCron() []cron.Entry {

	return cluster.scheduler.Entries()

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

func (cluster *Cluster) GetChildClusters() map[string]*Cluster {
	var clusters = make(map[string]*(Cluster))
	for _, c := range cluster.clusterList {
		//	cluster.LogPrintf(LvlErr, "GetChildClusters %s %s ", cluster.Name, c.Conf.ClusterHead)
		if cluster.Name == c.Conf.ClusterHead {
			clusters[c.Name] = c
		}
	}
	return clusters
}

func (cluster *Cluster) GetClusterFromName(name string) (*Cluster, error) {

	for _, c := range cluster.clusterList {

		if c.Name == name {
			return c, nil
		}
	}
	return nil, errors.New("No cluster found")
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
	defer rows.Close()
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
		s := strings.Split(value.Name, ".")
		t.Name = s[len(s)-1]
		t.Category = s[len(s)-2]
		tags = append(tags, t)
	}
	return tags
}

func (cluster *Cluster) GetBackups() []Backup {
	return cluster.Backups
}

func (cluster *Cluster) GetProxyModuleTags() []Tag {
	var tags []Tag
	for _, value := range cluster.ProxyModule.Filtersets {
		var t Tag
		t.Id = value.ID
		s := strings.SplitAfter(value.Name, ".")
		t.Name = s[len(s)-1]
		tags = append(tags, t)
	}
	return tags
}

func (cluster *Cluster) GetConfigMaxConnections() string {
	return strconv.Itoa(cluster.Conf.ProvMaxConnections)
}

func (cluster *Cluster) GetConfigExpireLogDays() string {
	return strconv.Itoa(cluster.Conf.ProvExpireLogDays)
}

func (cluster *Cluster) GetConfigRelaySpaceLimit() string {
	return strconv.Itoa(10 * 1024 * 1024)
}

// GetConfigInnoDBBPSize configure 80% of the ConfigMemory in Megabyte
func (cluster *Cluster) GetConfigInnoDBBPSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["innodb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigMyISAMKeyBufferSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["myisam"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigTokuDBBufferSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["tokudb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigQueryCacheSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["querycache"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigAriaCacheSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["aria"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigS3CacheSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["s3"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigRocksDBCacheSize() string {
	containermem, err := strconv.ParseInt(cluster.Conf.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["rocksdb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (cluster *Cluster) GetConfigMyISAMKeyBufferSegements() string {
	value, err := strconv.ParseInt(cluster.GetConfigMyISAMKeyBufferSize(), 10, 64)
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
		return "100"
	}
	value = value / 3
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBIOCapacityMax() string {
	value, err := strconv.ParseInt(cluster.Conf.ProvIops, 10, 64)
	if err != nil {
		return "200"
	}
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBMaxDirtyPagePct() string {
	/*	mem, err := strconv.ParseInt(cluster.GetConfigInnoDBBPSize(), 10, 64)
		if err != nil {
			return "20"
		}
		//Compute the ration of memory compare to  a G
		//	value := mem/1000

	*/
	var value int64
	value = 40
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBMaxDirtyPagePctLwm() string {
	var value int64
	value = 20
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBLogFileSize() string {
	//result in MB
	var valuemin int64
	var valuemax int64
	valuemin = 1024
	valuemax = 20 * 1024
	value, err := strconv.ParseInt(cluster.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return "1024"
	}
	value = value / 10
	if value < valuemin {
		value = valuemin
	}
	if value > valuemax {
		value = valuemax
	}
	if cluster.HaveDBTag("smallredolog") {
		return "128"
	}
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetConfigInnoDBLogBufferSize() string {
	//result in MB
	var value int64
	value = 16
	s10 := strconv.FormatInt(value, 10)
	return s10
}

// GetConfigInnoDBBPInstances configure BP/8G of the ConfigMemory in Megabyte
func (cluster *Cluster) GetConfigInnoDBBPInstances() string {
	value, err := strconv.ParseInt(cluster.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return "1"
	}
	value = value/8000 + 1
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (cluster *Cluster) GetQueryRules() []config.QueryRule {
	r := make([]config.QueryRule, 0, len(cluster.QueryRules))
	for _, value := range cluster.QueryRules {
		r = append(r, value)
	}
	sort.Sort(QueryRuleSorter(r))
	return r
}

func (cluster *Cluster) GetServicePlans() []config.ServicePlan {
	type Message struct {
		Rows []config.ServicePlan `json:"rows"`
	}
	var m Message

	client := http.Client{
		Timeout: 300 * time.Millisecond,
	}
	response, err := client.Get(cluster.Conf.ProvServicePlanRegistry)
	if err != nil {
		cluster.LogPrintf(LvlErr, "GetServicePlans: %s %s", cluster.Conf.ProvServicePlanRegistry, err)
		return nil
	}
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		cluster.LogPrintf(LvlErr, "GetServicePlans: %s", err)
		return nil
	}
	err = json.Unmarshal(contents, &m)
	if err != nil {
		cluster.LogPrintf(LvlErr, "GetServicePlans  %s", err)
		return nil
	}
	/*
		r := make([]config.ServicePlan, 0, len(m.Rows))
		for _, value := range m.Rows {
			r = append(r, value)
		}
		/*sort.Sort(QueryRuleSorter(r))*/
	return m.Rows
}

func (cluster *Cluster) GetClientCertificates() map[string]string {
	certs := make(map[string]string)
	clientCert, err := misc.ReadFile(cluster.WorkingDir + "/client-cert.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs
	}
	clientkey, err := misc.ReadFile(cluster.WorkingDir + "/client-key.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs
	}
	caCert, err := misc.ReadFile(cluster.WorkingDir + "/ca-cert.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs
	}
	certs["clientCert"] = clientCert
	certs["clientKey"] = clientkey
	certs["caCert"] = caCert
	return certs
}
