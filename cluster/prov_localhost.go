// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
)

func readPidFromFile(pidfile string) (string, error) {
	d, err := ioutil.ReadFile(pidfile)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(d)), nil
}

func (cluster *Cluster) LocalhostUnprovisionDatabaseService(server *ServerMonitor) error {
	cluster.LocalhostStopDatabaseService(server)
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionProxyService(prx *Proxy) error {
	prx.GetProxyConfig()
	if prx.Type == config.ConstProxySpider {
		cluster.LogPrintf(LvlInfo, "Bootstrap MariaDB Sharding Cluster")
		srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.Port, prx.User, prx.Pass, true)
		err := srv.Refresh()
		if err == nil {
			cluster.LogPrintf(LvlWarn, "Can connect to requested signal18 sharding proxy")
			//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
			cluster.errorChan <- err
			return nil
		}
		srv.ClusterGroup = cluster
		err = cluster.LocalhostProvisionDatabaseService(srv)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Bootstrap MariaDB Sharding Cluster Failed")
			cluster.errorChan <- err
			return err
		}
		srv.Close()
		cluster.ShardProxyBootstrap(prx)
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionProxyService(prx *Proxy) error {

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostProvisionDatabaseService(server *ServerMonitor) error {
	out := &bytes.Buffer{}
	path := server.Datadir + "/var"
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
	server.GetMyConfig()
	os.Symlink(server.Datadir+"/init/data", path)

	/*cmd = exec.Command("cp", "-rp", cluster.Conf.ShareDir+"/tests/data"+cluster.Conf.ProvDatadirVersion, path)

	// Attach buffer to command
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)
		return err
	}
	cluster.LogPrintf(LvlInfo, "Copy fresh datadir done: %s", out.Bytes())

	cmd = exec.Command("cp", "-rp", server.Datadir+"/init/data/.system", path+"/")
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlErr, "cp -rp %s %s failed %s ", server.Datadir+"/init/data/.system", path, err)
		cluster.LogPrintf(LvlInfo, "init fresh datadir err: %s", out.Bytes())
		return err
	}
	cluster.LogPrintf(LvlInfo, "copy datadir done: %s", out.Bytes())
	*/
	sysCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/../scripts/mysql_install_db", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--datadir="+server.Datadir+"/var", "--basedir="+cluster.Conf.MariaDBBinaryPath+"/../", "--force")
	sysCmd.Stdout = out
	err = sysCmd.Run()
	if err != nil {
		cluster.LogPrintf(LvlInfo, "init fresh datadir err: %s", out.Bytes())
		cluster.LogPrintf(LvlErr, "%s", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogPrintf(LvlInfo, "init fresh datadir done: %s", out.Bytes())
	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogPrintf("TEST", "Found no os process continue with start ")
		}

	}

	/*	err := os.RemoveAll(path + "/" + server.Id + ".pid")
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s", err)
			return err
		}*/

	err = cluster.LocalhostStartDatabaseServiceFistTime(server)
	if err != nil {
		cluster.errorChan <- err
		return err

	}
	cluster.errorChan <- nil
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

func (cluster *Cluster) LocalhostStartDatabaseServiceFistTime(server *ServerMonitor) error {

	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogPrintf("TEST", "Found no os process continue with start ")
		}

	}
	path := server.Datadir + "/var"
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
	//	mariadbdCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf --port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.Datadir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--general_log=1", "--general_log_file="+path+"/"+server.Id+".log", "--pid_file="+path+"/"+server.Id+".pid", "--log-error="+path+"/"+server.Id+".err")
	time.Sleep(time.Millisecond * 2000)
	mariadbdCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.Datadir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--pid_file="+path+"/"+server.Id+".pid")
	cluster.LogPrintf(LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err = mariadbdCmd.Run()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s ", err)
		}
		fmt.Printf("Command finished with error: %v", err)
	}()
	exitloop := 0
	time.Sleep(time.Millisecond * 4000)
	for exitloop < 30 {
		haveerror := false
		time.Sleep(time.Millisecond * 2000)
		//cluster.LogPrintf(LvlInfo, "Waiting database startup ")
		cluster.LogPrintf(LvlInfo, "Waiting database first start   .. %s", out)
		dsn := "root:@unix(" + server.Datadir + "/" + server.Id + ".sock)/?timeout=15s"
		conn, err2 := sqlx.Open("mysql", dsn)
		if err2 == nil {
			defer conn.Close()
			_, err := conn.Exec("set sql_log_bin=0")
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", "set sql_log_bin=0", err)
			}
			_, err = conn.Exec("delete from mysql.user where password=''")
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", "delete from mysql.user where password=''", err)
			}
			grants := "grant all on *.* to '" + server.User + "'@'localhost' identified by '" + server.Pass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			cluster.LogPrintf(LvlInfo, "%s", grants)
			grants = "grant all on *.* to '" + server.User + "'@'%' identified by '" + server.Pass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.User + "'@'127.0.0.1' identified by '" + server.Pass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.ClusterGroup.rplUser + "'@'localhost' identified by '" + server.ClusterGroup.rplPass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			cluster.LogPrintf(LvlInfo, "%s", grants)
			grants = "grant all on *.* to '" + server.ClusterGroup.rplUser + "'@'%' identified by '" + server.ClusterGroup.rplPass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			grants = "grant all on *.* to '" + server.ClusterGroup.rplUser + "'@'127.0.0.1' identified by '" + server.ClusterGroup.rplPass + "'"
			_, err = conn.Exec(grants)
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", grants, err)
			}
			_, err = conn.Exec("flush privileges")
			if err != nil {
				haveerror = true
				cluster.LogPrintf(LvlErr, " %s %s ", "flush privileges", err)
			}

			if !haveerror {
				exitloop = 100
			}
		} else {
			cluster.LogPrintf(LvlErr, "Database connection to init user  %s ", err2)
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogPrintf(LvlInfo, "Database started.")

	} else {
		cluster.LogPrintf(LvlInfo, "Database timeout.")
		return errors.New("Failed to start")
	}

	//	mariadbdCmd.Process.Release()

	return nil
}

func (cluster *Cluster) LocalhostStartDatabaseService(server *ServerMonitor) error {
	server.GetMyConfig()
	if server.Id == "" {
		_, err := os.Stat(server.Id)
		if err != nil {
			cluster.LogPrintf("TEST", "Found no os process continue with start ")
		}

	}
	path := server.Datadir + "/var"
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
	//	mariadbdCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf --port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.Datadir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--general_log=1", "--general_log_file="+path+"/"+server.Id+".log", "--pid_file="+path+"/"+server.Id+".pid", "--log-error="+path+"/"+server.Id+".err")
	time.Sleep(time.Millisecond * 2000)
	mariadbdCmd := exec.Command(cluster.Conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+server.Datadir+"/init/etc/mysql/my.cnf", "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+server.Datadir+"/"+server.Id+".sock", "--user="+usr.Username, "--bind-address=0.0.0.0", "--pid_file="+path+"/"+server.Id+".pid")
	cluster.LogPrintf(LvlInfo, "%s %s", mariadbdCmd.Path, mariadbdCmd.Args)

	var out bytes.Buffer
	mariadbdCmd.Stdout = &out

	go func() {
		err = mariadbdCmd.Run()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s ", err)
		}
		fmt.Printf("Command finished with error: %v", err)
	}()

	exitloop := 0
	time.Sleep(time.Millisecond * 4000)
	for exitloop < 30 {

		time.Sleep(time.Millisecond * 2000)
		//cluster.LogPrintf(LvlInfo, "Waiting database startup ")
		cluster.LogPrintf(LvlInfo, "Waiting database startup .. %s", out)
		conn, err2 := sqlx.Open("mysql", server.DSN)
		if err2 == nil {
			defer conn.Close()
			exitloop = 100

		} else {
			cluster.LogPrintf(LvlErr, "Database connection to init user  %s ", err2)
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogPrintf(LvlInfo, "Database started.")

	} else {
		cluster.LogPrintf(LvlInfo, "Database timeout.")
		return errors.New("Failed to start")
	}
	server.Process = mariadbdCmd.Process
	//	mariadbdCmd.Process.Release()

	return nil
}

func (cluster *Cluster) LocalhostStartProxyService(server *Proxy) error {
	return errors.New("Can't start proxy")
}
func (cluster *Cluster) LocalhostStopProxyService(server *Proxy) error {
	return errors.New("Can't stop proxy")
}

func (cluster *Cluster) LocalhostGetNodes() ([]Agent, error) {
	var info runtime.MemStats
	runtime.ReadMemStats(&info)

	name, err := os.Hostname()
	if err != nil {
		name = "127.0.0.1"
	}
	agents := []Agent{}
	/*	m.Alloc = rtm.Alloc
		m.TotalAlloc = rtm.TotalAlloc
		m.Sys = rtm.Sys
		m.Mallocs = rtm.Mallocs
		m.Frees = rtm.Frees
	*/

	var agent Agent
	agent.Id = "1"
	agent.OsName = cluster.Conf.GoOS
	agent.OsKernel = cluster.Conf.GoArch
	agent.CpuCores = int64(runtime.NumCPU())
	agent.CpuFreq = 0
	agent.MemBytes = int64(info.Sys)
	agent.HostName = name
	agents = append(agents, agent)

	return agents, nil
}

func (cluster *Cluster) LocalhostGetFreePort() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	defer listener.Close()
	return port, nil
}
