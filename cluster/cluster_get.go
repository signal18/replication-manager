// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"io/ioutil"
	"regexp"
	"sort"
	"strconv"
	"strings"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
	"github.com/siddontang/go/log"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) GetCrcTable() *crc64.Table {
	return cluster.crcTable
}

func (cluster *Cluster) getDumpParameter() string {
	dump_param := cluster.Conf.BackupMysqldumpOptions
	if cluster.master != nil {
		if !cluster.master.IsMariaDB() {
			re, err := regexp.Compile("--system=all")
			if err != nil {
				return dump_param
			}
			dump_param = re.ReplaceAllString(dump_param, "")
			if cluster.master.HasMySQLGTID() {
				dump_param = strings.ReplaceAll(dump_param, "--master-data=1", "")
			}

		}
	}
	return dump_param
}

func (cluster *Cluster) GetShareDir() string {
	return cluster.Conf.ShareDir
}

func (cluster *Cluster) GetMysqlDumpPath() string {
	if cluster.Conf.BackupMysqldumpPath == "" {
		return cluster.GetShareDir() + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysqldump"
	}
	return cluster.Conf.BackupMysqldumpPath
}

func (cluster *Cluster) GetMyDumperPath() string {
	if cluster.Conf.BackupMyDumperPath == "" {
		return cluster.GetShareDir() + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mydumper"
	}
	return cluster.Conf.BackupMyDumperPath
}

func (cluster *Cluster) GetMyLoaderPath() string {
	if cluster.Conf.BackupMyDumperPath == "" {
		return cluster.GetShareDir() + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/myloader"
	}
	return cluster.Conf.BackupMyLoaderPath
}

func (cluster *Cluster) GetMysqlBinlogPath() string {
	if cluster.Conf.BackupMysqlbinlogPath == "" {
		return cluster.GetShareDir() + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysqlbinlog"
	}
	return cluster.Conf.BackupMysqlbinlogPath
}

func (cluster *Cluster) GetMysqlclientPath() string {
	if cluster.Conf.BackupMysqlclientPath == "" {
		return cluster.GetShareDir() + "/" + cluster.Conf.GoArch + "/" + cluster.Conf.GoOS + "/mysql"
	}
	return cluster.Conf.BackupMysqlclientPath
}

func (cluster *Cluster) GetDomain() string {
	if cluster.Conf.ProvNetCNI {
		return "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
	}
	return ""
}

func (cluster *Cluster) GetOrchestrator() string {
	return cluster.Conf.ProvOrchestrator
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
	cluster.StateMachine.SetSla(clsave.SLA)
	cluster.StateMachine.SetMasterUpAndSyncRestart()

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
	return cluster.StateMachine
}

func (cluster *Cluster) GetMasterFailCount() int {
	return cluster.master.FailCount
}

func (cluster *Cluster) GetFailoverCtr() int {
	return cluster.FailoverCtr
}

func (cluster *Cluster) GetIncludeDir() string {
	return cluster.Conf.Include
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
		if server != nil {
			qps += server.QPS
		}
	}
	return qps
}

func (cluster *Cluster) GetConnections() int {
	allconns := 0
	for _, server := range cluster.Servers {
		if server != nil {
			if conns, ok := server.Status["THREADS_RUNNING"]; ok {
				numconns, _ := strconv.Atoi(conns)
				allconns += numconns
			}
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
	user, _ := misc.SplitPair(cluster.encryptedFlags["db-servers-credential"].Value)
	return user
}

func (cluster *Cluster) GetDbPass() string {
	_, pass := misc.SplitPair(cluster.encryptedFlags["db-servers-credential"].Value)
	return pass
}

func (cluster *Cluster) GetRplUser() string {
	user, _ := misc.SplitPair(cluster.encryptedFlags["replication-credential"].Value)
	return user
}

func (cluster *Cluster) GetRplPass() string {
	_, pass := misc.SplitPair(cluster.encryptedFlags["replication-credential"].Value)
	return pass
}

func (cluster *Cluster) GetShardUser() string {
	user, _ := misc.SplitPair(cluster.encryptedFlags["shardproxy-credential"].Value)
	return user
}

func (cluster *Cluster) GetShardPass() string {
	_, pass := misc.SplitPair(cluster.encryptedFlags["shardproxy-credential"].Value)
	return pass
}
func (cluster *Cluster) GetMonitorWriteHearbeatUser() string {
	user, _ := misc.SplitPair(cluster.encryptedFlags["monitoring-write-heartbeat-credential"].Value)
	return user
}

func (cluster *Cluster) GetMonitorWriteHeartbeatPass() string {
	_, pass := misc.SplitPair(cluster.encryptedFlags["monitoring-write-heartbeat-credential"].Value)
	return pass
}

func (cluster *Cluster) GetOnPremiseSSHUser() string {
	user, _ := misc.SplitPair(cluster.encryptedFlags["onpremise-ssh-credential"].Value)
	return user
}

func (cluster *Cluster) GetOnPremiseSSHPass() string {
	_, pass := misc.SplitPair(cluster.encryptedFlags["onpremise-ssh-credential"].Value)
	return pass
}

func (cluster *Cluster) GetDecryptedValue(key string) string {
	return cluster.encryptedFlags[key].Value
}

func (cluster *Cluster) GetStatus() bool {
	return cluster.StateMachine.IsFailable()
}

func (cluster *Cluster) GetGroupReplicationWhiteList() string {
	var gcomms []string
	for _, server := range cluster.Servers {
		gcomms = append(gcomms, server.Host)
	}
	return strings.Join(gcomms, ",")
}

func (cluster *Cluster) GetGComm() string {
	var gcomms []string
	for _, server := range cluster.Servers {
		if cluster.Conf.MultiMasterWsrep {
			gcomms = append(gcomms, server.Host+":"+strconv.Itoa(cluster.Conf.MultiMasterWsrepPort))
		} else {
			gcomms = append(gcomms, server.Host+":"+strconv.Itoa(cluster.Conf.MultiMasterGrouprepPort))
		}

	}
	//	For bootrap galera cluster on first node
	if cluster.AllServersFailed() && cluster.GetTopology() == topoMultiMasterWsrep {
		return ""
	}
	if cluster.GetTopology() == topoMultiMasterWsrep {
		return strings.Join(gcomms, ",") + "?pc.wait_prim=yes"
	}
	return strings.Join(gcomms, ",")
}

func (cluster *Cluster) getOnePreferedMaster() *ServerMonitor {
	if cluster.Conf.PrefMaster == "" {
		return nil
	}
	for _, server := range cluster.Servers {
		if cluster.Conf.LogLevel > 2 {
			cluster.LogPrintf(LvlDbg, "Lookup if server: %s is preferred master: %s", server.URL, cluster.Conf.PrefMaster)
		}
		if server.IsPrefered() {
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
			cluster.LogPrintf(LvlDbg, "Check for relay server %s: relay: %t", server.URL, server.IsRelay)
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

func (cluster *Cluster) GetProxyFromURL(url string) DatabaseProxy {
	for _, proxy := range cluster.Proxies {
		if strings.Contains(url, ":") {
			if proxy.GetHost()+":"+proxy.GetPort() == url {
				return proxy
			}
		} else {
			if proxy.GetHost() == url {
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
	//1	cluster.LogPrintf(LvlInfo, "%d ", len(cluster.Servers))

	for _, server := range cluster.Servers {
		if server == nil {
			return nil
		}
		//	cluster.LogPrintf(LvlInfo, "%s ", server.State)
		//	cluster.LogPrintf(LvlInfo, "%t ", server.PreferedBackup)

		if server.State != stateFailed && server.PreferedBackup {
			return server
		}
	}
	if cluster.master != nil {
		return cluster.master
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
		ret[i] = server.GetId()
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
	} else if cluster.Conf.MultiMasterGrouprep {
		cluster.Conf.Topology = topoMultiMasterGrouprep
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
	} else if cluster.Conf.ActivePassive {
		cluster.Conf.Topology = topoActivePassive
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

/*
	func (cluster *Cluster) GetDatabaseTags() []string {
		return strings.Split(cluster.Conf.ProvTags, ",")
	}

	func (cluster *Cluster) GetProxyTags() []string {
		return strings.Split(cluster.Conf.ProvProxTags, ",")
	}
*/
func (cluster *Cluster) GetCron() []cron.Entry {

	return cluster.scheduler.Entries()

}

func (cluster *Cluster) GetServerIndice(srv *ServerMonitor) int {
	for i, sv := range cluster.Servers {
		//	cluster.LogPrintf(LvlInfo, "HasServer:%s %s, %s %s", sv.Id, srv.Id, sv.URL, srv.URL)
		// id can not be used for checking equality because  same srv in different clusters
		if sv.URL == srv.URL {
			return i
		}
	}
	return 0
}

func (cluster *Cluster) getClusterByName(clname string) *Cluster {

	for _, c := range cluster.clusterList {
		if clname == c.GetName() {
			return c
		}
	}
	return nil
}

// GetClusterFromShardProxy return all clusters sharing same proxy
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
			cluster.LogPrintf(LvlDbg, "Discovering of a child cluster via ClusterHead %s replication source %s", c.Name, c.Conf.ClusterHead)
			clusters[c.Name] = c
		}
		// lopp over master multi source replication
		condidateclustermaster := c.GetMaster()
		if condidateclustermaster != nil && c.Name != cluster.Name {
			for _, rep := range condidateclustermaster.Replications {
				// is a source name has my cluster name or is any child cluster master point to my master
				if rep.ConnectionName.String == cluster.Name || (cluster.GetMaster() != nil && cluster.master.Host == rep.MasterHost.String && cluster.master.Port == rep.MasterPort.String) {
					cluster.LogPrintf(LvlDbg, "Discovering of a child cluster via multi source %s replication source %s", c.Name, rep.ConnectionName.String)
					clusters[c.Name] = c
				}
			}
		}
	}
	return clusters
}

func (cluster *Cluster) GetParentClusterFromReplicationSource(rep dbhelper.SlaveStatus) *Cluster {

	for _, c := range cluster.clusterList {
		if cluster.Name != c.Name {
			for _, srv := range c.Servers {
				if srv.Host == rep.MasterHost.String && srv.Port == rep.MasterPort.String {
					return c
				}
			}
		}
	}
	return nil
}

func (cluster *Cluster) GetRingChildServer(oldMaster *ServerMonitor) *ServerMonitor {
	for _, s := range cluster.Servers {
		if s.ServerID != cluster.oldMaster.ServerID {
			//cluster.LogPrintf(LvlDbg, "test %s failed %s", s.URL, cluster.oldMaster.URL)
			master, err := cluster.GetMasterFromReplication(s)
			if err == nil && master.ServerID == oldMaster.ServerID {
				return s
			}
		}
	}
	return nil
}

func (cluster *Cluster) GetRingParentServer(oldMaster *ServerMonitor) *ServerMonitor {
	ss, err := cluster.oldMaster.GetSlaveStatusLastSeen(cluster.oldMaster.ReplicationSourceName)
	if err != nil {
		return nil
	}
	return cluster.GetServerFromURL(ss.MasterHost.String + ":" + ss.MasterPort.String)
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

func (cluster *Cluster) GetBackups() []v3.Backup {
	return cluster.Backups
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

	file, err := ioutil.ReadFile(cluster.Conf.WorkingDir + "/serviceplan.json")
	if err != nil {
		log.Errorf("failed opening file because: %s", err.Error())
		return nil
	}

	err = json.Unmarshal([]byte(file), &m.Rows)
	if err != nil {
		cluster.LogPrintf(LvlErr, "GetServicePlans  %s", err)
		return nil
	}

	return m.Rows
}

func (cluster *Cluster) GetClientCertificates() (map[string]string, error) {
	certs := make(map[string]string)
	clientCert, err := misc.ReadFile(cluster.WorkingDir + "/client-cert.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs, fmt.Errorf("Can't load certificate: %w", err)
	}
	clientkey, err := misc.ReadFile(cluster.WorkingDir + "/client-key.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs, fmt.Errorf("Can't load certificate: %w", err)
	}
	caCert, err := misc.ReadFile(cluster.WorkingDir + "/ca-cert.pem")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't load certificate: %s", err)
		return certs, fmt.Errorf("Can't load certificate: %w", err)
	}
	certs["clientCert"] = clientCert
	certs["clientKey"] = clientkey
	certs["caCert"] = caCert
	return certs, nil
}

func (cluster *Cluster) GetVaultCredentials(client *vault.Client, path string, key string) (string, error) {
	if cluster.IsVaultUsed() && IsPath(path) {
		if cluster.Conf.VaultMode == VaultConfigStoreV2 {
			secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), path)

			if err != nil {
				return "", err
			}
			return secret.Data[key].(string), nil
		} else {
			secret, err := client.KVv1("").Get(context.Background(), path)
			if err != nil {
				return "", err
			}
			return secret.Data["username"].(string) + ":" + secret.Data["password"].(string), nil
		}
	}
	return "", errors.New("Failed to get vault credentials")
}

func (cluster *Cluster) GetVaultMonitorCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().User)

		if err != nil {
			return "", "", err
		}
		user, pass := misc.SplitPair(secret.Data["db-servers-credential"].(string))
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().User)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}
func (cluster *Cluster) GetVaultShardProxyCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().MdbsProxyCredential)

		if err != nil {
			return "", "", err
		}
		user, pass := misc.SplitPair(secret.Data["shardproxy-credential"].(string))
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().MdbsProxyCredential)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultProxySQLCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().MdbsProxyCredential)

		if err != nil {
			return "", "", err
		}
		user := secret.Data["proxysql-user"].(string)
		pass := secret.Data["proxysql-password"].(string)
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().MdbsProxyCredential)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultReplicationCredentials(client *vault.Client) (string, string, error) {
	if cluster.Conf.VaultMode == VaultConfigStoreV2 {
		secret, err := client.KVv2(cluster.Conf.VaultMount).Get(context.Background(), cluster.GetConf().RplUser)

		if err != nil {
			return "", "", err
		}
		user, pass := misc.SplitPair(secret.Data["replication-credential"].(string))
		return user, pass, nil
	} else {
		secret, err := client.KVv1("").Get(context.Background(), cluster.GetConf().User)
		if err != nil {
			return "", "", err
		}
		return secret.Data["username"].(string), secret.Data["password"].(string), nil
	}
}

func (cluster *Cluster) GetVaultConnection() (*vault.Client, error) {
	if cluster.IsVaultUsed() {

		cluster.LogPrintf(LvlDbg, "Vault AppRole Authentification")
		config := vault.DefaultConfig()

		config.Address = cluster.Conf.VaultServerAddr

		client, err := vault.NewClient(config)
		if err != nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}

		roleID := cluster.Conf.VaultRoleId
		secretID := &auth.SecretID{FromString: cluster.Conf.VaultSecretId}
		if roleID == "" || secretID == nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}

		appRoleAuth, err := auth.NewAppRoleAuth(
			roleID,
			secretID,
		)
		if err != nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}

		authInfo, err := client.Auth().Login(context.Background(), appRoleAuth)
		if err != nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}
		if authInfo == nil {
			cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], err), ErrFrom: "TOPO"})
			cluster.CanConnectVault = false
			cluster.errorConnectVault = err
			return nil, err
		}
		cluster.CanConnectVault = true
		return client, err
	}
	return nil, errors.New("Not using Vault")
}

func (cluster *Cluster) GetPasswordKey(MonitoringKeyPath string) ([]byte, error) {
	k, err := crypto.ReadKey(MonitoringKeyPath)
	if err != nil {
		k = nil
	}
	return k, err

}

func (cluster *Cluster) GetDecryptedPassword(key string, value string) string {
	if cluster.key != nil && strings.HasPrefix(value, "hash_") {
		value = strings.TrimLeft(value, "hash_")
		p := crypto.Password{Key: cluster.key}
		p.CipherText = value
		err := p.Decrypt()
		if err != nil {
			cluster.LogPrintf(LvlWarn, "Password decryption error on %s: %s", key, err)
			return value
		} else {
			return p.PlainText
		}

	}
	return value
}

func (cluster *Cluster) GetCloudSubDomain() string {
	return cluster.GetConf().Cloud18SubDomain
}

func (cluster *Cluster) GetUniqueId() uint64 {
	var sid uint64
	sid, _ = strconv.ParseUint(strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+cluster.GetCloudSubDomain()), cluster.GetCrcTable()), 10), 10, 64)
	return sid
}
