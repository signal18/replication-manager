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
	}
	for _, c := range cases {
		if got := classifySplitdumpStatement(c.stmt); got != c.want {
			t.Errorf("classifySplitdumpStatement(%q) = %d, want %d", c.stmt, got, c.want)
		}
	}
}

// TestPlanAndExecSplitdumpContinueOnError is the regression for the review gap:
// mysql.system-all (continueOnError) INSERTs must run INDIVIDUALLY so one conflicting
// seed row is skipped like the old --force, not batched into a transaction that
// aborts the whole restore on the first conflict.
func TestPlanAndExecSplitdumpContinueOnError(t *testing.T) {
	input := "INSERT INTO `mysql`.`user` VALUES (1);\nINSERT INTO `mysql`.`user` VALUES (2);\n"

	// continueOnError=true → each INSERT via single(coe=true), NO batch.
	var batches [][]string
	var singles []string
	rec := splitdumpExecutor{
		batch: func(s []string) error { batches = append(batches, append([]string(nil), s...)); return nil },
		single: func(s string, coe bool) error {
			if !coe {
				t.Errorf("continueOnError: single(%q) got coe=false, want true", s)
			}
			singles = append(singles, s)
			return nil
		},
	}
	if err := planAndExecSplitdump(strings.NewReader(input), true, 500, rec); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 0 {
		t.Errorf("continueOnError: expected NO batched transaction, got %v", batches)
	}
	if len(singles) != 2 {
		t.Errorf("continueOnError: expected 2 individual execs, got %d", len(singles))
	}

	// continueOnError=false → INSERTs batched into one transaction.
	batches = nil
	singles = nil
	rec2 := splitdumpExecutor{
		batch:  func(s []string) error { batches = append(batches, append([]string(nil), s...)); return nil },
		single: func(s string, coe bool) error { singles = append(singles, s); return nil },
	}
	if err := planAndExecSplitdump(strings.NewReader(input), false, 500, rec2); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Errorf("normal: expected 1 batch of 2 INSERTs, got %v", batches)
	}
	if len(singles) != 0 {
		t.Errorf("normal: expected no individual execs, got %v", singles)
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
		single: func(s string, coe bool) error { singles = append(singles, s); return nil },
	}
	if err := planAndExecSplitdump(strings.NewReader(input), false, 500, rec); err != nil {
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
