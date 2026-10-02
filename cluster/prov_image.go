// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
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
