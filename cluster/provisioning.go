package cluster

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/tanji/replication-manager/misc"
)

func (cluster *Cluster) RejoinMysqldump(source *ServerMonitor, dest *ServerMonitor) error {

	dumpCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqldump", "--opt", "--hex-blob", "--events", "--disable-keys", "--apply-slave-statements", "--gtid", "--single-transaction", "--all-databases", "--host="+source.Host, "--port="+source.Port, "--user="+cluster.dbUser, "--password="+cluster.dbPass)
	clientCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysql", "--host="+dest.Host, "--port="+dest.Port, "--user="+cluster.dbUser, "--password="+cluster.dbPass)
	//disableBinlogCmd := exec.Command("echo", "\"set sql_bin_log=0;\"")
	var err error
	clientCmd.Stdin, err = dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogPrintf("Error opening pipe: %s", err)
		return err
	}
	if err := dumpCmd.Start(); err != nil {
		cluster.LogPrintf("Error in mysqldump command: %s at %s", err, dumpCmd.Path)
		return err
	}
	if err := clientCmd.Run(); err != nil {
		cluster.LogPrintf("Error starting client:%s at %s", err, clientCmd.Path)
		return err
	}
	return nil
}

func (cluster *Cluster) InitClusterSemiSync() error {
	for k, server := range cluster.servers {
		cluster.LogPrintf("INFO : Starting Server %s", cluster.cfgGroup+strconv.Itoa(k))
		cluster.initMariaDB(server, cluster.cfgGroup+strconv.Itoa(k), "semisync.cnf")
	}
	return nil
}

func (cluster *Cluster) ShutdownClusterSemiSync() error {
	for _, server := range cluster.servers {
		cluster.killMariaDB(server)
	}

	return nil
}

func (cluster *Cluster) initMariaDB(server *ServerMonitor, name string, conf string) error {
	server.Name = name
	server.Conf = conf
	path := cluster.conf.HttpRoot + "/tests/" + name
	os.RemoveAll(path)

	err := misc.CopyDir(cluster.conf.HttpRoot+"/tests/data", path)
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	err = cluster.startMariaDB(server)
	if err == nil {
		_, err := server.Conn.Exec("grant all on *.* to root@'%' identified by ''")
		if err != nil {
			cluster.LogPrintf("TESTING : GRANTS %s", err)
		}
	}

	return nil
}

func (cluster *Cluster) killMariaDB(server *ServerMonitor) error {
	server.Process.Kill()
	return nil
}

func (cluster *Cluster) startMariaDB(server *ServerMonitor) error {
	path := cluster.conf.HttpRoot + "/tests/" + server.Name
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	mariadbdCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+path+"/../etc/"+server.Conf, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket=/tmp/"+server.Name+".sock", "--user="+usr.Username, "--pid_file=/tmp/"+server.Name+".pid")
	cluster.LogPrintf("%s %s", mariadbdCmd.Path, mariadbdCmd.Args)
	go mariadbdCmd.Run()
	server.Process = mariadbdCmd.Process

	var err2 error
	exitloop := 0
	for exitloop < 30 {
		time.Sleep(time.Millisecond * 2000)
		cluster.LogPrint("Waiting startup ..")
		_, err2 = os.Stat("/tmp/" + server.Name + ".pid")
		if err2 == nil {
			exitloop = 100
		}
		exitloop++

	}
	if exitloop == 101 {
		cluster.LogPrintf("MariaDB started.")

	} else {
		cluster.LogPrintf("MariaDB timeout.")
		return errors.New("Failed to start")
	}
	return nil
}

func (cluster *Cluster) StartAllNodes() error {

	return nil
}
