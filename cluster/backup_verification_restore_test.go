package cluster

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

func TestCountInsertRows(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want int64
	}{
		{"INSERT INTO `t` VALUES (1,'a'),(2,'b'),(3,'c')", 3},
		{"INSERT INTO `t` VALUES (1,'a),(b'),(2,'it''s'),(3,'back\\'slash),(')", 3},
		{"INSERT INTO `t` VALUES (1,\"x),(y\"),(2,NULL)", 2},
		{"INSERT INTO `t` VALUES (1,0x2829),(2,POINT(1,2))", 2},
		{"INSERT INTO `t` (`a`, `b`) VALUES (1,2)", 1},
		{"REPLACE INTO `innodb_index_stats` VALUES ('db','t','PRIMARY','2026-10-14 10:00:00','n_diff_pfx01',1,1,'id')", 1},
		{"INSERT INTO `t` SELECT 1", 0},
	} {
		if got := countInsertRows(tc.stmt); got != tc.want {
			t.Errorf("countInsertRows(%q) = %d, want %d", tc.stmt, got, tc.want)
		}
	}
}

// The statements of a dump (mysqldump and mariadb-dump forms) record their schema and
// objects; replication statements are recognized whatever their spelling.
func TestDumpInventoryRecord(t *testing.T) {
	l := &backupVerificationLoad{inv: newDumpInventory()}
	for _, stmt := range []string{
		"USE `app`",
		"CREATE TABLE `t` (\n  `id` int NOT NULL,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB",
		// MySQL 8 placeholder, then the view
		"/*!50001 CREATE VIEW `v` AS SELECT \n 1 AS `id`*/",
		"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013 DEFINER=`root`@`%` SQL SECURITY DEFINER */\n/*!50001 VIEW `v` AS select `t`.`id` AS `id` from `t` */",
		// MariaDB before 10.6: a placeholder table for the view
		"/*!50001 CREATE TABLE `v2` (\n  `id` tinyint NOT NULL\n) ENGINE=MyISAM */",
		"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013 DEFINER=`root`@`%` SQL SECURITY DEFINER */\n/*!50001 VIEW `v2` AS select 1 AS `id` */",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER `trg` BEFORE INSERT ON `t` FOR EACH ROW SET NEW.id = NEW.id */",
		"CREATE DEFINER=`root`@`%` PROCEDURE `p`()\nBEGIN\n  SELECT 1;\nEND",
		"CREATE DEFINER=`root`@`%` FUNCTION `f`() RETURNS int(11)\n    DETERMINISTIC\nRETURN 1",
		"/*!50106 CREATE*/ /*!50117 DEFINER=`root`@`%`*/ /*!50106 EVENT `e` ON SCHEDULE EVERY 1 DAY STARTS '2026-10-14 00:00:00' ON COMPLETION NOT PRESERVE ENABLE DO SELECT 1 */",
		"CREATE TABLE `odd``name` (`event` int, `trigger` int)",
		// MariaDB keeps a trigger's text as it was created: plain and qualified names
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER app.trg_plain BEFORE INSERT ON app.t FOR EACH ROW SET NEW.id = NEW.id */",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER `app`.`trg_quoted` BEFORE UPDATE ON `t` FOR EACH ROW SET NEW.id = NEW.id */",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER other.trg_other BEFORE DELETE ON other.t FOR EACH ROW SET @x = 1 */",
		"CREATE DEFINER=`root`@`%` PROCEDURE p_plain()\nBEGIN\n  SELECT 1;\nEND",
	} {
		l.record(normalizeDumpStatement(stmt))
	}
	l.recordInsert("INSERT INTO `t` VALUES (1),(2)")
	l.recordInsert("INSERT INTO `t` VALUES (3)")

	want := map[string]map[string]bool{
		"table":     {"t": true, "v2": true, "odd`name": true},
		"view":      {"v": true, "v2": true},
		"trigger":   {"trg": true, "trg_plain": true, "trg_quoted": true},
		"procedure": {"p": true, "p_plain": true},
		"function":  {"f": true},
		"event":     {"e": true},
	}
	if !reflect.DeepEqual(l.inv.objects["app"], want) {
		t.Fatalf("objects %v, want %v", l.inv.objects["app"], want)
	}
	if !l.inv.objects["other"]["trigger"]["trg_other"] {
		t.Fatalf("a trigger qualified with another schema is not recorded there: %v", l.inv.objects["other"])
	}
	if l.inv.rows["app"]["t"] != 3 || l.inv.rows["app"]["odd`name"] != 0 {
		t.Fatalf("rows %v", l.inv.rows["app"])
	}

	for _, stmt := range []string{
		"CHANGE MASTER TO MASTER_LOG_FILE='bin.000003', MASTER_LOG_POS=1234",
		"CHANGE MASTER '' TO MASTER_LOG_FILE='bin.000003', MASTER_LOG_POS=1234",
		"CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='bin.000003', SOURCE_LOG_POS=1234",
		"START SLAVE", "STOP SLAVE", "START REPLICA", "STOP ALL SLAVES", "START ALL SLAVES",
		"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ 'a5ce0000-0000-4000-8000-00000000b005:1-2'",
		"SET GLOBAL gtid_slave_pos='0-1-100'",
	} {
		if _, upper := normalizeDumpStatement(stmt); !stmtReplication.MatchString(upper) {
			t.Errorf("replication statement not recognized: %s", stmt)
		}
	}
	for _, stmt := range []string{
		"SET @@SESSION.SQL_LOG_BIN= 0",
		"INSERT INTO `t` VALUES ('START SLAVE')",
		"CREATE TABLE `slave_master_info` (`id` int)",
	} {
		if _, upper := normalizeDumpStatement(stmt); stmtReplication.MatchString(upper) {
			t.Errorf("not a replication statement: %s", stmt)
		}
	}
	if !strings.HasPrefix(dumpStatementSummary("/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER `trg` */"), "CREATE DEFINER") {
		t.Fatal("summary not normalized")
	}
	if got := dumpStatementSummary("CHANGE MASTER TO MASTER_HOST='db1', MASTER_PASSWORD='s3cret'"); strings.Contains(got, "s3cret") {
		t.Fatalf("summary shows the password: %s", got)
	}
}

// The backup is compared with its source by kind of object: a kind the source has and the
// backup none of is a warning (events left out of a dump); an object of a kind the backup
// has is never one (created since the backup); a source that can not be read is a note.
func TestCompareSource(t *testing.T) {
	_, server := newStagingTestServer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}

	l := &backupVerificationLoad{server: server, inv: newDumpInventory()}
	l.inv.addSchema("mysql")
	l.inv.addObject("app", dbhelper.SchemaObjectTable, "t")
	l.inv.addObject("app", dbhelper.SchemaObjectProcedure, "p")
	mock.ExpectQuery("information_schema.TABLES").WithArgs("app", "app", "app", "app").WillReturnRows(sqlmock.NewRows([]string{"kind", "name"}).
		AddRow("table", "t").AddRow("procedure", "p").AddRow("procedure", "p2").AddRow("event", "e1").AddRow("event", "e2"))

	warnings, note := l.compareSource()
	if note != "" || !reflect.DeepEqual(warnings, []string{"app: the source has 2 event(s), the backup none"}) {
		t.Fatalf("warnings %q note %q", warnings, note)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	server.State = stateFailed
	if warnings, note := l.compareSource(); warnings != nil || !strings.Contains(note, "the server is down") {
		t.Fatalf("down source: warnings %q note %q", warnings, note)
	}
}

// A dump without CREATE DATABASE (--no-create-db): its schema is cleared and created at its
// first USE, once; a schema created by the dump is not dropped again at its USE.
func TestVerificationLoadUseWithoutCreateDatabase(t *testing.T) {
	_, server := newStagingTestServer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sdb := sqlx.NewDb(db, "sqlmock")
	conn, err := sdb.Connx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	l := &backupVerificationLoad{server: server, ctx: context.Background(), conn: conn, db: sdb,
		ver: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, inv: newDumpInventory(), dropped: map[string]bool{}}

	mock.ExpectExec("DROP DATABASE IF EXISTS `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE DATABASE IF NOT EXISTS `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("USE `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("USE `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP DATABASE IF EXISTS `other`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE DATABASE `other`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("USE `other`").WillReturnResult(sqlmock.NewResult(0, 0))
	for _, stmt := range []string{"USE `app`", "USE `app`", "CREATE DATABASE `other`", "USE `other`"} {
		if err := l.single(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if !l.dropped["app"] || !l.dropped["other"] || l.schema != "other" {
		t.Fatalf("dropped %v schema %q", l.dropped, l.schema)
	}
}

// The event scheduler of the server a test restore loads into is switched off for it and
// back on after; an OFF one is left alone. On a replica, the events enabled after the load
// are disabled on the replicas, the others left.
func TestVerificationEventHandling(t *testing.T) {
	_, server := newStagingTestServer(t)
	maria := &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery("@@GLOBAL.event_scheduler").WillReturnRows(sqlmock.NewRows([]string{"s"}).AddRow("ON"))
	mock.ExpectExec("SET GLOBAL event_scheduler=0").WillReturnResult(sqlmock.NewResult(0, 0))
	resume, err := server.pauseEventScheduler(sdb, maria, "verify1:3306")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("SET GLOBAL event_scheduler=1").WillReturnResult(sqlmock.NewResult(0, 0))
	resume()

	mock.ExpectQuery("@@GLOBAL.event_scheduler").WillReturnRows(sqlmock.NewRows([]string{"s"}).AddRow("OFF"))
	resume, err = server.pauseEventScheduler(sdb, maria, "verify1:3306")
	if err != nil {
		t.Fatal(err)
	}
	resume() // nothing sent

	server.Conn = sdb
	server.DBVersion = maria
	mock.ExpectQuery("FROM mysql.event").WillReturnRows(sqlmock.NewRows([]string{"Db", "Name", "Definer", "Status"}).
		AddRow("app", "on_event", "root@%", 1).AddRow("app", "off_event", "root@%", 2).AddRow("app", "replica_event", "root@%", 3))
	mock.ExpectExec("ALTER /\\*replication-manager\\*/ DEFINER=`root`@`%` EVENT `app`.`on_event` DISABLE ON SLAVE").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := server.disableVerificationEvents(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The replication of the replica a test restore reseeds is read from the server, not from
// the monitor's cache: a channel still running is stopped and read again; one that keeps
// running refuses the load.
func TestStopLiveReplication(t *testing.T) {
	_, server := newStagingTestServer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	cols := []string{"Connection_name", "Slave_IO_Running", "Slave_SQL_Running"}

	mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("", "Yes", "Yes"))
	mock.ExpectExec("STOP SLAVE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("", "No", "No"))
	if err := server.stopLiveReplication(); err != nil {
		t.Fatalf("stopped channel: %v", err)
	}

	mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("", "Connecting", "Yes"))
	mock.ExpectExec("STOP SLAVE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("", "Connecting", "Yes"))
	if err := server.stopLiveReplication(); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("running channel: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
