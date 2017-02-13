package cluster

import (
	"bytes"
	"os"
	"os/exec"
	"os/user"
	"strconv"
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
		cluster.initMariaDB(server, cluster.cfgGroup+strconv.Itoa(k), "semisync.cnf")
	}

	return nil
}
func (cluster *Cluster) initMariaDB(server *ServerMonitor, name string, conf string) error {
	path := cluster.conf.HttpRoot + "/tests/" + name
	os.RemoveAll(path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(path, 0711)
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	usr, err := user.Current()
	if err != nil {
		cluster.LogPrintf("ERRROR : %s", err)
		return err
	}
	installDB := exec.Command(cluster.conf.MariaDBBinaryPath+"/scripts/mysql_install_db", "--datadir="+path, "--user="+usr.Username)

	var outrun bytes.Buffer
	installDB.Stdout = &outrun

	cmdrunErr := installDB.Run()
	if cmdrunErr != nil {
		cluster.LogPrintf("ERRROR : %s", cmdrunErr)
		return cmdrunErr
	}
	cluster.LogPrintf("PROVISIONING : %s", outrun.String())

	mariadbdCmd := exec.Command(cluster.conf.MariaDBBinaryPath+"/mysqld", "--defaults-file="+path+"../"+conf, "--port="+server.Port, "--server-id="+server.Port, "--datadir="+path, "--port="+server.Port, "--user="+cluster.dbUser, "--user="+usr.Username)
	mariadbdCmd.Process.Kill()
	cluster.LogPrintf("%s %s", mariadbdCmd.Path, mariadbdCmd.Args)
	go mariadbdCmd.Run()
	server.Process = mariadbdCmd.Process
	return nil
}

func (cluster *Cluster) killMariaDB(server *ServerMonitor) error {
	server.Process.Kill()
	return nil
}
