package splitdump

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mysql.engine_cost as a MariaDB client dumps a MySQL 8.0 server.
const engineCostSchema = "DROP TABLE IF EXISTS `engine_cost`;\n" +
	"CREATE TABLE `engine_cost` (\n" +
	"  `engine_name` varchar(64) NOT NULL,\n" +
	"  `device_type` int NOT NULL,\n" +
	"  `cost_name` varchar(64) NOT NULL,\n" +
	"  `cost_value` float DEFAULT NULL,\n" +
	"  `last_update` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,\n" +
	"  `comment` varchar(1024) DEFAULT NULL,\n" +
	"  `default_value` float GENERATED ALWAYS AS ((case `cost_name` when _utf8mb3'io_block_read_cost' then 1.0 else NULL end)) VIRTUAL,\n" +
	"  PRIMARY KEY (`cost_name`,`engine_name`,`device_type`)\n" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb3;\n"

func TestGeneratedColumns(t *testing.T) {
	columns, generated := GeneratedColumns(engineCostSchema)
	if want := []string{"engine_name", "device_type", "cost_name", "cost_value", "last_update", "comment", "default_value"}; !reflect.DeepEqual(columns, want) {
		t.Errorf("columns = %v, want %v", columns, want)
	}
	if want := []bool{false, false, false, false, false, false, true}; !reflect.DeepEqual(generated, want) {
		t.Errorf("generated = %v, want %v", generated, want)
	}
	// MariaDB's STORED/PERSISTENT forms, an escaped backtick in a name, key lines skipped
	columns, generated = GeneratedColumns("CREATE TABLE `t` (\n  `a``b` int(11) DEFAULT NULL,\n  `c` int(11) GENERATED ALWAYS AS (`a``b` * 2) STORED,\n  `d` varchar(10) DEFAULT 'GENERATED',\n  KEY `k` (`c`)\n) ENGINE=InnoDB")
	if !reflect.DeepEqual(columns, []string{"a`b", "c", "d"}) || !reflect.DeepEqual(generated, []bool{false, true, false}) {
		t.Errorf("got %v %v", columns, generated)
	}
}

func TestInsertWithoutGeneratedColumns(t *testing.T) {
	columns, generated := GeneratedColumns(engineCostSchema)
	for _, c := range []struct{ in, want string }{
		{"INSERT INTO `engine_cost` VALUES\n('default',0,'io_block_read_cost',NULL,'2026-10-30 19:37:32',NULL,1),\n('default',0,'memory_block_read_cost',NULL,'2026-10-30 19:37:32',NULL,0.25)",
			"INSERT INTO `engine_cost` (`engine_name`,`device_type`,`cost_name`,`cost_value`,`last_update`,`comment`) VALUES ('default',0,'io_block_read_cost',NULL,'2026-10-30 19:37:32',NULL),('default',0,'memory_block_read_cost',NULL,'2026-10-30 19:37:32',NULL)"},
		// quotes, escapes, commas and parentheses inside strings
		{"INSERT IGNORE INTO `mysql`.`engine_cost` VALUES ('a,b',1,'it''s (x)','o\\'k, \\\\',NULL,\"q,\\\"\",2)",
			"INSERT IGNORE INTO `mysql`.`engine_cost` (`engine_name`,`device_type`,`cost_name`,`cost_value`,`last_update`,`comment`) VALUES ('a,b',1,'it''s (x)','o\\'k, \\\\',NULL,\"q,\\\"\")"},
		// a column list already: unchanged
		{"INSERT INTO `engine_cost` (`engine_name`) VALUES ('x')", "INSERT INTO `engine_cost` (`engine_name`) VALUES ('x')"},
		// not an INSERT: unchanged
		{"UPDATE `engine_cost` SET cost_value=1", "UPDATE `engine_cost` SET cost_value=1"},
	} {
		got, err := InsertWithoutGeneratedColumns(c.in, columns, generated)
		if err != nil || got != c.want {
			t.Errorf("InsertWithoutGeneratedColumns(%q)\n got %q, %v\nwant %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{
		"INSERT INTO `engine_cost` VALUES ('default',0)",         // one value per column
		"INSERT INTO `engine_cost` VALUES ('default',0,'x',NULL", // not closed
		"INSERT INTO `engine_cost` VALUES ('unterminated)",
		"INSERT INTO `engine_cost` VALUES (1,2,3,4,5,6,7) ON DUPLICATE KEY UPDATE a=1",
	} {
		if _, err := InsertWithoutGeneratedColumns(bad, columns, generated); err == nil {
			t.Errorf("InsertWithoutGeneratedColumns(%q): expected an error", bad)
		}
	}
}

func TestTableGeneratedColumns(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "mysql.engine_cost-schema.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	gz.Write([]byte(engineCostSchema))
	gz.Close()
	f.Close()
	if err := os.WriteFile(filepath.Join(dir, "mysql.db-schema.sql"), []byte("CREATE TABLE `db` (\n  `Host` char(255) NOT NULL\n) ENGINE=InnoDB"), 0o600); err != nil {
		t.Fatal(err)
	}
	columns, generated, ok, err := TableGeneratedColumns(filepath.Join(dir, "mysql.engine_cost.00000.sql.gz"))
	if err != nil || !ok || len(columns) != 7 || !generated[6] {
		t.Errorf("engine_cost: %v %v %v %v", columns, generated, ok, err)
	}
	if _, _, ok, err := TableGeneratedColumns(filepath.Join(dir, "mysql.db.00000.sql.gz")); ok || err != nil {
		t.Errorf("db has no generated column: ok %v err %v", ok, err)
	}
	if _, _, ok, err := TableGeneratedColumns(filepath.Join(dir, "mysql.nosuch.00000.sql.gz")); ok || err != nil {
		t.Errorf("no schema file: ok %v err %v", ok, err)
	}
}
