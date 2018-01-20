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

func (server *ServerMonitor) BackupPhysical() error {
	server.Conn.Exec("set sql_log_bin=0")
	server.Conn.Exec("CREATE TABLE IF NOT EXISTS replication_manager_schema.backup(state int)")
	return nil
}

func (server *ServerMonitor) BackupLogical() error {
	server.ClusterGroup.LogPrintf(LvlInfo, "Receive backup request: %s", server.ClusterGroup.conf.BackupType)

	if server.ClusterGroup.conf.BackupType == "river" {
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
	if server.ClusterGroup.conf.BackupType == "mysqldump" {
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
