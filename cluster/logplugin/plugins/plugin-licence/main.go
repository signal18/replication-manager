// plugin-licence carries the Cloud18 licence of an instance: its URI,
// <company>.<expiry YYYYMM>.<seat>, is set when the plugin is built, so the plugin's
// signature (the Signal18 plugin key) covers it. Built once per seat by the licence-builder:
//
//	make plugin-licence LICENCE_URI=company.202612.1
//
// WARN0315 — the licence expires within warn-days (default 30) of the end of its expiry month.
// ERR00122 — the licence has expired, or the plugin was built without a URI.
//
// Config (TOML plugin-config or scoped env vars as fallback):
//
//	warn-days  int  default: 30  — days before the expiry to warn  (env: REPMAN_LICENCE_WARN_DAYS)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// licenceURI is set at build time (LICENCE_URI); empty in a plain `make plugins` build.
var licenceURI = ""

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	warnDays := wire.CfgInt(req.Config, "warn-days", wire.EnvInt("REPMAN_LICENCE_WARN_DAYS", 30))
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: evaluate(licenceURI, time.Now(), warnDays)})
}

// evaluate returns the findings of the licence uri at now: none while it is valid.
func evaluate(uri string, now time.Time, warnDays int) []wire.Finding {
	end, err := expiryEnd(uri)
	if err != nil {
		return []wire.Finding{{ErrKey: "ERR00122", Severity: "ERROR",
			Description: fmt.Sprintf("Cloud18 licence plugin unusable: %v", err)}}
	}
	switch {
	case !now.Before(end):
		return []wire.Finding{{ErrKey: "ERR00122", Severity: "ERROR",
			Description: fmt.Sprintf("Cloud18 licence %s expired on %s", uri, end.AddDate(0, 0, -1).Format("2006-01-02"))}}
	case now.AddDate(0, 0, warnDays).After(end):
		return []wire.Finding{{ErrKey: "WARN0315", Severity: "WARNING",
			Description: fmt.Sprintf("Cloud18 licence %s expires on %s", uri, end.AddDate(0, 0, -1).Format("2006-01-02"))}}
	}
	return nil
}

// expiryEnd parses <company>.<YYYYMM>.<seat> and returns the first instant after the expiry
// month (UTC).
func expiryEnd(uri string) (time.Time, error) {
	parts := strings.Split(uri, ".")
	if uri == "" || len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return time.Time{}, fmt.Errorf("licence URI %q is not <company>.<YYYYMM>.<seat>", uri)
	}
	month, err := time.Parse("200601", parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("licence URI %q: expiry %q is not YYYYMM", uri, parts[1])
	}
	return month.AddDate(0, 1, 0), nil
}
