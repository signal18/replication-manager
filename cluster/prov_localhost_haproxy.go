// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
)

func (cluster *Cluster) LocalhostUnprovisionHaProxyService(prx *Proxy) error {
	cluster.LocalhostStopHaProxyService(prx)
	os.RemoveAll(prx.Datadir + "/var")
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionHaProxyService(prx *Proxy) error {

	out := &bytes.Buffer{}
	path := prx.Datadir + "/var"
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}
	cluster.LogPrintf(LvlInfo, "Remove datadir done: %s", out.Bytes())
	prx.GetProxyConfig()
	os.Symlink(prx.Datadir+"/init/data", path)

	err = cluster.LocalhostStartHaProxyService(prx)
	if err != nil {
		cluster.errorChan <- err
		return err

	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopHaProxyService(prx *Proxy) error {

	//	cluster.LogPrintf("TEST", "Killing database %s %d", server.Id, server.Process.Pid)

	pid, err := ioutil.ReadFile(prx.Datadir + "/var/haproxy.pid")
	if err != nil {
		return errors.New("No such file " + prx.Datadir + "/var/haproxy.pid")
	}
	killCmd := exec.Command("kill", "-9", strings.Trim(string(pid), "\n"))
	killCmd.Run()
	return nil
}

func (cluster *Cluster) LocalhostStartHaProxyService(prx *Proxy) error {
	prx.GetProxyConfig()
	//init haproxy do start or reload
	cluster.initHaproxy(prx)
	/*mariadbdCmd := exec.Command(cluster.Conf.HaproxyBinaryPath+"/haproxy", "--config="+prx.Datadir+"/init/conf/haproxy.cnf", "--datadir="+prx.Datadir+"/var")
	cluster.LogPrintf(LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err := mariadbdCmd.Run()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s ", err)
			fmt.Printf("Command finished with error: %v", err)
		}
	}()
	time.Sleep(time.Millisecond * 2000)
	prx.Process = mariadbdCmd.Process*/
	//	mariadbdCmd.Process.Release()

	return nil
}
