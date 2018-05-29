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
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/httplog"
	river "github.com/signal18/replication-manager/river"
	"github.com/signal18/replication-manager/slowlog"
	"github.com/signal18/replication-manager/state"
)

func (server *ServerMonitor) JobInsertTaks(task string, port string, repmanhost string) (int64, error) {
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
	_, err = conn.Exec("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	if err != nil {
		return 0, err
	}
	_, err = conn.Exec("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task)) engine=innodb")
	if err != nil {
		if server.ClusterGroup.Conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf(LvlErr, "Can't create table replication_manager_schema.jobs")
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

	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"/"+server.Id+"_xtrabackup.xbtream", ConstJobCreateFile)
	if err != nil {
		return 0, nil
	}
	jobid, err := server.JobInsertTaks("xtrabackup", port, server.ClusterGroup.Conf.MonitorAddress)
	return jobid, err
}

func (server *ServerMonitor) JobReseedXtraBackup() (int64, error) {
	jobid, err := server.JobInsertTaks("reseedxtrabackup", "4444", server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	server.StopSlave()
	err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	})
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed physical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobFlashbackXtraBackup() (int64, error) {
	jobid, err := server.JobInsertTaks("flashbackxtrabackup", "4444", server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	server.StopSlave()
	err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	})
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed physical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobReseedMysqldump() (int64, error) {
	jobid, err := server.JobInsertTaks("reseedmysqldump", "4444", server.ClusterGroup.Conf.MonitorAddress)

	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	server.StopSlave()
	err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	})
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed logical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)

	return jobid, err
}

func (server *ServerMonitor) JobFlashbackMysqldump() (int64, error) {
	jobid, err := server.JobInsertTaks("flashbackmysqldump", "4444", server.ClusterGroup.Conf.MonitorAddress)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Receive reseed logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)

		return jobid, err
	}
	server.StopSlave()
	err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      server.ClusterGroup.master.Host,
		Port:      server.ClusterGroup.master.Port,
		User:      server.ClusterGroup.rplUser,
		Password:  server.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
	})
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL, err)
		return jobid, err
	}

	server.ClusterGroup.LogPrintf(LvlInfo, "Receive reseed logical backup %s request for server: %s", server.ClusterGroup.Conf.BackupPhysicalType, server.URL)
	return jobid, err
}

func (server *ServerMonitor) JobBackupErrorLog() (int64, error) {
	if server.IsDown() {
		return 0, nil
	}
	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"/"+server.Id+"_log_error.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTaks("error", port, server.ClusterGroup.Conf.MonitorAddress)
}

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) ErrorLogWatcher() {

	for line := range server.ErrorLogTailer.Lines {
		var log httplog.Message
		itext := strings.Index(line.Text, "]")
		if itext != -1 {
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
	log := slowlog.NewMessage()
	preline := ""
	var headerRe = regexp.MustCompile(`^#\s+[A-Z]`)
	for line := range server.SlowLogTailer.Lines {
		newlog := slowlog.NewMessage()
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
		} else {
			server.SlowLog.ParseLine(line.Text, log)
		}
		preline = line.Text
	}

}

func (server *ServerMonitor) JobBackupSlowQueryLog() (int64, error) {
	if server.IsDown() {
		return 0, nil
	}
	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"/"+server.Id+"_log_slow_query.log", ConstJobAppendFile)
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

func (server *ServerMonitor) JobsCheckRunning() error {
	if server.IsDown() {
		return nil
	}
	server.JobInsertTaks("", "", "")
	type DBTask struct {
		task string
		ct   int
	}
	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct FROM replication_manager_schema.jobs WHERE result IS NULL group by task ")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		return err
	}
	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct)
		if task.ct > 0 {
			if task.ct > 10 {
				server.ClusterGroup.sme.AddState("ERR00060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["ERR00060"], server.URL), ErrFrom: "JOB"})
			} else {
				if task.task == "optimized" {
					server.ClusterGroup.sme.AddState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB"})
				} else if task.task == "xtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0073"], server.URL), ErrFrom: "JOB"})
				} else if task.task == "reseedxtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0074"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmysqldump" {
					server.ClusterGroup.sme.AddState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0075"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackxtrabackup" {
					server.ClusterGroup.sme.AddState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0076"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmysqldump" {
					server.ClusterGroup.sme.AddState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(server.ClusterGroup.GetErrorList()["WARN0077"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
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

func (server *ServerMonitor) JobBackupLogical() error {
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server define")
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Request logical backup %s for: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return nil
	}

	if server.ClusterGroup.Conf.BackupLogicalType == "river" {
		cfg := new(river.Config)
		cfg.MyHost = server.URL
		cfg.MyUser = server.User
		cfg.MyPassword = server.Pass
		cfg.MyFlavor = "mariadb"

		//	cfg.ESAddr = *es_addr
		cfg.StatAddr = "127.0.0.1:12800"
		cfg.DumpServerID = 1001

		cfg.DumpPath = server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/river"
		cfg.DumpExec = server.ClusterGroup.Conf.ShareDir + "/" + server.ClusterGroup.Conf.GoArch + "/" + server.ClusterGroup.Conf.GoOS + "/mysqldump"
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
	if server.ClusterGroup.Conf.BackupLogicalType == "mysqldump" {
		//var outGzip, outFile bytes.Buffer
		//	var errStdout error
		usegtid := "--gtid"
		dumpCmd := exec.Command(server.ClusterGroup.Conf.ShareDir+"/"+server.ClusterGroup.Conf.GoArch+"/"+server.ClusterGroup.Conf.GoOS+"/mysqldump", "--opt", "--hex-blob", "--events", "--disable-keys", "--apply-slave-statements", usegtid, "--single-transaction", "--all-databases", "--host="+server.Host, "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)

		//if err != nil {
		//	log.Fatal(err)
		//}
		//stdout := io.MultiWriter(w, &outDump)

		f, err := os.Create(server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/" + server.Id + "_mysqldump.sql.gz")
		wf := bufio.NewWriter(f)
		gw := gzip.NewWriter(wf)
		//fw := bufio.NewWriter(gw)
		dumpCmd.Stdout = gw

		err = dumpCmd.Start()
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Error backup request: %s", err)
			return err
		}
		var wg sync.WaitGroup
		wg.Add(1)
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
	server.ClusterGroup.LogPrintf(LvlInfo, "Finish logical backup %s for: %s", server.ClusterGroup.Conf.BackupLogicalType, server.URL)

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
