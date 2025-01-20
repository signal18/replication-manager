package cluster

import (
	"os"
	"os/exec"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
)

func (cluster *Cluster) RefreshStaging() error {
	var script string
	var content []byte

	filename := "staging_refresh.sh"
	template := "scripts/" + filename

	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Refresh staging not enabled")
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Refresh staging initiated")

	script = cluster.Conf.TopologyStagingRefreshScript
	if cluster.Conf.TopologyStagingRefreshScript == "" {
		script = cluster.Conf.WorkingDir + "/" + filename

		if _, err := os.Stat(script); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Refresh Staging script not found. Use default script")
			if cluster.Conf.WithEmbed == "ON" {
				content, err = share.EmbededDbModuleFS.ReadFile(template)
			} else {
				content, err = os.ReadFile(cluster.Conf.ShareDir + "/" + template)
			}
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reading default staging script. %s", err)
				return err
			}

			err = os.WriteFile(script, content, 0755)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing default staging script. %s", err)
				return err
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Run refresh staging script %s", script)
	cmd := exec.Command(script)
	cmd.Env = cluster.GetExecEnv()
	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s\n", err)
		return err
	}

	err = cluster.PostDetachStaging()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Post detach error: %s\n", err)
		return err
	}

	return nil
}

func (cluster *Cluster) PostDetachStaging() error {
	if cluster.Conf.TopologyStaging && cluster.Conf.TopologyStagingPostDetachScript != "" {
		script := cluster.Conf.TopologyStagingPostDetachScript

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Run post detach staging script %s", script)

		cmd := exec.Command(script)
		cmd.Env = cluster.GetExecEnv()
		stdoutIn, _ := cmd.StdoutPipe()
		stderrIn, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			cluster.CopyLogs(stdoutIn, config.ConstLogModGeneral, config.LvlDbg, "staging")
			wg.Done()
		}()

		go func() {
			cluster.CopyLogs(stderrIn, config.ConstLogModGeneral, config.LvlDbg, "staging")
			wg.Done()
		}()

		wg.Wait()

		err := cmd.Wait()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s\n", err)
			return err
		}
	}
	return nil
}
