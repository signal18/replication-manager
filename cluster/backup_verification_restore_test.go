package cluster

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/backupmgr"
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
		// a complete insert whose column list is longer than 4 KiB
		{"INSERT INTO `t` (" + strings.Repeat("`a_long_column_name_0123456789`, ", 200) + "`id`) VALUES (1),(2)", 2},
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

// An external server receives a test restore when marked, and is marked on its first use
// when it holds no schema; one holding data, or monitored by a replication-manager (its
// replication_manager_schema has tables), is refused.
func TestEnsureVerificationMark(t *testing.T) {
	c, _ := newStagingTestServer(t)
	maria := &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	markCols := []string{"n"}
	schemaCols := []string{"SCHEMA_NAME"}
	objCols := []string{"kind", "name"}

	for _, tc := range []struct {
		name    string
		expect  func(sqlmock.Sqlmock)
		refused string
	}{
		{"marked", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(markCols).AddRow(1))
			m.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(sqlmock.NewRows(schemaCols).AddRow("app"))
		}, ""},
		{"empty, marked on first use", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(markCols).AddRow(0))
			m.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(sqlmock.NewRows(schemaCols).AddRow("replication_manager_schema"))
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(objCols))
			m.ExpectExec("CREATE DATABASE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec("REPLACE INTO").WithArgs(c.Name, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		}, ""},
		{"monitored by a replication-manager", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(markCols).AddRow(0))
			m.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(sqlmock.NewRows(schemaCols).AddRow("replication_manager_schema"))
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(objCols).AddRow("table", "jobs"))
		}, "replication_manager_schema"},
		{"holds data", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("information_schema.TABLES").WillReturnRows(sqlmock.NewRows(markCols).AddRow(0))
			m.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(sqlmock.NewRows(schemaCols).AddRow("app"))
		}, "app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tc.expect(mock)
			err = c.ensureVerificationMark(sqlx.NewDb(db, "sqlmock"), maria, "verify1:3306")
			if tc.refused == "" && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if tc.refused != "" && (err == nil || !strings.Contains(err.Error(), "not marked") || !strings.Contains(err.Error(), tc.refused)) {
				t.Fatalf("want refused for %s, got %v", tc.refused, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// On an external server, replication-manager's own schema of the source is not loaded:
// its CREATE DATABASE, USE and statements are skipped until the next schema's USE.
func TestVerificationLoadSkipsReplicationManagerSchema(t *testing.T) {
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
		ver: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, inv: newDumpInventory(), dropped: map[string]bool{},
		skipSchemas: backupVerificationSkippedSchemas}

	// nothing sent for the skipped schema, then app is dropped, created and used
	mock.ExpectExec("DROP DATABASE IF EXISTS `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE DATABASE `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("USE `app`").WillReturnResult(sqlmock.NewResult(0, 0))
	for _, stmt := range []string{
		"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `replication_manager_schema`",
		"USE `replication_manager_schema`",
		"DROP TABLE IF EXISTS `jobs`",
		"CREATE TABLE `jobs` (`id` int)",
		"CREATE DATABASE `app`",
		"USE `app`",
	} {
		if err := l.single(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if l.inv.objects["replication_manager_schema"] != nil || l.inSkipped {
		t.Fatalf("skipped schema recorded: %v, in skipped %v", l.inv.objects, l.inSkipped)
	}
}

// A splitdump file ends with the next schema's CREATE DATABASE and USE: a file of another
// schema ending with replication_manager_schema's does not skip the files after it.
func TestVerificationLoadSkipEndsWithItsFile(t *testing.T) {
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
		ver: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, inv: newDumpInventory(), dropped: map[string]bool{"app": true},
		skipSchemas: backupVerificationSkippedSchemas}

	if err := l.run(strings.NewReader("CREATE DATABASE /*!32312 IF NOT EXISTS*/ `replication_manager_schema`;\nUSE `replication_manager_schema`;\n"), "other"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("CREATE TABLE `t`").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := l.run(strings.NewReader("CREATE TABLE `t` (`id` int);\n"), "app"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the next file was skipped: %v", err)
	}
	if l.inv.objects["app"] == nil {
		t.Fatalf("table of app not recorded: %v", l.inv.objects)
	}
}

// The replica a test restore reseeds: taken out of service (reseed flag, maintenance,
// replication stopped), kept in maintenance after a failure of its load, released after a
// resume, and released with its replication restarted when it failed before its load.
func TestVerificationReplicaServiceStates(t *testing.T) {
	_, server := newStagingTestServer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}
	// maintenance membership is saved: a bare ConfigManager saves without I/O
	server.ClusterGroup.ConfigManager = &manager.ConfigManager{}
	server.SourceClusterName = server.ClusterGroup.Name
	cols := []string{"Connection_name", "Slave_IO_Running", "Slave_SQL_Running"}

	for _, tc := range []struct{ resumed, touched, released bool }{
		{resumed: false, touched: true, released: false},
		{resumed: true, touched: true, released: true},
		{resumed: false, touched: false, released: true},
	} {
		mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("", "No", "No"))
		setMaintenance, err := server.beginVerificationReseed()
		if err != nil || !setMaintenance || !server.IsMaintenance || server.IsReseeding != "verify" {
			t.Fatalf("begin: set %v, maintenance %v, reseeding %q, err %v", setMaintenance, server.IsMaintenance, server.IsReseeding, err)
		}
		if _, err := server.beginVerificationReseed(); err == nil {
			t.Fatal("a second test restore is refused while one runs")
		}
		if !tc.resumed && !tc.touched {
			mock.ExpectExec("START SLAVE").WillReturnResult(sqlmock.NewResult(0, 0))
		}
		server.endVerificationReseed(setMaintenance, tc.resumed, tc.touched)
		if server.IsReseeding != "" || server.IsMaintenance == tc.released {
			t.Fatalf("end %+v: maintenance %v, reseeding %q", tc, server.IsMaintenance, server.IsReseeding)
		}
		server.IsMaintenance = false
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A MariaDB backup position resumes when it is at or past the start of the primary's oldest
// binary log in each of its domains.
func TestMariadbGtidCovers(t *testing.T) {
	for _, tc := range []struct {
		pos, start, domain string
		ok                 bool
	}{
		{"0-1-100", "0-1-42", "", true},
		{"0-1-42", "0-1-42", "", true},
		{"0-1-41", "0-1-42", "0", false},
		{"0-1-100,1-2-5", "0-1-42,1-2-9", "1", false},
		{"0-1-100", "0-1-42,1-2-9", "1", false},
		{"0-1-100", "", "", true},
	} {
		domain, ok := mariadbGtidCovers(tc.pos, tc.start)
		if ok != tc.ok || domain != tc.domain {
			t.Errorf("mariadbGtidCovers(%q, %q) = %q, %v; want %q, %v", tc.pos, tc.start, domain, ok, tc.domain, tc.ok)
		}
	}
}

// An on-demand verification reading a scheduled backup holds off the next logical backup of
// its server (it rewrites the file) and the removal of its original; it waits itself while a
// logical backup runs.
func TestBackupVerificationReadExclusion(t *testing.T) {
	c, server := newStagingTestServer(t)
	meta := &backupmgr.BackupMetadata{Id: 7, Source: server.URL}
	if err := c.claimBackupVerificationRead(meta); err != nil {
		t.Fatalf("claim refused: %v", err)
	}
	started := make(chan struct{})
	go func() { server.startLogicalBackupAfterVerification(); close(started) }()
	removed := make(chan struct{})
	go func() { c.waitBackupVerificationRead(meta.Id); close(removed) }()
	select {
	case <-started:
		t.Fatal("the backup started while its previous file was read")
	case <-removed:
		t.Fatal("the original was removed while read")
	case <-time.After(1500 * time.Millisecond):
	}
	c.backupVerificationReads.Delete(meta.Id)
	for _, ch := range []chan struct{}{started, removed} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("still waiting once the read ended")
		}
	}
	if !c.InLogicalBackup {
		t.Fatal("logical backup state not set")
	}

	claimed := make(chan error)
	go func() { claimed <- c.claimBackupVerificationRead(meta) }()
	select {
	case <-claimed:
		t.Fatal("claimed while a logical backup runs")
	case <-time.After(1500 * time.Millisecond):
	}
	c.backupVerificationMu.Lock() // InLogicalBackup is a plain flag: the claim reads it under this lock
	c.SetInLogicalBackupState(false)
	c.backupVerificationMu.Unlock()
	select {
	case err := <-claimed:
		if err != nil {
			t.Fatal("claim refused")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("claim still waiting once the backup ended")
	}
}

// The claim of an on-demand verification and the start of a logical backup exclude each
// other and never wait on each other: whichever comes first runs, the other waits only until
// it ends (backupVerificationMu orders them on InLogicalBackup and the claims).
func TestBackupVerificationClaimAndBackupStartExclusive(t *testing.T) {
	c, server := newStagingTestServer(t)
	poll := backupVerificationPoll
	backupVerificationPoll = 10 * time.Millisecond
	defer func() { backupVerificationPoll = poll }()
	meta := &backupmgr.BackupMetadata{Id: 9, Source: server.URL}

	for i := 0; i < 20; i++ {
		claimed, started := make(chan error, 1), make(chan struct{})
		go func() { claimed <- c.claimBackupVerificationRead(meta) }()
		go func() { server.startLogicalBackupAfterVerification(); close(started) }()
		var first string
		select {
		case <-claimed:
			first = "claim"
		case <-started:
			first = "backup"
		case <-time.After(5 * time.Second):
			t.Fatalf("pass %d: neither the claim nor the backup start went on", i)
		}
		// the other one waits while the first holds
		other := map[string]<-chan struct{}{"claim": started}[first]
		if first == "backup" {
			ch := make(chan struct{})
			go func() { <-claimed; close(ch) }()
			other = ch
		}
		select {
		case <-other:
			t.Fatalf("pass %d: both went on (%s first)", i, first)
		case <-time.After(100 * time.Millisecond):
		}
		// the first ends: the other goes on
		c.backupVerificationMu.Lock()
		if first == "claim" {
			c.backupVerificationReads.Delete(meta.Id)
		} else {
			c.SetInLogicalBackupState(false)
		}
		c.backupVerificationMu.Unlock()
		select {
		case <-other:
		case <-time.After(5 * time.Second):
			t.Fatalf("pass %d: the %s ended, the other still waits", i, first)
		}
		c.backupVerificationMu.Lock()
		c.backupVerificationReads.Delete(meta.Id)
		c.SetInLogicalBackupState(false)
		c.backupVerificationMu.Unlock()
	}
}

// The waits between a backup and an on-demand verification end at their deadline
// (backup-verification-timeout, at least backupVerificationWaitFloor): a backup goes on, the
// archive removal goes on, a claim gives up and its verification is not run.
func TestBackupVerificationWaitsAreBounded(t *testing.T) {
	c, server := newStagingTestServer(t)
	poll, floor := backupVerificationPoll, backupVerificationWaitFloor
	backupVerificationPoll, backupVerificationWaitFloor = 50*time.Millisecond, time.Second
	defer func() { backupVerificationPoll, backupVerificationWaitFloor = poll, floor }()
	meta := &backupmgr.BackupMetadata{Id: 11, Source: server.URL}
	if err := c.claimBackupVerificationRead(meta); err != nil {
		t.Fatalf("claim refused: %v", err)
	}
	for name, wait := range map[string]func(){
		"backup start":    server.startLogicalBackupAfterVerification,
		"archive removal": func() { c.waitBackupVerificationRead(meta.Id) },
	} {
		start := time.Now()
		wait()
		if d := time.Since(start); d < 900*time.Millisecond || d > 5*time.Second {
			t.Fatalf("%s waited %s, want about 1s", name, d)
		}
	}
	if !c.InLogicalBackup {
		t.Fatal("the backup did not go on")
	}
	c.backupVerificationReads.Delete(meta.Id)
	start := time.Now()
	if err := c.claimBackupVerificationRead(meta); !errors.Is(err, errBackupVerificationClaimTimeout) {
		t.Fatalf("claimed while a logical backup runs: %v", err)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 5*time.Second {
		t.Fatalf("the claim waited %s, want about 1s", d)
	}
	// the bound: backup-verification-timeout, never under the floor
	for timeout, want := range map[int]time.Duration{0: time.Second, 1: time.Second, 3: 3 * time.Second} {
		c.Conf.BackupVerificationTimeout = timeout
		if got := c.backupVerificationWaitMax(); got != want {
			t.Fatalf("timeout %d: bound %s, want %s", timeout, got, want)
		}
	}
}

// A verification closed as interrupted from a meta file read while it was running keeps the
// result it recorded since: the meta file is read again before it is closed.
func TestCloseInterruptedVerificationKeepsALaterResult(t *testing.T) {
	c, server := newStagingTestServer(t)
	c.Servers = append(c.Servers, server)
	meta := &backupmgr.BackupMetadata{Id: 21, BackupTool: "mysqldump", BackupLine: backupmgr.BackupLineAdhoc, Source: server.URL,
		Dest: filepath.Join(server.GetMyBackupDirectory(), "mysqldump.21.sql.gz")}
	if err := os.MkdirAll(server.GetMyBackupDirectory(), 0755); err != nil {
		t.Fatal(err)
	}
	stale := *meta
	stale.Verification = &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationRunning}
	// the run ended: its result is in the meta file, it left backupVerifying
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed})
	server.closeInterruptedVerification(&stale)
	onDisk, err := readBackupMetadataFile(server.backupMetaFilePath(meta))
	if err != nil || onDisk.Verification == nil || onDisk.Verification.Status != backupmgr.BackupVerificationPassed {
		t.Fatalf("the passed result was overwritten: %+v, %v", onDisk.Verification, err)
	}
	// the stale copy, which its caller registers, takes the recorded result
	if stale.Verification == nil || stale.Verification.Status != backupmgr.BackupVerificationPassed {
		t.Fatalf("the stale copy still shows %+v", stale.Verification)
	}
	stale.Verification = &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationRunning}
	// the meta file still says running: closed
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationRunning})
	server.closeInterruptedVerification(&stale)
	if onDisk, _ = readBackupMetadataFile(server.backupMetaFilePath(meta)); onDisk.Verification.Status != backupmgr.BackupVerificationFailed {
		t.Fatalf("an interrupted verification was not closed: %+v", onDisk.Verification)
	}
}
