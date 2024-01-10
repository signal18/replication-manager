// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bufio"
	"bytes"

	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	gzip "github.com/klauspost/pgzip"
	dumplingext "github.com/pingcap/dumpling/v4/export"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	river "github.com/signal18/replication-manager/utils/river"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) JobRun() {

}

func (server *ServerMonitor) JobsCreateTable() error {
	cluster := server.ClusterGroup
	if server.IsDown() || cluster.IsInFailover() {
		return nil
	}

	server.ExecQueryNoBinLog("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	err := server.ExecQueryNoBinLog("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task)) engine=innodb")
	if err != nil {
		// if cluster.Conf.LogLevel > 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Can't create table replication_manager_schema.jobs")
		// }
	}
	return err
}

func (server *ServerMonitor) JobInsertTaks(task string, port string, repmanhost string) (int64, error) {
	cluster := server.ClusterGroup
	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Cancel job %s during failover", task)
		return 0, errors.New("In failover can't insert job")
	}
	server.JobsCreateTable()
	conn, err := server.GetNewDBConn()
	if err != nil {
		// if cluster.Conf.LogLevel > 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Job can't connect")
		// }
		return 0, err
	}
	defer conn.Close()
	_, err = conn.Exec("set sql_log_bin=0")
	if err != nil {
		// if cluster.Conf.LogLevel > 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Job can't disable binlog for session")
		// }
		return 0, err
	}

	if task != "" {
		res, err := conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('" + task + "'," + port + ",'" + repmanhost + "', NOW())")
		if err == nil {
			return res.LastInsertId()
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Job can't insert job %s", err)
		return 0, err
	}
	return 0, nil
}

func (server *ServerMonitor) JobBackupPhysical() (int64, error) {
	//server can be nil as no dicovered master
	if server == nil {
		return 0, nil
	}
	cluster := server.ClusterGroup

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Receive physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)
	if server.IsDown() {
		return 0, nil
	}
	// not  needed to stream internaly using S3 fuse
	/*
		if cluster.Conf.BackupRestic {
			port, err := cluster.SSTRunReceiverToRestic(server.DSN + ".xbtream")
			if err != nil {
				return 0, nil
			}
			jobid, err := server.JobInsertTaks(cluster.Conf.BackupPhysicalType, port, cluster.Conf.MonitorAddress)
			return jobid, err
		} else {
	*/
	var port string
	var err error
	var backupext string = ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
		port, err = cluster.SSTRunReceiverToGZip(server.GetMyBackupDirectory()+cluster.Conf.BackupPhysicalType+backupext, ConstJobCreateFile)
		if err != nil {
			return 0, nil
		}
	} else {
		port, err = cluster.SSTRunReceiverToFile(server.GetMyBackupDirectory()+cluster.Conf.BackupPhysicalType+backupext, ConstJobCreateFile)
		if err != nil {
			return 0, nil
		}
	}

	jobid, err := server.JobInsertTaks(cluster.Conf.BackupPhysicalType, port, cluster.Conf.MonitorAddress)

	return jobid, err
	//	}
	//return 0, nil
}

func (server *ServerMonitor) JobReseedPhysicalBackup() (int64, error) {
	cluster := server.ClusterGroup
	if cluster.master != nil && !cluster.GetBackupServer().HasBackupPhysicalCookie() {
		server.createCookie("cookie_waitbackup")
		return 0, errors.New("No Physical Backup")
	}
	jobid, err := server.JobInsertTaks("reseed"+cluster.Conf.BackupPhysicalType, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Receive reseed physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}
	logs, err := server.StopSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)

	if err != nil {
		return jobid, err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Receive reseed physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobFlashbackPhysicalBackup() (int64, error) {
	cluster := server.ClusterGroup
	if cluster.master != nil && !cluster.GetBackupServer().HasBackupPhysicalCookie() {
		server.createCookie("cookie_waitbackup")
		return 0, errors.New("No Physical Backup")
	}

	jobid, err := server.JobInsertTaks("flashback"+cluster.Conf.BackupPhysicalType, server.SSTPort, cluster.Conf.MonitorAddress)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Receive reseed physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}

	logs, err := server.StopSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {
		return jobid, err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Receive reseed physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobReseedLogicalBackup() (int64, error) {
	cluster := server.ClusterGroup
	if cluster.master != nil && !cluster.GetBackupServer().HasBackupLogicalCookie() {
		server.createCookie("cookie_waitbackup")
		return 0, errors.New("No Logical Backup")
	}

	jobid, err := server.JobInsertTaks("reseed"+cluster.Conf.BackupLogicalType, server.SSTPort, cluster.Conf.MonitorAddress)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Receive reseed logical backup %s request for server: %s %s", cluster.Conf.BackupLogicalType, server.URL, err)

		return jobid, err
	}
	logs, err := server.StopSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {

		return jobid, err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Receive reseed logical backup %s request for server: %s", cluster.Conf.BackupLogicalType, server.URL)
	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go server.JobReseedMyLoader()
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerStop() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTaks("stop", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Stop server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerRestart() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTaks("restart", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Restart server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobFlashbackLogicalBackup() (int64, error) {
	cluster := server.ClusterGroup
	if cluster.master != nil && !cluster.GetBackupServer().HasBackupLogicalCookie() {
		server.createCookie("cookie_waitbackup")
		return 0, errors.New("No Logical Backup")
	}
	jobid, err := server.JobInsertTaks("flashback"+cluster.Conf.BackupLogicalType, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Receive reseed logical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	logs, err := server.StopSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {
		return jobid, err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Receive reseed logical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)
	if cluster.Conf.BackupLoadScript != "" {
		go server.JobReseedBackupScript()
	} else if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go server.JobReseedMyLoader()
	}
	return jobid, err
}

func (server *ServerMonitor) JobBackupErrorLog() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	port, err := cluster.SSTRunReceiverToFile(server.Datadir+"/log/log_error.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTaks("error", port, cluster.Conf.MonitorAddress)
}

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) ErrorLogWatcher() {
	cluster := server.ClusterGroup
	for line := range server.ErrorLogTailer.Lines {
		var log s18log.HttpMessage
		itext := strings.Index(line.Text, "]")
		if itext != -1 && len(line.Text) > itext+2 {
			log.Text = line.Text[itext+2:]
		} else {
			log.Text = line.Text
		}
		itime := strings.Index(line.Text, "[")
		if itime != -1 {
			log.Timestamp = line.Text[0 : itime-1]
			if itext != -1 {
				log.Level = line.Text[itime+1 : itext]
			}
		} else {
			log.Timestamp = fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
		}
		log.Group = cluster.GetClusterName()

		server.ErrorLog.Add(log)
	}

}

func (server *ServerMonitor) SlowLogWatcher() {
	cluster := server.ClusterGroup
	log := s18log.NewSlowMessage()
	preline := ""
	var headerRe = regexp.MustCompile(`^#\s+[A-Z]`)
	for line := range server.SlowLogTailer.Lines {
		newlog := s18log.NewSlowMessage()
		if cluster.Conf.LogSST {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "New line %s", line.Text)
		}
		log.Group = cluster.GetClusterName()
		if headerRe.MatchString(line.Text) && !headerRe.MatchString(preline) {
			// new querySelector
			if cluster.Conf.LogSST {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "New query %s", log)
			}
			if log.Query != "" {
				server.SlowLog.Add(log)
			}
			log = newlog
		}
		server.SlowLog.ParseLine(line.Text, log)

		preline = line.Text
	}

}

func (server *ServerMonitor) JobBackupSlowQueryLog() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	port, err := cluster.SSTRunReceiverToFile(server.Datadir+"/log/log_slow_query.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTaks("slowquery", port, cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobOptimize() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTaks("optimize", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobZFSSnapBack() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTaks("zfssnapback", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobReseedMyLoader() {
	cluster := server.ClusterGroup
	threads := strconv.Itoa(cluster.Conf.BackupLogicalLoadThreads)

	myargs := strings.Split(strings.ReplaceAll(cluster.Conf.BackupMyLoaderOptions, "  ", " "), " ")
	myargs = append(myargs, "--directory="+cluster.master.GetMasterBackupDirectory(), "--threads="+threads, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser(), "--password="+cluster.GetDbPass())
	dumpCmd := exec.Command(cluster.GetMyLoaderPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Command: %s", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := dumpCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "MyLoader: %s", err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Finish logical restaure %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	server.Refresh()
	if server.IsSlave {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Parsing mydumper metadata ")
		meta, err := server.JobMyLoaderParseMeta(cluster.master.GetMasterBackupDirectory())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "MyLoader metadata parsing: %s", err)
		}
		if server.IsMariaDB() && server.HaveMariaDBGTID {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Starting slave with mydumper metadata")
			server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='" + meta.BinLogUuid + "'")
			server.StartSlave()
		}
	}

}

func (server *ServerMonitor) JobReseedBackupScript() {
	cluster := server.ClusterGroup
	cmd := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(cluster.master.Host))

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Command backup load script: %s", strings.Replace(cmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	cmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "My reload script: %s", err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Finish logical restaure from load script on %s ", server.URL)

}

func (server *ServerMonitor) JobMyLoaderParseMeta(dir string) (config.MyDumperMetaData, error) {

	var m config.MyDumperMetaData
	buf := new(bytes.Buffer)

	// metadata file name.
	meta := fmt.Sprintf("%s/metadata", dir)

	// open a file.
	MetaFd, err := os.Open(meta)
	if err != nil {
		return m, err
	}
	defer MetaFd.Close()

	MetaRd := bufio.NewReader(MetaFd)
	for {
		line, err := MetaRd.ReadBytes('\n')
		if err != nil {
			break
		}

		if len(line) > 2 {
			newline := bytes.TrimLeft(line, "")
			buf.Write(bytes.Trim(newline, "\n"))
			line = []byte{}
		}
		if strings.Contains(string(buf.Bytes()), "Started") == true {
			splitbuf := strings.Split(string(buf.Bytes()), ":")
			m.StartTimestamp, _ = time.ParseInLocation("2006-01-02 15:04:05", strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "), time.Local)
		}
		if strings.Contains(string(buf.Bytes()), "Log") == true {
			splitbuf := strings.Split(string(buf.Bytes()), ":")
			m.BinLogFileName = strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " ")
		}
		if strings.Contains(string(buf.Bytes()), "Pos") == true {
			splitbuf := strings.Split(string(buf.Bytes()), ":")
			pos, _ := strconv.Atoi(strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "))

			m.BinLogFilePos = uint64(pos)
		}

		if strings.Contains(string(buf.Bytes()), "GTID") == true {
			splitbuf := strings.Split(string(buf.Bytes()), ":")
			m.BinLogUuid = strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " ")
		}
		if strings.Contains(string(buf.Bytes()), "Finished") == true {
			splitbuf := strings.Split(string(buf.Bytes()), ":")
			m.EndTimestamp, _ = time.ParseInLocation("2006-01-02 15:04:05", strings.TrimLeft(strings.Join(splitbuf[1:], ":"), " "), time.Local)
		}
		buf.Reset()

	}

	return m, nil
}

func (server *ServerMonitor) JobsCheckRunning() error {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return nil
	}
	//server.JobInsertTaks("", "", "")
	type DBTask struct {
		task string
		ct   int
	}
	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct FROM replication_manager_schema.jobs WHERE done=0 AND result IS NULL group by task ")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct)
		if task.ct > 0 {
			if task.ct > 10 {
				cluster.StateMachine.AddState("ERR00060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["ERR00060"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				purge := "DELETE from replication_manager_schema.jobs WHERE task='" + task.task + "' AND done=0 AND result IS NULL order by start asc limit  " + strconv.Itoa(task.ct-1)
				err := server.ExecQueryNoBinLog(purge)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Scheduler error purging replication_manager_schema.jobs %s", err)
				}
			} else {
				if task.task == "optimized" {
					cluster.StateMachine.AddState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "restart" {
					cluster.StateMachine.AddState("WARN0096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0096"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "stop" {
					cluster.StateMachine.AddState("WARN0097", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0097"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "xtrabackup" {
					cluster.StateMachine.AddState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "mariabackup" {
					cluster.StateMachine.AddState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedxtrabackup" {
					cluster.StateMachine.AddState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmariabackup" {
					cluster.StateMachine.AddState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmysqldump" {
					cluster.StateMachine.AddState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmydumper" {
					cluster.StateMachine.AddState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackxtrabackup" {
					cluster.StateMachine.AddState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmariabackup" {
					cluster.StateMachine.AddState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmydumper" {
					cluster.StateMachine.AddState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmysqldump" {
					cluster.StateMachine.AddState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				}

			}
		}

	}

	return nil
}

func (server *ServerMonitor) JobHandler(JobId int64) error {
	exitloop := 0
	ticker := time.NewTicker(time.Second * 3600)

	for exitloop < 8 {
		select {
		case <-ticker.C:

			exitloop++

			if true == true {
				exitloop = 8
			}
		default:
		}

	}

	return nil
}

func (server *ServerMonitor) GetMyBackupDirectory() string {
	cluster := server.ClusterGroup
	s3dir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name + "/" + server.Host + "_" + server.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

func (server *ServerMonitor) GetMasterBackupDirectory() string {
	cluster := server.ClusterGroup
	s3dir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name + "/" + cluster.master.Host + "_" + cluster.master.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

func (server *ServerMonitor) JobBackupLogical() error {
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server define")
	}
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Request logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return errors.New("Can't backup when server down")
	}
	server.DelBackupLogicalCookie()
	if server.IsMariaDB() && server.DBVersion.Major == 10 &&
		server.DBVersion.Minor >= 4 &&
		cluster.Conf.BackupLockDDL &&
		(cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMysqldump || cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper) {
		bckConn, err := server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()
		_, err = bckConn.Exec("BACKUP STAGE START")
		cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", LvlErr, "Failed SQL for server %s: %s ", server.URL, err)
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", LvlErr, "Failed SQL for server %s: %s ", server.URL, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Blocking DDL via BACKUP STAGE")
	}
	if cluster.Conf.BackupSaveScript != "" {
		scriptCmd := exec.Command(cluster.Conf.BackupSaveScript, server.Host, server.GetCluster().GetMaster().Host, server.Port, server.GetCluster().GetMaster().Port)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Command: %s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))
		stdoutIn, _ := scriptCmd.StdoutPipe()
		stderrIn, _ := scriptCmd.StderrPipe()
		scriptCmd.Start()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			server.copyLogs(stdoutIn)
		}()
		go func() {
			defer wg.Done()
			server.copyLogs(stderrIn)
		}()
		wg.Wait()
		if err := scriptCmd.Wait(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Backup script error: %s", err)
			return err
		} else {
			server.SetBackupLogicalCookie()
		}
		return nil
	}
	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeRiver {
		cfg := new(river.Config)
		cfg.MyHost = server.URL
		cfg.MyUser = server.User
		cfg.MyPassword = server.Pass
		cfg.MyFlavor = "mariadb"

		//	cfg.ESAddr = *es_addr
		cfg.StatAddr = "127.0.0.1:12800"
		cfg.DumpServerID = 1001

		cfg.DumpPath = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"
		cfg.DumpExec = cluster.GetMysqlDumpPath()
		cfg.DumpOnly = true
		cfg.DumpInit = true
		cfg.BatchMode = "CSV"
		cfg.BatchSize = 100000
		cfg.BatchTimeOut = 1
		cfg.DataDir = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"

		os.RemoveAll(cfg.DumpPath)

		//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
		cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

		river.NewRiver(cfg)
	}

	// Blocking DDL
	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMysqldump {
		file, err2 := cluster.CreateTmpClientConfFile()
		if err2 != nil {
			return err2
		}
		defer os.Remove(file)
		usegtid := server.JobGetDumpGtidParameter()
		events := ""
		dumpslave := ""
		//		if !server.HasMySQLGTID() {
		if server.IsMaster() {
			dumpslave = "--master-data=1"
		} else {
			dumpslave = "--dump-slave=1"
		}
		//	}
		if server.HasEventScheduler() {
			events = "--events=true"
		} else {
			events = "--events=false"
		}

		dumpargs := strings.Split(strings.ReplaceAll("--defaults-file="+file+" "+cluster.getDumpParameter()+" "+dumpslave+" "+usegtid+" "+events, "  ", " "), " ")
		dumpargs = append(dumpargs, "--apply-slave-statements", "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser() /*"--log-error="+server.GetMyBackupDirectory()+"dump_error.log"*/)
		dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), dumpargs...)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))
		f, err := os.Create(server.GetMyBackupDirectory() + "mysqldump.sql.gz")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error mysqldump backup request: %s", err)
			return err
		}
		wf := bufio.NewWriter(f)
		gw := gzip.NewWriter(wf)
		//fw := bufio.NewWriter(gw)
		dumpCmd.Stdout = gw
		stderrIn, _ := dumpCmd.StderrPipe()
		err = dumpCmd.Start()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error backup request: %s", err)
			return err
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			server.copyLogs(stderrIn)
		}()
		go func() {
			defer wg.Done()
			err := dumpCmd.Wait()

			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "mysqldump: %s", err)
			} else {
				server.SetBackupLogicalCookie()
			}
			gw.Flush()
			gw.Close()
			wf.Flush()
			f.Close()
		}()
		wg.Wait()

	}

	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeDumpling {

		conf := dumplingext.DefaultConfig()
		conf.Database = ""
		conf.Host = misc.Unbracket(server.Host)
		conf.User = cluster.GetDbUser()
		conf.Port, _ = strconv.Atoi(server.Port)
		conf.Password = cluster.GetDbPass()

		conf.Threads = cluster.Conf.BackupLogicalDumpThreads
		conf.FileSize = 1000
		conf.StatementSize = dumplingext.UnspecifiedSize
		conf.OutputDirPath = server.GetMyBackupDirectory()
		conf.Consistency = "flush"
		conf.NoViews = true
		conf.StatusAddr = ":8281"
		conf.Rows = dumplingext.UnspecifiedSize
		conf.Where = ""
		conf.EscapeBackslash = true
		conf.LogLevel = LvlInfo

		err := dumplingext.Dump(conf)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Dumpling %s", err)

	}

	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		//  --no-schemas     --regex '^(?!(mysql))'

		threads := strconv.Itoa(cluster.Conf.BackupLogicalDumpThreads)
		myargs := strings.Split(strings.ReplaceAll(cluster.Conf.BackupMyLoaderOptions, "  ", " "), " ")
		myargs = append(myargs, "--outputdir="+server.GetMyBackupDirectory(), "--threads="+threads, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser(), "--password="+cluster.GetDbPass())
		dumpCmd := exec.Command(cluster.GetMyDumperPath(), myargs...)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "%s", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", 1))
		stdoutIn, _ := dumpCmd.StdoutPipe()
		stderrIn, _ := dumpCmd.StderrPipe()
		dumpCmd.Start()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			server.copyLogs(stdoutIn)
		}()
		go func() {
			defer wg.Done()
			server.copyLogs(stderrIn)
		}()
		wg.Wait()
		if err := dumpCmd.Wait(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "MyDumper: %s", err)
		} else {
			server.SetBackupLogicalCookie()
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	server.BackupRestic()
	return nil
}

func (server *ServerMonitor) copyLogs(r io.Reader) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "%s", s.Text())
		}
	}
}

func (server *ServerMonitor) BackupRestic() error {
	cluster := server.ClusterGroup
	var stdout, stderr []byte
	var errStdout, errStderr error

	if cluster.Conf.BackupRestic {
		resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, "backup", server.GetMyBackupDirectory())

		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()

		//out, err := resticcmd.CombinedOutput()

		resticcmd.Env = cluster.ResticGetEnv()

		if err := resticcmd.Start(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
			return err
		}

		// cmd.Wait() should be called only after we finish reading
		// from stdoutIn and stderrIn.
		// wg ensures that we finish
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			stdout, errStdout = server.copyAndCapture(os.Stdout, stdoutIn)
			wg.Done()
		}()

		stderr, errStderr = server.copyAndCapture(os.Stderr, stderrIn)

		wg.Wait()

		err := resticcmd.Wait()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s\n", err)
		}
		if errStdout != nil || errStderr != nil {
			log.Fatal("failed to capture stdout or stderr\n")
		}
		outStr, errStr := string(stdout), string(stderr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "result:%s\n%s\n%s", resticcmd.Path, outStr, errStr)

	}
	return nil
}

func (server *ServerMonitor) copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			_, err := w.Write(d)
			if err != nil {
				return out, err
			}
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
	}

}

func (server *ServerMonitor) JobRunViaSSH() error {
	cluster := server.ClusterGroup
	if cluster.IsInFailover() {
		return errors.New("Cancel dbjob via ssh during failover")
	}
	client, err := server.GetCluster().OnPremiseConnect(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "OnPremise run  job  %s", err)
		return err
	}
	defer client.Close()

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	scriptpath := server.Datadir + "/init/init/dbjobs_new"

	if _, err := os.Stat(scriptpath); os.IsNotExist(err) && server.GetCluster().GetConf().OnPremiseSSHDbJobScript == "" && !server.IsConfigGen {
		server.GetDatabaseConfig()

	}

	if server.GetCluster().GetConf().OnPremiseSSHDbJobScript != "" {
		scriptpath = server.GetCluster().GetConf().OnPremiseSSHDbJobScript
	}

	filerc, err2 := os.Open(scriptpath)
	if err2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "JobRunViaSSH %s, scriptpath : %s", err2, scriptpath)
		return errors.New("Cancel dbjob can't open script")

	}
	defer filerc.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(filerc)

	buf2 := strings.NewReader(server.GetSshEnv())
	r := io.MultiReader(buf2, buf)

	if client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Database jobs run via SSH: %s", stderr.String())
	}
	out := stdout.String()

	if server.GetCluster().Conf.LogSST {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Job run via ssh script: %s ,out: %s ,err: %s", scriptpath, out, stderr.String())
	}

	res := new(JobResult)
	val := reflect.ValueOf(res).Elem()
	for i := 0; i < val.NumField(); i++ {
		if strings.Contains(strings.ToLower(string(out)), strings.ToLower("no "+val.Type().Field(i).Name)) {
			val.Field(i).SetBool(false)
		} else {
			val.Field(i).SetBool(true)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Database jobs run via SSH: %s", val.Type().Field(i).Name)
		}
	}

	cluster.JobResults[server.URL] = res
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Exec via ssh  : %s", res)
	// }
	return nil
}

func (server *ServerMonitor) JobBackupBinlog(binlogfile string) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Copy only master binlog")
	}
	if cluster.IsInFailover() {
		return errors.New("Cancel job copy binlog during failover")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}

	cmdrun := exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--raw", "--server-id=10000", "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+server.GetMyBackupDirectory(), binlogfile)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "%s", strings.Replace(cmdrun.String(), cluster.GetRplPass(), "XXXX", 1))

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		cluster.LogPrint(cmdrun.Stderr)
		cluster.LogPrint(cmdrun.Stdout)
		return cmdrunErr
	}

	// Get

	return nil
}

func (server *ServerMonitor) JobBackupBinlogPurge(binlogfile string) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Purge only master binlog")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}
	binlogfilestart, _ := strconv.Atoi(strings.Split(binlogfile, ".")[1])
	prefix := strings.Split(binlogfile, ".")[0]
	binlogfilestop := binlogfilestart - cluster.Conf.BackupBinlogsKeep
	keeping := make(map[string]int)
	for binlogfilestop < binlogfilestart {
		if binlogfilestop > 0 {
			filename := prefix + "." + fmt.Sprintf("%06d", binlogfilestop)
			if _, err := os.Stat(server.GetMyBackupDirectory() + "/" + filename); os.IsNotExist(err) {
				if _, ok := server.BinaryLogFiles[filename]; ok {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Backup master missing binlog of %s,%s", server.URL, filename)
					server.JobBackupBinlog(filename)
				}
			}
			keeping[filename] = binlogfilestop
		}
		binlogfilestop++
	}
	files, err := ioutil.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Failed to read backup directory of %s,%s", server.URL, err.Error())
	}

	for _, file := range files {
		_, ok := keeping[file.Name()]
		if strings.HasPrefix(file.Name(), prefix) && !ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Purging binlog file %s", file.Name())
			os.Remove(server.GetMyBackupDirectory() + "/" + file.Name())
		}
	}
	return nil
}

func (server *ServerMonitor) JobCapturePurge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	i := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "capture") {
			i++
			drop[file.Name()] = i
		}
	}
	for key, value := range drop {

		if value < len(drop)-keep {
			os.Remove(path + "/" + key)
		}

	}
	return nil
}

func (server *ServerMonitor) JobGetDumpGtidParameter() string {
	usegtid := ""
	// MySQL force GTID in server configuration the dump transparently include GTID pos. In MariaDB both positional or GTID is possible and so must be choose at dump
	// Issue #422
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "gniac2 %s: %s,", server.URL, server.GetVersion())
	if server.GetVersion().IsMariaDB() {
		if server.HasGTIDReplication() {
			usegtid = "--gtid=true"
		} else {
			usegtid = "--gtid=false"
		}
	}
	return usegtid
}

func (cluster *Cluster) CreateTmpClientConfFile() (string, error) {
	confOut, err := os.CreateTemp("", "client.cnf")
	if err != nil {
		return "", err
	}

	if _, err := confOut.Write([]byte("[client]\npassword=" + cluster.GetDbPass() + "\n")); err != nil {
		return "", err
	}
	if err := confOut.Close(); err != nil {
		return "", err
	}
	return confOut.Name(), nil

}

func (cluster *Cluster) JobRejoinMysqldumpFromSource(source *ServerMonitor, dest *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Rejoining from direct mysqldump from %s", source.URL)
	dest.StopSlave()
	usegtid := dest.JobGetDumpGtidParameter()

	events := ""
	if source.HasEventScheduler() {
		events = "--events=true"
	} else {
		events = "--events=false"
	}
	dumpslave := ""
	//	if !source.HasMySQLGTID() {
	if source.IsMaster() {
		dumpslave = "--master-data=1"
	} else {
		dumpslave = "--dump-slave=1"
	}
	//	}
	file, err := cluster.CreateTmpClientConfFile()
	if err != nil {
		return err
	}
	defer os.Remove(file)
	dumpstring := "--defaults-file=" + file + " " + source.ClusterGroup.getDumpParameter() + " " + dumpslave + " " + usegtid + " " + events

	dumpargs := strings.Split(strings.ReplaceAll(dumpstring, "  ", " "), " ")

	dumpargs = append(dumpargs, "--apply-slave-statements", "--host="+misc.Unbracket(source.Host), "--port="+source.Port, "--user="+source.ClusterGroup.GetDbUser() /*, "--log-error="+source.GetMyBackupDirectory()+"dump_error.log"*/)

	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), dumpargs...)
	stderrIn, _ := dumpCmd.StderrPipe()
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), `--defaults-file=`+file, `--host=`+misc.Unbracket(dest.Host), `--port=`+dest.Port, `--user=`+cluster.GetDbUser(), `--force`, `--batch` /*, `--init-command=reset master;set sql_log_bin=0;set global slow_query_log=0;set global general_log=0;`*/)
	stderrOut, _ := clientCmd.StderrPipe()

	//disableBinlogCmd := exec.Command("echo", "\"set sql_bin_log=0;\"")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))

	iodumpreader, err := dumpCmd.StdoutPipe()
	clientCmd.Stdin = io.MultiReader(bytes.NewBufferString("reset master;set sql_log_bin=0;"), iodumpreader)

	/*clientCmd.Stdin, err = dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlErr, "Failed opening pipe: %s", err)
		return err
	}*/
	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Failed mysqldump command: %s at %s", err, strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))
		return err
	}
	if err := clientCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Can't start mysql client:%s at %s", err, strings.Replace(clientCmd.String(), cluster.GetDbPass(), "XXXX", -1))
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		source.copyLogs(stderrIn)
	}()
	go func() {
		defer wg.Done()
		dest.copyLogs(stderrOut)
	}()

	wg.Wait()

	dumpCmd.Wait()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Start slave after dump on %s", dest.URL)
	dest.StartSlave()
	return nil
}
