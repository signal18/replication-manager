package splitdump

import (
	"path/filepath"
	"sort"
)

// SourceEntry is a single restorable entry provided by a RestoreSource.
// For filesystem sources, Path is a real file path. For stream sources,
// Path is a logical entry identifier passed as-is to RestoreFileWithContext.
type SourceEntry struct {
	Path     string
	IsSchema bool
	GroupKey string // grouping key for parallel data dispatch
	ShardIdx int    // shard ordering within group (lower = restore first)
}

// RestoreSource abstracts entry discovery for splitdump restore orchestration.
// Implementations include FilesystemSource (directory on disk) and
// StreamContainerSource (stream container preflight index).
type RestoreSource interface {
	// Entries returns all restorable entries for the backup. Schema entries
	// should precede data entries in the returned slice. restoreUser controls
	// whether mysql system user entries are included (same semantics as ListFiles).
	Entries(restoreUser bool) ([]SourceEntry, error)

	// Metadata returns binlog position/GTID information for post-restore
	// application, or nil if no metadata is available. The error semantics
	// match those of ReadMetadata: os.ErrNotExist if not found,
	// ErrMetadataInvalid if malformed.
	Metadata() (*Metadata, error)
}

// FilesystemSource is a RestoreSource backed by a local backup directory.
// It wraps the existing ListFiles and ReadMetadata functions and preserves
// all current filesystem restore behavior.
type FilesystemSource struct {
	BackupPath string
}

// Entries reads schema and data files from BackupPath using the existing
// ListFiles function. Data entries carry GroupKey and ShardIdx derived from
// the splitdump filename convention via splitdumpDataKey.
func (s *FilesystemSource) Entries(restoreUser bool) ([]SourceEntry, error) {
	files, err := ListFiles(s.BackupPath, restoreUser)
	if err != nil {
		return nil, err
	}

	entries := make([]SourceEntry, 0, len(files.Schema)+len(files.Data))

	for _, p := range files.Schema {
		entries = append(entries, SourceEntry{Path: p, IsSchema: true})
	}

	for _, p := range files.Data {
		key, shard := splitdumpDataKey(filepath.Base(p))
		entries = append(entries, SourceEntry{
			Path:     p,
			IsSchema: false,
			GroupKey: key,
			ShardIdx: shard,
		})
	}

	return entries, nil
}

// Metadata reads binlog position/GTID from the backup directory metadata file.
func (s *FilesystemSource) Metadata() (*Metadata, error) {
	return ReadMetadata(s.BackupPath)
}

// StreamEntry describes one stream container entry for splitdump restore.
// The caller converts backupmgr.StreamEntryIndex entries to StreamEntry,
// mapping GroupHint → GroupKey and OrderHint → ShardIdx. This keeps the
// splitdump package free of a direct dependency on backupmgr.
type StreamEntry struct {
	Path     string
	IsSchema bool
	GroupKey string
	ShardIdx int
}

// StreamContainerSource is a RestoreSource backed by stream container entries.
// Entries are provided by the caller (typically from a DirectoryRestorePlan)
// via the StreamEntries field. No metadata is available from stream containers.
type StreamContainerSource struct {
	StreamEntries []StreamEntry
}

// Entries converts the StreamEntries to SourceEntry slices. The restoreUser
// parameter has no effect: all declared entries are returned regardless.
func (s *StreamContainerSource) Entries(_ bool) ([]SourceEntry, error) {
	result := make([]SourceEntry, 0, len(s.StreamEntries))
	for _, e := range s.StreamEntries {
		result = append(result, SourceEntry{
			Path:     e.Path,
			IsSchema: e.IsSchema,
			GroupKey: e.GroupKey,
			ShardIdx: e.ShardIdx,
		})
	}
	return result, nil
}

// Metadata always returns (nil, nil): stream containers carry no binlog metadata.
func (s *StreamContainerSource) Metadata() (*Metadata, error) {
	return nil, nil
}

// groupSourceDataEntries groups data SourceEntry slices by GroupKey. Within each
// group, entries are ordered ascending by ShardIdx. Groups are ordered by GroupKey
// alphabetically — matching the sort order produced by sortSplitdumpDataFiles for
// filesystem sources.
func groupSourceDataEntries(entries []SourceEntry) []dataGroup {
	if len(entries) == 0 {
		return nil
	}

	// Sort by GroupKey (alphabetically) then ShardIdx (ascending).
	sorted := make([]SourceEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].GroupKey != sorted[j].GroupKey {
			return sorted[i].GroupKey < sorted[j].GroupKey
		}
		return sorted[i].ShardIdx < sorted[j].ShardIdx
	})

	// Group consecutive entries with the same GroupKey.
	var groups []dataGroup
	var current dataGroup
	for _, e := range sorted {
		if current.key == "" {
			current = dataGroup{key: e.GroupKey, paths: []string{e.Path}}
			continue
		}
		if e.GroupKey == current.key {
			current.paths = append(current.paths, e.Path)
			continue
		}
		groups = append(groups, current)
		current = dataGroup{key: e.GroupKey, paths: []string{e.Path}}
	}
	if current.key != "" {
		groups = append(groups, current)
	}
	return groups
}
