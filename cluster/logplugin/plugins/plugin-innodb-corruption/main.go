// plugin-innodb-corruption detects InnoDB corruption indicators in the error log.
// WARN0300 — raised when corruption keywords appear within the last 24 hours.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

var corruptionKeywords = []string{
	"corruption", "corrupted", "Database page corruption",
	"InnoDB: Error: page", "space id and page", "is in the future",
	"checksum mismatch",
}

func main() {
	var req wire.Request
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
		if msg.Timestamp != "" {
			if t, err := parseTS(msg.Timestamp); err == nil && t.Before(cutoff) {
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

	resp := wire.Response{}
	if count > 0 {
		resp.Findings = []wire.Finding{{
			ErrKey:   "WARN0300",
			Severity: "ERROR",
			Description: fmt.Sprintf(
				"Server %s: %d InnoDB corruption indicator(s) in error log (last 24h)",
				req.ServerURL, count),
		}}
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}

func parseTS(s string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.000000Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown timestamp: %q", s)
}
