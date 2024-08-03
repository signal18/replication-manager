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
	"encoding/json"
	"os"

	"github.com/signal18/replication-manager/config"
)

func (server *ServerMonitor) FetchLastBackupMetadata() {
	cluster := server.ClusterGroup
	var logical int64 = 0
	var physical int64 = 0
	if server.HasBackupLogicalCookie() {
		// commented for backward compatibility. Will be used later for atomic checking
		// if server.HasBackupMysqldumpCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeMysqldump, &logical)
		// }
		// if server.HasBackupMydumperCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeMydumper, &logical)
		// }
		// if server.HasBackupDumplingCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeDumpling, &logical)
		// }

		if logical > 0 {
			server.LastBackupMeta.Logical = cluster.BackupMetaMap.Get(logical)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No logical backup metadata found")
		}
	}

	if server.HasBackupPhysicalCookie() {
		// commented for backward compatibility. Will be used later for atomic checking
		// if server.HasBackupXtrabackupCookie() {
		server.AppendLastMetadata(config.ConstBackupPhysicalTypeXtrabackup, &physical)
		// }
		// if server.HasBackupMariabackupCookie() {
		server.AppendLastMetadata(config.ConstBackupPhysicalTypeMariaBackup, &physical)
		// }

		if physical > 0 {
			server.LastBackupMeta.Physical = cluster.BackupMetaMap.Get(physical)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No physical backup metadata found")
		}
	}
}

func (server *ServerMonitor) AppendLastMetadata(method string, latest *int64) {
	cluster := server.ClusterGroup
	if meta, err := server.ReadLastMetadata(method); err == nil {
		cluster.BackupMetaMap.Set(meta.Id, meta)
		if *latest < meta.Id {
			*latest = meta.Id
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reading %s meta: %s", method, err.Error())
	}
}

func (server *ServerMonitor) ReadLastMetadata(method string) (*config.BackupMetadata, error) {
	var filename string = method
	var ext string = "meta.json"

	filename = server.GetMyBackupDirectory() + filename + ext
	_, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	meta := new(config.BackupMetadata)
	err = json.NewDecoder(file).Decode(meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}
