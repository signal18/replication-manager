package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"time"

	"github.com/tanji/replication-manager/dbhelper"
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

func readPidFromFile(pidfile string) (string, error) {
	d, err := ioutil.ReadFile(pidfile)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(d)), nil
}

func (cluster *Cluster) InitClusterSemiSync() error {
	cluster.sme.SetFailoverState()
	for k, server := range cluster.servers {
		cluster.LogPrintf("INFO : Starting Server %s", cluster.cfgGroup+strconv.Itoa(k))
		if server.Conn.Ping() == nil {
			cluster.LogPrintf("INFO : DB Server is not stop killing now %s", server.URL)
			if server.Name == "" {
				pidfile, _ := dbhelper.GetVariableByName(server.Conn, "PID_FILE")
				pid, _ := readPidFromFile(pidfile)
				server.Name = cluster.cfgGroup + strconv.Itoa(k)
				pidint, _ := strconv.Atoi(pid)
				server.Process, _ = os.FindProcess(pidint)
			}

			cluster.killMariaDB(server)
		}
		cluster.initMariaDB(server, cluster.cfgGroup+strconv.Itoa(k), "semisync.cnf")
	}
	cluster.sme.RemoveFailoverState()

	return nil
}

func (cluster *Cluster) ShutdownClusterSemiSync() error {
	cluster.sme.SetFailoverState()
	for _, server := range cluster.servers {
		cluster.killMariaDB(server)

	}
	/*server.delete(&cluster.slaves)
	server.delete(&cluster.servers)*/

	cluster.servers = nil
	cluster.slaves = nil
	cluster.master = nil
	cluster.sme.UnDiscovered()
	cluster.newServerList()

	cluster.sme.RemoveFailoverState()

	return nil
}

func (cluster *Cluster) initMariaDB(server *ServerMonitor, name string, conf string) error {
	server.Name = name
	server.Conf = conf
	path := cluster.conf.WorkingDir + "/" + name
	os.RemoveAll(path)
	mvCommand := exec.Command("cp", "-rp", cluster.conf.ShareDir+"/tests/data", path)
	mvCommand.Run()
	/*err := misc.CopyDir(cluster.conf.ShareDir+"/tests/data", path)
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}*/

	time.Sleep(time.Millisecond * 2000)
	err := cluster.startMariaDB(server)
	time.Sleep(time.Millisecond * 2000)
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

	//	server.Process.Kill()
	killCmd := exec.Command("kill", "-9", fmt.Sprintf("%d", server.Process.Pid))
	killCmd.Run()

	//cluster.waitMariaDBStop(server)
	return nil
}

func (cluster *Cluster) startMariaDB(server *ServerMonitor) error {
	cluster.LogPrintf("TEST : Starting MariaDB %s", server.Name)
	path := cluster.conf.WorkingDir + "/" + server.Name
	err := os.RemoveAll(path + "/" + server.Name + ".pid")
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	mariadbdCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+cluster.conf.ShareDir+"/tests/etc/"+server.Conf, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--socket="+cluster.conf.WorkingDir+"/"+server.Name+".sock", "--user="+usr.Username, "--general_log=1", "--general_log_file="+path+"/"+server.Name+".log", "--pid_file="+path+"/"+server.Name+".pid", "--log-error="+path+"/"+server.Name+".err")
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

	mariadbdCmdGrant := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysql", "--socket="+cluster.conf.WorkingDir+"/"+server.Name+".sock", "--user=root", "-e \"grant all on *.* to ''"+cluster.dbUser+"''@'%' identified by '"+cluster.dbPass+"'\"")
	mariadbdCmdGrant.Run()
	return nil
}

func (cluster *Cluster) StartAllNodes() error {

	return nil
}

func (cluster *Cluster) waitFailoverEndState() {
	for cluster.sme.IsInFailover() {
		time.Sleep(time.Second)
		cluster.LogPrintf("TEST: Waiting for failover stopped.")
	}
	time.Sleep(recoverTime * time.Second)
}

func (cluster *Cluster) waitFailoverEnd() error {
	cluster.waitFailoverEndState()
	return nil

	// following code deadlock they may be cases where the channel blocked lacking a receiver
	/*exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Failover startup")
			exitloop++
		case sig := <-endfailoverChan:
			if sig {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Failover started")
	} else {
		cluster.LogPrintf("TEST: Failover timeout")
		return errors.New("Failed to Failover")
	}
	return nil*/
}

func (cluster *Cluster) waitFailover(wg *sync.WaitGroup) {

	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Failover end")
			exitloop++
		case <-cluster.failoverCond.Recv:
			return
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Failover end")
	} else {
		cluster.LogPrintf("TEST: Failover end timeout")
		return
	}
	return
}

func (cluster *Cluster) waitSwitchover(wg *sync.WaitGroup) {

	defer wg.Done()
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Switchover end")
			exitloop++
		case <-cluster.switchoverCond.Recv:
			return
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Switchover end")
	} else {
		cluster.LogPrintf("TEST: Switchover end timeout")
		return
	}
	return
}

func (cluster *Cluster) waitRejoin(wg *sync.WaitGroup) {

	defer wg.Done()

	exitloop := 0

	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {

		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Rejoin ")
			exitloop++
		case <-cluster.rejoinCond.Recv:
			return

		default:

		}

	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Rejoin Finished")

	} else {
		cluster.LogPrintf("TEST: Rejoin timeout")
		return
	}
	return
}

func (cluster *Cluster) waitMariaDBStop(server *ServerMonitor) error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting MariaDB shutdown")
			exitloop++
			_, err := os.FindProcess(server.Process.Pid)
			if err != nil {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: MariaDB shutdown")
	} else {
		cluster.LogPrintf("TEST: MariaDB shutdown timeout")
		return errors.New("Failed to Stop MariaDB")
	}
	return nil
}

func (cluster *Cluster) waitBootstrapDiscovery() error {
	cluster.LogPrint("TEST: Waiting Bootstrap and discovery")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Bootstrap and discovery")
			exitloop++
			if cluster.sme.IsDiscovered() {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Cluster is Bootstraped and discovery")
	} else {
		cluster.LogPrintf("TEST: Bootstrap timeout")
		return errors.New("Failed Bootstrap timeout")
	}
	return nil
}

func (cluster *Cluster) waitMasterDiscovery() error {
	cluster.LogPrint("TEST: Waiting Master Found")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * 2000)
	for exitloop < 30 {
		select {
		case <-ticker.C:
			cluster.LogPrint("TEST: Waiting Master Found")
			exitloop++
			if cluster.master != nil {
				exitloop = 100
			}
		default:
		}
	}
	if exitloop == 100 {
		cluster.LogPrintf("TEST: Master founded")
	} else {
		cluster.LogPrintf("TEST: Master found timeout")
		return errors.New("Failed Master search timeout")
	}
	return nil
}
