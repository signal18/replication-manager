// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// See LICENSE in this directory for the integral text.

package cluster

// Cloud18 enterprise licence expiry (#2309). The authority is the signed plugin-licence: its
// expiry date is baked in at build and the ed25519 signature covers the binary, so it is
// unforgeable. RunLogPlugins drives LicenseExpired from the plugin's ERR00122 finding. The flag
// has two effects, both re-derived, never a shutdown or an on-disk cookie:
//   - the PASSIVE (standby) monitor's tick is paused (cluster.Run) so two un-arbitrated repman
//     never both act;
//   - the ACTIVE monitor treats arbitration as off (isActiveArbitration) so it keeps failover
//     without the lapsed external arbitrator — safe because the passive stood down.

// SetLicenseExpired records the signed plugin-licence's verdict for this cluster.
func (cluster *Cluster) SetLicenseExpired(expired bool) { cluster.LicenseExpired = expired }

// IsLicenseExpired reports whether the signed plugin-licence last evaluated as expired.
func (cluster *Cluster) IsLicenseExpired() bool { return cluster.LicenseExpired }
