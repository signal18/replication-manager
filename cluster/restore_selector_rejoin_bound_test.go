package cluster

import "testing"

// The rejoin of a failed primary restores no backup ahead of what the new primary received
// at the failover (#2365): on dev4, the old primary's own mydumper backup, holding 100 rows
// the new primary never received, was restored and the old primary diverged.
func TestResolveRestore_RejoinBoundExcludesABackupAheadOfTheFailover(t *testing.T) {
	sel := RestoreSelector{Type: []string{"logical"}, Tool: []string{"mydumper"}, Origin: OriginAny, Repo: RepoAny, Order: []string{"last", "local"}}
	catalog := []BackupCatalogEntry{
		{ID: 1, Server: "db3:3306", Kind: "logical", Tool: "mydumper", Timestamp: 1000, Gtid: "1-100-900"},
		{ID: 2, Server: "db1:3306", Kind: "logical", Tool: "mydumper", Timestamp: 2000, Gtid: "1-100-1100"},
	}
	ctx := ResolveContext{MasterURL: "db2:3306", TargetURL: "db1:3306"}
	if pick := ResolveRestore(catalog, sel, ctx); pick == nil || pick.ID != 2 {
		t.Fatalf("without the bound the newest backup is picked, got %+v", pick)
	}
	ctx.RejoinBoundGtid = "1-100-1000"
	pick := ResolveRestore(catalog, sel, ctx)
	if pick == nil || pick.ID != 1 {
		t.Fatalf("with the failover position 1-100-1000, the backup at 1-100-900 must be picked, got %+v", pick)
	}
	catalog = catalog[1:]
	if pick := ResolveRestore(catalog, sel, ctx); pick != nil {
		t.Fatalf("only a backup ahead of the failover position: none to restore, got %+v", pick)
	}
}

func TestGtidNotAheadOf(t *testing.T) {
	for _, c := range []struct {
		backup, bound string
		want          bool
	}{
		{"1-100-900", "1-100-1000", true},
		{"1-100-1000", "1-100-1000", true},
		{"1-100-1001", "1-100-1000", false},
		{"0-1-5,1-100-900", "0-1-5,1-100-1000", true},
		{"0-1-6,1-100-900", "0-1-5,1-100-1000", false}, // ahead in one domain is ahead
		{"2-7-1", "1-100-1000", false},                 // a domain the new primary never had
		{"1-200-900", "1-100-1000", true},              // the sequence is per domain, whatever server wrote it
		{"", "1-100-1000", false},                      // no position: cannot be placed
		{"3E11FA47-71CA-11E1-9E33-C80AA9429562:1-5", "1-100-1000", false},
		{"1-100-900", "", false},
	} {
		if got := gtidNotAheadOf(c.backup, c.bound); got != c.want {
			t.Errorf("gtidNotAheadOf(%q, %q) = %t, want %t", c.backup, c.bound, got, c.want)
		}
	}
}
