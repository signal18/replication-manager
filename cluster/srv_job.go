// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
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
	"compress/gzip"
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

	sshcli "github.com/helloyi/go-sshclient"
	"github.com/jmoiron/sqlx"
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
	if server.IsDown() || server.ClusterGroup.IsInFailover() {
		return nil
	}
	server.ExecQueryNoBinLog("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	err := server.ExecQueryNoBinLog("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task)) engine=innodb")
	if err != nil {
		if server.ClusterGroup.Conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf(LvlErr, "Can't create table replication_manager_schema.jobs")
		}
	}
	return err
}

func (server *ServerMonitor) JobInsertTaks(task string, port string, repmanhost string) (int64, error) {
	if server.ClusterGroup.IsInFailover() {
		server.ClusterGroup.LogPrintf(LvlInfo, "Cancel job %s during failover", task)
		return 0, errors.New("In failover can't insert job")
	}
	server.JobsCreateTable()
	conn, err := sqlx.Connect("mysql", server.DSN)
	if err != nil {
		if server.ClusterGroup.Conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf(LvlErr, "Job can't connect")
		}
		return 0, err
	}
	defer conn.Close()
	_, err = conn.Exec("set sql_log_bin=0")
	if err != nil {
		if server.ClusterGroup.Conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf(LvlErr, "Job can't disable binlog for session")
		}
		return 0, err
	}

	if task != "" {
		res, err := conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('" + task + "'," + port + ",'" + repmanhost + "', NOW())")
		if err == nil {
			return res.LastInsertId()
		}
		server.ClusterGroup.LogPrintf(LvlErr, "Job can't insert job %s", err)
		return 0, err
	}
	return 0, nil
}

func (server *ServerMonitor) JobBackupPhysical() (int64, error) {
	//server can be nil as no dicovered master
	if server == nil {
		return 0, nil
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Receive physical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)
	if server.IsDown() {
		return 0, nil
	}
	// not  needed to stream internaly using S3 fuse
	/*
		if server.ClusterGroup.Conf.BackupRestic {
			port, err := server.ClusterGroup.SSTRunReceiverToRestic(server.DSN + ".xbtream")
			if err != nil {
				return 0, nil
			}
			jobid, err := server.JobInsertTaks(server.ClusterGroup.Conf.BackupPhysicalType, port, server.ClusterGroup.Conf.MonitorAddress)
			return jobid, err
		} else {
	*/
	port, err := server.ClusterGroup.SSTRunReceiverToFile(server.GetMyBackupDirectory()+server.ClusterGroup.Conf.BackupPhysicalType+".xbtream", ConstJobCreateFile)
	if err != nil {
		return 0, nil
	}
	jobid, err := server.JobInsertTaks(server.ClusterGroup.Conf.BackupPhysicalType, port, server.ClusterGroup.Conf.MonitorAddress)

	return jobid, err
	//	}
	//return 0, nil
}

func (server *ServerMonitor) JobReseedPhysicalBackup() (int64, error) {

	jobid, err := server.JobInsertTaks("reseed"+server.ClusterGroup.Conf.BackupPhysicalType, server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}
	logs, err := server.StopSlave()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	}, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

	if err != nil {
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed physical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobFlashbackPhysicalBackup() (int64, error) {

	jobid, err := server.JobInsertTaks("flashback"+server.ClusterGroup.Conf.BackupPhysicalType, server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}

	logs, err := server.StopSlave()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	}, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed physical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobReseedLogicalBackup() (int64, error) {
	jobid, err := server.JobInsertTaks("reseed"+server.ClusterGroup.Conf.BackupLogicalType, server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL, err)

		return jobid, err
	}
	logs, err := server.StopSlave()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	}, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {

		return jobid, err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed logical backup %s request for server: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)
	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go server.JobReseedMyLoader()
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerStop() (int64, error) {
	jobid, err := server.JobInsertTaks("stop", server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Stop server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerRestart() (int64, error) {
	jobid, err := server.JobInsertTaks("restart", server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Restart server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobFlashbackLogicalBackup() (int64, error) {
	jobid, err := server.JobInsertTaks("flashback"+server.ClusterGroup.Conf.BackupLogicalType, server.SSTPort, server.ClusterGroup.Conf.MonitorAddress)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	logs, err := server.StopSlave()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed stop slave on server: %s %s", server.URL, err)

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	}, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
	if err != nil {
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed logical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)
	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go server.JobReseedMyLoader()
	}
	return jobid, err
}

func (server *ServerMonitor) JobBackupErrorLog() (int64, error) {
	if server.IsDown() {
		return 0, nil
	}
	port, err := server.ClusterGroup.SSTRunReceiverToFile(server.Datadir+"/log/log_error.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTaks("error", port, server.ClusterGroup.Conf.MonitorAddress)
}

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) ErrorLogWatcher() {

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
		log.Group = server.ClusterGroup.GetClusterName()

		server.ErrorLog.Add(log)
	}

}

func (server *ServerMonitor) SlowLogWatcher() {
	log := s18log.NewSlowMessage()
	preline := ""
	var headerRe = regexp.MustCompile(`^#\s+[A-Z]`)
	for line := range server.SlowLogTailer.Lines {
		newlog := s18log.NewSlowMessage()
		if server.ClusterGroup.Conf.LogSST {
			server.ClusterGroup.LogPrintf(LvlInfo, "New line %s", line.Text)
		}
		log.Group = server.ClusterGroup.GetClusterName()
		if headerRe.MatchString(line.Text) && !headerRe.MatchString(preline) {
			// new querySelector
			if server.ClusterGroup.Conf.LogSST {
				server.ClusterGroup.LogPrintf(LvlInfo, "New query %s", log)
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
	if server.IsDown() {
		return 0, nil
	}
	port, err := server.ClusterGroup.SSTRunReceiverToFile(server.Datadir+"/log/log_slow_query.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTaks("slowquery", port, server.ClusterGroup.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobOptimize() (int64, error) {
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTaks("optimize", "0", server.ClusterGroup.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobZFSSnapBack() (int64, error) {
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTaks("zfssnapback", "0", server.ClusterGroup.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobReseedMyLoader() {

	threads := strconv.Itoa(server.ClusterGroup.Conf.BackupLogicalLoadThreads)
	dumpCmd := exec.Command(server.ClusterGroup.GetMyLoaderPath(), "--overwrite-tables", "--directory="+server.ClusterGroup.master.GetMasterBackupDirectory(), "--verbose=3", "--threads="+threads, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
	server.ClusterGroup.LogPrintf(LvlInfo, "Command: %s", strings.Replace(dumpCmd.String(), server.ClusterGroup.dbPass, "XXXX", 1))

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
		server.ClusterGroup.LogPrintf(LvlErr, "MyLoader: %s", err)
		return
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Finish logical restaure %s for: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)
	server.Refresh()
	if server.IsSlave {
		server.ClusterGroup.LogPrintf(LvlInfo, "Parsing mydumper metadata ")
		meta, err := server.JobMyLoaderParseMeta(server.ClusterGroup.master.GetMasterBackupDirectory())
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "MyLoader metadata parsing: %s", err)
		}
		if server.IsMariaDB() && server.HaveMariaDBGTID {
			server.ClusterGroup.LogPrintf(LvlInfo, "Starting slave with mydumper metadata")
			server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='" + meta.BinLogUuid + "'")
			server.StartSlave()
		}
	}

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
	if server.IsDown() || server.ClusterGroup.Conf.MonitorScheduler == false {
		return nil
	}
	server.JobInsertTaks("", "", "")
	type DBTask struct {
		task string
		ct   int
	}
	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct FROM replication_manager_schema.jobs WHERE done=0 AND result IS NULL group by task ")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct)
		if task.ct > 0 {
			if task.ct > 10 {
				server.ClusterGroup.sme.AddState("ERR00060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["ERR00060"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				purge := "DELETE from replication_manager_schema.jobs WHERE task='" + task.task + "' AND done=0 AND result IS NULL order by start asc limit  " + strconv.Itoa(task.ct-1)
				err := server.ExecQueryNoBinLog(purge)
				if err != nil {
					server.ClusterGroup.LogPrintf(LvlErr, "Scheduler error purging replication_manager_schema.jobs %s", err)
				}
			} else {
				if task.task == "optimized" {
					server.ClusterGroup.sme.AddState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "restart" {
					server.ClusterGroup.sme.AddState("WARN0096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0096"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "stop" {
					server.ClusterGroup.sme.AddState("WARN0097", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0097"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "xtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0073"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "mariabackup" {
					server.ClusterGroup.sme.AddState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0073"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedxtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0074"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmariabackup" {
					server.ClusterGroup.sme.AddState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0074"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmysqldump" {
					server.ClusterGroup.sme.AddState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0075"], server.ClusterGroup.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmydumper" {
					server.ClusterGroup.sme.AddState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0075"], server.ClusterGroup.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackxtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0076"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmariabackup" {
					server.ClusterGroup.sme.AddState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0076"], server.ClusterGroup.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmydumper" {
					server.ClusterGroup.sme.AddState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0077"], server.ClusterGroup.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmysqldump" {
					server.ClusterGroup.sme.AddState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0077"], server.ClusterGroup.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
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

	s3dir := server.ClusterGroup.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + server.ClusterGroup.Name + "/" + server.Host + "_" + server.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

func (server *ServerMonitor) GetMasterBackupDirectory() string {

	s3dir := server.ClusterGroup.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + server.ClusterGroup.Name + "/" + server.ClusterGroup.master.Host + "_" + server.ClusterGroup.master.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

func (server *ServerMonitor) JobBackupLogical() error {
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server define")
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Request logical backup %s for: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return nil
	}

	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeRiver {
		cfg := new(river.Config)
		cfg.MyHost = server.URL
		cfg.MyUser = server.User
		cfg.MyPassword = server.Pass
		cfg.MyFlavor = "mariadb"

		//	cfg.ESAddr = *es_addr
		cfg.StatAddr = "127.0.0.1:12800"
		cfg.DumpServerID = 1001

		cfg.DumpPath = server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/river"
		cfg.DumpExec = server.ClusterGroup.GetMysqlDumpPath()
		cfg.DumpOnly = true
		cfg.DumpInit = true
		cfg.BatchMode = "CSV"
		cfg.BatchSize = 100000
		cfg.BatchTimeOut = 1
		cfg.DataDir = server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/river"

		os.RemoveAll(cfg.DumpPath)

		//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
		cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

		river.NewRiver(cfg)
	}
	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMysqldump {
		usegtid := "--gtid"

		dumpCmd := exec.Command(server.ClusterGroup.GetMysqlDumpPath(), "--opt", "--hex-blob", "--events", "--disable-keys", "--apply-slave-statements", usegtid, "--single-transaction", "--all-databases", "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)

		f, err := os.Create(server.GetMyBackupDirectory() + "mysqldump.sql.gz")
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Error backup request: %s", err)
			return err
		}
		wf := bufio.NewWriter(f)
		gw := gzip.NewWriter(wf)
		//fw := bufio.NewWriter(gw)
		dumpCmd.Stdout = gw
		stderrIn, _ := dumpCmd.StderrPipe()
		err = dumpCmd.Start()
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Error backup request: %s", err)
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
				log.Println(err)
			}
			gw.Flush()
			gw.Close()
			wf.Flush()
			f.Close()
		}()
		wg.Wait()

	}

	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeDumpling {

		conf := dumplingext.DefaultConfig()
		conf.Database = ""
		conf.Host = misc.Unbracket(server.Host)
		conf.User = server.ClusterGroup.dbUser
		conf.Port, _ = strconv.Atoi(server.Port)
		conf.Password = server.ClusterGroup.dbPass

		conf.Threads = server.ClusterGroup.Conf.BackupLogicalDumpThreads
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
		server.ClusterGroup.LogPrintf(LvlErr, "Dumpling %s", err)

	}
	if server.ClusterGroup.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		//  --no-schemas     --regex '^(?!(mysql))'

		threads := strconv.Itoa(server.ClusterGroup.Conf.BackupLogicalDumpThreads)
		dumpCmd := exec.Command(server.ClusterGroup.GetMyDumperPath(), "--outputdir="+server.GetMyBackupDirectory(), "--chunk-filesize=1000", "--compress", "--less-locking", "--verbose=3", "--triggers", "--routines", "--events", "--trx-consistency-only", "--kill-long-queries", "--threads="+threads, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
		server.ClusterGroup.LogPrintf(LvlInfo, "%s", strings.Replace(dumpCmd.String(), server.ClusterGroup.dbPass, "XXXX", 1))
		/*	pr, pw := io.Pipe()
			defer pw.Close()

			// tell the command to write to our pipe

					dumpCmd.Stdout = pw
					dumpCmd.Stderr = pw

				buf := new(bytes.Buffer)
				go func() {
					defer pr.Close()
					// copy the data written to the PipeReader via the cmd to stdout
					if _, err := io.Copy(buf, pr); err != nil {
						server.ClusterGroup.LogPrintf(LvlErr, "MyDumper: %s", err)
					}
					server.ClusterGroup.LogPrintf(LvlInfo, "%s", buf.String())
				}()

		*/
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
			server.ClusterGroup.LogPrintf(LvlErr, "MyDumper: %s", err)
		}
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Finish logical backup %s for: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)
	server.BackupRestic()
	return nil
}

func (server *ServerMonitor) copyLogs(r io.Reader) {
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			server.ClusterGroup.LogPrintf(LvlInfo, "%s", s.Text())
		}

		/*n, err := r.Read(buf)
		if n > 0 {
			server.ClusterGroup.LogPrintf(LvlInfo, "%s", buf)
		}
		if err != nil {
			break
		}*/
	}
}

func (server *ServerMonitor) BackupRestic() error {

	var stdout, stderr []byte
	var errStdout, errStderr error

	if server.ClusterGroup.Conf.BackupRestic {
		resticcmd := exec.Command(server.ClusterGroup.Conf.BackupResticBinaryPath, "backup", server.GetMyBackupDirectory())

		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()

		//out, err := resticcmd.CombinedOutput()

		resticcmd.Env = server.ClusterGroup.ResticGetEnv()

		if err := resticcmd.Start(); err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
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
			server.ClusterGroup.LogPrintf(LvlErr, "%s\n", err)
		}
		if errStdout != nil || errStderr != nil {
			log.Fatal("failed to capture stdout or stderr\n")
		}
		outStr, errStr := string(stdout), string(stderr)
		server.ClusterGroup.LogPrintf(LvlInfo, "result:%s\n%s\n%s", resticcmd.Path, outStr, errStr)

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
	if server.ClusterGroup.IsInFailover() {
		return errors.New("Cancel dbjob via ssh during failover")
	}
	key := os.Getenv("HOME") + "/.ssh/id_rsa"
	client, err := sshcli.DialWithKey(misc.Unbracket(server.Host)+":22", "apple", key)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "JobRunViaSSH %s", err)
		return err
	}
	defer client.Close()
	out, err2 := client.ScriptFile(server.Datadir + "/init/init/dbjobs_new").SmartOutput()
	if err2 != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "JobRunViaSSH %s", err2)
		return err
	}
	//	server.ClusterGroup.LogPrintf(LvlInfo, "Exec via ssh  : %s", out)
	res := new(JobResult)
	val := reflect.ValueOf(res).Elem()
	for i := 0; i < val.NumField(); i++ {
		if strings.Contains(strings.ToLower(string(out)), strings.ToLower("no "+val.Type().Field(i).Name)) {
			val.Field(i).SetBool(false)
		} else {
			val.Field(i).SetBool(true)
		}
	}
	server.ClusterGroup.JobResults[server.URL] = res
	return nil
}

func (server *ServerMonitor) JobBackupBinlog(binlogfile string) error {
	if !server.IsMaster() {
		return errors.New("Copy only master binlog")
	}
	if server.ClusterGroup.IsInFailover() {
		return errors.New("Cancel job copy binlog during failover")
	}
	if !server.ClusterGroup.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}

	cmdrun := exec.Command(server.ClusterGroup.GetMysqlBinlogPath(), "--read-from-remote-server", "--raw", "--server-id=10000", "--user="+server.ClusterGroup.rplUser, "--password="+server.ClusterGroup.rplPass, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+server.GetMyBackupDirectory()+"/", binlogfile)
	server.ClusterGroup.LogPrintf(LvlInfo, "%s", strings.Replace(cmdrun.String(), server.ClusterGroup.dbPass, "XXXX", 1))

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		server.ClusterGroup.LogPrint(cmdrun.Stderr)
		server.ClusterGroup.LogPrint(cmdrun.Stdout)
		return cmdrunErr
	}

	// Get

	return nil
}

func (server *ServerMonitor) JobBackupBinlogPurge(binlogfile string) error {
	if !server.IsMaster() {
		return errors.New("Purge only master binlog")
	}
	binlogfilestart, _ := strconv.Atoi(strings.Split(binlogfile, ".")[1])
	prefix := strings.Split(binlogfile, ".")[0]
	binlogfilestop := binlogfilestart - server.ClusterGroup.Conf.BackupBinlogsKeep
	keeping := make(map[string]int)
	for binlogfilestop < binlogfilestart {
		if binlogfilestop > 0 {
			filename := prefix + "." + fmt.Sprintf("%06d", binlogfilestop)
			if _, err := os.Stat(server.GetMyBackupDirectory() + "/" + filename); os.IsNotExist(err) {
				server.ClusterGroup.LogPrintf(LvlInfo, "Backup master missing binlog of %s,%s", server.URL, filename)
				server.JobBackupBinlog(filename)
			}
			keeping[filename] = binlogfilestop
		}
		binlogfilestop++
	}
	files, err := ioutil.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Failed to read backup directory  of %s,%s", server.URL, err.Error())
	}

	for _, file := range files {
		_, ok := keeping[file.Name()]
		if strings.HasPrefix(file.Name(), prefix) && !ok {
			fmt.Println(LvlInfo, "Purging binlog file %s", file.Name())
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
