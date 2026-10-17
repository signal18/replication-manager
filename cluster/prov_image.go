// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// The database image rule (#1862): the configuration holds what the operator asked for
// (a pin "mariadb:11.8.9", a line "mariadb:11.8", a pointer "latest" / "lts"); the
// service definition always carries the explicit release that request resolved to at
// provision or at the last rolling upgrade, never a pointer. A restart recreates the
// container from that release, so the release cannot move; only an upgrade resolves the
// request again, pins the result, pulls it and restarts.

// resolvedImageFor returns the explicit image recorded for the declared image, "" when
// the record is missing or belongs to another declaration.
func (cluster *Cluster) resolvedImageFor(declared string) string {
	r := strings.TrimSpace(cluster.Conf.ProvDbImgResolved)
	kv := strings.SplitN(r, "=", 2)
	if len(kv) != 2 || strings.TrimSpace(kv[0]) != declared {
		return ""
	}
	return strings.TrimSpace(kv[1])
}

// deployImage is the image a database deployment render uses: the explicit release
// recorded for prov-db-image, else prov-db-image itself (explicit already, or not
// resolved: another registry, registry unreachable).
func (cluster *Cluster) deployImage() string {
	declared := strings.TrimSpace(cluster.Conf.ProvDbImg)
	if e := cluster.resolvedImageFor(declared); e != "" {
		return e
	}
	return declared
}

// ResolveDatabaseImage records the real release prov-db-image means today, looked up
// in the image list (never a registry request). force resolves a pointer again (the
// rolling upgrade); without force a valid record is kept (provision, template push).
// An explicit declaration needs no record. On failure the record is left as is and
// the error returned: a provision renders the declared name, an upgrade stops.
func (cluster *Cluster) ResolveDatabaseImage(force bool) error {
	if cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return nil
	}
	declared := strings.TrimSpace(cluster.Conf.ProvDbImg)
	if declared == "" {
		return nil
	}
	if releases.IsExplicitImage(declared) {
		if cluster.Conf.ProvDbImgResolved != "" {
			cluster.Conf.ProvDbImgResolved = ""
			cluster.Save()
		}
		return nil
	}
	if !force && cluster.resolvedImageFor(declared) != "" {
		return nil
	}
	cat, err := cluster.ImageCatalog()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"prov-db-image %s not resolved, image list unavailable (%s): the service definition keeps the floating name", declared, err)
		return err
	}
	repo, tag := releases.SplitImage(declared)
	// A cluster already running without a record (provisioned before #1862) is pinned on
	// the release it runs: resolving its pointer against today's list would put another
	// release, maybe another major, in the service definitions, applied at its next restart
	// without anyone asking (preprod belair: latest -> 13.0.2 under 11.8.6). Only an upgrade
	// (force) resolves the pointer again. A running release the list does not know keeps
	// the declared name, no record: never today's pointer either.
	if !force {
		running := cluster.runningDatabaseRelease(declared)
		if running == "" && cluster.hasProvisionedDatabase() {
			// provisioned already, but no version known right now (primary down, servers
			// unreachable): resolving today's pointer could put another major in the service
			// definitions; the declared name is kept, the next push with a known version pins
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
				"prov-db-image %s not resolved: the cluster is provisioned and no running release is known, the service definition keeps the declared name", declared)
			return nil
		}
		if running != "" {
			if !cat.InList(repo, running) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
					"prov-db-image %s: the running release %s is not in the %s, the service definition keeps the declared name", declared, running, cat.Source)
				return nil
			}
			record := declared + "=" + repo + ":" + running
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"prov-db-image %s pinned on %s:%s, the release the cluster runs (no record yet): only an upgrade moves it", declared, repo, running)
			cluster.Conf.ProvDbImgResolved = record
			cluster.Save()
			return nil
		}
	}
	release := cat.Resolve(repo, releases.FlavorOfImage(declared), tag)
	if release == tag {
		// Not in the list: the input comes back unchanged and the service definition
		// carries the declared name (Stéphane's rule), with no record to clear.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"prov-db-image %s has no release in the %s: the service definition keeps the declared name", declared, cat.Source)
		if cluster.Conf.ProvDbImgResolved != "" {
			cluster.Conf.ProvDbImgResolved = ""
			cluster.Save()
		}
		return nil
	}
	record := declared + "=" + repo + ":" + release
	if record != cluster.Conf.ProvDbImgResolved {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"prov-db-image %s resolved to %s:%s from the %s, the service definition is pinned on it", declared, repo, release, cat.Source)
		cluster.Conf.ProvDbImgResolved = record
		cluster.Save()
	}
	return nil
}

// runningDatabaseRelease is the release the cluster runs (x.y.z): its primary's, else any
// database server's whose version is known; "" when none is known (a provision, or every
// server unreachable) or the flavor is not the declared image's (PostgreSQL's "PostgreSQL"
// never matches "postgres": not pinned, the safe side).
func (cluster *Cluster) runningDatabaseRelease(declared string) string {
	flavor := releases.FlavorOfImage(declared)
	release := func(s *ServerMonitor) string {
		if s == nil || s.DBVersion == nil || s.DBVersion.Major == 0 || !strings.EqualFold(s.DBVersion.Flavor, flavor) {
			return ""
		}
		return fmt.Sprintf("%d.%d.%d", s.DBVersion.Major, s.DBVersion.Minor, s.DBVersion.Release)
	}
	if r := release(cluster.GetMaster()); r != "" {
		return r
	}
	for _, s := range cluster.Servers {
		if r := release(s); r != "" {
			return r
		}
	}
	return ""
}

// hasProvisionedDatabase reports whether a database server of the cluster was provisioned
// once (its provision cookie): a template push then is not a provision.
func (cluster *Cluster) hasProvisionedDatabase() bool {
	for _, s := range cluster.Servers {
		if s != nil && s.HasProvisionCookie() {
			return true
		}
	}
	return false
}

// DBImageAutoUpgradeEnv says whether the database container gets MARIADB_AUTO_UPGRADE=1:
// the official MariaDB image then runs mariadb-upgrade at start when the data directory
// was written by another release, and does nothing otherwise, so an in-place major
// upgrade is complete on each node's first start and a restart is unaffected (dev3
// 2026-10-02: without it the 11.8 -> 12.3 move left mysql.proc at 21 columns and every
// dump failed on it, #1862). MySQL and Percona upgrade their system tables on their own.
func (cluster *Cluster) DBImageAutoUpgradeEnv() bool {
	return strings.Contains(strings.ToLower(cluster.Conf.ProvDbImg), "mariadb")
}
