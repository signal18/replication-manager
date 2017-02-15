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
	path := cluster.conf.HttpRoot + "/tests/" + name
	os.RemoveAll(path)
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}

	err = misc.CopyDir(cluster.conf.HttpRoot+"/tests/data", path)
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	/*
		  if _, err := os.Stat(path); os.IsNotExist(err) {
		    os.MkdirAll(path, 0711)

		  } else {
		    cluster.LogPrintf("ERRROR : %s", err)
		    return err
		  }
			installDB := exec.Command(cluster.conf.MariaDBBinaryPath+"/scripts/mysql_install_db", "--datadir="+path, "--user="+usr.Username)
			cluster.LogPrintf("INFO : %s", installDB.Path)
			var outrun bytes.Buffer
			installDB.Stdout = &outrun

			cmdrunErr := installDB.Run()
			if cmdrunErr != nil {
							cluster.LogPrintf("ERRROR : %s", cmdrunErr)
							return cmdrunErr
			}
			cluster.LogPrintf("PROVISIONING : %s", outrun.String())
	*/

	mariadbdCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+path+"/../etc/"+conf, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--port="+server.Port, "--user="+usr.Username, "--pid="+path+"/"+name+".pid")

	cluster.LogPrintf("%s %s", mariadbdCmd.Path, mariadbdCmd.Args)
	go mariadbdCmd.Run()
	server.Process = mariadbdCmd.Process

	var err2 error
	exitloop := 0
	for exitloop < 30 {
		time.Sleep(time.Millisecond * 2000)
		cluster.LogPrint("Waiting startup ..")
		_, err2 = os.Stat(path + "/" + name + ".pid")
		if err2 == nil {
			exitloop = 30
		}
		exitloop++

	}
	if exitloop < 30 {
		cluster.LogPrintf("MariaDB started.", err)
	} else {
		cluster.LogPrintf("MariaDB start timeout.", err)
		return errors.New("Failed to start")
	}

	return nil
}

func (cluster *Cluster) killMariaDB(server *ServerMonitor) error {
	server.Process.Kill()
	return nil
}

func (cluster *Cluster) StartAllNodes() error {
	return nil
}
