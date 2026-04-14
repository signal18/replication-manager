package splitdump

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeSQLGzip creates a gzip-compressed SQL file at dir/name with the given SQL content.
func writeSQLGzip(t *testing.T, dir, name, sql string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("writeSQLGzip: create %s: %v", name, err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(sql)); err != nil {
		t.Fatalf("writeSQLGzip: write %s: %v", name, err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("writeSQLGzip: close gzip %s: %v", name, err)
	}
	return path
}

// writeSQLPlain creates a plain (non-compressed) SQL file at dir/name.
func writeSQLPlain(t *testing.T, dir, name, sql string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(sql), 0644); err != nil {
		t.Fatalf("writeSQLPlain: write %s: %v", name, err)
	}
	return path
}

// ---- splitSchemaBuckets ----

func TestSplitSchemaBuckets(t *testing.T) {
	paths := []string{
		"db.tbl-schema.sql.gz",
		"mysql.system-all.sql.gz",
		"db.__routines-schema-routine.sql.gz",
		"db.__funcs-schema-function.sql.gz",
		"db.__procs-schema-procedure.sql",
		"db.v_tbl-schema-view.sql.gz",
		"db.other-schema.sql",
	}
	tables, systemAll, routines, views := splitSchemaBuckets(paths)

	wantTables := []string{"db.tbl-schema.sql.gz", "db.other-schema.sql"}
	if !reflect.DeepEqual(tables, wantTables) {
		t.Fatalf("tableSchemas = %v, want %v", tables, wantTables)
	}
	if !reflect.DeepEqual(systemAll, []string{"mysql.system-all.sql.gz"}) {
		t.Fatalf("mysqlSystemAll = %v", systemAll)
	}
	wantRoutines := []string{
		"db.__routines-schema-routine.sql.gz",
		"db.__funcs-schema-function.sql.gz",
		"db.__procs-schema-procedure.sql",
	}
	if !reflect.DeepEqual(routines, wantRoutines) {
		t.Fatalf("routineSchemas = %v, want %v", routines, wantRoutines)
	}
	if !reflect.DeepEqual(views, []string{"db.v_tbl-schema-view.sql.gz"}) {
		t.Fatalf("viewSchemas = %v", views)
	}
}

// ---- parseReferencedTables ----

func TestParseReferencedTablesBacktickImplicit(t *testing.T) {
	dir := t.TempDir()
	sql := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk1` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "db.child-schema.sql.gz", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "db.parent" {
		t.Fatalf("expected [db.parent], got %v", refs)
	}
}

func TestParseReferencedTablesBacktickExplicit(t *testing.T) {
	dir := t.TempDir()
	sql := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk1` FOREIGN KEY (`pid`) REFERENCES `mydb`.`parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "mydb.child-schema.sql.gz", sql)

	refs, err := parseReferencedTables(path, "mydb")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "mydb.parent" {
		t.Fatalf("expected [mydb.parent], got %v", refs)
	}
}

func TestParseReferencedTablesUnquoted(t *testing.T) {
	dir := t.TempDir()
	// Some MySQL versions and tools emit unquoted identifiers.
	sql := "CREATE TABLE child (\n" +
		"  id INT,\n" +
		"  pid INT,\n" +
		"  CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES parent (id)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLPlain(t, dir, "db.child-schema.sql", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "db.parent" {
		t.Fatalf("expected [db.parent], got %v", refs)
	}
}

func TestParseReferencedTablesUnquotedExplicitSchema(t *testing.T) {
	dir := t.TempDir()
	sql := "CREATE TABLE child (\n" +
		"  id INT,\n" +
		"  CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES other_db.parent (id)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLPlain(t, dir, "db.child-schema.sql", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "other_db.parent" {
		t.Fatalf("expected [other_db.parent], got %v", refs)
	}
}

func TestParseReferencedTablesMissingFile(t *testing.T) {
	refs, err := parseReferencedTables("/no/such/path/db.tbl-schema.sql.gz", "db")
	if refs != nil {
		t.Fatalf("expected nil for missing file, got %v", refs)
	}
	if err != nil {
		t.Fatalf("expected nil error for missing file (treated as dependency-free), got %v", err)
	}
}

func TestParseReferencedTablesMultipleRefs(t *testing.T) {
	dir := t.TempDir()
	sql := "CREATE TABLE `order_item` (\n" +
		"  `id` INT,\n" +
		"  `order_id` INT,\n" +
		"  `product_id` INT,\n" +
		"  CONSTRAINT `fk_order` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`),\n" +
		"  CONSTRAINT `fk_product` FOREIGN KEY (`product_id`) REFERENCES `products` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "db.order_item-schema.sql.gz", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %v", refs)
	}
	refSet := map[string]bool{}
	for _, r := range refs {
		refSet[r] = true
	}
	if !refSet["db.orders"] || !refSet["db.products"] {
		t.Fatalf("expected db.orders and db.products, got %v", refs)
	}
}

func TestParseReferencedTablesDeduplicated(t *testing.T) {
	dir := t.TempDir()
	// Same table referenced twice (composite FK or two separate columns).
	sql := "CREATE TABLE `t` (\n" +
		"  `a` INT,\n" +
		"  `b` INT,\n" +
		"  CONSTRAINT `fk1` FOREIGN KEY (`a`) REFERENCES `parent` (`x`),\n" +
		"  CONSTRAINT `fk2` FOREIGN KEY (`b`) REFERENCES `parent` (`y`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "db.t-schema.sql.gz", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected scan error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "db.parent" {
		t.Fatalf("expected single deduplicated [db.parent], got %v", refs)
	}
}

// TestParseReferencedTablesLineTooLong verifies that a file with a comment line
// exceeding the 4 MiB scanner token limit is handled gracefully via the large-parser
// retry: no error is returned and refs is empty (comment contains no REFERENCES).
func TestParseReferencedTablesLineTooLong(t *testing.T) {
	dir := t.TempDir()
	// Build a SQL file where a single comment line is 5 MiB.
	// The scanner fails with bufio.ErrTooLong; the large parser retries via
	// ReadString(';') and succeeds (no REFERENCES in the comment → nil refs).
	const lineSize = 5 * 1024 * 1024
	giant := "-- " + strings.Repeat("x", lineSize) + "\n"
	path := writeSQLPlain(t, dir, "db.huge-schema.sql", giant)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("expected successful retry for oversized comment, got error: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no refs from a comment-only file, got %v", refs)
	}
}

// TestParseReferencedTablesOversizedPlain verifies that a plain SQL file where the
// entire CREATE TABLE statement is a single >4 MiB line is still parsed correctly via
// the large-parser retry path.
func TestParseReferencedTablesOversizedPlain(t *testing.T) {
	dir := t.TempDir()
	// Embed a valid REFERENCES clause inside a statement padded to >4 MiB.
	// The scanner will fail on this line; the large parser retries via ReadString(';').
	const padSize = 5 * 1024 * 1024
	sql := "CREATE TABLE `child` (`id` INT, `pid` INT, CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`) -- " +
		strings.Repeat("x", padSize) +
		") ENGINE=InnoDB;"
	path := writeSQLPlain(t, dir, "db.child-schema.sql", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected error from large-parser retry: %v", err)
	}
	if len(refs) != 1 || refs[0] != "db.parent" {
		t.Fatalf("expected [db.parent] from oversized plain DDL, got %v", refs)
	}
}

// TestParseReferencedTablesOversizedGzip verifies the large-parser retry path for a
// gzip-compressed file whose content contains a single >4 MiB DDL statement.
func TestParseReferencedTablesOversizedGzip(t *testing.T) {
	dir := t.TempDir()
	const padSize = 5 * 1024 * 1024
	sql := "CREATE TABLE `child` (`id` INT, `pid` INT, CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `myparent` (`id`) -- " +
		strings.Repeat("z", padSize) +
		") ENGINE=InnoDB;"
	path := writeSQLGzip(t, dir, "db.child-schema.sql.gz", sql)

	refs, err := parseReferencedTables(path, "db")
	if err != nil {
		t.Fatalf("unexpected error from large-parser retry (gzip): %v", err)
	}
	if len(refs) != 1 || refs[0] != "db.myparent" {
		t.Fatalf("expected [db.myparent] from oversized gzip DDL, got %v", refs)
	}
}

// TestOrderTableSchemasByFKOversizedChildDDL verifies end-to-end that FK ordering
// works correctly when one of the schema files has an oversized DDL statement that
// requires the large-parser retry path to extract its REFERENCES clause.
func TestOrderTableSchemasByFKOversizedChildDDL(t *testing.T) {
	dir := t.TempDir()

	// achild's CREATE TABLE is a single >4 MiB line — scanner will fail and retry.
	const padSize = 5 * 1024 * 1024
	childSQL := "CREATE TABLE `achild` (`id` INT, `pid` INT, CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `zparent` (`id`) -- " +
		strings.Repeat("p", padSize) +
		") ENGINE=InnoDB;"
	parentSQL := "CREATE TABLE `zparent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	childPath := writeSQLGzip(t, dir, "db.achild-schema.sql.gz", childSQL)
	parentPath := writeSQLGzip(t, dir, "db.zparent-schema.sql.gz", parentSQL)

	// Input in alphabetical (wrong) order.
	result := orderTableSchemasByForeignKeys([]string{childPath, parentPath}, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if filepath.Base(result[0]) != "db.zparent-schema.sql.gz" {
		t.Fatalf("expected zparent first (FK ordering via large-parser retry), got: %s", filepath.Base(result[0]))
	}
	if filepath.Base(result[1]) != "db.achild-schema.sql.gz" {
		t.Fatalf("expected achild second, got: %s", filepath.Base(result[1]))
	}
}

// ---- orderTableSchemasByForeignKeys ----

func TestOrderTableSchemasByFKParentBeforeChild(t *testing.T) {
	dir := t.TempDir()

	// achild sorts before zparent alphabetically, but achild depends on zparent.
	childSQL := "CREATE TABLE `achild` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `zparent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `zparent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	childPath := writeSQLGzip(t, dir, "db.achild-schema.sql.gz", childSQL)
	parentPath := writeSQLGzip(t, dir, "db.zparent-schema.sql.gz", parentSQL)

	// Input in alphabetical (wrong) order.
	paths := []string{childPath, parentPath}
	result := orderTableSchemasByForeignKeys(paths, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if filepath.Base(result[0]) != "db.zparent-schema.sql.gz" {
		t.Fatalf("expected zparent first (FK ordering), got: %s", filepath.Base(result[0]))
	}
	if filepath.Base(result[1]) != "db.achild-schema.sql.gz" {
		t.Fatalf("expected achild second, got: %s", filepath.Base(result[1]))
	}
}

func TestOrderTableSchemasByFKSameSchemaImplicit(t *testing.T) {
	dir := t.TempDir()

	// Reference using just backtick-quoted table name (no schema qualifier).
	childSQL := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `parent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	childPath := writeSQLGzip(t, dir, "app.child-schema.sql.gz", childSQL)
	parentPath := writeSQLGzip(t, dir, "app.parent-schema.sql.gz", parentSQL)

	// Input: child before parent (wrong).
	result := orderTableSchemasByForeignKeys([]string{childPath, parentPath}, nil)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if filepath.Base(result[0]) != "app.parent-schema.sql.gz" {
		t.Fatalf("expected parent first, got: %s", filepath.Base(result[0]))
	}
}

func TestOrderTableSchemasByFKExplicitSchemaRef(t *testing.T) {
	dir := t.TempDir()

	// Reference using `schema`.`table` explicit form.
	childSQL := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `app`.`parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `parent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	childPath := writeSQLGzip(t, dir, "app.child-schema.sql.gz", childSQL)
	parentPath := writeSQLGzip(t, dir, "app.parent-schema.sql.gz", parentSQL)

	result := orderTableSchemasByForeignKeys([]string{childPath, parentPath}, nil)

	if filepath.Base(result[0]) != "app.parent-schema.sql.gz" {
		t.Fatalf("expected parent first via explicit schema ref, got: %s", filepath.Base(result[0]))
	}
}

func TestOrderTableSchemasByFKSelfReferenceIgnored(t *testing.T) {
	dir := t.TempDir()

	// Adjacency / parent-of table that references itself.
	sql := "CREATE TABLE `category` (\n" +
		"  `id` INT PRIMARY KEY,\n" +
		"  `parent_id` INT,\n" +
		"  CONSTRAINT `fk_self` FOREIGN KEY (`parent_id`) REFERENCES `category` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "db.category-schema.sql.gz", sql)

	// Single-item list — trivially returned unchanged, but self-reference must not
	// cause the node to be treated as having an unsatisfied dependency.
	result := orderTableSchemasByForeignKeys([]string{path}, nil)
	if len(result) != 1 || filepath.Base(result[0]) != "db.category-schema.sql.gz" {
		t.Fatalf("expected [db.category-schema.sql.gz], got %v", result)
	}

	// Two items: category and another table with no deps. Self-ref must not reorder.
	otherSQL := "CREATE TABLE `other` (`id` INT) ENGINE=InnoDB;\n"
	otherPath := writeSQLGzip(t, dir, "db.other-schema.sql.gz", otherSQL)
	result2 := orderTableSchemasByForeignKeys([]string{path, otherPath}, nil)
	if len(result2) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(result2), result2)
	}
	// Original order should be preserved since there is no dependency between them.
	if filepath.Base(result2[0]) != "db.category-schema.sql.gz" {
		t.Fatalf("expected category first (original order), got: %s", filepath.Base(result2[0]))
	}
}

func TestOrderTableSchemasByFKMissingRefIgnored(t *testing.T) {
	dir := t.TempDir()

	// Table references a table not in the backup set.
	sql := "CREATE TABLE `orphan` (\n" +
		"  `id` INT,\n" +
		"  `rid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`rid`) REFERENCES `nonexistent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	path := writeSQLGzip(t, dir, "db.orphan-schema.sql.gz", sql)

	result := orderTableSchemasByForeignKeys([]string{path}, nil)
	if len(result) != 1 || filepath.Base(result[0]) != "db.orphan-schema.sql.gz" {
		t.Fatalf("expected [db.orphan-schema.sql.gz], got %v", result)
	}
}

func TestOrderTableSchemasByFKCycleFallbackPreservesOriginalOrder(t *testing.T) {
	dir := t.TempDir()

	// A → B (A depends on B) and B → A (B depends on A) — true cycle.
	aSQL := "CREATE TABLE `a` (\n" +
		"  `id` INT,\n" +
		"  `bid` INT,\n" +
		"  CONSTRAINT `fk_ab` FOREIGN KEY (`bid`) REFERENCES `b` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	bSQL := "CREATE TABLE `b` (\n" +
		"  `id` INT,\n" +
		"  `aid` INT,\n" +
		"  CONSTRAINT `fk_ba` FOREIGN KEY (`aid`) REFERENCES `a` (`id`)\n" +
		") ENGINE=InnoDB;\n"

	aPath := writeSQLGzip(t, dir, "db.a-schema.sql.gz", aSQL)
	bPath := writeSQLGzip(t, dir, "db.b-schema.sql.gz", bSQL)

	var warnLogs []string
	logf := func(level, format string, args ...any) {
		if level == LogWarn {
			warnLogs = append(warnLogs, format)
		}
	}

	// Input: a, b (original order).
	result := orderTableSchemasByForeignKeys([]string{aPath, bPath}, logf)

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// Original order must be preserved for cycled nodes.
	if filepath.Base(result[0]) != "db.a-schema.sql.gz" || filepath.Base(result[1]) != "db.b-schema.sql.gz" {
		t.Fatalf("expected original order [a, b] for cycle fallback, got: %v",
			[]string{filepath.Base(result[0]), filepath.Base(result[1])})
	}
	// A cycle warning must have been emitted.
	if len(warnLogs) == 0 {
		t.Fatalf("expected cycle warning to be logged, got none")
	}
	foundCycleWarn := false
	for _, l := range warnLogs {
		if strings.Contains(strings.ToLower(l), "cycle") {
			foundCycleWarn = true
			break
		}
	}
	if !foundCycleWarn {
		t.Fatalf("expected log containing 'cycle', got: %v", warnLogs)
	}
}

func TestOrderTableSchemasByFKStableOriginalOrder(t *testing.T) {
	dir := t.TempDir()

	// Three independent tables with no FK relationships.
	// Result must preserve input order (stable sort).
	for _, name := range []string{"db.alpha-schema.sql.gz", "db.beta-schema.sql.gz", "db.gamma-schema.sql.gz"} {
		writeSQLGzip(t, dir, name,
			"CREATE TABLE `t` (`id` INT) ENGINE=InnoDB;\n")
	}

	paths := []string{
		filepath.Join(dir, "db.alpha-schema.sql.gz"),
		filepath.Join(dir, "db.beta-schema.sql.gz"),
		filepath.Join(dir, "db.gamma-schema.sql.gz"),
	}
	result := orderTableSchemasByForeignKeys(paths, nil)

	for i, want := range paths {
		if result[i] != want {
			t.Fatalf("stable order broken at index %d: got %s, want %s", i, filepath.Base(result[i]), filepath.Base(want))
		}
	}
}

// ---- orderSchemaPhaseByForeignKeys ----

func TestOrderSchemaPhasePreservesNonTableBuckets(t *testing.T) {
	dir := t.TempDir()

	// Write proper SQL so FK ordering can parse them.
	childSQL := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `parent` (`id` INT) ENGINE=InnoDB;\n"

	writeSQLGzip(t, dir, "db.child-schema.sql.gz", childSQL)
	writeSQLGzip(t, dir, "db.parent-schema.sql.gz", parentSQL)

	// Add non-table schema files — their relative order and bucket must be unchanged.
	systemAll := filepath.Join(dir, "mysql.system-all.sql.gz")
	routines := filepath.Join(dir, "db.__routines-schema-routine.sql.gz")
	views := filepath.Join(dir, "db.v_tbl-schema-view.sql.gz")
	for _, p := range []string{systemAll, routines, views} {
		if err := os.WriteFile(p, []byte("test"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Input in the order produced by sortSplitdumpSchemaFiles:
	// table schemas (alphabetical), then system-all, then routines, then views.
	input := []string{
		filepath.Join(dir, "db.child-schema.sql.gz"),
		filepath.Join(dir, "db.parent-schema.sql.gz"),
		systemAll,
		routines,
		views,
	}

	result := orderSchemaPhaseByForeignKeys(input, nil)

	if len(result) != 5 {
		t.Fatalf("expected 5 files, got %d: %v", len(result), result)
	}
	// Table schemas: parent must precede child after FK ordering.
	if filepath.Base(result[0]) != "db.parent-schema.sql.gz" {
		t.Fatalf("expected parent first in schema phase, got: %s", filepath.Base(result[0]))
	}
	if filepath.Base(result[1]) != "db.child-schema.sql.gz" {
		t.Fatalf("expected child second in schema phase, got: %s", filepath.Base(result[1]))
	}
	// Non-table artifacts must appear in the expected phase order at the end.
	if filepath.Base(result[2]) != "mysql.system-all.sql.gz" {
		t.Fatalf("expected mysql.system-all third, got: %s", filepath.Base(result[2]))
	}
	if filepath.Base(result[3]) != "db.__routines-schema-routine.sql.gz" {
		t.Fatalf("expected routines fourth, got: %s", filepath.Base(result[3]))
	}
	if filepath.Base(result[4]) != "db.v_tbl-schema-view.sql.gz" {
		t.Fatalf("expected view fifth, got: %s", filepath.Base(result[4]))
	}
}

// ---- BuildRestorePlan integration: FK ordering in legacy path ----

func TestBuildRestorePlanFKOrderParentBeforeChild(t *testing.T) {
	dir := t.TempDir()

	// child depends on parent but sorts alphabetically before it.
	childSQL := "CREATE TABLE `achild` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `zparent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `zparent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	writeSQLGzip(t, dir, "db.achild-schema.sql.gz", childSQL)
	writeSQLGzip(t, dir, "db.zparent-schema.sql.gz", parentSQL)
	// Data file to make the directory splitdump-detectable.
	if err := os.WriteFile(filepath.Join(dir, "db.achild.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write data: %v", err)
	}

	plan, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Schema) < 2 {
		t.Fatalf("expected at least 2 schema files, got %d: %v", len(plan.Schema), plan.Schema)
	}
	if filepath.Base(plan.Schema[0]) != "db.zparent-schema.sql.gz" {
		t.Fatalf("expected zparent first in plan (FK ordering), got: %s", filepath.Base(plan.Schema[0]))
	}
	if filepath.Base(plan.Schema[1]) != "db.achild-schema.sql.gz" {
		t.Fatalf("expected achild second in plan, got: %s", filepath.Base(plan.Schema[1]))
	}
}

func TestBuildRestorePlanManifestWinsOverFKOrdering(t *testing.T) {
	dir := t.TempDir()

	// Files on disk — parent sorts after child alphabetically.
	childSQL := "CREATE TABLE `achild` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`pid`) REFERENCES `zparent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `zparent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"

	writeSQLGzip(t, dir, "db.achild-schema.sql.gz", childSQL)
	writeSQLGzip(t, dir, "db.zparent-schema.sql.gz", parentSQL)
	if err := os.WriteFile(filepath.Join(dir, "db.achild.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write data: %v", err)
	}

	// Manifest records the correct FK-safe order (parent first).
	writeManifestFile(t, dir, &Manifest{
		Version: 1,
		Schema:  []string{"db.zparent-schema.sql.gz", "db.achild-schema.sql.gz"},
		Data:    []string{"db.achild.sql.gz"},
		Post:    nil,
	})

	plan, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Manifest order must be used — FK parsing is not invoked for the manifest path.
	if filepath.Base(plan.Schema[0]) != "db.zparent-schema.sql.gz" {
		t.Fatalf("expected manifest order (zparent first), got: %s", filepath.Base(plan.Schema[0]))
	}
}

func TestBuildRestorePlanFKOrderEmptyFiles(t *testing.T) {
	// Existing tests write files with content "test" (no SQL). FK ordering must
	// tolerate empty/non-SQL content without error and preserve alphabetical order.
	dir := t.TempDir()
	for _, f := range []string{"db.b_tbl-schema.sql.gz", "db.a_tbl-schema.sql.gz", "db.b_tbl.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	plan, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No FK deps found (dummy content) → alphabetical order preserved.
	if len(plan.Schema) != 2 {
		t.Fatalf("expected 2 schema files, got %d: %v", len(plan.Schema), plan.Schema)
	}
	// sortSplitdumpSchemaFiles puts a_tbl before b_tbl alphabetically.
	if filepath.Base(plan.Schema[0]) != "db.a_tbl-schema.sql.gz" {
		t.Fatalf("expected a_tbl first (alphabetical fallback), got: %s", filepath.Base(plan.Schema[0]))
	}
}

func TestBuildRestorePlanFKChain(t *testing.T) {
	// Three-level chain: grandparent ← parent ← child.
	// Input order: child, parent, grandparent (all wrong).
	dir := t.TempDir()

	grandSQL := "CREATE TABLE `grandparent` (`id` INT PRIMARY KEY) ENGINE=InnoDB;\n"
	parentSQL := "CREATE TABLE `parent` (\n" +
		"  `id` INT PRIMARY KEY,\n" +
		"  `gid` INT,\n" +
		"  CONSTRAINT `fk_g` FOREIGN KEY (`gid`) REFERENCES `grandparent` (`id`)\n" +
		") ENGINE=InnoDB;\n"
	childSQL := "CREATE TABLE `child` (\n" +
		"  `id` INT,\n" +
		"  `pid` INT,\n" +
		"  CONSTRAINT `fk_p` FOREIGN KEY (`pid`) REFERENCES `parent` (`id`)\n" +
		") ENGINE=InnoDB;\n"

	writeSQLGzip(t, dir, "db.child-schema.sql.gz", childSQL)
	writeSQLGzip(t, dir, "db.parent-schema.sql.gz", parentSQL)
	writeSQLGzip(t, dir, "db.grandparent-schema.sql.gz", grandSQL)
	// Need a data file for Detect() to recognise the layout.
	if err := os.WriteFile(filepath.Join(dir, "db.child.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write data: %v", err)
	}

	plan, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Schema) < 3 {
		t.Fatalf("expected 3 schema files, got %d", len(plan.Schema))
	}

	bases := make([]string, len(plan.Schema))
	for i, p := range plan.Schema {
		bases[i] = filepath.Base(p)
	}

	grandIdx := -1
	parentIdx := -1
	childIdx := -1
	for i, b := range bases {
		switch b {
		case "db.grandparent-schema.sql.gz":
			grandIdx = i
		case "db.parent-schema.sql.gz":
			parentIdx = i
		case "db.child-schema.sql.gz":
			childIdx = i
		}
	}
	if grandIdx < 0 || parentIdx < 0 || childIdx < 0 {
		t.Fatalf("missing schema files in plan: %v", bases)
	}
	if !(grandIdx < parentIdx && parentIdx < childIdx) {
		t.Fatalf("expected grandparent < parent < child order, got indices grand=%d parent=%d child=%d in %v",
			grandIdx, parentIdx, childIdx, bases)
	}
}
