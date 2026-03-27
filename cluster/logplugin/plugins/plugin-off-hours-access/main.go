// plugin-off-hours-access detects database connections or DML queries from
// unexpected hours or accounts — useful for PCI-DSS / HIPAA compliance.
//
// WARN0309 — raised when the audit log shows activity outside allowed hours
// from accounts not in the always-allowed list.
//
// Config (environment variables):
//
//	REPMAN_ALLOWED_HOURS_START  int     default: 8   — 08:00 local time
//	REPMAN_ALLOWED_HOURS_END    int     default: 20  — 20:00 local time
//	REPMAN_ALWAYS_ALLOWED_USERS string  default: "root,replication_manager"
//	REPMAN_ALLOWED_OPERATIONS   string  default: "QUERY,QUERY_DML,QUERY_DDL,CONNECT"
//	REPMAN_TIMEFRAME_HOURS      int     default: 1
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	allowedStart := envInt("REPMAN_ALLOWED_HOURS_START", 8)
	allowedEnd := envInt("REPMAN_ALLOWED_HOURS_END", 20)
	alwaysAllowedRaw := envStr("REPMAN_ALWAYS_ALLOWED_USERS", "root,replication_manager")
	opsRaw := envStr("REPMAN_ALLOWED_OPERATIONS", "QUERY,QUERY_DML,QUERY_DDL,CONNECT")
	hours := envInt("REPMAN_TIMEFRAME_HOURS", 1)
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	alwaysAllowed := make(map[string]bool)
	for _, u := range strings.Split(alwaysAllowedRaw, ",") {
		alwaysAllowed[strings.TrimSpace(strings.ToLower(u))] = true
	}
	watchOps := make(map[string]bool)
	for _, op := range strings.Split(opsRaw, ",") {
		watchOps[strings.TrimSpace(strings.ToUpper(op))] = true
	}

	type violation struct {
		user      string
		operation string
		hour      int
		ts        string
	}
	var violations []violation

	for _, msg := range req.AuditLog {
		if msg.Text == "" {
			continue
		}
		ts, err := parseTS(msg.Timestamp)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}

		parts := strings.SplitN(msg.Text, ", ", 8)
		if len(parts) < 6 {
			continue
		}
		username := strings.ToLower(strings.TrimSpace(parts[1]))
		operation := strings.TrimSpace(parts[5])

		if alwaysAllowed[username] {
			continue
		}
		if !watchOps[strings.ToUpper(operation)] {
			continue
		}

		h := ts.Hour()
		isOffHours := h < allowedStart || h >= allowedEnd

		if isOffHours {
			violations = append(violations, violation{
				user:      parts[1],
				operation: operation,
				hour:      h,
				ts:        msg.Timestamp,
			})
		}

		if len(violations) >= 20 {
			break
		}
	}

	var findings []wire.Finding
	if len(violations) > 0 {
		// Group by user
		byUser := make(map[string]int)
		for _, v := range violations {
			byUser[v.user]++
		}
		var parts []string
		for u, count := range byUser {
			parts = append(parts, fmt.Sprintf("%s (%d×)", u, count))
		}
		findings = append(findings, wire.Finding{
			ErrKey:   "WARN0309",
			Severity: "WARNING",
			Description: fmt.Sprintf(
				"Server %s: %d off-hours access event(s) outside %02d:00-%02d:00 in last %dh. Users: %s",
				req.ServerURL, len(violations), allowedStart, allowedEnd, hours,
				strings.Join(parts, ", ")),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func parseTS(s string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.000000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown ts: %q", s)
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
