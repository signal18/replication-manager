package configurator

import (
	"os"
	"strings"
	"testing"
)

// #2014: the UM applying its parent's replication gets loose_columnstore_replication_slave=ON
// in conf.d of its bundle.
func TestWriteColumnstoreReplicationSlave(t *testing.T) {
	dir := t.TempDir()
	if err := (&Configurator{}).writeColumnstoreReplicationSlave(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dir + "/init/etc/mysql/conf.d/" + columnstoreReplicationFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[mariadb]\nloose_columnstore_replication_slave=ON\n") {
		t.Fatalf("fragment:\n%s", b)
	}
}
