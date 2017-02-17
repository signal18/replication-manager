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
	cluster.sme.SetFailoverState()
	for k, server := range cluster.servers {
		cluster.LogPrintf("INFO : Starting Server %s", cluster.cfgGroup+strconv.Itoa(k))
		cluster.initMariaDB(server, cluster.cfgGroup+strconv.Itoa(k), "semisync.cnf")
	}
	cluster.sme.RemoveFailoverState()
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
	cluster.LogPrintf("TEST : Killing MariaDB %s %d", server.Name, server.Process.Pid)
	server.Process.Kill()
	return nil
}

func (cluster *Cluster) startMariaDB(server *ServerMonitor) error {
	cluster.LogPrintf("TEST : Starting MariaDB %s", server.Name)

	path := cluster.conf.HttpRoot + "/tests/" + server.Name
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	mariadbdCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+path+"/../etc/"+server.Conf, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket=/tmp/"+server.Name+".sock", "--user="+usr.Username, "--pid_file=/tmp/"+server.Name+".pid", "--log-error="+path+"/"+server.Name+".err")
	cluster.LogPrintf("%s %s", mariadbdCmd.Path, mariadbdCmd.Args)
	mariadbdCmd.Start()
	server.Process = mariadbdCmd.Process

	exitloop := 0
	for exitloop < 30 {
		time.Sleep(time.Millisecond * 2000)
		cluster.LogPrint("Waiting MariaDB startup ..")
		err2 := server.refresh()
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

func (cluster *Cluster) waitFailoverEnd() {
	for cluster.sme.IsInFailover() {
		time.Sleep(time.Second)
		cluster.LogPrintf("TEST: Waiting for failover stopped.")
	}
	time.Sleep(recover_time * time.Second)
}

func (cluster *Cluster) waitFailoverStart() error {
	exitloop := 0
	for exitloop < 30 {
		time.Sleep(time.Millisecond * 2000)
		cluster.LogPrint("TEST: Waiting Failover startup")

		select {
		case sig := <-switchoverChan:
			if sig {
				exitloop = 100
			}
		case sig := <-failoverChan:
			if sig {
				exitloop = 100
			}
		default:
			//do nothing
		}

		exitloop++

	}
	if exitloop == 101 {
		cluster.LogPrintf("TEST: Failover started")

	} else {
		cluster.LogPrintf("TEST: Failover timeout")
		return errors.New("Failed to Failover")
	}
	return nil
}
