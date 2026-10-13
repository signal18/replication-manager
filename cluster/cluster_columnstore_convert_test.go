package cluster

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
func ni(n int64) sql.NullInt64   { return sql.NullInt64{Int64: n, Valid: true} }

// The columns of a dev3 InnoDB table as information_schema.COLUMNS reports them (#2010).
func columnstoreSampleColumns() []dbhelper.TableColumn {
	return []dbhelper.TableColumn{
		{Name: "id", DataType: "int", ColumnType: "int(10) unsigned", Extra: "auto_increment", Default: ns("NULL")},
		{Name: "t", DataType: "tinyint", ColumnType: "tinyint(4)", Default: ns("1")},
		{Name: "e", DataType: "enum", ColumnType: "enum('red','green','blue-ish')", Nullable: true, Default: ns("'red'"), Charset: ns("utf8mb3"), Collation: ns("utf8mb3_general_ci"), MaxLength: ni(8), OctetLength: ni(24)},
		{Name: "bt", DataType: "bit", ColumnType: "bit(5)", Nullable: true, Precision: ni(5), Default: ns("b'101'")},
		{Name: "y", DataType: "year", ColumnType: "year(4)", Nullable: true},
		{Name: "bin", DataType: "binary", ColumnType: "binary(16)", Nullable: true},
		{Name: "u", DataType: "uuid", ColumnType: "uuid", Nullable: true},
		{Name: "v", DataType: "varchar", ColumnType: "varchar(3000)", Nullable: true, Charset: ns("utf8mb4"), Collation: ns("utf8mb4_bin"), MaxLength: ni(3000), OctetLength: ni(12000)},
		{Name: "name", DataType: "varchar", ColumnType: "varchar(50)", Charset: ns("latin1"), Collation: ns("latin1_swedish_ci"), MaxLength: ni(50), OctetLength: ni(50)},
		{Name: "ts", DataType: "timestamp", ColumnType: "timestamp", Default: ns("current_timestamp()"), Extra: "on update current_timestamp()"},
		{Name: "d", DataType: "decimal", ColumnType: "decimal(12,3) unsigned", Nullable: true, Default: ns("0.500"), Precision: ni(12), Scale: ni(3)},
		{Name: "g", DataType: "int", ColumnType: "int(11)", Nullable: true, Extra: "VIRTUAL GENERATED", GenerationExp: ns("`id` * 2")},
		{Name: "big", DataType: "bigint", ColumnType: "bigint(20)", Nullable: true},
		{Name: "ubig", DataType: "bigint", ColumnType: "bigint(20) unsigned", Nullable: true},
		{Name: "m", DataType: "mediumint", ColumnType: "mediumint(8) unsigned", Nullable: true},
		{Name: "nm", DataType: "varchar", ColumnType: "varchar(10)", Nullable: true, Default: ns("'it''s'"), Charset: ns("utf8mb3"), Collation: ns("utf8mb3_general_ci"), MaxLength: ni(10), OctetLength: ni(30), Comment: "customer name"},
	}
}

func TestColumnstoreTableDDL(t *testing.T) {
	conv, err := columnstoreTableDDL("conv", "src__mcs", columnstoreSampleColumns(),
		[]dbhelper.TableCheck{{Name: "CONSTRAINT_1", Clause: "`t` > -100"}},
		dbhelper.TableOptions{Engine: "InnoDB", Charset: "utf8mb4", Collation: "utf8mb4_general_ci", Comment: "orders", Partitioned: true},
		[]string{"PRIMARY", "name"}, []string{"fk_customer"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CREATE TABLE `conv`.`src__mcs` (",
		"`id` bigint unsigned NOT NULL,",   // INT UNSIGNED widened, AUTO_INCREMENT gone, NOT NULL kept
		"`t` smallint NOT NULL DEFAULT 1,", // TINYINT widened, literal default kept
		"`e` varchar(8) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci DEFAULT 'red',",
		"`bt` bigint unsigned,", // BIT, its b'' default removed
		"`y` smallint,",
		"`bin` blob,",
		"`u` char(36),",
		"`v` text CHARACTER SET utf8mb4 COLLATE utf8mb4_bin,",                // 12000 bytes > 8000
		"`name` varchar(50) CHARACTER SET latin1 COLLATE latin1_swedish_ci,", // NOT NULL removed on a string
		"`ts` timestamp NOT NULL,",                                           // CURRENT_TIMESTAMP default and ON UPDATE removed
		"`d` decimal(12,3) unsigned DEFAULT 0.500,",
		"`g` bigint,", // generated becomes plain, INT widened
		"`big` decimal(19,0),",
		"`ubig` decimal(20,0) unsigned,",
		"`m` mediumint(8) unsigned,", // MEDIUMINT has its full range
		"`nm` varchar(10) CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci DEFAULT 'it''s' COMMENT 'customer name',",
		"CONSTRAINT `CONSTRAINT_1` CHECK (`t` > -100)",
		") ENGINE=Columnstore DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='orders'",
	} {
		if !strings.Contains(conv.DDL, want) {
			t.Errorf("DDL misses %q:\n%s", want, conv.DDL)
		}
	}
	if strings.Contains(strings.ToUpper(conv.DDL), "KEY") || strings.Contains(strings.ToUpper(conv.DDL), "AUTO_INCREMENT") || strings.Contains(strings.ToUpper(conv.DDL), "PARTITION") {
		t.Errorf("no key, AUTO_INCREMENT or partition in a ColumnStore table:\n%s", conv.DDL)
	}
	changes := strings.Join(conv.Changes, "\n")
	for _, want := range []string{
		"id: AUTO_INCREMENT removed", "t: tinyint(4) widened to smallint", "bt: DEFAULT b'101' removed",
		"name: NOT NULL removed", "ts: DEFAULT current_timestamp() removed", "ts: ON UPDATE removed",
		"g: generated column becomes a plain column", "indexes removed: PRIMARY, name",
		"foreign keys removed: fk_customer", "partitioning removed",
	} {
		if !strings.Contains(changes, want) {
			t.Errorf("changes miss %q:\n%s", want, changes)
		}
	}
}

// A table ColumnStore cannot hold stays as it is: the error names the column.
func TestColumnstoreTableDDLRefusal(t *testing.T) {
	for _, c := range []dbhelper.TableColumn{
		{Name: "g", DataType: "point", ColumnType: "point"},
		{Name: "d", DataType: "decimal", ColumnType: "decimal(65,10)", Precision: ni(65)},
		{Name: "x", DataType: "vector", ColumnType: "vector(3)"},
	} {
		if _, err := columnstoreTableDDL("s", "t", []dbhelper.TableColumn{c}, nil, dbhelper.TableOptions{}, nil, nil); err == nil || !strings.Contains(err.Error(), "column "+c.Name) {
			t.Errorf("%s must be refused naming its column, got %v", c.ColumnType, err)
		}
	}
	// a comment ColumnStore would read as an option is dropped
	conv, err := columnstoreTableDDL("s", "t", []dbhelper.TableColumn{{Name: "a", DataType: "int", ColumnType: "int(11)", Nullable: true, Comment: "autoincrement"}},
		nil, dbhelper.TableOptions{Comment: "compression=0"}, nil, nil)
	if err != nil || strings.Contains(conv.DDL, "COMMENT") {
		t.Errorf("comments read as ColumnStore options must go: %v\n%s", err, conv.DDL)
	}
}
