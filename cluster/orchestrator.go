package cluster

import (
	"github.com/signal18/replication-manager/config"
	"github.com/spf13/pflag"
)

type Orchetrator struct {
	DatabaseOrchetrator
	Id      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Cluster *Cluster
}

type DatabaseOrchetrator interface {
	SetCluster(c *Cluster)
	AddFlags(flags *pflag.FlagSet, conf config.Config)
	Init()
	GetNodes() ([]Agent, error)
	ProvisionDatabaseService(server *ServerMonitor)
	ProvisionProxyService(server DatabaseProxy) error
	UnprovisionDatabaseService(server *ServerMonitor)
	UnprovisionProxyService(server DatabaseProxy) error
	StartDatabaseService(server *ServerMonitor)
	StartProxyService(server DatabaseProxy) error
	StopDatabaseService(server *ServerMonitor)
	StopProxyService(server DatabaseProxy) error
}

type orchestratorList []DatabaseOrchetrator

func (o *Orchetrator) SetCluster(c *Cluster) {
	o.Cluster = c
}

func (o *Orchetrator) GetType() string {
	return o.Type
}
