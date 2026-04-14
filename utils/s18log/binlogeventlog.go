// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s18log

import "sync"

// BinlogEvent captures a single QUERY_EVENT read from a server's binary log.
// Only statement-level SQL text is stored; row-format payload is not decoded.
type BinlogEvent struct {
	Timestamp string `json:"timestamp"` // "2006-01-02 15:04:05" UTC
	Schema    string `json:"schema"`    // default schema at query time
	Query     string `json:"query"`     // raw SQL statement text
	ServerID  uint32 `json:"server_id"` // originating server-id
}

// BinlogEventLog is a fixed-size ring buffer of BinlogEvent entries.
// Thread-safe via the embedded read-write mutex.
// Writers (Add) take a full write lock; readers (snapshot) take a read lock so
// multiple concurrent plugin snapshots do not block each other or ScanBinlogQueryEvents.
type BinlogEventLog struct {
	Buffer []BinlogEvent  `json:"buffer"`
	Len    int            `json:"len"`
	Line   int            `json:"line"`
	L      sync.RWMutex   `json:"-"`
}

// NewBinlogEventLog allocates a ring buffer that holds at most sz entries.
func NewBinlogEventLog(sz int) BinlogEventLog {
	return BinlogEventLog{
		Len:    sz,
		Buffer: make([]BinlogEvent, sz),
	}
}

// Add prepends e at the front of the ring buffer (newest first).
func (bl *BinlogEventLog) Add(e BinlogEvent) {
	bl.L.Lock()
	defer bl.L.Unlock()
	ns := make([]BinlogEvent, 1)
	ns[0] = e
	bl.Buffer = append(ns, bl.Buffer[0:bl.Len]...)
}
