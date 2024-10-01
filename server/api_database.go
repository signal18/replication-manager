// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/auth"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
)

func (repman *ReplicationManager) GetDatabasePublicRoutes() []Route {
	return []Route{
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/is-master", repman.handlerMuxServersIsMasterStatus},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/is-slave", repman.handlerMuxServersIsSlaveStatus},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", repman.handlerMuxServersPortIsMasterStatus},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-restart", repman.handlerMuxServerNeedRestart},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-reprov", repman.handlerMuxServerNeedReprov},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-prov", repman.handlerMuxServerNeedProv},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-unprov", repman.handlerMuxServerNeedUnprov},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-start", repman.handlerMuxServerNeedStart},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-stop", repman.handlerMuxServerNeedStop},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-config-change", repman.handlerMuxServerNeedConfigChange},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/need-rolling-reprov", repman.handlerMuxServerNeedRollingReprov},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/need-rolling-restart", repman.handlerMuxServerNeedRollingRestart},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", repman.handlerMuxServersPortIsSlaveStatus},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/config", repman.handlerMuxServersPortConfig},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/write-log/{task}", repman.handlerMuxServersWriteLog},
	}
}

func (repman *ReplicationManager) GetDatabaseProtectedRoutes() []Route {
	return []Route{
		// PROTECTED ENDPOINTS FOR SERVERS
		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", repman.handlerMuxServersPortBackup},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/processlist", repman.handlerMuxServerProcesslist},
		{auth.ClusterPermission, config.GrantDBShowVariables, "/api/clusters/{clusterName}/servers/{serverName}/variables", repman.handlerMuxServerVariables},
		{auth.ClusterPermission, config.GrantDBShowStatus, "/api/clusters/{clusterName}/servers/{serverName}/status", repman.handlerMuxServerStatus},
		{auth.ClusterPermission, config.GrantDBShowStatus, "/api/clusters/{clusterName}/servers/{serverName}/status-delta", repman.handlerMuxServerStatusDelta},

		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/errorlog", repman.handlerMuxServerErrorLog},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/slow-queries", repman.handlerMuxServerSlowLog},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/digest-statements-pfs", repman.handlerMuxServerPFSStatements},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/digest-statements-slow", repman.handlerMuxServerPFSStatementsSlowLog},
		{auth.ClusterPermission, config.GrantDBShowSchema, "/api/clusters/{clusterName}/servers/{serverName}/tables", repman.handlerMuxServerTables},
		{auth.ClusterPermission, config.GrantDBShowSchema, "/api/clusters/{clusterName}/servers/{serverName}/vtables", repman.handlerMuxServerVTables},
		{auth.ClusterPermission, config.GrantDBShowSchema, "/api/clusters/{clusterName}/servers/{serverName}/schemas", repman.handlerMuxServerSchemas},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/status-innodb", repman.handlerMuxServerInnoDBStatus},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/all-slaves-status", repman.handlerMuxServerAllSlavesStatus},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/master-status", repman.handlerMuxServerMasterStatus},
		{auth.ClusterPermission, config.GrantProvDBProvision, "/api/clusters/{clusterName}/servers/{serverName}/service-opensvc", repman.handlerMuxGetDatabaseServiceConfig},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/meta-data-locks", repman.handlerMuxServerMetaDataLocks},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/query-response-time", repman.handlerMuxServerQueryResponseTime},

		{auth.ClusterPermission, config.GrantDBStart, "/api/clusters/{clusterName}/servers/{serverName}/actions/start", repman.handlerMuxServerStart},
		{auth.ClusterPermission, config.GrantDBStop, "/api/clusters/{clusterName}/servers/{serverName}/actions/stop", repman.handlerMuxServerStop},
		{auth.ClusterPermission, config.GrantDBMaintenance, "/api/clusters/{clusterName}/servers/{serverName}/actions/maintenance", repman.handlerMuxServerMaintenance},
		{auth.ClusterPermission, config.GrantDBMaintenance, "/api/clusters/{clusterName}/servers/{serverName}/actions/set-maintenance", repman.handlerMuxServerSetMaintenance},
		{auth.ClusterPermission, config.GrantDBMaintenance, "/api/clusters/{clusterName}/servers/{serverName}/actions/del-maintenance", repman.handlerMuxServerDelMaintenance},
		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/servers/{serverName}/actions/switchover", repman.handlerMuxServerSwitchover},
		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/servers/{serverName}/actions/set-prefered", repman.handlerMuxServerSetPrefered},
		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/servers/{serverName}/actions/set-unrated", repman.handlerMuxServerSetUnrated},
		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/servers/{serverName}/actions/set-ignored", repman.handlerMuxServerSetIgnored},
		{auth.ClusterPermission, config.GrantProvDBProvision, "/api/clusters/{clusterName}/servers/{serverName}/actions/unprovision", repman.handlerMuxServerUnprovision},
		{auth.ClusterPermission, config.GrantProvDBUnprovision, "/api/clusters/{clusterName}/servers/{serverName}/actions/provision", repman.handlerMuxServerProvision},

		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/actions/backup-physical", repman.handlerMuxServerBackupPhysical},
		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/actions/backup-logical", repman.handlerMuxServerBackupLogical},

		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/actions/backup-error-log", repman.handlerMuxServerBackupErrorLog},
		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/actions/backup-slowquery-log", repman.handlerMuxServerBackupSlowQueryLog},

		{auth.ClusterPermission, config.GrantDBOptimize, "/api/clusters/{clusterName}/servers/{serverName}/actions/optimize", repman.handlerMuxServerOptimize},
		{auth.ClusterPermission, config.GrantDBRestore, "/api/clusters/{clusterName}/servers/{serverName}/actions/reseed/{backupMethod}", repman.handlerMuxServerReseed},
		{auth.ClusterPermission, config.GrantDBRestore, "/api/clusters/{clusterName}/servers/{serverName}/actions/pitr", repman.handlerMuxServerPITR},
		{auth.ClusterPermission, config.GrantDBRestore, "/api/clusters/{clusterName}/servers/{serverName}/actions/reseed-cancel", repman.handlerMuxServerReseedCancel},
		{auth.ClusterPermission, config.GrantClusterProcess, "/api/clusters/{clusterName}/servers/{serverName}/actions/job-cancel/{task}", repman.handlerMuxServersTaskCancel},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-innodb-monitor", repman.handlerMuxSetInnoDBMonitor},
		{auth.ClusterPermission, config.GrantDBMaintenance, "/api/clusters/{clusterName}/servers/{serverName}/actions/wait-innodb-purge", repman.handlerWaitInnoDBPurge},
		{auth.ClusterPermission, config.GrantDBCapture, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-capture", repman.handlerMuxSwitchSlowQueryCapture},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-table", repman.handlerMuxSwitchSlowQueryTable},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query", repman.handlerMuxSwitchSlowQuery},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-pfs-slow-query", repman.handlerMuxSwitchPFSSlowQuery},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/set-long-query-time/{queryTime}", repman.handlerMuxSwitchSetLongQueryTime},

		{auth.ClusterPermission, config.GrantDBReadOnly, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-read-only", repman.handlerMuxServerSwitchReadOnly},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-meta-data-locks", repman.handlerMuxServerSwitchMetaDataLocks},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-query-response-time", repman.handlerMuxServerSwitchQueryResponseTime},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-sql-error-log", repman.handlerMuxServerSwitchSqlErrorLog},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/actions/reset-master", repman.handlerMuxServerResetMaster},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/actions/reset-slave-all", repman.handlerMuxServerResetSlaveAll},
		{auth.ClusterPermission, config.GrantDBBackup, "/api/clusters/{clusterName}/servers/{serverName}/actions/flush-logs", repman.handlerMuxServerFlushLogs},
		{auth.ClusterPermission, config.GrantDBAnalyse, "/api/clusters/{clusterName}/servers/{serverName}/actions/reset-pfs-queries", repman.handlerMuxServerResetPFSQueries},

		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/actions/start-slave", repman.handlerMuxServerStartSlave},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/actions/stop-slave", repman.handlerMuxServerStopSlave},
		{auth.ClusterPermission, config.GrantDBReplication, "/api/clusters/{clusterName}/servers/{serverName}/actions/skip-replication-event", repman.handlerMuxSkipReplicationEvent},
		{auth.ClusterPermission, config.GrantClusterProcess, "/api/clusters/{clusterName}/servers/{serverName}/actions/run-jobs", repman.handlerMuxRunJobs},
		{auth.ClusterPermission, config.GrantDBKill, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-thread", repman.handlerMuxQueryKillThread},
		{auth.ClusterPermission, config.GrantDBKill, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-query", repman.handlerMuxQueryKillQuery},

		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-pfs", repman.handlerMuxQueryExplainPFS},
		{auth.ClusterPermission, config.GrantDBLogs, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-slowlog", repman.handlerMuxQueryExplainSlowLog},
		{auth.ClusterPermission, config.GrantDBAnalyse, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-pfs", repman.handlerMuxQueryAnalyzePFS},
		{auth.ClusterPermission, config.GrantDBAnalyse, "/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-slowlog", repman.handlerMuxQueryAnalyzePFS},
	}
}

func (repman *ReplicationManager) handlerMuxQueryKillQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.KillQuery(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryKillThread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.KillThread(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryExplainPFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {

			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _ := node.GetQueryExplainPFS(vars["queryDigest"])
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryExplainSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _ := node.GetQueryExplainSlowLog(vars["queryDigest"])
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryAnalyzePFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.GetQueryAnalyzePFS(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.StopDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupPhysical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupPhysical()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupLogical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			go node.JobBackupLogical()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobOptimize()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerReseed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			if vars["backupMethod"] == "logicalbackup" {
				err := node.JobReseedLogicalBackup("default")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "logical reseed restore failed %s", err)
					http.Error(w, "Error reseed logical backup", 500)
					return
				}
			}
			if vars["backupMethod"] == "logicalmaster" {
				err := node.RejoinDirectDump()
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "direct reseed restore failed %s", err)
				}
			}
			if vars["backupMethod"] == "physicalbackup" {
				err := node.JobReseedPhysicalBackup("default")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "physical reseed restore failed %s", err)
				}
			}

		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerPITR(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			var formPit config.PointInTimeMeta
			// This will always true for making standalone
			formPit.IsInPITR = true
			err := json.NewDecoder(r.Body).Decode(&formPit)
			if err != nil {
				http.Error(w, fmt.Sprintf("Decode error :%s", err.Error()), http.StatusInternalServerError)
				return
			}

			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Requesting PITR on node %s", node.URL)

			err = node.ReseedPointInTime(formPit)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR on %s failed, err: %s", node.URL, err.Error())
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "PITR on %s failed, err: %s", node.URL, err.Error())
				http.Error(w, fmt.Sprintf("PITR error :%s", err.Error()), http.StatusInternalServerError)
				return
			} else {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR on %s finished successfully", node.URL)
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "PITR on %s finished successfully", node.URL)
			}

			marshal, err := json.MarshalIndent(formPit, "", "\t")
			if err != nil {
				http.Error(w, fmt.Sprintf("Encode error :%s", err.Error()), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ApiResponse{Data: string(marshal), Success: true})
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerReseedCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			tasks := []string{"reseedmariabackup", "reseedxtrabackup", "flashbackmariabackup", "flashbackxtrabackup"}
			err := node.JobsCancelTasks(false, tasks...)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error canceling %s task: %s", vars["task"], err.Error()), 500)
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupErrorLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupSlowQueryLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupSlowQueryLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.SwitchServerMaintenance(node.ServerID)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSetMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SetMaintenance()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerDelMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.DelMaintenance()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive switchover request")
			savedPrefMaster := mycluster.GetPreferedMasterList()
			if mycluster.IsMasterFailed() {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Master failed, cannot initiate switchover")
				http.Error(w, "Leader is failed can not promote", http.StatusBadRequest)
				return
			}
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API force for prefered leader: %s", node.URL)
			mycluster.SetPrefMaster(node.URL)
			mycluster.MasterFailover(false)
			mycluster.SetPrefMaster(savedPrefMaster)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSetPrefered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as prefered request")
			mycluster.AddPrefMaster(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSetUnrated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as unrated request")
			mycluster.RemovePrefMaster(node)
			mycluster.RemoveIgnoreSrv(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSetIgnored(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive request: set node as ignored")
			mycluster.AddIgnoreSrv(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerWaitInnoDBPurge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.WaitInnoDBPurge()
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

func (repman *ReplicationManager) handlerMuxServerSwitchReadOnly(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchReadOnly()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchMetaDataLocks(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchMetaDataLocks()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchQueryResponseTime(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchQueryResponseTime()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchSqlErrorLog(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSqlErrorLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStartSlave(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StartSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStopSlave(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StopSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerResetSlaveAll(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StopSlave()
			node.ResetSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
func (repman *ReplicationManager) handlerMuxServerFlushLogs(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.FlushLogs()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerResetMaster(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.ResetMaster()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerResetPFSQueries(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.ResetPFSQueries()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQueryCapture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCapture()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
func (repman *ReplicationManager) handlerMuxSwitchPFSSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCapturePFS()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQuery()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQueryTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCaptureMode()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSetLongQueryTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SetLongQueryTime(vars["queryTime"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.StartDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.InitDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.UnprovisionDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/is-master serverName-is-master
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//     schema:
//       type: string
//     examples:
//       text/plain: 200 -Valid Master!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Master
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Master!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServersIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return
		} else {

			w.Write([]byte("503 -Not a Valid Master!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "No restart needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasReprovCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasReprovCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedProv(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasProvisionCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasProvisionCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedUnprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasUnprovisionCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasUnprovisionCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			if node.HasWaitStartCookie() {
				w.Write([]byte("200 -Need start!"))
				node.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No start needed!"))

		} else if proxy != nil {
			if proxy.HasWaitStartCookie() {
				w.Write([]byte("200 -Need start!"))
				proxy.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No start needed!"))
			//http.Error(w, "No start needed", 501)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
		}

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasWaitStopCookie() {
				node.DelWaitStopCookie()
				w.Write([]byte("200 -Need stop!"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No stop needed!"))

		} else if proxy != nil {
			if proxy.HasWaitStopCookie() {
				w.Write([]byte("200 -Need stop!"))
				proxy.DelWaitStopCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No stop needed!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
		}

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedConfigChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			if node.HasConfigCookie() {
				w.Write([]byte("200 -Need config change!"))
				node.DelConfigCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No config change needed!"))

		} else if proxy != nil {
			if proxy.HasConfigCookie() {
				w.Write([]byte("200 -Need config change!"))
				proxy.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No config change needed!"))
			//http.Error(w, "No start needed", 501)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
		}

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedRollingReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if mycluster.HasRequestDBRollingReprov() {
			w.Write([]byte("200 -Need rolling reprov!"))
			return
		}
		w.Write([]byte("503 -No rooling reprov needed!"))
		http.Error(w, "Encoding error", 503)

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if mycluster.HasRequestDBRollingRestart() {
			w.Write([]byte("200 -Need rolling restart!"))
			return
		}
		w.Write([]byte("503 -No rooling reprov restart!"))
		http.Error(w, "Encoding error", 503)

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	// Not used anymore
			// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			// 	http.Error(w, "No valid ACL", 403)
			// 	return
			// }*/
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Node not Found!"))
			return
		}
		if node != nil && mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Master!"))
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/is-slave serverName-is-slave
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//     schema:
//       type: string
//     examples:
//       text/plain: 200 -Valid Slave!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Slave!
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Slave!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServersIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	// Not used anymore
			// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			// 	http.Error(w, "No valid ACL", 403)
			// 	return
			// }*/
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXServersReadOnMaster)) {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Slave!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
				http.Error(w, "No valid ACL", 403)
				return
			}*/
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXServersReadOnMaster)) {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			//	w.WriteHeader(http.StatusInternalServerError)
			http.Error(w, "-Not a Valid Slave!", 503)
			//	w.Write([]byte("503 -Not a Valid Slave!"))
			return
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node.IsDown() == false && node.IsMaintenance == false {
			go node.JobBackupPhysical()
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("503 -Not a Valid Slave! Cluster IsActive=%t IsDown=%t IsMaintenance=%t HasReplicationIssue=%t ", mycluster.IsActive(), node.IsDown(), node.IsMaintenance, node.HasReplicationIssue())))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if mycluster.Conf.APISecureConfig {
			if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
				http.Error(w, "No valid ACL", 403)
				return
			}
		}
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			node.GetDatabaseConfig()
			data, err := os.ReadFile(string(node.Datadir + "/config.tar.gz"))
			if err != nil {
				r.URL.Path = r.URL.Path + ".tar.gz"
				w.WriteHeader(404)
				w.Write([]byte("404 Something went wrong reading : " + string(node.Datadir+"/config.tar.gz") + " " + err.Error() + " - " + http.StatusText(404)))
				return
			}
			w.Write(data)

		} else if proxy != nil {
			proxy.GetProxyConfig()
			data, err := os.ReadFile(string(proxy.GetDatadir() + "/config.tar.gz"))
			if err != nil {
				r.URL.Path = r.URL.Path + ".tar.gz"
				w.WriteHeader(404)
				w.Write([]byte("404 Something went wrong reading : " + string(proxy.GetDatadir()+"/config.tar.gz") + " " + err.Error() + " - " + http.StatusText(404)))

				return
			}
			w.Write(data)
		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}

func (repman *ReplicationManager) handlerMuxServersWriteLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var mod int
		switch vars["task"] {
		case "mariabackup", "xtrabackup", "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariadbackup":
			mod = config.ConstLogModBackupStream
		case "error", "slowquery", "zfssnapback", "optimize", "reseedmysqldump", "flashbackmysqldump", "stop", "restart", "start":
			mod = config.ConstLogModTask
		default:
			http.Error(w, "Bad request: Task is not registered", http.StatusBadRequest)
			return
		}

		var decodedData struct {
			Data string `json:"data"`
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode reading body :%s", err.Error()), http.StatusBadRequest)
			return
		}

		err = json.Unmarshal(body, &decodedData)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode body :%s. Err: %s", string(body), err.Error()), http.StatusBadRequest)
			return
		}

		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			// Decrypt the encrypted data
			key := crypto.GetSHA256Hash(node.Pass)
			iv := crypto.GetMD5Hash(node.Pass)

			err := node.WriteJobLogs(mod, decodedData.Data, key, iv, vars["task"])
			if err != nil {
				http.Error(w, "Error decrypting data : "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ApiResponse{Data: "Message logged", Success: true})

		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}

func (repman *ReplicationManager) handlerMuxServerProcesslist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetProcessList()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerMetaDataLocks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetMetaDataLocks()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerQueryResponseTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetQueryResponseTime()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetErrorLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetSlowLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerPFSStatements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetPFSStatements()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerPFSStatementsSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetPFSStatementsSlowLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetVariables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStatusDelta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetStatusDelta()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetTables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerVTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetVTables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxRunJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.JobRunViaSSH()
			if err != nil {
				http.Error(w, "Encoding running job", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSchemas(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _, _ := node.GetSchemas()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerInnoDBStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetInnoDBStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerAllSlavesStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetAllSlavesStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/master-status serverName-master-status
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//   '403':
//     description: No valid ACL
//     schema:
//       type: string
//     examples:
//       text/plain: No valid ACL
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: Encoding error
//     schema:
//       type: string
//     examples:
//       text/plain: Encoding error
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Server!
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Server!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServerMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetMasterStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSkipReplicationEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			node.SkipReplicationEvent()
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSetInnoDBMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			node.SetInnoDBMonitor()
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxGetDatabaseServiceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			res := mycluster.GetDatabaseServiceConfig(node)
			w.Write([]byte(res))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersTaskCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.JobsCancelTasks(true, vars["task"])
			if err != nil {
				http.Error(w, fmt.Sprintf("Error canceling %s task: %s", vars["task"], err.Error()), 500)
			}
		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}
