package cluster

import (
	"hash/crc64"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (cluster *Cluster) loadTableColumns(server *ServerMonitor, tablelist []dbhelper.Table, progress func(int, int64)) (int, int64, error) {
	if !cluster.Conf.MonitorSchemaColumns {
		return 0, 0, nil
	}
	if server == nil {
		return 0, 0, nil
	}

	timeout := time.Duration(cluster.Conf.MonitorSchemaScanTimeout) * time.Second
	delay := time.Duration(cluster.Conf.MonitorSchemaScanDelayMs) * time.Millisecond

	processedTables := 0
	processedBytes := int64(0)
	for i := range tablelist {
		t := &tablelist[i]
		columns, logs, err := dbhelper.LoadTableColumns(server.Conn, server.DBVersion, t.TableSchema, t.TableName, timeout)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not fetch table columns %s", err)
		if err != nil {
			return processedTables, processedBytes, err
		}
		t.TableColumns = columns
		processedTables++
		processedBytes += t.DataLength + t.IndexLength
		if progress != nil {
			progress(processedTables, processedBytes)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	return processedTables, processedBytes, nil
}

func (cluster *Cluster) loadTableIndexes(server *ServerMonitor, tablelist []dbhelper.Table, progress func(int, int64)) (int, int64, error) {
	if !cluster.Conf.MonitorSchemaIndexes {
		return 0, 0, nil
	}
	if server == nil {
		return 0, 0, nil
	}

	timeout := time.Duration(cluster.Conf.MonitorSchemaScanTimeout) * time.Second
	delay := time.Duration(cluster.Conf.MonitorSchemaScanDelayMs) * time.Millisecond

	processedTables := 0
	processedBytes := int64(0)
	for i := range tablelist {
		t := &tablelist[i]
		indexes, logs, err := dbhelper.LoadTableIndexes(server.Conn, server.DBVersion, t.TableSchema, t.TableName, timeout)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not fetch table indexes %s", err)
		if err != nil {
			return processedTables, processedBytes, err
		}
		t.TableIndexes = indexes
		processedTables++
		processedBytes += t.DataLength + t.IndexLength
		if progress != nil {
			progress(processedTables, processedBytes)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	return processedTables, processedBytes, nil
}

func (cluster *Cluster) hashTableMetadata(tablelist []dbhelper.Table) {
	crc64Table := crc64.MakeTable(0xC96C5795D7870F42)
	for i := range tablelist {
		t := &tablelist[i]
		if cluster.Conf.MonitorSchemaColumns {
			t.HashColumns(crc64Table)
		} else {
			t.TableColumns = nil
			t.TableColumnMap = nil
			t.TableColumnsCrc64 = 0
		}
		if cluster.Conf.MonitorSchemaIndexes {
			t.HashIndexes(crc64Table)
		} else {
			t.TableIndexes = nil
			t.TableIndexMap = nil
			t.TableIndexesCrc64 = 0
		}
		t.HashTableCrc(crc64Table)
	}
}
