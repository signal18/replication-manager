package cluster

import (
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/config"
)

// DiscoverClusterApps reads the saved config files from the apps directory
func (cluster *Cluster) DiscoverClusterApps() error {
	if !cluster.Conf.ConfRewrite {
		cluster.Logrus.Info("Unable to discover apps without dynamic config")
		return nil
	}

	subfiles, err := os.ReadDir(cluster.Conf.WorkingDir + "/" + cluster.Name + "/apps")
	if err == nil {
		for _, f := range subfiles {
			if f.IsDir() {
				var appConfig config.AppConfig
				cluster.Logrus.Infof("Parsing saved config from app directory %s ", cluster.Conf.WorkingDir+"/"+cluster.Name+"/apps/"+f.Name())
				appname := strings.Split(f.Name(), ".")[0]

				err := app.LoadConfig(cluster.Conf.WorkingDir+"/"+cluster.Name+"/apps/"+f.Name()+"/"+appname+".json", &appConfig)
				if err != nil {
					cluster.Logrus.WithField("app", appname).Errorf("Unable to load saved config file: %s", err)
					continue
				}

				var found bool
				for _, app := range cluster.Apps {
					if app.Name == appname {
						found = true
						break
					}
				}

				if found {
					continue
				}

				if appConfig.AppHosts == "" {
					continue
				}

				hosts := strings.Split(appConfig.AppHosts, ",")

				for _, host := range hosts {
					newapp := app.NewAppInstance(cluster, len(cluster.Apps), host, cluster.GetDomain(), "")
					newapp.AppConfig = appConfig
					err := newapp.LoadSectionConfigs(cluster.Conf.WorkingDir + "/" + cluster.Name + "/apps/" + f.Name() + "/" + appname + ".sections.json")
					if err != nil {
						cluster.Logrus.WithField("app", appname).Errorf("Unable to load saved section config file: %s", err)
						continue
					}
					newapp.Init()
					cluster.Logrus.Infof("Adding app %s to cluster %s", appname, cluster.Name)
					cluster.Apps = append(cluster.Apps, newapp)
				}
			}
		}
	}

	return nil
}
