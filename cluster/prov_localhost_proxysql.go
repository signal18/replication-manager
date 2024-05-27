// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/signal18/replication-manager/config"
)

// TODO: Make all of these part of ProxySQLProxy and not Cluster

func (cluster *Cluster) LocalhostUnprovisionProxySQLService(prx *ProxySQLProxy) error {
	cluster.LocalhostStopProxySQLService(prx)

	out := &bytes.Buffer{}
	path := prx.Datadir //+ "/var"
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Remove datadir done: %s", out.Bytes())

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionProxySQLService(prx *ProxySQLProxy) error {

	out := &bytes.Buffer{}
	path := prx.Datadir + "/var"
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Remove datadir done: %s", out.Bytes())
	prx.GetProxyConfig()
	os.Symlink(prx.Datadir+"/init/data", path)

	err = cluster.LocalhostStartProxySQLService(prx)
	if err != nil {
		cluster.errorChan <- err
		return err

	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopProxySQLService(prx *ProxySQLProxy) error {

	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,"TEST", "Killing database %s %d", server.Id, server.Process.Pid)
	prx.Shutdown()
	//killCmd := exec.Command("kill", "-9", fmt.Sprintf("%d", prx.Process.Pid))
	//killCmd.Run()
	return nil
}

func (cluster *Cluster) LocalhostStartProxySQLService(prx *ProxySQLProxy) error {
	prx.GetProxyConfig()

	/*	path := prx.Datadir + "/var"
			err := os.RemoveAll(path + "/" + server.Id + ".pid")
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
					return err
				}
			usr, err := user.Current()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", err)
			return err
		}	*/

	mariadbdCmd := exec.Command(cluster.Conf.ProxysqlBinaryPath, "--config", prx.Datadir+"/init/etc/proxysql/proxysql.cnf", "--datadir", prx.Datadir+"/var", "--initial")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err := mariadbdCmd.Run()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s ", err)
			fmt.Printf("Command finished with error: %v", err)
		}
	}()
	time.Sleep(time.Millisecond * 2000)
	prx.Process = mariadbdCmd.Process
	//	mariadbdCmd.Process.Release()

	return nil
}
