package cluster

import (
	"os"
	"os/exec"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
)

func (cluster *Cluster) ReloadStagingScript() error {
	var script string
	var err error
	var content []byte

	filename := "staging_refresh.sh"
	template := "scripts/" + filename

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reload staging script")

	script = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + filename

	if cluster.Conf.WithEmbed == "ON" {
		content, err = share.EmbededDbModuleFS.ReadFile(template)
	} else {
		content, err = os.ReadFile(cluster.Conf.ShareDir + "/" + template)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reading default staging script. %s", err)
		return err
	}

	os.Remove(script)

	err = os.WriteFile(script, content, 0755)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing default staging script. %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) RefreshStaging() error {
	var script string

	filename := "staging_refresh.sh"

	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Refresh staging not enabled")
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Refresh staging initiated")

	script = cluster.Conf.TopologyStagingRefreshScript
	if cluster.Conf.TopologyStagingRefreshScript == "" {
		script = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + filename

		if _, err := os.Stat(script); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Staging script not found, reloading")
			err := cluster.ReloadStagingScript()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error reloading staging script. %s", err)
				return err
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Run refresh staging script %s", script)
	cmd := exec.Command(script)
	cmd.Env = cluster.GetExecEnv()
	stdoutIn, err := cmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}
	stderrIn, err := cmd.StderrPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}

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

	err = cmd.Wait()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s\n", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Refresh staging completed")

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
