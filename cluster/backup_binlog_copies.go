// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

// BinlogCopies is what a server's backup directory holds of its binary logs: the copies
// binary-logs.meta.json lists (backup-binlogs), their number and the lowest and highest.
type BinlogCopies struct {
	Server     string `json:"server"`
	Count      int    `json:"count"`
	Min        string `json:"min,omitempty"`        // the lowest binlog copied
	Max        string `json:"max,omitempty"`        // the highest
	MinStart   int64  `json:"minStart,omitempty"`   // the time of the lowest one's first event (unix)
	MaxStart   int64  `json:"maxStart,omitempty"`   // the time of the highest one's first event
	TotalBytes int64  `json:"totalBytes,omitempty"` // their size on the server
}

// binlogNumber is the sequence number of a binlog name (mysql-bin.000042: 42), -1 none.
func binlogNumber(name string) int {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return -1
	}
	n, err := strconv.Atoi(name[i+1:])
	if err != nil {
		return -1
	}
	return n
}

// GetBinlogCopies reads each server's binary-logs.meta.json: a server whose directory holds no
// copy has count 0.
func (cluster *Cluster) GetBinlogCopies() []BinlogCopies {
	out := make([]BinlogCopies, 0, len(cluster.Servers))
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		c := BinlogCopies{Server: server.URL}
		data, err := os.ReadFile(filepath.Join(server.GetMyBackupDirectoryPath(), "binary-logs.meta.json"))
		if err == nil {
			var metas map[string]dbhelper.BinaryLogMetadata
			if json.Unmarshal(data, &metas) == nil {
				names := make([]string, 0, len(metas))
				for name, m := range metas {
					names = append(names, name)
					c.TotalBytes += int64(m.Size)
				}
				sort.Slice(names, func(i, j int) bool { return binlogNumber(names[i]) < binlogNumber(names[j]) })
				if len(names) > 0 {
					c.Count = len(names)
					c.Min, c.Max = names[0], names[len(names)-1]
					c.MinStart, c.MaxStart = metas[c.Min].Start, metas[c.Max].Start
				}
			}
		}
		out = append(out, c)
	}
	return out
}
