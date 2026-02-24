package splitdump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Logger                 func(level, format string, args ...any)
	RestoreFile            func(path string) error
	Context                context.Context
	RestoreFileWithContext func(ctx context.Context, path string) error
}

type FileSet struct {
	Schema []string
	Data   []string
}

type dataGroup struct {
	key   string
	paths []string
}

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
		isSchema, ok := classifyFile(name, restoreUser)
		if !ok {
			continue
		}
		fullPath := filepath.Join(backupPath, name)
		if isSchema {
			set.Schema = append(set.Schema, fullPath)
		} else {
			set.Data = append(set.Data, fullPath)
		}
	}
	sort.Strings(set.Schema)
	sortSplitdumpDataFiles(set.Data)
	return set, nil
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

	files, err := ListFiles(backupPath, opts.RestoreUser)
	if err != nil {
		return err
	}
	logf(LogInfo, "Splitdump restore files listed (schema=%d data=%d)", len(files.Schema), len(files.Data))

	for _, schemaFile := range files.Schema {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf(LogDebug, "Splitdump restoring schema (%s)", filepath.Base(schemaFile))
		var err error
		if opts.RestoreFileWithContext != nil {
			err = opts.RestoreFileWithContext(ctx, schemaFile)
		} else {
			err = opts.RestoreFile(schemaFile)
		}
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

	if len(files.Data) == 0 {
		logf(LogInfo, "Splitdump restore completed (no data files)")
		return nil
	}

	dataGroups := groupSplitdumpDataFiles(files.Data)
	if len(dataGroups) == 0 {
		logf(LogInfo, "Splitdump restore completed (no data files)")
		return nil
	}

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
					var err error
					if opts.RestoreFileWithContext != nil {
						err = opts.RestoreFileWithContext(ctx, path)
					} else {
						err = opts.RestoreFile(path)
					}
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

	if firstErr == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		logf(LogInfo, "Splitdump restore completed")
	}
	return firstErr
}

func classifyFile(name string, restoreUser bool) (isSchema bool, ok bool) {
	lower := strings.ToLower(name)
	if lower == "metadata" {
		return false, false
	}
	if strings.HasSuffix(lower, ".sql.gz") || strings.HasSuffix(lower, ".sql") {
		switch {
		case strings.HasSuffix(lower, "-schema.sql.gz") || strings.HasSuffix(lower, "-schema.sql"):
			return true, true
		case strings.HasSuffix(lower, "-schema-view.sql.gz") || strings.HasSuffix(lower, "-schema-view.sql"):
			return true, true
		case lower == "mysql.system-all.sql.gz" || lower == "mysql.system-all.sql":
			if !restoreUser {
				return false, false
			}
			return true, true
		default:
			return false, true
		}
	}
	return false, false
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
