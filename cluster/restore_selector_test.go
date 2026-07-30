// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"reflect"
	"testing"

	"github.com/signal18/replication-manager/utils/gtid"
)

// a small fixed catalog covering the axes the resolver ranks/gates on.
func fixtureCatalog() []BackupCatalogEntry {
	return []BackupCatalogEntry{
		{Server: "db1", Location: RepoLocal, Kind: "logical", Tool: "mysqldump", Timestamp: 100, Gtid: "0-1-100", Proven: false},
		{Server: "db2", Location: RepoLocal, Kind: "logical", Tool: "mydumper", Caps: []string{"can-partial-restorable"}, Timestamp: 300, Gtid: "0-1-300", Proven: true},
		{Server: "db2", Location: RepoRemote, Kind: "logical", Tool: "mysqldump", Timestamp: 400, Gtid: "0-1-400", Proven: false},
		{Server: "db3", Location: RepoLocal, Kind: "physical", Tool: "mariadbbackup", Timestamp: 250, Gtid: "0-1-250", Proven: false},
	}
}

func TestGate_Type(t *testing.T) {
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Type: []string{"physical"}}, ResolveContext{})
	if got == nil || got.Kind != "physical" {
		t.Fatalf("type=physical should pick the physical backup, got %+v", got)
	}
}

func TestGate_TypeCap(t *testing.T) {
	// type=["logical","can-partial-restorable"] must AND: only db2 mydumper qualifies
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Type: []string{"logical", "can-partial-restorable"}}, ResolveContext{})
	if got == nil || got.Tool != "mydumper" {
		t.Fatalf("type AND cap should pick the mydumper backup, got %+v", got)
	}
}

func TestGate_Tool(t *testing.T) {
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Tool: []string{"mariadbbackup"}}, ResolveContext{})
	if got == nil || got.Tool != "mariadbbackup" {
		t.Fatalf("tool=mariadbbackup should pick it, got %+v", got)
	}
}

func TestGate_Proven(t *testing.T) {
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Proven: true}, ResolveContext{})
	if got == nil || !got.Proven {
		t.Fatalf("proven gate must return a proven backup, got %+v", got)
	}
	if got.Server != "db2" || got.Tool != "mydumper" {
		t.Fatalf("only db2 mydumper is proven, got %+v", got)
	}
}

func TestGate_TimeNotAfterHead(t *testing.T) {
	// head at seq 300 → the remote seq-400 backup is excluded (beyond head)
	ctx := ResolveContext{HeadGtid: "0-1-300"}
	sel := RestoreSelector{Time: TimeNotAfterHead, Order: []string{"last"}}
	got := ResolveRestore(fixtureCatalog(), sel, ctx)
	if got == nil {
		t.Fatal("expected a candidate within the head window")
	}
	if maxGtidSeq(got.Gtid) > 300 {
		t.Fatalf("notafterhead must exclude backups beyond seq 300, got %s", got.Gtid)
	}
}

func TestGate_TimeNotBeforeOldest(t *testing.T) {
	// oldest at seq 250 → seq-100 backup excluded (purged past)
	ctx := ResolveContext{OldestGtid: "0-1-250"}
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Time: TimeNotBeforeOldest}, ctx)
	if got == nil {
		t.Fatal("expected a candidate at/after oldest")
	}
	if maxGtidSeq(got.Gtid) < 250 {
		t.Fatalf("nobeforeoldest must exclude backups before seq 250, got %s", got.Gtid)
	}
}

func TestRank_OrderLast(t *testing.T) {
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Type: []string{"logical"}, Order: []string{"last"}}, ResolveContext{})
	if got == nil || got.Timestamp != 400 {
		t.Fatalf("order=last should pick the newest logical (ts=400), got %+v", got)
	}
}

func TestRank_OrderLastThenLocal(t *testing.T) {
	// two logicals at different ts; last dominates, local is the tiebreak.
	cat := []BackupCatalogEntry{
		{Server: "db1", Location: RepoRemote, Kind: "logical", Tool: "mysqldump", Timestamp: 500, Gtid: "0-1-500"},
		{Server: "db1", Location: RepoLocal, Kind: "logical", Tool: "mysqldump", Timestamp: 500, Gtid: "0-1-500"},
	}
	got := ResolveRestore(cat, RestoreSelector{Type: []string{"logical"}, Order: []string{"last", "local"}}, ResolveContext{})
	if got == nil || !got.isLocal() {
		t.Fatalf("equal ts → local tiebreak should win, got %+v", got)
	}
}

func TestRank_PreferMasterOrigin(t *testing.T) {
	ctx := ResolveContext{MasterURL: "db2"}
	// no order → origin=master preference should surface a db2 backup
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Type: []string{"logical"}, Origin: OriginMaster}, ctx)
	if got == nil || got.Server != "db2" {
		t.Fatalf("origin=master should prefer a db2 backup, got %+v", got)
	}
}

func TestRank_SafetyPrefersLocalForNetwork(t *testing.T) {
	// preservenetwork ranks local ahead of remote (but never excludes).
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Type: []string{"logical"}, Safety: []string{"preservenetwork"}}, ResolveContext{})
	if got == nil || !got.isLocal() {
		t.Fatalf("preservenetwork should prefer a local backup, got %+v", got)
	}
}

func TestRank_SafetyMasterloadPrefersNonMasterLiveEntry(t *testing.T) {
	// Two "live" candidates (no on-disk backup involved, see liveDumpCatalog
	// in restore_catalog.go): one served by the master, one by a designated
	// backup replica. preservemasterload/preservemasterlock must rank the
	// non-master one first -- this is what lets resolveLiveDumpSource
	// (srv_rejoin.go) reproduce "prefer the backup replica, else master"
	// through the selector.
	cat := []BackupCatalogEntry{
		{Server: "master", Location: RepoLive, Kind: "logical", Tool: "mysqldump", Timestamp: 100},
		{Server: "replica", Location: RepoLive, Kind: "logical", Tool: "mysqldump", Timestamp: 100},
	}
	ctx := ResolveContext{MasterURL: "master"}
	sel := RestoreSelector{Origin: OriginAny, Repo: RepoLive, Safety: []string{"preservenetwork", "preservemasterload"}}
	got := ResolveRestore(cat, sel, ctx)
	if got == nil || got.Server != "replica" {
		t.Fatalf("preservemasterload should prefer the non-master live entry, got %+v", got)
	}
}

func TestRank_SafetyMasterloadFallsBackToMasterWhenOnlyCandidate(t *testing.T) {
	cat := []BackupCatalogEntry{
		{Server: "master", Location: RepoLive, Kind: "logical", Tool: "mysqldump", Timestamp: 100},
	}
	ctx := ResolveContext{MasterURL: "master"}
	sel := RestoreSelector{Origin: OriginAny, Repo: RepoLive, Safety: []string{"preservenetwork", "preservemasterload"}}
	got := ResolveRestore(cat, sel, ctx)
	if got == nil || got.Server != "master" {
		t.Fatalf("with only the master live entry available, expected it to be picked, got %+v", got)
	}
}

func TestNoCandidate(t *testing.T) {
	got := ResolveRestore(fixtureCatalog(), RestoreSelector{Tool: []string{"xtrabackup"}}, ResolveContext{})
	if got != nil {
		t.Fatalf("no xtrabackup in catalog → expected nil, got %+v", got)
	}
}

// ---- method presets (doc/implementation/cluster/RESTORE_SELECTOR.md table) ----

func TestPresetRejoinLogical(t *testing.T) {
	got := PresetRejoinLogical()
	want := RestoreSelector{
		Origin: OriginAny, Repo: RepoAny, Type: []string{"logical"}, Time: TimeNotAfterHead,
		Safety: []string{"any"}, Order: []string{"last", "local"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetRejoinLogical = %+v, want %+v", got, want)
	}
}

func TestPresetReseedFromMaster(t *testing.T) {
	got := PresetReseedFromMaster()
	want := RestoreSelector{Origin: OriginMaster, Repo: RepoLive, Safety: []string{"any"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetReseedFromMaster = %+v, want %+v", got, want)
	}
}

func TestPresetReseedSpareMaster(t *testing.T) {
	got := PresetReseedSpareMaster()
	want := RestoreSelector{Origin: OriginMaster, Repo: RepoLive, Safety: []string{"preservenetwork"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetReseedSpareMaster = %+v, want %+v", got, want)
	}
}

func TestPresetResticRemoteFetch(t *testing.T) {
	got := PresetResticRemoteFetch()
	want := RestoreSelector{Repo: RepoRemote}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetResticRemoteFetch = %+v, want %+v", got, want)
	}
}

func TestPresetRejoinPhysical(t *testing.T) {
	got := PresetRejoinPhysical()
	want := RestoreSelector{Type: []string{"physical"}, Order: []string{"last", "local"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetRejoinPhysical = %+v, want %+v", got, want)
	}
}

func TestPresetsReturnFreshValues(t *testing.T) {
	a := PresetRejoinLogical()
	a.Type[0] = "mutated"
	b := PresetRejoinLogical()
	if b.Type[0] == "mutated" {
		t.Fatalf("preset must return a fresh value per call, got a shared slice mutated across calls")
	}
}

// ---- masterHeadGtidString ----

func TestMasterHeadGtidString(t *testing.T) {
	if got := masterHeadGtidString(nil); got != "" {
		t.Fatalf("nil master should yield empty string, got %q", got)
	}
	if got := masterHeadGtidString(&ServerMonitor{}); got != "" {
		t.Fatalf("nil GTIDBinlogPos should yield empty string (not panic), got %q", got)
	}
	master := &ServerMonitor{GTIDBinlogPos: gtid.NewList("0-1-300")}
	if got := masterHeadGtidString(master); got != "0-1-300" {
		t.Fatalf("expected the GTID list's Sprint(), got %q", got)
	}
}
