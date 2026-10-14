package cluster

import (
	"strings"
	"testing"
)

// TestForEachSplitdumpStatement is the parser-only segmentation test: DELIMITER
// handling (trigger body), standalone-comment skipping, terminator stripping, and a
// trailing statement with no terminator — no live DB needed.
func TestForEachSplitdumpStatement(t *testing.T) {
	input := strings.Join([]string{
		"-- MySQL dump header comment",
		"/*!40101 SET NAMES utf8 */;",
		"# hash comment",
		"LOCK TABLES `t` WRITE;",
		"/*!40000 ALTER TABLE `t` DISABLE KEYS */;",
		"INSERT INTO `t` VALUES (1,'a;b'),(2,'c');", // embedded ';' inside a value
		"INSERT INTO `t` VALUES (3,'d');",
		"/*!40000 ALTER TABLE `t` ENABLE KEYS */;",
		"UNLOCK TABLES;",
		"DELIMITER ;;",
		"CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW BEGIN",
		"  SET @x = 1;", // internal ';' must NOT end the statement (delimiter is ;;)
		"END ;;",
		"DELIMITER ;",
		"INSERT INTO `t` VALUES (4,'e')", // trailing, no terminator
	}, "\n") + "\n"

	var got []string
	if err := forEachSplitdumpStatement(strings.NewReader(input), func(s string) error {
		got = append(got, s)
		return nil
	}); err != nil {
		t.Fatalf("forEachSplitdumpStatement: %v", err)
	}

	want := []string{
		"/*!40101 SET NAMES utf8 */",
		"LOCK TABLES `t` WRITE",
		"/*!40000 ALTER TABLE `t` DISABLE KEYS */",
		"INSERT INTO `t` VALUES (1,'a;b'),(2,'c')",
		"INSERT INTO `t` VALUES (3,'d')",
		"/*!40000 ALTER TABLE `t` ENABLE KEYS */",
		"UNLOCK TABLES",
		"CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW BEGIN\n  SET @x = 1;\nEND",
		"INSERT INTO `t` VALUES (4,'e')",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d statements, want %d:\n%#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stmt %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

func TestClassifySplitdumpStatement(t *testing.T) {
	cases := []struct {
		stmt string
		want splitdumpStmtKind
	}{
		{"LOCK TABLES `t` WRITE", splitdumpStmtSkip},
		{"UNLOCK TABLES", splitdumpStmtSkip},
		{"/*!40000 ALTER TABLE `t` DISABLE KEYS */", splitdumpStmtSkip}, // wrapped in /*! */ → Contains, not prefix
		{"/*!40000 ALTER TABLE `t` ENABLE KEYS */", splitdumpStmtSkip},
		{"INSERT INTO `t` VALUES (1)", splitdumpStmtInsert},
		{"insert into t values (1)", splitdumpStmtInsert}, // case-insensitive
		{"REPLACE INTO `t` VALUES (1)", splitdumpStmtInsert},
		{"CREATE TABLE `t` (id int)", splitdumpStmtOther},
		{"/*!40101 SET NAMES utf8 */", splitdumpStmtOther},
		{"USE `db`", splitdumpStmtOther},
		// the dumps' footer statements (lab: MySQL 8.0, 8.4, Percona 8.0, 8.4, MariaDB clients), skipped
		{"START SLAVE", splitdumpStmtSkip},
		{"START REPLICA", splitdumpStmtSkip},
		{"SET @@SESSION.SQL_LOG_BIN = @MYSQLDUMP_TEMP_LOG_BIN", splitdumpStmtSkip},
		{"/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */", splitdumpStmtSkip},
		{"/*!50606 SET GLOBAL INNODB_STATS_AUTO_RECALC=@OLD_INNODB_STATS_AUTO_RECALC */", splitdumpStmtSkip},
		{"/*!40101 SET SQL_MODE=@OLD_SQL_MODE */", splitdumpStmtSkip},
		{"/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */", splitdumpStmtSkip},
		{"/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */", splitdumpStmtSkip},
		{"/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */", splitdumpStmtSkip},
		{"/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */", splitdumpStmtSkip},
		{"/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */", splitdumpStmtSkip},
		{"/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */", splitdumpStmtSkip},
		{"/*M!100616 SET NOTE_VERBOSITY=@OLD_NOTE_VERBOSITY */", splitdumpStmtSkip},
		// run: per-table and header settings, Percona's RocksDB block, data and routines with the same text
		{"/*!40101 SET @saved_cs_client     = @@character_set_client */", splitdumpStmtOther},
		{"/*!40101 SET character_set_client = @saved_cs_client */", splitdumpStmtOther},
		{"/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */", splitdumpStmtOther},
		{"/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */", splitdumpStmtOther},
		{"/*!40103 SET TIME_ZONE='+00:00' */", splitdumpStmtOther},
		{"/*!50112 SET @disable_bulk_load = IF (@is_rocksdb_supported, 'SET SESSION rocksdb_bulk_load = @old_rocksdb_bulk_load', 'SET @dummy_rocksdb_bulk_load = 0') */", splitdumpStmtOther},
		{"/*!50112 EXECUTE s */", splitdumpStmtOther},
		{"STOP SLAVE", splitdumpStmtOther}, // written before the first table only, never in a file
		{"START SLAVE IO_THREAD", splitdumpStmtOther},
		{"SET @@GLOBAL.GTID_PURGED=@x", splitdumpStmtOther},
		{"INSERT INTO `t` VALUES (1,'START SLAVE')", splitdumpStmtInsert},
		{"INSERT INTO `t` VALUES (1,'/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */')", splitdumpStmtInsert},
		{"CREATE PROCEDURE `p`() BEGIN START SLAVE; END", splitdumpStmtOther},
		{"CREATE PROCEDURE `p`() BEGIN SET TIME_ZONE=@OLD_TIME_ZONE; END", splitdumpStmtOther},
	}
	for _, c := range cases {
		if got := classifySplitdumpStatement(c.stmt); got != c.want {
			t.Errorf("classifySplitdumpStatement(%q) = %d, want %d", c.stmt, got, c.want)
		}
	}
}

// TestPlanAndExecSplitdumpBatchesInserts verifies INSERT/REPLACE statements are
// always batched into a single transaction (mysql.system-all's INSTALL PLUGIN
// skip is a live, per-statement dbhelper lookup inside execSplitdumpSingle, not
// a continue-on-error mode of planAndExecSplitdump — there is no other path that
// runs INSERTs individually).
func TestPlanAndExecSplitdumpBatchesInserts(t *testing.T) {
	input := "INSERT INTO `mysql`.`user` VALUES (1);\nINSERT INTO `mysql`.`user` VALUES (2);\n"

	var batches [][]string
	var singles []string
	rec := splitdumpExecutor{
		batch:  func(s []string) error { batches = append(batches, append([]string(nil), s...)); return nil },
		single: func(s string) error { singles = append(singles, s); return nil },
	}
	if err := planAndExecSplitdump(strings.NewReader(input), 500, rec); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Errorf("expected 1 batch of 2 INSERTs, got %v", batches)
	}
	if len(singles) != 0 {
		t.Errorf("expected no individual execs, got %v", singles)
	}
}

// TestPlanAndExecSplitdumpDropsAndFlushes verifies LOCK/UNLOCK/DISABLE-KEYS are
// dropped (never executed) and a non-INSERT statement flushes the pending INSERT
// batch before running.
func TestPlanAndExecSplitdumpDropsAndFlushes(t *testing.T) {
	input := strings.Join([]string{
		"LOCK TABLES `t` WRITE;",
		"/*!40000 ALTER TABLE `t` DISABLE KEYS */;",
		"INSERT INTO `t` VALUES (1);",
		"INSERT INTO `t` VALUES (2);",
		"/*!40101 SET NAMES utf8 */;", // non-insert → flush [1,2], then single
		"INSERT INTO `t` VALUES (3);",
		"/*!40000 ALTER TABLE `t` ENABLE KEYS */;",
		"UNLOCK TABLES;",
	}, "\n") + "\n"

	var batches [][]string
	var singles []string
	rec := splitdumpExecutor{
		batch:  func(s []string) error { batches = append(batches, append([]string(nil), s...)); return nil },
		single: func(s string) error { singles = append(singles, s); return nil },
	}
	if err := planAndExecSplitdump(strings.NewReader(input), 500, rec); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Errorf("expected batches [[1,2],[3]], got %v", batches)
	}
	if len(singles) != 1 || !strings.Contains(singles[0], "SET NAMES") {
		t.Errorf("expected the SET NAMES as the only single exec, got %v", singles)
	}
	for _, b := range batches {
		for _, s := range b {
			if !strings.HasPrefix(s, "INSERT") {
				t.Errorf("non-INSERT leaked into a batch: %q", s)
			}
		}
	}
}
