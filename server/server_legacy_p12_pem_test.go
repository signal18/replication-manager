package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A deleted cluster's directory held only the om3 client files written after
// DeleteCluster removed it: the repair at start removes them and the empty directory, so
// the name can be used again. A known cluster's directory stays, an orphan with content too.
func TestRemoveLegacyP12PemRepairsWorkingDir(t *testing.T) {
	wd := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(wd, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("x"), 0600)
	}
	for _, d := range []string{"blab", "dev3"} {
		write(d + "/.s18.p12-key.pem")
		write(d + "/.s18.p12-cert.pem")
		write(d + "/.s18.p12-chain.pem")
	}
	write("orphan/.s18.p12-key.pem")
	write("orphan/backup.tar")
	os.MkdirAll(filepath.Join(wd, "emptykept"), 0755) // a known cluster, empty for now

	repman := &ReplicationManager{Conf: &config.Config{WorkingDir: wd}}
	repman.ClusterList = []string{"dev3", "emptykept"}
	repman.removeLegacyP12Pem()

	if _, err := os.Stat(filepath.Join(wd, "blab")); !os.IsNotExist(err) {
		t.Fatalf("blab's leftover directory must be removed: %v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(wd, "dev3")); len(left) != 0 {
		t.Fatalf("dev3 keeps %v", left)
	}
	if _, err := os.Stat(filepath.Join(wd, "dev3")); err != nil {
		t.Fatal("a known cluster's directory stays")
	}
	if _, err := os.Stat(filepath.Join(wd, "emptykept")); err != nil {
		t.Fatal("a known cluster's empty directory stays")
	}
	if left, _ := os.ReadDir(filepath.Join(wd, "orphan")); len(left) != 1 || left[0].Name() != "backup.tar" {
		t.Fatalf("an orphan with content keeps it: %v", left)
	}
}
