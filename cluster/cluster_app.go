package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) newAppList() error {
	cluster.Apps = make([]*app.App, 0)

	if cluster.Conf.AppHosts != "" {
		for _, appHost := range strings.Split(cluster.Conf.AppHosts, ",") {
			apl := app.NewAppInstance(cluster, len(cluster.Apps), appHost)
			cluster.AddClusterApp(apl)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg, "New application created: %s %s", apl.GetHost(), apl.GetPort())
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Loaded %d apps", len(cluster.Apps))

	return nil
}

// DiscoverClusterApps reads the saved config files from the apps directory
func (cluster *Cluster) DiscoverClusterApps() {
	if !cluster.Conf.ConfRewrite {
		cluster.Logrus.Info("Unable to discover apps without dynamic config")
		return
	}

	if cluster.Conf.AppHosts == "" {
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

func (c *Cluster) AddClusterApp(apl *app.App) {
	apl.SetCluster(c)
	apl.SetID()
	apl.SetDataDir()
	apl.SetServiceName(c.Name)
	c.LogModulePrintf(c.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "New app monitored %s: %s:%s", apl.GetType(), apl.GetHost(), apl.GetPort())
	apl.SetState(stateSuspect)
	c.Apps = append(c.Apps, apl)
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
