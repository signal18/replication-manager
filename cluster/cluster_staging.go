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
	var script string

	filename := "staging_refresh.sh"

	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging not enabled")
		return nil
	}

	if cluster.StagingServer == nil {
		for _, srv := range cluster.Servers {
			if srv.State == stateUnconn {
				cluster.StagingServer = srv
				break
			}
		}
	}

	cluster.IsRefreshStaging = true
	defer func() {
		cluster.IsRefreshStaging = false
	}()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging initiated")

	script = cluster.Conf.TopologyStagingRefreshScript
	if cluster.Conf.TopologyStagingRefreshScript == "" {
		script = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + filename

		if _, err := os.Stat(script); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Staging script not found, reloading")
			err := cluster.ReloadStagingScript()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error reloading staging script. %s", err)
				return err
			}
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

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging completed")

	for _, srv := range cluster.Servers {
		if srv.State == stateUnconn {
			cluster.StagingServer = srv
			break
		}
	}

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
