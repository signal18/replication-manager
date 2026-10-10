// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"math"

	"github.com/signal18/replication-manager/config"
)

// appVolumeBytes is the app's DECLARED volume size, prov-app-disk-size (the app's own value,
// else the cluster default), exactly like prov-db-disk-size for a database and
// prov-proxy-disk for a proxy: every unit is billed from its declared setting, nothing is
// read back from the orchestrator (Stéphane 2026-09-28).
func (cluster *Cluster) appVolumeBytes(app *App) int64 {
	gb, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.GetAppDisk(app.AppConfig), true)
	if gb < 0 {
		gb = 0
	}
	return int64(gb) * 1024 * 1024 * 1024
}

// appDiskSplit is the cluster's app disk, declared volume × copies, rounded up PER APP,
// split by profile: compute apps go to BKU, storage apps (app-s3-provider) are archive
// producers and go to BAU.
type appDiskSplit struct {
	computeBytes  int64
	computeUnits  int
	producerBytes int64
	producerUnits int
}

// appDiskAccounting folds every app of the cluster into an appDiskSplit at the given unit.
func (cluster *Cluster) appDiskAccounting(unitBytes int64) appDiskSplit {
	var d appDiskSplit
	if unitBytes <= 0 {
		return d
	}
	for _, app := range cluster.Apps {
		if app == nil || app.AppConfig == nil {
			continue
		}
		b := cluster.appVolumeBytes(app) * int64(cluster.appCopyCount(app))
		if b <= 0 {
			continue
		}
		u := int(math.Ceil(float64(b) / float64(unitBytes)))
		if app.AppConfig.AppS3Provider {
			d.producerBytes += b
			d.producerUnits += u
		} else {
			d.computeBytes += b
			d.computeUnits += u
		}
	}
	return d
}
