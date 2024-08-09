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
	"fmt"
	"os"

	"github.com/signal18/replication-manager/config"
)

func (server *ServerMonitor) delCookie(key string) error {
	cluster := server.ClusterGroup
	err := os.Remove(server.Datadir + "/@" + key)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Remove cookie (%s) %s", key, err)
	}

	return err
}

func (server *ServerMonitor) DelProvisionCookie() error {
	return server.delCookie("cookie_prov")
}

func (server *ServerMonitor) DelWaitStartCookie() error {
	return server.delCookie("cookie_waitstart")
}

func (server *ServerMonitor) DelWaitStopCookie() error {
	return server.delCookie("cookie_waitstop")
}

func (server *ServerMonitor) DelReprovisionCookie() error {
	return server.delCookie("cookie_reprov")
}

func (server *ServerMonitor) DelUnprovisionCookie() error {
	return server.delCookie("cookie_unprov")
}

func (server *ServerMonitor) DelRestartCookie() error {
	return server.delCookie("cookie_restart")
}

func (server *ServerMonitor) DelWaitBackupCookie() error {
	return server.delCookie("cookie_waitbackup")
}

func (server *ServerMonitor) DelWaitLogicalBackupCookie() error {
	return server.delCookie("cookie_waitlogicalbackup")
}

func (server *ServerMonitor) DelWaitPhysicalBackupCookie() error {
	return server.delCookie("cookie_waitphysicalbackup")
}

func (server *ServerMonitor) DelBackupLogicalCookie() error {
	return server.delCookie("cookie_logicalbackup")
}

func (server *ServerMonitor) DelBackupPhysicalCookie() error {
	return server.delCookie("cookie_physicalbackup")
}

func (server *ServerMonitor) DelBackupScriptCookie() error {
	return server.delCookie("cookie_backup_script")
}

func (server *ServerMonitor) DelBackupMysqldumpCookie() error {
	return server.delCookie("cookie_backup_mysqldump")
}

func (server *ServerMonitor) DelBackupMydumperCookie() error {
	return server.delCookie("cookie_backup_mydumper")
}

func (server *ServerMonitor) DelBackupDumplingCookie() error {
	return server.delCookie("cookie_backup_dumpling")
}

func (server *ServerMonitor) DelBackupXtrabackupCookie() error {
	return server.delCookie("cookie_backup_xtrabackup")
}

func (server *ServerMonitor) DelBackupMariabackupCookie() error {
	return server.delCookie("cookie_backup_mariabackup")
}

func (server *ServerMonitor) DelBackupTypeCookie(backtype string) error {
	switch backtype {
	case config.ConstBackupLogicalTypeMysqldump:
		server.DelBackupLogicalCookie()
		return server.DelBackupMysqldumpCookie()
	case config.ConstBackupLogicalTypeMydumper:
		server.DelBackupLogicalCookie()
		return server.DelBackupMydumperCookie()
	case config.ConstBackupLogicalTypeDumpling:
		server.DelBackupLogicalCookie()
		return server.DelBackupDumplingCookie()
	case config.ConstBackupPhysicalTypeXtrabackup:
		server.DelBackupPhysicalCookie()
		return server.DelBackupXtrabackupCookie()
	case config.ConstBackupPhysicalTypeMariaBackup:
		server.DelBackupPhysicalCookie()
		return server.DelBackupMariabackupCookie()
	case "script":
		server.DelBackupLogicalCookie()
		return server.DelBackupScriptCookie()
	}

	return fmt.Errorf("No backup type of %s", backtype)
}

func (server *ServerMonitor) DelMaintenance() {
	server.IsMaintenance = false
}
