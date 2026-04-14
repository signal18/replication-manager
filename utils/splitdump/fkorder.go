package splitdump

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// referencesRe matches a FOREIGN KEY REFERENCES clause in mysqldump SQL output and
// captures the referenced schema and table name. It handles four identifier forms:
//
//	`schema`.`table`  — backtick-quoted explicit (groups 1, 2)
//	`table`           — backtick-quoted implicit  (group 3)
//	schema.table      — unquoted explicit          (groups 4, 5)
//	table             — unquoted implicit           (group 4 only)
//
// The trailing \s*\( requires the column list to follow, which is always present
// in mysqldump output and significantly reduces false positives from comments.
var referencesRe = regexp.MustCompile(
	"(?i)REFERENCES\\s+" +
		"(?:" +
		"`([^`]+)`\\.`([^`]+)`" + // groups 1=schema, 2=table (backtick explicit)
		"|`([^`]+)`" + // group 3=table        (backtick implicit)
		"|([A-Za-z_][A-Za-z0-9_$]*)(?:\\.([A-Za-z_][A-Za-z0-9_$]*))?" + // groups 4,5 unquoted
		")\\s*\\(")

// splitSchemaBuckets partitions a schema-phase file list into four ordered sub-lists:
//   - tableSchemas: plain table definitions (-schema.sql[.gz])
//   - mysqlSystemAll: the mysql.system-all artifact
//   - routineSchemas: stored procedures, functions, and routine bundles
//   - viewSchemas: view definitions (-schema-view.sql[.gz])
//
// The relative order within each sub-list matches the input order.
func splitSchemaBuckets(paths []string) (tableSchemas, mysqlSystemAll, routineSchemas, viewSchemas []string) {
	for _, p := range paths {
		lower := strings.ToLower(filepath.Base(p))
		switch {
		case IsMysqlSystemAll(lower):
			mysqlSystemAll = append(mysqlSystemAll, p)
		case strings.HasSuffix(lower, "-schema-routine.sql.gz") ||
			strings.HasSuffix(lower, "-schema-routine.sql") ||
			strings.HasSuffix(lower, "-schema-function.sql.gz") ||
			strings.HasSuffix(lower, "-schema-function.sql") ||
			strings.HasSuffix(lower, "-schema-procedure.sql.gz") ||
			strings.HasSuffix(lower, "-schema-procedure.sql"):
			routineSchemas = append(routineSchemas, p)
		case strings.HasSuffix(lower, "-schema-view.sql.gz") ||
			strings.HasSuffix(lower, "-schema-view.sql"):
			viewSchemas = append(viewSchemas, p)
		default:
			// Includes plain -schema.sql[.gz] and any unrecognised schema artifact.
			tableSchemas = append(tableSchemas, p)
		}
	}
	return
}

// appendReferencedTablesFromSQL extracts FOREIGN KEY REFERENCES clauses from arbitrary
// SQL text and appends newly-seen "schema.table" keys to *refs, deduplicating via seen.
// defaultSchema is used when no explicit schema qualifier is present.
func appendReferencedTablesFromSQL(sqlText, defaultSchema string, seen map[string]bool, refs *[]string) {
	// Fast path: skip text that cannot contain a REFERENCES keyword.
	if !strings.Contains(strings.ToUpper(sqlText), "REFERENCES") {
		return
	}
	for _, m := range referencesRe.FindAllStringSubmatch(sqlText, -1) {
		var refSchema, refTable string
		switch {
		case m[1] != "" && m[2] != "": // `schema`.`table`
			refSchema = strings.ToLower(m[1])
			refTable = strings.ToLower(m[2])
		case m[3] != "": // `table`
			refSchema = defaultSchema
			refTable = strings.ToLower(m[3])
		case m[4] != "" && m[5] != "": // schema.table unquoted
			refSchema = strings.ToLower(m[4])
			refTable = strings.ToLower(m[5])
		case m[4] != "": // table unquoted
			refSchema = defaultSchema
			refTable = strings.ToLower(m[4])
		}
		if refTable == "" {
			continue
		}
		key := refSchema + "." + refTable
		if !seen[key] {
			seen[key] = true
			*refs = append(*refs, key)
		}
	}
}

// openAndDecompress opens path and, if the content is gzip-compressed, wraps it in a
// gzip.Reader. Returns (reader, closer, nil) on success; (nil, nil, nil) when the file
// cannot be opened or decompressed (caller treats as dependency-free).
func openAndDecompress(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil //nolint:nilerr // treat unreadable file as dependency-free
	}
	bufR := bufio.NewReaderSize(f, 32*1024)
	peek, peekErr := bufR.Peek(2)
	if peekErr == nil && len(peek) >= 2 && peek[0] == 0x1f && peek[1] == 0x8b {
		gz, gzErr := gzip.NewReader(bufR)
		if gzErr != nil {
			f.Close()
			return nil, nil, nil //nolint:nilerr // corrupt gzip header — treat as dependency-free
		}
		return gz, func() { gz.Close(); f.Close() }, nil
	}
	return bufR, func() { f.Close() }, nil
}

// parseReferencedTablesScanner reads a SQL file line-by-line using bufio.Scanner and
// returns all FOREIGN KEY REFERENCES found. It returns (partial-refs, bufio.ErrTooLong)
// when a single line exceeds the 4 MiB token limit so the caller can retry via the
// statement-accumulation path. On file-open or decompression errors it returns (nil, nil).
func parseReferencedTablesScanner(path string, defaultSchema string) ([]string, error) {
	r, closeAll, _ := openAndDecompress(path)
	if r == nil {
		return nil, nil
	}
	defer closeAll()

	scanner := bufio.NewScanner(r)
	// Allow up to 4 MiB per line. mysqldump schema files are typically well under
	// this limit; the cap prevents unbounded memory use on pathological inputs while
	// being large enough to handle any realistic CREATE TABLE statement.
	const maxTokenSize = 4 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxTokenSize)

	defaultSchema = strings.ToLower(defaultSchema)
	seen := make(map[string]bool)
	var refs []string

	for scanner.Scan() {
		appendReferencedTablesFromSQL(scanner.Text(), defaultSchema, seen, &refs)
	}
	// Return partial refs alongside the error so the caller can decide whether to
	// use what was extracted before the failure.
	return refs, scanner.Err()
}

// parseReferencedTablesLarge reads a SQL file using semicolon-delimited statement
// accumulation rather than line-by-line scanning. This handles DDL files where a single
// logical line exceeds the scanner token limit. Each chunk up to ';' is treated as one
// statement and searched for REFERENCES clauses. On file-open or decompression errors it
// returns (nil, nil).
func parseReferencedTablesLarge(path string, defaultSchema string) ([]string, error) {
	r, closeAll, _ := openAndDecompress(path)
	if r == nil {
		return nil, nil
	}
	defer closeAll()

	stmtReader := bufio.NewReaderSize(r, 64*1024)
	defaultSchema = strings.ToLower(defaultSchema)
	seen := make(map[string]bool)
	var refs []string

	for {
		stmt, readErr := stmtReader.ReadString(';')
		if len(stmt) > 0 {
			appendReferencedTablesFromSQL(stmt, defaultSchema, seen, &refs)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return refs, readErr
		}
	}
	return refs, nil
}

// parseReferencedTables reads a SQL file (plain or gzip-compressed) and returns the
// normalised "schema.table" keys of every table referenced via FOREIGN KEY REFERENCES
// clauses. defaultSchema is used for references that omit an explicit schema qualifier.
//
// It first attempts line-by-line scanning (fast path). If bufio.ErrTooLong is returned
// because a single line exceeds the 4 MiB token limit, it transparently retries using
// statement-accumulation (reading up to the next semicolon) so that oversized DDL
// statements are still correctly parsed.
//
// On file-open or decompression errors the function returns (nil, nil) so the caller
// treats the file as dependency-free without surfacing an I/O error.
func parseReferencedTables(path string, defaultSchema string) ([]string, error) {
	refs, err := parseReferencedTablesScanner(path, defaultSchema)
	if err == nil {
		return refs, nil
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		// Non-ErrTooLong scan failure: return whatever partial refs were collected.
		return refs, err
	}
	// Retry with the statement-accumulation large parser.
	largeRefs, largeErr := parseReferencedTablesLarge(path, defaultSchema)
	if largeErr == nil {
		return largeRefs, nil
	}
	if len(largeRefs) > 0 {
		return largeRefs, largeErr
	}
	return refs, largeErr
}

// orderTableSchemasByForeignKeys reorders paths so that any table referenced by a
// FOREIGN KEY constraint appears before the table that declares the constraint.
// Stable tie-breaking by original input index preserves the existing order for tables
// that have no ordering constraint relative to each other.
//
// When unresolvable cycles are detected the remaining nodes are appended in their
// original input order. If logf is non-nil a warning is emitted for each cycle group.
func orderTableSchemasByForeignKeys(paths []string, logf func(level, format string, args ...any)) []string {
	if len(paths) <= 1 {
		return paths
	}

	type tableNode struct {
		path    string
		key     string // "schema.table" (lower-cased), empty for unparseable filenames
		origIdx int
	}

	nodes := make([]tableNode, len(paths))
	keyToIdx := make(map[string]int, len(paths))
	for i, p := range paths {
		schema := strings.ToLower(SchemaFromFilename(p))
		table := strings.ToLower(TableFromFilename(p))
		key := ""
		if schema != "" && table != "" {
			key = schema + "." + table
		}
		nodes[i] = tableNode{path: p, key: key, origIdx: i}
		if key != "" {
			keyToIdx[key] = i
		}
	}

	// Build Kahn's algorithm structures.
	// indegree[i] = number of not-yet-satisfied parent dependencies for node i.
	// children[i] = indices of nodes that list i as a dependency.
	indegree := make([]int, len(nodes))
	children := make([][]int, len(nodes))

	for i, n := range nodes {
		refs, scanErr := parseReferencedTables(n.path, SchemaFromFilename(n.path))
		if scanErr != nil && logf != nil {
			logf(LogWarn, "Splitdump FK parser: scan error reading %s; FK dependencies may be incomplete: %v",
				filepath.Base(n.path), scanErr)
		}
		added := make(map[int]bool)
		for _, ref := range refs {
			ref = strings.ToLower(ref)
			if ref == n.key { // self-reference — no ordering pressure
				continue
			}
			parentIdx, ok := keyToIdx[ref]
			if !ok { // referenced table not in backup set — ignore
				continue
			}
			if added[parentIdx] { // deduplicate edges
				continue
			}
			added[parentIdx] = true
			indegree[i]++
			children[parentIdx] = append(children[parentIdx], i)
		}
	}

	// Initialise the eligible set (nodes with no unsatisfied dependencies).
	eligible := make([]int, 0, len(nodes))
	for i, deg := range indegree {
		if deg == 0 {
			eligible = append(eligible, i)
		}
	}
	sort.Slice(eligible, func(a, b int) bool {
		return nodes[eligible[a]].origIdx < nodes[eligible[b]].origIdx
	})

	result := make([]string, 0, len(paths))
	processed := make([]bool, len(nodes))

	for len(eligible) > 0 {
		// Pick the eligible node with the lowest original index.
		idx := eligible[0]
		eligible = eligible[1:]
		processed[idx] = true
		result = append(result, nodes[idx].path)

		// Unlock any nodes that depended solely on idx.
		var newlyEligible []int
		for _, child := range children[idx] {
			indegree[child]--
			if indegree[child] == 0 {
				newlyEligible = append(newlyEligible, child)
			}
		}
		if len(newlyEligible) > 0 {
			eligible = append(eligible, newlyEligible...)
			sort.Slice(eligible, func(a, b int) bool {
				return nodes[eligible[a]].origIdx < nodes[eligible[b]].origIdx
			})
		}
	}

	// Any unprocessed node is part of a dependency cycle.
	hasCycle := false
	for i := range nodes {
		if !processed[i] {
			if !hasCycle {
				hasCycle = true
				if logf != nil {
					logf(LogWarn, "Splitdump FK ordering: unresolvable dependency cycle detected; affected tables appended in original input order")
				}
			}
			result = append(result, nodes[i].path)
		}
	}

	return result
}

// orderSchemaPhaseByForeignKeys partitions a schema-phase file list into sub-buckets,
// applies FK-based reordering to plain table schemas only, then reassembles in the
// canonical phase order: table schemas → mysql.system-all → routines → views.
//
// mysql.system-all, routine, and view artifacts are never reordered; only the plain
// table schema sub-list is modified. logf may be nil to suppress cycle warnings.
func orderSchemaPhaseByForeignKeys(schemaFiles []string, logf func(level, format string, args ...any)) []string {
	tables, systemAll, routines, views := splitSchemaBuckets(schemaFiles)
	ordered := orderTableSchemasByForeignKeys(tables, logf)
	result := make([]string, 0, len(schemaFiles))
	result = append(result, ordered...)
	result = append(result, systemAll...)
	result = append(result, routines...)
	result = append(result, views...)
	return result
}
