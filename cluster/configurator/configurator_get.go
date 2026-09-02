// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"hash/crc32"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
)

func (configurator *Configurator) GetDBModuleTags() []*v3.Tag {
	tags := make([]*v3.Tag, 0, len(configurator.DBModule.Filtersets))
	for _, value := range configurator.DBModule.Filtersets {
		s := strings.Split(value.Name, ".")
		tags = append(tags, &v3.Tag{
			Id:       uint64(value.ID),
			Name:     s[len(s)-1],
			Category: s[len(s)-2],
		})
	}
	return tags
}

func (configurator *Configurator) GetDBModuleCategories() map[string]string {
	cats := make(map[string]string)
	for _, value := range configurator.DBModule.Filtersets {
		var t v3.Tag
		t.Id = uint64(value.ID)
		s := strings.Split(value.Name, ".")
		t.Name = s[len(s)-1]
		t.Category = s[len(s)-2]
		cats[t.Category] = t.Name
	}
	return cats
}

func (configurator *Configurator) GetDBTags() []string {
	return configurator.DBTags
}
func (configurator *Configurator) GetProxyTags() []string {
	return configurator.ProxyTags
}

func (configurator *Configurator) GetProxyModuleTags() []*v3.Tag {
	tags := make([]*v3.Tag, 0, len(configurator.ProxyModule.Filtersets))
	for _, value := range configurator.ProxyModule.Filtersets {
		s := strings.SplitAfter(value.Name, ".")
		tags = append(tags, &v3.Tag{
			Id:   uint64(value.ID),
			Name: s[len(s)-1],
		})
	}
	return tags
}

func (configurator *Configurator) GetConfigMaxConnections() string {
	return strconv.Itoa(configurator.ClusterConfig.ProvMaxConnections)
}

func (configurator *Configurator) GetConfigExpireLogDays() string {
	return strconv.Itoa(configurator.ClusterConfig.ProvExpireLogDays)
}

func (configurator *Configurator) GetConfigRelaySpaceLimit() string {
	return strconv.Itoa(10 * 1024 * 1024)
}

func (configurator *Configurator) GetConfigReplicationDomain(ClusterName string) string {
	// Multi source need differnt domain id
	if configurator.ClusterConfig.MasterConn != "" && configurator.ClusterConfig.ProvDomain == "0" {
		crcTable := crc32.MakeTable(0xD5828281)
		return strconv.FormatUint(uint64(crc32.Checksum([]byte(ClusterName), crcTable)), 10)
	}
	return configurator.ClusterConfig.ProvDomain
}

const memReserveMB int64 = 2048

func (configurator *Configurator) getUsableMemoryMB() (int64, error) {
	memMB, err := config.ParseUnitMeasurementToInt("M,bytes,required", configurator.ClusterConfig.ProvMem, true)
	if err != nil {
		return 0, err
	}
	containermem := int64(memMB)
	usable := containermem - memReserveMB
	if usable < 0 {
		usable = 0
	}
	return usable, nil
}

// minEngineMemMB is the floor for any allocated storage-engine buffer, in MB.
// Below this an engine can drop under its own startup minimum — e.g. InnoDB with
// a 16K page size refuses to start with innodb_buffer_pool_size under 6 MiB — which
// is exactly how a small/misresolved prov-db-memory produced innodb_buffer_pool_size=0M
// and stopped the DB from booting. Every engine that is allocated memory gets at least this.
const minEngineMemMB int64 = 128

// engineMemMB returns an engine buffer size in MB from the usable memory and the
// engine's shared-memory percentage. An enabled engine (pct > 0) never gets less
// than minEngineMemMB; a disabled engine (pct <= 0) stays at 0 (so we never silently
// turn on an engine, e.g. the query cache, that is meant to be off).
func engineMemMB(usableMB, pct int64) int64 {
	if pct <= 0 {
		return 0
	}
	if v := usableMB * pct / 100; v > minEngineMemMB {
		return v
	}
	return minEngineMemMB
}

func (configurator *Configurator) GetConfigInnoDBBPSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["innodb"])), 10)
}

func (configurator *Configurator) GetConfigMyISAMKeyBufferSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["myisam"])), 10)
}

func (configurator *Configurator) GetConfigTokuDBBufferSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["tokudb"])), 10)
}

func (configurator *Configurator) GetConfigQueryCacheSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["querycache"])), 10)
}

func (configurator *Configurator) GetConfigAriaCacheSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["aria"])), 10)
}

func (configurator *Configurator) GetConfigS3CacheSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["s3"])), 10)
}

func (configurator *Configurator) GetConfigRocksDBCacheSize() string {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return strconv.FormatInt(minEngineMemMB, 10)
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	return strconv.FormatInt(engineMemMB(usable, int64(sharedmempcts["rocksdb"])), 10)
}

// GetConfigPFSMemoryMB returns the memory share budgeted to the Performance
// Schema, from the "pfs" entry of prov-db-memory-shared-pct (default 5 when
// absent; pfs:0 disables the budget so MariaDB defaults stay untouched).
func (configurator *Configurator) GetConfigPFSMemoryMB() int64 {
	usable, err := configurator.getUsableMemoryMB()
	if err != nil {
		return 0
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	pct, ok := sharedmempcts["pfs"]
	if !ok {
		pct = 5
	}
	if pct <= 0 {
		return 0
	}
	return usable * int64(pct) / 100
}

// GetConfigPFSDigestLength derives performance_schema_max_{digest,digest_text,
// sql_text}_length from the PFS memory budget. The fixed 16384 capture sizing
// was measured to make P_S preallocate ~727MB at init (history_long consumers
// with 1000 connections) and OOM small cgroups before InnoDB init (#1749); it
// is only emitted when the budget affords it, with the MariaDB default (1024)
// below and an intermediate tier in between.
func (configurator *Configurator) GetConfigPFSDigestLength() string {
	budget := configurator.GetConfigPFSMemoryMB()
	switch {
	case budget >= 768:
		return "16384"
	case budget >= 192:
		return "4096"
	default:
		return "1024"
	}
}

func (configurator *Configurator) GetConfigMyISAMKeyBufferSegements() string {
	value, err := strconv.ParseInt(configurator.GetConfigMyISAMKeyBufferSize(), 10, 64)
	if err != nil {
		return "1"
	}
	value = value/8000 + 1
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBIOCapacity() string {
	value, err := strconv.ParseInt(configurator.ClusterConfig.ProvIops, 10, 64)
	if err != nil {
		return "100"
	}
	value = value / 3
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBIOCapacityMax() string {
	value, err := strconv.ParseInt(configurator.ClusterConfig.ProvIops, 10, 64)
	if err != nil {
		return "200"
	}
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBMaxDirtyPagePct() string {
	/*	mem, err := strconv.ParseInt(cluster.GetConfigInnoDBBPSize(), 10, 64)
		if err != nil {
			return "20"
		}
		//Compute the ration of memory compare to  a G
		//	value := mem/1000

	*/
	value := int64(40)
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBMaxDirtyPagePctLwm() string {
	value := int64(20)
	s10 := strconv.FormatInt(value, 10)
	return s10
}

// redoFloorMB/redoCapMB bound the redo size in MB, both powers of two. The old
// 1024MB floor produced a redo larger than the whole memory cgroup on small
// prov-db-memory instances, making crash recovery unaffordable (#1749).
const redoFloorMB int64 = 128
const redoCapMB int64 = 16384

// floorPow2MB returns the largest power of two (in MB) not exceeding n, clamped
// to [redoFloorMB, redoCapMB]. Powers of two keep the generated redo sizes
// clean and predictable.
func floorPow2MB(n int64) int64 {
	p := redoFloorMB
	for p*2 <= n {
		p *= 2
	}
	if p > redoCapMB {
		p = redoCapMB
	}
	return p
}

// GetConfigInnoDBLogFileSize sizes the redo at a power of two around a quarter
// of the InnoDB buffer pool (BP/4): BP/2 over-allocated the redo (up to 8GB on
// a 32GB instance), and modern MariaDB/MySQL flushing no longer needs a redo
// half the buffer pool. Floor 128MB, cap 16GB; the smallredolog tag forces the
// floor.
func (configurator *Configurator) GetConfigInnoDBLogFileSize() string {
	//result in MB
	if configurator.HaveDBTag("smallredolog") {
		return strconv.FormatInt(redoFloorMB, 10)
	}
	bp, err := strconv.ParseInt(configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return strconv.FormatInt(redoFloorMB, 10)
	}
	return strconv.FormatInt(floorPow2MB(bp/4), 10)
}

func (configurator *Configurator) GetConfigInnoDBLogBufferSize() string {
	//result in MB
	value := int64(16)
	s10 := strconv.FormatInt(value, 10)
	return s10
}

// GetConfigInnoDBBPInstances configure BP/8G of the ConfigMemory in Megabyte
func (configurator *Configurator) GetConfigInnoDBBPInstances() string {
	value, err := strconv.ParseInt(configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return "1"
	}
	value = value/8000 + 1
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBWriteIoThreads() string {
	iopsLatency, err := strconv.ParseFloat(configurator.ClusterConfig.ProvIopsLatency, 64)
	if err != nil {
		return "4"
	}
	iops, err := strconv.ParseFloat(configurator.ClusterConfig.ProvIops, 64)
	if err != nil {
		return "4"
	}
	nbthreads := int(iopsLatency * iops)
	if nbthreads < 1 {
		return "1"
	}
	strnbthreads := strconv.Itoa(nbthreads)
	return strnbthreads
}

func (configurator *Configurator) GetConfigInnoDBReadIoThreads() string {
	return configurator.ClusterConfig.ProvCores
}

func (configurator *Configurator) GetConfigInnoDBPurgeThreads() string {
	return "4"
}

func (configurator *Configurator) GetConfigInnoDBLruFlushSize() string {
	return "1024"
}

func (configurator *Configurator) GetConfigDBCores() string {
	return configurator.ClusterConfig.ProvCores
}

func (configurator *Configurator) GetConfigDBMemory() string {
	return configurator.ClusterConfig.ProvMem
}

func (configurator *Configurator) GetConfigDBDisk() string {
	return configurator.ClusterConfig.ProvDisk
}

func (configurator *Configurator) GetConfigDBDiskIOPS() string {
	return configurator.ClusterConfig.ProvIops
}

func (configurator *Configurator) GetConfigDBMaxConnections() int {
	return configurator.ClusterConfig.ProvMaxConnections
}

func (configurator *Configurator) GetConfigProxyTags() string {
	return strings.Join(configurator.ProxyTags, ",")
}

func (configurator *Configurator) GetConfigDBTags() string {
	return strings.Join(configurator.DBTags, ",")
}

func (configurator *Configurator) GetConfigDBExpireLogDays() int {

	return configurator.ClusterConfig.ProvExpireLogDays
}

func (configurator *Configurator) GetConfigProxyCores() string {
	return configurator.ClusterConfig.ProvProxCores
}

func (configurator *Configurator) GetProxyMemorySize() string {
	return configurator.ClusterConfig.ProvProxMem
}

func (configurator *Configurator) GetProxyDiskSize() string {
	return configurator.ClusterConfig.ProvProxDisk
}

func (configurator *Configurator) GetSshStartDBScript() string {
	dbtype := "mariadb"
	if configurator.ClusterConfig.OnPremiseSSHStartDbScript != "" {
		return configurator.ClusterConfig.OnPremiseSSHStartDbScript
	}
	if configurator.HaveDBTag("rpm") {
		return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/repository/redhat/" + dbtype + "/start"
	}
	if configurator.HaveDBTag("package") {
		return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/package/linux/" + dbtype + "/start"
	}
	return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/repository/debian/" + dbtype + "/start"
}

// GetActiveDBOsFamily returns the OS family (apt/yum) matching the cluster's active DB tags.
func (configurator *Configurator) GetActiveDBOsFamily() *DBOsFamily {
	if configurator.DBDistributions == nil {
		return nil
	}
	return configurator.DBDistributions.GetOsFamily(configurator.HaveDBTag)
}

// GetActiveDBDeployMethod returns the deploy method (docker/tarball/repository) matching the cluster's active DB tags.
// For container orchestrators (OpenSVC, K8S), defaults to docker even without an explicit docker tag.
func (configurator *Configurator) GetActiveDBDeployMethod() *DBDeployMethod {
	if configurator.DBDistributions == nil {
		return nil
	}
	dm := configurator.DBDistributions.GetDeployMethod(configurator.HaveDBTag)
	// If no explicit deploy tag matched and the orchestrator is container-based,
	// the default should be docker, not repository.
	if dm != nil && dm.Filter == "" && isContainerOrchestrator(configurator.ClusterConfig.ProvOrchestrator) {
		if dockerDM := configurator.DBDistributions.GetDeployMethodByType("docker"); dockerDM != nil {
			return dockerDM
		}
	}
	return dm
}

func (configurator *Configurator) GetSshUpgradeDBScript() string {
	dbtype := "mariadb"
	if configurator.ClusterConfig.OnPremiseSSHUpgradeDbScript != "" {
		return configurator.ClusterConfig.OnPremiseSSHUpgradeDbScript
	}
	if configurator.HaveDBTag("rpm") {
		return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/repository/redhat/" + dbtype + "/upgrade"
	}
	if configurator.HaveDBTag("package") {
		return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/package/linux/" + dbtype + "/upgrade"
	}
	return configurator.ClusterConfig.HttpRoot + "/static/configurator/onpremise/repository/debian/" + dbtype + "/upgrade"
}
