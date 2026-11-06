// plugin-licence carries the Cloud18 licence of an instance: its URI,
// <company>.<expiry YYYYMM>.<seat>, is set when the plugin is built, so the plugin's
// signature (the Signal18 plugin key) covers it. Built once per seat by the licence-builder:
//
//	make plugin-licence LICENCE_URI=company.202612.1
//
// WARN0315 — the licence expires within warn-days (default 30) of the end of its expiry month.
// WARN0316 — the licence has expired but is within the renewal grace (graceMonths after the expiry
//
//	month): the instance keeps full function, renew before enforcement.
//
// ERR00122 — the grace has elapsed (or the plugin was built without a URI): enforcement applies.
//
// The grace is a fixed policy baked into the signed binary (not a client-editable config), so an
// expired client is never cut on the date itself and the window cannot be stretched to dodge it.
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

// licenceURI is set at build time (LICENCE_URI); empty in a plain `make plugins` build. For a
// registered (online) instance it is the full identity URI (domain.subdomain.zone) and the expiry
// is carried separately in licenceExpiry; for an air-gapped seat it is <company>.<YYYYMM>.<seat>
// and the expiry is the YYYYMM inside it.
var licenceURI = ""

// licenceExpiry is set at build time (LICENCE_EXPIRY) for a full-URI licence: the end date the
// signature covers, as YYYY-MM-DD (valid through that day) or YYYYMM (valid through that month).
// Empty for the legacy <company>.<YYYYMM>.<seat> form, whose expiry comes from the URI itself.
var licenceExpiry = ""

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	warnDays := wire.CfgInt(req.Config, "warn-days", wire.EnvInt("REPMAN_LICENCE_WARN_DAYS", 30))
	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: evaluate(licenceURI, licenceExpiry, time.Now(), warnDays)})
}

// graceMonths is how long after the expiry month the client keeps full function before enforcement
// cuts in (arbitration off, standby paused). A fixed policy in the signed binary, never a
// client-editable config, so an expired client is not cut on the date itself and the window cannot
// be stretched to dodge enforcement.
const graceMonths = 2

// evaluate returns the findings of the licence uri at now: none while it is valid, WARN0315 as
// expiry nears, WARN0316 through the renewal grace after expiry (full function), ERR00122 once the
// grace has elapsed (enforcement).
func evaluate(uri, expiry string, now time.Time, warnDays int) []wire.Finding {
	end, err := licenceEnd(uri, expiry)
	if err != nil {
		return []wire.Finding{{ErrKey: "ERR00122", Severity: "ERROR",
			Description: fmt.Sprintf("Cloud18 licence plugin unusable: %v", err)}}
	}
	lastValid := end.AddDate(0, 0, -1).Format("2006-01-02")
	graceEnd := end.AddDate(0, graceMonths, 0)
	switch {
	case !now.Before(graceEnd):
		return []wire.Finding{{ErrKey: "ERR00122", Severity: "ERROR",
			Description: fmt.Sprintf("Cloud18 licence %s expired on %s; the %d-month renewal grace ended %s", uri, lastValid, graceMonths, graceEnd.AddDate(0, 0, -1).Format("2006-01-02"))}}
	case !now.Before(end):
		return []wire.Finding{{ErrKey: "WARN0316", Severity: "WARNING",
			Description: fmt.Sprintf("Cloud18 licence %s expired on %s — renew within the grace period; enforcement begins %s", uri, lastValid, graceEnd.Format("2006-01-02"))}}
	case now.AddDate(0, 0, warnDays).After(end):
		return []wire.Finding{{ErrKey: "WARN0315", Severity: "WARNING",
			Description: fmt.Sprintf("Cloud18 licence %s expires on %s", uri, lastValid)}}
	}
	return nil
}

// licenceEnd returns the first instant after the licence's last valid day. A full-URI licence
// carries its expiry separately (licenceExpiry); the legacy <company>.<YYYYMM>.<seat> form carries
// it as the URI's middle label.
func licenceEnd(uri, expiry string) (time.Time, error) {
	if expiry != "" {
		return parseExpiry(expiry)
	}
	return expiryEnd(uri)
}

// parseExpiry accepts YYYY-MM-DD (valid through that day) or YYYYMM (valid through that month) and
// returns the first instant after the last valid day (UTC).
func parseExpiry(s string) (time.Time, error) {
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return d.AddDate(0, 0, 1), nil
	}
	if m, err := time.Parse("200601", s); err == nil {
		return m.AddDate(0, 1, 0), nil
	}
	return time.Time{}, fmt.Errorf("licence expiry %q is not YYYY-MM-DD or YYYYMM", s)
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
