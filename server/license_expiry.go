// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// Cloud18 enterprise licence expiry (#2309). A client's licence is dated: its URI is
// <company>.<YYYYMM>.<seat> and the instance is configured with that YYYYMM as its sub-domain, so
// the expiry is read from Conf.Cloud18SubDomain and the licence is valid through the END of that
// month.
//
// Enforcement NEVER compromises the client's database HA (F2): the ACTIVE repman keeps monitoring
// and failing over past expiry. What lapses is the paid monitor-HA — the external arbitration and
// the DR (standby) repman. So on expiry arbitration is forced off, and the arbitration STANDBY
// pauses its monitoring (monitoring-pause), so two un-arbitrated repman never both act on the same
// cluster. A pause (not a shutdown) needs no cookie: a restarted standby simply re-pauses, and a
// renewed licence un-pauses it — all re-derived each tick from the expiry and the active/standby
// status.
const (
	licenseExpiryWarnDays = 30
	licenseExpiryStateKey = "GWARN024@license-expiry"
)

var licenseMonthRe = regexp.MustCompile(`^[0-9]{6}$`)

// licenseExpiryDate derives the expiry from the instance's sub-domain (YYYYMM). The licence is valid
// through the end of that month, so expiry is the first instant of the following month. ok is false
// when the sub-domain is not a YYYYMM — a free / non-dated instance has no expiry.
func (repman *ReplicationManager) licenseExpiryDate() (time.Time, bool) {
	sd := strings.TrimSpace(repman.Conf.Cloud18SubDomain)
	if !licenseMonthRe.MatchString(sd) {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(sd[:4])
	m, _ := strconv.Atoi(sd[4:6])
	if m < 1 || m > 12 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0), true
}

// IsLicenseExpired reports the last evaluated expiry state (read by isActiveArbitration).
func (repman *ReplicationManager) IsLicenseExpired() bool { return repman.licenseExpired.Load() }

// checkLicenseExpiry runs each tick: WARNING as expiry approaches, a blocker once past, arbitration
// forced off, and the arbitration standby's clusters paused. Never stops the active or gates
// failover (F2).
func (repman *ReplicationManager) checkLicenseExpiry() {
	exp, ok := repman.licenseExpiryDate()
	if !ok {
		repman.clearLicenseExpiry()
		return
	}
	now := time.Now()
	through := exp.AddDate(0, 0, -1) // last valid day, for human-readable messages

	switch {
	case now.After(exp) || now.Equal(exp): // expired
		if !repman.licenseExpired.Swap(true) {
			repman.setClustersLicenseExpired(true)
		}
		repman.SetState(licenseExpiryStateKey, state.State{ErrType: config.LvlErr, ErrKey: "GERR00120",
			ErrDesc: fmt.Sprintf("Cloud18 enterprise licence expired on %s — arbitration off; databases keep monitoring and failover, the standby monitor is paused; renew the licence", through.Format("2006-01-02")),
			ErrFrom: "REPMAN"})
		repman.pauseLicenseStandbyClusters()
	case now.After(exp.AddDate(0, 0, -licenseExpiryWarnDays)): // within the warning window
		repman.clearLicenseExpiry()
		days := int(exp.Sub(now).Hours()/24) + 1
		repman.SetState(licenseExpiryStateKey, state.State{ErrType: "WARNING", ErrKey: "GWARN024",
			ErrDesc: fmt.Sprintf("Cloud18 enterprise licence expires in %d day(s), on %s — renew to keep arbitration / DR", days, through.Format("2006-01-02")),
			ErrFrom: "REPMAN"})
	default: // comfortably valid — set nothing, so any prior state auto-resolves
		repman.clearLicenseExpiry()
	}
}

// clearLicenseExpiry resets the expired flag and un-pauses any cluster we paused for expiry; it sets
// no state so a prior WARNING/blocker auto-resolves.
func (repman *ReplicationManager) clearLicenseExpiry() {
	if repman.licenseExpired.Swap(false) {
		repman.setClustersLicenseExpired(false)
	}
	for _, cl := range repman.Clusters {
		if cl.IsLicensePaused() {
			cl.SetLicensePause(false)
		}
	}
}

func (repman *ReplicationManager) setClustersLicenseExpired(expired bool) {
	for _, cl := range repman.Clusters {
		cl.SetLicenseExpired(expired)
	}
}

// pauseLicenseStandbyClusters pauses monitoring on the arbitrated clusters this instance is the
// standby for (active for none of them), so that with arbitration off two repman never both act.
// The active clusters keep monitoring and failover. Only clusters not already paused are touched,
// so an operator's own pause is never clobbered on renewal.
func (repman *ReplicationManager) pauseLicenseStandbyClusters() {
	for _, cl := range repman.Clusters {
		if cl.Conf.Arbitration && !cl.IsActive() && !cl.Conf.MonitorPause && !cl.IsLicensePaused() {
			cl.SetLicensePause(true)
		}
	}
}
