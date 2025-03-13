package cluster

import (
	"errors"
	"fmt"
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
}

// AddClusterApp adds a new app to the cluster
func (cluster *Cluster) AddClusterApp(host string) {
	if !cluster.Conf.ConfRewrite {
		cluster.Logrus.Info("Unable to add apps without dynamic config")
		return
	}

	newapp := app.NewAppInstance(cluster, len(cluster.Apps), host)
	cluster.Logrus.Infof("Adding app %s to cluster %s", newapp.Name, cluster.Name)
	cluster.Apps = append(cluster.Apps, newapp)
	cluster.AppsIdList = append(cluster.AppsIdList, newapp.Id)
}

func (cluster *Cluster) RemoveAppMonitor(host string, port string) error {
	newApps := make([]*app.App, 0)
	newAppHosts := make([]string, 0)
	index := -1
	for i, pr := range cluster.Apps {
		if pr.GetHost() == host && pr.GetPort() == port {
			index = i
		} else {
			newApps = append(newApps, pr)
			newAppHosts = append(newAppHosts, pr.HostCnf)
		}
	}
	if index >= 0 {
		cluster.StateMachine.SetFailoverState()
		cluster.Lock()
		cluster.Apps = newApps
		cluster.Conf.AppHosts = strings.Join(newAppHosts, ",")
		cluster.Unlock()
		cluster.StateMachine.RemoveFailoverState()
	} else {
		return errors.New(fmt.Sprintf("App host with address %s:%s not found in cluster!", host, port))
	}

	return nil
}
