// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"strings"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (configurator *Configurator) HasInstallPlugin(Plugins map[string]*dbhelper.Plugin, name string) bool {
	val, ok := Plugins[name]
	if !ok {
		return false
	}
	if val.Status == "ACTIVE" {
		return true
	}
	return false
}

func (configurator *Configurator) HasWsrep(Variables map[string]string) bool {
	return Variables["WSREP_ON"] == "ON"
}

func (configurator *Configurator) HaveDBTag(tag string) bool {
	for _, t := range configurator.DBTags {
		if t == tag {
			return true
		}
	}
	return false
}

func (configurator *Configurator) HaveProxyTag(tag string) bool {
	for _, t := range configurator.ProxyTags {
		if t == tag {
			return true
		}
	}
	return false
}

func (configurator *Configurator) IsFilterInProxyTags(filter string) bool {
	tags := configurator.GetProxyTags()
	for _, tag := range tags {
		if strings.HasSuffix(filter, tag) {
			//fmt.Println("test tag: " + tag + "  against " + filter)
			return true
		}
	}
	return false
}

func (configurator *Configurator) IsFilterInDBTags(filter string) bool {
	tags := configurator.GetDBTags()
	for _, tag := range tags {
		if strings.HasSuffix(filter, tag) {
			//	fmt.Println(server.ClusterGroup.Conf.ProvTags + " vs tag: " + tag + "  against " + filter)
			return true
		}

	}
	return false
}

func (configurator *Configurator) HasProxyReadLeader() bool {
	if configurator.IsFilterInProxyTags("readonmaster") {
		return true
	}
	if configurator.ClusterConfig.PRXServersReadOnMaster {
		return true
	}
	return false
}

func (configurator *Configurator) HasProxyReadLeaderNoSlave() bool {
	if configurator.IsFilterInProxyTags("readonmasternoslave") {
		return true
	}
	if configurator.ClusterConfig.PRXServersReadOnMasterNoSlave {
		return true
	}
	return false
}
