package configurator

import (
	"os"
	"strings"
	"testing"
)

// #2015: a ColumnStore child's channel ignores the tables the plugin carries, in conf.d.
func TestWriteColumnstoreReplicateIgnore(t *testing.T) {
	dir := t.TempDir()
	if err := (&Configurator{}).writeColumnstoreReplicateIgnore(dir, "belair.replicate_ignore_table=repl_test.pk_int,repl_test.pk_enum"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dir + "/init/etc/mysql/conf.d/" + columnstoreReplicateIgnoreFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[mariadb]\nbelair.replicate_ignore_table=repl_test.pk_int,repl_test.pk_enum\n") {
		t.Fatalf("fragment:\n%s", b)
	}
}
