// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (server *ServerMonitor) GetEnv() map[string]string {

	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                                   server.ClusterGroup.Configurator.GetConfigDBCores(),
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":                            server.ClusterGroup.Configurator.GetConfigDBCores(),
		"%%ENV:SVC_CONF_ENV_MAX_CONNECTIONS%%":                      server.ClusterGroup.Configurator.GetConfigMaxConnections(),
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":                             string(server.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":                            string(server.Id[2:10]),
		"%%ENV:SERVER_IP%%":                                         misc.Unbracket(server.GetBindAddress()),
		"%%ENV:SERVER_HOST%%":                                       server.Host,
		"%%ENV:SERVER_PORT%%":                                       server.Port,
		"%%ENV:SVC_CONF_ENV_MYSQL_DATADIR%%":                        server.GetDatabaseDatadir(),
		"%%ENV:SVC_CONF_ENV_MYSQL_TMPDIR%%":                         server.GetConfigVariable("TMPDIR"),
		"%%ENV:SVC_CONF_ENV_MYSQL_SLAVE_LOAD_TMPDIR%%":              server.GetConfigVariable("SLAVE_LOAD_TMPDIR"),
		"%%ENV:SVC_CONF_ENV_MYSQL_LOG_ERROR%%":                      server.GetConfigVariable("LOG_ERROR"),
		"%%ENV:SVC_CONF_ENV_MYSQL_SLOW_QUERY_LOG_FILE%%":            server.GetConfigVariable("SLOW_QUERY_LOG_FILE"),
		"%%ENV:SVC_CONF_ENV_MYSQL_GENERAL_LOG_FILE%%":               server.GetConfigVariable("GENERAL_LOG_FILE"),
		"%%ENV:SVC_CONF_ENV_MYSQL_INNODB_DATA_HOME_DIR%%":           server.GetConfigVariable("INNODB_DATA_HOME_DIR"),
		"%%ENV:SVC_CONF_ENV_MYSQL_INNODB_LOG_GROUP_HOME_DIR%%":      server.GetConfigVariable("INNODB_LOG_GROUP_HOME_DIR"),
		"%%ENV:SVC_CONF_ENV_MYSQL_INNODB_UNDO_DIRECTORY%%":          server.GetConfigVariable("INNODB_UNDO_DIRECTORY"),
		"%%ENV:SVC_CONF_ENV_MYSQL_LOG_BIN%%":                        server.GetConfigVariable("LOG_BIN"),
		"%%ENV:SVC_CONF_ENV_MYSQL_LOG_BIN_INDEX%%":                  server.GetConfigVariable("LOG_BIN_INDEX"),
		"%%ENV:SVC_CONF_ENV_MYSQL_RELAY_LOG%%":                      server.GetConfigVariable("RELAY_LOG"),
		"%%ENV:SVC_CONF_ENV_MYSQL_RELAY_LOG_INDEX%%":                server.GetConfigVariable("RELAY_LOG_INDEX"),
		"%%ENV:SVC_CONF_ENV_MYSQL_ARIA_LOG_DIR_PATH%%":              server.GetConfigVariable("ARIA_LOG_DIR_PATH"),
		"%%ENV:SVC_CONF_ENV_MYSQL_CONFDIR%%":                        server.GetDatabaseConfdir(),
		"%%ENV:SVC_CONF_ENV_CLIENT_BASEDIR%%":                       server.GetDatabaseClientBasedir(),
		"%%ENV:SVC_CONF_ENV_MYSQL_SOCKET%%":                         server.GetDatabaseSocket(),
		"%%ENV:SVC_CONF_ENV_GROUP_REPLICATION_LOCAL_ADDRESS%%":      server.GetGroupReplicationLocalAddress(),
		"%%ENV:SVC_CONF_ENV_WSREP_NODE_ADDRESS%%":                   server.GetWsrepNodeAddress(),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%":                      server.ClusterGroup.GetDbUser(),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":                  server.ClusterGroup.GetDbPass(),
		"%%ENV:SVC_CONF_ENV_MAX_MEM%%":                              server.ClusterGroup.Configurator.GetConfigInnoDBBPSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_CACHE_SIZE%%":                    server.ClusterGroup.Configurator.GetConfigInnoDBBPSize(),
		"%%ENV:SVC_CONF_ENV_TOKUDB_CACHE_SIZE%%":                    server.ClusterGroup.Configurator.GetConfigTokuDBBufferSize(),
		"%%ENV:SVC_CONF_ENV_MYISAM_CACHE_SIZE%%":                    server.ClusterGroup.Configurator.GetConfigMyISAMKeyBufferSize(),
		"%%ENV:SVC_CONF_ENV_MYISAM_CACHE_SEGMENTS%%":                server.ClusterGroup.Configurator.GetConfigMyISAMKeyBufferSegements(),
		"%%ENV:SVC_CONF_ENV_ARIA_CACHE_SIZE%%":                      server.ClusterGroup.Configurator.GetConfigAriaCacheSize(),
		"%%ENV:SVC_CONF_ENV_QUERY_CACHE_SIZE%%":                     server.ClusterGroup.Configurator.GetConfigQueryCacheSize(),
		"%%ENV:SVC_CONF_ENV_ROCKSDB_CACHE_SIZE%%":                   server.ClusterGroup.Configurator.GetConfigRocksDBCacheSize(),
		"%%ENV:SVC_CONF_ENV_S3_CACHE_SIZE%%":                        server.ClusterGroup.Configurator.GetConfigS3CacheSize(),
		"%%ENV:IBPINSTANCES%%":                                      server.ClusterGroup.Configurator.GetConfigInnoDBBPInstances(),
		"%%ENV:CHECKPOINTIOPS%%":                                    server.ClusterGroup.Configurator.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_MAX_IOPS%%":                             server.ClusterGroup.Configurator.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY%%":                   server.ClusterGroup.Configurator.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY_MAX%%":               server.ClusterGroup.Configurator.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_MAX_DIRTY_PAGE_PCT%%":            server.ClusterGroup.Configurator.GetConfigInnoDBMaxDirtyPagePct(),
		"%%ENV:SVC_CONF_ENV_INNODB_MAX_DIRTY_PAGE_PCT_LWM%%":        server.ClusterGroup.Configurator.GetConfigInnoDBMaxDirtyPagePctLwm(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_INSTANCES%%":         server.ClusterGroup.Configurator.GetConfigInnoDBBPInstances(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%":              server.ClusterGroup.Configurator.GetConfigInnoDBBPSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_LOG_BUFFER_SIZE%%":               server.ClusterGroup.Configurator.GetConfigInnoDBLogBufferSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_LOG_FILE_SIZE%%":                 server.ClusterGroup.Configurator.GetConfigInnoDBLogFileSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_WRITE_IO_THREADS%%":              server.ClusterGroup.Configurator.GetConfigInnoDBWriteIoThreads(),
		"%%ENV:SVC_CONF_ENV_INNODB_READ_IO_THREADS%%":               server.ClusterGroup.Configurator.GetConfigInnoDBReadIoThreads(),
		"%%ENV:SVC_CONF_ENV_INNODB_PURGE_THREADS%%":                 server.ClusterGroup.Configurator.GetConfigInnoDBPurgeThreads(),
		"%%ENV:SVC_CONF_ENV_INNODB_LRU_FLUSH_SIZE%%":                server.ClusterGroup.Configurator.GetConfigInnoDBLruFlushSize(),
		"%%ENV:SVC_CONF_ENV_EXPIRE_LOG_DAYS%%":                      server.ClusterGroup.Configurator.GetConfigExpireLogDays(),
		"%%ENV:SVC_CONF_ENV_RELAY_SPACE_LIMIT%%":                    server.ClusterGroup.Configurator.GetConfigRelaySpaceLimit(),
		"%%ENV:SVC_CONF_ENV_GCOMM%%":                                server.ClusterGroup.GetGComm(),
		"%%ENV:SVC_CONF_ENV_GROUP_REPLICATION_WHITELIST%%":          server.ClusterGroup.GetGroupReplicationWhiteList(),
		"%%ENV:SVC_NAMESPACE%%":                                     server.ClusterGroup.Name,
		"%%ENV:SVC_NAME%%":                                          server.Name,
		"%%ENV:SVC_CONF_ENV_SST_METHOD%%":                           server.ClusterGroup.Conf.MultiMasterWsrepSSTMethod,
		"%%ENV:SVC_CONF_ENV_DOMAIN_ID%%":                            server.ClusterGroup.Configurator.GetConfigReplicationDomain(server.ClusterGroup.Name),
		"%%ENV:SVC_CONF_ENV_SST_RECEIVER_PORT%%":                    server.SSTPort,
		"%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_ADDR%%":             server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort,
		"%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL%%":              server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_HOST_NAME%%":    server.Host,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_HOST_PORT%%":    server.Port,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_CLUSTER_NAME%%": server.ClusterGroup.Name,
	}

	//	size = ` + collector.ProvDisk + `
}

func (server *ServerMonitor) GetDatabaseDatadir() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir + "/var"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/var/lib/mysql"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorOnPremise {
		value := server.GetConfigVariable("DATADIR")
		if value != "" {
			return value
		}
	}
	return "/var/lib/mysql"
}
func (server *ServerMonitor) GetDatabaseConfdir() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir + "/init/etc/mysql"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/etc/mysql"
	}
	return "/etc/mysql"
}
func (server *ServerMonitor) GetDatabaseBinary() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.ClusterGroup.Conf.ProvDBBinaryBasedir + "/mysqld"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/usr/sbin/mysqld"
	}
	return "/usr/sbin/mysqld"
}
func (server *ServerMonitor) GetDatabaseSocket() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir + "/" + server.Id + ".sock"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/var/mysqld.sock"
	}
	return "/var/run/mysqld/mysqld.sock"
}

func (server *ServerMonitor) GetDatabaseClientBasedir() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.ClusterGroup.Conf.ProvDBClientBasedir
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/usr/bin/"
	}
	return "/usr/bin"
}

func (server *ServerMonitor) GetConfigVariable(variable string) string {
	if server.Variables == nil {
		return ""
	}
	value := server.Variables[variable]
	return value
}

func (server *ServerMonitor) GetDatabaseConfig() string {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Database Config generation "+server.Datadir+"/config.tar.gz")
	if server.IsCompute {
		cluster.Configurator.AddDBTag("spider")
	}
	err := cluster.Configurator.GenerateDatabaseConfig(server.Datadir, cluster.Conf.WorkingDir+"/"+cluster.Name, server.GetDatabaseBasedir(), server.GetEnv())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Database Config generation "+server.Datadir+"/config.tar.gz error: %s", err)
	}
	server.IsConfigGen = true
	return ""
}

func (server *ServerMonitor) GetDatabaseDynamicConfig(filter string, cmd string) string {
	cluster := server.ClusterGroup
	mydynamicconf, err := cluster.Configurator.GetDatabaseDynamicConfig(filter, cmd, server.Datadir)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
	}
	return mydynamicconf
}

func (server *ServerMonitor) GetSlaveVariables() SlaveVariables {
	svar := SlaveVariables{}
	if server.Variables == nil {
		return svar
	}

	if v, ok := server.Variables["SLAVE_PARALLEL_MODE"]; ok {
		svar.SlaveParallelMode = v
	}

	if v, ok := server.Variables["SLAVE_TYPE_CONVERSIONS"]; ok {
		svar.SlaveTypeConversions = v
	}

	if v, ok := server.Variables["SLAVE_PARALLEL_MAX_QUEUED"]; ok {
		mq, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelMaxQueued = mq
		}
	}

	if v, ok := server.Variables["SLAVE_PARALLEL_THREADS"]; ok {
		pt, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelThreads = pt
		}
	}

	if v, ok := server.Variables["SLAVE_PARALLEL_WORKERS"]; ok {
		pw, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelWorkers = pw
		}
	}

	return svar
}
