// plugin-innodb-corruption — example replication-manager log plugin.
//
// Build:
//
//	CGO_ENABLED=0 go build -o plugin-innodb-corruption .
//
// The back office places the resulting binary under
//
//	<cluster.WorkingDir>/plugins/plugin-innodb-corruption
//
// via the per-cluster GitLab repository.  replication-manager calls it once
// per monitoring tick per server, writes log data to stdin as JSON, and reads
// findings from stdout as JSON.
//
// Wire protocol:
//
//	stdin  ← {"server_url":"h:3306","error_log":[...],"sql_error_log":[...],"slow_log":[...]}
//	stdout → {"findings":[{"err_key":"WARN0300","severity":"WARNING","description":"..."}]}
//	exit 0  = evaluation complete (empty findings = all clear)
//	exit ≠0 = error (repman logs WARN0203 and skips state injection)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ---- wire types (copy these into every plugin — no shared dependency needed) ----

type Request struct {
	ServerURL   string   `json:"server_url"`
	ErrorLog    []LogMsg `json:"error_log"`
	SqlErrorLog []LogMsg `json:"sql_error_log"`
	SlowLog     []LogMsg `json:"slow_log"`
}

type LogMsg struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

type Response struct {
	Findings []Finding `json:"findings"`
}

type Finding struct {
	ErrKey      string `json:"err_key"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// ---- plugin logic -----------------------------------------------------------

// corruptionKeywords are InnoDB phrases that indicate data corruption.
var corruptionKeywords = []string{
	"corruption",
	"corrupted",
	"Database page corruption",
	"InnoDB: Error: page",
	"space id and page",
	"is in the future",
	"checksum mismatch",
}

func main() {
	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	count := 0

	for _, msg := range req.ErrorLog {
		if msg.Text == "" {
			continue
		}
		// Honour the 24-hour window when a timestamp is present.
		if msg.Timestamp != "" {
			t, err := parseTimestamp(msg.Timestamp)
			if err == nil && t.Before(cutoff) {
				continue
			}
		}
		for _, kw := range corruptionKeywords {
			if strings.Contains(msg.Text, kw) {
				count++
				break
			}
		}
	}

	resp := Response{}
	if count > 0 {
		resp.Findings = []Finding{{
			ErrKey:   "WARN0300",
			Severity: "ERROR",
			Description: fmt.Sprintf(
				"Server %s: %d InnoDB corruption indicator(s) in error log (last 24h)",
				req.ServerURL, count,
			),
		}}
	}

	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
		os.Exit(1)
	}
}

func parseTimestamp(s string) (time.Time, error) {
	for _, f := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006/01/02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown timestamp: %q", s)
}
