package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func warn0280(c *Cluster) string {
	for _, st := range c.StateMachine.GetOpenStates() {
		if st.ErrKey == "WARN0280" {
			return st.ErrDesc
		}
	}
	return ""
}

// The tables a restore kept as the server has them open WARN0280 on every refresh, until a
// new reseed starts.
func TestRestoreKeptTablesState(t *testing.T) {
	c, sv := reseedLinkFixture()
	sv.checkRestoreKeptTables()
	if warn0280(c) != "" {
		t.Fatal("no table kept: no WARN0280")
	}
	sv.markRestoreKeptTable("mysql.innodb_index_stats")
	sv.markRestoreKeptTable("mysql.innodb_table_stats")
	sv.markRestoreKeptTable("mysql.innodb_index_stats")
	sv.checkRestoreKeptTables()
	desc := warn0280(c)
	if !strings.Contains(desc, "mysql.innodb_index_stats, mysql.innodb_table_stats") || strings.Count(desc, "innodb_index_stats") != 1 {
		t.Fatalf("WARN0280 names each kept table once: %q", desc)
	}
	c.StateMachine.ClearState()
	sv.checkRestoreKeptTables()
	if warn0280(c) == "" {
		t.Fatal("WARN0280 stays, tick after tick, until the next restore")
	}
	c.StateMachine.ClearState()
	if ok, _ := sv.TrySetInReseedBackup("reseedmysqldump"); !ok {
		t.Fatal("reseed not armed")
	}
	sv.checkRestoreKeptTables()
	if warn0280(c) != "" {
		t.Fatal("a new reseed clears WARN0280")
	}
}

// A data file of a table with generated columns has its INSERTs rewritten without them;
// any other file is left alone.
func TestSplitdumpGeneratedColumnsRewrite(t *testing.T) {
	_, sv := reseedLinkFixture()
	dir := t.TempDir()
	schema := "CREATE TABLE `server_cost` (\n  `cost_name` varchar(64) NOT NULL,\n  `cost_value` float DEFAULT NULL,\n  `default_value` float GENERATED ALWAYS AS (1.0) VIRTUAL,\n  PRIMARY KEY (`cost_name`)\n) ENGINE=InnoDB"
	if err := os.WriteFile(filepath.Join(dir, "mysql.server_cost-schema.sql"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	if sv.splitdumpGeneratedColumnsRewrite(filepath.Join(dir, "mysql.server_cost-schema.sql")) != nil {
		t.Error("a schema file is not rewritten")
	}
	if sv.splitdumpGeneratedColumnsRewrite(filepath.Join(dir, "mysql.db.00000.sql.gz")) != nil {
		t.Error("a table without a schema file or generated column is not rewritten")
	}
	rewrite := sv.splitdumpGeneratedColumnsRewrite(filepath.Join(dir, "mysql.server_cost.00000.sql.gz"))
	if rewrite == nil {
		t.Fatal("a table with a generated column is rewritten")
	}
	if got, want := rewrite("INSERT INTO `server_cost` VALUES ('disk_temptable_create_cost',NULL,20)"), "INSERT INTO `server_cost` (`cost_name`,`cost_value`) VALUES ('disk_temptable_create_cost',NULL)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := rewrite("INSERT INTO `server_cost` VALUES ('x')"); got != "INSERT INTO `server_cost` VALUES ('x')" {
		t.Errorf("a row that does not parse is sent as dumped: %q", got)
	}
}
