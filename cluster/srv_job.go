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
	"bytes"
	"compress/gzip"
	"io/ioutil"
	"os"
	"os/exec"

	river "github.com/signal18/replication-manager/river"
)

func (server *ServerMonitor) CreateOrReplaceSystemTable() error {
	_, err := server.Conn.Exec("set sql_log_bin=0")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't disable binlog for session")
		return err
	}
	_, err = server.Conn.Exec("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	if err != nil {
		return err
	}
	_, err = server.Conn.Exec("CREATE TABLE IF NOT EXISTS replication_manager_schema.jobs(id INT NOT NULL auto_increment PRIMARY KEY, task VARCHAR(20),  port INT, server VARCHAR(255), done TINYINT not null default 0, result VARCHAR(1000), start DATETIME, end DATETIME, KEY idx1(task,done)) engine=innodb")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't create table replication_manager_schema.jobs")
		return err
	}
	return nil
}

func (server *ServerMonitor) BackupPhysical() error {
	server.CreateOrReplaceSystemTable()
	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"/"+server.Id+"_xtrabackup.xbtream", ConstJobCreateFile)
	server.Conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('xtrabackup'," + port + ",'" + server.ClusterGroup.conf.BindAddr + "', NOW())")
	return err
}

func (server *ServerMonitor) BackupErrorLog() error {
	server.CreateOrReplaceSystemTable()
	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"/"+server.Id+"_log_error.log", ConstJobAppendFile)
	server.Conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('log_error'," + port + ",'" + server.ClusterGroup.conf.BindAddr + "', NOW())")
	return err
}

func (server *ServerMonitor) BackupSlowQueryLog() error {
	server.CreateOrReplaceSystemTable()
	port, err := server.ClusterGroup.SSTRunReceiver(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"/"+server.Id+"_slow_query_log_file.log", ConstJobAppendFile)
	server.Conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('slow_query_log_file'," + port + ",'" + server.ClusterGroup.conf.BindAddr + "', NOW())")
	return err
}

func (server *ServerMonitor) ZFSSnapBack() error {
	server.CreateOrReplaceSystemTable()
	server.Conn.Exec("INSERT INTO replication_manager_schema.jobs(task, port,server,start) VALUES('zfssnapback',0,'" + server.ClusterGroup.conf.BindAddr + "', NOW())")
	return nil
}

func (server *ServerMonitor) BackupLogical() error {
	server.ClusterGroup.LogPrintf(LvlInfo, "Receive backup request: %s", server.ClusterGroup.conf.BackupLogicalType)

	if server.ClusterGroup.conf.BackupLogicalType == "river" {
		cfg := new(river.Config)
		cfg.MyHost = server.URL
		cfg.MyUser = server.User
		cfg.MyPassword = server.Pass
		cfg.MyFlavor = "mariadb"

		//	cfg.ESAddr = *es_addr
		cfg.StatAddr = "127.0.0.1:12800"
		cfg.DumpServerID = 1001

		cfg.DumpPath = server.ClusterGroup.conf.WorkingDir + "/" + server.ClusterGroup.cfgGroup + "/river"
		cfg.DumpExec = server.ClusterGroup.conf.ShareDir + "/" + server.ClusterGroup.conf.GoArch + "/" + server.ClusterGroup.conf.GoOS + "/mysqldump"
		cfg.DumpOnly = true
		cfg.DumpInit = true
		cfg.BatchMode = "CSV"
		cfg.BatchSize = 100000
		cfg.BatchTimeOut = 1
		cfg.DataDir = server.ClusterGroup.conf.WorkingDir + "/" + server.ClusterGroup.cfgGroup + "/river"

		os.RemoveAll(cfg.DumpPath)

		//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
		cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

		river.NewRiver(cfg)
	}
	if server.ClusterGroup.conf.BackupLogicalType == "mysqldump" {
		usegtid := "--gtid"
		dumpCmd := exec.Command(server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqldump", "--opt", "--hex-blob", "--events", "--disable-keys", "--apply-slave-statements", usegtid, "--single-transaction", "--all-databases", "--host="+server.Host, "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
		var out bytes.Buffer
		dumpCmd.Stdout = &out
		err := dumpCmd.Run()
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "Error backup request: %s", err)
			return err
		}
		var outGzip bytes.Buffer
		w := gzip.NewWriter(&outGzip)
		w.Write(out.Bytes())
		w.Close()
		out = outGzip
		ioutil.WriteFile(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"/mysqldump.gz", out.Bytes(), 0666)
	}
	return nil
}
