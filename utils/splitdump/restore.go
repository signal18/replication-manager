package splitdump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	LogError = "ERROR"
	LogWarn  = "WARN"
	LogInfo  = "INFO"
	LogDebug = "DEBUG"
)

var ErrMetadataInvalid = errors.New("splitdump metadata invalid")

// ErrNotSplitdump is returned by BuildRestorePlan when the backup directory
// does not contain a splitdump-compatible artifact layout.
var ErrNotSplitdump = errors.New("not a splitdump backup layout")

// ErrDefinerStrict is returned when strict DEFINER enforcement blocks a restore.
var ErrDefinerStrict = errors.New("strict DEFINER enforcement blocked restore")

// definerErrRe matches the MySQL/MariaDB DEFINER compatibility error. The pattern targets the
// canonical error number (1449) and the distinctive phrase in the error message, avoiding false
// positives from passwords, row counts, or unrelated messages that happen to contain "1449" or
// "definer" as substrings.
var definerErrRe = regexp.MustCompile(`(?i)(ERROR 1449|the user specified as a definer)`)

// IsDefinerError reports whether err indicates a MySQL/MariaDB DEFINER compatibility failure
// (typically error 1449: the definer user does not exist on the target server).
func IsDefinerError(err error) bool {
	return err != nil && definerErrRe.MatchString(err.Error())
}

type Metadata struct {
	File       string
	Position   uint64
	GTID       string
	SourceData int
}

type RestoreOptions struct {
	Parallel               int
	RestoreUser            bool
	StrictMetadata         bool
	// DefinerStrict causes restore to fail closed when an incompatible DEFINER clause is
	// encountered. When false (default), non-strict fallback is applied: RestoreFileWithoutDefiner
	// is called if provided, otherwise the file is skipped with a warning.
	DefinerStrict             bool
	Logger                    func(level, format string, args ...any)
	RestoreFile               func(path string) error
	Context                   context.Context
	RestoreFileWithContext     func(ctx context.Context, path string) error
	// RestoreFileWithoutDefiner is called instead of RestoreFileWithContext when a DEFINER
	// error is detected and DefinerStrict is false. The implementation is responsible for
	// stripping DEFINER clauses before execution.
	RestoreFileWithoutDefiner func(ctx context.Context, path string) error
}

type FileSet struct {
	Schema []string
	Data   []string
	Post   []string
}

type dataGroup struct {
	key   string
	paths []string
}

type fileCategory int

const (
	fileCategorySchema fileCategory = iota
	fileCategoryData
	fileCategoryPost
)

func ReadMetadata(backupPath string) (*Metadata, error) {
	metadataPath := filepath.Join(backupPath, "metadata")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read splitdump metadata: %w", err)
	}
	meta := &Metadata{}
	positionSet := false
	sourceDataSet := false
	positionMissing := false
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Source_Data ="):
			srcStr := strings.TrimSpace(strings.TrimPrefix(line, "Source_Data ="))
			if srcStr == "" {
				return nil, fmt.Errorf("%w: missing source data", ErrMetadataInvalid)
			}
			src, err := strconv.Atoi(srcStr)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid source data %q: %v", ErrMetadataInvalid, srcStr, err)
			}
			meta.SourceData = src
			sourceDataSet = true
		case strings.HasPrefix(line, "File ="):
			meta.File = strings.TrimSpace(strings.TrimPrefix(line, "File ="))
		case strings.HasPrefix(line, "Position ="):
			posStr := strings.TrimSpace(strings.TrimPrefix(line, "Position ="))
			if posStr == "" {
				positionMissing = true
				continue
			}
			pos, err := strconv.ParseUint(posStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid position %q: %v", ErrMetadataInvalid, posStr, err)
			}
			meta.Position = pos
			positionSet = true
		case strings.HasPrefix(line, "Executed_Gtid_Set ="):
			meta.GTID = strings.TrimSpace(strings.TrimPrefix(line, "Executed_Gtid_Set ="))
		}
	}
	if sourceDataSet && meta.SourceData == 0 {
		return meta, nil
	}
	if meta.File == "" {
		return nil, fmt.Errorf("%w: missing binlog file", ErrMetadataInvalid)
	}
	if positionMissing || !positionSet {
		return nil, fmt.Errorf("%w: missing position", ErrMetadataInvalid)
	}
	return meta, nil
}

func ListFiles(backupPath string, restoreUser bool) (FileSet, error) {
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		return FileSet{}, err
	}
	set := FileSet{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		category, ok := classifyFile(name, restoreUser)
		if !ok {
			continue
		}
		fullPath := filepath.Join(backupPath, name)
		switch category {
		case fileCategorySchema:
			set.Schema = append(set.Schema, fullPath)
		case fileCategoryPost:
			set.Post = append(set.Post, fullPath)
		default:
			set.Data = append(set.Data, fullPath)
		}
	}
	sortSplitdumpSchemaFiles(set.Schema)
	sortSplitdumpDataFiles(set.Data)
	sortSplitdumpPostFiles(set.Post)
	return set, nil
}

var systemSchemas = map[string]bool{
	"mysql":              true,
	"information_schema": true,
	"performance_schema": true,
	"sys":                true,
}

// ListSchemas returns the sorted list of unique user-defined schema names found
// in the backup directory, excluding system schemas (mysql, information_schema,
// performance_schema, sys).
func ListSchemas(backupPath string) ([]string, error) {
	files, err := ListFiles(backupPath, false)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var schemas []string
	// Explicit allocation avoids the append-aliasing pitfall where a first append with spare
	// capacity in files.Schema would write files.Data entries into its backing array.
	allFiles := make([]string, 0, len(files.Schema)+len(files.Data)+len(files.Post))
	allFiles = append(allFiles, files.Schema...)
	allFiles = append(allFiles, files.Data...)
	allFiles = append(allFiles, files.Post...)
	for _, path := range allFiles {
		schema := SchemaFromFilename(path)
		if schema == "" || systemSchemas[strings.ToLower(schema)] {
			continue
		}
		if !seen[schema] {
			seen[schema] = true
			schemas = append(schemas, schema)
		}
	}
	sort.Strings(schemas)
	return schemas, nil
}

func Restore(backupPath string, opts RestoreOptions) error {
	if opts.RestoreFile == nil && opts.RestoreFileWithContext == nil {
		return fmt.Errorf("splitdump restore requires RestoreFile")
	}
	logf := func(level, format string, args ...any) {
		if opts.Logger != nil {
			opts.Logger(level, format, args...)
		}
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	meta, err := ReadMetadata(backupPath)
	if err != nil {
		if opts.StrictMetadata {
			return err
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			logf(LogWarn, "Splitdump metadata not found; continuing without binlog info")
		case errors.Is(err, ErrMetadataInvalid):
			logf(LogWarn, "Splitdump metadata malformed; continuing without binlog info: %v", err)
		default:
			return err
		}
	} else {
		logf(LogInfo, "Splitdump metadata loaded (file=%s pos=%d)", meta.File, meta.Position)
	}

	plan, err := BuildRestorePlan(backupPath, opts.RestoreUser)
	if err != nil {
		return err
	}
	files := *plan
	logf(LogInfo, "Splitdump detection: compatible backup layout confirmed at %s", backupPath)
	logf(LogInfo, "Splitdump restore files listed (schema=%d data=%d post=%d)", len(files.Schema), len(files.Data), len(files.Post))

	restoreOneFile := func(path string) error {
		var restoreErr error
		if opts.RestoreFileWithContext != nil {
			restoreErr = opts.RestoreFileWithContext(ctx, path)
		} else {
			restoreErr = opts.RestoreFile(path)
		}
		if restoreErr == nil || !IsDefinerError(restoreErr) {
			return restoreErr
		}
		// DEFINER compatibility error detected.
		if opts.DefinerStrict {
			logf(LogError, "Splitdump strict DEFINER enforcement blocked restore of %s: %v", filepath.Base(path), restoreErr)
			return fmt.Errorf("%w: %s: %v", ErrDefinerStrict, filepath.Base(path), restoreErr)
		}
		// Non-strict fallback: try without DEFINER if a fallback function is provided.
		if opts.RestoreFileWithoutDefiner != nil {
			logf(LogWarn, "Splitdump DEFINER fallback for %s (retrying without DEFINER)", filepath.Base(path))
			return opts.RestoreFileWithoutDefiner(ctx, path)
		}
		// No fallback function — skip the file and log the omission.
		logf(LogWarn, "Splitdump DEFINER skipped for %s: incompatible DEFINER clause, no fallback function provided", filepath.Base(path))
		return nil
	}

	logf(LogInfo, "Splitdump restore phase: schema (%d files)", len(files.Schema))
	for _, schemaFile := range files.Schema {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf(LogDebug, "Splitdump restoring schema (%s)", filepath.Base(schemaFile))
		err := restoreOneFile(schemaFile)
		if err != nil {
			logf(LogError, "Splitdump schema restore failed (%s): %v", filepath.Base(schemaFile), err)
			cancel()
			return err
		}
	}

	parallel := opts.Parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(files.Data) && len(files.Data) > 0 {
		parallel = len(files.Data)
	}

	logf(LogInfo, "Splitdump restore phase: data (%d files)", len(files.Data))
	dataGroups := groupSplitdumpDataFiles(files.Data)
	if len(dataGroups) == 0 {
		logf(LogInfo, "Splitdump restoring data files skipped (none)")
	} else {
		var wg sync.WaitGroup
		var firstErr error
		var errOnce sync.Once

		setErr := func(err error) {
			errOnce.Do(func() {
				firstErr = err
				cancel()
			})
		}

		groupJobs := make(chan dataGroup)
		groupWorker := func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case group, ok := <-groupJobs:
					if !ok {
						return
					}
					for _, path := range group.paths {
						select {
						case <-ctx.Done():
							return
						default:
						}
						logf(LogDebug, "Splitdump restoring data (%s)", filepath.Base(path))
						err := restoreOneFile(path)
						if err != nil {
							logf(LogError, "Splitdump data restore failed (%s): %v", filepath.Base(path), err)
							setErr(err)
							return
						}
					}
				}
			}
		}

		if parallel > len(dataGroups) {
			parallel = len(dataGroups)
		}

		for i := 0; i < parallel; i++ {
			wg.Add(1)
			go groupWorker()
		}

		logf(LogInfo, "Splitdump restoring data files (count=%d parallel=%d)", len(files.Data), parallel)

	sendLoop:
		for _, group := range dataGroups {
			select {
			case <-ctx.Done():
				break sendLoop
			case groupJobs <- group:
			}
		}
		close(groupJobs)
		wg.Wait()

		if firstErr != nil {
			return firstErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	logf(LogInfo, "Splitdump restore phase: post-data (%d files)", len(files.Post))
	for _, postFile := range files.Post {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf(LogDebug, "Splitdump restoring post-data (%s)", filepath.Base(postFile))
		err := restoreOneFile(postFile)
		if err != nil {
			logf(LogError, "Splitdump post-data restore failed (%s): %v", filepath.Base(postFile), err)
			cancel()
			return err
		}
	}

	logf(LogInfo, "Splitdump restore completed")
	return nil
}

func classifyFile(name string, restoreUser bool) (category fileCategory, ok bool) {
	lower := strings.ToLower(name)
	if lower == "metadata" {
		return fileCategoryData, false
	}
	if strings.HasSuffix(lower, ".sql.gz") || strings.HasSuffix(lower, ".sql") {
		switch {
		case strings.HasSuffix(lower, "-schema.sql.gz") || strings.HasSuffix(lower, "-schema.sql"):
			return fileCategorySchema, true
		case strings.HasSuffix(lower, "-schema-view.sql.gz") || strings.HasSuffix(lower, "-schema-view.sql"):
			return fileCategorySchema, true
		case strings.HasSuffix(lower, "-schema-routine.sql.gz") || strings.HasSuffix(lower, "-schema-routine.sql"):
			return fileCategorySchema, true
		case strings.HasSuffix(lower, "-schema-function.sql.gz") || strings.HasSuffix(lower, "-schema-function.sql"):
			return fileCategorySchema, true
		case strings.HasSuffix(lower, "-schema-procedure.sql.gz") || strings.HasSuffix(lower, "-schema-procedure.sql"):
			return fileCategorySchema, true
		case strings.HasSuffix(lower, "-schema-trigger.sql.gz") || strings.HasSuffix(lower, "-schema-trigger.sql"):
			return fileCategoryPost, true
		case strings.HasSuffix(lower, "-schema-event.sql.gz") || strings.HasSuffix(lower, "-schema-event.sql"):
			return fileCategoryPost, true
		case lower == "mysql.system-all.sql.gz" || lower == "mysql.system-all.sql":
			if !restoreUser {
				return fileCategoryData, false
			}
			return fileCategorySchema, true
		default:
			return fileCategoryData, true
		}
	}
	return fileCategoryData, false
}

func sortSplitdumpDataFiles(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		left := filepath.Base(paths[i])
		right := filepath.Base(paths[j])
		leftKey, leftShard := splitdumpDataKey(left)
		rightKey, rightShard := splitdumpDataKey(right)
		if leftKey == rightKey {
			return leftShard < rightShard
		}
		return leftKey < rightKey
	})
}

func sortSplitdumpSchemaFiles(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		left := filepath.Base(paths[i])
		right := filepath.Base(paths[j])
		leftPriority := splitdumpSchemaPriority(left)
		rightPriority := splitdumpSchemaPriority(right)
		if leftPriority == rightPriority {
			return left < right
		}
		return leftPriority < rightPriority
	})
}

func splitdumpSchemaPriority(name string) int {
	lower := strings.ToLower(filepath.Base(name))
	switch {
	case strings.HasSuffix(lower, "-schema.sql.gz") || strings.HasSuffix(lower, "-schema.sql"):
		return 0
	case IsMysqlSystemAll(lower):
		return 1
	case strings.HasSuffix(lower, "-schema-routine.sql.gz") || strings.HasSuffix(lower, "-schema-routine.sql") ||
		strings.HasSuffix(lower, "-schema-function.sql.gz") || strings.HasSuffix(lower, "-schema-function.sql") ||
		strings.HasSuffix(lower, "-schema-procedure.sql.gz") || strings.HasSuffix(lower, "-schema-procedure.sql"):
		return 2
	case strings.HasSuffix(lower, "-schema-view.sql.gz") || strings.HasSuffix(lower, "-schema-view.sql"):
		return 3
	default:
		return 4
	}
}

func sortSplitdumpPostFiles(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		left := filepath.Base(paths[i])
		right := filepath.Base(paths[j])
		leftPriority := splitdumpPostPriority(left)
		rightPriority := splitdumpPostPriority(right)
		if leftPriority == rightPriority {
			return left < right
		}
		return leftPriority < rightPriority
	})
}

func splitdumpPostPriority(name string) int {
	lower := strings.ToLower(filepath.Base(name))
	switch {
	case strings.HasSuffix(lower, "-schema-trigger.sql.gz") || strings.HasSuffix(lower, "-schema-trigger.sql"):
		return 0
	case strings.HasSuffix(lower, "-schema-event.sql.gz") || strings.HasSuffix(lower, "-schema-event.sql"):
		return 1
	default:
		return 2
	}
}

func groupSplitdumpDataFiles(paths []string) []dataGroup {
	if len(paths) == 0 {
		return nil
	}
	groups := make([]dataGroup, 0)
	var current dataGroup
	for _, path := range paths {
		key, _ := splitdumpDataKey(filepath.Base(path))
		if current.key == "" {
			current = dataGroup{key: key, paths: []string{path}}
			continue
		}
		if key == current.key {
			current.paths = append(current.paths, path)
			continue
		}
		groups = append(groups, current)
		current = dataGroup{key: key, paths: []string{path}}
	}
	if current.key != "" {
		groups = append(groups, current)
	}
	return groups
}

func splitdumpDataKey(name string) (key string, shard int) {
	key = strings.TrimSuffix(name, ".sql.gz")
	key = strings.TrimSuffix(key, ".sql")
	shard = 0
	if len(key) > 6 && key[len(key)-6] == '.' {
		suffix := key[len(key)-5:]
		if n, err := strconv.Atoi(suffix); err == nil {
			shard = n
			key = key[:len(key)-6]
		}
	}
	return key, shard
}

func SchemaFromFilename(name string) string {
	schema, _ := splitdumpSchemaTable(name)
	return schema
}

func TableFromFilename(name string) string {
	_, table := splitdumpSchemaTable(name)
	return table
}

// IsSchemaFile reports whether name is a schema-phase file (tables, mysql.system-all, routines,
// views). Trigger and event files are post-phase artifacts (see IsPostFile) and are intentionally
// excluded here so that callers gating schema-phase behaviour (table existence checks, ordering)
// do not accidentally include them.
func IsSchemaFile(name string) bool {
	base := filepath.Base(name)
	if IsMysqlSystemAll(base) {
		return true
	}
	lower := strings.ToLower(base)
	return strings.HasSuffix(lower, "-schema.sql.gz") ||
		strings.HasSuffix(lower, "-schema.sql") ||
		strings.HasSuffix(lower, "-schema-view.sql.gz") ||
		strings.HasSuffix(lower, "-schema-view.sql") ||
		strings.HasSuffix(lower, "-schema-routine.sql.gz") ||
		strings.HasSuffix(lower, "-schema-routine.sql") ||
		strings.HasSuffix(lower, "-schema-function.sql.gz") ||
		strings.HasSuffix(lower, "-schema-function.sql") ||
		strings.HasSuffix(lower, "-schema-procedure.sql.gz") ||
		strings.HasSuffix(lower, "-schema-procedure.sql")
}

// IsPostFile reports whether name is a post-data-phase file (triggers or events).
// These are distinct from schema-phase files even though their filenames carry a "-schema-" infix.
func IsPostFile(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	return strings.HasSuffix(lower, "-schema-trigger.sql.gz") ||
		strings.HasSuffix(lower, "-schema-trigger.sql") ||
		strings.HasSuffix(lower, "-schema-event.sql.gz") ||
		strings.HasSuffix(lower, "-schema-event.sql")
}

func IsGtidSlavePosDataFile(name string) bool {
	schema, table := splitdumpSchemaTable(name)
	if schema != "mysql" || table != "gtid_slave_pos" {
		return false
	}
	return !IsSchemaFile(name)
}

func IsMissingTableError(errOutput string) bool {
	lower := strings.ToLower(errOutput)
	return strings.Contains(lower, "error 1146") || strings.Contains(lower, "42s02")
}

func RestorePreamble(name string) string {
	schema := SchemaFromFilename(name)
	if schema == "" {
		return "SET FOREIGN_KEY_CHECKS=0;\n"
	}
	escaped := strings.ReplaceAll(schema, "`", "``")
	return "SET FOREIGN_KEY_CHECKS=0;\nUSE `" + escaped + "`;\n"
}

func splitdumpSchemaTable(name string) (string, string) {
	base := filepath.Base(name)
	if IsMysqlSystemAll(base) {
		return "mysql", ""
	}
	lower := strings.ToLower(base)
	if !strings.HasSuffix(lower, ".sql.gz") && !strings.HasSuffix(lower, ".sql") {
		return "", ""
	}

	trimmed := strings.TrimSuffix(base, ".sql.gz")
	trimmed = strings.TrimSuffix(trimmed, ".sql")
	trimmed = strings.TrimSuffix(trimmed, "-schema-event")
	trimmed = strings.TrimSuffix(trimmed, "-schema-trigger")
	trimmed = strings.TrimSuffix(trimmed, "-schema-routine")
	trimmed = strings.TrimSuffix(trimmed, "-schema-function")
	trimmed = strings.TrimSuffix(trimmed, "-schema-procedure")
	trimmed = strings.TrimSuffix(trimmed, "-schema-view")
	trimmed = strings.TrimSuffix(trimmed, "-schema")
	trimmed, _ = splitdumpDataKey(trimmed)
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, ".", 2)
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func IsMysqlSystemAll(name string) bool {
	lower := strings.ToLower(name)
	return lower == "mysql.system-all.sql.gz" || lower == "mysql.system-all.sql" || lower == "mysql.system-all"
}

// Detect reports whether backupPath contains a splitdump-compatible artifact layout.
// A directory is splitdump-compatible if it contains at least one file whose name
// follows the schema.table naming convention used by splitdump (e.g. db.tbl-schema.sql.gz,
// mysql.system-all.sql.gz). Directories with only generic SQL files (no schema.table prefix)
// or only non-SQL files are not considered splitdump-compatible.
// Returns (true, nil) for compatible layouts, (false, nil) for incompatible layouts,
// and (false, err) for I/O errors.
func Detect(backupPath string) (bool, error) {
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if SchemaFromFilename(entry.Name()) != "" {
			return true, nil
		}
	}
	return false, nil
}

// BuildRestorePlan classifies the files in backupPath into typed restore phases:
// schema (tables, mysql.system-all, routines, views), data, and post-data (triggers, events).
// Phase ordering is fixed before any replay begins: schema files are ordered by type priority,
// data files by table and shard, post-data files with triggers before events.
// Returns ErrNotSplitdump if the directory does not contain a splitdump-compatible layout.
func BuildRestorePlan(backupPath string, restoreUser bool) (*FileSet, error) {
	ok, err := Detect(backupPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotSplitdump
	}
	fs, err := ListFiles(backupPath, restoreUser)
	if err != nil {
		return nil, err
	}
	return &fs, nil
}

// IsMysqlTableCheckEligible reports whether a tableExists check makes sense before restoring name.
// Trigger files reference a table but are post-phase objects: running a table-existence guard on
// them would skip legitimate trigger restores. Views, routines, events, and mysql.system-all are
// also excluded because they have their own ordering or existence semantics.
func IsMysqlTableCheckEligible(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	if !strings.HasSuffix(lower, ".sql.gz") && !strings.HasSuffix(lower, ".sql") {
		return false
	}
	if IsMysqlSystemAll(lower) {
		return false
	}
	if strings.HasSuffix(lower, "-schema-trigger.sql.gz") || strings.HasSuffix(lower, "-schema-trigger.sql") {
		return false
	}
	if strings.HasSuffix(lower, "-schema-view.sql.gz") || strings.HasSuffix(lower, "-schema-view.sql") {
		return false
	}
	if strings.HasSuffix(lower, "-schema-routine.sql.gz") || strings.HasSuffix(lower, "-schema-routine.sql") {
		return false
	}
	if strings.HasSuffix(lower, "-schema-function.sql.gz") || strings.HasSuffix(lower, "-schema-function.sql") {
		return false
	}
	if strings.HasSuffix(lower, "-schema-procedure.sql.gz") || strings.HasSuffix(lower, "-schema-procedure.sql") {
		return false
	}
	if strings.HasSuffix(lower, "-schema-event.sql.gz") || strings.HasSuffix(lower, "-schema-event.sql") {
		return false
	}
	return true
}
