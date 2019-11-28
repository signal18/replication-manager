// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/misc"
)

type APIUser struct {
	User     string          `json:"user"`
	Password string          `json:"-"`
	Grants   map[string]bool `json:"grants"`
}

const (
	GrantDBStart                string = "db-start"
	GrantDBStop                 string = "db-stop"
	GrantDBKill                 string = "db-kill"
	GrantDBOptimize             string = "db-optimize"
	GrantDBAnalyse              string = "db-analyse"
	GrantDBReplication          string = "db-replication"
	GrantDBBackup               string = "db-backup"
	GrantDBRestore              string = "db-restore"
	GrantDBReadOnly             string = "db-readonly"
	GrantDBLogs                 string = "db-logs"
	GrantDBShowVariables        string = "db-show-variables"
	GrantDBShowStatus           string = "db-show-status"
	GrantDBShowSchema           string = "db-show-schema"
	GrantDBShowProcess          string = "db-show-process"
	GrantDBShowLogs             string = "db-show-logs"
	GrantDBCapture              string = "db-capture"
	GrantDBMaintenance          string = "db-maintenance"
	GrantDBConfigCreate         string = "db-config-create"
	GrantDBConfigRessource      string = "db-config-ressource"
	GrantDBConfigFlag           string = "db-config-flag"
	GrantDBConfigGet            string = "db-config-get"
	GrantDBDebug                string = "db-debug"
	GrantClusterCreate          string = "cluster-create"
	GrantClusterDrop            string = "cluster-drop"
	GrantClusterCreateMonitor   string = "cluster-create-monitor"
	GrantClusterDropMonitor     string = "cluster-drop-monitor"
	GrantClusterFailover        string = "cluster-failover"
	GrantClusterSwitchover      string = "cluster-switchover"
	GrantClusterRolling         string = "cluster-rolling"
	GrantClusterSettings        string = "cluster-settings"
	GrantClusterGrant           string = "cluster-grant"
	GrantClusterChecksum        string = "cluster-checksum"
	GrantClusterSharding        string = "cluster-sharding"
	GrantClusterReplication     string = "cluster-replication"
	GrantClusterBench           string = "cluster-bench"
	GrantClusterTest            string = "cluster-test"
	GrantClusterTraffic         string = "cluster-traffic"
	GrantClusterShowBackups     string = "cluster-show-backups"
	GrantClusterShowRoutes      string = "cluster-show-routes"
	GrantClusterShowGraphs      string = "cluster-show-graphs"
	GrantClusterShowAgents      string = "cluster-show-agents"
	GrantClusterDebug           string = "cluster-debug"
	GrantProxyConfigCreate      string = "proxy-config-create"
	GrantProxyConfigGet         string = "proxy-config-get"
	GrantProxyConfigRessource   string = "proxy-config-ressource"
	GrantProxyConfigFlag        string = "proxy-config-flag"
	GrantProxyStart             string = "proxy-start"
	GrantProxyStop              string = "proxy-stop"
	GrantProvClusterProvision   string = "prov-cluster-provision"
	GrantProvClusterUnprovision string = "prov-cluster-unprovision"
	GrantProvProxyProvision     string = "prov-proxy-provision"
	GrantProvProxyUnprovision   string = "prov-proxy-unprovision"
	GrantProvDBProvision        string = "prov-db-provision"
	GrantProvDBUnprovision      string = "prov-db-unprovision"
	GrantProvSettings           string = "prov-settings"
	GrantProvCluster            string = "prov-cluster"
)

func (cluster *Cluster) LoadAPIUsers() error {

	k, err := crypto.ReadKey(cluster.Conf.MonitoringKeyPath)
	if err != nil {
		cluster.LogPrintf(LvlInfo, "No existing password encryption scheme in LoadAPIUsers")
		k = nil
	}
	credentials := strings.Split(cluster.Conf.APIUsers, ",")
	meUsers := make(map[string]APIUser)
	for _, credential := range credentials {
		var newapiuser APIUser

		newapiuser.User, newapiuser.Password = misc.SplitPair(credential)
		if k != nil {
			p := crypto.Password{Key: k}
			p.CipherText = newapiuser.Password
			p.Decrypt()
			newapiuser.Password = p.PlainText
		}
		usersAllowACL := strings.Split(cluster.Conf.APIUsersACLAllow, ",")
		newapiuser.Grants = make(map[string]bool)
		for _, userACL := range usersAllowACL {
			useracl, listacls := misc.SplitPair(userACL)
			acls := strings.Split(listacls, " ")
			if useracl == newapiuser.User {
				for key, value := range cluster.Grants {
					found := false
					for _, acl := range acls {
						if strings.HasPrefix(key, acl) && acl != "" {
							found = true
							break
						}
					}
					newapiuser.Grants[value] = found
				}
			}
		}
		usersDiscardACL := strings.Split(cluster.Conf.APIUsersACLDiscard, ",")
		for _, userACL := range usersDiscardACL {
			useracl, listacls := misc.SplitPair(userACL)
			acls := strings.Split(listacls, " ")
			if useracl == newapiuser.User {
				for _, acl := range acls {
					newapiuser.Grants[acl] = false
				}
			}
		}
		meUsers[newapiuser.User] = newapiuser
	}
	cluster.APIUsers = meUsers
	return nil
}

func (cluster *Cluster) IsURLPassDatabasesACL(strUser string, URL string) bool {
	/*
		missing "/api/clusters/{clusterName}/servers/{serverName}/service-opensvc"
	*/

	if cluster.APIUsers[strUser].Grants[GrantProvDBProvision] {
		if strings.Contains(URL, "/actions/provision") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProvDBUnprovision] {
		if strings.Contains(URL, "/actions/unprovision") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBStart] {
		if strings.Contains(URL, "/actions/start") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBStop] {
		if strings.Contains(URL, "/actions/stop") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBKill] {
		if strings.Contains(URL, "/actions/kill") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBOptimize] {
		if strings.Contains(URL, "/actions/analyze-pfs") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBAnalyse] {
		if strings.Contains(URL, "/actions/analyze-pfs") {
			return true
		}
		if strings.Contains(URL, "/actions/analyze-slowlog") {
			return true
		}
		if strings.Contains(URL, "/actions/reset-pfs-queries") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBReplication] {
		if strings.Contains(URL, "/all-slaves-status") {
			return true
		}
		if strings.Contains(URL, "/master-status") {
			return true
		}
		if strings.Contains(URL, "actions/start-slave") {
			return true
		}
		if strings.Contains(URL, "actions/stop-slave") {
			return true
		}
		if strings.Contains(URL, "actions/skip-replication-event") {
			return true
		}
		if strings.Contains(URL, "actions/reset-master") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBBackup] {
		if strings.Contains(URL, "/action/backup-logical") {
			return true
		}
		if strings.Contains(URL, "/actions/backup-error-log") {
			return true
		}
		if strings.Contains(URL, "/actions/backup-physical") {
			return true
		}
		if strings.Contains(URL, "/actions/backup-slowquery-log") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBRestore] {
		if strings.Contains(URL, "/actions/reseed/") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBReadOnly] {
		if strings.Contains(URL, "actions/toogle-read-only") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBLogs] {
		if strings.Contains(URL, "/processlist") {
			return true
		}
		if strings.Contains(URL, "/status-innodb") {
			return true
		}
		if strings.Contains(URL, "/errorlog") {
			return true
		}
		if strings.Contains(URL, "/slow-queries") {
			return true
		}
		if strings.Contains(URL, "/query-response-time") {
			return true
		}
		if strings.Contains(URL, "/meta-data-locks") {
			return true
		}
		if strings.Contains(URL, "/digest-statements-pfs") {
			return true
		}
		if strings.Contains(URL, "/digest-statements-slow") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-sql-error-log") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-sql-error-log") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-query-response-time") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-meta-data-locks") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-slow-query-table") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-slow-query-capture") {
			return true
		}
		if strings.Contains(URL, "/actions/set-long-query-time") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-pfs-slow-query") {
			return true
		}
		if strings.Contains(URL, "/actions/toogle-slow-querys") {
			return true
		}
		if strings.Contains(URL, "actions/toogle-innodb-monitor") {
			return true
		}
		if strings.Contains(URL, "/actions/explain-pfs") {
			return true
		}
		if strings.Contains(URL, "/actions/explain-slowlog") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBCapture] {
		if strings.Contains(URL, "/actions/toogle-slow-query-capture") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBMaintenance] {
		if strings.Contains(URL, "/actions/optimize") {
			return true
		}
		if strings.Contains(URL, "/actions/maintenance") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBConfigCreate] {
		if strings.Contains(URL, "/kill") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBConfigGet] {
		if strings.Contains(URL, "/kill") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBConfigFlag] {
		if strings.Contains(URL, "/kill") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBShowVariables] {
		if strings.Contains(URL, "/variables") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBShowSchema] {
		if strings.Contains(URL, "/tables") {
			return true
		}
		if strings.Contains(URL, "/vtables") {
			return true
		}
		if strings.Contains(URL, "/tables") {
			return true
		}
		if strings.Contains(URL, "/schemas") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBShowStatus] {
		if strings.Contains(URL, "/status") {
			return true
		}
		if strings.Contains(URL, "/status-delta") {
			return true
		}
	}
	cluster.LogPrintf(LvlInfo, "ACL check failed for user %s : %s ", strUser, URL)
	return false
}

func (cluster *Cluster) IsURLPassProxiesACL(strUser string, URL string) bool {

	if cluster.APIUsers[strUser].Grants[GrantProvProxyProvision] {
		if strings.Contains(URL, "/actions/provision") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProvProxyUnprovision] {
		if strings.Contains(URL, "/actions/unprovision") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProxyStart] {
		if strings.Contains(URL, "/actions/start") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProxyStop] {
		if strings.Contains(URL, "/actions/stop") {
			return true
		}
	}

	return false
}

func (cluster *Cluster) IsURLPassACL(strUser string, URL string) bool {
	switch URL {
	case "/api/login":
		return true
	case "/api/clusters":
		return true
	case "/api/monitor":
		return true
	case "/api/clusters/" + cluster.Name:
		return true
	}
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/servers") {
		return cluster.IsURLPassDatabasesACL(strUser, URL)
	}
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/proxies") {
		return cluster.IsURLPassProxiesACL(strUser, URL)
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterSharding] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/schema") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterShowBackups] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/backups") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterShowRoutes] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/queryrules") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterCreateMonitor] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/addserver") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterSwitchover] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/switchover") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterTraffic] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/stop-traffic") {
			return true

		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/start-traffic") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterBench] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/sysbench") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterTest] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/sysbench") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterFailover] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/failover") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterReplication] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/replication/bootstrap") {
			return true
		}

		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/replication/cleanup") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterRolling] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/optimize") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/rolling") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantDBConfigFlag] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/drop-db-tag") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/add-db-tag") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProxyConfigFlag] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/drop-proxy-tag") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/add-proxy-tag") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterSettings] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/reload") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/switch") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/set") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/settings/actions/reset-failover-control") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterChecksum] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/actions/checksum-all-tables") {
			return true
		}
	}

	if cluster.APIUsers[strUser].Grants[GrantProvCluster] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/services/actions/provision") {
			return true
		}
		if strings.Contains(URL, "/api/clusters/actions/add") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantProvClusterUnprovision] {
		if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/services/actions/unprovision") {
			return true
		}
	}
	if cluster.APIUsers[strUser].Grants[GrantClusterCreate] {
		if strings.Contains(URL, "/api/clusters/actions/add") {
			return true
		}
	}
	/*	case cluster.APIUsers[strUser].Grants[GrantClusterGrant] == true:
			return false
		case cluster.APIUsers[strUser].Grants[GrantClusterDropMonitor] == true:
			return false
		case cluster.APIUsers[strUser].Grants[GrantClusterCreate] == true:
			return false
	*/

	cluster.LogPrintf(LvlInfo, "ACL check failed for user %s : %s ", strUser, URL)
	return false
}
