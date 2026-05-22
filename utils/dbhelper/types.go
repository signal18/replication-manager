// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// This file contains data structure definitions for database operations.
// It defines types for slave status, master status, tables, grants, processlist,
// binlog events, performance schema queries, and other database metadata structures.

package dbhelper

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const debug = false
const (
	DDMMYYYYhhmmss = "2006-01-02 15:04:05"
)

// chunk represents a table checksum chunk
type Chunk struct {
	ChunkId             uint64 `json:"chunkId"`
	ChunkRangeCondition string `json:"chunkRangeCondition"`
	ChunkCheckSum       uint64 `json:"chunkCheckSum"`
}

// Plugin represents a database plugin
type Plugin struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Type    string         `json:"type"`
	Library sql.NullString `json:"library"`
	License string         `json:"license"`
}

// MetaDataLock represents metadata lock information
type MetaDataLock struct {
	Thread_id     uint64         `json:"threadId" db:"THREAD_ID"`
	Lock_mode     sql.NullString `json:"lockMode" db:"LOCK_MODE"`
	Lock_duration sql.NullString `json:"lockDuration" db:"LOCK_DURATION"`
	Lock_time_ms  sql.NullInt64  `json:"lockTimeMs" db:"LOCK_TIME_MS"`
	Lock_type     sql.NullString `json:"lockType" db:"LOCK_TYPE"`
	Lock_catalog  sql.NullString `json:"lockCatalog" db:"TABLE_CATALOG"`
	Lock_schema   sql.NullString `json:"lockSchema" db:"TABLE_SCHEMA"`
	Lock_name     sql.NullString `json:"lockName" db:"TABLE_NAME"`
}

// ResponseTime represents query response time histogram data
type ResponseTime struct {
	Time  string `json:"time" db:"TIME"`
	Count uint64 `json:"count" db:"COUNT"`
	Total string `json:"total" db:"TOTAL"`
}

// PFSQuery represents Performance Schema query statistics
type PFSQuery struct {
	Digest           string          `json:"digest"`
	Query            string          `json:"query"`
	Sample_query     string          `json:"sampleQuery"`  // one concrete SQL example for this digest (for EXPLAIN)
	Digest_text      string          `json:"digestText"`
	Schema_name      string          `json:"shemaName"`
	Last_seen        string          `json:"lastSeen"`
	Plan_full_scan   string          `json:"planFullScan"`
	Plan_tmp_disk    int64           `json:"planTmpDisk"`
	Plan_tmp_mem     int64           `json:"planTmpMem"`
	Exec_count       int64           `json:"execCount"`
	Err_count        int64           `json:"errCount"`
	Warn_count       int64           `json:"warnCount"`
	Exec_time_total  string          `json:"execTimeTotal"`
	Exec_time_max    sql.NullFloat64 `json:"execTimeMax"`
	Exec_time_avg_ms sql.NullFloat64 `json:"execTimeAvgMs"`
	Rows_sent        int64           `json:"rowsSent"`
	Rows_sent_avg    int64           `json:"rowsSentAvg"`
	Rows_scanned     int64           `json:"rowsScanned"`
	Value            string          `json:"value"`
}

// PFSQuerySorter sorts PFSQuery by value
type PFSQuerySorter []PFSQuery

func (a PFSQuerySorter) Len() int      { return len(a) }
func (a PFSQuerySorter) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a PFSQuerySorter) Less(i, j int) bool {
	l, _ := strconv.ParseFloat(a[i].Value, 64)
	r, _ := strconv.ParseFloat(a[j].Value, 64)
	return l > r
}

// Column represents a table column definition
type Column struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Nullable  bool    `json:"nullable"`
	Default   *string `json:"default,omitempty"`
	Extra     string  `json:"extra,omitempty"`
	Charset   *string `json:"charset,omitempty"`
	Collation *string `json:"collation,omitempty"`
	Crc64     uint64  `json:"crc64,omitempty"`
}

// IndexColumn represents a column in an index
type IndexColumn struct {
	Name   string  `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Prefix *uint16 `protobuf:"varint,2,opt,name=prefix,proto3" json:"prefix,omitempty"`
}

// Index represents a table index
type Index struct {
	Name    string        `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Unique  bool          `protobuf:"varint,2,opt,name=unique,proto3" json:"unique,omitempty"`
	Type    string        `protobuf:"bytes,3,opt,name=type,proto3" json:"type,omitempty"`
	Crc64   uint64        `protobuf:"varint,4,opt,name=crc64,proto3" json:"crc64,omitempty"`
	Columns []IndexColumn `protobuf:"bytes,5,opt,name=columns,proto3" json:"columns,omitempty"`
}

// Table represents a database table with metadata
type Table struct {
	TableSchema        string             `protobuf:"bytes,1,opt,name=table_schema,json=tableSchema,proto3" json:"table_schema,omitempty"`
	TableName          string             `protobuf:"bytes,2,opt,name=table_name,json=tableName,proto3" json:"table_name,omitempty"`
	Engine             string             `protobuf:"bytes,3,opt,name=engine,proto3" json:"engine,omitempty"`
	TableRows          int64              `protobuf:"varint,4,opt,name=table_rows,json=tableRows,proto3" json:"table_rows"`
	DataLength         int64              `protobuf:"varint,5,opt,name=data_length,json=dataLength,proto3" json:"data_length"`
	IndexLength        int64              `protobuf:"varint,6,opt,name=index_length,json=indexLength,proto3" json:"index_length"`
	DataFree           int64              `protobuf:"varint,23,opt,name=data_free,json=dataFree,proto3" json:"data_free"`
	AvgRowLength       int64              `protobuf:"varint,24,opt,name=avg_row_length,json=avgRowLength,proto3" json:"avg_row_length"`
	TableCrc           uint64             `protobuf:"varint,7,opt,name=table_crc,json=tableCrc,proto3" json:"table_crc"`
	TableClusters      string             `protobuf:"bytes,8,opt,name=table_clusters,json=tableClusters,proto3" json:"table_clusters,omitempty"`
	TableSync          string             `protobuf:"bytes,9,opt,name=table_sync,json=tableSync,proto3" json:"table_sync"`
	TableColumns       []Column           `protobuf:"bytes,10,opt,name=table_columns,json=tableColumns,proto3" json:"table_columns,omitempty"`
	TableIndexes       []Index            `protobuf:"bytes,11,opt,name=table_indexes,json=tableIndexes,proto3" json:"table_indexes,omitempty"`
	TableColumnsCrc64  uint64             `protobuf:"varint,12,opt,name=table_columns_crc64,json=tableColumnsCrc64,proto3" json:"table_columns_crc64,omitempty"`
	TableIndexesCrc64  uint64             `protobuf:"varint,13,opt,name=table_indexes_crc64,json=tableIndexesCrc64,proto3" json:"table_indexes_crc64,omitempty"`
	TableType          string             `protobuf:"bytes,14,opt,name=table_type,json=tableType,proto3" json:"table_type,omitempty"`
	RowFormat          string             `protobuf:"bytes,15,opt,name=row_format,json=rowFormat,proto3" json:"row_format,omitempty"`
	TableCollation     string             `protobuf:"bytes,16,opt,name=table_collation,json=tableCollation,proto3" json:"table_collation,omitempty"`
	CreateOptions      string             `protobuf:"bytes,17,opt,name=create_options,json=createOptions,proto3" json:"create_options,omitempty"`
	TableComment       string             `protobuf:"bytes,18,opt,name=table_comment,json=tableComment,proto3" json:"table_comment,omitempty"`
	AutoIncrement      int64              `protobuf:"varint,19,opt,name=auto_increment,json=autoIncrement,proto3" json:"auto_increment"`
	TableChunksError   []Chunk            `protobuf:"bytes,20,opt,name=table_chunks_error,json=tableChunksError,proto3" json:"table_chunks_error,omitempty"`
	TableChunksCount   int64              `protobuf:"varint,21,opt,name=table_chunks_count,json=tableChunksCount,proto3" json:"table_chunks_count,omitempty"`
	TableChunksCurrent int64              `protobuf:"varint,22,opt,name=table_chunks_current,json=tableChunksCurrent,proto3" json:"table_chunks_current,omitempty"`
	TableColumnMap     map[string]*Column `protobuf:"-" json:"-"`
	TableIndexMap      map[string]*Index  `protobuf:"-" json:"-"`
	// TableParents lists tables this table references (outgoing FK / match).
	// Populated by GetTables() after columns and indexes are loaded.
	TableParents []TableLink `protobuf:"-" json:"table_parents,omitempty"`
	// TableChildren lists tables that reference this table (incoming FK / match).
	// Populated by GetTables() after columns and indexes are loaded.
	TableChildren []TableLink `protobuf:"-" json:"table_children,omitempty"`
	// SizeWeightPct is (data_length + index_length) as a percentage of the
	// total bytes across all tables returned in the same GetTables() call.
	// Used by the graph node-size encoding.
	SizeWeightPct float64 `protobuf:"-" json:"size_weight_pct,omitempty"`
}

// CanonicalizeColumns sorts table columns by name
func (x *Table) CanonicalizeColumns() {
	sort.Slice(x.TableColumns, func(i, j int) bool {
		return x.TableColumns[i].Name < x.TableColumns[j].Name
	})
}

// CanonicalizeIndexes sorts table indexes and index columns by name
func (x *Table) CanonicalizeIndexes() {
	sort.Slice(x.TableIndexes, func(i, j int) bool {
		return x.TableIndexes[i].Name < x.TableIndexes[j].Name
	})

	for i := range x.TableIndexes {
		sort.Slice(x.TableIndexes[i].Columns, func(a, b int) bool {
			return x.TableIndexes[i].Columns[a].Name <
				x.TableIndexes[i].Columns[b].Name
		})
	}
}

// BuildColumnMap creates a map of column names to Column pointers
func (x *Table) BuildColumnMap() {
	colMap := make(map[string]*Column, len(x.TableColumns))
	for i := range x.TableColumns {
		colMap[x.TableColumns[i].Name] = &x.TableColumns[i]
	}
	x.TableColumnMap = colMap
}

// BuildIndexMap creates a map of index names to Index pointers
func (x *Table) BuildIndexMap() {
	idxMap := make(map[string]*Index, len(x.TableIndexes))
	for i := range x.TableIndexes {
		idxMap[x.TableIndexes[i].Name] = &x.TableIndexes[i]
	}
	x.TableIndexMap = idxMap
}

// HashColumns calculates CRC64 checksum for all columns
func (x *Table) HashColumns(crc64Table *crc64.Table) {
	var tableData strings.Builder
	for i := range x.TableColumns {
		col := &x.TableColumns[i]
		if tableData.Len() > 0 {
			tableData.WriteString("||")
		}

		var columnData strings.Builder
		columnData.WriteString(col.Name)
		columnData.WriteString("|" + col.Type)
		if col.Nullable {
			columnData.WriteString("|NULLABLE")
		} else {
			columnData.WriteString("|")
		}
		if col.Default != nil {
			columnData.WriteString("|DEFAULT " + *col.Default)
		} else {
			columnData.WriteString("|")
		}
		if col.Extra != "" {
			columnData.WriteString("|" + col.Extra)
		}
		if col.Charset != nil {
			columnData.WriteString("|CHARSET|" + *col.Charset)
		}
		if col.Collation != nil {
			columnData.WriteString("|COLLATION|" + *col.Collation)
		}
		col.Crc64 = crc64.Checksum([]byte(columnData.String()), crc64Table)
		tableData.WriteString(fmt.Sprintf("%d", col.Crc64))
	}

	x.TableColumnsCrc64 = crc64.Checksum([]byte(tableData.String()), crc64Table)
	x.BuildColumnMap()
}

// HashIndexes calculates CRC64 checksum for all indexes
func (x *Table) HashIndexes(crc64Table *crc64.Table) {
	var tableData strings.Builder

	for i := range x.TableIndexes {
		idx := &x.TableIndexes[i]
		if tableData.Len() > 0 {
			tableData.WriteString("||")
		}

		var indexData strings.Builder
		indexData.WriteString(idx.Name)
		if idx.Unique {
			indexData.WriteString("|UNIQUE|")
		} else {
			indexData.WriteString("|NONUNIQUE|")
		}
		indexData.WriteString(idx.Type)
		for _, col := range idx.Columns {
			indexData.WriteString("|" + col.Name)
			if col.Prefix != nil {
				indexData.WriteString("(" + strconv.Itoa(int(*col.Prefix)) + ")")
			}
		}
		indexData.WriteString("||")

		idx.Crc64 = crc64.Checksum([]byte(indexData.String()), crc64Table)
		tableData.WriteString(fmt.Sprintf("%d", idx.Crc64))
	}

	x.TableIndexesCrc64 = crc64.Checksum([]byte(tableData.String()), crc64Table)
	x.BuildIndexMap()
}

// HashTableCrc calculates CRC64 checksum for table metadata and structure.
func (x *Table) HashTableCrc(crc64Table *crc64.Table) {
	var tableData strings.Builder
	tableData.WriteString(x.TableSchema)
	tableData.WriteString("|")
	tableData.WriteString(x.TableName)
	tableData.WriteString("|")
	tableData.WriteString(x.Engine)
	tableData.WriteString("|")
	tableData.WriteString(x.RowFormat)
	tableData.WriteString("|")
	tableData.WriteString(x.TableCollation)
	tableData.WriteString("|")
	tableData.WriteString(x.CreateOptions)
	tableData.WriteString("|")
	tableData.WriteString(strconv.FormatUint(x.TableColumnsCrc64, 10))
	tableData.WriteString("|")
	tableData.WriteString(strconv.FormatUint(x.TableIndexesCrc64, 10))
	x.TableCrc = crc64.Checksum([]byte(tableData.String()), crc64Table)
}

// ColumnDiffs returns differences between this table's columns and another table
func (x *Table) ColumnDiffs(other *Table, replicaID string) []string {
	var diffs []string
	for colName, col := range x.TableColumnMap {
		otherCol, ok := other.TableColumnMap[colName]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("%s: missing on replica %s", colName, replicaID))
			continue
		}

		if col.Crc64 != otherCol.Crc64 {
			diffs = append(diffs, fmt.Sprintf("%s: different", colName))
		}
	}

	for _, colName := range other.TableColumns {
		if _, ok := x.TableColumnMap[colName.Name]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: missing on master", colName.Name))
		}
	}

	return diffs
}

// IndexDiffs returns differences between this table's indexes and another table
func (x *Table) IndexDiffs(other *Table, replicaID string) []string {
	var diffs []string
	for idxName, idx := range x.TableIndexMap {
		otherIdx, ok := other.TableIndexMap[idxName]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("index %s: missing on replica %s", idxName, replicaID))
			continue
		}
		if idx.Crc64 != otherIdx.Crc64 {
			diffs = append(diffs, fmt.Sprintf("index %s: different with replica %s", idxName, replicaID))
		}
	}
	for _, idxName := range other.TableIndexes {
		if _, ok := x.TableIndexMap[idxName.Name]; !ok {
			diffs = append(diffs, fmt.Sprintf("index %s: missing on master", idxName.Name))
		}
	}
	return diffs
}

// GetTableSchema returns the table schema name
func (x *Table) GetTableSchema() string {
	if x != nil {
		return x.TableSchema
	}
	return ""
}

// GetTableName returns the table name
func (x *Table) GetTableName() string {
	if x != nil {
		return x.TableName
	}
	return ""
}

// GetEngine returns the storage engine
func (x *Table) GetEngine() string {
	if x != nil {
		return x.Engine
	}
	return ""
}

// GetTableRows returns the estimated row count
func (x *Table) GetTableRows() int64 {
	if x != nil {
		return x.TableRows
	}
	return 0
}

// GetDataLength returns the data length in bytes
func (x *Table) GetDataLength() int64 {
	if x != nil {
		return x.DataLength
	}
	return 0
}

// GetIndexLength returns the index length in bytes
func (x *Table) GetIndexLength() int64 {
	if x != nil {
		return x.IndexLength
	}
	return 0
}

// GetTableCrc returns the table CRC
func (x *Table) GetTableCrc() uint64 {
	if x != nil {
		return x.TableCrc
	}
	return 0
}

// GetTableClusters returns the cluster list
func (x *Table) GetTableClusters() string {
	if x != nil {
		return x.TableClusters
	}
	return ""
}

// GetTableSync returns the sync status
func (x *Table) GetTableSync() string {
	if x != nil {
		return x.TableSync
	}
	return ""
}

// TableSizeSorter sorts tables by size (data + index length)
type TableSizeSorter []*Table

func (a TableSizeSorter) Len() int      { return len(a) }
func (a TableSizeSorter) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a TableSizeSorter) Less(i, j int) bool {
	return a[i].DataLength+a[i].IndexLength > a[j].DataLength+a[j].IndexLength
}

// Disk represents disk usage information
type Disk struct {
	Disk      string
	Path      string
	Total     int32
	Used      int32
	Available int32
}

// Grant represents a database user grant
type Grant struct {
	User          string `json:"user"`
	Host          string `json:"host"`
	Password      string `json:"-"`
	Hash          uint64 `json:"hash"`
	Plugin        string `json:"plugin"`        // authentication plugin name (mysql_native_password, ed25519, caching_sha2_password, …)
	AccountLocked bool   `json:"accountLocked"` // true when account_locked = 'Y' (MariaDB 10.4+ / MySQL 5.7.6+)
}

// Event represents a scheduled event
type Event struct {
	Db      string `json:"db"`
	Name    string `json:"name"`
	Definer string `json:"definer"`
	Status  int64  `json:"status"`
}

// Processlist represents a process/connection in the database
type Processlist struct {
	Id                 uint64          `json:"id" db:"Id"`
	User               string          `json:"user" db:"User"`
	Host               string          `json:"host" db:"Host"`
	Db                 sql.NullString  `json:"db" db:"db"`
	Command            string          `json:"command" db:"Command"`
	Time               sql.NullFloat64 `json:"time" db:"Time"`
	TimeMs             sql.NullFloat64 `json:"timeMs" db:"Time_ms"`
	State              sql.NullString  `json:"state" db:"State"`
	Info               sql.NullString  `json:"info" db:"Info"`
	Progress           sql.NullFloat64 `json:"progress" db:"Progress"`
	RowsSent           uint64          `json:"rowsSent" db:"Rows_sent"`
	RowsExamined       uint64          `json:"rowsExamined" db:"Rows_examined"`
	Url                string          `json:"url" db:"Url"`
	TrxIsolation       sql.NullString  `json:"trxIsolationLevel" db:"trx_isolation_level"`
	TrxTime            uint64          `json:"trxTime" db:"trx_time"`
	TrxTablesInUse     uint64          `json:"txrTablesInUse" db:"trx_tables_in_use"`
	TrxTablesLocked    uint64          `json:"trxTablesLocked" db:"trx_tables_locked"`
	TrxLockStructs     uint64          `json:"trxLockStructs" db:"trx_lock_structs"`
	TrxLockMemoryBytes uint64          `json:"trxLockMemoryBytes" db:"trx_lock_memory_bytes"`
	TrxRowsLocked      uint64          `json:"trxRowsLocked" db:"trx_rows_locked"`
	TrxRowsModified    uint64          `json:"trxRowsModified" db:"trx_rows_modified"`
	TrxIsReadOnly      int             `json:"trxIsReadOnly" db:"trx_is_read_only"`
}

// LogSlow represents a slow query log entry
type LogSlow struct {
	Start_time     int64          `db:"start_time"`
	User_host      sql.NullString `db:"user_host"`
	Query_time     string         `db:"query_time"`
	Lock_time      string         `db:"lock_time"`
	Rows_sent      int            `db:"rows_sent"`
	Rows_examined  int            `db:"rows_examined"`
	Db             sql.NullString `db:"db"`
	Last_insert_id int            `db:"last_insert_id"`
	Insert_id      int            `db:"insert_id"`
	Server_id      int            `db:"server_id"`
	Sql_text       sql.NullString `db:"sql_text"`
	Thread_id      int64          `db:"thread_id"`
	Rows_affected  int            `db:"rows_affected"`
	Digest         string
}

// SlaveHosts represents information about slave/replica hosts
type SlaveHosts struct {
	Server_id    uint64 `json:"serverId"`
	Host         string `json:"host"`
	Port         uint   `json:"port"`
	Master_id    uint64 `json:"masterId"`
	Source_id    uint64 `json:"sourceId"`
	Replica_UUID string `json:"replicaUUID"`
}

// MasterStatus represents master/source replication status
type MasterStatus struct {
	File              string `json:"file"`
	Position          uint   `json:"position"`
	Binlog_Do_DB      string `json:"binlogDoDB"`
	Binlog_Ignore_DB  string `json:"binlogIgnoreDB"`
	Executed_Gtid_Set string `json:"executedGtidSet"`
}

// SlaveStatus represents slave/replica replication status (MariaDB and older MySQL)
type SlaveStatus struct {
	ConnectionName           sql.NullString `db:"Connection_name" json:"connectionName"`
	ChannelName              sql.NullString `db:"Channel_Name" json:"channelName"`
	MasterHost               sql.NullString `db:"Master_Host" json:"masterHost"`
	MasterUser               sql.NullString `db:"Master_User" json:"masterUser"`
	MasterPort               sql.NullString `db:"Master_Port" json:"masterPort"`
	MasterLogFile            sql.NullString `db:"Master_Log_File" json:"masterLogFile"`
	ReadMasterLogPos         sql.NullString `db:"Read_Master_Log_Pos" json:"readMasterLogPos"`
	RelayMasterLogFile       sql.NullString `db:"Relay_Master_Log_File" json:"relayMasterLogFile"`
	SlaveIORunning           sql.NullString `db:"Slave_IO_Running" json:"slaveIoRunning"`
	SlaveSQLRunning          sql.NullString `db:"Slave_SQL_Running" json:"slaveSqlRunning"`
	ExecMasterLogPos         sql.NullString `db:"Exec_Master_Log_Pos" json:"execMasterLogPos"`
	SecondsBehindMaster      sql.NullInt64  `db:"Seconds_Behind_Master" json:"secondsBehindMaster"`
	MasterLastEventTime      sql.NullString `db:"Master_last_event_time" json:"masterLastEventTime"`
	SlaveLastEventTime       sql.NullString `db:"Slave_last_event_time" json:"slaveLastEventTime"`
	MasterSlaveTimeDiff      sql.NullInt64  `db:"Master_Slave_time_diff" json:"masterSlaveTimeDiff"`
	LastIOErrno              sql.NullString `db:"Last_IO_Errno" json:"lastIoErrno"`
	LastIOError              sql.NullString `db:"Last_IO_Error" json:"lastIoError"`
	LastSQLErrno             sql.NullString `db:"Last_SQL_Errno" json:"lastSqlErrno"`
	LastSQLError             sql.NullString `db:"Last_SQL_Error" json:"lastSqlError"`
	MasterServerID           uint64         `db:"Master_Server_Id" json:"masterServerId"`
	UsingGtid                sql.NullString `db:"Using_Gtid" json:"usingGtid"`
	GtidIOPos                sql.NullString `db:"Gtid_IO_Pos" json:"gtidIoPos"`
	GtidSlavePos             sql.NullString `db:"Gtid_Slave_Pos" json:"gtidSlavePos"`
	SlaveHeartbeatPeriod     float64        `db:"Slave_Heartbeat_Period" json:"slaveHeartbeatPeriod"`
	ExecutedGtidSet          sql.NullString `db:"Executed_Gtid_Set" json:"executedGtidSet"`
	RetrievedGtidSet         sql.NullString `db:"Retrieved_Gtid_Set" json:"retrievedGtidSet"`
	SlaveSQLRunningState     sql.NullString `db:"Slave_SQL_Running_State" json:"slaveSQLRunningState"`
	PGExternalID             sql.NullString `db:"external_id" json:"postgresExternalId"`
	DoDomainIds              sql.NullString `db:"Replicate_Do_Domain_Ids" json:"replicateDoDomainIds"`
	IgnoreDomainIds          sql.NullString `db:"Replicate_Ignore_Domain_Ids" json:"replicateIgnoreDomainIds"`
	IgnoreServerIds          sql.NullString `db:"Replicate_Ignore_Server_Ids" json:"replicateIgnoreServerIds"`
	ReplicateDoDB            sql.NullString `db:"Replicate_Do_DB" json:"replicateDoDb"`
	ReplicateIgnoreDB        sql.NullString `db:"Replicate_Ignore_DB" json:"replicateIgnoreDb"`
	ReplicateDoTable         sql.NullString `db:"Replicate_Do_Table" json:"replicateDoTable"`
	ReplicateIgnoreTable     sql.NullString `db:"Replicate_Ignore_Table" json:"replicateIgnoreTable"`
	ReplicateWildDoTable     sql.NullString `db:"Replicate_Do_Wild_Table" json:"replicateWildDoTable"`
	ReplicateWildIgnoreTable sql.NullString `db:"Replicate_Wild_Ignore_Table" json:"replicateWildIgnoreTable"`
	SQLDelay                 sql.NullInt64  `db:"SQL_Delay" json:"sqlDelay"`
	SQLRemainingDelay        sql.NullInt64  `db:"SQL_Remaining_Delay" json:"sqlRemainingDelay"`
	AutoPosition             int            `db:"Auto_Position" json:"autoPosition"`
	MasterRetryCount         sql.NullInt64  `db:"Master_Retry_Count" json:"masterRetryCount"`
}

// ImportFromReplicaStatus converts ReplicaStatus to SlaveStatus format
func (s *SlaveStatus) ImportFromReplicaStatus(rs *ReplicaStatus) {
	s.ConnectionName = sql.NullString{String: rs.ChannelName.String, Valid: rs.ChannelName.Valid}
	s.ChannelName = sql.NullString{String: rs.ChannelName.String, Valid: rs.ChannelName.Valid}
	s.MasterHost = rs.SourceHost
	s.MasterUser = rs.SourceUser
	s.MasterPort = rs.SourcePort
	s.MasterLogFile = rs.SourceLogFile
	s.ReadMasterLogPos = rs.ReadSourceLogPos
	s.RelayMasterLogFile = rs.RelaySourceLogFile
	s.SlaveIORunning = rs.ReplicaIORunning
	s.SlaveSQLRunning = rs.ReplicaSQLRunning
	s.ExecMasterLogPos = rs.ExecSourceLogPos
	s.SecondsBehindMaster = sql.NullInt64{Int64: rs.SecondsBehindSource.Int64, Valid: rs.SecondsBehindSource.Valid}
	s.LastIOErrno = rs.LastIOErrno
	s.LastIOError = rs.LastIOError
	s.LastSQLErrno = rs.LastSQLErrno
	s.LastSQLError = rs.LastSQLError
	s.MasterServerID = rs.SourceServerID
	s.UsingGtid = rs.ExecutedGtidSet
	s.GtidIOPos = rs.RetrievedGtidSet
	s.GtidSlavePos = rs.RetrievedGtidSet
	s.ExecutedGtidSet = rs.ExecutedGtidSet
	s.RetrievedGtidSet = rs.RetrievedGtidSet
	s.SlaveSQLRunningState = rs.ReplicaSQLRunningState
	s.PGExternalID = rs.SourceUUID
	s.DoDomainIds = rs.ReplicateDoDB
	s.IgnoreDomainIds = rs.ReplicateIgnoreDB
	s.IgnoreServerIds = rs.ReplicateIgnoreServerIds
	s.ReplicateDoDB = rs.ReplicateDoDB
	s.ReplicateIgnoreDB = rs.ReplicateIgnoreDB
	s.ReplicateDoTable = rs.ReplicateDoTable
	s.ReplicateIgnoreTable = rs.ReplicateIgnoreTable
	s.ReplicateWildDoTable = rs.ReplicateWildDoTable
	s.ReplicateWildIgnoreTable = rs.ReplicateWildIgnoreTable
	s.SQLDelay = rs.SQLDelay
	s.SQLRemainingDelay = rs.SQLRemainingDelay
	s.AutoPosition = rs.AutoPosition
	s.MasterRetryCount = rs.SourceRetryCount
}

// ReplicaStatus represents replica replication status (MySQL 8.4+)
type ReplicaStatus struct {
	ReplicaIOState                 sql.NullString `db:"Replica_IO_State" json:"replicaIoState"`
	SourceHost                     sql.NullString `db:"Source_Host" json:"sourceHost"`
	SourceUser                     sql.NullString `db:"Source_User" json:"sourceUser"`
	SourcePort                     sql.NullString `db:"Source_Port" json:"sourcePort"`
	ConnectRetry                   sql.NullInt64  `db:"Connect_Retry" json:"connectRetry"`
	SourceLogFile                  sql.NullString `db:"Source_Log_File" json:"sourceLogFile"`
	ReadSourceLogPos               sql.NullString `db:"Read_Source_Log_Pos" json:"readSourceLogPos"`
	RelayLogFile                   sql.NullString `db:"Relay_Log_File" json:"relayLogFile"`
	RelayLogPos                    sql.NullString `db:"Relay_Log_Pos" json:"relayLogPos"`
	RelaySourceLogFile             sql.NullString `db:"Relay_Source_Log_File" json:"relaySourceLogFile"`
	ReplicaIORunning               sql.NullString `db:"Replica_IO_Running" json:"replicaIoRunning"`
	ReplicaSQLRunning              sql.NullString `db:"Replica_SQL_Running" json:"replicaSqlRunning"`
	ReplicateDoDB                  sql.NullString `db:"Replicate_Do_DB" json:"replicateDoDb"`
	ReplicateIgnoreDB              sql.NullString `db:"Replicate_Ignore_DB" json:"replicateIgnoreDb"`
	ReplicateDoTable               sql.NullString `db:"Replicate_Do_Table" json:"replicateDoTable"`
	ReplicateIgnoreTable           sql.NullString `db:"Replicate_Ignore_Table" json:"replicateIgnoreTable"`
	ReplicateWildDoTable           sql.NullString `db:"Replicate_Wild_Do_Table" json:"replicateWildDoTable"`
	ReplicateWildIgnoreTable       sql.NullString `db:"Replicate_Wild_Ignore_Table" json:"replicateWildIgnoreTable"`
	LastErrno                      sql.NullString `db:"Last_Errno" json:"lastErrno"`
	LastError                      sql.NullString `db:"Last_Error" json:"lastError"`
	SkipCounter                    sql.NullInt64  `db:"Skip_Counter" json:"skipCounter"`
	ExecSourceLogPos               sql.NullString `db:"Exec_Source_Log_Pos" json:"execSourceLogPos"`
	// RelayLogSpace uses sql.Null[uint64] (not sql.NullInt64) because Percona/MySQL 8.4 can
	// return values exceeding int64 max, causing a scan overflow. JSON encoding changes from
	// {"Int64":…,"Valid":…} (sql.NullInt64) to {"V":…,"Valid":…} (sql.Null[uint64]).
	RelayLogSpace sql.Null[uint64] `db:"Relay_Log_Space" json:"relayLogSpace"`
	UntilCondition                 sql.NullString `db:"Until_Condition" json:"untilCondition"`
	UntilLogFile                   sql.NullString `db:"Until_Log_File" json:"untilLogFile"`
	UntilLogPos                    sql.NullString `db:"Until_Log_Pos" json:"untilLogPos"`
	SourceSSLAllowed               sql.NullString `db:"Source_SSL_Allowed" json:"sourceSslAllowed"`
	SourceSSLCaFile                sql.NullString `db:"Source_SSL_CA_File" json:"sourceSslCaFile"`
	SourceSSLCaPath                sql.NullString `db:"Source_SSL_CA_Path" json:"sourceSslCaPath"`
	SourceSSLCert                  sql.NullString `db:"Source_SSL_Cert" json:"sourceSslCert"`
	SourceSSLCipher                sql.NullString `db:"Source_SSL_Cipher" json:"sourceSslCipher"`
	SourceSSLKey                   sql.NullString `db:"Source_SSL_Key" json:"sourceSslKey"`
	SecondsBehindSource            sql.NullInt64  `db:"Seconds_Behind_Source" json:"secondsBehindSource"`
	SourceSSLVerifyServerCert      sql.NullString `db:"Source_SSL_Verify_Server_Cert" json:"sourceSslVerifyServerCert"`
	LastIOErrno                    sql.NullString `db:"Last_IO_Errno" json:"lastIoErrno"`
	LastIOError                    sql.NullString `db:"Last_IO_Error" json:"lastIoError"`
	LastSQLErrno                   sql.NullString `db:"Last_SQL_Errno" json:"lastSqlErrno"`
	LastSQLError                   sql.NullString `db:"Last_SQL_Error" json:"lastSqlError"`
	ReplicateIgnoreServerIds       sql.NullString `db:"Replicate_Ignore_Server_Ids" json:"replicateIgnoreServerIds"`
	SourceServerID                 uint64         `db:"Source_Server_Id" json:"sourceServerId"`
	SourceUUID                     sql.NullString `db:"Source_UUID" json:"sourceUuid"`
	SourceInfoFile                 sql.NullString `db:"Source_Info_File" json:"sourceInfoFile"`
	SQLDelay                       sql.NullInt64  `db:"SQL_Delay" json:"sqlDelay"`
	SQLRemainingDelay              sql.NullInt64  `db:"SQL_Remaining_Delay" json:"sqlRemainingDelay"`
	ReplicaSQLRunningState         sql.NullString `db:"Replica_SQL_Running_State" json:"replicaSqlRunningState"`
	SourceRetryCount               sql.NullInt64  `db:"Source_Retry_Count" json:"sourceRetryCount"`
	SourceBind                     sql.NullString `db:"Source_Bind" json:"sourceBind"`
	LastIOErrorTimestamp           sql.NullString `db:"Last_IO_Error_Timestamp" json:"lastIoErrorTimestamp"`
	LastSQLExceptionErrorTimestamp sql.NullString `db:"Last_SQL_Error_Timestamp" json:"lastSqlErrorTimestamp"`
	SourceSSLCrl                   sql.NullString `db:"Source_SSL_Crl" json:"sourceSslCrl"`
	SourceSSLCrlpath               sql.NullString `db:"Source_SSL_Crlpath" json:"sourceSslCrlpath"`
	RetrievedGtidSet               sql.NullString `db:"Retrieved_Gtid_Set" json:"retrievedGtidSet"`
	ExecutedGtidSet                sql.NullString `db:"Executed_Gtid_Set" json:"executedGtidSet"`
	AutoPosition                   int            `db:"Auto_Position" json:"autoPosition"`
	ReplicateRewriteDB             sql.NullString `db:"Replicate_Rewrite_DB" json:"replicateRewriteDb"`
	ChannelName                    sql.NullString `db:"Channel_Name" json:"channelName"`
	SourceTLSVersion               sql.NullString `db:"Source_TLS_Version" json:"sourceTlsVersion"`
	SourcePublicKeyPath            sql.NullString `db:"Source_public_key_path" json:"sourcePublicKeyPath"`
	GetSourcePublicKey             sql.NullString `db:"Get_Source_public_key" json:"getSourcePublicKey"`
	NetworkNamespace               sql.NullString `db:"Network_Namespace" json:"networkNamespace"`
}

// Privileges represents user privileges
type Privileges struct {
	Select_priv      string `json:"selectPriv"`
	Process_priv     string `json:"processPriv"`
	Super_priv       string `json:"superPriv"`
	Repl_slave_priv  string `json:"replSlavePriv"`
	Repl_client_priv string `json:"replClientPriv"`
	Reload_priv      string `json:"reloadPriv"`
}

// SpiderTableNoSync represents Spider tables that need synchronization
type SpiderTableNoSync struct {
	Tbl_src      string
	Tbl_src_link string
	Tbl_dest     string
	Srv_dsync    string
	Srv_sync     string
}

// BinlogEvents represents binlog event information
type BinlogEvents struct {
	Log_name    string `db:"Log_name" json:"logName"`
	Pos         uint   `db:"Pos" json:"pos"`
	Event_type  string `db:"Event_type" json:"eventType"`
	Server_id   uint   `db:"Server_id" json:"serverId"`
	End_log_pos uint   `db:"End_log_pos" json:"endLogPos"`
	Info        string `db:"Info" json:"info"`
}

// MySQLServer represents mysql.servers table entry
type MySQLServer struct {
	Server_name string `db:"Server_name" json:"serverName"`
	Host        string `db:"Host" json:"host"`
	Db          string `db:"Db" json:"db"`
	Username    string `db:"Username" json:"username"`
	Password    string `db:"Password" json:"password"`
	Port        uint   `db:"Port" json:"port"`
	Socket      string `db:"Socket" json:"socket"`
	Wrapper     string `db:"Wrapper" json:"wrapper"`
	Owner       string `db:"Owner" json:"owner"`
}

// Variable represents a database variable
type Variable struct {
	Variable_name string `json:"variableName"`
	Value         string `json:"value"`
}

// Binarylogs represents binary log file information
type Binarylogs struct {
	Log_name  string `json:"logName"`
	File_size uint   `json:"fileSize"`
	Encrypted string `json:"encrypted"` //mysql 8.0
}

// Explain represents EXPLAIN query output
type Explain struct {
	Id            uint           `db:"id" json:"id"`
	Select_type   sql.NullString `db:"select_type" json:"selectType"`
	Table         sql.NullString `db:"table" json:"table"`
	Type          sql.NullString `db:"type" json:"type"`
	Possible_keys sql.NullString `db:"possible_keys" json:"possibleKeys"`
	Key           sql.NullString `db:"key" json:"key"`
	Key_len       sql.NullString `db:"key_len" json:"keyLen"`
	Ref           sql.NullString `db:"ref" json:"ref"`
	Rows          sql.NullString `db:"rows" json:"rows"`
	Extra         sql.NullString `db:"Extra" json:"extra"`
}

// VariableSorter sorts variables by name
type VariableSorter []Variable

func (a VariableSorter) Len() int           { return len(a) }
func (a VariableSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a VariableSorter) Less(i, j int) bool { return a[i].Variable_name < a[j].Variable_name }

// ChangeMasterOpt contains options for CHANGE MASTER/REPLICATION SOURCE command
type ChangeMasterOpt struct {
	Host      string
	Port      string
	User      string
	Password  string
	Retry     string
	Heartbeat string
	SSL       bool
	Logfile   string
	Logpos    string
	Mode      string

	RetryCount string // Start from MariaDB 12 and MySQL 8.4

	Channel         string
	PostgressDB     string
	IsDelayed       bool
	Delay           string
	DoDomainIds     string
	IgnoreDomainIds string
	IgnoreServerIds string
}

// BinaryLogMetadata represents metadata about a binary log file
type BinaryLogMetadata struct {
	Source   string `json:"source"`
	Filename string `json:"filename"`
	Start    int64  `json:"start"`
	Size     uint   `json:"size"`
}

// BinaryLogMetaMap is a thread-safe map for binary log metadata
type BinaryLogMetaMap struct {
	*sync.Map
}

// NewBinaryLogMetaMap creates a new BinaryLogMetaMap
func NewBinaryLogMetaMap() *BinaryLogMetaMap {
	s := new(sync.Map)
	m := &BinaryLogMetaMap{Map: s}
	return m
}

// LoadOrStore loads or stores a binary log metadata entry
func (m *BinaryLogMetaMap) LoadOrStore(key string, value *BinaryLogMetadata) (*BinaryLogMetadata, bool) {
	v, ok := m.Map.LoadOrStore(key, value)
	return v.(*BinaryLogMetadata), ok
}

// Get retrieves a binary log metadata entry
func (m *BinaryLogMetaMap) Get(key string) *BinaryLogMetadata {
	if v, ok := m.Load(key); ok {
		return v.(*BinaryLogMetadata)
	}
	return nil
}

// CheckAndGet checks if a key exists and returns the value
func (m *BinaryLogMetaMap) CheckAndGet(key string) (*BinaryLogMetadata, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*BinaryLogMetadata), true
	}
	return nil, false
}

// Set stores a binary log metadata entry
func (m *BinaryLogMetaMap) Set(key string, value *BinaryLogMetadata) {
	m.Store(key, value)
}

// ToNormalMap converts to a regular map
func (m *BinaryLogMetaMap) ToNormalMap(c map[string]*BinaryLogMetadata) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the BinaryLogMetaMap to the output map
	m.Callback(func(key string, value *BinaryLogMetadata) bool {
		c[key] = value
		return true
	})
}

// ToNewMap creates a new map with all entries
func (m *BinaryLogMetaMap) ToNewMap() map[string]BinaryLogMetadata {
	result := make(map[string]BinaryLogMetadata)
	m.Range(func(k, v any) bool {
		result[k.(string)] = *v.(*BinaryLogMetadata)
		return true
	})
	return result
}

// GetKeys returns all keys sorted
func (m *BinaryLogMetaMap) GetKeys() []string {
	result := make([]string, 0)
	m.Range(func(k, v any) bool {
		result = append(result, k.(string))
		return true
	})

	slices.Sort(result)
	return result
}

// GetKeysDesc returns all keys sorted in descending order
func (m *BinaryLogMetaMap) GetKeysDesc() []string {
	result := make([]string, 0)
	m.Range(func(k, v any) bool {
		result = append(result, k.(string))
		return true
	})

	slices.SortFunc(result, compareDesc)
	return result
}

// Callback iterates over all entries with a callback function
func (m *BinaryLogMetaMap) Callback(f func(key string, value *BinaryLogMetadata) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(string), v.(*BinaryLogMetadata))
	})
}

// Clear removes all entries
func (m *BinaryLogMetaMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(string))
		return true
	})
}

// MarshalIndent marshals the map to JSON with indentation
func (m *BinaryLogMetaMap) MarshalIndent(prefix, indent string) ([]byte, error) {
	result := make(map[string]BinaryLogMetadata, 0)
	fnames := make([]string, 0)
	m.Range(func(k, v any) bool {
		fnames = append(fnames, k.(string))
		return true
	})

	slices.Sort(fnames)

	for _, key := range fnames {
		result[key] = *m.Get(key)
	}

	return json.MarshalIndent(result, prefix, indent)
}

// ClearObsoleteMetadata removes entries older than the specified binlog file
func (m *BinaryLogMetaMap) ClearObsoleteMetadata(oldest string) (deleted []string) {
	deleted = make([]string, 0)
	keys := make([]string, 0)
	m.Range(func(k, v any) bool {
		keys = append(keys, k.(string))
		return true
	})

	slices.Sort(keys)

	index := sort.Search(len(keys), func(i int) bool { return keys[i] >= oldest })

	// If the target string is found, return the slice starting from that index
	if index < len(keys) && keys[index] == oldest {
		for _, key := range keys[:index] {
			deleted = append(deleted, key)
			m.Delete(key)
		}
	}

	return deleted
}

// FromNormalBinaryLogMetaMap converts a regular map to BinaryLogMetaMap
func FromNormalBinaryLogMetaMap(m *BinaryLogMetaMap, c map[string]*BinaryLogMetadata) *BinaryLogMetaMap {
	if m == nil {
		m = NewBinaryLogMetaMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

// FromBinaryLogMetaMap copies from one BinaryLogMetaMap to another
func FromBinaryLogMetaMap(m *BinaryLogMetaMap, c *BinaryLogMetaMap) *BinaryLogMetaMap {
	if m == nil {
		m = NewBinaryLogMetaMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key string, value *BinaryLogMetadata) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

// compareDesc compares strings in descending order
func compareDesc(a, b string) int {
	return map[bool]int{a > b: -1, a < b: 1}[true]
}
