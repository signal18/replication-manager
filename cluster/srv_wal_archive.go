// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// PostgreSQL WAL archive: the binlog copy of PostgreSQL. With backup-binlogs the server
// archives its completed WAL segments (archive_mode, postgres_start.sh) into a directory
// shared with its jobs sidecar, which ships each file here (task pgwalarchive, one
// receiver per file). The archive sits with the server's backups and is kept as long as a
// physical backup can use it (PostgresPurgeWalArchive).

// PostgresWalArchiveFileRe is what the archive accepts: a WAL segment, a timeline history
// file or a backup label file (the names PostgreSQL gives archive_command)
var PostgresWalArchiveFileRe = regexp.MustCompile(`^[0-9A-F]{24}(\.[0-9A-F]{8}\.backup)?$|^[0-9A-F]{8}\.history$`)

// PostgresWalArchiveDir is where the shipped files land, with a trailing slash
func (server *ServerMonitor) PostgresWalArchiveDir() string {
	return server.GetMyBackupDirectory() + "wal/"
}

// PostgresPurgeWalArchive keeps the segments a stored physical backup can replay: from one
// hour before the oldest physical backup of this server (the segment holding its start is
// always older than the backup file) to now; without a physical backup, the last
// backup-binlogs-keep segments, as the binlog copy does.
func (server *ServerMonitor) PostgresPurgeWalArchive() {
	cluster := server.ClusterGroup
	entries, err := os.ReadDir(server.PostgresWalArchiveDir())
	if err != nil {
		return
	}
	var oldest time.Time
	if backups, err := filepath.Glob(server.GetMyBackupDirectory() + "pgbasebackup*"); err == nil {
		for _, b := range backups {
			if strings.HasSuffix(b, ".meta.json") {
				continue
			}
			if st, err := os.Stat(b); err == nil && (oldest.IsZero() || st.ModTime().Before(oldest)) {
				oldest = st.ModTime()
			}
		}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && PostgresWalArchiveFileRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // WAL names sort in LSN order
	keep := cluster.Conf.BackupBinlogsKeep
	if keep <= 0 {
		keep = 10
	}
	removed := 0
	for i, n := range names {
		if strings.HasSuffix(n, ".history") {
			continue // tiny, and a timeline switch is always worth keeping
		}
		path := server.PostgresWalArchiveDir() + n
		drop := false
		if oldest.IsZero() {
			drop = i < len(names)-keep
		} else if st, err := os.Stat(path); err == nil {
			drop = st.ModTime().Before(oldest.Add(-time.Hour))
		}
		if drop && os.Remove(path) == nil {
			removed++
		}
	}
	if removed > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "WAL archive of %s: %d segments purged, %d kept", server.URL, removed, len(names)-removed)
	}
}

// postgresRefreshWalArchiveKey rewrites the PG_WAL_ARCHIVE config key of this server's
// service after backup-binlogs changed; the start script reads it, the rolling restart
// applies it.
func (server *ServerMonitor) postgresRefreshWalArchiveKey() {
	cluster := server.ClusterGroup
	app := cluster.engineAppOfServer(server)
	if app == nil || postgresWalArchiveMount(app) == "" || cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return
	}
	v := "off"
	if cluster.Conf.BackupBinlogs {
		v = "on"
	}
	svc := cluster.OpenSVCConnect()
	if err := svc.CreateConfigKeyValue(cluster.Name, app.Name, "PG_WAL_ARCHIVE", v); err != nil { // create updates an existing key
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not set PG_WAL_ARCHIVE=%s on %s: %s", v, server.URL, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "PG_WAL_ARCHIVE=%s set for %s, applied by its next (rolling) restart", v, server.URL)
}

// postgresCheckWalArchiver opens WARN0232 when the archiver fails: the archive_command of a
// segment keeps failing (pg_stat_archiver) and segments wait in pg_wal; the primary's disk
// fills up while they wait. Each tick, on a server that archives.
func (server *ServerMonitor) postgresCheckWalArchiver() {
	cluster := server.ClusterGroup
	if server.Conn == nil || server.Variables.Get("ARCHIVE_MODE") != "ON" && server.Variables.Get("ARCHIVE_MODE") != "ALWAYS" {
		return
	}
	st, logs, err := dbhelper.PostgresArchiverStatus(server.Conn)
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not read the archiver status of %s: %s", server.URL, err)
	if err != nil {
		return
	}
	if st.Ready > 0 && st.LastFailed.After(st.LastArchived) {
		cluster.SetState("WARN0232", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0232"], server.URL, st.Ready, st.FailedCount, st.LastFailed.Format(time.RFC3339)), ErrFrom: "MON", ServerUrl: server.URL})
	}
}
