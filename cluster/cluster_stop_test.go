package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/sirupsen/logrus"
)

func TestClusterStopWithResticManager(t *testing.T) {
	logger := logrus.New()
	conf := &config.Config{}
	resticManager := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	configManager := manager.NewConfigManager(config.NewLogrusWrapper(conf, logger))
	configManager.Stop()
	cluster := &Cluster{
		Conf:                   conf,
		Logrus:                 logger,
		ResticManager:          resticManager,
		ConfigManager:          configManager,
		RefreshTemplateMD5Chan: make(chan *App),
	}
	cluster.Stop()
}
