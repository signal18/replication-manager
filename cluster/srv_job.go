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
	"database/sql"
	"encoding/json"
	"slices"

	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	gzip "github.com/klauspost/pgzip"
	dumplingext "github.com/pingcap/dumpling/v4/export"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	river "github.com/signal18/replication-manager/utils/river"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

type DBTask struct {
	task string
	ct   int
	id   int64
}

/*
  - 0-2	Indicates Job still not done yet
  - 3		Indicate it's finished recently and check if there is post-job task
  - 4-6	Job completed
    either success or failed
*/
var (
	JobStateAvailable  int = 0
	JobStateRunning    int = 1
	JobStateHalted     int = 2
	JobStateFinished   int = 3
	JobStateSuccess    int = 4
	JobStateErrorExec  int = 5
	JobStateErrorAfter int = 6
)

func (server *ServerMonitor) JobRun() {

}

func (server *ServerMonitor) JobsCreateTable() error {
	cluster := server.ClusterGroup
	if server.IsDown() || cluster.IsInFailover() {
		return nil
	}

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	server.ExecQueryNoBinLog("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	err := server.ExecQueryNoBinLog("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, state tinyint not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task), KEY idx3 (task, state), UNIQUE(task)) engine=innodb")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't create table replication_manager_schema.jobs")
		return err
	}

	var exist int
	server.Conn.Get(&exist, "SELECT COUNT(CASE WHEN COLUMN_KEY = 'UNI' THEN 1 END) AS num_task_unique FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' GROUP BY table_name")

	if exist == 0 {
		server.ExecQueryNoBinLog("DROP TABLE IF EXISTS replication_manager_schema.jobs")
		err := server.ExecQueryNoBinLog("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, state tinyint not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done) ,KEY idx2(result(1),task), KEY idx3 (task, state), UNIQUE(task)) engine=innodb")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't create table replication_manager_schema.jobs_tmp")
			return err
		}
	}

	server.Conn.Get(&exist, "SELECT COUNT(*) col_exists FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'replication_manager_schema' AND TABLE_NAME = 'jobs' AND COLUMN_NAME = 'state'")
	if exist == 0 {
		//Add column instead of changing create table for compatibility
		err = server.ExecQueryNoBinLog("ALTER TABLE replication_manager_schema.jobs ADD COLUMN state tinyint not null default 0 AFTER `done`")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Can't add column on table replication_manager_schema.jobs")
			return err
		}

		//Add index
		err = server.ExecQueryNoBinLog("ALTER TABLE replication_manager_schema.jobs ADD INDEX idx3 (task, state)")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Can't add column on table replication_manager_schema.jobs")
			return err
		}
	}

	return nil
}

func (server *ServerMonitor) JobsUpdateEntries() error {
	cluster := server.ClusterGroup
	if server.IsLoadingJobList {
		return errors.New("Waiting for previous update")
	}

	server.SetLoadingJobList(true)
	defer server.SetLoadingJobList(false)

	if server.IsDown() {
		return errors.New("Node is down")
	}

	query := "SELECT id, task, port, server, done, state, result, floor(UNIX_TIMESTAMP(start)) start, floor(UNIX_TIMESTAMP(end)) end FROM replication_manager_schema.jobs"

	rows, err := server.Conn.Queryx(query)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't retrieve jobs data from server %s", server.URL)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var t config.Task
		var res sql.NullString
		var end sql.NullInt64
		err := rows.Scan(&t.Id, &t.Task, &t.Port, &t.Server, &t.Done, &t.State, &res, &t.Start, &end)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error retrieving job data from %s: %s", server.URL, err.Error())
		}
		t.Result = res.String
		t.End = end.Int64
		if v, exists := server.JobResults.LoadOrStore(t.Task, &t); exists {
			v.Set(t)
		}
	}

	server.SetNeedRefreshJobs(false)

	return nil
}

func (server *ServerMonitor) JobInsertTask(task string, port string, repmanhost string) (int64, error) {
	cluster := server.ClusterGroup
	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Cancel job %s during failover", task)
		return 0, errors.New("In failover can't insert job")
	}

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return 0, nil
	}

	conn, err := server.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't connect")
		return 0, err
	}
	defer conn.Close()

	// Better to create after connection established
	server.JobsCreateTable()

	if task == "" {
		err = errors.New("Job can't insert empty task")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't insert empty task")
		return 0, err
	}

	rows, err := conn.Queryx("SELECT id, task, done, state FROM replication_manager_schema.jobs WHERE id = (SELECT max(id) FROM replication_manager_schema.jobs WHERE task = '" + task + "')")
	if err != nil && err != sql.ErrNoRows {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching replication_manager_schema.jobs: %s", err)
		server.JobsCreateTable()
		return 0, err
	}
	defer rows.Close()

	t, _ := server.JobResults.LoadOrStore(task, &config.Task{Task: task, Start: time.Now().Unix()})
	nr := 0
	for rows.Next() {
		nr = 1
		rows.Scan(&t.Id, &t.Task, &t.Done, &t.State)

		if t.State <= 3 && t.Done == 0 {
			err = errors.New("Previous job with same type is still running. Exiting")
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error: %s", err.Error())
			rows.Close()
			return 0, err
		}
	}
	rows.Close()

	_, err = conn.Exec("set sql_log_bin=0")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't disable binlog for session")
		return 0, err
	}

	var res sql.Result
	if nr > 0 {
		res, err = conn.Exec(fmt.Sprintf("DELETE FROM replication_manager_schema.jobs WHERE ID = %d", t.Id))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't delete job: %s", err)
			return 0, err
		}
	}

	//Reuse the same id
	res, err = conn.Exec(fmt.Sprintf("INSERT INTO replication_manager_schema.jobs(id, task, port,server,start) VALUES(%d,'%s',%s,'%s', NOW())", t.Id, task, port, repmanhost))
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't insert job %s", err)
		return 0, err
	}

	server.SetNeedRefreshJobs(true)
	return res.LastInsertId()
}

func (server *ServerMonitor) JobBackupPhysical() (int64, error) {
	//server can be nil as no dicovered master
	if server == nil {
		return 0, nil
	}
	cluster := server.ClusterGroup

	if cluster.IsInBackup() && cluster.Conf.BackupRestic {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Physical", cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupPhysical()
	}

	// Prevent backing up with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && cluster.Conf.BackupPhysicalType == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master %s MariaDB version is greater than 10.1. Changing from xtrabackup to mariabackup as physical backup tools", server.URL)
		cluster.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	}

	cluster.SetInPhysicalBackupState(true)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)
	if server.IsDown() {
		return 0, nil
	}

	var port string
	var err error
	var backupext string = ".xbtream"
	var dest string = server.GetMyBackupDirectory() + cluster.Conf.BackupPhysicalType
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToGZip(server, dest, ConstJobCreateFile)
		if err != nil {
			return 0, nil
		}
	} else {
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToFile(server, dest, ConstJobCreateFile)
		if err != nil {
			return 0, nil
		}
	}

	now := time.Now()
	// Reset last backup meta
	server.LastBackupMeta.Physical = &config.BackupMetadata{
		Id:             now.Unix(),
		StartTime:      now,
		BackupMethod:   config.BackupMethodPhysical,
		BackupStrategy: config.BackupStrategyFull,
		BackupTool:     cluster.Conf.BackupPhysicalType,
		Source:         server.URL,
		Dest:           dest,
		Compressed:     cluster.Conf.CompressBackups,
	}

	cluster.BackupMetaMap.Set(server.LastBackupMeta.Physical.Id, server.LastBackupMeta.Physical)

	jobid, err := server.JobInsertTask(cluster.Conf.BackupPhysicalType, port, cluster.Conf.MonitorAddress)

	return jobid, err
}

func (server *ServerMonitor) JobReseedPhysicalBackup() error {
	cluster := server.ClusterGroup

	// Prevent backing up with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && cluster.Conf.BackupPhysicalType == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master %s MariaDB version is greater than 10.1. Changing from xtrabackup to mariabackup as physical backup tools", server.URL)
		cluster.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	}

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	useMaster := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := master.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useMaster = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	if server.IsFlashingBack {
		err := errors.New("Server is in flashback state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	if server.IsReseeding {
		err := errors.New("Server is in reseeding state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	server.SetInReseedBackup(true)

	_, err := server.JobInsertTask("reseed"+cluster.Conf.BackupPhysicalType, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
		Channel:   cluster.Conf.MasterConn,
	}, server.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return nil
}

func (server *ServerMonitor) JobFlashbackPhysicalBackup() error {
	cluster := server.ClusterGroup

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	useSelfBackup := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	if server.IsFlashingBack {
		return errors.New("Server is in flashback state")
	}

	if server.IsReseeding {
		return errors.New("Server is in reseeding state")
	}

	server.SetInFlashbackBackup(true)

	_, err := server.JobInsertTask("flashback"+cluster.Conf.BackupPhysicalType, server.SSTPort, cluster.Conf.MonitorAddress)

	if err != nil {
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

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
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		server.SetInReseedBackup(false)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return nil
}

func (server *ServerMonitor) JobReseedLogicalBackup() error {
	cluster := server.ClusterGroup
	task := "reseed" + cluster.Conf.BackupLogicalType

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed logical backup")
	}

	useMaster := true
	var dest string
	switch cluster.Conf.BackupLogicalType {
	case config.ConstBackupLogicalTypeMysqldump:
		dest = "mysqldump.sql.gz"
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}

	// Can't handle script validation, unknown logic
	if cluster.Conf.BackupLogicalType != "script" {
		backupfile := master.GetMyBackupDirectory() + dest

		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupLogicalType) {
			if _, err := os.Stat(bckserver.GetMyBackupDirectory() + dest); err == nil {
				backupfile = bckserver.GetMyBackupDirectory() + dest
				useMaster = false
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(cluster.Conf.BackupLogicalType)
			}
		}

		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				//Remove false cookie
				master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", cluster.Conf.BackupLogicalType)
			}
		}
	}

	if server.IsFlashingBack {
		err := errors.New("Server is in flashback state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	if server.IsReseeding {
		err := errors.New("Server is in reseeding state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	server.SetInReseedBackup(true)

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	_, err := server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed logical backup %s request for server: %s %s", cluster.Conf.BackupLogicalType, server.URL, err)
		server.SetInReseedBackup(false)
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
		Channel:   cluster.Conf.MasterConn,
	}, server.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		server.SetInReseedBackup(false)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", cluster.Conf.BackupLogicalType, server.URL)
	if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMysqldump {
		go func() {
			useMaster := true
			file := "mysqldump.sql.gz"
			backupfile := cluster.master.GetMyBackupDirectory() + file

			bckserver := cluster.GetBackupServer()
			if bckserver != nil && bckserver.HasBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump) {
				if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
					backupfile = bckserver.GetMasterBackupDirectory() + file
					useMaster = false
				} else {
					//Remove false cookie
					bckserver.DelBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump)
				}
			}

			if useMaster {
				if _, err := os.Stat(backupfile); err != nil {
					//Remove false cookie
					cluster.master.DelBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump)
				}
			}

			err := server.JobReseedMysqldump(backupfile)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", cluster.Conf.BackupLogicalType, server.URL, err.Error())
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}()
	} else if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go func() {
			useMaster := true
			dir := "mydumper"
			backupdir := cluster.master.GetMyBackupDirectory() + dir

			bckserver := cluster.GetBackupServer()
			if bckserver != nil && bckserver.HasBackupTypeCookie(config.ConstBackupLogicalTypeMydumper) {
				if _, err := os.Stat(bckserver.GetMyBackupDirectory() + dir); err == nil {
					backupdir = bckserver.GetMasterBackupDirectory() + dir
					useMaster = false
				} else {
					//Remove false cookie
					bckserver.DelBackupTypeCookie(config.ConstBackupLogicalTypeMydumper)
				}
			}

			if useMaster {
				if _, err := os.Stat(backupdir); err != nil {
					//Remove false cookie
					cluster.master.DelBackupTypeCookie(config.ConstBackupLogicalTypeMydumper)
				}
			}

			err = server.JobReseedMyLoader(backupdir)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", cluster.Conf.BackupLogicalType, server.URL, err.Error())
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}()
	}
	return err
}

func (server *ServerMonitor) JobServerStop() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTask("stop", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Stop server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobServerRestart() (int64, error) {
	cluster := server.ClusterGroup
	jobid, err := server.JobInsertTask("restart", server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restart server: %s %s", server.URL, err)
		return jobid, err
	}
	return jobid, err
}

func (server *ServerMonitor) JobFlashbackLogicalBackup() error {
	cluster := server.ClusterGroup
	task := "flashback" + cluster.Conf.BackupLogicalType

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed logical backup")
	}

	useMaster := true
	var dest string
	switch cluster.Conf.BackupLogicalType {
	case config.ConstBackupLogicalTypeMysqldump:
		dest = "mysqldump.sql.gz"
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}

	// Can't handle script validation, unknown logic
	if cluster.Conf.BackupLogicalType != "script" {
		backupfile := master.GetMyBackupDirectory() + dest

		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupLogicalType) {
			if _, err := os.Stat(bckserver.GetMyBackupDirectory() + dest); err == nil {
				backupfile = bckserver.GetMyBackupDirectory() + dest
				useMaster = false
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(cluster.Conf.BackupLogicalType)
			}
		}

		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				//Remove false cookie
				master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", cluster.Conf.BackupLogicalType)
			}
		}
	}

	if server.IsFlashingBack {
		err := errors.New("Server is in flashback state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	if server.IsReseeding {
		err := errors.New("Server is in reseeding state")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, err.Error())
		return err
	}

	server.SetInFlashbackBackup(true)

	_, err := server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      cluster.master.Host,
		Port:      cluster.master.Port,
		User:      cluster.GetRplUser(),
		Password:  cluster.GetRplPass(),
		Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
		SSL:       cluster.Conf.ReplicationSSL,
		Channel:   cluster.Conf.MasterConn,
	}, server.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "flashback can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupLogicalType, server.URL, err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback logical backup %s request for server: %s", cluster.Conf.BackupLogicalType, server.URL)
	if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		go server.JobReseedBackupScript()
	} else if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMysqldump {
		go func() {
			useSelfBackup := true
			file := "mysqldump.sql.gz"
			backupfile := server.GetMyBackupDirectory() + file

			bckserver := cluster.GetBackupServer()
			if bckserver != nil && bckserver.HasBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump) {
				if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
					backupfile = bckserver.GetMasterBackupDirectory() + file
					useSelfBackup = false
				} else {
					//Remove false cookie
					bckserver.DelBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump)
				}
			}

			if useSelfBackup {
				if _, err := os.Stat(backupfile); err != nil {
					//Remove false cookie
					server.DelBackupTypeCookie(config.ConstBackupLogicalTypeMysqldump)
				}
			}
			err := server.JobReseedMysqldump(backupfile)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", cluster.Conf.BackupLogicalType, server.URL, err.Error())
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}()
	} else if cluster.Conf.BackupLogicalType == config.ConstBackupLogicalTypeMydumper {
		go func() {
			useSelfBackup := true
			dir := "mydumper"
			backupdir := server.GetMyBackupDirectory() + dir

			bckserver := cluster.GetBackupServer()
			if bckserver != nil && bckserver.HasBackupTypeCookie(config.ConstBackupLogicalTypeMydumper) {
				if _, err := os.Stat(bckserver.GetMyBackupDirectory() + dir); err == nil {
					backupdir = bckserver.GetMasterBackupDirectory() + dir
					useSelfBackup = false
				} else {
					//Remove false cookie
					bckserver.DelBackupTypeCookie(config.ConstBackupLogicalTypeMydumper)
				}
			}

			if useSelfBackup {
				if _, err := os.Stat(backupdir); err != nil {
					//Remove false cookie
					server.DelBackupTypeCookie(config.ConstBackupLogicalTypeMydumper)
				}
			}
			err := server.JobReseedMyLoader(backupdir)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", cluster.Conf.BackupLogicalType, server.URL, err.Error())
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}()
	}

	return nil
}

func (server *ServerMonitor) JobBackupErrorLog() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	port, err := cluster.SSTRunReceiverToFile(server, server.Datadir+"/log/log_error.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask("error", port, cluster.Conf.MonitorAddress)
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "New line %s", line.Text)
		}
		log.Group = cluster.GetClusterName()
		if headerRe.MatchString(line.Text) && !headerRe.MatchString(preline) {
			// new querySelector
			if cluster.Conf.LogSST {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "New query %s", log)
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
	port, err := cluster.SSTRunReceiverToFile(server, server.Datadir+"/log/log_slow_query.log", ConstJobAppendFile)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask("slowquery", port, cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobOptimize() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTask("optimize", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobZFSSnapBack() (int64, error) {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return 0, nil
	}
	return server.JobInsertTask("zfssnapback", "0", cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobReseedMyLoader(backupdir string) error {
	cluster := server.ClusterGroup
	threads := strconv.Itoa(cluster.Conf.BackupLogicalLoadThreads)

	defer server.SetInReseedBackup(false)

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master. Cancel backup reseeding %s", server.URL)
	}

	myargs := strings.Split(strings.ReplaceAll(cluster.Conf.BackupMyLoaderOptions, "  ", " "), " ")
	if server.URL == cluster.GetMaster().URL {
		myargs = append(myargs, "--enable-binlog")
	}

	myargs = append(myargs, "--directory="+backupdir, "--threads="+threads, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser(), "--password="+cluster.GetDbPass())
	dumpCmd := exec.Command(cluster.GetMyLoaderPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.ReplaceAll(dumpCmd.String(), cluster.GetDbPass(), "XXXX"))

	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := dumpCmd.Wait(); err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restaure %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	server.Refresh()
	if server.IsSlave {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
		meta, err := server.JobMyLoaderParseMeta(backupdir)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err)
		}
		if server.IsMariaDB() && server.HaveMariaDBGTID {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
			server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='" + meta.BinLogUuid + "'")
			server.StartSlave()
		}
	}
	return nil
}

func (server *ServerMonitor) JobReseedMysqldump(backupfile string) error {
	cluster := server.ClusterGroup
	var err error
	defer server.SetInReseedBackup(false)

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master. Cancel backup reseeding %s", server.URL)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Sending logical backup to reseed %s", server.URL)

	server.StopSlave()

	file, err := cluster.CreateTmpClientConfFile()
	if err != nil {
		return fmt.Errorf("[%s] Failed creating tmp connection file:  %s ", server.URL, err)
	}
	defer os.Remove(file)

	gzfile, err := os.Open(backupfile)
	if err != nil {
		return fmt.Errorf("[%s] Failed opening backup file in backup server for reseed:  %s ", server.URL, err)
	}

	fz, err := gzip.NewReader(gzfile)
	if err != nil {
		return fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}
	defer fz.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, fz)
	if err != nil {
		return fmt.Errorf("[%s] Error happened when unzipping backup file in backup server for reseed:  %s ", server.URL, err)
	}

	clientCmd := exec.Command(cluster.GetMysqlclientPath(), `--defaults-file=`+file, `--host=`+misc.Unbracket(server.Host), `--port=`+server.Port, `--user=`+cluster.GetDbUser(), `--force`, `--batch` /*, `--init-command=reset master;set sql_log_bin=0;set global slow_query_log=0;set global general_log=0;`*/)
	clientCmd.Stdin = io.MultiReader(bytes.NewBufferString("reset master;set sql_log_bin=0;"), &buf)

	stderr, _ := clientCmd.StderrPipe()

	if err := clientCmd.Start(); err != nil {
		return fmt.Errorf("Can't start mysql client:%s at %s", err, strings.ReplaceAll(clientCmd.String(), cluster.GetDbPass(), "XXXX"))
	}

	go func() {
		server.copyLogs(stderr, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	err = clientCmd.Wait()
	if err != nil {
		return fmt.Errorf("Error waiting reseed %s at %s", server.URL, err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
	server.StartSlave()

	return nil
}

func (server *ServerMonitor) JobReseedBackupScript() {
	cluster := server.ClusterGroup
	defer server.SetInReseedBackup(false)

	cmd := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(cluster.master.Host))

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command backup load script: %s", strings.Replace(cmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	cmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "My reload script: %s", err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restaure from load script on %s ", server.URL)

}

func (server *ServerMonitor) JobsCheckRunning() error {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return nil
	}
	//server.JobInsertTask("", "", "")
	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs WHERE state=0 group by task ")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct, &task.id)
		if task.ct > 0 {
			if task.ct > 10 {
				cluster.SetState("ERR00060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["ERR00060"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				purge := "DELETE from replication_manager_schema.jobs WHERE task='" + task.task + "' AND done=0 AND result IS NULL order by start asc limit  " + strconv.Itoa(task.ct-1)
				err := server.ExecQueryNoBinLog(purge)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error purging replication_manager_schema.jobs %s", err)
				}
			} else {
				if task.task == "optimized" {
					cluster.SetState("WARN0072", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0072"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "restart" {
					cluster.SetState("WARN0096", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0096"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "stop" {
					cluster.SetState("WARN0097", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0097"], server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "xtrabackup" {
					cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "mariabackup" {
					cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedxtrabackup" {
					cluster.SetState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmariabackup" {
					cluster.SetState("WARN0074", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0074"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmysqldump" {
					cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "reseedmydumper" {
					cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackxtrabackup" {
					cluster.SetState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmariabackup" {
					cluster.SetState("WARN0076", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0076"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmydumper" {
					cluster.SetState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				} else if task.task == "flashbackmysqldump" {
					cluster.SetState("WARN0077", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0077"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
				}
			}
		}

	}

	return nil
}

func (server *ServerMonitor) JobsCheckPending() error {
	cluster := server.ClusterGroup
	if server.IsDown() {
		return nil
	}

	//Only cancel if not reseeding status
	if server.IsReseeding {
		return nil
	}

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	server.ExecQueryNoBinLog("UPDATE replication_manager_schema.jobs SET state=5, result='Timeout waiting for job to start' where state=0 and start <= DATE_SUB(NOW(), interval 1 hour)")

	//server.JobInsertTask("", "", "")
	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs WHERE state=2 group by task ")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct, &task.id)
		if task.ct > 0 {
			if strings.HasPrefix(task.task, "reseed") || strings.HasPrefix(task.task, "flashback") {
				res := "Replication-manager is down while preparing task, cancelling operation for data safety."
				query := "UPDATE replication_manager_schema.jobs SET state=5, result='%s' where task = '%s'"
				server.ExecQueryNoBinLog(fmt.Sprintf(query, res, task.task))
				server.SetNeedRefreshJobs(true)
			}
		}
	}

	return nil
}

func (server *ServerMonitor) JobsCheckErrors() error {
	var err error

	cluster := server.ClusterGroup
	if server.IsDown() {
		return nil
	}

	rows, err := server.Conn.Queryx("SELECT task, result FROM replication_manager_schema.jobs WHERE done=0 AND state=5")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching finished replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()

	ct := 0
	p := make([]string, 0)
	for rows.Next() {
		ct++
		var task, result sql.NullString
		rows.Scan(&task, &result)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job %s ended with ERROR: %s", task.String, result.String)
		p = append(p, "'"+task.String+"'")
		switch task.String {
		case "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariabackup":
			defer server.SetInReseedBackup(false)
		}
	}

	if ct > 0 {
		query := "UPDATE replication_manager_schema.jobs SET done=1 WHERE done=0 AND state=5 and task in (%s)"
		server.ExecQueryNoBinLog(fmt.Sprintf(query, strings.Join(p, ",")))
		server.SetNeedRefreshJobs(true)
	}

	return err
}

func (server *ServerMonitor) JobsCancelTasks(force bool, tasks ...string) error {
	var err error
	var canCancel bool = true
	cluster := server.ClusterGroup

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	//Check for already running task
	server.JobResults.Range(func(k, v any) bool {
		key := k.(string)
		val := v.(*config.Task)
		if slices.Contains(tasks, key) {
			if val.State > 0 {
				canCancel = false
			}
		}
		return true
	})

	if !canCancel || force {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to cancel tasks. No rows found or tasks already started", server.URL)
	}

	if server.IsDown() {
		if canCancel || force {
			server.SetInReseedBackup(false)
			server.SetNeedRefreshJobs(true)
		}
		return nil
	}

	conn, err := server.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't connect")
		return err
	}
	defer conn.Close()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Cancelling tasks on %s as requested", server.URL)
	//Using lock to prevent wrong reads
	_, err = conn.Exec("set sql_log_bin=0;")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't disable binlog for session")
		return err
	}

	_, err = conn.Exec("LOCK TABLES replication_manager_schema.jobs WRITE;")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't lock table jobs for cancel reseed")
		return err
	}

	defer conn.Exec("UNLOCK TABLES;")
	query := "UPDATE replication_manager_schema.jobs SET done=1, state=5, result='cancelled by user' WHERE done=0 AND state=0 and task in (?);"

	if force {
		query = "UPDATE replication_manager_schema.jobs SET done=1, state=5, result='cancelled by user' WHERE task in (?);"
	}

	query, args, err := sqlx.In(query, tasks)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't process tasks args")
	}

	// Rebind the query to match the database's bind type
	query = conn.Rebind(query)

	var res sql.Result
	res, err = conn.Exec(query, args...)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error can't cancel job: %s", err)
		return err
	}

	aff, err := res.RowsAffected()
	if err == nil {
		if aff > 0 {
			server.SetInReseedBackup(false)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed cancelled successfully on %s. Please start slave manually after checking data integrity.", server.URL)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to cancel reseed on %s. No rows found or reseed already started", server.URL)
		}
	}

	server.SetNeedRefreshJobs(true)

	return err
}

func (server *ServerMonitor) JobsCheckFinished() error {
	var err error

	cluster := server.ClusterGroup
	if server.IsDown() {
		return nil
	}

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	rows, err := server.Conn.Queryx("SELECT task ,count(*) as ct, max(id) as id FROM replication_manager_schema.jobs WHERE done=1 AND state=3")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching finished replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var task DBTask
		rows.Scan(&task.task, &task.ct, &task.id)
		if task.ct > 0 {
			if err := server.AfterJobProcess(task); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching finished replication_manager_schema.jobs %s", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finished %s successfully", task.task)
			}
			server.SetNeedRefreshJobs(true)
		}
	}
	return err
}

func (server *ServerMonitor) AfterJobProcess(task DBTask) error {
	//Still use done=1 and state=3 to prevent unwanted changes
	query := "UPDATE replication_manager_schema.jobs SET result=CONCAT(result,'%s'), state=%d WHERE id=%d AND done=1 AND state=3"
	errStr := ""
	if task.task == "" {
		return errors.New("Cannot check task. Task name is empty!")
	}

	switch task.task {
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		server.SetBackupPhysicalCookie(task.task)
		server.LastBackupMeta.Physical.Completed = true
		errStr = "Backup completed"
	case "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariabackup":
		defer server.SetInReseedBackup(false)
		if _, err := server.StartSlave(); err != nil {
			errStr = err.Error()
			// Only set as failed if no error connection
			if server.Conn != nil {
				// Set state as 6 to differ post-job error with in-job error (code: 5)
				server.ExecQueryNoBinLog(fmt.Sprintf(query, "\n"+errStr, JobStateErrorAfter, task.id))
			}
			return err
		}
	}
	server.ExecQueryNoBinLog(fmt.Sprintf(query, errStr, JobStateSuccess, task.id))
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Create backup path failed: %s", s3dir, err)
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

func (server *ServerMonitor) JobBackupScript() error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	scriptCmd := exec.Command(cluster.Conf.BackupSaveScript, server.Host, server.GetCluster().GetMaster().Host, server.Port, server.GetCluster().GetMaster().Port)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))
	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	if err = scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup script error: %s", err)
		return err
	}
	return err
}

func (server *ServerMonitor) JobBackupMysqldump(filename string) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB

	defer cluster.SetInLogicalBackupState(false)

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	binlogRegex := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)

	var bfile, bgtid string
	var bpos uint64

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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))
	// Get the stdout pipe from the command
	stdout, err := dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting stdout pipe:", err)
		fmt.Println()
		return err
	}

	// dumpCmd.Stdout = gw
	stderrIn, _ := dumpCmd.StderrPipe()

	f, err := os.Create(filename)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mysqldump backup request: %s", err.Error())
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer func() {
		if err := gw.Flush(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flushing gzip: %s", err.Error())
		}
		if err := gw.Close(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error closing gzip: %s", err.Error())
		}
	}()

	teeReader := io.TeeReader(stdout, gw)

	err = dumpCmd.Start()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		return err
	}

	// Create a buffered reader to read the duplicated stream
	reader := bufio.NewReader(teeReader)
	buffer := make([]byte, cluster.Conf.SSTSendBuffer) // 64 KB buffer

	errCh := make(chan error, 2) // Create a channel to send errors
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()

		var remainingLine string

		for {
			n, err := reader.Read(buffer)
			if err != nil && err != io.EOF {
				errCh <- fmt.Errorf("Error reading buffer: %w", err) // Send the error through the channel with more context
			}
			if n == 0 {
				break
			}

			// Process buffer content
			content := remainingLine + string(buffer[:n])
			lines := strings.Split(content, "\n")
			remainingLine = lines[len(lines)-1] // Last element is the remaining part

			for _, line := range lines[:len(lines)-1] {
				if server.LastBackupMeta.Logical.BinLogFileName == "" {
					if matches := binlogRegex.FindStringSubmatch(line); matches != nil {
						bfile = matches[1]
						bpos, _ = strconv.ParseUint(matches[2], 10, 64)
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binlog filename:%s, pos: %s", bfile, strconv.FormatUint(bpos, 10))
					}
				}

				if server.LastBackupMeta.Logical.BinLogGtid == "" && server.IsMariaDB() {
					if matches := gtidRegex.FindStringSubmatch(line); matches != nil {
						bgtid = matches[1]
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "GTID:%s", bgtid)
					}
				}
			}
		}

		// Process any remaining line data after the loop
		if remainingLine != "" {
			if server.LastBackupMeta.Logical.BinLogFileName == "" {
				if matches := binlogRegex.FindStringSubmatch(remainingLine); matches != nil {
					bfile = matches[1]
					bpos, _ = strconv.ParseUint(matches[2], 10, 64)
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binlog filename:%s, pos: %s", bfile, strconv.FormatUint(bpos, 10))
				}
			}

			if server.LastBackupMeta.Logical.BinLogGtid == "" && server.IsMariaDB() {
				if matches := gtidRegex.FindStringSubmatch(remainingLine); matches != nil {
					bgtid = matches[1]
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "GTID:%s", bgtid)
				}
			}
		}

		err := dumpCmd.Wait()

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "mysqldump: %s", err)
			errCh <- fmt.Errorf("Error mysqldump: %w", err) // Send the error through the channel with more context
		}
	}()

	// Wait for goroutines to finish
	wg.Wait()

	// Check for errors
	select {
	case err := <-errCh:
		// Handle the error here
		fmt.Println("Error occurred:", err)
	default:
		// No errors occurred
		fmt.Println("No errors occurred")
	}

	server.LastBackupMeta.Logical.BinLogGtid = bgtid
	server.LastBackupMeta.Logical.BinLogFilePos = bpos
	server.LastBackupMeta.Logical.BinLogFileName = bfile

	return err
}

func (server *ServerMonitor) JobBackupMyDumper(outputdir string) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB

	defer cluster.SetInLogicalBackupState(false)

	if cluster.MyDumperVersion == nil {
		if err = cluster.SetMyDumperVersion(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting MyDumper version: %s", err)
			return err
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "MyDumper version: %s", cluster.MyDumperVersion.ToString())
		}
	}

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && cluster.MyDumperVersion.Lower("0.12.3") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	threads := strconv.Itoa(cluster.Conf.BackupLogicalDumpThreads)
	myargs := strings.Split(strings.ReplaceAll(cluster.Conf.BackupMyDumperOptions, "  ", " "), " ")
	if cluster.MyDumperVersion.GreaterEqual("0.15.3") {
		myargs = append(myargs, "--clear")
	}
	myargs = append(myargs, "--outputdir", outputdir, "--threads", threads, "--host", misc.Unbracket(server.Host), "--port", server.Port, "--user", cluster.GetDbUser(), "--password", cluster.GetDbPass(), "--regex", "^(?!(replication_manager_schema\\.jobs$)).*")
	dumpCmd := exec.Command(cluster.GetMyDumperPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "%s", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", 1))
	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()

	var wg sync.WaitGroup
	var valid bool = true
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.myDumperCopyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		valid = server.myDumperCopyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err = dumpCmd.Wait(); err != nil && !valid {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mydumper:  %s", err)
		return err
	}

	if e2 := server.JobParseMyDumperMeta(); e2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error parsing mydumper metadata: %s", err.Error())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Success backup data via mydumper. Setting logical cookie")
	server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMydumper)

	return err
}

func (server *ServerMonitor) JobBackupDumpling(outputdir string) error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	conf := dumplingext.DefaultConfig()
	conf.Database = ""
	conf.Host = misc.Unbracket(server.Host)
	conf.User = cluster.GetDbUser()
	conf.Port, _ = strconv.Atoi(server.Port)
	conf.Password = cluster.GetDbPass()

	conf.Threads = cluster.Conf.BackupLogicalDumpThreads
	conf.FileSize = 1000
	conf.StatementSize = dumplingext.UnspecifiedSize
	conf.OutputDirPath = outputdir
	conf.Consistency = "flush"
	conf.NoViews = true
	conf.StatusAddr = ":8281"
	conf.Rows = dumplingext.UnspecifiedSize
	conf.Where = ""
	conf.EscapeBackslash = true
	conf.LogLevel = config.LvlInfo

	err = dumplingext.Dump(conf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Dumpling %s", err)
		return err
	}

	return err
}

func (server *ServerMonitor) JobBackupRiver() error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

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
	server.LastBackupMeta.Logical.Dest = cfg.DumpPath

	//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
	cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

	_, err = river.NewRiver(cfg)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error river backup: %s", err)
	}

	return err
}

func (server *ServerMonitor) JobBackupLogical() error {
	var err error
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server defined")
	}

	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Request logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return errors.New("Can't backup when server down")
	}

	//Wait for previous restic backup
	if cluster.IsInBackup() && cluster.Conf.BackupRestic {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Logical", cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupLogical()
	}

	cluster.SetInLogicalBackupState(true)
	start := time.Now()
	server.LastBackupMeta.Logical = &config.BackupMetadata{
		Id:             start.Unix(),
		StartTime:      start,
		BackupMethod:   config.BackupMethodLogical,
		BackupTool:     cluster.Conf.BackupLogicalType,
		BackupStrategy: config.BackupStrategyFull,
		Source:         server.URL,
	}

	// Removing previous valid backup state and start
	server.DelBackupLogicalCookie()

	//Skip other type if using backup script
	if cluster.Conf.BackupSaveScript != "" {
		server.LastBackupMeta.Logical.BackupTool = "script"
		server.LastBackupMeta.Logical.Dest = cluster.Conf.BackupSaveScript
		err = server.JobBackupScript()
		if err == nil {
			server.LastBackupMeta.Logical.Completed = true
			server.SetBackupLogicalCookie("script")
		}
	} else {

		//Only for record
		server.JobInsertTask(cluster.Conf.BackupLogicalType, "0", cluster.Conf.MonitorAddress)

		//Change to switch since we only allow one type of backup (for now)
		switch cluster.Conf.BackupLogicalType {
		case config.ConstBackupLogicalTypeMysqldump:
			filename := server.GetMyBackupDirectory() + "mysqldump.sql.gz"
			server.LastBackupMeta.Logical.Dest = filename
			server.LastBackupMeta.Logical.Compressed = true
			if cluster.Conf.BackupKeepUntilValid {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", filename, filename+".old").Run()
			}

			err = server.JobBackupMysqldump(filename)
			if err != nil {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				finfo, e3 := os.Stat(filename)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.Size = finfo.Size()
					server.LastBackupMeta.Logical.Completed = true
					server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMysqldump)
				}
			}
		case config.ConstBackupLogicalTypeDumpling:
			outputdir := server.GetMyBackupDirectory() + "dumpling"
			server.LastBackupMeta.Logical.Dest = outputdir
			if cluster.Conf.BackupKeepUntilValid {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}

			err = server.JobBackupDumpling(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				finfo, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.Size = finfo.Size()
					server.LastBackupMeta.Logical.Completed = true
					server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeDumpling)
				}
			}
		case config.ConstBackupLogicalTypeMydumper:
			outputdir := server.GetMyBackupDirectory() + "mydumper"
			server.LastBackupMeta.Logical.Dest = outputdir
			server.LastBackupMeta.Logical.Compressed = true
			if cluster.Conf.BackupKeepUntilValid {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}
			err = server.JobBackupMyDumper(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}

				finfo, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.Size = finfo.Size()
					server.LastBackupMeta.Logical.Completed = true
					server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeDumpling)
				}
			}
		case config.ConstBackupLogicalTypeRiver:
			//No change on river
			err = server.JobBackupRiver()
			if err != nil {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(cluster.Conf.BackupLogicalType, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}
	}

	server.WriteBackupMetadata(config.BackupMethodLogical)
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[SUCCESS] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "[ERROR] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	}

	backtype := "logical"
	server.BackupRestic(cluster.Conf.Cloud18GitUser, cluster.Name, server.DBVersion.Flavor, server.DBVersion.ToString(), backtype, cluster.Conf.BackupLogicalType)
	return nil
}

func (server *ServerMonitor) copyLogs(r io.Reader, module int, level string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, s.Text())
		}
	}
}

func (server *ServerMonitor) myDumperCopyLogs(r io.Reader, module int, level string) bool {
	cluster := server.ClusterGroup
	valid := true
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			stream := s.Text()
			if strings.Contains(stream, "Error") {
				if !strings.Contains(stream, "#mysql50#") {
					valid = false
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlErr, "[%s] %s", server.Name, stream)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, stream)
			}
		}
	}
	return valid
}

func (server *ServerMonitor) BackupRestic(tags ...string) error {
	cluster := server.ClusterGroup
	var stdout, stderr []byte
	var errStdout, errStderr error

	if cluster.Conf.BackupRestic {
		cluster.SetInResticBackupState(true)
		defer cluster.SetInResticBackupState(false)

		args := make([]string, 0)

		args = append(args, "backup")
		for _, tag := range tags {
			if tag != "" {
				args = append(args, "--tag")
				args = append(args, tag)
			}
		}
		args = append(args, server.GetMyBackupDirectory())

		resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, args...)

		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()

		//out, err := resticcmd.CombinedOutput()

		resticcmd.Env = cluster.ResticGetEnv()

		if err := resticcmd.Start(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s\n", err)
		}
		if errStdout != nil || errStderr != nil {
			log.Fatal("failed to capture stdout or stderr\n")
		}
		outStr, errStr := string(stdout), string(stderr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "result:%s\n%s\n%s", resticcmd.Path, outStr, errStr)

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "OnPremise run  job  %s", err)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "JobRunViaSSH %s, scriptpath : %s", err2, scriptpath)
		return errors.New("Cancel dbjob can't open script")

	}
	defer filerc.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(filerc)

	buf2 := strings.NewReader(server.GetSshEnv())
	r := io.MultiReader(buf2, buf)

	if client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Database jobs run via SSH: %s", stderr.String())
	}
	out := stdout.String()

	//Log Task - Debug Level
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Job run via ssh script: %s ,out: %s ,err: %s", scriptpath, out, stderr.String())

	res := new(JobResult)
	val := reflect.ValueOf(res).Elem()
	for i := 0; i < val.NumField(); i++ {
		jobname := val.Type().Field(i).Name
		if strings.Contains(strings.ToLower(string(out)), strings.ToLower("no "+jobname)) {
			val.Field(i).SetBool(false)
		} else {
			val.Field(i).SetBool(true)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Database jobs run via SSH: %s", val.Type().Field(i).Name)
			lower := strings.ToLower(jobname)
			if strings.HasPrefix(lower, "reseed") || strings.HasPrefix(lower, "flashback") {
				server.SetInReseedBackup(false)
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Exec via ssh  : %s", res)
	return nil
}

func (server *ServerMonitor) JobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup
	var err error

	if !server.IsMaster() {
		err = errors.New("Cancelling backup because server is not master")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if cluster.IsInFailover() {
		err = errors.New("Cancel job copy binlog during failover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if !cluster.Conf.BackupBinlogs {
		err = errors.New("Copy binlog not enable")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if server.IsReseeding {
		err = errors.New("Cancel job copy binlog during reseed")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() && cluster.Conf.BackupRestic {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlog(binlogfile, isPurge)
		}

		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	cmdrun := exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--raw", "--server-id=10000", "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+server.GetMyBackupDirectory(), binlogfile)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "%s", strings.ReplaceAll(cmdrun.String(), cluster.GetRplPass(), "XXXX"))

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		cluster.LogPrint(cmdrun.Stderr)
		cluster.LogPrint(cmdrun.Stdout)
		return cmdrunErr
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		defer server.BackupRestic(cluster.Conf.Cloud18GitUser, cluster.Name, server.DBVersion.Flavor, server.DBVersion.ToString(), backtype)
	}

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

	if cluster.IsInBackup() && cluster.Conf.BackupRestic {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupBinlogPurge(binlogfile)
	}

	cluster.SetInBinlogBackupState(true)
	defer cluster.SetInBinlogBackupState(false)

	binlogfilestart, _ := strconv.Atoi(strings.Split(binlogfile, ".")[1])
	prefix := strings.Split(binlogfile, ".")[0]
	binlogfilestop := binlogfilestart - cluster.Conf.BackupBinlogsKeep
	keeping := make(map[string]int)
	for binlogfilestop < binlogfilestart {
		if binlogfilestop > 0 {
			filename := prefix + "." + fmt.Sprintf("%06d", binlogfilestop)
			if _, err := os.Stat(server.GetMyBackupDirectory() + "/" + filename); os.IsNotExist(err) {
				if _, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup master missing binlog of %s,%s", server.URL, filename)
					//Set true to skip sending to resting multiple times
					server.InitiateJobBackupBinlog(filename, true)
				}
			}
			keeping[filename] = binlogfilestop
		}
		binlogfilestop++
	}
	files, err := os.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed to read backup directory of %s,%s", server.URL, err.Error())
	}

	for _, file := range files {
		_, ok := keeping[file.Name()]
		if strings.HasPrefix(file.Name(), prefix) && !ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Purging binlog file %s", file.Name())
			os.Remove(server.GetMyBackupDirectory() + "/" + file.Name())
		}
	}
	return nil
}

func (server *ServerMonitor) JobCapturePurge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := os.ReadDir(path)
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
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask,LvlInfo, "gniac2 %s: %s,", server.URL, server.GetVersion())
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
	defer dest.SetInReseedBackup(false)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rejoining from direct mysqldump from %s", source.URL)
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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))

	iodumpreader, err := dumpCmd.StdoutPipe()
	clientCmd.Stdin = io.MultiReader(bytes.NewBufferString("reset master;set sql_log_bin=0;"), iodumpreader)

	/*clientCmd.Stdin, err = dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask,config.LvlErr, "Failed opening pipe: %s", err)
		return err
	}*/
	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqldump command: %s at %s", err, strings.Replace(dumpCmd.String(), cluster.GetDbPass(), "XXXX", -1))
		return err
	}
	if err := clientCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't start mysql client:%s at %s", err, strings.Replace(clientCmd.String(), cluster.GetDbPass(), "XXXX", -1))
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		source.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		dest.copyLogs(stderrOut, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	dumpCmd.Wait()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", dest.URL)
	dest.StartSlave()
	return nil
}

func (server *ServerMonitor) JobBackupBinlogSSH(binlogfile string, isPurge bool) error {
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

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() && cluster.Conf.BackupRestic {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlogSSH(binlogfile, isPurge)
		}

		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	client, err := server.GetCluster().OnPremiseConnect(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "OnPremise run  job  %s", err)
		return err
	}
	defer client.Close()

	remotefile := server.BinaryLogDir + "/" + binlogfile
	localfile := server.GetMyBackupDirectory() + "/" + binlogfile

	fileinfo, err := client.Sftp().Stat(remotefile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting binlog file [%s] stat:  %s", remotefile, err)
		return err
	}

	err = client.Sftp().Download(remotefile, localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Download binlog error:  %s", err)
		return err
	}

	localinfo, err := os.Stat(localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	if fileinfo.Size() != localinfo.Size() {
		err := errors.New("Remote filesize is different with downloaded filesize")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		defer server.BackupRestic(cluster.Conf.Cloud18GitUser, cluster.Name, server.DBVersion.Flavor, server.DBVersion.ToString(), backtype)
	}
	return nil
}

func (server *ServerMonitor) InitiateJobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup

	if server.BinaryLogDir == "" {
		//Not using Variables[] due to uppercase values
		basename, _, err := dbhelper.GetVariableByName(server.Conn, "LOG_BIN_BASENAME", server.DBVersion)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Variable log_bin_basename not found!")
			return err
		}

		parts := strings.Split(basename, "/")
		binlogpath := strings.Join(parts[:len(parts)-1], "/")

		server.SetBinaryLogDir(binlogpath)
	}

	if !isPurge {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlogfile)
	}

	switch cluster.Conf.BinlogCopyMode {
	case "client", "mysqlbinlog":
		return server.JobBackupBinlog(binlogfile, isPurge)
	case "ssh":
		return server.JobBackupBinlogSSH(binlogfile, isPurge)
	case "script":
		return cluster.BinlogCopyScript(server, binlogfile, isPurge)
	}

	return errors.New("Wrong configuration for Backup Binlog Method!")
}

func (server *ServerMonitor) WaitAndSendSST(task string, filename string, loop int) error {
	cluster := server.ClusterGroup
	var err error

	if cluster.Conf.SuperReadOnly && cluster.GetMaster().URL != server.URL {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return nil
	}

	if !server.IsReseeding {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server is not in reseeding state, cancel sending file to %s", server.URL)
		return nil
	}

	switch task {
	case "reseedmariabackup", "reseedxtrabackup":
		if !server.IsReseeding {
			return fmt.Errorf("Server is not in reseeding state, cancel sending file to %s", server.URL)
		}
	case "flashbackmariabackup", "flashbackxtrabackup":
		if !server.IsFlashingBack {
			return fmt.Errorf("Server is not in flashback state, cancel sending file to %s", server.URL)
		}
	}

	rows, err := server.Conn.Queryx(fmt.Sprintf("SELECT done FROM replication_manager_schema.jobs WHERE task='%s' and state=%d", task, 2))
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Scheduler error fetching replication_manager_schema.jobs %s", err)
		server.JobsCreateTable()
		return err
	}

	var done int
	var count int = 0
	defer rows.Close()
	for rows.Next() {
		rows.Scan(&done)
		count++
	}

	time.Sleep(time.Second * 15)
	//Check if id exists
	if count > 0 {
		server.JobsUpdateState(task, "processing", 1, 0)
		go func() {
			err := cluster.SSTRunSender(filename, server)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, err.Error())
				server.JobsUpdateState(task, err.Error(), 5, 0)
			}
		}()
		return nil
	} else {
		if loop < 10 {
			loop++
			return server.WaitAndSendSST(task, filename, loop)
		}
	}

	query := "UPDATE replication_manager_schema.jobs SET state=5, result='Waiting more than max loop' where task = '%s'"
	server.ExecQueryNoBinLog(fmt.Sprintf(query, task))
	server.SetNeedRefreshJobs(true)
	return errors.New("Error: waiting for " + task + " more than max loop.")
}

func (server *ServerMonitor) ProcessReseedPhysical(task string) error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if server.IsFlashingBack {
		return errors.New("Server is in flashback state")
	}

	if master == nil {
		return errors.New("No master found")
	}

	useMaster := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := master.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useMaster = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending master physical backup to reseed %s", server.URL)

	go server.WaitAndSendSST(task, backupfile, 0)

	return nil
}

func (server *ServerMonitor) ProcessFlashbackPhysical(task string) error {

	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if server.IsReseeding {
		return errors.New("Server is in reseeding state")
	}

	if master == nil {
		return errors.New("No master found")
	}

	useSelfBackup := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling flashback. No backup file found for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending physical backup to flashback %s", server.URL)

	go server.WaitAndSendSST(task, backupfile, 0)

	return nil
}

func (server *ServerMonitor) WriteJobLogs(mod int, encrypted, key, iv, task string) error {
	cluster := server.ClusterGroup
	eCmd := exec.Command("echo", encrypted)
	// Create a pipe for the stdout of lsCmd
	eStdout, err := eCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error creating stdout pipe for log message: %s", err.Error())
		return err
	}

	dCmd := exec.Command("openssl", "aes-256-cbc", "-d", "-a", "-nosalt", "-K", ""+key+"", "-iv", ""+iv+"")
	dCmd.Stdin = eStdout
	dStdout, err := dCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error piping log message decryption: %s", err.Error())
		return err
	}
	// Start the first command
	if err := eCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error starting log message: %s", err.Error())
		return err
	}

	// Start the second command
	if err := dCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error starting log message decrypt: %s", err.Error())
		return err
	}

	// Read the output from grepCmd
	scanner := bufio.NewScanner(dStdout)
	for scanner.Scan() {
		output := scanner.Text()
		pos := strings.LastIndex(output, "}")
		if pos > 10 {
			output = output[:pos+1]
		}

		var logEntry config.LogEntry
		err = json.Unmarshal([]byte(output), &logEntry)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error loading JSON Entry: %s. Err: %s", output, err.Error())
			continue
		}

		server.ParseLogEntries(logEntry, mod, task)
	}

	if err := scanner.Err(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reading from log message decrypt: %s", err.Error())
		return err
	}

	// Wait for the commands to complete
	if err := eCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for log message done: %s", err.Error())
		return err
	}

	if err := dCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for log message decription: %s", err.Error())
		return err
	}

	return nil
}

func (server *ServerMonitor) ParseLogEntries(entry config.LogEntry, mod int, task string) error {
	cluster := server.ClusterGroup
	if entry.Server != server.URL {
		err := fmt.Errorf("Log entries and source mismatch: %s with %s", entry.Server, server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, err.Error())
		return err
	}

	binRegex := regexp.MustCompile(`filename '([^']+)', position '([^']+)', GTID of the last change '([^']+)'`)

	lines := strings.Split(strings.ReplaceAll(entry.Log, "\\n", "\n"), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			// Process the individual log line (e.g., write to file, send to a logging system, etc.)
			if strings.Contains(line, "ERROR") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlErr, "[%s] %s", server.URL, line)
			} else {
				switch task {
				case "xtrabackup", "mariabackup":
					if matches := binRegex.FindStringSubmatch(line); matches != nil {
						server.LastBackupMeta.Physical.BinLogGtid = matches[3]
						server.LastBackupMeta.Physical.BinLogFilePos, _ = strconv.ParseUint(matches[2], 10, 64)
						server.LastBackupMeta.Physical.BinLogFileName = matches[1]
					}
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlDbg, "[%s] %s", server.URL, line)
			}
		}
	}
	return nil
}

func (server *ServerMonitor) WriteBackupMetadata(backtype config.BackupMethod) {
	cluster := server.ClusterGroup
	var lastmeta *config.BackupMetadata

	switch backtype {
	case config.BackupMethodLogical:
		lastmeta = server.LastBackupMeta.Logical
	case config.BackupMethodPhysical:
		lastmeta = server.LastBackupMeta.Physical
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Wrong backup type for metadata in %s", server.URL)
		return
	}

	if finfo, err := os.Stat(lastmeta.Dest); err == nil {
		lastmeta.Size = finfo.Size()
		lastmeta.EndTime = time.Now()
	}

	task := server.JobResults.Get(lastmeta.BackupTool)

	//Wait until job result changed since we're using pointer
	for task.State < 3 {
		time.Sleep(time.Second)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Continue for writing metadata", server.URL)

	if task.State == 3 || task.State == 4 {
		//Wait for binlog metadata sent by writelog API
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Waiting for binlog info: %v", server.LastBackupMeta.Logical)
		for lastmeta.BinLogFileName == "" {
			time.Sleep(time.Second)
		}
		lastmeta.Completed = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Metadata completed: %v", server.LastBackupMeta.Logical)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Error occured in backup, writing incomplete metadata for backup in %s", server.URL)
	}

	bjson, err := json.MarshalIndent(lastmeta, "", "\t")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to marshall metadata for backup in %s: %s", server.URL, err.Error())
	}

	err = os.WriteFile(server.GetMyBackupDirectory()+lastmeta.BackupTool+".meta.json", bjson, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write metadata for backup in %s: %s", server.URL, err.Error())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Created metadata for backup in %s", server.URL)
	}

	//Don't change river
	if cluster.Conf.BackupKeepUntilValid && lastmeta.BackupTool != config.ConstBackupLogicalTypeRiver {
		if lastmeta.Completed {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup valid, removing old backup.")
			exec.Command("rm", "-r", lastmeta.Dest+".old").Run()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Error occured in backup, rolling back to old backup.")
			exec.Command("mv", lastmeta.Dest, lastmeta.Dest+".err").Run()
			exec.Command("mv", lastmeta.Dest+".old", lastmeta.Dest).Run()
			exec.Command("rm", "-r", lastmeta.Dest+".err").Run()
		}
	}
}

// Job state always updated in replication-manager runtime.
func (server *ServerMonitor) JobsUpdateState(task, result string, state, done int) error {
	var err error
	cluster := server.ClusterGroup

	if t, exists := server.JobResults.LoadOrStore(task, &config.Task{
		Task:   task,
		State:  state,
		Result: result,
		Done:   done,
	}); exists {
		t.State = state
		t.Done = done
		t.Result = result
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Job state updated in runtime. Continue to update state in jobs table.")

	conn, err := server.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't connect")
		return err
	}
	defer conn.Close()

	//Using lock to prevent wrong reads
	_, err = conn.Exec("set sql_log_bin=0;")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't disable binlog for session")
		return err
	}

	_, err = conn.Exec("LOCK TABLES replication_manager_schema.jobs WRITE;")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't lock table jobs for cancel reseed")
		return err
	}

	defer conn.Exec("UNLOCK TABLES;")

	if done == 1 {
		_, err = conn.Exec("UPDATE replication_manager_schema.jobs SET done=?, state=?, result=?, end=NOW() WHERE task =?;", done, state, result, task)
	} else {
		_, err = conn.Exec("UPDATE replication_manager_schema.jobs SET done=?, state=?, result=? WHERE task =?;", done, state, result, task)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Job can't update job: %s", err)
		return err
	}

	server.SetNeedRefreshJobs(true)

	return err
}

func (server *ServerMonitor) JobMyLoaderParseMeta(dir string) (config.MyDumperMetaData, error) {
	cluster := server.ClusterGroup
	dir = strings.TrimSuffix(dir, "/")
	if cluster.MyDumperVersion.GreaterEqual("0.14.1") {
		return server.JobParseMyDumperMetaNew(dir)
	} else {
		return server.JobParseMyDumperMetaOld(dir)
	}
}

func (server *ServerMonitor) JobParseMyDumperMeta() error {
	var m config.MyDumperMetaData
	var err error

	m, err = server.JobMyLoaderParseMeta(server.LastBackupMeta.Logical.Dest)
	if err != nil {
		return err
	}

	server.LastBackupMeta.Logical.BinLogGtid = m.BinLogUuid
	server.LastBackupMeta.Logical.BinLogFilePos = m.BinLogFilePos
	server.LastBackupMeta.Logical.BinLogFileName = m.BinLogFileName

	return nil
}

func (server *ServerMonitor) JobParseMyDumperMetaNew(dir string) (config.MyDumperMetaData, error) {

	var m config.MyDumperMetaData

	meta := dir + "/metadata"
	file, err := os.Open(meta)
	if err != nil {
		return m, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	var binlogFile, position, gtidSet string

	reFile := regexp.MustCompile(`^File\s*=\s*(.*)`)
	rePos := regexp.MustCompile(`^Position\s*=\s*(\d+)`)
	reGTID := regexp.MustCompile(`^Executed_Gtid_Set\s*=\s*(.*)`)

	for scanner.Scan() {
		line := scanner.Text()

		if binlogFile == "" {
			if matches := reFile.FindStringSubmatch(line); matches != nil {
				binlogFile = matches[1]
			}
		}

		if position == "" {
			if matches := rePos.FindStringSubmatch(line); matches != nil {
				position = matches[1]
			}
		}

		if gtidSet == "" {
			if matches := reGTID.FindStringSubmatch(line); matches != nil {
				gtidSet = matches[1]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
		return m, err
	}

	m.BinLogUuid = gtidSet
	m.BinLogFilePos, _ = strconv.ParseUint(position, 10, 64)
	m.BinLogFileName = binlogFile

	return m, nil
}

func (server *ServerMonitor) JobParseMyDumperMetaOld(dir string) (config.MyDumperMetaData, error) {

	var m config.MyDumperMetaData
	buf := new(bytes.Buffer)

	// metadata file name.
	meta := dir + "/metadata"

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
