package s18log

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// writeLine formats one synthetic logrus TextFormatter line, matching the
// exact shape RotateFileConfig's Formatter produces at every call site (see
// server/server.go, cluster/cluster.go): time="..." level=... msg="..."
// cluster=... module=... type=log.
func writeLine(ts, level, msg, cluster, module string) string {
	return `time="` + ts + `" level=` + level + ` msg="` + msg + `" cluster=` + cluster + ` type=log module=` + module + "\n"
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mustWriteGzipFile(t *testing.T, path, content string) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(content)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// setupHistoryFixture builds: an oldest gzip backup, a newer plain backup,
// and the active (current) log file, in that chronological order — matching
// what NewRotateFileHook (lumberjack, Compress:true) produces on disk.
func setupHistoryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	oldest := writeLine("2024-01-01 10:00:00", "info", "general message one", "none", "general") +
		writeLine("2024-01-01 10:01:00", "error", "an early error happened", "clusterA", "sql") +
		"not a logfmt line at all\n" +
		"\n"
	mustWriteGzipFile(t, filepath.Join(dir, "repman-2024-01-01T00-00-00.000.log.gz"), oldest)

	middle := writeLine("2024-01-02 10:00:00", "warning", "proxy backend flapped", "clusterA", "proxy") +
		writeLine("2024-01-02 10:05:00", "info", "clusterB general note", "clusterB", "general")
	mustWriteFile(t, filepath.Join(dir, "repman-2024-01-02T00-00-00.000.log"), middle)

	active := writeLine("2024-01-03 10:00:00", "error", "clusterA sql failure", "clusterA", "sql") +
		writeLine("2024-01-03 10:01:00", "debug", "verbose heartbeat tick", "clusterA", "heartbeat") +
		writeLine("2024-01-03 10:02:00", "info", "clusterA general recovery", "clusterA", "general")
	mustWriteFile(t, base, active)

	return base
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	// Delegates to parseHistoryTimestamp (not a direct time.Parse) so tests
	// exercise the exact same layout-plus-legacy-fallback logic production
	// code uses, rather than a copy that could silently drift from it.
	ts, ok, _ := parseHistoryTimestamp(s)
	if !ok {
		t.Fatalf("parse time %q", s)
	}
	return ts
}

func TestReadHistory_NewestFirstAcrossFiles(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 7 {
		t.Fatalf("expected 7 messages (malformed/blank lines skipped), got %d: %+v", len(res.Messages), res.Messages)
	}
	if res.Messages[0].Text != "clusterA general recovery" {
		t.Errorf("expected newest message first, got %q", res.Messages[0].Text)
	}
	if res.Messages[len(res.Messages)-1].Text != "general message one" {
		t.Errorf("expected oldest message last, got %q", res.Messages[len(res.Messages)-1].Text)
	}
	for i := 1; i < len(res.Messages); i++ {
		prev := mustParseTime(t, res.Messages[i-1].Timestamp)
		cur := mustParseTime(t, res.Messages[i].Timestamp)
		if cur.After(prev) {
			t.Fatalf("messages not newest-first at index %d: %s came after %s", i, cur, prev)
		}
	}
}

func TestReadHistory_LevelFilter(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, Levels: map[string]bool{"ERR": true}})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected 2 ERR messages, got %d: %+v", len(res.Messages), res.Messages)
	}
	for _, m := range res.Messages {
		if m.Level != "ERROR" {
			t.Errorf("expected level ERROR, got %q", m.Level)
		}
	}
}

func TestReadHistory_ModuleFilter(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, Modules: map[int]bool{config.ConstLogModProxy: true}})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "proxy backend flapped" {
		t.Fatalf("expected exactly the proxy message, got %+v", res.Messages)
	}
}

// TestReadHistory_TaskSplitAppliedBeforeLimit guards against the general/task
// endpoint returning near-empty results whenever general-log volume
// dominates near the requested window: TaskSplit must be applied during the
// scan (so the Limit cutoff counts only classified matches), not as a
// post-filter over an already limit-capped raw result.
func TestReadHistory_TaskSplitAppliedBeforeLimit(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	// One "task" line buried in the oldest backup...
	oldest := writeLine("2024-01-01 09:00:00", "info", "the one task event", "clusterA", "job")
	mustWriteGzipFile(t, filepath.Join(dir, "repman-2024-01-01T00-00-00.000.log.gz"), oldest)

	// ...behind a wall of "general" lines in the active (newest) file, well
	// past a small Limit.
	var active strings.Builder
	for i := 0; i < 5; i++ {
		active.WriteString(writeLine(fmt.Sprintf("2024-01-03 10:0%d:00", i), "info", fmt.Sprintf("general noise %d", i), "clusterA", "general"))
	}
	mustWriteFile(t, base, active.String())

	res, err := ReadHistory(base, HistoryQuery{Limit: 2, TaskSplit: "task"})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "the one task event" {
		t.Fatalf("expected the task event to survive a small Limit despite being behind general noise, got %+v", res.Messages)
	}

	// And the complementary "general" split must exclude it.
	res, err = ReadHistory(base, HistoryQuery{Limit: 100, TaskSplit: "general"})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	for _, m := range res.Messages {
		if m.Text == "the one task event" {
			t.Fatalf("TaskSplit=general must not include a task-classified message, got %+v", res.Messages)
		}
	}
	if len(res.Messages) != 5 {
		t.Fatalf("expected all 5 general lines, got %d: %+v", len(res.Messages), res.Messages)
	}
}

func TestReadHistory_GroupFilter(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, Group: "clusterB"})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Group != "clusterB" {
		t.Fatalf("expected exactly clusterB's message, got %+v", res.Messages)
	}
}

func TestReadHistory_TextFilter(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, Text: "FAILURE"})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "clusterA sql failure" {
		t.Fatalf("expected exactly the sql failure message (case-insensitive), got %+v", res.Messages)
	}
}

func TestReadHistory_SinceUntil(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{
		Limit: 100,
		Since: mustParseTime(t, "2024-01-02 00:00:00"),
		Until: mustParseTime(t, "2024-01-02 23:59:59"),
	})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected 2 messages within the Jan 2 window, got %d: %+v", len(res.Messages), res.Messages)
	}
	for _, m := range res.Messages {
		if m.Timestamp[:10] != "2024-01-02" {
			t.Errorf("expected only Jan 2 messages, got timestamp %q", m.Timestamp)
		}
	}
}

// TestReadHistory_TimezoneOffsetIsRespected guards the actual point of
// historyTimestampLayout carrying an offset: a since/until comparison must
// be correct by true instant, not by naively comparing wall-clock digits —
// otherwise "since/until" is only ever correct when the browser reading it
// happens to share the server's timezone.
func TestReadHistory_TimezoneOffsetIsRespected(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	// 09:00 UTC+7 (offset-aware) is 02:00 UTC — earlier than a naive digit
	// comparison against "03:00" (no offset, legacy/UTC-labeled) would
	// suggest. If parsing ignored the offset, this line would wrongly look
	// like it's *after* a q.Until of "03:00 UTC" and get excluded.
	mustWriteFile(t, base, writeLine("2024-01-01 09:00:00 +0700", "info", "offset aware line", "none", "general"))

	res, err := ReadHistory(base, HistoryQuery{
		Limit: 100,
		Until: mustParseTime(t, "2024-01-01 03:00:00"), // legacy layout -> UTC
	})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "offset aware line" {
		t.Fatalf("expected the +0700 line (true instant 02:00 UTC) to be included under until=03:00 UTC, got %+v", res.Messages)
	}
}

// TestReadHistory_MixedLegacyAndExactTimestampsInOneFile guards the format
// transition itself: a server restart to pick up the offset-aware
// TimestampFormat does not rotate the active log file, so for one rotation
// window a single file can contain legacy (zone-less) lines followed by
// exact (offset-aware) ones. On an east-of-UTC server, a legacy line's
// mislabeled-as-UTC parse is artificially LATER than its true value, which
// can make it look like it's already past Until — the early-exit in
// scanHistoryFile must not trust that to stop scanning, or it would skip a
// later, genuinely-in-range exact line.
func TestReadHistory_MixedLegacyAndExactTimestampsInOneFile(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	// Legacy line: no offset, so it parses as (mislabeled) 08:55:00 UTC —
	// already past a q.Until of 03:00:00 UTC, even though this server's real
	// offset (+0700, per the exact line right after it) means its true
	// instant was 01:55:00 UTC — genuinely before Until.
	legacy := writeLine("2024-01-01 08:55:00", "info", "legacy line before restart", "none", "general")
	// Exact line, written after the restart that picked up the new format:
	// true instant 02:05:00 UTC — also genuinely before Until.
	exact := writeLine("2024-01-01 09:05:00 +0700", "info", "exact line after restart", "none", "general")
	mustWriteFile(t, base, legacy+exact)

	res, err := ReadHistory(base, HistoryQuery{
		Limit: 100,
		Until: mustParseTime(t, "2024-01-01 03:00:00"), // legacy layout -> UTC
	})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}

	found := false
	for _, m := range res.Messages {
		if m.Text == "exact line after restart" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the exact line to survive scanning past the earlier (mislabeled-later) legacy line, got %+v", res.Messages)
	}
}

func TestReadHistory_LimitKeepsMostRecent(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 2})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected exactly 2 messages, got %d", len(res.Messages))
	}
	want := []string{"clusterA general recovery", "verbose heartbeat tick"}
	for i, w := range want {
		if res.Messages[i].Text != w {
			t.Errorf("index %d: expected %q, got %q", i, w, res.Messages[i].Text)
		}
	}
	// The fixture has more than 2 matching messages, so stopping at Limit=2
	// means older files/lines were never scanned — the caller can't tell
	// "that's everything" from "that's all we have" without this.
	if !res.Truncated {
		t.Error("expected Truncated=true when Limit (not exhaustion) is why the scan stopped")
	}
}

// TestReadHistory_LimitNotReachedIsNotTruncated is the control for the test
// above: Limit alone must not force Truncated=true when it was never the
// binding constraint (everything available fit within it).
func TestReadHistory_LimitNotReachedIsNotTruncated(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if res.Truncated {
		t.Error("expected Truncated=false when the fixture's full history fits well within Limit")
	}
}

// TestReadHistory_UntilExcludesBoundaryRow guards the "Load older" pagination
// contract: the client re-sends until=<oldest row's timestamp already
// shown>, so that exact row must not reappear in the next page.
func TestReadHistory_UntilExcludesBoundaryRow(t *testing.T) {
	base := setupHistoryFixture(t)

	first, err := ReadHistory(base, HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	oldest := first.Messages[len(first.Messages)-1]

	page2, err := ReadHistory(base, HistoryQuery{
		Limit: 100,
		Until: mustParseTime(t, oldest.Timestamp),
	})
	if err != nil {
		t.Fatalf("ReadHistory (page 2): %v", err)
	}
	for _, m := range page2.Messages {
		if m.Text == oldest.Text && m.Timestamp == oldest.Timestamp {
			t.Fatalf("boundary row %q re-appeared in the next page (until must be exclusive)", oldest.Text)
		}
	}
}

func TestReadHistory_ScannerErrorTruncatesInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	good := writeLine("2024-01-01 10:00:00", "info", "before the oversized line", "none", "general")
	oversized := strings.Repeat("x", maxScanLineBytes+1024) + "\n"
	mustWriteFile(t, base, good+oversized)

	res, err := ReadHistory(base, HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatalf("expected no error (a bad line should truncate, not fail the request), got %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when a line exceeds the scanner's max token size")
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "before the oversized line" {
		t.Fatalf("expected the one message parsed before the oversized line, got %+v", res.Messages)
	}
}

// TestReadHistory_CorruptGzipBackupTruncatesInsteadOfLookingComplete covers
// a partially-written or corrupt .gz backup (e.g. the process was killed
// mid-compression, or a backup was manually restored badly): its lines are
// unrecoverable, but the result must say so via Truncated=true rather than
// silently reporting "this file had nothing" the same way a genuinely empty
// or nonexistent file would — a caller can't otherwise tell "definitely no
// matches here" apart from "couldn't read this file at all."
func TestReadHistory_CorruptGzipBackupTruncatesInsteadOfLookingComplete(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman.log")

	// Not valid gzip content, but named like a compressed backup so
	// listHistoryFiles/scanHistoryFile treat it as one.
	mustWriteFile(t, filepath.Join(dir, "repman-2024-01-01T00-00-00.000.log.gz"), "not actually gzip data")

	active := writeLine("2024-01-02 10:00:00", "info", "active file line", "none", "general")
	mustWriteFile(t, base, active)

	res, err := ReadHistory(base, HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatalf("expected no error (a corrupt backup should truncate, not fail the request), got %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when a rotated backup can't be gzip-decoded")
	}
	// The active file is scanned first (newest-first order) and succeeds;
	// the corrupt backup is only reached afterward, so the readable line is
	// still returned rather than the whole request coming back empty.
	if len(res.Messages) != 1 || res.Messages[0].Text != "active file line" {
		t.Fatalf("expected the readable active-file line despite the corrupt backup, got %+v", res.Messages)
	}
}

func TestReadHistory_MaxFilesTruncates(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, MaxFiles: 1})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when MaxFiles caps the scan short of all history")
	}
	if res.ScannedFiles != 1 {
		t.Errorf("expected exactly 1 file scanned, got %d", res.ScannedFiles)
	}
	// MaxFiles=1 with newest-first ordering should scan the ACTIVE file
	// (most recent), not the oldest backup.
	if len(res.Messages) == 0 || res.Messages[0].Text != "clusterA general recovery" {
		t.Fatalf("expected the active file's newest message, got %+v", res.Messages)
	}
}

func TestReadHistory_MaxScanBytesTruncates(t *testing.T) {
	base := setupHistoryFixture(t)
	res, err := ReadHistory(base, HistoryQuery{Limit: 100, MaxScanBytes: 10})
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true with a tiny MaxScanBytes budget")
	}
	if res.ScannedBytes < 10 {
		t.Errorf("expected ScannedBytes to reach the tiny budget, got %d", res.ScannedBytes)
	}
}

func TestReadHistory_MissingFileIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	res, err := ReadHistory(filepath.Join(dir, "does-not-exist.log"), HistoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("expected no error for a missing log file, got %v", err)
	}
	if len(res.Messages) != 0 {
		t.Errorf("expected no messages, got %+v", res.Messages)
	}
}

// TestListHistoryFiles_ExtensionlessBaseFindsBackups guards against a
// regex regression where lumberjackBackupRE required a mandatory dot
// extension in the backup filename. A log-file configured with no extension
// (e.g. log-file = /var/log/repman) rotates to "repman-{timestamp}" and
// "repman-{timestamp}.gz" (lumberjack's backupName omits the extension
// segment entirely when there is none) — those must still be discovered,
// not just the active file.
func TestListHistoryFiles_ExtensionlessBaseFindsBackups(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "repman")

	mustWriteFile(t, filepath.Join(dir, "repman-2024-01-02T00-00-00.000"), "plain backup\n")
	mustWriteGzipFile(t, filepath.Join(dir, "repman-2024-01-01T00-00-00.000.gz"), "gzip backup\n")
	mustWriteFile(t, base, "active\n")

	files, err := listHistoryFiles(base)
	if err != nil {
		t.Fatalf("listHistoryFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 2 backups + active file, got %d: %+v", len(files), files)
	}

	var sawPlainBackup, sawGzipBackup bool
	for _, f := range files {
		switch filepath.Base(f.path) {
		case "repman-2024-01-02T00-00-00.000":
			sawPlainBackup = true
			if f.gzip {
				t.Errorf("plain backup misclassified as gzip: %+v", f)
			}
		case "repman-2024-01-01T00-00-00.000.gz":
			sawGzipBackup = true
			if !f.gzip {
				t.Errorf("gzip backup misclassified as plain: %+v", f)
			}
		}
	}
	if !sawPlainBackup {
		t.Error("plain extensionless backup not discovered")
	}
	if !sawGzipBackup {
		t.Error("gzip extensionless backup not discovered")
	}
}

// TestChownHistoryFiles_CoversActiveAndBackups guards the privilege-drop fix
// (server.go's root -> --user Setuid/Setgid path): ChownHistoryFiles must
// walk the active file AND every rotated backup found by listHistoryFiles,
// not just the active one, or a pre-existing backup stays owned by the old
// user and log-history reads on it keep failing after the drop. Can't
// exercise an actual cross-user chown without root, so this chowns every
// file to the test's own uid/gid (a no-op a non-root process is always
// allowed to make) and verifies, via a real stat of each file afterward,
// that every one of them was actually visited — not just that the batch
// call returned no error (which a bug that silently skipped a subset,
// e.g. only handling the active file, would still do).
func TestChownHistoryFiles_CoversActiveAndBackups(t *testing.T) {
	base := setupHistoryFixture(t)

	files, err := listHistoryFiles(base)
	if err != nil {
		t.Fatalf("listHistoryFiles: %v", err)
	}
	if len(files) != 3 { // 2 backups (1 gzip, 1 plain) + the active file
		t.Fatalf("test fixture assumption changed: expected 3 candidate files, got %d: %+v", len(files), files)
	}

	if err := ChownHistoryFiles(base, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("ChownHistoryFiles: %v", err)
	}

	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			t.Fatalf("stat %s after ChownHistoryFiles: %v", f.path, err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("unexpected Sys() type for %s", f.path)
		}
		if int(st.Uid) != os.Getuid() || int(st.Gid) != os.Getgid() {
			t.Errorf("%s: expected owner %d:%d, got %d:%d", f.path, os.Getuid(), os.Getgid(), st.Uid, st.Gid)
		}
	}
}

func TestChownHistoryFiles_MissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if err := ChownHistoryFiles(filepath.Join(dir, "does-not-exist.log"), os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("expected no error when there's nothing to chown, got %v", err)
	}
}

// mustSetModTime pins a file's mtime so cross-file recency ordering
// (historyFile.recency, ReadHistoryFiles) is deterministic in tests instead
// of depending on real wall-clock gaps between two back-to-back WriteFile
// calls, which can tie on filesystems/CI runners with coarse mtime
// resolution and make the test flaky.
func mustSetModTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes(%s): %v", path, err)
	}
}

// TestReadHistoryFiles_MergesAcrossFiles covers the bug this function fixes:
// task/general history split across the main log file and a separately
// rotated "-maintenance" sibling (cluster.LogModuleWithFieldsPrintf routes
// maintenance-adjacent modules there — see cluster/cluster_log.go). A caller
// scanning only the main file would silently miss every line that landed in
// the second file, regardless of how old or recent it is.
//
// The two active files are scanned as whole units in mtime order (newest
// file first), not globally re-sorted line-by-line across files — same
// file-granularity contract ReadHistory already has for one base's own
// backups. So with the maintenance file pinned newer here, both of its
// lines precede both of the main file's lines in the result, even though
// one main-file line's own timestamp (10:02) is later than one
// maintenance-file line's (10:01). See ReadHistoryFiles' doc comment.
func TestReadHistoryFiles_MergesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "repman.log")
	maint := filepath.Join(dir, "repman-maintenance.log")

	mustWriteFile(t, main,
		writeLine("2024-01-01 10:00:00", "info", "main file oldest", "clusterA", "general")+
			writeLine("2024-01-01 10:02:00", "info", "main file newest", "clusterA", "general"))
	mustWriteFile(t, maint,
		writeLine("2024-01-01 10:01:00", "info", "maintenance file middle", "clusterA", "task")+
			writeLine("2024-01-01 10:03:00", "info", "maintenance file newest overall", "clusterA", "task"))
	mustSetModTime(t, main, time.Date(2024, 1, 1, 10, 2, 0, 0, time.UTC))
	mustSetModTime(t, maint, time.Date(2024, 1, 1, 10, 3, 0, 0, time.UTC))

	res, err := ReadHistoryFiles([]string{main, maint}, HistoryQuery{})
	if err != nil {
		t.Fatalf("ReadHistoryFiles: %v", err)
	}

	var gotTexts []string
	for _, m := range res.Messages {
		gotTexts = append(gotTexts, m.Text)
	}
	wantTexts := []string{
		"maintenance file newest overall",
		"maintenance file middle",
		"main file newest",
		"main file oldest",
	}
	if len(gotTexts) != len(wantTexts) {
		t.Fatalf("expected %d merged messages (both files' lines), got %d: %+v", len(wantTexts), len(gotTexts), gotTexts)
	}
	for i := range wantTexts {
		if gotTexts[i] != wantTexts[i] {
			t.Errorf("message %d: expected %q, got %q (full order: %+v)", i, wantTexts[i], gotTexts[i], gotTexts)
		}
	}

	if res.ScannedFiles != 2 {
		t.Errorf("expected ScannedFiles=2 (one per source file), got %d", res.ScannedFiles)
	}
}

// TestReadHistoryFiles_SharesFileBudgetAcrossFiles ensures MaxFiles is
// enforced across the whole request, not reset to a fresh full budget for
// each base file — otherwise scanning N base files would let a caller open
// N times its configured MaxFiles, silently defeating the "per request"
// bound documented on log-history-max-files (T18: bounds are never
// per-source, always per-request).
func TestReadHistoryFiles_SharesFileBudgetAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "repman.log")
	maint := filepath.Join(dir, "repman-maintenance.log")
	mustWriteFile(t, main, writeLine("2024-01-01 10:00:00", "info", "main line", "clusterA", "general"))
	mustWriteFile(t, maint, writeLine("2024-01-01 10:01:00", "info", "maintenance line", "clusterA", "task"))
	mustSetModTime(t, main, time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC))
	mustSetModTime(t, maint, time.Date(2024, 1, 1, 10, 1, 0, 0, time.UTC))

	res, err := ReadHistoryFiles([]string{main, maint}, HistoryQuery{MaxFiles: 1})
	if err != nil {
		t.Fatalf("ReadHistoryFiles: %v", err)
	}
	if res.ScannedFiles != 1 {
		t.Fatalf("expected the shared MaxFiles=1 budget to stop after 1 file total, got ScannedFiles=%d", res.ScannedFiles)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "maintenance line" {
		t.Fatalf("expected only the more recent (maintenance) file's line, got %+v", res.Messages)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true: the main file was skipped entirely, not exhausted")
	}
}

// TestReadHistoryFiles_RecencyNotListOrder is the direct regression test for
// the bug this priority scheme fixes: under a tight budget, a base file
// EARLIER in the caller's list must not automatically win over a base file
// LATER in the list that actually holds more recent data. A naive
// "exhaust base A's budget before ever touching base B" implementation would
// scan main's (older) content and never reach maintenance's (newer) content
// purely because of list position — this pins the main file newer than the
// maintenance file (the reverse of the fixture above) and passes maintenance
// FIRST in the list, so a passing test here can't be explained by "first
// list entry always wins."
func TestReadHistoryFiles_RecencyNotListOrder(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "repman.log")
	maint := filepath.Join(dir, "repman-maintenance.log")
	mustWriteFile(t, main, writeLine("2024-01-01 10:05:00", "info", "main line, newer", "clusterA", "general"))
	mustWriteFile(t, maint, writeLine("2024-01-01 10:00:00", "info", "maintenance line, older", "clusterA", "task"))
	mustSetModTime(t, maint, time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC))
	mustSetModTime(t, main, time.Date(2024, 1, 1, 10, 5, 0, 0, time.UTC))

	// maint listed FIRST, despite being the OLDER file by recency.
	res, err := ReadHistoryFiles([]string{maint, main}, HistoryQuery{MaxFiles: 1})
	if err != nil {
		t.Fatalf("ReadHistoryFiles: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "main line, newer" {
		t.Fatalf("expected the more recent (main) file's line despite it being second in the list, got %+v", res.Messages)
	}
}

// TestReadHistoryFiles_SkipsMissingFile ensures a nonexistent second file
// (e.g. maintenance logging was never enabled, or no maintenance-class
// module has logged yet) degrades to "scan what exists" instead of erroring
// the whole request.
func TestReadHistoryFiles_SkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "repman.log")
	mustWriteFile(t, main, writeLine("2024-01-01 10:00:00", "info", "only line", "clusterA", "general"))

	res, err := ReadHistoryFiles([]string{main, filepath.Join(dir, "repman-maintenance.log")}, HistoryQuery{})
	if err != nil {
		t.Fatalf("ReadHistoryFiles: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "only line" {
		t.Fatalf("expected the single line from the existing file, got %+v", res.Messages)
	}
}
