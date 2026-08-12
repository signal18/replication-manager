package splitdump

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// classifyMaxLineSize is the hard ceiling on a single buffered line.
// 1073741824 (1GiB) is not an arbitrary guess: it is MySQL/MariaDB's own
// absolute protocol ceiling for max_allowed_packet (the hard ceiling both
// client and server enforce; a server will reject any single statement
// larger than this regardless of configuration). No legitimate mysqldump
// line -- including one extended-INSERT statement -- can ever need to exceed
// it, since the destination `mysql` client would be unable to send it
// anyway. Bounding on this bright line turns "one arbitrarily large
// allocation" into "a hard ceiling with an explicit failure," per repo law
// T18 (never build an unbounded in-memory buffer).
const classifyMaxLineSize = 1024 * 1024 * 1024

// scanLineKeepTerminator is a bufio.SplitFunc like bufio.ScanLines, except it
// keeps the trailing '\n' (and any preceding '\r') on the returned token.
// ClassifyStream forwards raw bytes byte-for-byte to whichever writer a line
// is routed to, so it must not lose or reconstruct line terminators.
func scanLineKeepTerminator(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[0 : i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	if atEOF {
		return 0, nil, nil
	}
	return 0, nil, nil
}

// ClassifyOptions routes ClassifyStream's two output streams.
type ClassifyOptions struct {
	ApplicationWriter io.Writer
	SystemWriter      io.Writer
}

// ClassifyResult reports whether any mysql.system-all content was found, and
// any GTID/binlog-position metadata observed anywhere in the stream.
type ClassifyResult struct {
	HasSystemContent bool
	Metadata         Metadata
}

// ClassifyStream splits a single mariadb-dump/mysqldump stream into
// application SQL and system-catalogue SQL (mysql.system-all), reusing the
// exact section-header rules SplitDumpLineParser already uses
// (isSystemSectionBoundary, isNonSystemSectionHeader) so both call sites
// share one boundary contract (Design Contract #12 in
// SYSTEM_ALL_RESEED_FIX_PLAN.md). It reads r exactly once and writes each
// line to exactly one of ApplicationWriter or SystemWriter as soon as that
// line is classified — no full-stream or full-section accumulation, so
// overall memory does not grow with the size of the dump. Per-line buffering
// is bounded, not unbounded: classifyMaxLineSize is a hard ceiling
// (bufio.Scanner returns bufio.ErrTooLong, surfaced as a normal
// classify-stream error) grounded in MySQL/MariaDB's own max_allowed_packet
// protocol ceiling — see its doc comment.
//
// Boundary contract, confirmed against SplitDumpLineParser's own proven
// dispatch (which opens mysql.system-all in append mode, same as the
// routine/event/trigger sections, precisely because it can be re-entered):
// the system section is not a single EOF-bounded run. Any line matching
// isSystemSectionBoundary switches routing to SystemWriter; any line matching
// one of the OTHER recognized section headers (USE, table schema/data, view,
// routine, event, trigger) switches routing back to ApplicationWriter. This
// toggles as many times as the dump actually does. A concrete case this
// exists for: mariadb-dump's --system=all "stats" component dumps
// mysql.innodb_table_stats/column_stats/etc. as ordinary tables (USE mysql;
// followed by normal table-structure/data sections) after the
// INSTALL PLUGIN/CREATE USER/GRANT statements that opened the system
// section — that is expected re-entry into application-style content, not an
// ambiguous boundary.
func ClassifyStream(r io.Reader, opts ClassifyOptions) (ClassifyResult, error) {
	return classifyStream(r, opts, classifyMaxLineSize)
}

// classifyStream is ClassifyStream's implementation with the line-size
// ceiling as a parameter, so tests can verify the ceiling is actually
// enforced (bufio.ErrTooLong triggers and is wrapped correctly) with a small
// cap instead of needing to construct a real ~1GiB line.
func classifyStream(r io.Reader, opts ClassifyOptions, maxLineSize int) (ClassifyResult, error) {
	if opts.ApplicationWriter == nil || opts.SystemWriter == nil {
		return ClassifyResult{}, errors.New("splitdump: ClassifyStream requires both ApplicationWriter and SystemWriter")
	}

	var result ClassifyResult
	sourceDataDisabled := false
	var bgtid, bfile, bpos string

	initialBufSize := 64 * 1024
	if maxLineSize < initialBufSize {
		initialBufSize = maxLineSize
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, initialBufSize), maxLineSize)
	scanner.Split(scanLineKeepTerminator)
	inSystemSection := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case isSystemSectionBoundary(line):
			inSystemSection = true
			result.HasSystemContent = true
		case isNonSystemSectionHeader(line):
			inSystemSection = false
		}

		dest := opts.ApplicationWriter
		if inSystemSection {
			dest = opts.SystemWriter
		}
		if _, writeErr := io.WriteString(dest, line); writeErr != nil {
			return ClassifyResult{}, fmt.Errorf("splitdump: classify stream write: %w", writeErr)
		}

		if !sourceDataDisabled {
			captureStreamMetadata(line, &sourceDataDisabled, &bgtid, &bfile, &bpos)
		}
	}
	if err := scanner.Err(); err != nil {
		return ClassifyResult{}, fmt.Errorf("splitdump: classify stream read: %w", err)
	}

	if sourceDataDisabled {
		result.Metadata.SourceData = 0
	} else {
		result.Metadata.File = bfile
		result.Metadata.GTID = bgtid
		if bpos != "" {
			if pos, err := strconv.ParseUint(bpos, 10, 64); err == nil {
				result.Metadata.Position = pos
			}
		}
	}

	return result, nil
}

// captureStreamMetadata mirrors the GTID/binlog-position capture in
// SplitDumpLineParser (same regexes, same "source-data=0 disables capture"
// rule), reused rather than reimplemented.
func captureStreamMetadata(line string, sourceDataDisabled *bool, bgtid, bfile, bpos *string) {
	if strings.Contains(strings.ToLower(line), "source-data=0") {
		*sourceDataDisabled = true
		*bgtid, *bfile, *bpos = "", "", ""
		return
	}
	if matches := gtidRegexMariaDB.FindStringSubmatch(line); matches != nil {
		*bgtid = matches[1]
	}
	if matches := gtidRegexMySQL.FindStringSubmatch(line); matches != nil {
		*bgtid = matches[1]
	}
	if matches := binlogRegexMariaDB.FindStringSubmatch(line); matches != nil {
		*bfile, *bpos = matches[1], matches[2]
	}
	if matches := binlogRegexMySQL.FindStringSubmatch(line); matches != nil {
		*bfile, *bpos = matches[1], matches[2]
	}
}
