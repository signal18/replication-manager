// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
		"%%ENV:SVC_CONF_ENV_BINARY_LOG_NAME%%":                      server.GetBinaryLogName(),
		"%%ENV:SVC_CONF_ENV_ERROR_LOG%%":                            server.GetDbErrorLog(),
		"%%ENV:SVC_CONF_ENV_SLOW_LOG%%":                             server.GetDbSlowLog(),
		"%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%":                         server.GetJobDatadir(),
	}

	//	size = ` + collector.ProvDisk + `
}

// GetDatabaseDatadir returns the database data directory from variables
// If the data is not exists, it will return the default datadir
// If the server is localhost, default will return server.Datadir + "/var"
// If the server is slapOS, default will return server.SlapOSDatadir + "/var/lib/mysql"
// If the server is not localhost or slapOS, default will return "/var/lib/mysql"
func (server *ServerMonitor) GetDatabaseDatadir() string {

	// If sensitive variables is not loaded, reload it
	if server.SensitiveVariables == nil {
		server.ReloadSaveInfosVariables()
	}

	// Check if DATADIR is exists
	if value, ok := server.SensitiveVariables.CheckAndGet("DATADIR"); ok && value != "" {
		value, _ := strings.CutSuffix(value, "/")
		return value
	}

	// If server is localhost or slapOS return the default datadir based on the server
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir + "/var"
	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir + "/var/lib/mysql"
	}

	// Return the default datadir
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

func (server *ServerMonitor) GetDbErrorLog() string {

	if v, ok := server.SensitiveVariables.CheckAndGet("LOG_ERROR"); ok && v != "" {
		return v
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/error.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/error.log"
}

func (server *ServerMonitor) GetDbSlowLog() string {

	if v, ok := server.SensitiveVariables.CheckAndGet("SLOW_QUERY_LOG_FILE"); ok && v != "" {
		return v
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/slow-query.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/slow-query.log"
}

func (server *ServerMonitor) GetConfigVariable(variable string) string {
	// if server.Variables == nil {
	// 	return ""
	// }
	// value := server.Variables[variable]
	// return value
	return server.Variables.Get(variable)
}

func (server *ServerMonitor) GetDatabaseConfig() error {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Database Config generation "+server.Datadir+"/config.tar.gz")
	if server.IsCompute {
		cluster.Configurator.AddDBTag("spider")
	}

	err := cluster.Configurator.GenerateDatabaseConfig(server.Datadir, cluster.Conf.WorkingDir+"/"+cluster.Name, server.GetDatabaseBasedir(), server.GetEnv(), cluster.RepMgrVersion)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Database Config generation "+server.Datadir+"/config.tar.gz error: %s", err)
		return err
	}

	server.IsConfigGen = true
	return nil
}

func (server *ServerMonitor) PrintDefaults(fetch bool) error {
	cluster := server.ClusterGroup
	cluster.PrintDefaultDatabaseService(server, fetch)

	server.ReadVariablesFromConfigs()

	return nil
}

func (server *ServerMonitor) ReadVariablesFromConfigs() {
	cluster := server.ClusterGroup

	var err error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err = server.ReadVariablesFromConfigFile(filepath.Join(server.Datadir, "dummy.cnf"), false)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
		}
	}()

	go func() {
		defer wg.Done()
		err = server.ReadVariablesFromConfigFile(filepath.Join(server.Datadir, "current.cnf"), true)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
		}
	}()

	wg.Wait()
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
	// if server.Variables == nil {
	// 	return svar
	// }

	if v, ok := server.Variables.CheckAndGet("SLAVE_PARALLEL_MODE"); ok {
		svar.SlaveParallelMode = v
	}

	if v, ok := server.Variables.CheckAndGet("SLAVE_TYPE_CONVERSIONS"); ok {
		svar.SlaveTypeConversions = v
	}

	if v, ok := server.Variables.CheckAndGet("SLAVE_PARALLEL_MAX_QUEUED"); ok {
		mq, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelMaxQueued = mq
		}
	}

	if v, ok := server.Variables.CheckAndGet("SLAVE_PARALLEL_THREADS"); ok {
		pt, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelThreads = pt
		}
	}

	if v, ok := server.Variables.CheckAndGet("SLAVE_PARALLEL_WORKERS"); ok {
		pw, err := strconv.Atoi(v)
		if err == nil {
			svar.SlaveParallelWorkers = pw
		}
	}

	return svar
}

func (server *ServerMonitor) GetBinaryLogName() string {
	cluster := server.ClusterGroup

	// If no variables loaded, load them from disk
	if server.SensitiveVariables == nil {
		server.ReloadSaveInfosVariables()
	}

	binlogname := server.SensitiveVariables.Get("LOG_BIN_BASENAME")
	if binlogname != "" {
		parts := strings.Split(binlogname, "/")
		return parts[len(parts)-1]
	}

	return cluster.Conf.ProvDBBinaryLogName
}

func (server *ServerMonitor) GetBinaryLogDir() string {
	// If no variables loaded, load them from disk
	if server.SensitiveVariables == nil {
		server.ReloadSaveInfosVariables()
	}

	binlogname := server.SensitiveVariables.Get("LOG_BIN_BASENAME")
	if binlogname != "" {
		parts := strings.Split(binlogname, "/")
		return strings.Join(parts[:len(parts)-1], "/")
	}

	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir()
	}

	return server.GetDatabaseDatadir() + "/.system/repl"
}

func (server *ServerMonitor) GetJobDatadir() string {
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return "/var/lib/replication-manager-jobs"
	}

	return server.GetDatabaseDatadir() + "/.system/jobs"
}

func (server *ServerMonitor) CreateLocalCnf(cnf string) error {
	// Create the directory if it doesn't exist
	parent := filepath.Dir(cnf)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		if err = os.MkdirAll(parent, 0755); err != nil {
			return err
		}
	}

	// Create the dummy.cnf file
	return os.WriteFile(cnf, []byte("[mysqld]\n!includedir "+filepath.Join(server.Datadir, "init/etc/mysql/conf.d")+"\n"), 0644)
}

func (server *ServerMonitor) ReadVariablesFromConfigFile(filepath string, deployed bool) error {
	cluster := server.ClusterGroup

	if _, err := os.Stat(filepath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error checking file %s: %s", filepath, err)
		return err

	}

	// Read the file
	file, err := os.Open(filepath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reading file %s: %s", filepath, err)
		return err
	}
	defer file.Close()

	// Read the contents of the file
	var out bytes.Buffer
	if _, err := out.ReadFrom(file); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reading file %s: %s", filepath, err)
		return err
	}

	// Step 3: Process output
	lines := strings.Split(out.String(), "--")
	list := map[string]bool{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		line = strings.TrimPrefix(line, "loose_") // handle --loose_option
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.ToUpper(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			if v, ok := server.VariablesMap.CheckAndGet(key); ok {
				if deployed {
					if v.Deployed == nil {
						v.Deployed = &value
					} else if *v.Deployed != value {
						if multi, ok := list[key]; ok && multi {
							// If the previous key is the same, append the value
							*v.Deployed += "," + value
						} else {
							// If the previous key is not the same, update the value
							*v.Deployed = value
						}
						// Update the value in the map
						server.VariablesMap.Store(key, v)
					}
				} else {
					if v.Config == nil {
						v.Config = &value
					} else if *v.Config != value {
						if multi, ok := list[key]; ok && multi {
							// If the previous key is the same, append the value
							*v.Config += "," + value
						} else {
							// If the previous key is not the same, update the value
							*v.Config = value
						}
						// Update the value in the map
						server.VariablesMap.Store(key, v)
					}
				}
			} else {
				if deployed {
					server.VariablesMap.Store(key, &config.VariableState{
						Variable_name: key,
						Deployed:      &value,
					})
				} else {
					server.VariablesMap.Store(key, &config.VariableState{
						Variable_name: key,
						Config:        &value,
					})
				}
			}

			list[key] = true
		}
	}

	return nil
}
