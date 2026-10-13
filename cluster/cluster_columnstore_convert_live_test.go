package cluster

import (
	"context"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/s18log"
	logsql "github.com/sirupsen/logrus"
)

// TestColumnstoreConversionLive converts InnoDB tables on a real ColumnStore UM the way the
// splitdump reload does (#2010): each table created as the dump writes it, on a pinned
// connection with FOREIGN_KEY_CHECKS=0, then convertTableToColumnstore; then the rows a
// dump holds, the reserved integer extremes and ” in a NOT NULL string included, must load
// and read back unchanged. Runs only with CSCONV_DSN (user:pass@tcp(host:3306)/).
func TestColumnstoreConversionLive(t *testing.T) {
	dsn := os.Getenv("CSCONV_DSN")
	if dsn == "" {
		t.Skip("CSCONV_DSN not set")
	}
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	const schema = "csconv_live"
	if _, err := db.Exec("DROP DATABASE IF EXISTS " + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE DATABASE " + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP DATABASE IF EXISTS " + schema)
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"SET SESSION FOREIGN_KEY_CHECKS=0", "USE " + schema} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	cl := newColumnstoreTestCluster()
	cl.SqlErrorLog, cl.SqlGeneralLog = logsql.New(), logsql.New()
	cl.SQLErrorLog, cl.SQLGeneralLog = s18log.NewHttpLog(20), s18log.NewHttpLog(20)
	server := &ServerMonitor{Conn: db, URL: "live", ClusterGroup: cl}

	tables := []struct{ name, ddl string }{
		{"parent", "CREATE TABLE `parent` (`id` int(11) NOT NULL AUTO_INCREMENT, `label` varchar(20) NOT NULL DEFAULT '', PRIMARY KEY (`id`)) ENGINE=InnoDB"},
		{"child", "CREATE TABLE `child` (`id` int(11) NOT NULL, `pid` int(11) DEFAULT NULL, PRIMARY KEY (`id`), KEY `fk_p` (`pid`), CONSTRAINT `fk_p` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`)) ENGINE=InnoDB"},
		{"src", "CREATE TABLE `src` (" +
			"`id` int(10) unsigned NOT NULL AUTO_INCREMENT, `t` tinyint(4) NOT NULL DEFAULT 1, `ut` tinyint(3) unsigned, `si` smallint(6), `usi` smallint(5) unsigned, " +
			"`i` int(11), `ui` int(10) unsigned, `b` bigint(20), `ub` bigint(20) unsigned, `m` mediumint(9), " +
			"`e` enum('red','green','blue-ish') DEFAULT 'red', `s` set('a','bb','ccc'), `bt` bit(5) DEFAULT b'101', `y` year(4), " +
			"`bin` binary(4), `vb` varbinary(10), `u` uuid, `i6` inet6, `i4` inet4, " +
			"`v` varchar(3000) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin, `nn` varchar(10) NOT NULL, " +
			"`ts` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(), `dt` datetime(6) DEFAULT '2020-01-01 00:00:00.000000', " +
			"`d` decimal(38,10), `g` int(11) GENERATED ALWAYS AS (`t` * 2) VIRTUAL, `j` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`j`)), " +
			"PRIMARY KEY (`id`), UNIQUE KEY `nn` (`nn`), KEY `v` (`v`(10)), CONSTRAINT `t_range` CHECK (`t` > -200)" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb3 COMMENT='orders'"},
	}
	for _, tb := range tables {
		if _, err := conn.ExecContext(ctx, tb.ddl); err != nil {
			t.Fatalf("dump CREATE TABLE %s: %v", tb.name, err)
		}
		if err := server.convertTableToColumnstore(ctx, conn, schema, tb.name); err != nil {
			t.Fatalf("convert %s: %v", tb.name, err)
		}
		var engine string
		if err := db.Get(&engine, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?", schema, tb.name); err != nil || !strings.EqualFold(engine, "Columnstore") {
			// why it stayed: the generated DDL run again, its error shown
			cols, _, _ := dbhelper.GetTableColumns(db, nil, schema, tb.name)
			checks, _, _ := dbhelper.GetTableChecks(db, nil, schema, tb.name)
			opts, _, _ := dbhelper.GetTableOptions(db, nil, schema, tb.name)
			conv, cerr := columnstoreTableDDL(schema, tb.name+"__probe", cols, checks, opts, nil, nil)
			if cerr == nil {
				_, cerr = conn.ExecContext(ctx, conv.DDL)
			}
			t.Fatalf("%s is %q (%v), not ColumnStore: %v\n%s", tb.name, engine, err, cerr, conv.DDL)
		}
	}

	// rows as mariadb-dump writes them: every column positional, the generated one with its value
	rows := []string{
		"INSERT INTO `parent` VALUES (1,''),(2,'two')",
		"INSERT INTO `child` VALUES (1,1),(2,2),(3,99)",
		"INSERT INTO `src` VALUES (4294967295,-128,255,-32768,65535,-2147483648,4294967295,-9223372036854775808,18446744073709551615,-8388608," +
			"'blue-ish','a,ccc',b'10111',2155,'ab\\0\\0',X'00FF','11111111-2222-3333-4444-555555555555','2001:db8::1','10.0.0.1'," +
			"'é','', '2026-10-13 10:00:00','2026-10-13 10:00:00.123456',-1234567890123456789012345678.1234567890,-256,'{\"k\":1}')",
	}
	for _, q := range rows {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q[:40], err)
		}
	}
	var got []string
	if err := db.Select(&got, "SELECT CONCAT_WS('|', id, t, ut, si, usi, i, ui, b, ub, m, e, s, bt, y, u, i6, i4, v, IFNULL(nn,'NULL'), dt, d, g, j) FROM "+schema+".src"); err != nil {
		t.Fatal(err)
	}
	want := "4294967295|-128|255|-32768|65535|-2147483648|4294967295|-9223372036854775808|18446744073709551615|-8388608|blue-ish|a,ccc|23|2155|11111111-2222-3333-4444-555555555555|2001:db8::1|10.0.0.1|é|NULL|2026-10-13 10:00:00.123456|-1234567890123456789012345678.1234567890|-256|{\"k\":1}"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("src read back\n got %q\nwant %q", got, want)
	}
	// ColumnStore takes no function on a BLOB (HEX: "VARBINARY/BLOB in filter or function
	// is not supported"): the binary columns are compared raw
	var bin struct{ Bin, Vb []byte }
	if err := db.QueryRowx("SELECT bin, vb FROM "+schema+".src").Scan(&bin.Bin, &bin.Vb); err != nil {
		t.Fatal(err)
	}
	if string(bin.Bin) != "ab\x00\x00" || string(bin.Vb) != "\x00\xff" {
		t.Fatalf("binary columns read back %q %q", bin.Bin, bin.Vb)
	}
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM "+schema+".child"); err != nil || n != 3 {
		t.Fatalf("child: %d rows, %v (no foreign key any more: the orphan 99 loads)", n, err)
	}
}
