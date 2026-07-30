// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/state"
)

// newCatalogTestCluster builds a minimal Cluster fixture rooted at a fresh
// temp dir, safe to pass through buildBackupCatalog (StateMachine must be
// non-nil: GetBackupServer()->IsDiscovered() dereferences it).
func newCatalogTestCluster(t *testing.T) *Cluster {
	t.Helper()
	return &Cluster{
		Name:          "testcluster",
		Conf:          &config.Config{WorkingDir: t.TempDir(), BackupPhysicalType: "mariabackup"},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
		StateMachine:  &state.StateMachine{},
	}
}

func newCatalogTestServer(cl *Cluster, host, port string) *ServerMonitor {
	return &ServerMonitor{
		URL:          host + ":" + port,
		Host:         host,
		Port:         port,
		ClusterGroup: cl,
	}
}

func TestBuildBackupCatalog_MetadataOnlyRegression(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical,
		BackupTool:   "mysqldump",
		Dest:         "/backups/db1/mysqldump.sql.gz",
		Completed:    true,
		EndTime:      time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}

	cat := cl.buildBackupCatalog()
	if len(cat) != 1 {
		t.Fatalf("expected exactly 1 entry from LastBackupMeta, got %d: %+v", len(cat), cat)
	}
	if cat[0].Server != sv.URL || cat[0].Kind != "logical" || cat[0].Path != sv.LastBackupMeta.Logical.Dest {
		t.Fatalf("unexpected entry: %+v", cat[0])
	}
}

func TestBuildBackupCatalog_FoldsFullBackupMetaMapHistory(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	last := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, Dest: "/backups/db1/mysqldump.sql.gz", Completed: true, EndTime: time.Unix(2000, 0),
	}
	older := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, Dest: "/backups/db1/mysqldump.1000.sql.gz", Completed: true, EndTime: time.Unix(1000, 0),
	}
	sv.LastBackupMeta.Logical = last
	cl.Servers = serverList{sv}
	cl.BackupMetaMap.Store(last.Id, last)
	cl.BackupMetaMap.Store(older.Id, older)

	cat := cl.buildBackupCatalog()
	if len(cat) != 2 {
		t.Fatalf("expected 2 entries (last + history), got %d: %+v", len(cat), cat)
	}
	paths := map[string]bool{}
	for _, e := range cat {
		paths[e.Path] = true
	}
	if !paths[last.Dest] || !paths[older.Dest] {
		t.Fatalf("expected both dests present, got %+v", cat)
	}
}

// snapshotIndexFixture wires a snapshot ID's resolved SnapshotMetadataSummary
// directly into the metadata cache, mirroring TestBuildSnapshotMetadataIndexFromCache
// in cluster_bck_test.go, so BuildSnapshotMetadataIndex resolves it without a
// real BackupMetaMap Dest-prefix match.
func snapshotIndexFixture(cl *Cluster, snapshotID string, summary *SnapshotMetadataSummary) {
	manager := cl.getSnapshotMetadataManager()
	key := snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault)
	manager.cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
}

// markSnapshotMetadataReady flags a snapshot's fetch/extraction status as
// ready in the (separate from BackupMetaMap-derived summaries) metadata
// cache RequireSnapshotMetadataReady checks -- without this, ResolveResticSnapshot
// treats every snapshot as not-yet-ready and never returns a pick.
func markSnapshotMetadataReady(cl *Cluster, snapshotID string) {
	manager := cl.getSnapshotMetadataManager()
	manager.cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
	})
}

func TestBuildBackupCatalog_ResticSnapshotWithMatchingSummary(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.ResticManager = &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-1", Time: time.Unix(3000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}
	snapshotIndexFixture(cl, "snap-1", &SnapshotMetadataSummary{
		Dest:             "/backups/db1/mysqldump.sql.gz",
		BackupMethod:     "logical",
		BackupTool:       "mysqldump",
		BackupLine:       backupmgr.BackupLineDefault,
		StartTime:        time.Unix(2900, 0),
		EndTime:          time.Unix(3000, 0),
		ResticSnapshotID: "snap-1",
	})

	cat := cl.buildBackupCatalog()
	if len(cat) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d: %+v", len(cat), cat)
	}
	if cat[0].Kind != "logical" || cat[0].Tool != "mysqldump" || cat[0].Location != RepoRemote || cat[0].Path != "snap-1" {
		t.Fatalf("unexpected entry from matched summary: %+v", cat[0])
	}
	if cat[0].Timestamp != 3000 {
		t.Fatalf("expected timestamp from summary EndTime, got %d", cat[0].Timestamp)
	}
}

func TestBuildBackupCatalog_ResticSnapshotDedupedAgainstTrackedMeta(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		ResticEnabled: true, ResticSnapshotID: "snap-1",
		Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-1", Time: time.Unix(1000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}

	cat := cl.buildBackupCatalog()
	count := 0
	for _, e := range cat {
		if e.Path == "snap-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected snap-1 represented exactly once (already tracked via LastBackupMeta), got %d in %+v", count, cat)
	}
}

func TestBuildBackupCatalog_ResticSnapshotOrphaned(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.ResticManager = &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-orphan", Time: time.Unix(500, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/gone"}},
	}}

	cat := cl.buildBackupCatalog()
	if len(cat) != 1 {
		t.Fatalf("expected exactly 1 best-effort entry, got %d: %+v", len(cat), cat)
	}
	e := cat[0]
	if e.Path != "snap-orphan" || e.Location != RepoRemote {
		t.Fatalf("unexpected orphaned entry: %+v", e)
	}
	if e.Kind != "" || e.Tool != "" {
		t.Fatalf("orphaned entry should have no Kind/Tool (no metadata to infer from), got %+v", e)
	}
	if e.Timestamp != 500 {
		t.Fatalf("expected timestamp from snapshot.Time fallback, got %d", e.Timestamp)
	}
}

func TestBuildBackupCatalog_OnDiskDirMergeAndDedup(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{sv}
	cl.master = sv

	dir := sv.GetMyBackupDirectoryPath()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mysqldump.sql.gz"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cat := cl.buildBackupCatalog()
	if len(cat) != 1 {
		t.Fatalf("expected exactly 1 on-disk entry, got %d: %+v", len(cat), cat)
	}
	if cat[0].Kind != "logical" || cat[0].Tool != config.ConstBackupLogicalTypeMysqldump || !cat[0].isLocal() {
		t.Fatalf("unexpected on-disk entry: %+v", cat[0])
	}

	// Now also track the very same file via LastBackupMeta — must not double it.
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Dest: filepath.Join(dir, "mysqldump.sql.gz"), Completed: true, EndTime: time.Unix(1000, 0),
	}
	cat = cl.buildBackupCatalog()
	if len(cat) != 1 {
		t.Fatalf("expected dedup to keep exactly 1 entry once tracked by metadata too, got %d: %+v", len(cat), cat)
	}
}

// TestBuildBackupCatalog_OnDiskScanCoversNonMasterNonBackupServer guards
// against candidateBackupServers narrowing back to master/backup-server/
// has-LastBackupMeta only: enumerateOnDiskBackups exists specifically for
// backups with NO surviving metadata pointer, so a plain replica with an
// orphaned on-disk file and no LastBackupMeta must still be scanned.
func TestBuildBackupCatalog_OnDiskScanCoversNonMasterNonBackupServer(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306") // not master, not backup server, no LastBackupMeta
	cl.Servers = serverList{master, replica}
	cl.master = master

	dir := replica.GetMyBackupDirectoryPath()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mysqldump.sql.gz"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cat := cl.buildBackupCatalog()
	found := false
	for _, e := range cat {
		if e.Server == replica.URL {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the orphaned on-disk backup on the non-master, non-backup-server replica to be included, got %+v", cat)
	}
}

// TestBuildBackupCatalog_ResticSnapshotMultipleSummariesSameKindDifferentTool
// guards the dedup key: SummarizeSnapshotMetadata dedups by method+line, not
// by tool, so one snapshot ID can resolve to two summaries sharing Kind but
// differing in Tool (a real hard-gate dimension). A Kind+Path-only dedup key
// would silently collapse them into one row.
func TestBuildBackupCatalog_ResticSnapshotMultipleSummariesSameKindDifferentTool(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.ResticManager = &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-mixed", Time: time.Unix(1000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}
	manager := cl.getSnapshotMetadataManager()
	manager.cache.Update("snap-mixed", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault): {
				BackupMethod: "logical", BackupTool: "mysqldump", BackupLine: backupmgr.BackupLineDefault,
				StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0), ResticSnapshotID: "snap-mixed",
			},
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineAdhoc): {
				BackupMethod: "logical", BackupTool: "mydumper", BackupLine: backupmgr.BackupLineAdhoc,
				StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0), ResticSnapshotID: "snap-mixed",
			},
		}
	})

	cat := cl.buildBackupCatalog()
	if len(cat) != 2 {
		t.Fatalf("expected both same-Kind different-Tool summaries preserved, got %d: %+v", len(cat), cat)
	}
	tools := map[string]bool{}
	for _, e := range cat {
		tools[e.Tool] = true
	}
	if !tools["mysqldump"] || !tools["mydumper"] {
		t.Fatalf("expected both mysqldump and mydumper entries, got %+v", cat)
	}
}

func TestBuildBackupCatalog_ResticSnapshotOneTrackedLineDoesNotHideOtherSummary(t *testing.T) {
	// Same two-line snapshot as the test above, but this time ONE of the two
	// lines (mysqldump) is ALSO tracked via a server's LastBackupMeta (as if
	// that line's BackupMetadata record survived while the mydumper line's
	// never got recorded, or was pruned). Before the fix, seenResticIDs was
	// keyed on snap.Id alone, so tracking the mysqldump line made
	// enumerateResticSnapshotEntries skip the WHOLE snapshot ID — silently
	// dropping the untracked mydumper line from the catalog entirely.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		ResticEnabled: true, ResticSnapshotID: "snap-mixed",
		Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-mixed", Time: time.Unix(1000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}
	manager := cl.getSnapshotMetadataManager()
	manager.cache.Update("snap-mixed", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault): {
				BackupMethod: "logical", BackupTool: "mysqldump", BackupLine: backupmgr.BackupLineDefault,
				StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0), ResticSnapshotID: "snap-mixed",
			},
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineAdhoc): {
				BackupMethod: "logical", BackupTool: "mydumper", BackupLine: backupmgr.BackupLineAdhoc,
				StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0), ResticSnapshotID: "snap-mixed",
			},
		}
	})

	cat := cl.buildBackupCatalog()
	tools := map[string]int{}
	for _, e := range cat {
		if e.Path == "snap-mixed" {
			tools[e.Tool]++
		}
	}
	if tools["mysqldump"] != 1 {
		t.Fatalf("expected exactly 1 mysqldump entry (from tracked metadata, not duplicated by the summary loop), got %d: %+v", tools["mysqldump"], cat)
	}
	if tools["mydumper"] != 1 {
		t.Fatalf("expected the untracked mydumper summary to still surface, got %d: %+v", tools["mydumper"], cat)
	}
}

func TestOnDiskBackupPatterns(t *testing.T) {
	patterns := onDiskBackupPatterns("mariabackup")
	cases := []struct {
		name        string
		wantKind    string
		wantTool    string
		wantTs      int64
		wantNoMatch bool
	}{
		{name: "mysqldump.sql.gz", wantKind: "logical", wantTool: "mysqldump"},
		{name: "mysqldump.1700000000.sql.gz", wantKind: "logical", wantTool: "mysqldump", wantTs: 1700000000},
		{name: "splitdump", wantKind: "logical", wantTool: "mysqldump"},
		{name: "splitdump.1700000000", wantKind: "logical", wantTool: "mysqldump", wantTs: 1700000000},
		{name: "mydumper", wantKind: "logical", wantTool: "mydumper"},
		{name: "dumpling.1700000000", wantKind: "logical", wantTool: "dumpling", wantTs: 1700000000},
		{name: "mariabackup.xbtream", wantKind: "physical", wantTool: "mariabackup"},
		{name: "mariabackup.1700000000.xbtream.gz", wantKind: "physical", wantTool: "mariabackup", wantTs: 1700000000},
		{name: "random.txt", wantNoMatch: true},
		{name: "xtrabackup.xbtream", wantNoMatch: true}, // configured physical tool is mariabackup, not xtrabackup
	}
	for _, c := range cases {
		matched := false
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(c.name)
			if m == nil {
				continue
			}
			matched = true
			if p.kind != c.wantKind || p.tool != c.wantTool {
				t.Errorf("%s: got kind=%s tool=%s, want kind=%s tool=%s", c.name, p.kind, p.tool, c.wantKind, c.wantTool)
			}
			if c.wantTs != 0 && m[1] != "1700000000" {
				t.Errorf("%s: expected ts capture 1700000000, got %q", c.name, m[1])
			}
			break
		}
		if matched == c.wantNoMatch {
			t.Errorf("%s: matched=%v, want match=%v", c.name, matched, !c.wantNoMatch)
		}
	}
}

func TestResolveServerURLForDest(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv1 := newCatalogTestServer(cl, "db1", "3306")
	sv2 := newCatalogTestServer(cl, "db2", "3306")
	cl.Servers = serverList{sv1, sv2}

	dest := sv2.GetMyBackupDirectoryPath() + "mysqldump.sql.gz"
	if got := cl.resolveServerURLForDest(dest); got != sv2.URL {
		t.Fatalf("expected to resolve to %s, got %q", sv2.URL, got)
	}
	if got := cl.resolveServerURLForDest("/unrelated/path/file"); got != "" {
		t.Fatalf("expected empty resolution for unmatched dest, got %q", got)
	}
	if got := cl.resolveServerURLForDest(""); got != "" {
		t.Fatalf("expected empty resolution for empty dest, got %q", got)
	}
}

func TestResolveLogicalBackupSource_HistoricalPickReturnsItsOwnMetaNotLatest(t *testing.T) {
	// The exact Claim-1 scenario: a server's CURRENT LastBackupMeta.Logical
	// (SplitUser=true) is newer than the master's binlog head, so the
	// default selector's TimeNotAfterHead gate excludes it -- the resolver
	// falls back to an OLDER BackupMetaMap entry for the SAME server
	// (SplitUser=false) that IS within the window. meta must reflect that
	// OLDER entry, not source.LastBackupMeta.Logical -- otherwise a
	// restore-time SplitUser decision would be made from a backup that
	// wasn't even the one selected.
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	master.GTIDBinlogPos = gtid.NewList("0-1-300")
	replica := newCatalogTestServer(cl, "db2", "3306")

	newest := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: replica.URL, Dest: "/backups/db2/mysqldump.sql.gz",
		Completed: true, EndTime: time.Unix(2000, 0), BinLogGtid: "0-1-400", SplitUser: true,
	}
	older := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: replica.URL, Dest: "/backups/db2/mysqldump.1000.sql.gz",
		Completed: true, EndTime: time.Unix(1000, 0), BinLogGtid: "0-1-200", SplitUser: false,
	}
	replica.LastBackupMeta.Logical = newest
	cl.Servers = serverList{master, replica}
	cl.master = master
	cl.BackupMetaMap.Store(newest.Id, newest)
	cl.BackupMetaMap.Store(older.Id, older)

	backupfile, source, meta := cl.resolveLogicalBackupSource(master, replica, "mysqldump")
	if backupfile != older.Dest {
		t.Fatalf("expected the older, within-window backup to be picked, got %q", backupfile)
	}
	if source == nil || source.URL != replica.URL {
		t.Fatalf("expected source to resolve to the replica, got %+v", source)
	}
	if meta != older {
		t.Fatalf("expected meta to be the OLDER entry actually picked, not LastBackupMeta.Logical (%+v); got %+v", newest, meta)
	}
	if meta == replica.LastBackupMeta.Logical {
		t.Fatal("meta must not equal source.LastBackupMeta.Logical here -- that's the excluded, newer backup")
	}
}

func TestResolveLogicalBackupSource_PickFound(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306")
	replica.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Dest: "/backups/db2/mysqldump.sql.gz", Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{master, replica}
	cl.master = master

	backupfile, source, meta := cl.resolveLogicalBackupSource(master, replica, "mysqldump")
	if backupfile != "/backups/db2/mysqldump.sql.gz" {
		t.Fatalf("expected resolved backupfile from replica's metadata, got %q", backupfile)
	}
	if source == nil || source.URL != replica.URL {
		t.Fatalf("expected source to resolve to the replica that actually has the backup, got %+v", source)
	}
	if meta != replica.LastBackupMeta.Logical {
		t.Fatalf("expected meta to be the exact BackupMetadata backing the pick, got %+v", meta)
	}
}

func TestResolveLogicalBackupSource_NoPick(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{master}
	cl.master = master

	backupfile, source, meta := cl.resolveLogicalBackupSource(master, master, "mysqldump")
	if backupfile != "" || source != nil || meta != nil {
		t.Fatalf("expected no pick against an empty catalog, got backupfile=%q source=%+v meta=%+v", backupfile, source, meta)
	}
}

func TestResolveLogicalBackupSource_ToolForced(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	master.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mydumper",
		Dest: "/backups/db1/mydumper", Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{master}
	cl.master = master

	// catalog only has a mydumper backup; requesting mysqldump must not match it.
	if backupfile, source, meta := cl.resolveLogicalBackupSource(master, master, "mysqldump"); backupfile != "" || source != nil || meta != nil {
		t.Fatalf("expected tool mismatch to yield no pick, got backupfile=%q source=%+v meta=%+v", backupfile, source, meta)
	}

	// requesting mydumper (the actual tool) must match.
	backupfile, source, meta := cl.resolveLogicalBackupSource(master, master, "mydumper")
	if backupfile != "/backups/db1/mydumper" || source == nil || source.URL != master.URL {
		t.Fatalf("expected mydumper pick to match, got backupfile=%q source=%+v", backupfile, source)
	}
	if meta != master.LastBackupMeta.Logical {
		t.Fatalf("expected meta to be the exact BackupMetadata backing the pick, got %+v", meta)
	}
}

func TestLegacyLogicalBackupFallback_MasterMetadataMatch(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	dest := master.GetMyBackupDirectoryPath() + "mysqldump.sql.gz"
	master.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupTool: "mysqldump", Dest: dest, Completed: true,
	}
	if err := os.MkdirAll(master.GetMyBackupDirectoryPath(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cl.Servers = serverList{master}
	cl.master = master

	backupfile, source, useMaster := cl.legacyLogicalBackupFallback(master, "mysqldump", "mysqldump.sql.gz", []string{"mysqldump.sql.gz", "splitdump"})
	if backupfile != dest {
		t.Fatalf("expected metadata-matched dest, got %q", backupfile)
	}
	if source == nil || source.URL != master.URL || !useMaster {
		t.Fatalf("expected source=master, useMaster=true, got source=%+v useMaster=%v", source, useMaster)
	}
}

func TestLegacyLogicalBackupFallback_BareDestFallback(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{master}
	cl.master = master

	backupfile, source, useMaster := cl.legacyLogicalBackupFallback(master, "mysqldump", "mysqldump.sql.gz", []string{"mysqldump.sql.gz", "splitdump"})
	want := master.GetMyBackupDirectoryPath() + "mysqldump.sql.gz"
	if backupfile != want {
		t.Fatalf("expected bare-dest fallback %q, got %q", want, backupfile)
	}
	if source == nil || source.URL != master.URL || !useMaster {
		t.Fatalf("expected source=master, useMaster=true, got source=%+v useMaster=%v", source, useMaster)
	}
}

func TestResolvePhysicalBackupSource_PickFound(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306")
	replica.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: "mariabackup",
		Dest: "/backups/db2/mariabackup.xbtream", Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{master, replica}
	cl.master = master

	backupfile, source := cl.resolvePhysicalBackupSource(master, replica, "mariabackup")
	if backupfile != "/backups/db2/mariabackup.xbtream" {
		t.Fatalf("expected resolved backupfile from replica's metadata, got %q", backupfile)
	}
	if source == nil || source.URL != replica.URL {
		t.Fatalf("expected source to resolve to the replica that actually has the backup, got %+v", source)
	}
}

func TestResolvePhysicalBackupSource_NoPick(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{master}
	cl.master = master

	backupfile, source := cl.resolvePhysicalBackupSource(master, master, "mariabackup")
	if backupfile != "" || source != nil {
		t.Fatalf("expected no pick against an empty catalog, got backupfile=%q source=%+v", backupfile, source)
	}
}

func TestResolvePhysicalBackupSource_ToolForced(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	master.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: "xtrabackup",
		Dest: "/backups/db1/xtrabackup.xbtream", Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{master}
	cl.master = master

	// catalog only has an xtrabackup backup; requesting mariabackup must not match it.
	if backupfile, source := cl.resolvePhysicalBackupSource(master, master, "mariabackup"); backupfile != "" || source != nil {
		t.Fatalf("expected tool mismatch to yield no pick, got backupfile=%q source=%+v", backupfile, source)
	}

	// requesting xtrabackup (the actual tool) must match.
	backupfile, source := cl.resolvePhysicalBackupSource(master, master, "xtrabackup")
	if backupfile != "/backups/db1/xtrabackup.xbtream" || source == nil || source.URL != master.URL {
		t.Fatalf("expected xtrabackup pick to match, got backupfile=%q source=%+v", backupfile, source)
	}
}

func TestLegacyPhysicalBackupFallback_BackupServerCookieMatch(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	bck := newCatalogTestServer(cl, "db2", "3306")
	bck.PreferedBackup = true
	bck.Datadir = t.TempDir()
	cl.Servers = serverList{master, bck}
	cl.master = master
	cl.StateMachine.Discovered = true

	if err := bck.SetBackupPhysicalCookie("mariabackup"); err != nil {
		t.Fatalf("SetBackupPhysicalCookie: %v", err)
	}
	dir := bck.GetMyBackupDirectoryPath()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dir+"mariabackup.xbtream", []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	backupfile, source, useMaster := cl.legacyPhysicalBackupFallback(master, "mariabackup")
	if backupfile != dir+"mariabackup.xbtream" {
		t.Fatalf("expected backup-server file, got %q", backupfile)
	}
	if source == nil || source.URL != bck.URL || useMaster {
		t.Fatalf("expected source=backup server, useMaster=false, got source=%+v useMaster=%v", source, useMaster)
	}
}

func TestLegacyPhysicalBackupFallback_MasterDefault(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{master}
	cl.master = master

	backupfile, source, useMaster := cl.legacyPhysicalBackupFallback(master, "mariabackup")
	want := master.GetMyBackupDirectoryPath() + "mariabackup.xbtream"
	if backupfile != want {
		t.Fatalf("expected bare-dest fallback %q, got %q", want, backupfile)
	}
	if source == nil || source.URL != master.URL || !useMaster {
		t.Fatalf("expected source=master, useMaster=true, got source=%+v useMaster=%v", source, useMaster)
	}
}

// ---- liveDumpCatalog (RejoinDirectDump's live-source catalog) ----

func TestLiveDumpCatalog_BackupReplicaAndMasterBothPresent(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306")
	replica.PreferedBackup = true
	cl.Servers = serverList{master, replica}
	cl.master = master
	cl.StateMachine.Discovered = true

	cat := cl.liveDumpCatalog()
	if len(cat) != 2 {
		t.Fatalf("expected 2 live entries (replica + master), got %d: %+v", len(cat), cat)
	}
	for _, e := range cat {
		if e.Location != RepoLive || e.Kind != "logical" || e.Tool != "mysqldump" {
			t.Fatalf("unexpected live entry shape: %+v", e)
		}
	}
}

func TestLiveDumpCatalog_NoBackupReplicaDedupesToOneMasterEntry(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306") // PreferedBackup left false
	cl.Servers = serverList{master, replica}
	cl.master = master
	cl.StateMachine.Discovered = true

	// GetBackupServer() falls back to master when nobody has PreferedBackup
	// set (cluster_get.go) -- liveDumpCatalog must not double-add master.
	cat := cl.liveDumpCatalog()
	if len(cat) != 1 || cat[0].Server != master.URL {
		t.Fatalf("expected exactly 1 deduped master entry, got %d: %+v", len(cat), cat)
	}
}

// ---- ResolveResticSnapshot (restic reseed request-builder auto-select) ----

func TestResolveResticSnapshot_PicksRemoteEntryForMethod(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod:     backupmgr.BackupMethodLogical,
		BackupTool:       "mysqldump",
		ResticEnabled:    true,
		ResticSnapshotID: "snap-logical",
		Completed:        true,
		EndTime:          time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{{Id: "snap-logical"}}}
	markSnapshotMetadataReady(cl, "snap-logical")

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "snap-logical" {
		t.Fatalf("expected snap-logical, got %q", got)
	}
}

func TestResolveResticSnapshot_PrunedSnapshotDegradesToEmpty(t *testing.T) {
	// The catalog can carry a restic snapshot ID from historical metadata
	// (BackupMetaMap/LastBackupMeta) after the snapshot itself has been
	// purged from the restic repo -- buildBackupCatalog has no freshness
	// check against the CURRENT snapshot list. ResolveResticSnapshot must
	// not hand back an ID that's guaranteed to fail
	// "Snapshot with given ID not found" downstream.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod:     backupmgr.BackupMethodLogical,
		BackupTool:       "mysqldump",
		ResticEnabled:    true,
		ResticSnapshotID: "snap-purged",
		Completed:        true,
		EndTime:          time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	// ResticManager has no snapshots at all (as if snap-purged was removed).
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}}

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "" {
		t.Fatalf("expected empty pick for a since-purged snapshot, got %q", got)
	}
}

func TestResolveResticSnapshot_NotReadySnapshotDegradesToEmpty(t *testing.T) {
	// A catalog entry's Kind/Tool can come straight from BackupMetaMap
	// (backupMetaToCatalog), which is entirely independent of the separate
	// fetch/extraction status cache handlerMuxServerReseedRestic gates on
	// via RequireSnapshotMetadataReady. The snapshot here EXISTS in
	// ResticManager.Backups (passes the existence check) but its metadata
	// extraction status was never marked ready (defaults to Unknown) --
	// ResolveResticSnapshot must not hand back an ID that's guaranteed to
	// fail RequireSnapshotMetadataReady's 409 downstream.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod:     backupmgr.BackupMethodLogical,
		BackupTool:       "mysqldump",
		ResticEnabled:    true,
		ResticSnapshotID: "snap-notready",
		Completed:        true,
		EndTime:          time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-notready"},
	}}
	// Deliberately NOT calling markSnapshotMetadataReady here.

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "" {
		t.Fatalf("expected empty pick for a not-yet-ready snapshot, got %q", got)
	}
}

func TestResolveResticSnapshot_FallsBackToNextBestWhenNewestNotReady(t *testing.T) {
	// Two remote logical candidates: the newest one's metadata was never
	// marked ready (as if extraction is still pending), the older one is
	// fully ready. ResolveResticSnapshot must not degrade to "" just
	// because the top-ranked (newest) candidate isn't usable yet -- it
	// should fall through to the older, ready one instead.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	newer := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-newer-notready",
		Completed: true, EndTime: time.Unix(2000, 0),
	}
	older := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-older-ready",
		Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.BackupMetaMap.Store(newer.Id, newer)
	cl.BackupMetaMap.Store(older.Id, older)
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-newer-notready"}, {Id: "snap-older-ready"},
	}}
	// snap-newer-notready deliberately left without markSnapshotMetadataReady.
	markSnapshotMetadataReady(cl, "snap-older-ready")

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "snap-older-ready" {
		t.Fatalf("expected fallback to the older ready snapshot, got %q", got)
	}
}

func TestResolveResticSnapshot_FallsBackToNextBestWhenNewestPruned(t *testing.T) {
	// Same idea, but the newest candidate's snapshot ID was pruned from the
	// restic repo entirely (absent from ResticManager.Backups) rather than
	// just not-ready.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	newer := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-newer-pruned",
		Completed: true, EndTime: time.Unix(2000, 0),
	}
	older := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-older-present",
		Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.BackupMetaMap.Store(newer.Id, newer)
	cl.BackupMetaMap.Store(older.Id, older)
	// snap-newer-pruned is absent from Backups entirely.
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-older-present"},
	}}
	markSnapshotMetadataReady(cl, "snap-older-present")

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "snap-older-present" {
		t.Fatalf("expected fallback to the older present snapshot, got %q", got)
	}
}

func TestResolveResticSnapshot_NilResticManagerDegradesToEmpty(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod:     backupmgr.BackupMethodLogical,
		BackupTool:       "mysqldump",
		ResticEnabled:    true,
		ResticSnapshotID: "snap-logical",
		Completed:        true,
		EndTime:          time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	// cl.ResticManager left nil.

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "" {
		t.Fatalf("expected empty pick with no ResticManager, got %q", got)
	}
}

func TestResolveResticSnapshot_NoRemoteCandidateReturnsEmpty(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical,
		BackupTool:   "mysqldump",
		Dest:         "/backups/db1/mysqldump.sql.gz",
		Completed:    true,
		EndTime:      time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "" {
		t.Fatalf("expected no auto-pick when only a local backup exists, got %q", got)
	}
}

func TestResolveResticSnapshot_ToolGateOverridesNewest(t *testing.T) {
	// Two remote logical snapshots: a newer mydumper (directory-based -- not
	// dump-strategy compatible) and an older mysqldump. Without a tool gate,
	// "newest" ranking would pick mydumper; forcing tool=mysqldump (what
	// handlerMuxServerReseedRestic now does for method=logical+strategy=dump,
	// server/api_database.go) must exclude it via the hard gate instead of
	// merely being outranked.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	newer := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mydumper",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-mydumper",
		Completed: true, EndTime: time.Unix(2000, 0),
	}
	older := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-mysqldump",
		Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.BackupMetaMap.Store(newer.Id, newer)
	cl.BackupMetaMap.Store(older.Id, older)
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-mydumper"}, {Id: "snap-mysqldump"},
	}}
	markSnapshotMetadataReady(cl, "snap-mydumper")
	markSnapshotMetadataReady(cl, "snap-mysqldump")

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "snap-mydumper" {
		t.Fatalf("expected newest (mydumper) without a tool gate, got %q", got)
	}
	if got, _ := cl.ResolveResticSnapshot("logical", "mysqldump", false); got != "snap-mysqldump" {
		t.Fatalf("tool=mysqldump must exclude the newer mydumper snapshot, got %q", got)
	}
}

func TestResolveResticSnapshot_RequireSingleFileExcludesSplitdump(t *testing.T) {
	// A splitdump-mode mysqldump backup is STILL cataloged with
	// Tool=="mysqldump" (srv_job_backup.go:3184 sets SplitDump as a bool
	// orthogonal to BackupTool, not a distinct tool name), so tool=mysqldump
	// alone cannot exclude it -- only IsDirectory (isDirectoryBackupLayout,
	// restore_catalog.go) can. requireSingleFile=true (what
	// handlerMuxServerReseedRestic now passes for method=logical+strategy=dump)
	// must exclude the newer splitdump entry even though tool=mysqldump
	// matches both. This is the BackupMetaMap-backed path (backupMetaToCatalog);
	// see TestResolveResticSnapshot_RequireSingleFileExcludesSplitdumpViaSummary
	// for the summary-backed path (snapshotSummaryToCatalog).
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	splitDump := &backupmgr.BackupMetadata{
		Id: 2, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-splitdump",
		Completed: true, EndTime: time.Unix(2000, 0), SplitDump: true,
	}
	singleFile := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, ResticEnabled: true, ResticSnapshotID: "snap-singlefile",
		Completed: true, EndTime: time.Unix(1000, 0), SplitDump: false,
	}
	cl.Servers = serverList{sv}
	cl.BackupMetaMap.Store(splitDump.Id, splitDump)
	cl.BackupMetaMap.Store(singleFile.Id, singleFile)
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-splitdump"}, {Id: "snap-singlefile"},
	}}
	markSnapshotMetadataReady(cl, "snap-splitdump")
	markSnapshotMetadataReady(cl, "snap-singlefile")

	if got, _ := cl.ResolveResticSnapshot("logical", "mysqldump", false); got != "snap-splitdump" {
		t.Fatalf("without requireSingleFile, expected newest (splitdump) to win on tool=mysqldump alone, got %q", got)
	}
	if got, _ := cl.ResolveResticSnapshot("logical", "mysqldump", true); got != "snap-singlefile" {
		t.Fatalf("requireSingleFile=true must exclude the splitdump snapshot despite tool=mysqldump matching, got %q", got)
	}
}

func TestResolveResticSnapshot_RequireSingleFileExcludesSplitdumpViaSummary(t *testing.T) {
	// Same as TestResolveResticSnapshot_RequireSingleFileExcludesSplitdump,
	// but the splitdump entry reaches the catalog via the SUMMARY-backed
	// path (snapshotSummaryToCatalog, no surviving BackupMetaMap record for
	// this exact snapshot) rather than a directly tracked BackupMetadata --
	// the "Option B" gap: SnapshotMetadataSummary now carries SplitDump
	// through (cluster_bck_meta.go) so isDirectoryBackupLayout can still
	// exclude it here too.
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-splitdump-summary", Time: time.Unix(2000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
		{Id: "snap-singlefile-summary", Time: time.Unix(1000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}
	snapshotIndexFixture(cl, "snap-splitdump-summary", &SnapshotMetadataSummary{
		Dest: "/backups/db1/splitdump", BackupMethod: "logical", BackupTool: "mysqldump",
		BackupLine: backupmgr.BackupLineDefault, StartTime: time.Unix(1900, 0), EndTime: time.Unix(2000, 0),
		ResticSnapshotID: "snap-splitdump-summary", SplitDump: true,
	})
	snapshotIndexFixture(cl, "snap-singlefile-summary", &SnapshotMetadataSummary{
		Dest: "/backups/db1/mysqldump.sql.gz", BackupMethod: "logical", BackupTool: "mysqldump",
		BackupLine: backupmgr.BackupLineDefault, StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0),
		ResticSnapshotID: "snap-singlefile-summary", SplitDump: false,
	})

	if got, _ := cl.ResolveResticSnapshot("logical", "mysqldump", false); got != "snap-splitdump-summary" {
		t.Fatalf("without requireSingleFile, expected newest (splitdump) to win, got %q", got)
	}
	if got, _ := cl.ResolveResticSnapshot("logical", "mysqldump", true); got != "snap-singlefile-summary" {
		t.Fatalf("requireSingleFile=true must exclude the summary-backed splitdump snapshot too, got %q", got)
	}
}

func TestResolveResticSnapshot_GatesOnMethod(t *testing.T) {
	cl := newCatalogTestCluster(t)
	sv := newCatalogTestServer(cl, "db1", "3306")
	sv.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		BackupMethod:     backupmgr.BackupMethodPhysical,
		BackupTool:       "mariabackup",
		ResticEnabled:    true,
		ResticSnapshotID: "snap-physical",
		Completed:        true,
		EndTime:          time.Unix(1000, 0),
	}
	cl.Servers = serverList{sv}
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{{Id: "snap-physical"}}}
	markSnapshotMetadataReady(cl, "snap-physical")

	if got, _ := cl.ResolveResticSnapshot("logical", "", false); got != "" {
		t.Fatalf("expected no logical pick when only a physical restic snapshot exists, got %q", got)
	}
	if got, _ := cl.ResolveResticSnapshot("physical", "", false); got != "snap-physical" {
		t.Fatalf("expected snap-physical for physical method, got %q", got)
	}
}
