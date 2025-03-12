package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/cluster/app"
)

// DiscoverClusterApps reads the saved config files from the apps directory
func (cluster *Cluster) DiscoverClusterApps() {
	if !cluster.Conf.ConfRewrite {
		cluster.Logrus.Info("Unable to discover apps without dynamic config")
		return
	}

	hosts := strings.Split(cluster.Conf.AppHosts, ",")

	for k, host := range hosts {
		newapp := app.NewAppInstance(cluster, k, host)
		cluster.Logrus.Infof("Adding app %s to cluster %s", newapp.Name, cluster.Name)
		cluster.Apps = append(cluster.Apps, newapp)
		cluster.AppsIdList = append(cluster.AppsIdList, newapp.Id)
	}

	return
}
