// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
)

// IsGlobalUnitPricing reports whether this cluster's partner prices it via
// cloud18-marketplace-pricing-mode == global-unit-pricing, in which case Database
// sizing must stay ratio-locked (see doc/implementation/config/CLOUD18_CREDIT_MODEL.md §2).
func (cluster *Cluster) IsGlobalUnitPricing() bool {
	return cluster.Conf.Cloud18MarketplacePricingMode == config.ConstMarketplacePricingModeGlobalUnitPricing
}

// ResizeDatabaseUnits atomically resizes the cluster's Database sizing to units whole
// Database Units per node (1 core / 4GB / 40GB / 1000 IOPS each), applying all four
// dimensions in one operation so no intermediate off-ratio shape is ever observed. This
// is the only supported way to resize DB sizing while cloud18-marketplace-pricing-mode
// is global-unit-pricing, since per-field writes to prov-db-* are rejected in that mode
// (see setClusterSetting in server/api_cluster.go).
func (cluster *Cluster) ResizeDatabaseUnits(units int) error {
	if units < 1 {
		return fmt.Errorf("database units must be greater than or equal to 1, got %d", units)
	}

	cluster.SetDBCores(fmt.Sprintf("%d", units*config.DBUnitCpuCores))
	cluster.SetDBMemorySize(fmt.Sprintf("%d", units*config.DBUnitMemMB))
	cluster.SetDBDiskSize(fmt.Sprintf("%d", units*config.DBUnitDiskGB))
	cluster.SetDBDiskIOPS(fmt.Sprintf("%d", units*config.DBUnitIops))
	cluster.ConfigManager.SaveConfig(cluster, false)

	return nil
}
