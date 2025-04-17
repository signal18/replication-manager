package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) ReloadStagingScript() error {
	var script string
	var err error
	var content []byte

	filename := "staging_refresh.sh"
	template := "scripts/" + filename

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Reload staging script")

	script = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + filename

	if cluster.Conf.WithEmbed == "ON" {
		content, err = share.EmbededDbModuleFS.ReadFile(template)
	} else {
		content, err = os.ReadFile(cluster.Conf.ShareDir + "/" + template)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error reading default staging script. %s", err)
		return err
	}

	os.Remove(script)

	err = os.WriteFile(script, content, 0755)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error writing default staging script. %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) RefreshStaging() error {
	var err error

	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging not enabled")
		return nil
	}

	if cluster.IsRefreshStaging {
		return nil
	}

	if cluster.StagingServer == nil || cluster.StagingServer.State != stateUnconn {
		for _, srv := range cluster.Servers {
			if srv.State == stateUnconn {
				cluster.StagingServer = srv
				break
			}
		}
	}

	bcksrv := cluster.GetBackupServer()
	if bcksrv != nil && !bcksrv.HasBackupLogicalCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Current master has no backup. Create logical backup for refresh staging on %s", bcksrv.URL)

		err = bcksrv.JobBackupLogical()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Create logical backup for refresh staging on %s failed: %s", bcksrv.URL, err)
			return err
		}
	}

	cluster.IsNeedStagingChange = true
	cluster.IsRefreshStaging = true
	defer func() {
		cluster.IsRefreshStaging = false
	}()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging initiated")

	if cluster.Conf.TopologyStagingRefreshScript != "" {
		return cluster.RefreshStagingScript()
	}

	NB_SLAVES := len(cluster.GetSlaves())

	if NB_SLAVES == 2 {
		STG := cluster.GetSlaveByIndex(0)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Refresh staging initiated")

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Stopping slave %s replication", STG.Name)
		STG.StopSlave()

		// Wait until the slave is stopped
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Waiting for slave %s to stop replication", STG.URL)
		waitstart := time.Now()
		for STG.State == stateSlave {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to stop replication", STG.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s replication stopped", STG.URL)

		fileio, err := os.OpenFile(STG.Datadir+"/replications.json", os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error opening replication info file: %s", err)
		} else {
			defer fileio.Close()
			encoder := json.NewEncoder(fileio)
			encoder.SetIndent("", "\t")
			err = encoder.Encode(STG.Replications)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error encoding replication info: %s", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication info saved to %s", fileio.Name())
			}
		}

		_, err = STG.ResetSlave()
		if err != nil {
			return err
		}

		waitstart = time.Now()
		for STG.State != stateUnconn {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to be reset", STG.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s is now STANDALONE", STG.URL)
	} else if NB_SLAVES == 1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Picking a slave and standalone")

		STG := cluster.StagingServer
		if STG == nil {
			STG, err = cluster.GetStandaloneServerByIndex(0)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error getting standalone server: %s", err)
				return err
			}
		}

		if STG == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No standalone server found")
			return fmt.Errorf("no standalone server found")
		}

		SL := cluster.GetSlaveByIndex(0)
		if SL == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave server found")
			return fmt.Errorf("no slave server found")
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Switching staging from STANDALONE %s to SLAVE %s", STG.URL, SL.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Stopping slave %s replication", SL.URL)

		SL.StopSlave()

		// Wait until the slave is stopped
		waitstart := time.Now()
		for SL.State == stateSlave {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to stop replication", SL.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s replication stopped", SL.URL)

		fileio, err := os.OpenFile(SL.Datadir+"/replications.json", os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error opening replication info file: %s", err)
		} else {
			defer fileio.Close()
			encoder := json.NewEncoder(fileio)
			encoder.SetIndent("", "\t")
			err = encoder.Encode(SL.Replications)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error encoding replication info: %s", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication info saved to %s", fileio.Name())
			}
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Resetting master position on slave %s", SL.URL)
		SL.StopSlave()
		_, err = SL.ResetSlave()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error resetting slave: %s", err)
			return err
		}

		waitstart = time.Now()
		for SL.State != stateUnconn {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to be reset", SL.URL)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error resetting slave: %s", err)
				return err
			}

			time.Sleep(1 * time.Second)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s is now STANDALONE", SL.URL)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Restoring STANDALONE %s as SLAVE", STG.URL)

		// Set as read only
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Setting STANDALONE %s to read only", STG.URL)
		STG.SetReadOnly()

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Resetting master position on standalone %s", STG.URL)
		_, err = STG.ResetMaster()
		if err != nil {
			return err
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Reseed standalone %s", STG.URL)

		err = STG.JobReseedLogicalBackup("default")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for refresh staging on %s failed: %s", STG.URL, err)
			return err
		}

		waitstart = time.Now()
		for STG.State != stateSlave && STG.State != stateSlaveLate {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for standalone %s to be reseeded", STG.URL)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for refresh staging on %s failed: %s", STG.URL, err)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Standalone %s is now a slave", STG.URL)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Refresh staging completed")
	}

	return nil
}

func (cluster *Cluster) RefreshStagingScript() error {
	script := cluster.Conf.TopologyStagingRefreshScript

	if _, err := os.Stat(script); os.IsNotExist(err) {
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error checking script %s: %s", script, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Run refresh staging script %s", script)

	cmd := exec.Command(script)
	cmd.Env = cluster.GetExecEnv()
	stdoutIn, err := cmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}
	stderrIn, err := cmd.StderrPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}

	if err := cmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		cluster.CopyLogs(stdoutIn, config.ConstLogModTask, config.LvlInfo, "staging")
		wg.Done()
	}()

	go func() {
		cluster.CopyLogs(stderrIn, config.ConstLogModTask, config.LvlInfo, "staging")
		wg.Done()
	}()

	wg.Wait()

	err = cmd.Wait()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "%s\n", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging script completed")

	return nil
}

func (cluster *Cluster) PostDetachStaging(host, port, newstate, oldstate string) error {
	if cluster.Conf.TopologyStaging && cluster.Conf.TopologyStagingPostDetachScript != "" {
		script := cluster.Conf.TopologyStagingPostDetachScript

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Run post detach staging script %s", script)

		cmd := exec.Command(script, cluster.Name, host, port, newstate, oldstate, cluster.GetDbUser(), cluster.GetDbPass())
		cmd.Env = cluster.GetExecEnv()
		stdoutIn, _ := cmd.StdoutPipe()
		stderrIn, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed post detach command : %s %s", cmd.Path, err)
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			cluster.CopyLogs(stdoutIn, config.ConstLogModTask, config.LvlInfo, "staging")
			wg.Done()
		}()

		go func() {
			cluster.CopyLogs(stderrIn, config.ConstLogModTask, config.LvlInfo, "staging")
			wg.Done()
		}()

		wg.Wait()

		err := cmd.Wait()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "%s\n", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) AddProxyToStagingHosts(prx DatabaseProxy) {
	if prx == nil {
		return
	}

	prx.SetStaging(true)

	if cluster.Conf.StagingProxyHosts == "" {
		cluster.Conf.StagingProxyHosts = prx.GetName()
	} else {
		cluster.Conf.StagingProxyHosts = cluster.Conf.StagingProxyHosts + "," + prx.GetName()
	}
}

func (cluster *Cluster) DelProxyFromStagingHosts(prx DatabaseProxy) {
	prx.SetStaging(false)

	// Remove from staging hosts
	cluster.Conf.StagingProxyHosts = misc.RemoveFromList(cluster.Conf.StagingProxyHosts, prx.GetName())
}

func (cluster *Cluster) GetStagingProxyHosts() []string {
	if cluster.Conf.StagingProxyHosts == "" {
		return []string{}
	}
	return strings.Split(cluster.Conf.StagingProxyHosts, ",")
}
