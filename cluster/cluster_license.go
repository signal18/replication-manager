// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// See LICENSE in this directory for the integral text.

package cluster

// Cloud18 enterprise licence expiry hooks (#2309). The server-side expiry check drives these:
// past expiry, arbitration is treated as off (SetLicenseExpired) and the arbitration standby's
// monitoring is paused (SetLicensePause). See server/license_expiry.go.

// SetLicenseExpired records whether this cluster's Cloud18 enterprise licence is past its expiry;
// isActiveArbitration then behaves as arbitration-off (the paid tiebreaker lapses, failover does
// not).
func (cluster *Cluster) SetLicenseExpired(expired bool) { cluster.LicenseExpired = expired }

// IsLicensePaused reports that monitoring was paused by the licence-expiry enforcement (the
// standby), as opposed to an operator's own monitoring-pause.
func (cluster *Cluster) IsLicensePaused() bool { return cluster.LicensePaused }

// SetLicensePause pauses or resumes monitoring for the licence-expiry enforcement, tracking that
// it was us (not the operator) so a renewal only un-pauses what we paused.
func (cluster *Cluster) SetLicensePause(pause bool) {
	cluster.LicensePaused = pause
	cluster.Conf.MonitorPause = pause
}
