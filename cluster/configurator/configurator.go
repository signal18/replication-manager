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
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/sirupsen/logrus"
)

type Configurator struct {
	ClusterConfig         config.Config     `json:"-"`
	ClusterConfigDiscover config.Config     `json:"-"`
	DBModule              config.Compliance `json:"-"`
	ProxyModule           config.Compliance `json:"-"`
	Logger                *logrus.Logger    `json:"-"`
	ConfigDBTags          []*v3.Tag         `json:"configTags"`    //from module
	ConfigPrxTags         []*v3.Tag         `json:"configPrxTags"` //from module
	DBTags                []string          `json:"dbServersTags"` //from conf
	ProxyTags             []string          `json:"proxyServersTags"`
	DBTagsDiscover        []string          `json:"dbServersTagsDiscover"` //from conf
	ProxyTagsDiscover     []string          `json:"proxyServersTagsDiscover"`
	WorkingDir            string            `json:"-"` // working dir is the place to generate the all cluster config
	DocHelp               *DocHelp          `json:"-"` // variable documentation lookup (singleton, lazy-loaded)
	// ActiveDBCRC and ActivePrxCRC are CRC32 checksums of the compliance
	// modules last accepted by the user. Persisted to disk so upgrades
	// (new embedded module) and BO pushes are both detected on restart.
	ActiveDBCRC           uint32            `json:"-"`
	ActivePrxCRC          uint32            `json:"-"`
	// PendingDBCRC and PendingPrxCRC are CRC32 checksums of new compliance
	// files found in PluginDataDir or embedded. Non-zero when an update is pending.
	PendingDBCRC          uint32            `json:"pendingDBCRC,omitempty"`
	PendingPrxCRC         uint32            `json:"pendingPrxCRC,omitempty"`
}

func (configurator *Configurator) Init(conf config.Config, logger *logrus.Logger) error {
	var err error
	configurator.SetLogger(logger)
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
	configurator.DocHelp = NewDocHelp(conf.ShareDir + "/plugins/data")
	if conf.ProvAutoUpdateCompliance {
		// Trust mode (default): always use the current module (embedded or BO-pushed).
		// Save it to disk so it becomes the baseline for future comparisons.
		configurator.ActiveDBCRC = configurator.complianceCRC(configurator.DBModule)
		configurator.ActivePrxCRC = configurator.complianceCRC(configurator.ProxyModule)
		configurator.saveAcceptedCompliance()
	} else {
		// Approval mode: load the previously accepted compliance from disk.
		// If found, use it instead of the embedded module — this preserves the
		// accepted version across binary upgrades until the user explicitly
		// approves the new version via the API.
		if configurator.loadAcceptedCompliance() {
			configurator.ConfigDBTags = configurator.GetDBModuleTags()
			configurator.ConfigPrxTags = configurator.GetProxyModuleTags()
		}
		configurator.ActiveDBCRC = configurator.complianceCRC(configurator.DBModule)
		configurator.ActivePrxCRC = configurator.complianceCRC(configurator.ProxyModule)
	}
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

// ReloadComplianceFromDataDir checks for updated compliance files in the
// plugin data directory (pushed by the back office via git pull) and reloads
// the DB and Proxy modules if the files exist and parse successfully.
// Returns true if either module was reloaded.
func (configurator *Configurator) ReloadComplianceFromDataDir(pluginDataDir string) (bool, error) {
	reloaded := false

	dbFile := filepath.Join(pluginDataDir, "moduleset_mariadb.svc.mrm.db.json")
	if data, err := os.ReadFile(dbFile); err == nil {
		var module config.Compliance
		if err := json.Unmarshal(data, &module); err == nil && len(module.Rulesets) > 0 {
			configurator.DBModule = module
			configurator.ConfigDBTags = configurator.GetDBModuleTags()
			reloaded = true
		}
	}

	proxyFile := filepath.Join(pluginDataDir, "moduleset_mariadb.svc.mrm.proxy.json")
	if data, err := os.ReadFile(proxyFile); err == nil {
		var module config.Compliance
		if err := json.Unmarshal(data, &module); err == nil && len(module.Rulesets) > 0 {
			configurator.ProxyModule = module
			configurator.ConfigPrxTags = configurator.GetProxyModuleTags()
			reloaded = true
		}
	}

	return reloaded, nil
}

// complianceCRC computes a CRC32 checksum of a compliance module by
// re-serialising it to JSON. Used to detect changes.
func (configurator *Configurator) complianceCRC(module config.Compliance) uint32 {
	data, err := json.Marshal(module)
	if err != nil {
		return 0
	}
	return crc32.ChecksumIEEE(data)
}

// CheckComplianceUpdate checks if the current in-memory compliance modules
// differ from the last accepted CRC. This detects both:
// - BO-pushed files in PluginDataDir (pulled via git)
// - Embedded module changes from a binary upgrade
// Sets PendingDBCRC/PendingPrxCRC when a change is pending.
func (configurator *Configurator) CheckComplianceUpdate(pluginDataDir string) bool {
	pending := false

	// Check PluginDataDir first (BO push takes priority over embedded)
	dbCRC := uint32(0)
	dbFile := filepath.Join(pluginDataDir, "moduleset_mariadb.svc.mrm.db.json")
	if data, err := os.ReadFile(dbFile); err == nil {
		dbCRC = crc32.ChecksumIEEE(data)
	} else {
		// No BO file — check the current in-memory (embedded) module
		dbCRC = configurator.complianceCRC(configurator.DBModule)
	}
	if dbCRC != 0 && dbCRC != configurator.ActiveDBCRC {
		configurator.PendingDBCRC = dbCRC
		pending = true
	} else {
		configurator.PendingDBCRC = 0
	}

	prxCRC := uint32(0)
	prxFile := filepath.Join(pluginDataDir, "moduleset_mariadb.svc.mrm.proxy.json")
	if data, err := os.ReadFile(prxFile); err == nil {
		prxCRC = crc32.ChecksumIEEE(data)
	} else {
		prxCRC = configurator.complianceCRC(configurator.ProxyModule)
	}
	if prxCRC != 0 && prxCRC != configurator.ActivePrxCRC {
		configurator.PendingPrxCRC = prxCRC
		pending = true
	} else {
		configurator.PendingPrxCRC = 0
	}

	return pending
}

// HasPendingComplianceUpdate returns true when new compliance files are
// available but not yet accepted by the user.
func (configurator *Configurator) HasPendingComplianceUpdate() bool {
	return configurator.PendingDBCRC != 0 || configurator.PendingPrxCRC != 0
}

// AcceptComplianceUpdate loads the pending compliance (from PluginDataDir or
// the new embedded modules after a binary upgrade), replaces the in-memory
// modules, persists the full accepted compliance to disk, and clears the
// pending state. On next restart the accepted version is loaded from disk.
func (configurator *Configurator) AcceptComplianceUpdate(pluginDataDir string) error {
	// Try loading from PluginDataDir first (BO push)
	reloaded, _ := configurator.ReloadComplianceFromDataDir(pluginDataDir)
	if !reloaded {
		// No BO files — the change is from an embedded module upgrade.
		// Reload from the embedded defaults to get the new version.
		configurator.LoadDBModules()
		configurator.LoadProxyModules()
		configurator.ConfigDBTags = configurator.GetDBModuleTags()
		configurator.ConfigPrxTags = configurator.GetProxyModuleTags()
	}
	configurator.ActiveDBCRC = configurator.complianceCRC(configurator.DBModule)
	configurator.ActivePrxCRC = configurator.complianceCRC(configurator.ProxyModule)
	configurator.PendingDBCRC = 0
	configurator.PendingPrxCRC = 0
	// Persist the full accepted modules to disk so they survive the next restart.
	configurator.saveAcceptedCompliance()
	return nil
}

const (
	acceptedDBFile  = "accepted_compliance_db.json"
	acceptedPrxFile = "accepted_compliance_proxy.json"
	previousDBFile  = "accepted_compliance_db.json.old"
	previousPrxFile = "accepted_compliance_proxy.json.old"
)

// saveAcceptedCompliance writes the current in-memory compliance modules to
// disk so they survive binary upgrades. The previous accepted version is
// renamed to .old so operators can diff what changed.
func (configurator *Configurator) saveAcceptedCompliance() {
	if configurator.WorkingDir == "" {
		return
	}
	// Rotate: current accepted → .old (for diffing)
	dbPath := filepath.Join(configurator.WorkingDir, acceptedDBFile)
	prxPath := filepath.Join(configurator.WorkingDir, acceptedPrxFile)
	if _, err := os.Stat(dbPath); err == nil {
		os.Rename(dbPath, filepath.Join(configurator.WorkingDir, previousDBFile))
	}
	if _, err := os.Stat(prxPath); err == nil {
		os.Rename(prxPath, filepath.Join(configurator.WorkingDir, previousPrxFile))
	}
	// Write new accepted
	if data, err := json.Marshal(configurator.DBModule); err == nil {
		os.WriteFile(dbPath, data, 0644)
	}
	if data, err := json.Marshal(configurator.ProxyModule); err == nil {
		os.WriteFile(prxPath, data, 0644)
	}
}

// loadAcceptedCompliance reads previously accepted compliance modules from
// disk and replaces the in-memory modules. Returns true if at least one
// module was loaded from disk.
func (configurator *Configurator) loadAcceptedCompliance() bool {
	if configurator.WorkingDir == "" {
		return false
	}
	loaded := false

	dbPath := filepath.Join(configurator.WorkingDir, acceptedDBFile)
	if data, err := os.ReadFile(dbPath); err == nil {
		var module config.Compliance
		if err := json.Unmarshal(data, &module); err == nil && len(module.Rulesets) > 0 {
			configurator.DBModule = module
			loaded = true
		}
	}

	prxPath := filepath.Join(configurator.WorkingDir, acceptedPrxFile)
	if data, err := os.ReadFile(prxPath); err == nil {
		var module config.Compliance
		if err := json.Unmarshal(data, &module); err == nil && len(module.Rulesets) > 0 {
			configurator.ProxyModule = module
			loaded = true
		}
	}

	return loaded
}

// ComplianceTagChange describes one tag that was added, removed, or modified.
type ComplianceTagChange struct {
	Tag      string `json:"tag"`
	Category string `json:"category"`
	Action   string `json:"action"` // "added", "removed", "modified"
	OldCnf   string `json:"old_cnf,omitempty"`
	NewCnf   string `json:"new_cnf,omitempty"`
}

// ComplianceDiffResult is the structured diff between old and new compliance.
type ComplianceDiffResult struct {
	HasOld     bool                  `json:"has_old"`
	HasNew     bool                  `json:"has_new"`
	OldDBCRC   uint32                `json:"old_db_crc"`
	NewDBCRC   uint32                `json:"new_db_crc"`
	OldPrxCRC  uint32                `json:"old_prx_crc"`
	NewPrxCRC  uint32                `json:"new_prx_crc"`
	DBChanges  []ComplianceTagChange `json:"db_changes"`
	PrxChanges []ComplianceTagChange `json:"prx_changes"`
}

// ComplianceDiff compares the previous (.old) and current accepted compliance
// and returns a structured list of tag-level changes.
func (configurator *Configurator) ComplianceDiff() ComplianceDiffResult {
	result := ComplianceDiffResult{}
	if configurator.WorkingDir == "" {
		return result
	}

	// Load old and new DB modules
	oldDB := configurator.loadComplianceFile(filepath.Join(configurator.WorkingDir, previousDBFile))
	newDB := configurator.loadComplianceFile(filepath.Join(configurator.WorkingDir, acceptedDBFile))
	result.HasOld = oldDB != nil
	result.HasNew = newDB != nil
	if oldDB != nil {
		result.OldDBCRC = configurator.complianceCRC(*oldDB)
	}
	if newDB != nil {
		result.NewDBCRC = configurator.complianceCRC(*newDB)
	}

	if oldDB != nil && newDB != nil {
		result.DBChanges = configurator.diffModuleTags(oldDB, newDB)
	}

	// Load old and new Proxy modules
	oldPrx := configurator.loadComplianceFile(filepath.Join(configurator.WorkingDir, previousPrxFile))
	newPrx := configurator.loadComplianceFile(filepath.Join(configurator.WorkingDir, acceptedPrxFile))
	if oldPrx != nil {
		result.OldPrxCRC = configurator.complianceCRC(*oldPrx)
	}
	if newPrx != nil {
		result.NewPrxCRC = configurator.complianceCRC(*newPrx)
	}

	if oldPrx != nil && newPrx != nil {
		result.PrxChanges = configurator.diffModuleTags(oldPrx, newPrx)
	}

	return result
}

func (configurator *Configurator) loadComplianceFile(path string) *config.Compliance {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var module config.Compliance
	if err := json.Unmarshal(data, &module); err != nil || len(module.Rulesets) == 0 {
		return nil
	}
	return &module
}

// diffModuleTags compares two compliance modules at the tag level.
// For each tag it extracts the cnf content and reports added/removed/modified.
func (configurator *Configurator) diffModuleTags(oldMod, newMod *config.Compliance) []ComplianceTagChange {
	oldTags := extractTagCnfMap(oldMod)
	newTags := extractTagCnfMap(newMod)

	var changes []ComplianceTagChange

	// Removed or modified
	for tag, oldCnf := range oldTags {
		newCnf, exists := newTags[tag]
		if !exists {
			changes = append(changes, ComplianceTagChange{
				Tag:    tag,
				Action: "removed",
				OldCnf: oldCnf,
			})
		} else if oldCnf != newCnf {
			changes = append(changes, ComplianceTagChange{
				Tag:    tag,
				Action: "modified",
				OldCnf: oldCnf,
				NewCnf: newCnf,
			})
		}
	}

	// Added
	for tag, newCnf := range newTags {
		if _, exists := oldTags[tag]; !exists {
			changes = append(changes, ComplianceTagChange{
				Tag:    tag,
				Action: "added",
				NewCnf: newCnf,
			})
		}
	}

	return changes
}

// extractTagCnfMap builds a map of tag_name→cnf_content from a compliance module
// by iterating rulesets and extracting file variables.
func extractTagCnfMap(mod *config.Compliance) map[string]string {
	type fileVar struct {
		Path string `json:"path"`
		Fmt  string `json:"fmt"`
	}
	tags := make(map[string]string)
	for _, rule := range mod.Rulesets {
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(variable.Value), &fv); err != nil {
				continue
			}
			if !strings.HasSuffix(fv.Path, ".cnf") {
				continue
			}
			// Use var_name as the tag key (normalised)
			key := strings.ToLower(variable.Name)
			tags[key] = fv.Fmt
		}
	}
	return tags
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

func (configurator *Configurator) ConfigDiscovery(Variables *config.StringsMap, Plugins *dbhelper.PluginsMap) error {
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
		s3mem, err = strconv.ParseUint(Variables.Get("S3_PAGECACHE_BUFFER_SIZE"), 10, 64)
		if err != nil {
			return err
		}
		totalmem += s3mem
	}

	rocksmem := uint64(0)
	if _, ok := Variables.CheckAndGet("ROCKSDB_BLOCK_CACHE_SIZE"); ok {
		configurator.AddDBTag("myrocks")
		rocksmem, err = strconv.ParseUint(Variables.Get("ROCKSDB_BLOCK_CACHE_SIZE"), 10, 64)
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
						fpath := strings.ReplaceAll(f.Symlink, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init")
						if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
							configurator.Logger.Debugf("Config symlink %s", fpath)
						}
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

func (configurator *Configurator) GenerateDatabaseConfig(Datadir string, ClusterDir string, RemoteBasedir string, TemplateEnv map[string]string, RepMgrVersion string, preserve bool) error {

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
						fpath := strings.ReplaceAll(f.Symlink, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init")
						if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
							configurator.Logger.Debugf("Config symlink %s", fpath)
						}
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

	rootchk, err := crypto.ChecksumDirectory(Datadir+"/init", false)
	if err == nil {
		os.WriteFile(Datadir+"/init/root-checksum.txt", []byte(rootchk), 0644)
	}

	if preserve {
		difflist := []string{"01_preserved.cnf", "02_delta.cnf", "03_agreed.cnf"}

		for _, fname := range difflist {
			srcpath := filepath.Join(Datadir, fname)
			destpath := filepath.Join(Datadir, "init/etc/mysql/custom.d/", fname)

			// Check if the source file exists before copying
			if _, err := os.Stat(srcpath); err == nil {
				misc.CopyFile(srcpath, destpath)
			}
		}
	}

	// Override the dbjobs_new script from the embedded full version.
	// The compliance moduleset variable is truncated at 65535 bytes (MySQL TEXT
	// column limit in the OpenSVC collector), so we replace it with the complete
	// script embedded in the binary via go:embed share/scripts/dbjobs_new.sh.
	if scriptBytes, err := share.EmbededDbModuleFS.ReadFile("scripts/dbjobs_new.sh"); err == nil {
		dbjobsPath := filepath.Join(Datadir, "init", "init", "dbjobs_new")
		os.MkdirAll(filepath.Dir(dbjobsPath), 0775)
		content := misc.ExtractKey(string(scriptBytes), TemplateEnv)
		if err := os.WriteFile(dbjobsPath, []byte(content), 0755); err != nil {
			configurator.Logger.Errorf("Failed to write embedded dbjobs_new: %s", err)
		}
	}

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
						if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
							configurator.Logger.Debugf("content %s %s", filter, rule.Filter)
						}
						if filter == "" || strings.Contains(rule.Filter, filter) {
							var f Link
							json.Unmarshal([]byte(variable.Value), &f)
							fpath := Datadir + "/init/etc/mysql/conf.d/"
							if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
								configurator.Logger.Debugf("Config symlink %s , %s", fpath, f.Target)
							}

							file, err := os.Open(fpath + f.Target)
							if err == nil {
								r, _ := regexp.Compile(cmd)
								scanner := bufio.NewScanner(file)
								for scanner.Scan() {
									if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
										configurator.Logger.Debugf("content: %s", scanner.Text())
									}

									if r.MatchString(scanner.Text()) {
										cmd_lines := strings.Split(scanner.Text(), ":")
										if len(cmd_lines) > 1 {
											mydynamicconf = mydynamicconf + cmd_lines[1]
										} else {
											return mydynamicconf, fmt.Errorf("Error in dynamic config: separator ':' not found in file")
										}
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

// GetTagSQL extracts the SQL for a compliance module tag directly from the in-memory
// module — no deployed files required.
//
// tagName is matched against each ruleset's fset_name (e.g. "with_sec_localinfile",
// "default_security").  cmdPrefix is one of "mariadb_command", "mysql_command",
// "mariadb_default", or "mysql_default".
//
// Returns the raw SQL string after the colon (may contain multiple semicolon-separated
// statements) or "" when the tag or prefix is not found.
func (configurator *Configurator) GetTagSQL(tagName, cmdPrefix string) string {
	type fileVar struct {
		Fmt string `json:"fmt"`
	}
	commentPrefix := "# " + cmdPrefix + ":"
	var result strings.Builder
	for _, rule := range configurator.DBModule.Rulesets {
		if !strings.Contains(rule.Filter, tagName) {
			continue
		}
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(variable.Value), &fv); err != nil {
				continue
			}
			for _, line := range strings.Split(fv.Fmt, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, commentPrefix) {
					sql := strings.TrimSpace(strings.TrimPrefix(trimmed, commentPrefix))
					result.WriteString(sql)
				}
			}
		}
	}
	return result.String()
}

// GetTagMyCnf returns the my.cnf snippet from a compliance module tag's cnf fmt content.
// It extracts the non-comment, non-empty lines starting from the first [section] header.
// tagName is matched against each ruleset's fset_name.
func (configurator *Configurator) GetTagMyCnf(tagName string) string {
	type fileVar struct {
		Path string `json:"path"`
		Fmt  string `json:"fmt"`
	}
	type symlinkVar struct {
		Target string `json:"target"`
	}
	// Flatten: remove underscores for comparison so tag "nodoublewrite" matches
	// var_name "db_cnf_disk_no_doublewrite" (which flattens to "dbcnfdisknodoublewrite").
	tagFlat := strings.ReplaceAll(strings.ToLower(tagName), "_", "")

	// Pass 1: direct match by filter (fset_name) or flattened var_name.
	for _, rule := range configurator.DBModule.Rulesets {
		if !strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf") {
			continue
		}
		filterMatch := rule.Filter != "" && strings.HasSuffix(rule.Filter, tagName)
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			varFlat := strings.ReplaceAll(strings.ToLower(variable.Name), "_", "")
			if !filterMatch && !strings.Contains(varFlat, tagFlat) {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(variable.Value), &fv); err != nil {
				continue
			}
			if !strings.HasSuffix(fv.Path, ".cnf") {
				continue
			}
			return configurator.extractCnfContent(fv.Fmt)
		}
		// Pass 2: if filter matched but no file variable found (only symlink),
		// resolve the symlink target filename and find it in the generic ruleset.
		if filterMatch {
			for _, variable := range rule.Variables {
				if variable.Class != "symlink" {
					continue
				}
				var sv symlinkVar
				if err := json.Unmarshal([]byte(variable.Value), &sv); err != nil {
					continue
				}
				targetFile := filepath.Base(sv.Target)
				if !strings.HasSuffix(targetFile, ".cnf") {
					continue
				}
				// Search the generic ruleset for a file with this path basename.
				return configurator.getFileContentByBasename(targetFile)
			}
		}
	}
	return ""
}

// extractCnfContent returns all non-empty lines from a cnf fmt field.
func (configurator *Configurator) extractCnfContent(fmt string) string {
	var result strings.Builder
	for _, line := range strings.Split(fmt, "\n") {
		if strings.TrimSpace(line) != "" {
			result.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(result.String())
}

// getFileContentByBasename finds a file variable by its path basename across
// all rulesets and returns its content. Used to resolve symlink targets.
func (configurator *Configurator) getFileContentByBasename(basename string) string {
	type fileVar struct {
		Path string `json:"path"`
		Fmt  string `json:"fmt"`
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if !strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf") {
			continue
		}
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(variable.Value), &fv); err != nil {
				continue
			}
			if filepath.Base(fv.Path) == basename {
				return configurator.extractCnfContent(fv.Fmt)
			}
		}
	}
	return ""
}

// ParseVariableNamesFromCnf extracts MySQL/MariaDB variable names from cnf
// file content. Parses lines like "innodb_buffer_pool_size=128M" and returns
// a deduplicated list of normalised variable names (lowercase, hyphens→underscores).
// This is a standalone function — it works on any cnf string, not on a tag.
func ParseVariableNamesFromCnf(cnf string) []string {
	if cnf == "" {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(cnf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		// Skip lines that look like shell commands or template macros
		if strings.HasPrefix(line, "@") || strings.HasPrefix(line, "if ") ||
			strings.HasPrefix(line, "mariadb_") || strings.HasPrefix(line, "mysql_") ||
			strings.Contains(line, "$(") || strings.Contains(line, "${") ||
			strings.Contains(line, "%%ENV:") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		varName := NormaliseVariableName(parts[0])
		if varName != "" && !seen[varName] {
			seen[varName] = true
			names = append(names, varName)
		}
	}
	return names
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
						if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
							configurator.Logger.Debugf("content %s %s", filter, rule.Filter)
						}
						if filter == "" || strings.Contains(rule.Filter, filter) {
							var f Link
							json.Unmarshal([]byte(variable.Value), &f)
							fpath := datadir + "/init/etc/mysql/conf.d/"
							if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
								configurator.Logger.Debugf("Config symlink %s , %s", fpath, f.Target)
							}
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

	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}

	var f File
	json.Unmarshal([]byte(variable.Value), &f)
	fpath := strings.ReplaceAll(f.Path, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init")
	dir := filepath.Dir(fpath)
	if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
		configurator.Logger.Debugf("Config create %s", fpath)
	}

	// create directory
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.FileMode(0775))
		if err != nil {
			return fmt.Errorf("Compliance create directory %q: %s", dir, err)
		}
	}

	if len(fpath) > 0 && fpath[len(fpath)-1:] != "/" && (configurator.IsFilterInDBTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.db.cnf.generic") {
		content := misc.ExtractKey(f.Content, TemplateEnv)

		if configurator.IsFilterInDBTags("docker") && configurator.ClusterConfig.ProvOrchestrator != config.ConstOrchestratorLocalhost {
			if configurator.IsFilterInDBTags("wsrep") {
				//if galera don't cusomized system files
				if strings.Contains(content, "./.system") && !strings.Contains(content, "exclude") && !strings.Contains(content, "ignore") {
					content = ""
				}
			} else {
				content = strings.ReplaceAll(content, "./.system", "/var/lib/mysql/.system")
			}
		}

		if configurator.ClusterConfig.ProvOrchestrator == config.ConstOrchestratorLocalhost {
			content = strings.ReplaceAll(content, "includedir ..", "includedir "+RemoteBasedir+"/init")
			content = strings.ReplaceAll(content, "../etc/mysql", RemoteBasedir+"/init/etc/mysql")

		} else if configurator.ClusterConfig.ProvOrchestrator == config.ConstOrchestratorSlapOS {
			content = strings.ReplaceAll(content, "includedir ..", "includedir "+RemoteBasedir+"/")
			content = strings.ReplaceAll(content, "../etc/mysql", RemoteBasedir+"/etc/mysql")
			content = strings.ReplaceAll(content, "./.system", RemoteBasedir+"/var/lib/mysql/.system")
		}

		outFile, err := os.Create(fpath)
		if err != nil {
			return fmt.Errorf("Compliance create file failed %q: %s", fpath, err)
		} else {
			if !strings.Contains(content, "# Generated by Signal18 replication-manager") {
				_, err = fmt.Fprintf(outFile, "# %s", TemplateEnv["%%ENV:GENLINE%%"])
				if err != nil {
					outFile.Close()
					return fmt.Errorf("Compliance writing header file failed %q: %s", fpath, err)
				}
			}

			_, err = outFile.WriteString(content)
			if err != nil {
				outFile.Close()
				return fmt.Errorf("Compliance writing file failed %q: %s", fpath, err)
			}
			if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
				configurator.Logger.Debugf("Variable name %s", variable.Name)
			}
		}

	}
	return nil
}

func (configurator *Configurator) WriteProxyConfigFile(Datadir string, TemplateEnv map[string]string, RepMgrVersion string, rule *config.ComplianceRuleset, variable *config.ComplianceVariable) error {

	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}

	var f File
	json.Unmarshal([]byte(variable.Value), &f)
	fpath := strings.ReplaceAll(f.Path, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", Datadir+"/init")
	dir := filepath.Dir(fpath)
	if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
		configurator.Logger.Debugf("Config create %s", fpath)
	}

	// create directory
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.FileMode(0775))
		if err != nil {
			return fmt.Errorf("Compliance create directory %q: %s", dir, err)
		}
	}

	if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
		configurator.Logger.Debugf("rule %s filter %s %t", rule.Name, rule.Filter, configurator.IsFilterInProxyTags(rule.Filter))
	}

	if fpath[len(fpath)-1:] != "/" && (configurator.IsFilterInProxyTags(rule.Filter) || rule.Filter == "") {
		content := misc.ExtractKey(f.Content, TemplateEnv)
		outFile, err := os.Create(fpath)
		if err != nil {
			return fmt.Errorf("Compliance create file failed %q: %s", fpath, err)
		} else {
			// Check if we have # %%ENV:GENLINE%% in content to place the header on the next line
			if !strings.Contains(content, "# Generated by Signal18 replication-manager") {
				_, err = fmt.Fprintf(outFile, "# %s", TemplateEnv["%%ENV:GENLINE%%"])
				if err != nil {
					outFile.Close()
					return fmt.Errorf("Compliance writing header file failed %q: %s", fpath, err)
				}
			}
			_, err = outFile.WriteString(content)
			if err != nil {
				outFile.Close()
				return fmt.Errorf("Compliance writing file failed %q: %s", fpath, err)
			}

			if configurator.ClusterConfig.IsEligibleForPrinting(config.ConstLogModConfigLoad, config.LvlDbg) || configurator.ClusterConfig.Verbose {
				configurator.Logger.Debugf("Variable name %s", variable.Name)
			}
		}
	}

	return nil
}
