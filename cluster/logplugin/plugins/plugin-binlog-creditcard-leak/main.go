// plugin-binlog-creditcard-leak scans recent binlog QUERY events for SQL
// statements that contain what appears to be a credit-card number in cleartext.
//
// Detection uses two layers:
//  1. A regex that finds 13-19 digit sequences (with optional spaces/dashes as
//     separators), matching the common formats for Visa, Mastercard, Amex,
//     Discover, and UnionPay.
//  2. Luhn algorithm validation so that random numeric strings (phone numbers,
//     order IDs, timestamps, …) are not flagged as false positives.
//
// WARN0311 — raised for each binlog event that contains at least one valid PAN.
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
	"unicode"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// panRe matches potential credit-card PANs: 13–19 digits optionally separated
// by spaces or dashes in groups (e.g. "4111 1111 1111 1111" or "4111-1111-1111-1111").
var panRe = regexp.MustCompile(
	`\b(?:\d[ -]?){12,18}\d\b`,
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

		matches := panRe.FindAllString(ev.Query, -1)
		if len(matches) == 0 {
			continue
		}

		// Validate each candidate with the Luhn algorithm.
		var validPANs []string
		seen := make(map[string]bool)
		for _, m := range matches {
			digits := stripNonDigits(m)
			if seen[digits] {
				continue
			}
			seen[digits] = true
			if luhn(digits) {
				validPANs = append(validPANs, maskPAN(digits))
			}
		}

		if len(validPANs) == 0 {
			continue
		}

		desc := fmt.Sprintf(
			"Server %s: potential credit-card number(s) detected in binlog at %s (schema: %s, PANs: %s): %s",
			req.ServerURL,
			ev.Timestamp,
			ev.Schema,
			strings.Join(validPANs, ", "),
			truncate(ev.Query, 300),
		)
		findings = append(findings, wire.Finding{
			ErrKey:      "WARN0311",
			Severity:    "ERROR",
			Description: desc,
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

// luhn returns true if the digit string passes the Luhn check.
func luhn(digits string) bool {
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// maskPAN returns the PAN with all but the last four digits replaced by '*'.
func maskPAN(digits string) string {
	if len(digits) <= 4 {
		return strings.Repeat("*", len(digits))
	}
	return strings.Repeat("*", len(digits)-4) + digits[len(digits)-4:]
}

// stripNonDigits removes any character that is not an ASCII digit.
func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
