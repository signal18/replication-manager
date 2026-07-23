// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

// assertReseedProgressStates surfaces any in-flight restore per tick with its
// streamed-bytes progress, so a long (hours/days) reseed is visible in the
// timeline/GUI instead of a silent "processing". Called per tick.
func (cluster *Cluster) assertReseedProgressStates() {
	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		line, info := sv.reseedProgressLine()
		if info == nil {
			continue
		}
		cluster.SetState("WARN0189", state.State{
			ErrType:   "WARNING",
			ErrKey:    "WARN0189",
			ErrDesc:   fmt.Sprintf(cluster.GetErrorList()["WARN0189"], sv.URL, line, info.Backup),
			ErrFrom:   "REJOIN",
			ServerUrl: sv.URL,
		})
	}
}

// ReseedProgress is the in-flight restore's backup, stamped on the ServerMonitor
// so the per-tick progress state reports which backup is being restored.
type ReseedProgress struct {
	Backup string // backup file/path being restored
	Source string // node the backup was taken from (selector origin)
	Tool   string
}

// countingReader tallies raw bytes streamed through a restore. It counts the
// COMPRESSED input (no decompression accounting yet), so the state reads
// "<streamed> streamed out of <compressed total>".
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

// startReseedProgress stamps the in-flight restore and returns a reader that
// counts bytes streamed. defer stopReseedProgress when done. total is the
// compressed backup file size (0 if unknown).
func (server *ServerMonitor) startReseedProgress(info *ReseedProgress, r io.Reader, total int64) io.Reader {
	server.reseedBytes.Store(0)
	server.reseedTotal.Store(total)
	server.reseedStart.Store(time.Now().UnixNano())
	server.reseedInfo.Store(info)
	return &countingReader{r: r, n: &server.reseedBytes}
}

func (server *ServerMonitor) stopReseedProgress() {
	server.reseedInfo.Store((*ReseedProgress)(nil))
}

// beginReseedProgress stamps an in-flight restore whose streamed bytes accumulate
// across MANY readers (a splitdump restore reads one file per table/shard). total
// is the sum of all input sizes (0 = unknown). Unlike startReseedProgress it
// returns no reader — wrap each input with countReseedReader so every shard
// accumulates into the single byte counter. defer stopReseedProgress when done.
func (server *ServerMonitor) beginReseedProgress(info *ReseedProgress, total int64) {
	server.reseedBytes.Store(0)
	server.reseedTotal.Store(total)
	server.reseedStart.Store(time.Now().UnixNano())
	server.reseedInfo.Store(info)
}

// countReseedReader wraps r so bytes read accumulate into the current reseed's
// byte counter (set up by beginReseedProgress). The counter is atomic, so the
// parallel shard readers of a splitdump restore can be counted concurrently.
func (server *ServerMonitor) countReseedReader(r io.Reader) io.Reader {
	return &countingReader{r: r, n: &server.reseedBytes}
}

// sumSplitdumpBytes returns the total compressed size of the splitdump shard files
// under dir (flat *.sql / *.sql.gz), the denominator for the progress line. Best
// effort: unreadable entries are skipped (0 total just hides the "out of N" part).
func sumSplitdumpBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".sql.gz") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// reseedProgressLine returns a human progress line + the backup being restored,
// or "" when no restore is in flight — e.g.
// "223M streamed out of 100G compressed backup, started 14:32:01 (1h23m, avg 45M/s)".
//
// Rate is the AVERAGE over elapsed (steady) rather than an instantaneous rate,
// because the per-table restore speed varies a lot; the started-at date + elapsed
// give the operator the real "how long has this been running" signal.
func (server *ServerMonitor) reseedProgressLine() (string, *ReseedProgress) {
	info, _ := server.reseedInfo.Load().(*ReseedProgress)
	if info == nil {
		return "", nil
	}
	done := server.reseedBytes.Load()
	line := humanBytes(done) + " streamed"
	if total := server.reseedTotal.Load(); total > 0 {
		line += " out of " + humanBytes(total) + " compressed backup"
	}
	if startNanos := server.reseedStart.Load(); startNanos > 0 {
		start := time.Unix(0, startNanos)
		elapsed := time.Since(start).Round(time.Second)
		line += fmt.Sprintf(", started %s (%s", start.Format("15:04:05"), elapsed)
		if secs := int64(elapsed / time.Second); secs > 0 {
			line += fmt.Sprintf(", avg %s/s", humanBytes(done/secs))
		}
		line += ")"
	}
	return line, info
}

// humanBytes formats a byte count like "223M" / "100G".
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%c", float64(b)/float64(div), "KMGTPE"[exp])
}
