// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// globalBufferVariables are the global memory buffers a server allocates once, whatever
// its connection count: their sum must fit the container memory.
var globalBufferVariables = []string{"innodb_buffer_pool_size", "key_buffer_size", "aria_pagecache_buffer_size", "query_cache_size"}

// mysqlSizeMB reads a MySQL size value in MB: a plain number is bytes, K/M/G/T suffixes
// are powers of 1024 (my.cnf units).
func mysqlSizeMB(v string) (int64, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	mult := 1.0 / (1024 * 1024)
	switch {
	case strings.HasSuffix(v, "K"):
		mult, v = 1.0/1024, strings.TrimSuffix(v, "K")
	case strings.HasSuffix(v, "M"):
		mult, v = 1, strings.TrimSuffix(v, "M")
	case strings.HasSuffix(v, "G"):
		mult, v = 1024, strings.TrimSuffix(v, "G")
	case strings.HasSuffix(v, "T"):
		mult, v = 1024*1024, strings.TrimSuffix(v, "T")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return int64(f * mult), true
}

// preservedGlobalBuffersOverMemory sums the global buffers the server will start with
// (the preserved value where one is kept, the configurator value otherwise) and reports
// the sum and the preserved part when at least one is preserved and the sum reaches the
// container memory (prov-db-memory). A preserve kept from a larger container (e.g. an
// 8 GB buffer pool in an 8 GB container after a resize down) is how a full read
// OOM-killed a master (dbaas-portal, 2026-11-17).
func (server *ServerMonitor) preservedGlobalBuffersOverMemory() (totalMB int64, preserved []string, over bool) {
	cluster := server.ClusterGroup
	memMB, ok := mysqlSizeMB(cluster.Conf.ProvMem + "M")
	if !ok || memMB <= 0 || server.VariablesMap == nil {
		return 0, nil, false
	}
	for _, name := range globalBufferVariables {
		v, ok := server.VariablesMap.CheckAndGet(name)
		if !ok {
			continue
		}
		val := ""
		if v.IsPreserved() {
			val = v.Preserved.String()
			preserved = append(preserved, name+"="+val)
		} else if v.Config != nil {
			val = v.Config.String()
		}
		if mb, ok := mysqlSizeMB(val); ok {
			totalMB += mb
		}
	}
	return totalMB, preserved, len(preserved) > 0 && totalMB >= memMB
}

// checkPreservedMemoryFitsContainer opens WARN0322 (config domain, this server) while the
// global buffers of its preserved config reach the container memory.
func (server *ServerMonitor) checkPreservedMemoryFitsContainer() {
	cluster := server.ClusterGroup
	total, preserved, over := server.preservedGlobalBuffersOverMemory()
	if !over {
		return
	}
	memMB, _ := mysqlSizeMB(cluster.Conf.ProvMem + "M")
	cluster.ConfigStateMachine.AddState("WARN0322", state.State{ErrType: config.LvlWarn, ErrKey: "WARN0322", ServerUrl: server.URL, ErrFrom: "CONF",
		ErrDesc: fmt.Sprintf(clusterError["WARN0322"], server.URL, total, strings.Join(preserved, ", "), memMB)})
}
