// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"io"
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

// ReseedProgressView is the JSON-friendly snapshot of an in-flight restore for the
// dashboard: enough to render a progress bar (bytes/total/percent) or a generic
// "started T (elapsed)" timer when the method has no byte instrumentation.
type ReseedProgressView struct {
	InProgress   bool   `json:"inProgress"`
	FromRejoin   bool   `json:"fromRejoin"`   // armed by a rejoin (reseedFromRejoin)
	Task         string `json:"task"`         // reseed task: "reseedmysqldump", "reseedmariabackup", "direct", …
	Backup       string `json:"backup"`       // backup file/dir being restored ("" if generic)
	Tool         string `json:"tool"`         // restore tool
	Bytes        int64  `json:"bytes"`        // streamed (compressed) so far
	Total        int64  `json:"total"`        // total compressed size (0 = unknown)
	Percent      int    `json:"percent"`      // 0..100, or -1 when total is unknown
	StartedUnix  int64  `json:"startedUnix"`  // restore start (byte path) or rejoin-arm time (generic)
	ElapsedSecs  int64  `json:"elapsedSecs"`
	RateBytesSec int64  `json:"rateBytesSec"` // average over elapsed (0 when no bytes)
	Line         string `json:"line"`         // the human progress line
}

// GetReseedProgress returns a snapshot of the server's in-flight restore, or nil
// when idle. It merges the byte-instrumented progress (reseedInfo + counters) with
// the generic rejoin timer (rejoinReseedStart) so a rejoin is always reportable
// even for methods that do not count bytes.
func (server *ServerMonitor) GetReseedProgress() *ReseedProgressView {
	task := server.IsReseeding
	fromRejoin := server.reseedFromRejoin.Load()
	if task == "" && !fromRejoin {
		return nil
	}
	v := &ReseedProgressView{InProgress: task != "", FromRejoin: fromRejoin, Task: task, Percent: -1}
	info, _ := server.reseedInfo.Load().(*ReseedProgress)
	startNanos := server.reseedStart.Load()
	if info != nil {
		v.Backup = info.Backup
		v.Tool = info.Tool
		v.Bytes = server.reseedBytes.Load()
		v.Total = server.reseedTotal.Load()
		if v.Total > 0 {
			v.Percent = int(v.Bytes * 100 / v.Total)
		}
	} else if startNanos == 0 {
		startNanos = server.rejoinReseedStart.Load() // generic rejoin timer, no byte instrumentation
	}
	if startNanos > 0 {
		v.StartedUnix = startNanos / int64(time.Second)
		elapsed := time.Since(time.Unix(0, startNanos))
		v.ElapsedSecs = int64(elapsed / time.Second)
		if v.ElapsedSecs > 0 && v.Bytes > 0 {
			v.RateBytesSec = v.Bytes / v.ElapsedSecs
		}
	}
	if line, _ := server.reseedProgressLine(); line != "" {
		v.Line = line
	} else if v.StartedUnix > 0 {
		v.Line = fmt.Sprintf("rejoin reseed in progress, started %s (%s)",
			time.Unix(v.StartedUnix, 0).Format("15:04:05"),
			(time.Duration(v.ElapsedSecs) * time.Second).String())
	}
	return v
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
