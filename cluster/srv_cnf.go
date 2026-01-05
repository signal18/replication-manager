// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
		"%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL_HOST%%":         server.ClusterGroup.Conf.MonitorAddress,
		"%%ENV:SVC_CONF_ENV_REPLICATION_MANAGER_URL_PORT%%":         server.ClusterGroup.Conf.APIPort,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_HOST_NAME%%":    server.Host,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_HOST_PORT%%":    server.Port,
		"%%ENV:ENV:SVC_CONF_ENV_REPLICATION_MANAGER_CLUSTER_NAME%%": server.ClusterGroup.Name,
		"%%ENV:SVC_CONF_ENV_BINARY_LOG_NAME%%":                      server.GetBinaryLogName(),
		"%%ENV:SVC_CONF_ENV_ERROR_LOG%%":                            server.GetDbErrorLog(),
		"%%ENV:SVC_CONF_ENV_SLOW_LOG%%":                             server.GetDbSlowLog(),
		"%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%":                         server.GetJobDatadir(),
		"%%ENV:SVC_CONF_ENV_AUDIT_LOG%%":                            server.GetServerAuditFilePath(),
		"%%ENV:SVC_CONF_ENV_SQL_ERROR_LOG%%":                        server.GetSqlErrorLogFilename(),
		"%%ENV:SVC_CONF_ENV_LOG_ROTATE_MAX_AGE%%":                   fmt.Sprintf("%d", server.ClusterGroup.Conf.LogRotateMaxAge),
		"%%ENV:GENLINE%%":                                           fmt.Sprintf("Generated by Signal18 replication-manager %s on %s \n", server.ClusterGroup.RepMgrVersion, time.Now().Format(time.RFC3339)),
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
		if strings.HasPrefix(v, "/") {
			return v
		} else {
			return server.GetDatabaseDatadir() + "/" + v
		}
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/error.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/error.log"
}

func (server *ServerMonitor) GetDbSlowLog() string {

	if v, ok := server.SensitiveVariables.CheckAndGet("SLOW_QUERY_LOG_FILE"); ok && v != "" {
		if strings.HasPrefix(v, "/") {
			return v
		} else {
			return server.GetDatabaseDatadir() + "/" + v
		}
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/slow-query.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/slow-query.log"
}

func (server *ServerMonitor) GetServerAuditFilePath() string {

	if v, ok := server.SensitiveVariables.CheckAndGet("SERVER_AUDIT_FILE_PATH"); ok && v != "" {
		if strings.HasPrefix(v, "/") {
			return v
		} else {
			return server.GetDatabaseDatadir() + "/" + v
		}
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/server_audit.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/server_audit.log"
}

func (server *ServerMonitor) GetSqlErrorLogFilename() string {
	if v, ok := server.SensitiveVariables.CheckAndGet("SQL_ERROR_LOG_FILENAME"); ok && v != "" {
		if strings.HasPrefix(v, "/") {
			return v
		} else {
			return server.GetDatabaseDatadir() + "/" + v
		}
	}

	// If has nosplitpath
	if server.ClusterGroup.Configurator.HaveDBTag("nosplitpath") {
		return server.GetDatabaseDatadir() + "/sql_errors.log"
	}

	return server.GetDatabaseDatadir() + "/.system/logs/sql_errors.log"
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
	server.configGenMutex.Lock()
	defer server.configGenMutex.Unlock()
	return server.getDatabaseConfig(true)
}

func (server *ServerMonitor) GetDummyConfig() error {
	server.configGenMutex.Lock()
	defer server.configGenMutex.Unlock()
	return server.getDatabaseConfig(false)
}

// ProcessDummyConfigSendCookie checks if this server has a dummy config send cookie,
// and if so, processes it by generating and sending the config file
func (server *ServerMonitor) ProcessDummyConfigSendCookie() error {
	cluster := server.ClusterGroup

	if !server.HasWaitDummyConfigSendCookie() {
		return nil
	}

	// Lock to prevent concurrent config generation AND protect the config.tar.gz file
	// during the send operation. The lock will be held until the entire operation completes
	// (including the wait and send) to prevent another goroutine from regenerating the
	// config.tar.gz while it's being transmitted.
	server.configGenMutex.Lock()
	defer server.configGenMutex.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Found dummy config send cookie for server %s", server.URL)

	// Re-check cookie after acquiring lock (another goroutine might have processed it)
	if !server.HasWaitDummyConfigSendCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Cookie already processed by another goroutine for server %s", server.URL)
		return nil
	}

	// Get the cookie modification time before deletion
	cookieModTime, modTimeErr := server.GetWaitDummyConfigSendCookieModTime()
	if modTimeErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Failed to get cookie modtime for server %s: %s", server.URL, modTimeErr)
		// Still try to proceed even if we can't get modtime
		cookieModTime = time.Now().Add(-5 * time.Second) // Assume it's been at least 5 seconds
	}

	// Remove the cookie first to prevent re-processing
	server.DelWaitDummyConfigSendCookie()

	// Generate dummy config without double locking
	err := server.getDatabaseConfig(false)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Failed to generate dummy config for server %s: %s", server.URL, err)
		return err
	}

	// Send the config file - still protected by the lock held above via defer
	// This ensures no other goroutine can regenerate config.tar.gz during transmission
	configFile := filepath.Join(server.Datadir, "config.tar.gz")
	return server.sendDummyConfigWithWait(configFile, cookieModTime)
}

// sendDummyConfigWithWait sends the dummy config file after ensuring client listener is ready
func (server *ServerMonitor) sendDummyConfigWithWait(configFile string, cookieModTime time.Time) error {
	cluster := server.ClusterGroup

	// Calculate time elapsed since cookie creation
	elapsed := time.Since(cookieModTime)
	minWaitTime := 2 * time.Second // Minimum wait to ensure client listener is ready

	// If not enough time has passed, wait for the remaining duration
	if elapsed < minWaitTime {
		waitDuration := minWaitTime - elapsed
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Waiting %v before sending dummy config to ensure client listener is ready for server %s",
			waitDuration, server.URL)
		time.Sleep(waitDuration)
	}

	// Send the config file using SSTRunSender
	// Uses the server's pre-defined Host and SSTPort
	err := cluster.SSTRunSender(configFile, server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Failed to send dummy config for server %s: %s", server.URL, err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Successfully sent dummy config for server %s", server.URL)
	}

	return err
}

func (server *ServerMonitor) getDatabaseConfig(preserve bool) error {
	cluster := server.ClusterGroup

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Database Config generation "+server.Datadir+"/config.tar.gz")
	if server.IsCompute {
		cluster.Configurator.AddDBTag("spider")
	}

	err := cluster.Configurator.GenerateDatabaseConfig(server.Datadir, cluster.Conf.WorkingDir+"/"+cluster.Name, server.GetDatabaseBasedir(), server.GetEnv(), cluster.RepMgrVersion, preserve)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Database Config generation "+server.Datadir+"/config.tar.gz error: %s", err)
		return err
	}

	server.IsConfigGen = true
	return nil
}

func (server *ServerMonitor) PreserveConfigPath() {
	srcpath := filepath.Join(server.Datadir, "init/etc/mysql/replication-manager.d/default_path.cnf")
	destpath := filepath.Join(server.Datadir, "default_path.cnf")

	// Check if the source file exists before copying
	if _, err := os.Stat(srcpath); err == nil {
		misc.CopyFile(srcpath, destpath)
	}
}

func (server *ServerMonitor) RemovePreservedConfigPath() {
	filename := filepath.Join(server.Datadir, "default_path.cnf")

	// Check if the source file exists before copying
	if _, err := os.Stat(filename); err == nil {
		os.Remove(filename)
	}
}

func (server *ServerMonitor) ReadVariablesFromConfigs() {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		// Error is already logged inside ReadVariablesFromConfigFile
		server.ReadVariablesFromConfigFile(filepath.Join(server.Datadir, "dummy.cnf"), "config", true)
	}()

	go func() {
		defer wg.Done()
		// Error is already logged inside ReadVariablesFromConfigFile
		server.ReadVariablesFromConfigFile(filepath.Join(server.Datadir, "current.cnf"), "deployed", true)
	}()

	wg.Wait()

	server.ReadPreservedVariables()
	server.WriteDeltaVariables()
	server.IsNeedPathCheck = true
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

func (server *ServerMonitor) LoadFromTempConfigFile(srcpath, dstpath string) error {
	vars := make([]string, 0)

	_, err := os.Stat(srcpath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	srcfile, err := os.Open(srcpath)
	if err != nil {
		return err
	}
	defer srcfile.Close()

	// Read the file content
	scanner := bufio.NewScanner(srcfile)
	for scanner.Scan() {
		line := scanner.Text()

		// Only process lines that start with --
		if !strings.HasPrefix(line, "--") {
			continue
		}

		// Remove the -- prefix and trim whitespace
		line = strings.TrimSpace(strings.TrimPrefix(line, "--"))
		if line == "" {
			continue
		}

		vars = append(vars, line)
	}

	dstFile, err := os.OpenFile(dstpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = dstFile.WriteString("[mysqld]\n")
	if err != nil {
		return err
	}

	for _, v := range vars {
		_, err = dstFile.WriteString(v + "\n")
		if err != nil {
			return err
		}
	}

	return nil
}

func (server *ServerMonitor) ReadVariablesFromConfigFile(srcpath string, cnftype string, empty bool) error {
	cluster := server.ClusterGroup
	var err error

	var lastUpdate *time.Time
	switch cnftype {
	case "deployed":
		lastUpdate = &server.LastConfigUpdate.Deployed
	case "config":
		lastUpdate = &server.LastConfigUpdate.Config
	default:
		return fmt.Errorf("invalid config type: %s", cnftype)
	}

	finfo, err := os.Stat(srcpath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error checking file %s: %s", srcpath, err)
		return err
	}

	if !lastUpdate.IsZero() && !lastUpdate.Before(finfo.ModTime()) {
		// No need to read the file if it hasn't changed
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reading %s variables from %s", cnftype, srcpath)

	*lastUpdate = finfo.ModTime()

	if server.VariablesMap == nil {
		server.VariablesMap = config.NewVariablesMap()
	}

	// Clear all previous values
	if empty {
		switch cnftype {
		case "deployed":
			server.VariablesMap.EmptyDeployedValues()
		case "config":
			server.VariablesMap.EmptyConfigValues()
		}
	}

	// Read the file
	err = server.VariablesMap.LoadFromConfigFile(srcpath, cnftype)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reading file %s: %s", srcpath, err)
		return err
	}

	return nil
}

func (server *ServerMonitor) ReadPreservedVariables() error {
	cluster := server.ClusterGroup

	if server.VariablesMap == nil {
		server.VariablesMap = config.NewVariablesMap()
	}

	server.VariablesMap.LoadFromConfigFile(filepath.Join(server.Datadir, "01_preserved.cnf"), "preserved")

	// Read the preserved variables from config
	list := strings.Split(cluster.Conf.ProvDBConfigPreserveVars, ";")
	for _, opt := range list {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}

		value := ""
		parts := strings.SplitN(opt, "=", 2)
		if len(parts) == 2 {
			opt = parts[0]
			value = parts[1]
		}

		isMap := strings.Contains(value, "=")

		key := strings.ToUpper(opt)
		if v, ok := server.VariablesMap.CheckAndGet(key); ok {
			v.SetPreservedValue(value)
		} else if value != "" {
			v = new(config.VariableState)
			if isMap {
				v.Preserved = make(config.MapValue)
			} else {
				v.Preserved = new(config.SingleValue)
			}
			v.SetPreservedValue(value)

			server.VariablesMap.Store(key, v)
		}
	}

	return nil
}

func (server *ServerMonitor) WriteDeltaVariables() error {
	cluster := server.ClusterGroup
	deltapath := filepath.Join(server.Datadir, "02_delta.cnf")

	// Build content in memory first
	var content strings.Builder
	content.WriteString("[mysqld]\n")

	delta := server.VariablesMap.GetVariables(true)
	for _, v := range delta {
		// Skip variables that have preservation status set
		// These are handled by 01_preserved.cnf or 03_agreed.cnf
		if v.Preserved != nil {
			continue
		}
		content.WriteString(v.PrintDeployedDelta() + "\n")
	}

	// Write atomically
	if err := atomicWriteFile(deltapath, []byte(content.String()), 0644); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing file %s: %s", deltapath, err)
		return err
	}

	return nil
}

// atomicWriteFile writes data to a file atomically by writing to a temp file first,
// then renaming it to the target path. This prevents partial writes and corruption.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	// Create temp file in the same directory to ensure same filesystem
	dir := filepath.Dir(path)
	tmpfile, err := os.CreateTemp(dir, ".tmp-*.cnf")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpfile.Name()

	// Clean up temp file on error
	defer func() {
		if tmpfile != nil {
			tmpfile.Close()
			os.Remove(tmpPath)
		}
	}()

	// Write data to temp file
	if _, err := tmpfile.Write(data); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := tmpfile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// Close the temp file
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	tmpfile = nil // Mark as closed for defer cleanup

	// Set permissions
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (server *ServerMonitor) WritePreservedVariables() error {
	cluster := server.ClusterGroup
	preservepath := filepath.Join(server.Datadir, "01_preserved.cnf")
	agreedpath := filepath.Join(server.Datadir, "03_agreed.cnf")

	// Build content for both files in memory
	var preservedContent strings.Builder
	var agreedContent strings.Builder
	preservedContent.WriteString("[mysqld]\n")
	agreedContent.WriteString("[mysqld]\n")

	for key, v := range server.VariablesMap.ToNewMap() {
		// If preserve is set, write to preserve.cnf or agreed.cnf
		if v.Preserved != nil {
			if v.IsPreserved() {
				// Write preserved variables to preserve.cnf
				preservedContent.WriteString(fmt.Sprintf("%s=%s\n", key, v.Preserved.String()))
			} else {
				// Write non-preserved variables to agreed.cnf
				agreedContent.WriteString(fmt.Sprintf("%s=%s\n", key, v.Preserved.String()))
			}
		}
	}

	// Write both files atomically
	if err := atomicWriteFile(agreedpath, []byte(agreedContent.String()), 0644); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing file %s: %s", agreedpath, err)
		return err
	}

	if err := atomicWriteFile(preservepath, []byte(preservedContent.String()), 0644); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing file %s: %s", preservepath, err)
		return err
	}

	return nil
}

// SetVariablePreserved marks a variable difference as "preserved" (keep deployed value)
func (server *ServerMonitor) SetVariablePreserved(variableName string) error {
	cluster := server.ClusterGroup

	varState, exists := server.VariablesMap.ToNewMap()[strings.ToLower(variableName)]
	if !exists {
		return fmt.Errorf("variable %s not found", variableName)
	}

	// Set preserved to deployed value to keep current deployed value
	if varState.Deployed != nil {
		varState.Preserved = varState.Deployed
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Variable %s marked as preserved with value: %s", variableName, varState.Deployed.String())
	} else {
		return fmt.Errorf("variable %s has no deployed value", variableName)
	}

	// Write preserved variables to files
	return server.WritePreservedVariables()
}

// SetVariableCustomPreserved sets a custom preserved value for a variable
// This allows DBAs to manually override variable values like in my.cnf
func (server *ServerMonitor) SetVariableCustomPreserved(variableName string, customValue string) error {
	cluster := server.ClusterGroup

	varState, exists := server.VariablesMap.ToNewMap()[strings.ToLower(variableName)]
	if !exists {
		return fmt.Errorf("variable %s not found", variableName)
	}

	// Create a new variable value from the custom string
	// Determine the type based on the existing value type
	var newValue config.VariableValue

	switch varState.Config.(type) {
	case config.MapValue:
		// For map values (like optimizer_switch, performance_schema_instrument)
		mv := make(config.MapValue)
		mv.Set(customValue)
		newValue = mv
	case *config.SliceValue:
		// For slice values
		sv := &config.SliceValue{}
		sv.Set(customValue)
		newValue = sv
	case *config.SingleValue:
		// For simple string values
		singleVal := config.SingleValue(customValue)
		newValue = &singleVal
	default:
		// Default to single value
		singleVal := config.SingleValue(customValue)
		newValue = &singleVal
	}

	varState.Preserved = newValue
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Variable %s set to custom preserved value: %s", variableName, customValue)

	// Write preserved variables to files
	return server.WritePreservedVariables()
}

// SetVariableAccepted marks a variable difference as "accepted" (use config value)
func (server *ServerMonitor) SetVariableAccepted(variableName string) error {
	cluster := server.ClusterGroup

	varState, exists := server.VariablesMap.ToNewMap()[strings.ToLower(variableName)]
	if !exists {
		return fmt.Errorf("variable %s not found", variableName)
	}

	// Set preserved to config value to accept config value
	if varState.Config != nil {
		varState.Preserved = varState.Config
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Variable %s marked as accepted with value: %s", variableName, varState.Config.String())
	} else {
		return fmt.Errorf("variable %s has no config value", variableName)
	}

	// Write preserved variables to files
	return server.WritePreservedVariables()
}

// ClearVariablePreservation removes preservation status from a variable
func (server *ServerMonitor) ClearVariablePreservation(variableName string) error {
	cluster := server.ClusterGroup

	varState, exists := server.VariablesMap.ToNewMap()[strings.ToLower(variableName)]
	if !exists {
		return fmt.Errorf("variable %s not found", variableName)
	}

	varState.Preserved = nil
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Variable %s preservation cleared", variableName)

	// Write preserved variables to files
	return server.WritePreservedVariables()
}
