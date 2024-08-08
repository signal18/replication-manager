// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/misc"
)

type Configurator struct {
	ClusterConfig         config.Config     `json:"-"`
	ClusterConfigDiscover config.Config     `json:"-"`
	DBModule              config.Compliance `json:"-"`
	ProxyModule           config.Compliance `json:"-"`
	ConfigDBTags          []v3.Tag          `json:"configTags"`    //from module
	ConfigPrxTags         []v3.Tag          `json:"configPrxTags"` //from module
	DBTags                []string          `json:"dbServersTags"` //from conf
	ProxyTags             []string          `json:"proxyServersTags"`
	DBTagsDiscover        []string          `json:"dbServersTagsDiscover"` //from conf
	ProxyTagsDiscover     []string          `json:"proxyServersTagsDiscover"`
	WorkingDir            string            `json:"-"` // working dir is the place to generate the all cluster config
}

func (configurator *Configurator) Init(conf config.Config) error {
	var err error
	configurator.SetConfig(conf)

	err = configurator.LoadDBModules()
	if err != nil {
		return err
	}
	err = configurator.LoadProxyModules()
	if err != nil {
		return err
	}
	configurator.ConfigDBTags = configurator.GetDBModuleTags()
	configurator.ConfigPrxTags = configurator.GetProxyModuleTags()
	if conf.PRXServersReadOnMaster && !configurator.IsFilterInProxyTags("readonmaster") {
		configurator.AddProxyTag("readonmaster")
	} else {
		configurator.DropProxyTag("readonmaster")
	}
	// We should not force this here but rather via adding the readonly tag in default de tags
	/*
		if conf.ReadOnly && !configurator.IsFilterInDBTags("readonly") {
			configurator.AddDBTag("readonly")

		} else {
			configurator.DropDBTag("readonly")
		}*/
	return err
}

func (configurator *Configurator) LoadDBModules() error {
	var byteValue []byte
	if configurator.ClusterConfig.Test {
		file := configurator.ClusterConfig.ShareDir + "/opensvc/moduleset_mariadb.svc.mrm.db.json"
		if configurator.ClusterConfig.ProvDBCompliance != "" {
			file = configurator.ClusterConfig.ProvDBCompliance
		}
		jsonFile, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("Failed opened module %s %s", file, err)
		}
		// defer the closing of our jsonFile so that we can parse it later on
		defer jsonFile.Close()

		byteValue, _ = io.ReadAll(jsonFile)
	} else {
		byteValue, _ = share.EmbededDbModuleFS.ReadFile("opensvc/moduleset_mariadb.svc.mrm.db.json")
	}

	err := json.Unmarshal([]byte(byteValue), &configurator.DBModule)
	if err != nil {
		return fmt.Errorf("Failed unmarshal file %s %s", "opensvc/moduleset_mariadb.svc.mrm.db.json", err)
	}
	return nil
}

func (configurator *Configurator) LoadProxyModules() error {
	var byteValue []byte
	if configurator.ClusterConfig.Test {
		file := configurator.ClusterConfig.ShareDir + "/opensvc/moduleset_mariadb.svc.mrm.proxy.json"
		if configurator.ClusterConfig.ProvDBCompliance != "" {
			file = configurator.ClusterConfig.ProvProxyCompliance
		}
		jsonFile, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("Failed opened module %s %s", file, err)
		}
		defer jsonFile.Close()
		byteValue, _ = io.ReadAll(jsonFile)
	} else {
		byteValue, _ = share.EmbededDbModuleFS.ReadFile("opensvc/moduleset_mariadb.svc.mrm.proxy.json")
	}

	err := json.Unmarshal([]byte(byteValue), &configurator.ProxyModule)
	if err != nil {
		//return fmt.Errorf("Failed unmarshal file %s %s", file, err)
		return fmt.Errorf("Failed unmarshal file %s %s", "opensvc/moduleset_mariadb.svc.mrm.proxy.json", err)
	}
	return nil
}

func (configurator *Configurator) ConfigDiscovery(Variables *config.StringsMap, Plugins *config.PluginsMap) error {
	pmap := Plugins.ToNewMap()
	vmap := Variables.ToNewMap()
	innodbmem, err := strconv.ParseUint(Variables.Get("INNODB_BUFFER_POOL_SIZE"), 10, 64)
	if err != nil {
		return err
	}
	totalmem := innodbmem
	myisammem, err := strconv.ParseUint(Variables.Get("KEY_BUFFER_SIZE"), 10, 64)
	if err != nil {
		return err
	}
	totalmem += myisammem
	qcmem, err := strconv.ParseUint(Variables.Get("QUERY_CACHE_SIZE"), 10, 64)
	if err != nil {
		return err
	}
	if qcmem == 0 {
		configurator.AddDBTag("noquerycache")
	}
	totalmem += qcmem
	ariamem := uint64(0)
	if _, ok := Variables.CheckAndGet("ARIA_PAGECACHE_BUFFER_SIZE"); ok {
		ariamem, err = strconv.ParseUint(Variables.Get("ARIA_PAGECACHE_BUFFER_SIZE"), 10, 64)
		if err != nil {
			return err
		}
		totalmem += ariamem
	}
	tokumem := uint64(0)
	if _, ok := Variables.CheckAndGet("TOKUDB_CACHE_SIZE"); ok {
		configurator.AddDBTag("tokudb")
		tokumem, err = strconv.ParseUint(Variables.Get("TOKUDB_CACHE_SIZE"), 10, 64)
		if err != nil {
			return err
		}
		totalmem += tokumem
	}
	s3mem := uint64(0)
	if _, ok := Variables.CheckAndGet("S3_PAGECACHE_BUFFER_SIZE"); ok {
		configurator.AddDBTag("s3")
		tokumem, err = strconv.ParseUint(Variables.Get("S3_PAGECACHE_BUFFER_SIZE"), 10, 64)
		if err != nil {
			return err
		}
		totalmem += s3mem
	}

	rocksmem := uint64(0)
	if _, ok := Variables.CheckAndGet("ROCKSDB_BLOCK_CACHE_SIZE"); ok {
		configurator.AddDBTag("myrocks")
		tokumem, err = strconv.ParseUint(Variables.Get("ROCKSDB_BLOCK_CACHE_SIZE"), 10, 64)
		if err != nil {
			return err
		}
		totalmem += rocksmem
	}

	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	totalmem = totalmem + totalmem*uint64(sharedmempcts["threads"])/100
	configurator.SetDBMemory(strconv.FormatUint((totalmem / 1024 / 1024), 10))
	configurator.SetDBCores(Variables.Get("THREAD_POOL_SIZE"))

	if Variables.Get("INNODB_DOUBLEWRITE") == "OFF" {
		configurator.AddDBTag("nodoublewrite")
	}
	if Variables.Get("INNODB_FLUSH_LOG_AT_TRX_COMMIT") != "1" && Variables.Get("SYNC_BINLOG") != "1" {
		configurator.AddDBTag("nodurable")
	}
	if Variables.Get("INNODB_FLUSH_METHOD") != "O_DIRECT" {
		configurator.AddDBTag("noodirect")
	}
	if Variables.Get("LOG_BIN_COMPRESS") == "ON" {
		configurator.AddDBTag("compressbinlog")
	}
	if Variables.Get("INNODB_DEFRAGMENT") == "ON" {
		configurator.AddDBTag("autodefrag")
	}
	if Variables.Get("INNODB_COMPRESSION_DEFAULT") == "ON" {
		configurator.AddDBTag("compresstable")
	}

	if configurator.HasInstallPlugin(pmap, "BLACKHOLE") {
		configurator.AddDBTag("blackhole")
	}
	if configurator.HasInstallPlugin(pmap, "QUERY_RESPONSE_TIME") {
		configurator.AddDBTag("userstats")
	}
	if configurator.HasInstallPlugin(pmap, "SQL_ERROR_LOG") {
		configurator.AddDBTag("sqlerror")
	}
	if configurator.HasInstallPlugin(pmap, "METADATA_LOCK_INFO") {
		configurator.AddDBTag("metadatalocks")
	}
	if configurator.HasInstallPlugin(pmap, "SERVER_AUDIT") {
		configurator.AddDBTag("audit")
	}
	if Variables.Get("SLOW_QUERY_LOG") == "ON" {
		configurator.AddDBTag("slow")
	}
	if Variables.Get("GENERAL_LOG") == "ON" {
		configurator.AddDBTag("general")
	}
	if Variables.Get("PERFORMANCE_SCHEMA") == "ON" {
		configurator.AddDBTag("pfs")
	}
	if Variables.Get("LOG_OUTPUT") == "TABLE" {
		configurator.AddDBTag("logtotable")
	}

	if configurator.HasInstallPlugin(pmap, "CONNECT") {
		configurator.AddDBTag("connect")
	}
	if configurator.HasInstallPlugin(pmap, "SPIDER") {
		configurator.AddDBTag("spider")
	}
	if configurator.HasInstallPlugin(pmap, "SPHINX") {
		configurator.AddDBTag("sphinx")
	}
	if configurator.HasInstallPlugin(pmap, "MROONGA") {
		configurator.AddDBTag("mroonga")
	}
	if configurator.HasWsrep(vmap) {
		configurator.AddDBTag("wsrep")
	}
	//missing in compliance
	if configurator.HasInstallPlugin(pmap, "ARCHIVE") {
		configurator.AddDBTag("archive")
	}

	if configurator.HasInstallPlugin(pmap, "CRACKLIB_PASSWORD_CHECK") {
		configurator.AddDBTag("pwdcheckcracklib")
	}
	if configurator.HasInstallPlugin(pmap, "SIMPLE_PASSWORD_CHECK") {
		configurator.AddDBTag("pwdchecksimple")
	}

	if Variables.Get("LOCAL_INFILE") == "ON" {
		configurator.AddDBTag("localinfile")
	}
	if Variables.Get("SKIP_NAME_RESOLVE") == "OFF" {
		configurator.AddDBTag("resolvdns")
	}
	if Variables.Get("READ_ONLY") == "ON" {
		configurator.AddDBTag("readonly")
	}
	if Variables.Get("HAVE_SSL") == "YES" {
		configurator.AddDBTag("ssl")
	}

	if Variables.Get("BINLOG_FORMAT") == "STATEMENT" {
		configurator.AddDBTag("statement")
	}
	if Variables.Get("BINLOG_FORMAT") == "ROW" {
		configurator.AddDBTag("row")
	}
	if Variables.Get("LOG_BIN") == "OFF" {
		configurator.AddDBTag("nobinlog")
	}
	if Variables.Get("LOG_BIN") == "OFF" {
		configurator.AddDBTag("nobinlog")
	}
	if Variables.Get("LOG_SLAVE_UPDATES") == "OFF" {
		configurator.AddDBTag("nologslaveupdates")
	}
	if Variables.Get("RPL_SEMI_SYNC_MASTER_ENABLED") == "ON" {
		configurator.AddDBTag("semisync")
	}
	if Variables.Get("GTID_STRICT_MODE") == "ON" {
		configurator.AddDBTag("gtidstrict")
	}
	if strings.Contains(Variables.Get("SLAVE_TYPE_COVERSIONS"), "ALL_NON_LOSSY") || strings.Contains(Variables.Get("SLAVE_TYPE_COVERSIONS"), "ALL_LOSSY") {
		configurator.AddDBTag("lossyconv")
	}
	if Variables.Get("SLAVE_EXEC_MODE") == "IDEMPOTENT" {
		configurator.AddDBTag("idempotent")
	}

	//missing in compliance
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "SUBQUERY_CACHE=ON") {
		configurator.AddDBTag("subquerycache")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "SEMIJOIN_WITH_CACHE=ON") {
		configurator.AddDBTag("semijoincache")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "FIRSTMATCH=ON") {
		configurator.AddDBTag("firstmatch")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "EXTENDED_KEYS=ON") {
		configurator.AddDBTag("extendedkeys")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "LOOSESCAN=ON") {
		configurator.AddDBTag("loosescan")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "INDEX_CONDITION_PUSHDOWN=OFF") {
		configurator.AddDBTag("noicp")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "IN_TO_EXISTS=OFF") {
		configurator.AddDBTag("nointoexists")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "DERIVED_MERGE=OFF") {
		configurator.AddDBTag("noderivedmerge")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "DERIVED_WITH_KEYS=OFF") {
		configurator.AddDBTag("noderivedwithkeys")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "MRR=OFF") {
		configurator.AddDBTag("nomrr")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "OUTER_JOIN_WITH_CACHE=OFF") {
		configurator.AddDBTag("noouterjoincache")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "SEMI_JOIN_WITH_CACHE=OFF") {
		configurator.AddDBTag("nosemijoincache")
	}
	if strings.Contains(Variables.Get("OPTIMIZER_SWITCH"), "TABLE_ELIMINATION=OFF") {
		configurator.AddDBTag("notableelimination")
	}
	if strings.Contains(Variables.Get("SQL_MODE"), "ORACLE") {
		configurator.AddDBTag("sqlmodeoracle")
	}
	if Variables.Get("SQL_MODE") == "" {
		configurator.AddDBTag("sqlmodeunstrict")
	}
	//index_merge=on
	//index_merge_union=on,
	//index_merge_sort_union=on
	//index_merge_intersection=on
	//index_merge_sort_intersection=off
	//engine_condition_pushdown=on
	//materialization=on
	//semijoin=on
	//partial_match_rowid_merge=on
	//partial_match_table_scan=on,
	//mrr_cost_based=off
	//mrr_sort_keys=on,
	//join_cache_incremental=on,
	//join_cache_hashed=on,
	//join_cache_bka=on,
	//optimize_join_buffer_size=on,
	//orderby_uses_equalities=on
	//condition_pushdown_for_derived=on
	//split_materialized=on//
	//condition_pushdown_for_subquery=on,
	//rowid_filter=on
	//condition_pushdown_from_having=on

	if Variables.Get("TX_ISOLATION") == "READ-COMMITTED" {
		configurator.AddDBTag("readcommitted")
	}
	//missing
	if Variables.Get("TX_ISOLATION") == "READ-UNCOMMITTED" {
		configurator.AddDBTag("readuncommitted")
	}
	if Variables.Get("TX_ISOLATION") == "REPEATABLE-READ" {
		configurator.AddDBTag("reapeatableread")
	}
	if Variables.Get("TX_ISOLATION") == "SERIALIZED" {
		configurator.AddDBTag("serialized")
	}

	if Variables.Get("JOIN_CACHE_LEVEL") == "8" {
		configurator.AddDBTag("hashjoin")
	}
	if Variables.Get("JOIN_CACHE_LEVEL") == "6" {
		configurator.AddDBTag("mrrjoin")
	}
	if Variables.Get("JOIN_CACHE_LEVEL") == "2" {
		configurator.AddDBTag("nestedjoin")
	}
	if Variables.Get("LOWER_CASE_TABLE_NAMES") == "1" {
		configurator.AddDBTag("lowercasetable")
	}
	if Variables.Get("USER_STAT_TABLES") == "PREFERABLY_FOR_QUERIES" {
		configurator.AddDBTag("eits")
	}

	if Variables.Get("CHARACTER_SET_SERVER") == "UTF8MB4" {
		if strings.Contains(Variables.Get("COLLATION_SERVER"), "_ci") {
			configurator.AddDBTag("bm4ci")
		} else {
			configurator.AddDBTag("bm4cs")
		}
	}
	if Variables.Get("CHARACTER_SET_SERVER") == "UTF8" {
		if strings.Contains(Variables.Get("COLLATION_SERVER"), "_ci") {
			configurator.AddDBTag("utf8ci")
		} else {
			configurator.AddDBTag("utf8cs")
		}
	}

	//slave_parallel_mode = optimistic
	/*

		tmpmem, err := strconv.ParseUint(Variables.Get("TMP_TABLE_SIZE"), 10, 64)
		if err != nil {
			return err
		}
			qttmp, err := strconv.ParseUint(Variables.Get("MAX_TMP_TABLES"), 10, 64)
			if err != nil {
				return err
			}
			tmpmem = tmpmem * qttmp
			totalmem += tmpmem

			cores, err := strconv.ParseUint(Variables["THREAD_POOL_SIZE"], 10, 64)
			if err != nil {
				return err
			}

			joinmem, err := strconv.ParseUint(Variables["JOIN_BUFFER_SPACE_LIMIT"], 10, 64)
			joinmem = joinmem * cores

			sortmem, err := strconv.ParseUint(Variables["SORT_BUFFER_SIZE"], 10, 64)
	*/
	//
	//	containermem = containermem * int64(sharedmempcts["innodb"]) / 100

	return nil
}

func (configurator *Configurator) GenerateProxyConfig(Datadir string, ClusterDir string, TemplateEnv map[string]string, RepMgrVersion string) error {

	os.RemoveAll(Datadir + "/init")
	// Extract files
	for _, rule := range configurator.ProxyModule.Rulesets {

		if strings.Contains(rule.Name, "mariadb.svc.mrm.proxy.cnf") {

			for _, variable := range rule.Variables {
				if variable.Class == "file" || variable.Class == "fileprop" {
					err := configurator.WriteProxyConfigFile(Datadir, TemplateEnv, RepMgrVersion, &rule, &variable)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	// processing symlink
	type Link struct {
		Symlink string `json:"symlink"`
		Target  string `json:"target"`
	}
	for _, rule := range configurator.ProxyModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.proxy.cnf") {
			for _, variable := range rule.Variables {
				if variable.Class == "symlink" {
					if configurator.IsFilterInProxyTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.proxy.cnf" {
						var f Link
						json.Unmarshal([]byte(variable.Value), &f)
						fpath := strings.Replace(f.Symlink, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init", -1)
						/*	if proxy.ClusterGroup.Conf.LogLevel > 2 {
											cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config symlink %s", fpath)
							  			}
						*/
						os.Symlink(f.Target, fpath)

					}
				}
			}
		}
	}
	misc.CopyFile(ClusterDir+"/ca-cert.pem", Datadir+"/init/etc/proxysql/ssl/ca-cert.pem")
	misc.CopyFile(ClusterDir+"/server-cert.pem", Datadir+"/init/etc/proxysql/ssl/server-cert.pem")
	misc.CopyFile(ClusterDir+"/server-key.pem", Datadir+"/init/etc/proxysql/ssl/server-key.pem")
	misc.CopyFile(ClusterDir+"/client-cert.pem", Datadir+"/init/etc/proxysql/ssl/client-cert.pem")
	misc.CopyFile(ClusterDir+"/client-key.pem", Datadir+"/init/etc/proxysql/ssl/client-key.pem")
	misc.CopyFile(ClusterDir+"/ca-cert.pem", Datadir+"/init/data/proxysql-ca.pem")
	misc.CopyFile(ClusterDir+"/server-cert.pem", Datadir+"/init/data/proxysql-cert.pem")
	misc.CopyFile(ClusterDir+"/server-key.pem", Datadir+"/init/data/proxysql-key.pem")
	misc.CopyFile(ClusterDir+"/ca-cert.pem", Datadir+"/init/etc/maxscale/ssl/ca-cert.pem")
	misc.CopyFile(ClusterDir+"/server-cert.pem", Datadir+"/init/etc/maxscale/ssl/server-cert.pem")
	misc.CopyFile(ClusterDir+"/server-key.pem", Datadir+"/init/etc/maxscale/ssl/server-key.pem")
	misc.CopyFile(ClusterDir+"/client-cert.pem", Datadir+"/init/etc/maxscale/ssl/client-cert.pem")
	misc.CopyFile(ClusterDir+"/client-key.pem", Datadir+"/init/etc/maxscale/ssl/client-key.pem")
	misc.CopyFile(ClusterDir+"/ca-cert.pem", Datadir+"/init/etc/haproxy/ssl/ca-cert.pem")
	misc.CopyFile(ClusterDir+"/server-cert.pem", Datadir+"/init/etc/haproxy/ssl/server-cert.pem")
	misc.CopyFile(ClusterDir+"/server-key.pem", Datadir+"/init/etc/haproxy/ssl/server-key.pem")
	misc.CopyFile(ClusterDir+"/client-cert.pem", Datadir+"/init/etc/haproxy/ssl/client-cert.pem")
	misc.CopyFile(ClusterDir+"/client-key.pem", Datadir+"/init/etc/haproxy/ssl/client-key.pem")

	/*if configurator.HaveProxyTag("docker") {
		err := misc.ChownR(Datadir+"/init/data", 999, 999)
		if err != nil {
			return fmt.Errorf("Chown failed %q: %s", Datadir+"/init/data", err)
		}
	}*/
	configurator.TarGz(Datadir+"/config.tar.gz", Datadir+"/init")

	return nil
}

func (configurator *Configurator) GenerateDatabaseConfig(Datadir string, ClusterDir string, RemoteBasedir string, TemplateEnv map[string]string, RepMgrVersion string) error {

	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}

	// Extract files
	if configurator.ClusterConfig.ProvBinaryInTarball {
		url, err := configurator.ClusterConfig.GetTarballUrl(configurator.ClusterConfig.ProvBinaryTarballName)
		if err != nil {
			return fmt.Errorf("Compliance get binary %s directory  %s", url, err)
		}
		err = misc.DownloadFileTimeout(url, Datadir+"/"+configurator.ClusterConfig.ProvBinaryTarballName, 1200)
		if err != nil {
			return fmt.Errorf("Compliance dowload binary %s directory  %s", url, err)
		}
		misc.Untargz(Datadir+"/init", Datadir+"/"+configurator.ClusterConfig.ProvBinaryTarballName)
	}

	if configurator.ClusterConfig.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		os.RemoveAll(Datadir + "/init/etc")
	} else {
		os.RemoveAll(Datadir + "/init")
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf") {

			for _, variable := range rule.Variables {
				if variable.Class == "file" || variable.Class == "fileprop" {
					err := configurator.WriteDatabaseConfigFile(Datadir, RemoteBasedir, TemplateEnv, RepMgrVersion, &rule, &variable)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	// processing symlink
	type Link struct {
		Symlink string `json:"symlink"`
		Target  string `json:"target"`
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf.generic") {
			for _, variable := range rule.Variables {
				if variable.Class == "symlink" {
					if configurator.IsFilterInDBTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.db.cnf.generic" {
						var f Link
						json.Unmarshal([]byte(variable.Value), &f)
						fpath := strings.Replace(f.Symlink, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init", -1)
						/*		if configurator.ClusterConfig.LogLevel > 2 {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config symlink %s", fpath)
							} */
						os.Symlink(f.Target, fpath)
						//	keys := strings.Split(variable.Value, " ")
					}
				}
			}
		}
	}

	/*	if configurator.HaveDBTag("docker") {
			err := misc.ChownR(Datadir+"/init/data", 999, 999)
			if err != nil {
				return fmt.Errorf("Chown failed %q: %s", Datadir+"/init/data", err)
			}
			err = misc.ChmodR(Datadir+"/init/init", 0755)
			if err != nil {
				return fmt.Errorf("Chown failed %q: %s", Datadir+"/init/init", err)
			}
		}
	*/
	misc.CopyFile(ClusterDir+"/ca-cert.pem", Datadir+"/init/etc/mysql/ssl/ca-cert.pem")
	misc.CopyFile(ClusterDir+"/server-cert.pem", Datadir+"/init/etc/mysql/ssl/server-cert.pem")
	misc.CopyFile(ClusterDir+"/server-key.pem", Datadir+"/init/etc/mysql/ssl/server-key.pem")
	misc.CopyFile(ClusterDir+"/client-cert.pem", Datadir+"/init/etc/mysql/ssl/client-cert.pem")
	misc.CopyFile(ClusterDir+"/client-key.pem", Datadir+"/init/etc/mysql/ssl/client-key.pem")

	configurator.TarGz(Datadir+"/config.tar.gz", Datadir+"/init")

	return nil
}

func (configurator *Configurator) GetDatabaseDynamicConfig(filter string, cmd string, Datadir string) (string, error) {
	mydynamicconf := ""
	// processing symlink
	type Link struct {
		Symlink string `json:"symlink"`
		Target  string `json:"target"`
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf.generic") {
			for _, variable := range rule.Variables {
				if variable.Class == "symlink" {
					if configurator.IsFilterInDBTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.db.cnf.generic" {
						//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "content %s %s", filter, rule.Filter)
						if filter == "" || strings.Contains(rule.Filter, filter) {
							var f Link
							json.Unmarshal([]byte(variable.Value), &f)
							fpath := Datadir + "/init/etc/mysql/conf.d/"
							//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config symlink %s , %s", fpath, f.Target)
							file, err := os.Open(fpath + f.Target)
							if err == nil {
								r, _ := regexp.Compile(cmd)
								scanner := bufio.NewScanner(file)
								for scanner.Scan() {
									//		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "content: %s", scanner.Text())
									if r.MatchString(scanner.Text()) {
										mydynamicconf = mydynamicconf + strings.Split(scanner.Text(), ":")[1]
									}
								}
								file.Close()

							} else {
								return mydynamicconf, fmt.Errorf("Error in dynamic config: %s", err)
							}
						}
					}
				}
			}
		}
	}
	return mydynamicconf, nil
}

func (configurator *Configurator) GetDatabaseConfig(filter string, datadir string) (string, error) {
	mydynamicconf := ""
	// processing symlink
	type Link struct {
		Symlink string `json:"symlink"`
		Target  string `json:"target"`
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf.generic") {
			for _, variable := range rule.Variables {
				if variable.Class == "symlink" {
					if configurator.IsFilterInDBTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.db.cnf.generic" {
						//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "content %s %s", filter, rule.Filter)
						if filter == "" || strings.Contains(rule.Filter, filter) {
							var f Link
							json.Unmarshal([]byte(variable.Value), &f)
							fpath := datadir + "/init/etc/mysql/conf.d/"
							//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config symlink %s , %s", fpath, f.Target)
							file, err := os.Open(fpath + f.Target)
							if err == nil {
								scanner := bufio.NewScanner(file)
								for scanner.Scan() {
									mydynamicconf = mydynamicconf + strings.Split(scanner.Text(), ":")[1]
								}
								file.Close()

							} else {
								return mydynamicconf, fmt.Errorf("Error in dynamic config: %s", err)
							}
						}
					}
				}
			}
		}
	}
	return mydynamicconf, nil
}

func (configurator *Configurator) WriteDatabaseConfigFile(Datadir string, RemoteBasedir string, TemplateEnv map[string]string, RepMgrVersion string, rule *config.ComplianceRuleset, variable *config.ComplianceVariable) error {
	ts := time.Now().Format(time.RFC3339)

	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}

	var f File
	json.Unmarshal([]byte(variable.Value), &f)
	fpath := strings.Replace(f.Path, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init", -1)
	dir := filepath.Dir(fpath)
	/*		if server.ClusterGroup.Conf.LogLevel > 2 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config create %s", fpath)
			}
	*/
	// create directory
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.FileMode(0775))
		if err != nil {
			return fmt.Errorf("Compliance create directory %q: %s", dir, err)
		}
	}

	if fpath[len(fpath)-1:] != "/" && (configurator.IsFilterInDBTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.db.cnf.generic") {
		content := misc.ExtractKey(f.Content, TemplateEnv)

		if configurator.IsFilterInDBTags("docker") && configurator.ClusterConfig.ProvOrchestrator != config.ConstOrchestratorLocalhost {
			if configurator.IsFilterInDBTags("wsrep") {
				//if galera don't cusomized system files
				if strings.Contains(content, "./.system") && !(strings.Contains(content, "exclude") || strings.Contains(content, "ignore")) {
					content = ""
				}
			} else {
				content = strings.Replace(content, "./.system", "/var/lib/mysql/.system", -1)
			}
		}

		if configurator.ClusterConfig.ProvOrchestrator == config.ConstOrchestratorLocalhost {
			content = strings.Replace(content, "includedir ..", "includedir "+RemoteBasedir+"/init", -1)
			content = strings.Replace(content, "../etc/mysql", RemoteBasedir+"/init/etc/mysql", -1)

		} else if configurator.ClusterConfig.ProvOrchestrator == config.ConstOrchestratorSlapOS {
			content = strings.Replace(content, "includedir ..", "includedir "+RemoteBasedir+"/", -1)
			content = strings.Replace(content, "../etc/mysql", RemoteBasedir+"/etc/mysql", -1)
			content = strings.Replace(content, "./.system", RemoteBasedir+"/var/lib/mysql/.system", -1)
		}

		outFile, err := os.Create(fpath)
		if err != nil {
			return fmt.Errorf("Compliance create file failed %q: %s", fpath, err)
		} else {
			_, err = outFile.WriteString(fmt.Sprintf("# Generated by Signal18 replication-manager %s on %s \n", RepMgrVersion, ts))
			if err != nil {
				outFile.Close()
				return fmt.Errorf("Compliance writing header file failed %q: %s", fpath, err)
			}

			_, err = outFile.WriteString(content)
			if err != nil {
				outFile.Close()
				return fmt.Errorf("Compliance writing file failed %q: %s", fpath, err)
			}
			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Variable name %s", variable.Name)
		}

	}
	return nil
}

func (configurator *Configurator) WriteProxyConfigFile(Datadir string, TemplateEnv map[string]string, RepMgrVersion string, rule *config.ComplianceRuleset, variable *config.ComplianceVariable) error {
	ts := time.Now().Format(time.RFC3339)

	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}

	var f File
	json.Unmarshal([]byte(variable.Value), &f)
	fpath := strings.Replace(f.Path, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init", -1)
	dir := filepath.Dir(fpath)
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Config create %s", fpath)
	// create directory
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.FileMode(0775))
		if err != nil {
			return fmt.Errorf("Compliance create directory %q: %s", dir, err)
		}
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "rule %s filter %s %t", rule.Name, rule.Filter, proxy.IsFilterInTags(rule.Filter))
	if fpath[len(fpath)-1:] != "/" && (configurator.IsFilterInProxyTags(rule.Filter) || rule.Filter == "") {

	}
	content := misc.ExtractKey(f.Content, TemplateEnv)
	outFile, err := os.Create(fpath)
	if err != nil {
		return fmt.Errorf("Compliance create file failed %q: %s", fpath, err)
	} else {
		_, err = outFile.WriteString(fmt.Sprintf("# Generated by Signal18 replication-manager %s on %s \n", RepMgrVersion, ts))
		if err != nil {
			outFile.Close()
			return fmt.Errorf("Compliance writing file failed %q: %s", fpath, err)
		}
		_, err = outFile.WriteString(content)
		if err != nil {
			outFile.Close()
			return fmt.Errorf("Compliance writing file failed %q: %s", fpath, err)
		}

		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Variable name %s", variable.Name)
	}
	return nil
}
