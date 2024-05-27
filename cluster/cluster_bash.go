// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os/exec"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) BashScriptAlert(alert alert.Alert) error {
	if cluster.Conf.AlertScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling alert script")
		var out []byte
		out, err := exec.Command(cluster.Conf.AlertScript, alert.Cluster, alert.Host, alert.PrevState, alert.State).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Alert script complete: %s", string(out))
	}
	return nil
}

func (cluster *Cluster) BashScriptOpenSate(state state.State) error {
	if cluster.Conf.MonitoringOpenStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling open state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.MonitoringOpenStateScript, cluster.Name, state.ServerUrl, state.ErrKey).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Open state script complete: %s", string(out))
	}
	return nil
}
func (cluster *Cluster) BashScriptCloseSate(state state.State) error {
	if cluster.Conf.MonitoringCloseStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling close state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.MonitoringCloseStateScript, cluster.Name, state.ServerUrl, state.ErrKey).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Close state script complete %s:", string(out))
	}
	return nil
}

func (cluster *Cluster) failoverPostScript(fail bool) {
	if cluster.Conf.PostScript != "" {

		var out []byte
		var err error

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling post-failover script")
		failtype := "failover"
		if !fail {
			failtype = "switchover"
		}
		out, err = exec.Command(cluster.Conf.PostScript, cluster.oldMaster.Host, cluster.GetMaster().Host, cluster.oldMaster.Port, cluster.GetMaster().Port, cluster.oldMaster.MxsServerName, cluster.GetMaster().MxsServerName, failtype).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Post-failover script complete %s", string(out))
	}
}

func (cluster *Cluster) failoverPreScript(fail bool) {
	// Call pre-failover script
	if cluster.Conf.PreScript != "" {
		failtype := "failover"
		if !fail {
			failtype = "switchover"
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling pre-failover script")
		var out []byte
		var err error
		out, err = exec.Command(cluster.Conf.PreScript, cluster.oldMaster.Host, cluster.GetMaster().Host, cluster.oldMaster.Port, cluster.GetMaster().Port, cluster.oldMaster.MxsServerName, cluster.GetMaster().MxsServerName, failtype).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pre-failover script complete:", string(out))
	}
}
