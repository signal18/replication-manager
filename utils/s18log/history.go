// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package s18log

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// Default bounds applied when a caller passes a non-positive value. Per T18,
// bounding is always-on: "0/unset" means "use the default", never "unbounded".
const (
	DefaultHistoryMaxScanBytes = 50 * 1024 * 1024
	DefaultHistoryMaxLines     = 5000
	DefaultHistoryMaxFiles     = 20

	// maxScanLineBytes caps a single line so one pathological line can't
	// blow up scanner memory independent of the overall byte budget.
	maxScanLineBytes = 1 << 20 // 1MiB
)

// historyLevelBuckets mirrors LEVEL_BUCKETS in
// share/dashboard_react/src/Pages/Dashboard/components/Logs/index.jsx.
// Keep the two in sync: this is the server-side counterpart used to filter
// on-disk history by the same ERR/WARN/INFO/DBG buckets the GUI already
// buckets the live buffer into.
var historyLevelBuckets = map[string]string{
	"ERROR":   "ERR",
	"ALERT":   "ERR",
	"FATAL":   "ERR",
	"PANIC":   "ERR",
	"WARN":    "WARN",
	"WARNING": "WARN",
	"START":   "WARN",
	"STATE":   "WARN",
	"INFO":    "INFO",
	"TEST":    "INFO",
	"BENCH":   "INFO",
	"ALERTOK": "INFO",
	"DEBUG":   "DBG",
	"TRACE":   "DBG",
}

func bucketForLevel(level string) string {
	if b, ok := historyLevelBuckets[strings.ToUpper(level)]; ok {
		return b
	}
	return "OTHER"
}

// HistoryQuery bounds and filters a call to ReadHistory. Zero values for the
// bound fields (MaxScanBytes/MaxLines/MaxFiles) fall back to the package
// defaults above, never to "unbounded".
type HistoryQuery struct {
	Group   string          // cluster name, "" = no filter (global view)
	Levels  map[string]bool // bucket set (ERR/WARN/INFO/DBG); empty/nil = all
	Modules map[int]bool    // module ids; empty/nil = all
	// TaskSplit restricts to the general/task split cluster log buffers use
	// (config.IsTaskLogModule): "" = no restriction, "general" or "task"
	// restrict to that half. Applied during the scan, before Limit cuts the
	// scan off — unlike Modules (an arbitrary caller-supplied allow-list),
	// this must happen pre-limit: post-filtering an already limit-capped,
	// unclassified result can silently starve the minority class (e.g. rare
	// task events buried under a limit's worth of general noise).
	TaskSplit    string
	Text         string    // substring match on Text, case-insensitive
	Since        time.Time // zero = no lower bound, inclusive (ts >= Since)
	Until        time.Time // zero = no upper bound, exclusive (ts < Until) — see scanHistoryFile
	Limit        int       // max matches returned
	MaxScanBytes int64     // total bytes read across all files
	MaxFiles     int       // max rotated files opened
}

// HistoryResult is the outcome of a bounded on-disk log scan.
type HistoryResult struct {
	// Messages is newest-first, matching HttpLog.Buffer's convention (Shift
	// prepends), so callers can directly concatenate the live buffer with
	// Messages to extend it further into the past.
	Messages     []HttpMessage
	Truncated    bool // a bound (bytes/files) was hit before history was exhausted
	ScannedBytes int64
	ScannedFiles int
}

// historyFile is one candidate on-disk file to scan, in chronological order.
type historyFile struct {
	path string
	gzip bool
	// recency orders this file against candidates from OTHER base log files
	// (ReadHistoryFiles) — never needed to order files within a single base,
	// since listHistoryFiles already returns those in exact chronological
	// order via the rotation timestamp embedded in each backup's filename.
	// For a backup, recency IS that embedded timestamp (reliable — encodes
	// when lumberjack rotated the file, immune to a later chown/copy
	// touching mtime). For the active file, there is no such embedded
	// timestamp, so recency falls back to the directory entry's ModTime as
	// the best available proxy for "how recent is this file's content".
	recency time.Time
}

// lumberjackBackupRE builds the regexp matching backup filenames produced by
// s18log.NewRotateFileHook (gopkg.in/natefinch/lumberjack.v2, Compress:true)
// for a specific base log file: "{prefix}-{2006-01-02T15-04-05.000}{ext}" and
// the gzip'd "{...}{ext}.gz". ext is taken as a literal (not a generic
// "any extension" capture group) because lumberjack's own backupName omits
// the extension entirely when the configured log file has none (e.g.
// log-file = /var/log/repman) — the backup is "{prefix}-{timestamp}" or
// "{prefix}-{timestamp}.gz" with no dot before ".gz". A generic optional
// "(\.[^.]+)?" extension group would then be ambiguous with the trailing
// ".gz" itself (both start with a dot), so ext must be pinned to the known,
// literal value for this base file rather than re-derived from the pattern.
func lumberjackBackupRE(ext string) *regexp.Regexp {
	return regexp.MustCompile(`^(.+)-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d{3})` + regexp.QuoteMeta(ext) + `(\.gz)?$`)
}

const lumberjackBackupTimeFormat = "2006-01-02T15-04-05.000"

// listHistoryFiles enumerates the active log file plus its rotated backups
// (lumberjack naming, optionally gzip-compressed), oldest first, active file
// last.
func listHistoryFiles(baseLogFile string) ([]historyFile, error) {
	dir := filepath.Dir(baseLogFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return matchHistoryFiles(baseLogFile, entries), nil
}

// matchHistoryFiles is listHistoryFiles' matching/sorting logic, factored
// out to take an already-read directory listing instead of calling
// os.ReadDir itself — see ChownHistoryFilesBatch, which reads a shared
// directory once and matches several sibling base log files (main/security/
// workload/schema/maintenance) against that one listing instead of each
// independently re-reading the same directory.
func matchHistoryFiles(baseLogFile string, entries []os.DirEntry) []historyFile {
	dir := filepath.Dir(baseLogFile)
	base := filepath.Base(baseLogFile)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	backupRE := lumberjackBackupRE(ext)

	var backups []historyFile
	haveActive := false
	var activeModTime time.Time

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == base {
			haveActive = true
			if info, err := e.Info(); err == nil {
				activeModTime = info.ModTime()
			}
			continue
		}
		m := backupRE.FindStringSubmatch(name)
		if m == nil || m[1] != prefix {
			continue
		}
		t, err := time.Parse(lumberjackBackupTimeFormat, m[2])
		if err != nil {
			continue
		}
		backups = append(backups, historyFile{
			path:    filepath.Join(dir, name),
			gzip:    m[3] == ".gz",
			recency: t,
		})
	}

	sort.Slice(backups, func(i, j int) bool { return backups[i].recency.Before(backups[j].recency) })

	files := make([]historyFile, 0, len(backups)+1)
	files = append(files, backups...)
	if haveActive {
		files = append(files, historyFile{path: baseLogFile, recency: activeModTime})
	}
	return files
}

// ChownHistoryFiles transfers ownership of baseLogFile and its rotated
// backups to uid:gid. Intended for the root -> --user privilege drop
// (server.go): the active log file is opened for writing while still root,
// and an already-open fd keeps working for writes across a later
// setuid/setgid — but a *fresh* open (e.g. this package's own ReadHistory,
// browsing log history) re-checks permissions under the now-dropped UID and
// fails with "permission denied" unless ownership is also transferred, the
// same way the caller already does for Conf.WorkingDir. Must be called
// while still privileged enough to chown (root, or already owning the
// files). Best-effort: continues past a single failed/missing file (e.g. a
// backup deleted by a concurrent purge) and returns the first error, if any,
// rather than aborting the whole batch over one file.
func ChownHistoryFiles(baseLogFile string, uid, gid int) error {
	files, err := listHistoryFiles(baseLogFile)
	if err != nil {
		return err
	}
	return chownFiles(files, uid, gid)
}

// ChownHistoryFilesBatch is ChownHistoryFiles extended to several base log
// files at once (server.go's LimitPrivileges: the main log plus its
// security/workload/schema/maintenance siblings, all conventionally in the
// same directory). Base log files sharing a directory have that directory's
// os.ReadDir done exactly once and matched against each of them, instead of
// each calling ChownHistoryFiles independently and re-reading the same
// directory listing once per sibling. Best-effort per file, same as
// ChownHistoryFiles: continues past a single failed/missing file/directory
// and returns the first error, if any.
func ChownHistoryFilesBatch(baseLogFiles []string, uid, gid int) error {
	byDir := make(map[string][]string)
	var dirOrder []string
	for _, base := range baseLogFiles {
		if base == "" {
			continue
		}
		dir := filepath.Dir(base)
		if _, ok := byDir[dir]; !ok {
			dirOrder = append(dirOrder, dir)
		}
		byDir[dir] = append(byDir[dir], base)
	}

	var firstErr error
	for _, dir := range dirOrder {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, base := range byDir[dir] {
			if err := chownFiles(matchHistoryFiles(base, entries), uid, gid); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// chownFiles is the shared best-effort chown loop behind ChownHistoryFiles
// and ChownHistoryFilesBatch: continues past a single failed/missing file
// and returns the first error, if any, rather than aborting the whole batch.
func chownFiles(files []historyFile, uid, gid int) error {
	var firstErr error
	for _, f := range files {
		if err := os.Chown(f.path, uid, gid); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// parseLogfmtLine parses one logrus TextFormatter line ("key=value
// key=\"quoted value\" ...", space-separated, %q-quoted when a value has
// anything other than [A-Za-z0-9-._/@^+]) into a flat field map. Lines that
// don't look like logfmt (blank lines, stack traces, anything not produced by
// our own RotateFileHook formatter) return ok=false and are skipped by the
// caller rather than treated as an error — on-disk logs are for humans, not a
// strict wire format.
func parseLogfmtLine(line string) (map[string]string, bool) {
	fields := make(map[string]string)
	i, n := 0, len(line)
	for i < n {
		for i < n && line[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}
		keyStart := i
		for i < n && line[i] != '=' && line[i] != ' ' {
			i++
		}
		if i >= n || line[i] != '=' {
			// Malformed token (no '=') — not a logfmt line we understand.
			return nil, false
		}
		key := line[keyStart:i]
		i++ // skip '='

		var val string
		if i < n && line[i] == '"' {
			j := i + 1
			for j < n {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == '"' {
					break
				}
				j++
			}
			if j >= n {
				return nil, false
			}
			unquoted, err := strconv.Unquote(line[i : j+1])
			if err != nil {
				return nil, false
			}
			val = unquoted
			i = j + 1
		} else {
			valStart := i
			for i < n && line[i] != ' ' {
				i++
			}
			val = line[valStart:i]
		}
		fields[key] = val
	}
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

// levelStringFromLogfmt maps a logrus level= value to the uppercase bucket
// vocabulary the frontend already understands (see historyLevelBuckets /
// LEVEL_BUCKETS): "warning" lands directly on "WARNING", which the frontend's
// WARN bucket already lists, so no further translation is needed downstream.
func levelStringFromLogfmt(raw string) string {
	switch strings.ToLower(raw) {
	case "trace":
		return "DEBUG"
	case "fatal", "panic":
		return "ERROR"
	default:
		return strings.ToUpper(raw)
	}
}

func buildHttpMessage(fields map[string]string) (HttpMessage, bool) {
	timeStr, ok := fields["time"]
	if !ok {
		return HttpMessage{}, false
	}
	group := fields["cluster"]
	if group == "" {
		group = GroupNone
	}
	return HttpMessage{
		Group:     group,
		Level:     levelStringFromLogfmt(fields["level"]),
		Timestamp: timeStr,
		Text:      fields["msg"],
		Module:    config.ModuleFromTag(fields["module"]),
	}, true
}

// historyTimestampLayout matches the main log file's Formatter.TimestampFormat
// (server/server.go, the only NewRotateFileHook call site ReadHistory ever
// scans — see the Scope cut in doc/implementation/utils/s18log/
// LOG_HISTORY_READER.md). Carries the numeric UTC offset so a since/until
// comparison is correct regardless of what timezone the browser reading it is
// in — a bare wall-clock string is ambiguous the instant server and browser
// timezones differ.
const historyTimestampLayout = "2006-01-02 15:04:05 -0700"

// historyTimestampLayoutLegacy has no offset — how every log file wrote
// timestamps before historyTimestampLayout gained one. Rotated files written
// before that change won't parse against the new layout; falling back here
// keeps them readable during the transition (they age out on their own via
// log-rotate-max-age). time.Parse defaults a zone-less layout's result to
// UTC, which is wrong (it's actually the server's local time) but matches
// this package's pre-existing behavior for that data — no regression, just
// not yet correct for files old enough to predate the fix.
const historyTimestampLayoutLegacy = "2006-01-02 15:04:05"

// parseHistoryTimestamp parses s and reports whether the parse succeeded, and
// separately whether it succeeded via the *exact* (offset-aware) layout as
// opposed to the legacy fallback. That second bool matters beyond just
// "is this value precise": an active log file can, for exactly one rotation
// window per server upgrade, contain legacy lines followed by exact ones —
// restarting the process to pick up the new TimestampFormat does not itself
// rotate the file, so old (pre-upgrade) and new (post-upgrade) lines can
// share one file until it next rotates on size. Within that mixed file, the
// *parsed* legacy values (mislabeled UTC — see historyTimestampLayoutLegacy)
// are not guaranteed to stay chronologically ascending relative to the exact
// values that follow them on disk: on a server west of UTC a legacy line can
// parse *later* than a genuinely later exact line; east of UTC (as here) it
// can parse *earlier*. scanHistoryFile's ascending-order early-exit on Until
// is only sound when every value it's comparing is trustworthy — see its
// use of the second return value.
func parseHistoryTimestamp(s string) (t time.Time, ok bool, exact bool) {
	if t, err := time.Parse(historyTimestampLayout, s); err == nil {
		return t, true, true
	}
	if t, err := time.Parse(historyTimestampLayoutLegacy, s); err == nil {
		return t, true, false
	}
	return time.Time{}, false, false
}

// ReadHistory scans baseLogFile and its rotated backups for lines matching q,
// never reading more than q.MaxScanBytes total or opening more than
// q.MaxFiles files. It never loads a whole file into memory: each file is
// streamed line by line (through gzip.Reader for compressed backups).
//
// Files are visited newest first (the active file, then backups from most to
// least recent) so that when a bound (MaxScanBytes/MaxFiles) cuts the scan
// short, what gets dropped is the OLDEST history, not the most recent —
// recency is what an operator investigating "what just happened" actually
// wants. Within a single file, lines are necessarily read in on-disk
// (ascending, oldest-of-file first) order — there's no cheap backward read
// through a gzip stream — but each file is itself bounded by the rotation
// config (log-rotate-max-size), so a full per-file read stays small in
// practice. Known trade-off: if a single file's own size exceeds
// MaxScanBytes, its true tail may be missed; Truncated=true signals that.
func ReadHistory(baseLogFile string, q HistoryQuery) (HistoryResult, error) {
	files, err := listHistoryFiles(baseLogFile) // oldest -> newest
	if err != nil {
		return HistoryResult{}, err
	}
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i] // newest -> oldest
	}
	return scanFileList(files, q)
}

// scanFileList is ReadHistory's scan/bound/merge loop, factored out so
// ReadHistoryFiles can run it over a candidate list gathered from more than
// one base log file instead of just one. files must already be newest-first;
// callers are responsible for producing that order (ReadHistory reverses
// listHistoryFiles' oldest-first result; ReadHistoryFiles rank-merges
// multiple bases by recency — see its comment).
func scanFileList(files []historyFile, q HistoryQuery) (HistoryResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultHistoryMaxLines
	}
	maxScanBytes := q.MaxScanBytes
	if maxScanBytes <= 0 {
		maxScanBytes = DefaultHistoryMaxScanBytes
	}
	maxFiles := q.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultHistoryMaxFiles
	}

	res := HistoryResult{}
	result := make([]HttpMessage, 0, min(limit, 256))
	var scannedBytes int64

	for _, f := range files {
		if len(result) >= limit {
			// Stopped because the request's Limit was reached, not because
			// history was exhausted — files/lines beyond this point were
			// never scanned, so there may be more matching data. Treat this
			// the same as a byte/file budget cutoff: both mean "the caller
			// might not be seeing everything," which is what Truncated is
			// for. A false positive (result size happens to exactly equal
			// the total available) is possible but strictly safer than
			// silently under-reporting — callers can always narrow
			// since/until or filters to confirm either way.
			res.Truncated = true
			break
		}
		if res.ScannedFiles >= maxFiles {
			res.Truncated = true
			break
		}
		res.ScannedFiles++

		// Only ask this file's scan for as many matches as result can still
		// take, not the full request limit — scanHistoryFile already caps
		// its own per-file result to this count (ring buffer, see below), so
		// once earlier files have filled most of `limit`, a busy later file
		// does proportionally less buffering/eviction work instead of always
		// tracking up to the full limit before being trimmed down here.
		needed := limit - len(result)
		fileMatches, hitBudget, err := scanHistoryFile(f, q, needed, maxScanBytes, &scannedBytes)
		if err != nil {
			return res, err
		}

		// fileMatches is ascending (oldest-of-file first) and already capped
		// to at most `needed` entries by scanHistoryFile — merge newest-first
		// into result directly, no further trimming needed here.
		for i := len(fileMatches) - 1; i >= 0; i-- {
			result = append(result, fileMatches[i])
		}

		if hitBudget {
			res.Truncated = true
			break
		}
	}
	res.ScannedBytes = scannedBytes
	res.Messages = result
	return res, nil
}

// ReadHistoryFiles is ReadHistory extended to more than one independently
// rotated log file, scanning them as a single recency-ordered sequence
// instead of one file at a time. This exists because module routing splits
// log lines across more than one on-disk file
// (cluster.LogModuleWithFieldsPrintf routes maintenance-adjacent modules —
// ConstLogModMaintenance/Task/Restic/SST/BackupStream/Purge — to a dedicated
// MaintenanceLogrus/*-maintenance.log file instead of the main log file; see
// cluster/cluster_log.go). Both the "general" and "task" history splits
// (config.IsTaskLogModule) straddle that boundary, so scanning only the main
// log file silently drops real matches for those modules — see
// doc/implementation/utils/s18log/LOG_HISTORY_READER.md.
//
// Every base's candidate files (active + rotated backups) are pooled and
// sorted together by historyFile.recency, newest first, BEFORE scanning
// starts — not scanned one whole base at a time in caller-supplied list
// order. That distinction matters under a tight MaxScanBytes/MaxFiles
// budget: with a naive "finish base A, then start base B" approach, a busy
// base earlier in the list can exhaust the shared budget before a genuinely
// more recent file from a later base is ever opened, even though recency
// (not list position) is what the budget is supposed to prioritize — see
// ReadHistory's own newest-first rationale, which this generalizes across
// sources instead of abandoning it at the multi-file boundary.
// MaxScanBytes/MaxFiles/Limit are then a single shared budget over that
// pooled, recency-ordered list (scanFileList), exactly as they already are
// for one base file in ReadHistory — never unbounded, consistent with T18.
// A caller passing a single file gets the same result as calling ReadHistory
// directly, aside from the pooling overhead.
func ReadHistoryFiles(baseLogFiles []string, q HistoryQuery) (HistoryResult, error) {
	var pooled []historyFile
	for _, base := range baseLogFiles {
		if base == "" {
			continue
		}
		files, err := listHistoryFiles(base) // oldest -> newest
		if err != nil {
			return HistoryResult{}, err
		}
		pooled = append(pooled, files...)
	}

	// Newest first across the whole pool. SliceStable so that two files
	// with an identical or unknown (zero-value, stat failed) recency keep
	// their original per-base relative order rather than being shuffled.
	sort.SliceStable(pooled, func(i, j int) bool { return pooled[i].recency.After(pooled[j].recency) })

	return scanFileList(pooled, q)
}

// scanHistoryFile streams one candidate file and returns its matching
// messages in ascending (on-disk) order, capped locally to at most limit
// entries (dropping the oldest-so-far as new matches arrive — valid here
// because within one file ascending insertion order IS chronological order).
// hitBudget reports whether the global byte budget was exhausted mid-file.
//
// Capping is a fixed-size ring buffer (matches[ring] + ringNext), not a
// slice shifted left by one on every match past limit: once full, each new
// match is one O(1) overwrite instead of an O(limit) copy — the difference
// between a file with far more matches than limit costing O(matches) total
// vs. O((matches-limit)·limit). The ring is unwrapped back into chronological
// order once, in finalize(), rather than kept sorted on every insert.
func scanHistoryFile(f historyFile, q HistoryQuery, limit int, maxScanBytes int64, scannedBytes *int64) (matches []HttpMessage, hitBudget bool, err error) {
	if limit <= 0 {
		limit = 1
	}
	file, err := os.Open(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if os.IsPermission(err) {
			// The most common cause: the server dropped from root to a less
			// privileged --user after this file was created/opened for
			// writing while still root (see server.go's Setuid/Setgid drop
			// and s18log.ChownHistoryFiles). A pre-existing file from before
			// that fix landed, or a manually-restored backup, can still be
			// owned by the wrong user.
			return nil, false, fmt.Errorf("%w (owned by a different user than the current process? try: chown <repman-user> %s)", err, f.path)
		}
		return nil, false, err
	}
	defer file.Close()

	var r io.Reader = file
	if f.gzip {
		gz, gzErr := gzip.NewReader(file)
		if gzErr != nil {
			// A partially-written or corrupt backup shouldn't abort the
			// whole history request — but it also must not look like a
			// clean, complete scan: an unreadable file's lines are exactly
			// as missing from the result as ones cut off by a byte/file
			// budget, so it's reported the same way (hitBudget=true), not
			// as if this file legitimately had zero matching lines. Same
			// treatment as scanner.Err() below for the same reason.
			return nil, true, nil
		}
		defer gz.Close()
		r = gz
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxScanLineBytes)

	textLower := strings.ToLower(q.Text)
	matches = make([]HttpMessage, 0, min(limit, 256))
	ringNext := 0 // next write index once len(matches) == limit; the ring's oldest slot

	// finalize unwraps the ring back into ascending (chronological) order —
	// the contract callers rely on (scanFileList merges newest-first off the
	// END of this slice). A no-op unless the ring actually wrapped.
	finalize := func() []HttpMessage {
		if len(matches) < limit || ringNext == 0 {
			return matches
		}
		rotated := make([]HttpMessage, limit)
		n := copy(rotated, matches[ringNext:])
		copy(rotated[n:], matches[:ringNext])
		return rotated
	}

	for scanner.Scan() {
		line := scanner.Text()
		*scannedBytes += int64(len(line)) + 1
		if *scannedBytes >= maxScanBytes {
			return finalize(), true, nil
		}

		fields, ok := parseLogfmtLine(line)
		if !ok {
			continue
		}
		msg, ok := buildHttpMessage(fields)
		if !ok {
			continue
		}

		ts, hasTS, exactTS := parseHistoryTimestamp(msg.Timestamp)
		if !q.Until.IsZero() && hasTS && !ts.Before(q.Until) {
			// Until is exclusive (ts < Until, not <=): callers page backwards
			// with until=<oldest row already shown>, and an inclusive bound
			// would re-return that exact row on every subsequent page. This
			// line itself is always excluded on its own value, exact or not
			// — that matches pre-existing (already-accepted) legacy-line
			// imprecision, same as Since below.
			//
			// Whether to stop scanning entirely is a separate question:
			// "ascending within-file order, nothing further can be < Until
			// either" only holds when THIS value is trustworthy. A legacy
			// (mislabeled-UTC) value earlier on disk isn't guaranteed to
			// stay <= later exact values once a file straddles the format
			// transition (see parseHistoryTimestamp) — breaking here on a
			// legacy line could skip real matches that follow it. Only
			// short-circuit the rest of the file when exact.
			if exactTS {
				break
			}
			continue
		}
		if !q.Since.IsZero() && hasTS && ts.Before(q.Since) {
			continue
		}
		if q.Group != "" && msg.Group != q.Group {
			continue
		}
		if len(q.Levels) > 0 && !q.Levels[bucketForLevel(msg.Level)] {
			continue
		}
		if len(q.Modules) > 0 && !q.Modules[msg.Module] {
			continue
		}
		if (q.TaskSplit == "general" || q.TaskSplit == "task") && config.IsTaskLogModule(msg.Module) != (q.TaskSplit == "task") {
			continue
		}
		if textLower != "" && !strings.Contains(strings.ToLower(msg.Text), textLower) {
			continue
		}

		if len(matches) < limit {
			matches = append(matches, msg)
		} else {
			matches[ringNext] = msg
			ringNext = (ringNext + 1) % limit
		}
	}
	if scanner.Err() != nil {
		// A line over maxScanLineBytes or an I/O/gzip read error stops this
		// file's scan early (bufio.Scanner can't resume after Err() is set).
		// Report it the same way as a budget cutoff — a signal that the
		// result may be incomplete — rather than silently returning a
		// partial file as if it were complete, but without failing the
		// whole request over one bad file (F8: less broken beats
		// completely broken).
		return finalize(), true, nil
	}
	return finalize(), false, nil
}
