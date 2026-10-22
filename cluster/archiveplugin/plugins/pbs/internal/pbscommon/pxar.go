package pbscommon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path"
	"sort"
	"strings"

	//	"io/ioutil"
	"path/filepath"

	"github.com/dchest/siphash"
)

const (
	PXAR_ENTRY               uint64 = 0xd5956474e588acef
	PXAR_ENTRY_V1            uint64 = 0x11da850a1c1cceff
	PXAR_FILENAME            uint64 = 0x16701121063917b3
	PXAR_SYMLINK             uint64 = 0x27f971e7dbf5dc5f
	PXAR_DEVICE              uint64 = 0x9fc9e906586d5ce9
	PXAR_XATTR               uint64 = 0x0dab0229b57dcd03
	PXAR_ACL_USER            uint64 = 0x2ce8540a457d55b8
	PXAR_ACL_GROUP           uint64 = 0x136e3eceb04c03ab
	PXAR_ACL_GROUP_OBJ       uint64 = 0x10868031e9582876
	PXAR_ACL_DEFAULT         uint64 = 0xbbbb13415a6896f5
	PXAR_ACL_DEFAULT_USER    uint64 = 0xc89357b40532cd1f
	PXAR_ACL_DEFAULT_GROUP   uint64 = 0xf90a8a5816038ffe
	PXAR_FCAPS               uint64 = 0x2da9dd9db5f7fb67
	PXAR_QUOTA_PROJID        uint64 = 0xe07540e82f7d1cbb
	PXAR_HARDLINK            uint64 = 0x51269c8422bd7275
	PXAR_PAYLOAD             uint64 = 0x28147a1b0b7c1a25
	PXAR_GOODBYE             uint64 = 0x2fec4fa642d5731d
	PXAR_GOODBYE_TAIL_MARKER uint64 = 0xef5eed5b753e1555
)

var catalog_magic = []byte{145, 253, 96, 249, 196, 103, 88, 213}

// Windows system folders to exclude automatically from backups
// These folders contain VSS snapshots, recycle bin, and other system data
// that should not be included in file-mode backups
var excludedSystemFolders = []string{
	"System Volume Information", // VSS snapshots storage
	"$RECYCLE.BIN",               // Windows recycle bin
	"Recovery",                   // Windows recovery partition data
}

// Windows system files to exclude automatically from backups
// These are large paging/hibernation files that should not be backed up
var excludedSystemFiles = []string{
	"pagefile.sys",  // Windows page file
	"hiberfil.sys",  // Hibernation file
	"swapfile.sys",  // Windows swap file
	"DumpStack.log.tmp", // Crash dump temporary file
}

// shouldSkipSystemFolder checks if a folder should be automatically excluded
// Uses case-insensitive comparison for Windows compatibility
func shouldSkipSystemFolder(name string) bool {
	for _, excluded := range excludedSystemFolders {
		if strings.EqualFold(name, excluded) {
			return true
		}
	}
	return false
}

// shouldSkipSystemFile checks if a file should be automatically excluded
// Uses case-insensitive comparison for Windows compatibility
func shouldSkipSystemFile(name string) bool {
	for _, excluded := range excludedSystemFiles {
		if strings.EqualFold(name, excluded) {
			return true
		}
	}
	return false
}

// normExcludePath lowercases, converts backslashes to forward slashes and trims a
// trailing slash so Windows/Unix paths and patterns compare uniformly.
func normExcludePath(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimSuffix(s, "/")
	return strings.ToLower(s)
}

// relExcludePath returns full relative to root (both normalized). If full is not
// under root it returns the normalized full path unchanged. Used both to make a
// walked entry relative to the walked root and to make an absolute exclusion
// pattern relative to the original (logical) backup root.
func relExcludePath(root, full string) string {
	r := normExcludePath(root)
	f := normExcludePath(full)
	if r != "" && strings.HasPrefix(f, r+"/") {
		return f[len(r)+1:]
	}
	if f == r {
		return ""
	}
	return f
}

// isExcluded reports whether an entry matches any user exclusion pattern.
//   - A glob without a separator matches the entry basename anywhere in the tree
//     ("*.tmp", "node_modules") — basename match ANYWHERE in the tree.
//   - A pattern containing a separator is ANCHORED to the backup root (absolute
//     patterns via excludeRoot, e.g. "C:\\Users\\Alice\\Temp" -> "temp" when the
//     root is "C:\\Users\\Alice") and matched against the entry's relative path as
//     a path glob via path.Match ("logs/*.tmp"), or as an exact match / subtree
//     prefix. It is never a basename-anywhere match, so an anchored pattern that
//     reduces to a single component (e.g. "temp") does not match a nested "x/temp".
//
// path.Match (always '/') is used rather than filepath.Match because paths are
// pre-normalized to forward slashes, so matching must not depend on the host OS.
func isExcluded(rel, name, excludeRoot string, patterns []string) bool {
	relN := normExcludePath(rel)
	nameN := strings.ToLower(name)
	for _, pat := range patterns {
		if strings.TrimSpace(pat) == "" {
			continue
		}
		hasGlob := strings.ContainsAny(pat, "*?[")
		hasSep := strings.ContainsAny(pat, "/\\")

		if !hasSep {
			// Separatorless pattern: match the basename anywhere in the tree.
			bn := normExcludePath(pat)
			if hasGlob {
				if ok, _ := path.Match(bn, nameN); ok {
					return true
				}
			} else if nameN == bn {
				return true
			}
			continue
		}

		// Pattern has a separator: anchor it to the backup root and match the
		// entry's relative path. Never a basename-anywhere match.
		p := relExcludePath(excludeRoot, pat)
		if p == "" {
			continue
		}
		if hasGlob {
			if ok, _ := path.Match(p, relN); ok {
				return true
			}
			continue
		}
		if relN == p || strings.HasPrefix(relN, p+"/") {
			return true
		}
	}
	return false
}

const (
	IFMT   uint64 = 0o0170000
	IFSOCK uint64 = 0o0140000
	IFLNK  uint64 = 0o0120000
	IFREG  uint64 = 0o0100000
	IFBLK  uint64 = 0o0060000
	IFDIR  uint64 = 0o0040000
	IFCHR  uint64 = 0o0020000
	IFIFO  uint64 = 0o0010000

	ISUID uint64 = 0o0004000
	ISGID uint64 = 0o0002000
	ISVTX uint64 = 0o0001000
)

type MTime struct {
	secs    uint64
	nanos   uint32
	padding uint32
}
type PXARFileEntry struct {
	hdr   uint64
	len   uint64
	mode  uint64
	flags uint64
	uid   uint32
	gid   uint32
	mtime MTime
}

type PXARFilenameEntry struct {
	hdr uint64
	len uint64
}

type GoodByeItem struct {
	hash   uint64
	offset uint64
	len    uint64
}

type GoodByeBST struct {
	self  *GoodByeItem
	left  *GoodByeBST
	right *GoodByeBST
}

func (B *GoodByeBST) AddNode(i *GoodByeItem) {
	if i.hash < B.self.hash {
		if B.left == nil {
			B.left = &GoodByeBST{
				self: i,
			}
		} else {
			B.left.AddNode(i)
		}
	}
	if i.hash > B.self.hash {
		if B.right == nil {
			B.right = &GoodByeBST{
				self: i,
			}
		} else {
			B.right.AddNode(i)
		}
	}
}

func pow_of_2(e uint64) uint64 {
	return 1 << e
}

func log_of_2(k uint64) uint64 {
	return 8*8 - uint64(bits.LeadingZeros64(k)) - 1
}

func make_bst_inner(input []GoodByeItem, n uint64, e uint64, output *[]GoodByeItem, i uint64) {
	if n == 0 {
		return
	}
	p := pow_of_2(e - 1)
	q := pow_of_2(e)
	var k uint64
	if n >= p-1+p/2 {
		k = (q - 2) / 2
	} else {
		v := p - 1 + p/2 - n
		k = (q-2)/2 - v
	}

	(*output)[i] = input[k]

	make_bst_inner(input, k, e-1, output, i*2+1)
	make_bst_inner(input[k+1:], n-k-1, e-1, output, i*2+2)
}

func ca_make_bst(input []GoodByeItem, output *[]GoodByeItem) {
	n := uint64(len(input))
	make_bst_inner(input, n, log_of_2(n)+1, output, 0)
}

type PXAROutCB func([]byte) error

// MetaCollector is an optional hook invoked during the PXAR walk for every
// directory and regular file that is actually being backed up (after skip
// checks). Implementations capture per-entry metadata that PXAR itself cannot
// represent — the primary use case is NTFS ACLs/owner/attributes on Windows.
//
// The walk is single-threaded, so implementations do not need to be
// concurrency-safe with respect to the walk itself. Collect errors are logged
// but never abort the backup: metadata capture is best-effort and must never
// cause an otherwise-successful backup to fail.
type MetaCollector interface {
	Collect(absPath string, info os.FileInfo, isDir bool) error
}

type PXARArchive struct {
	//Create(filename string, WriteCB PXAROutCB)
	//AddFile(filename string)
	//AddDirectory(dirname string)
	WriteCB        PXAROutCB
	CatalogWriteCB PXAROutCB
	buffer         bytes.Buffer
	pos            uint64
	ArchiveName    string

	catalog_pos  uint64
	SkippedFiles []string // ALL skips (read errors, junctions, system auto-excludes) — for logging/sidecar display
	// ReadErrors is the OUTCOME-affecting subset of SkippedFiles: genuine read
	// failures (cannot stat/read/open) and content instability (file shrank or grew
	// during read). Expected skips (system auto-excludes, junctions) are NOT here,
	// so they don't downgrade a backup from verified_success (v2-H-02).
	ReadErrors []string

	// ExcludeList holds user-configured exclusion patterns (H-04). Patterns are
	// matched against each child entry; matches are pruned from the archive and
	// recorded in ExcludedFiles. Set by the caller before WriteDir.
	ExcludeList []string
	// ExcludedFiles records the full paths excluded by ExcludeList (policy, not an
	// error) so they can be surfaced distinctly from SkippedFiles in the status.
	ExcludedFiles []string
	// ExcludeRoot is the ORIGINAL logical backup root (e.g. "C:\\Users\\Alice"),
	// set by the caller. Absolute exclusion patterns are relativized against it so
	// "C:\\Users\\Alice\\Temp" matches when backing up "C:\\Users\\Alice". Under VSS
	// the walked path differs from this, which is why pattern and entry are both
	// reduced to paths relative to their respective roots before comparison.
	ExcludeRoot string
	// root is the toplevel WALKED path, captured on the first WriteDir call, used to
	// compute each entry's path relative to the (possibly VSS shadow-copy) root.
	root string

	// VirtualFiles are injected at the root of the archive before real files.
	// Key = filename (e.g. ".proxmox_backup_client_meta.json"), Value = content bytes.
	VirtualFiles map[string][]byte

	// MetaCollector, if set, is called for every directory and file that is
	// actually backed up. Used to capture NTFS ACLs and other per-file metadata
	// that PXAR cannot represent. Best-effort: errors are logged and ignored.
	MetaCollector MetaCollector
}

//This function will flush the internal buffer and update position
//WriteCB for pxar stream will be called.
//It is useful when we building a data structure and we need to keep a specific offset and output it only at the end

func (a *PXARArchive) Flush() error {

	b := make([]byte, 64*1024)
	for {
		count, _ := a.buffer.Read(b)
		if count <= 0 {
			break
		}
		if err := a.WriteCB(b[:count]); err != nil {
			return fmt.Errorf("failed to write PXAR data: %w", err)
		}
		a.pos = a.pos + uint64(count)
	}
	//fmt.Printf("Flush %d bytes\n", count)
	return nil
}

func (a *PXARArchive) Create() {
	a.pos = 0
	a.catalog_pos = 8
}

type CatalogDir struct {
	Pos  uint64 //Points to next table so parent has always to be written before children
	Name string
}

type CatalogFile struct {
	Name  string
	MTime uint64
	Size  uint64
}

func append_u64_7bit(a []byte, v uint64) []byte {
	x := a
	for {
		if v < 128 {
			x = append(x, byte(v&0x7f))
			break
		}
		x = append(x, byte(v&0x7f)|byte(0x80))
		v = v >> 7
	}
	return x
}

//PXAR format, documentation had many missing bits i had to figure out
/*
	Suppose we have
	abc
		file.txt
		ced
			file2.txt
			file3.txt

	First entry is always without filename

	PXAR_ENTRY(DIR)
		PXAR_FILENAME(file.txt)
		PXAR_ENTRY(file, attributes etc)
		PXAR_PAYLOAD(file.txt)
		PXAR_FILENAME(ced)
			PXAR_FILENAME(file2.txt)
			PXAR_ENTRY(file,attributes etc)
			PXAR_PAYLOAD(file2.txt)
			PXAR_FILENAME(file3.txt)
			PXAR_ENTRY(file,attributes etc)
			PXAR_PAYLOAD(file3.txt)
			PXAR_GOODBYE( relative to ced
				will have entries sorted using casync algorithms below
				for sip hash of "file2.txt" and "file3.txt", offset is relative to PXAR_GOODBYE header offset
				last special entry with fixed hash and not sorted
			)
		PXAR_GOODBYE(relative to abc or top dir )
			will have entries sorted using casync algorithms below
			for sip hash of "file.txt" and "ced", offset is relative to PXAR_GOODBYE header offset
			last special entry with fixed hash and not sorted
		)

*/

// addReadError records a genuine read failure or content instability: it is added
// to both SkippedFiles (display) and ReadErrors (which downgrades the backup
// outcome below verified_success). Expected skips append to SkippedFiles directly.
func (a *PXARArchive) addReadError(msg string) {
	a.SkippedFiles = append(a.SkippedFiles, msg)
	a.ReadErrors = append(a.ReadErrors, msg)
}

func (a *PXARArchive) WriteDir(path string, dirname string, toplevel bool) (CatalogDir, error) {
	//fmt.Printf("Write dir %s at %d\n", path, a.pos)

	// Capture the backup root so exclusion patterns can be matched against the
	// path relative to it (VSS-safe — see relExcludePath/isExcluded).
	if toplevel {
		a.root = path
	}

	// Check if directory is a junction point/symlink before reading it
	fileInfo, err := os.Lstat(path)
	if err != nil {
		if toplevel {
			// Toplevel directory MUST be accessible — this is a fatal error
			return CatalogDir{}, fmt.Errorf("cannot stat backup root directory: %s: %w", path, err)
		}
		// Sub-directories: skip and continue backup
		skipMsg := fmt.Sprintf("Cannot stat directory: %s (Error: %v)", path, err)
		a.addReadError(skipMsg)
		return CatalogDir{}, nil
	}

	// Skip directory junction points to avoid infinite loops and access errors
	if !toplevel && fileInfo.Mode()&os.ModeSymlink != 0 {
		skipMsg := fmt.Sprintf("Junction point (skipped): %s", path)
		a.SkippedFiles = append(a.SkippedFiles, skipMsg)
		return CatalogDir{}, nil // Return nil error to continue backup
	}

	files, err := os.ReadDir(path)
	if err != nil {
		if toplevel {
			// Toplevel directory MUST be readable — this is a fatal error
			return CatalogDir{}, fmt.Errorf("cannot read backup root directory: %s: %w", path, err)
		}
		// Sub-directories: skip and continue backup
		skipMsg := fmt.Sprintf("Cannot read directory: %s (Error: %v)", path, err)
		a.addReadError(skipMsg)
		return CatalogDir{}, nil
	}

	//Avoid writing filename entry on root
	if !toplevel {
		fname_entry := &PXARFilenameEntry{
			hdr: PXAR_FILENAME,
			len: uint64(16) + uint64(len(dirname)) + 1,
		}

		_ = binary.Write(&a.buffer, binary.LittleEndian, fname_entry)

		a.buffer.WriteString(dirname)
		a.buffer.WriteByte(0x00)
	} else {
		if a.CatalogWriteCB != nil {
			if err := a.CatalogWriteCB(catalog_magic); err != nil {
				return CatalogDir{}, fmt.Errorf("failed to write catalog magic: %w", err)
			}
			a.catalog_pos = 8
		}
	}

	if err := a.Flush(); err != nil {
		return CatalogDir{}, err
	}

	dir_start_pos := a.pos

	// Permissions stay fixed (the project targets Windows, which has no
	// Unix execute/traverse bits); the owner is the real one on Unix.
	uid, gid := fileOwner(fileInfo)
	entry := &PXARFileEntry{
		hdr:   PXAR_ENTRY,
		len:   56,
		mode:  IFDIR | 0o777,
		flags: 0,
		uid:   uid,
		gid:   gid,
		mtime: MTime{
			secs:    uint64(fileInfo.ModTime().Unix()),
			nanos:   0,
			padding: 0,
		},
	}
	_ = binary.Write(&a.buffer, binary.LittleEndian, entry)

	if err := a.Flush(); err != nil {
		return CatalogDir{}, err
	}

	// Capture per-directory metadata (NTFS ACLs on Windows). Best-effort.
	if a.MetaCollector != nil {
		if err := a.MetaCollector.Collect(path, fileInfo, true); err != nil {
			a.SkippedFiles = append(a.SkippedFiles,
				fmt.Sprintf("Metadata collect failed for dir %s: %v", path, err))
		}
	}

	goodbyteitems := make([]GoodByeItem, 0)
	catalog_files := make([]CatalogFile, 0)
	catalog_dirs := make([]CatalogDir, 0)

	// Inject virtual files (metadata) at the root of the archive
	if toplevel && len(a.VirtualFiles) > 0 {
		// Sort keys for deterministic output
		vfNames := make([]string, 0, len(a.VirtualFiles))
		for name := range a.VirtualFiles {
			vfNames = append(vfNames, name)
		}
		sort.Strings(vfNames)

		now := uint64(entry.mtime.secs)
		for _, name := range vfNames {
			startpos := a.pos
			F, err := a.WriteVirtualFile(name, a.VirtualFiles[name], now)
			if err != nil {
				return CatalogDir{}, fmt.Errorf("failed to write virtual file %s: %w", name, err)
			}
			catalog_files = append(catalog_files, F)
			goodbyteitems = append(goodbyteitems, GoodByeItem{
				offset: startpos,
				hash:   siphash.Hash(0x83ac3f1cfbb450db, 0xaa4f1b6879369fbd, []byte(name)),
				len:    a.pos - startpos,
			})
		}
	}

	for _, file := range files {
		startpos := a.pos

		// User-configured exclusions (H-04): prune the entry (and its subtree for
		// directories) before it enters the archive, recorded as policy not error.
		if len(a.ExcludeList) > 0 {
			childPath := filepath.Join(path, file.Name())
			if isExcluded(relExcludePath(a.root, childPath), file.Name(), a.ExcludeRoot, a.ExcludeList) {
				a.ExcludedFiles = append(a.ExcludedFiles, childPath)
				continue
			}
		}

		if file.IsDir() {
			// Skip Windows system folders (VSS snapshots, recycle bin, etc.)
			if shouldSkipSystemFolder(file.Name()) {
				skipMsg := fmt.Sprintf("System folder (auto-excluded): %s", filepath.Join(path, file.Name()))
				a.SkippedFiles = append(a.SkippedFiles, skipMsg)
				continue
			}

			D, err := a.WriteDir(filepath.Join(path, file.Name()), file.Name(), false)
			if err != nil {
				return CatalogDir{}, err
			}
			if a.pos == startpos {
				// WriteDir skipped this subdirectory (junction/unreadable): nothing
				// written, so don't add a phantom zero-value catalog/goodbye entry (M-06).
				continue
			}
			catalog_dirs = append(catalog_dirs, D)
			goodbyteitems = append(goodbyteitems, GoodByeItem{
				offset: startpos,
				hash:   siphash.Hash(0x83ac3f1cfbb450db, 0xaa4f1b6879369fbd, []byte(file.Name())),
				len:    a.pos - startpos,
			})
		} else {
			// Skip Windows system files (pagefile, hiberfil, etc.)
			if shouldSkipSystemFile(file.Name()) {
				skipMsg := fmt.Sprintf("System file (auto-excluded): %s", filepath.Join(path, file.Name()))
				a.SkippedFiles = append(a.SkippedFiles, skipMsg)
				continue
			}

			F, err := a.WriteFile(filepath.Join(path, file.Name()), file.Name())
			if err != nil {
				return CatalogDir{}, err
			}
			if a.pos == startpos {
				// WriteFile skipped this entry (unreadable/junction): nothing was
				// written, so don't add a phantom zero-value catalog/goodbye entry (M-06).
				continue
			}

			catalog_files = append(catalog_files, F)
			goodbyteitems = append(goodbyteitems, GoodByeItem{
				offset: startpos,
				hash:   siphash.Hash(0x83ac3f1cfbb450db, 0xaa4f1b6879369fbd, []byte(file.Name())),
				len:    a.pos - startpos,
			})
		}
	}

	//Here we can write AFTER the recursion so leaves get written first
	//We need to write leaves first because otherwise we won't know offsets
	oldpos := a.catalog_pos
	tabledata := make([]byte, 0)
	tabledata = append_u64_7bit(tabledata, uint64(len(catalog_files)+len(catalog_dirs)))
	for _, d := range catalog_dirs {
		tabledata = append(tabledata, 'd')
		tabledata = append_u64_7bit(tabledata, uint64(len(d.Name)))
		tabledata = append(tabledata, []byte(d.Name)...)
		tabledata = append_u64_7bit(tabledata, oldpos-d.Pos)
	}

	for _, f := range catalog_files {
		tabledata = append(tabledata, 'f')
		tabledata = append_u64_7bit(tabledata, uint64(len(f.Name)))
		tabledata = append(tabledata, []byte(f.Name)...)
		tabledata = append_u64_7bit(tabledata, f.Size)
		tabledata = append_u64_7bit(tabledata, f.MTime)
	}

	catalog_outdata := make([]byte, 0)
	catalog_outdata = append_u64_7bit(catalog_outdata, uint64(len(tabledata)))
	catalog_outdata = append(catalog_outdata, tabledata...)

	if a.CatalogWriteCB != nil {
		if err := a.CatalogWriteCB(catalog_outdata); err != nil {
			return CatalogDir{}, fmt.Errorf("failed to write catalog data: %w", err)
		}

	}

	a.catalog_pos += uint64(len(catalog_outdata))

	if err := a.Flush(); err != nil {
		return CatalogDir{}, err
	}

	//Sort goodbyeitems by sip hash to build later kinda of heap

	sort.Slice(goodbyteitems, func(i, j int) bool {
		return goodbyteitems[i].hash < goodbyteitems[j].hash
	})

	goodbyteitemsnew := make([]GoodByeItem, len(goodbyteitems))

	//Make casync binary search tree structure out of the sorted array

	ca_make_bst(goodbyteitems, &goodbyteitemsnew)

	goodbyteitems = goodbyteitemsnew

	if err := a.Flush(); err != nil {
		return CatalogDir{}, err
	}
	goodbye_start := a.pos

	_ = binary.Write(&a.buffer, binary.LittleEndian, PXAR_GOODBYE)
	goodbyelen := uint64(16 + 24*(len(goodbyteitems)+1))
	_ = binary.Write(&a.buffer, binary.LittleEndian, goodbyelen)

	for _, gi := range goodbyteitems {
		gi.offset = a.pos - gi.offset
		_ = binary.Write(&a.buffer, binary.LittleEndian, gi)
	}

	gi := &GoodByeItem{
		offset: goodbye_start - dir_start_pos,
		len:    goodbyelen,
		hash:   0xef5eed5b753e1555,
	}

	_ = binary.Write(&a.buffer, binary.LittleEndian, gi)

	if err := a.Flush(); err != nil {
		return CatalogDir{}, err
	}

	if toplevel {
		//We write special pointer to root dir here

		tabledata := make([]byte, 0)
		tabledata = append_u64_7bit(tabledata, uint64(1))
		tabledata = append(tabledata, 'd')
		tabledata = append_u64_7bit(tabledata, uint64(len(a.ArchiveName)))
		tabledata = append(tabledata, []byte(a.ArchiveName)...)
		tabledata = append_u64_7bit(tabledata, a.catalog_pos-oldpos)
		catalog_outdata := make([]byte, 0)
		catalog_outdata = append_u64_7bit(catalog_outdata, uint64(len(tabledata)))
		catalog_outdata = append(catalog_outdata, tabledata...)
		ptr := make([]byte, 0)
		ptr = binary.LittleEndian.AppendUint64(ptr, a.catalog_pos)
		if a.CatalogWriteCB != nil {
			if err := a.CatalogWriteCB(catalog_outdata); err != nil {
				return CatalogDir{}, fmt.Errorf("failed to write catalog toplevel data: %w", err)
			}
			if err := a.CatalogWriteCB(ptr); err != nil {
				return CatalogDir{}, fmt.Errorf("failed to write catalog pointer: %w", err)
			}
		}
	}

	return CatalogDir{
		Name: dirname,
		Pos:  oldpos,
	}, nil
}

// On pxar first item and consquently entry point must always be WriteDir , because toplevel is always a directory
// So backing up single file is not possible
func (a *PXARArchive) WriteFile(path string, basename string) (CatalogFile, error) {
	//fmt.Printf("Write file %s at %d\n", path, a.pos)

	// Use Lstat to detect symlinks/junction points without following them
	fileInfo, err := os.Lstat(path)
	if err != nil {
		// Log stat errors but continue backup - don't fail on inaccessible files
		skipMsg := fmt.Sprintf("Cannot stat file: %s (Error: %v)", path, err)
		a.addReadError(skipMsg)
		return CatalogFile{}, nil
	}

	// Skip junction points and symlinks (common on Windows: "Application Data", etc.)
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		skipMsg := fmt.Sprintf("Junction point (skipped): %s", path)
		a.SkippedFiles = append(a.SkippedFiles, skipMsg)
		return CatalogFile{}, nil // Return nil error to continue backup
	}

	file, err := os.Open(path)

	if err != nil {
		// Log file open errors but continue backup - don't fail on locked/system files
		skipMsg := fmt.Sprintf("Cannot open file: %s (Error: %v)", path, err)
		a.addReadError(skipMsg)
		return CatalogFile{}, nil
	}

	defer func() { _ = file.Close() }()

	// Capture per-file metadata (NTFS ACLs on Windows). Best-effort.
	if a.MetaCollector != nil {
		if err := a.MetaCollector.Collect(path, fileInfo, false); err != nil {
			a.SkippedFiles = append(a.SkippedFiles,
				fmt.Sprintf("Metadata collect failed for file %s: %v", path, err))
		}
	}

	fname_entry := &PXARFilenameEntry{
		hdr: PXAR_FILENAME,
		len: uint64(16) + uint64(len(basename)) + 1,
	}

	_ = binary.Write(&a.buffer, binary.LittleEndian, fname_entry)

	a.buffer.WriteString(basename)
	a.buffer.WriteByte(0x00)

	uid, gid := fileOwner(fileInfo)
	entry := &PXARFileEntry{
		hdr:   PXAR_ENTRY,
		len:   56,
		mode:  IFREG | 0o777,
		flags: 0,
		uid:   uid,
		gid:   gid,
		mtime: MTime{
			secs:    uint64(fileInfo.ModTime().Unix()),
			nanos:   0,
			padding: 0,
		},
	}
	_ = binary.Write(&a.buffer, binary.LittleEndian, entry)

	// The PXAR stream is a flat byte sequence: the next entry's header begins
	// immediately after exactly declaredSize payload bytes. We commit declaredSize
	// in the header here, so we MUST emit exactly that many bytes regardless of how
	// many the file actually yields. A file changing size between the Lstat above
	// and the read below (common for files in use WITHOUT VSS: logs, .pst, SQL .mdf)
	// would otherwise desynchronise the whole archive and corrupt every entry that
	// follows. So we cap reads at declaredSize and zero-pad any shortfall.
	_ = binary.Write(&a.buffer, binary.LittleEndian, PXAR_PAYLOAD)
	declaredSize := uint64(fileInfo.Size())
	filesize := declaredSize + 16 //Payload size + header size
	_ = binary.Write(&a.buffer, binary.LittleEndian, filesize)

	if err := a.Flush(); err != nil {
		return CatalogFile{}, err
	}

	readbuffer := make([]byte, 1024*64)
	var written uint64

	for written < declaredSize {
		toRead := uint64(len(readbuffer))
		if remaining := declaredSize - written; remaining < toRead {
			toRead = remaining
		}
		nread, err := file.Read(readbuffer[:toRead])
		if nread > 0 {
			a.buffer.Write(readbuffer[:nread])
			written += uint64(nread)
			if ferr := a.Flush(); ferr != nil {
				return CatalogFile{}, ferr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return CatalogFile{}, fmt.Errorf("failed to read from %s: %w", path, err)
		}
	}

	// File shrank since Lstat (or read short): pad with zeros so the emitted
	// payload matches the declared length and the stream stays aligned. Flag it as
	// a content-instability read error so the backup is not reported as fully
	// verified (v2-H-02) rather than silently passing.
	if written < declaredSize {
		a.addReadError(
			fmt.Sprintf("File shrank during backup, zero-padded to declared size (content inconsistent): %s", path))
		pad := make([]byte, 1024*64)
		for written < declaredSize {
			n := uint64(len(pad))
			if remaining := declaredSize - written; remaining < n {
				n = remaining
			}
			a.buffer.Write(pad[:n])
			written += n
			if ferr := a.Flush(); ferr != nil {
				return CatalogFile{}, ferr
			}
		}
	} else {
		// File grew after Lstat: we emitted only declaredSize bytes, so the appended
		// tail is NOT in the snapshot. The read loop stopped exactly at declaredSize
		// (capped), so probe one more byte to detect the growth and flag it — this
		// silent truncation otherwise passed as success (v2-H-02).
		var probe [1]byte
		if n, _ := file.Read(probe[:]); n > 0 {
			a.addReadError(
				fmt.Sprintf("File grew during backup, tail past %d bytes not captured (content incomplete): %s", declaredSize, path))
		}
	}

	if err := a.Flush(); err != nil {
		return CatalogFile{}, err
	}

	return CatalogFile{
		Name:  basename,
		MTime: uint64(fileInfo.ModTime().Unix()),
		Size:  uint64(fileInfo.Size()),
	}, nil
}

// WriteVirtualFile writes an in-memory file into the PXAR archive.
// Used for injecting metadata files (e.g. .proxmox_backup_client_meta.json) without a real file on disk.
func (a *PXARArchive) WriteVirtualFile(filename string, data []byte, mtime uint64) (CatalogFile, error) {
	fname_entry := &PXARFilenameEntry{
		hdr: PXAR_FILENAME,
		len: uint64(16) + uint64(len(filename)) + 1,
	}
	_ = binary.Write(&a.buffer, binary.LittleEndian, fname_entry)
	a.buffer.WriteString(filename)
	a.buffer.WriteByte(0x00)

	uid, gid := processOwner()
	entry := &PXARFileEntry{
		hdr:   PXAR_ENTRY,
		len:   56,
		mode:  IFREG | 0o444,
		flags: 0,
		uid:   uid,
		gid:   gid,
		mtime: MTime{
			secs:    mtime,
			nanos:   0,
			padding: 0,
		},
	}
	_ = binary.Write(&a.buffer, binary.LittleEndian, entry)

	_ = binary.Write(&a.buffer, binary.LittleEndian, PXAR_PAYLOAD)
	filesize := uint64(len(data)) + 16
	_ = binary.Write(&a.buffer, binary.LittleEndian, filesize)

	a.buffer.Write(data)

	if err := a.Flush(); err != nil {
		return CatalogFile{}, err
	}

	return CatalogFile{
		Name:  filename,
		MTime: mtime,
		Size:  uint64(len(data)),
	}, nil
}
