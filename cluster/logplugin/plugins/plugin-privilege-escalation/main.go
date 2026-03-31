// plugin-privilege-escalation watches the audit log for privilege-changing
// operations (GRANT, CREATE USER, ALTER USER, DROP USER, REVOKE) performed by
// accounts that are not in the allowed-admin-users list.
//
// WARN0308 — raised for each unauthorized privilege-changing operation found.
//
// Config (environment variables):
//
//	REPMAN_ALLOWED_ADMIN_USERS  string  default: "root,replication_manager"
//	                                    — comma-separated whitelist
//	REPMAN_TIMEFRAME_HOURS      int     default: 24
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

var privilegeOps = []string{
	"GRANT", "REVOKE", "CREATE USER", "ALTER USER",
	"DROP USER", "RENAME USER", "SET PASSWORD",
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	allowedRaw := envStr("REPMAN_ALLOWED_ADMIN_USERS", "root,replication_manager")
	hours := envInt("REPMAN_TIMEFRAME_HOURS", 24)
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	allowed := make(map[string]bool)
	for _, u := range strings.Split(allowedRaw, ",") {
		allowed[strings.TrimSpace(strings.ToLower(u))] = true
	}

	var findings []wire.Finding

	for _, msg := range req.AuditLog {
		if msg.Text == "" {
			continue
		}
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
				continue
			}
		}
		// AuditLog .Text = "serverhost, username, host, connid, queryid, OPERATION, database, SQL,retcode"
		parts := strings.SplitN(msg.Text, ", ", 8)
		if len(parts) < 8 {
			continue
		}
		operation := strings.TrimSpace(parts[5])
		if operation != "QUERY" && operation != "QUERY_DDL" {
			continue
		}
		username := strings.TrimSpace(parts[1])
		sqlAndRetcode := parts[7]
		sqlText := strings.Trim(sqlAndRetcode, "'")
		if idx := strings.LastIndex(sqlAndRetcode, ","); idx > 0 {
			sqlText = strings.TrimSpace(strings.Trim(sqlAndRetcode[:idx], "'"))
		}
		sqlUpper := strings.ToUpper(strings.TrimSpace(sqlText))

		for _, op := range privilegeOps {
			if strings.HasPrefix(sqlUpper, op) {
				if !allowed[strings.ToLower(username)] {
					findings = append(findings, wire.Finding{
						ErrKey:   "WARN0308",
						Severity: "WARNING",
						Description: fmt.Sprintf(
							"Server %s: privilege change by unauthorized user '%s': %s",
							req.ServerURL, username, truncate(sqlText, 200)),
					})
				}
				break
			}
		}
		if len(findings) >= 10 {
			break
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
