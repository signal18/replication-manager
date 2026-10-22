package pbscommon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNotCatalog is returned by ParseCatalog when the buffer does not start with
// the pcat1 magic, i.e. it is not a Proxmox catalog stream.
var ErrNotCatalog = errors.New("not a pcat1 catalog (magic mismatch)")

// CatalogEntry is a single file or directory listed in a pcat1 catalog. Paths
// are archive-relative with forward slashes, matching PXARReader.ListEntries so
// the two listing paths are interchangeable.
type CatalogEntry struct {
	Path    string
	IsDir   bool
	Size    uint64
	ModTime int64
}

// Guards against malformed or hostile catalogs: bound recursion and total work.
const (
	catalogMaxDepth   = 4096
	catalogMaxEntries = 50_000_000
)

// catalogDirRef points at a child directory's table within the catalog buffer.
type catalogDirRef struct {
	name  string
	start uint64
}

// catalogFileRef is a file entry read from a directory table, before its parent
// path is known.
type catalogFileRef struct {
	name  string
	size  uint64
	mtime uint64
}

// readVarint decodes one unsigned LEB128 value (the append_u64_7bit encoding the
// catalog writer uses) from data[pos:end], returning the value and the offset
// just past it. Bounding reads to end keeps a truncated table from consuming
// bytes that belong to the next table or the trailing pointer.
func readVarint(data []byte, pos, end uint64) (uint64, uint64, error) {
	if end > uint64(len(data)) {
		end = uint64(len(data))
	}
	var v uint64
	var shift uint
	for {
		if pos >= end {
			return 0, 0, fmt.Errorf("varint truncated at offset %d", pos)
		}
		b := data[pos]
		pos++
		// The 10th byte (shift 63) may only contribute bit 0; anything more
		// would silently truncate when shifted into the uint64.
		if shift == 63 && b&0x7f > 1 {
			return 0, 0, fmt.Errorf("varint overflows uint64 at offset %d", pos-1)
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 64 {
			return 0, 0, fmt.Errorf("varint overflows uint64 at offset %d", pos)
		}
	}
	return v, pos, nil
}

// ParseCatalog decodes a fully-buffered pcat1 catalog stream into the flat list
// of every file and directory it contains. The buffer is the reconstructed
// contents of catalog.pcat1.didx: an 8-byte magic, then per-directory tables
// written leaves-first, then a trailing table whose single entry references the
// root directory, then an 8-byte little-endian pointer to that trailing table.
//
// Listing a snapshot this way fetches only the few MB of the catalog instead of
// re-reading the whole (potentially multi-hundred-GB) data archive.
func ParseCatalog(data []byte) ([]CatalogEntry, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("catalog too short: %d bytes", len(data))
	}
	if !bytes.Equal(data[:8], catalog_magic) {
		return nil, ErrNotCatalog
	}
	rootPtr := binary.LittleEndian.Uint64(data[len(data)-8:])
	if rootPtr < 8 || rootPtr > uint64(len(data))-8 {
		return nil, fmt.Errorf("catalog root pointer %d out of range (len %d)", rootPtr, len(data))
	}

	// The trailing table holds exactly one 'd' entry pointing at the root dir.
	dirs, files, err := parseCatalogTable(data, rootPtr)
	if err != nil {
		return nil, fmt.Errorf("trailer table: %w", err)
	}
	if len(files) != 0 || len(dirs) != 1 {
		return nil, fmt.Errorf("catalog trailer malformed: %d dirs, %d files (expected 1 dir)", len(dirs), len(files))
	}

	entries := make([]CatalogEntry, 0, 256)
	if err := walkCatalog(data, dirs[0].start, "", 0, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// parseCatalogTable parses the directory table whose length prefix begins at
// tableStart, returning its subdirectory references and file entries. Child
// directory offsets are stored relative to tableStart and point backwards
// (children are written before their parent), which the caller relies on to
// guarantee termination.
func parseCatalogTable(data []byte, tableStart uint64) ([]catalogDirRef, []catalogFileRef, error) {
	// The length prefix itself is not table-bounded yet, so read it against the
	// whole buffer; everything after is bounded to this table's [pos, end).
	tableLen, pos, err := readVarint(data, tableStart, uint64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	end := pos + tableLen
	// Bound to the region before the trailing 8-byte root pointer so a table can
	// never overlap (and thus alias) the root pointer. len(data) >= 16 is checked
	// by the only caller chain (ParseCatalog), so len-8 cannot underflow here.
	if end < pos || end > uint64(len(data))-8 {
		return nil, nil, fmt.Errorf("table at %d overruns buffer (len %d)", tableStart, tableLen)
	}

	count, pos, err := readVarint(data, pos, end)
	if err != nil {
		return nil, nil, err
	}

	// Do NOT preallocate by count: count is read from the (possibly hostile)
	// stream, so a huge value would panic make before the per-entry bounds
	// checks below ever run. append grows the slices safely instead.
	var dirs []catalogDirRef
	var files []catalogFileRef
	for i := uint64(0); i < count; i++ {
		if pos >= end {
			return nil, nil, fmt.Errorf("table at %d: ran out of bytes after %d/%d entries", tableStart, i, count)
		}
		typ := data[pos]
		pos++

		nameLen, np, err := readVarint(data, pos, end)
		if err != nil {
			return nil, nil, err
		}
		pos = np
		if pos+nameLen > end || pos+nameLen < pos {
			return nil, nil, fmt.Errorf("table at %d: name length %d overruns table", tableStart, nameLen)
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen

		switch typ {
		case 'd':
			delta, np, err := readVarint(data, pos, end)
			if err != nil {
				return nil, nil, err
			}
			pos = np
			if delta > tableStart {
				return nil, nil, fmt.Errorf("dir %q backward offset %d underflows table start %d", name, delta, tableStart)
			}
			dirs = append(dirs, catalogDirRef{name: name, start: tableStart - delta})
		case 'f':
			size, np, err := readVarint(data, pos, end)
			if err != nil {
				return nil, nil, err
			}
			pos = np
			mtime, np, err := readVarint(data, pos, end)
			if err != nil {
				return nil, nil, err
			}
			pos = np
			files = append(files, catalogFileRef{name: name, size: size, mtime: mtime})
		default:
			return nil, nil, fmt.Errorf("table at %d: unknown entry type %q", tableStart, typ)
		}
	}
	if pos != end {
		return nil, nil, fmt.Errorf("table at %d: %d trailing bytes after %d entries", tableStart, end-pos, count)
	}
	return dirs, files, nil
}

// walkCatalog recursively emits the entries of the directory whose table starts
// at dirStart, prefixing each with the directory's archive-relative path.
func walkCatalog(data []byte, dirStart uint64, prefix string, depth int, entries *[]CatalogEntry) error {
	if depth > catalogMaxDepth {
		return fmt.Errorf("catalog nesting exceeds %d levels", catalogMaxDepth)
	}
	dirs, files, err := parseCatalogTable(data, dirStart)
	if err != nil {
		return err
	}

	for _, f := range files {
		if len(*entries) >= catalogMaxEntries {
			return fmt.Errorf("catalog exceeds %d entries", catalogMaxEntries)
		}
		*entries = append(*entries, CatalogEntry{
			Path:    joinArchivePath(prefix, f.name),
			IsDir:   false,
			Size:    f.size,
			ModTime: int64(f.mtime),
		})
	}

	for _, d := range dirs {
		// Children are written before their parents, so a child table must start
		// strictly before this one. Anything else is a malformed back-reference
		// that could otherwise loop forever.
		if d.start >= dirStart {
			return fmt.Errorf("catalog dir %q at %d does not precede parent at %d", d.name, d.start, dirStart)
		}
		if len(*entries) >= catalogMaxEntries {
			return fmt.Errorf("catalog exceeds %d entries", catalogMaxEntries)
		}
		p := joinArchivePath(prefix, d.name)
		*entries = append(*entries, CatalogEntry{Path: p, IsDir: true})
		if err := walkCatalog(data, d.start, p, depth+1, entries); err != nil {
			return err
		}
	}
	return nil
}
