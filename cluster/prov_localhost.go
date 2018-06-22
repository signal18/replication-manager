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
	"os/user"
	"time"

	"github.com/jmoiron/sqlx"
)

func readPidFromFile(pidfile string) (string, error) {
	d, err := ioutil.ReadFile(pidfile)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(d)), nil
}

func (cluster *Cluster) LocalhostProvisionCluster() error {
	err := cluster.LocalhostProvisionDatabases()
	if err != nil {
		return err
	}

	err = cluster.LocalhostProvisionProxies()
	if err != nil {
		return err
	}
	return nil
}

func (cluster *Cluster) LocalhostProvisionProxies() error {

	for _, prx := range cluster.Proxies {
		err := cluster.LocalhostProvisionProxyService(prx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (cluster *Cluster) LocalhostProvisionDatabases() error {
	for _, server := range cluster.Servers {
		cluster.LogPrintf(LvlInfo, "Starting Server %s", server.URL)

		if server.State == stateFailed || server.State == stateSuspect {
			cluster.LocalhostProvisionDatabaseService(server)
		}
	}
	return nil

}

func (cluster *Cluster) LocalhostUnprovision() error {

	for _, server := range cluster.Servers {
		cluster.StopDatabaseService(server)
	}
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionDatabaseService(server *ServerMonitor) error {
	cluster.LocalhostStopDatabaseService(server)
	return nil

}

func (cluster *Cluster) LocalhostProvisionProxyService(prx *Proxy) error {
	if prx.Type == proxySpider {
		cluster.LogPrintf(LvlInfo, "Bootstrap MariaDB Sharding Cluster")
		srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, "mdbsproxy.cnf", "")
		err := srv.Refresh()
		if err == nil {
			cluster.LogPrintf(LvlWarn, "Can connect to requested signal18 sharding proxy")
			//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
			return nil
		}
		srv.ClusterGroup = cluster
		err = cluster.LocalhostProvisionDatabaseService(srv)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Bootstrap MariaDB Sharding Cluster Failed")
			return err
		}
		srv.Close()
		cluster.ShardProxyBootstrap(prx)
	}
	return nil
}

func (cluster *Cluster) LocalhostProvisionDatabaseService(server *ServerMonitor) error {
	out := &bytes.Buffer{}
	path := cluster.Conf.WorkingDir + "/" + server.Id
	//os.RemoveAll(path)

	cmd := exec.Command("rm", "-rf", path)

	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return err
	}
	cluster.LogPrintf(LvlInfo, "Remove datadir done: %s", out.Bytes())

	cmd = exec.Command("cp", "-rp", cluster.Conf.ShareDir+"/tests/data"+cluster.Conf.ProvDatadirVersion, path)

	// Attach buffer to command
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return err
	}
	cluster.LogPrintf(LvlInfo, "Copy fresh datadir done: %s", out.Bytes())

	err = cluster.LocalhostStartDatabaseService(server)
	if err != nil {
		return err
	}

	return nil
}

func (cluster *Cluster) LocalhostStopDatabaseService(server *ServerMonitor) error {
	_, err := server.Conn.Exec("SHUTDOWN")
	if err != nil {
		cluster.LogPrintf("TEST", "Shutdown failed %s", err)
	}
	//	cluster.LogPrintf("TEST", "Killing database %s %d", server.Id, server.Process.Pid)

	//	killCmd := exec.Command("kill", "-9", fmt.Sprintf("%d", server.Process.Pid))
	//	killCmd.Run()
	return nil
}

func (cluster *Cluster) LocalhostStartDatabaseService(server *ServerMonitor) error {

	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogPrintf("TEST", "Found no os process continue with start ")
		}

	}
	path := cluster.Conf.WorkingDir + "/" + server.Id
	/*	err := os.RemoveAll(path + "/" + server.Id + ".pid")
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s", err)
			return err
		}*/
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return err
	}
	mariadbdCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+cluster.Conf.ShareDir+"/tests/etc/"+server.TestConfig, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+cluster.Conf.WorkingDir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--general_log=1", "--general_log_file="+path+"/"+server.Id+".log", "--pid_file="+path+"/"+server.Id+".pid", "--log-error="+path+"/"+server.Id+".err")
	cluster.LogPrintf(LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)
	mariadbdCmd.Start()
	server.Process = mariadbdCmd.Process
	mariadbdCmd.Process.Release()
	exitloop := 0
	for exitloop < 30 {
		time.Sleep(time.Millisecond * 2000)
		cluster.LogPrintf(LvlInfo, "Waiting database startup ..")
		dsn := "root:@unix(" + cluster.Conf.WorkingDir + "/" + server.Id + ".sock)/?timeout=15s"
		conn, err2 := sqlx.Open("mysql", dsn)
		if err2 == nil {
			defer conn.Close()
			conn.Exec("set sql_log_bin=0")
			grants := "grant all on *.* to '" + server.User + "'@'localhost' identified by '" + server.Pass + "'"
			conn.Exec(grants)
			cluster.LogPrintf(LvlInfo, "%s", grants)
			grants = "grant all on *.* to '" + server.User + "'@'%' identified by '" + server.Pass + "'"
			conn.Exec(grants)
			grants = "grant all on *.* to '" + server.User + "'@'127.0.0.1' identified by '" + server.Pass + "'"
			conn.Exec(grants)
			exitloop = 100
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogPrintf(LvlInfo, "Database started.")

	} else {
		cluster.LogPrintf(LvlInfo, "Database timeout.")
		return errors.New("Failed to start")
	}

	return nil
}
