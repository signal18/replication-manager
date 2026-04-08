// plugin-binlog-cleartext-password scans recent binlog QUERY events for SQL
// statements that carry a password literal in cleartext.
//
// Detected patterns (case-insensitive):
//   - CREATE USER  … IDENTIFIED BY 'password'
//   - ALTER USER   … IDENTIFIED BY 'password'
//   - GRANT        … IDENTIFIED BY 'password'
//   - SET PASSWORD … = 'password'
//   - SET PASSWORD FOR … = 'password'
//
// WARN0310 — raised for each matching binlog event found.
//
// Config (TOML plugin-config or environment variables):
//
//	REPMAN_TIMEFRAME_HOURS   int     default: 1   — only inspect events within this window
//	REPMAN_MAX_FINDINGS      int     default: 10  — cap on findings per evaluation
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// cleartextPasswordRe matches SQL statements that carry a plaintext password
// literal after IDENTIFIED BY or SET PASSWORD [FOR …] =.
// The password literal itself is captured in group 1 so it can be redacted in
// the finding description.
var cleartextPasswordRe = regexp.MustCompile(
	`(?i)(?:IDENTIFIED\s+BY\s+|SET\s+PASSWORD(?:\s+FOR\s+\S+)?\s*=\s*)` +
		`['"]([^'"]{1,128})['"]`,
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	hours := envInt("REPMAN_TIMEFRAME_HOURS", 1)
	maxFindings := envInt("REPMAN_MAX_FINDINGS", 10)
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	var findings []wire.Finding

	for _, ev := range req.BinlogEvents {
		if len(findings) >= maxFindings {
			break
		}
		if ev.Query == "" {
			continue
		}
		if ev.Timestamp != "" {
			if t, err := parseTS(ev.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}

		upper := strings.ToUpper(ev.Query)
		// Quick pre-filter before running the heavier regex.
		if !strings.Contains(upper, "IDENTIFIED") && !strings.Contains(upper, "SET PASSWORD") {
			continue
		}

		if cleartextPasswordRe.MatchString(ev.Query) {
			match := cleartextPasswordRe.FindStringSubmatch(ev.Query)
			redacted := "<redacted>"
			if len(match) > 1 && match[1] != "" {
				// Show only first/last char so the alert is unambiguous but not a leak.
				pw := match[1]
				if len(pw) > 2 {
					redacted = string(pw[0]) + strings.Repeat("*", len(pw)-2) + string(pw[len(pw)-1])
				} else {
					redacted = strings.Repeat("*", len(pw))
				}
			}
			desc := fmt.Sprintf(
				"Server %s: cleartext password detected in binlog at %s (schema: %s, password hint: %s): %s",
				req.ServerURL,
				ev.Timestamp,
				ev.Schema,
				redacted,
				truncate(ev.Query, 300),
			)
			findings = append(findings, wire.Finding{
				ErrKey:      "WARN0310",
				Severity:    "ERROR",
				Description: desc,
			})
		}
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseTS(s string) (time.Time, error) {
	for _, f := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown ts: %q", s)
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
