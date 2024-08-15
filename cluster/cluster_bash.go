// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"time"

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

func (cluster *Cluster) BashScriptDbServersChangeState(srv *ServerMonitor, newState string, oldState string) error {
	if cluster.Conf.DbServersChangeStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling database change state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.DbServersChangeStateScript, cluster.Name, srv.Host, srv.Port, newState, oldState).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Database change state script %s:", string(out))
	}
	return nil
}

func (cluster *Cluster) BashScriptPrxServersChangeState(srv DatabaseProxy, newState string, oldState string) error {
	if cluster.Conf.PRXServersChangeStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling proxy change state script")
		var out []byte
		master := cluster.GetMaster()
		if master == nil {
			return errors.New("No leader found in bash script Proxy Servers Change State ")
		}
		out, err := exec.Command(cluster.Conf.PRXServersChangeStateScript, cluster.Name, srv.GetHost(), srv.GetPort(), newState, oldState, master.State).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Proxy change state script %s:", string(out))
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

func (cluster *Cluster) BinlogRotationScript(srv *ServerMonitor) error {
	if cluster.Conf.BinlogRotationScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling binlog rotation script")
		var out []byte
		out, err := exec.Command(cluster.Conf.BinlogRotationScript, cluster.Name, srv.Host, srv.Port, srv.BinaryLogFile, srv.BinaryLogFilePrevious, srv.BinaryLogFileOldest).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Binlog rotation script complete: %s", string(out))
	}
	return nil
}

func (cluster *Cluster) BinlogCopyScript(server *ServerMonitor, binlog string, isPurge bool) error {
	if !server.IsMaster() {
		return errors.New("Copy only master binlog")
	}
	if cluster.IsInFailover() {
		return errors.New("Cancel job copy binlog during failover")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() && cluster.Conf.BackupRestic {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)
			return cluster.BinlogCopyScript(server, binlog, isPurge)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlog)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	if cluster.Conf.BinlogCopyScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Calling binlog copy script on %s. Binlog: %s", server.URL, binlog)
		var out []byte
		out, err := exec.Command(cluster.Conf.BinlogCopyScript, cluster.Name, server.Host, server.Port, strconv.Itoa(cluster.Conf.OnPremiseSSHPort), server.BinaryLogDir, server.GetMyBackupDirectory(), binlog).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		} else {
			// Skip backup to restic if in purge binlog
			if !isPurge {
				if idx := slices.Index(server.BinaryLogMetaToWrite, binlog); idx == -1 {
					server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlog)
				}
				server.WriteBackupBinlogMetadata()
				// Backup to restic when no error (defer to prevent unfinished physical copy)
				backtype := "binlog"
				defer server.BackupRestic(cluster.Conf.Cloud18GitUser, cluster.Name, server.DBVersion.Flavor, server.DBVersion.ToString(), backtype)
			}
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Binlog copy script complete: %s", string(out))
	}
	return nil
}
