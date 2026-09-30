package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// An unprovisioned instance leaves NOTHING of its old life in repman: datadir recreated
// empty, its crash events gone from disk and memory (so the rejoin one-shot guard owes
// the new life an attempt), tracked state reset -- without a restart.
func TestForgetInstance_CleanSlateWithoutRestart(t *testing.T) {
	wd := t.TempDir()
	c := &Cluster{Name: "t", Conf: &config.Config{WorkingDir: wd}, resources: NewResourceManager()}
	c.WorkingDir = filepath.Join(wd, "t")
	url := "db2.t:3306"
	other := "db1.t:3306"
	srv := &ServerMonitor{ClusterGroup: c, URL: url, Host: "db2.t", Port: "3306", Datadir: filepath.Join(c.WorkingDir, "db2.t_3306"), FailCount: 3, IsReseeding: "logical"}
	srv.reseedFromRejoin.Store(true)
	srv.DBUConsumed = &DBUReading{Dbu: 1}
	c.resources.SetConsumed(ResourceKey{Cluster: c.Name, Server: url}, srv.DBUConsumed)

	// The old life on disk: cookies + config in the datadir, two crash events, one per server.
	os.MkdirAll(srv.Datadir, 0o755)
	os.WriteFile(filepath.Join(srv.Datadir, "@cookie_prov"), nil, 0o644)
	os.WriteFile(filepath.Join(srv.Datadir, "01_preserved.cnf"), []byte("[mysqld]\n"), 0o644)
	os.MkdirAll(filepath.Join(srv.Datadir, "log"), 0o755)
	os.WriteFile(filepath.Join(srv.Datadir, "log", "error.log"), []byte("x"), 0o644)
	mine := &Crash{URL: url, ElectedMasterURL: other, UnixTimestamp: 100, RejoinResult: RejoinResultNoDivergence, RejoinResultTs: 101}
	theirs := &Crash{URL: other, ElectedMasterURL: url, UnixTimestamp: 200}
	for _, cr := range []*Crash{mine, theirs} {
		c.ensureCrashArchive(cr)
		if err := cr.Save(cr.ArchiveDir + "/crash.json"); err != nil {
			t.Fatal(err)
		}
	}
	c.LoadFailoverHistory()
	if len(c.FailoverHistory) != 2 {
		t.Fatalf("fixture: %d events loaded, want 2", len(c.FailoverHistory))
	}
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("fixture: the old outcome must block the rejoin before the forget")
	}

	c.ForgetInstance(srv)

	if _, err := os.Stat(filepath.Join(srv.Datadir, "@cookie_prov")); !os.IsNotExist(err) {
		t.Fatalf("cookie must be gone with the datadir")
	}
	if _, err := os.Stat(filepath.Join(srv.Datadir, "01_preserved.cnf")); !os.IsNotExist(err) {
		t.Fatalf("preserved cnf must be gone with the datadir")
	}
	if _, err := os.Stat(filepath.Join(srv.Datadir, "log", "error.log")); !os.IsNotExist(err) {
		t.Fatalf("files under the tree must be gone")
	}
	for _, d := range []string{"log", "var", "init"} {
		if fi, err := os.Stat(filepath.Join(srv.Datadir, d)); err != nil || !fi.IsDir() {
			t.Fatalf("the directory tree must be kept: %s/", d)
		}
	}
	if _, err := os.Stat(mine.ArchiveDir); !os.IsNotExist(err) {
		t.Fatalf("the instance's crash archive must be deleted")
	}
	if _, err := os.Stat(theirs.ArchiveDir); err != nil {
		t.Fatalf("another server's crash archive must survive")
	}
	if len(c.FailoverHistory) != 1 || c.FailoverHistory[0].URL != other {
		t.Fatalf("history must hold only the other server's event, got %+v", c.FailoverHistory)
	}
	if c.rejoinAlreadyAttempted(url) {
		t.Fatalf("the new life owes a rejoin attempt: the guard must not block")
	}
	if srv.FailCount != 0 || srv.IsReseeding != "" || srv.reseedFromRejoin.Load() || srv.DBUConsumed != nil {
		t.Fatalf("tracked state must be reset: %+v", srv)
	}
	if c.resources.GetConsumed(ResourceKey{Cluster: c.Name, Server: url}) != nil {
		t.Fatalf("the resource manager must drop the consumed reading")
	}
}
