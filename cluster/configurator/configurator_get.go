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

	v3 "github.com/signal18/replication-manager/repmanv3"
)

func (configurator *Configurator) GetDBModuleTags() []v3.Tag {
	var tags []v3.Tag
	for _, value := range configurator.DBModule.Filtersets {
		var t v3.Tag
		t.Id = uint64(value.ID)
		s := strings.Split(value.Name, ".")
		t.Name = s[len(s)-1]
		t.Category = s[len(s)-2]
		tags = append(tags, t)
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

func (configurator *Configurator) GetProxyModuleTags() []v3.Tag {
	var tags []v3.Tag
	for _, value := range configurator.ProxyModule.Filtersets {
		var t v3.Tag
		t.Id = uint64(value.ID)
		s := strings.SplitAfter(value.Name, ".")
		t.Name = s[len(s)-1]
		tags = append(tags, t)
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

// GetConfigInnoDBBPSize configure 80% of the ConfigMemory in Megabyte
func (configurator *Configurator) GetConfigInnoDBBPSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["innodb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigMyISAMKeyBufferSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["myisam"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigTokuDBBufferSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()

	containermem = containermem * int64(sharedmempcts["tokudb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigQueryCacheSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["querycache"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigAriaCacheSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["aria"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigS3CacheSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["s3"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
}

func (configurator *Configurator) GetConfigRocksDBCacheSize() string {
	containermem, err := strconv.ParseInt(configurator.ClusterConfig.ProvMem, 10, 64)
	if err != nil {
		return "128"
	}
	sharedmempcts, _ := configurator.ClusterConfig.GetMemoryPctShared()
	containermem = containermem * int64(sharedmempcts["rocksdb"]) / 100
	s10 := strconv.FormatInt(containermem, 10)
	return s10
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
	var value int64
	value = 40
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBMaxDirtyPagePctLwm() string {
	var value int64
	value = 20
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBLogFileSize() string {
	//result in MB
	var valuemin int64
	var valuemax int64
	valuemin = 1024
	valuemax = 20 * 1024
	value, err := strconv.ParseInt(configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return "1024"
	}
	value = value / 2
	if value < valuemin {
		value = valuemin
	}
	if value > valuemax {
		value = valuemax
	}
	if configurator.HaveDBTag("smallredolog") {
		return "128"
	}
	s10 := strconv.FormatInt(value, 10)
	return s10
}

func (configurator *Configurator) GetConfigInnoDBLogBufferSize() string {
	//result in MB
	var value int64
	value = 16
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
