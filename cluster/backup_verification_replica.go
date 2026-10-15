// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/version"
)

// verificationResolveTimeout bounds each name resolution of verificationReplica.
const verificationResolveTimeout = 3 * time.Second

// lookupServerAddrs resolves a host name (replaced in tests).
var lookupServerAddrs = net.DefaultResolver.LookupIPAddr

// The restore level of backup verification (#2023) can run on a replica of the cluster
// designated as backup-verification-host: it must be listed in db-servers-ignored-hosts
// (never a failover candidate), and be neither the primary nor a relay. The test restore
// is then a reseed of that replica from the backup being verified: maintenance (the
// proxies drain it), replication stopped, the backup loaded and compared, then replication
// restarted from the backup's position as a logical reseed does, and maintenance left. Its
// schemas are kept: they are its data, caught up by replication.

// verificationReplica returns the server of the cluster backup-verification-host names
// (nil for a server outside the cluster), or why it can not receive a test restore. A
// server of the cluster is recognized under another name too: an alias, an IP address in
// any form, a CNAME (sameServerAddress).
func (cluster *Cluster) verificationReplica(name, port string) (*ServerMonitor, error) {
	for _, s := range cluster.Servers {
		if s == nil || (s.URL != name+":"+port && s.Host+":"+s.Port != name+":"+port && !sameServerAddress(name, port, s)) {
			continue
		}
		switch {
		case !s.IsIgnored():
			return nil, fmt.Errorf("backup-verification-host %s is a server of the cluster not listed in db-servers-ignored-hosts: a test restore replaces its data", s.URL)
		case s.IsMaster():
			return nil, fmt.Errorf("backup-verification-host %s is the primary", s.URL)
		case s.IsRelay:
			return nil, fmt.Errorf("backup-verification-host %s is a relay: its replicas would replicate the test restore", s.URL)
		}
		return s, nil
	}
	return nil, nil
}

// beginVerificationReseed takes the replica out of service for the test restore: the reseed
// flag, maintenance unless it already was, replication stopped. It returns whether it set
// maintenance.
func (replica *ServerMonitor) beginVerificationReseed() (bool, error) {
	cluster := replica.ClusterGroup
	if ok, running := replica.TrySetInReseedBackup("verify"); !ok {
		return false, fmt.Errorf("%s is already reseeding (%s)", replica.URL, running)
	}
	setMaintenance := !replica.IsMaintenance
	if setMaintenance {
		replica.SetMaintenance()
	}
	logs, err := replica.StopAllSlaves()
	cluster.LogSQL(logs, err, replica.URL, "BackupVerification", config.LvlInfo, "Backup verification could not stop the replication of %s: %s", replica.URL, err)
	if err == nil {
		// StopAllSlaves stops the channels the monitor knows: an empty or stale list stops none
		err = replica.stopLiveReplication()
	}
	if err != nil {
		replica.endVerificationReseed(setMaintenance, false)
		return false, fmt.Errorf("stop the replication of %s: %w", replica.URL, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification took %s out of service: maintenance, replication stopped", replica.URL)
	return setMaintenance, nil
}

// resumeVerificationReplica points the replica at the backup's position and restarts its
// replication, as a logical reseed does after its load: the binary logs of the load are
// reset, GTID_PURGED set on MySQL (MariaDB gets gtid_slave_pos from the repoint).
func (replica *ServerMonitor) resumeVerificationReplica(meta *backupmgr.BackupMetadata) error {
	cluster := replica.ClusterGroup
	logs, err := replica.ResetMaster()
	cluster.LogSQL(logs, err, replica.URL, "BackupVerification", config.LvlInfo, "Backup verification could not reset the binary logs of %s: %s", replica.URL, err)
	if err != nil {
		return fmt.Errorf("reset the binary logs of %s: %w", replica.URL, err)
	}
	if replica.HasMySQLGTID() && meta.BinLogGtid != "" {
		logs, err := dbhelper.SetGtidPurged(replica.Conn, replica.DBVersion, meta.BinLogGtid)
		cluster.LogSQL(logs, err, replica.URL, "BackupVerification", config.LvlInfo, "Backup verification could not set GTID_PURGED on %s: %s", replica.URL, err)
		if err != nil {
			return fmt.Errorf("GTID_PURGED on %s: %w", replica.URL, err)
		}
	}
	replica.logicalRestorePosition.Store(nil) // the position of the backup's metadata
	if err := replica.pointLogicalReseedReplica(meta, true, false); err != nil {
		return fmt.Errorf("restart the replication of %s: %w", replica.URL, err)
	}
	return nil
}

// endVerificationReseed releases the reseed flag, and leaves maintenance when the test
// restore set it and the replica replicates again; a replica whose test restore failed
// stays in maintenance, its replication stopped: it needs a reseed.
func (replica *ServerMonitor) endVerificationReseed(setMaintenance, resumed bool) {
	cluster := replica.ClusterGroup
	replica.SetInReseedBackup("")
	if !resumed {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr, "Backup verification left %s in maintenance, its replication stopped: its data is the partial test restore, reseed it", replica.URL)
		return
	}
	if setMaintenance {
		replica.DelMaintenance()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification reseeded %s from the backup: replicating from its position", replica.URL)
}

// sameServerAddress reports whether host:port is server s under another name: the same port
// and a common address once both names are resolved.
func sameServerAddress(host, port string, s *ServerMonitor) bool {
	if s.Port != port {
		return false
	}
	for _, a := range resolveServerAddrs(host) {
		for _, b := range resolveServerAddrs(s.Host) {
			if a.Equal(b) {
				return true
			}
		}
	}
	return false
}

// resolveServerAddrs returns the addresses of host: itself when it is an IP address (IPv6
// with or without brackets), else what it resolves to; none when it does not resolve.
func resolveServerAddrs(host string) []net.IP {
	host = misc.Unbracket(host)
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}
	}
	ctx, cancel := context.WithTimeout(context.Background(), verificationResolveTimeout)
	defer cancel()
	addrs, err := lookupServerAddrs(ctx, host)
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips
}

// pauseEventScheduler switches off the event scheduler of the server a test restore loads
// into, so that no event of the backup runs there (an event created ENABLE whose start has
// passed runs at once); it returns what switches it back on. An OFF or DISABLED scheduler
// is left as it is.
func (server *ServerMonitor) pauseEventScheduler(db *sqlx.DB, ver *version.Version, host string) (func(), error) {
	cluster := server.ClusterGroup
	state, query, err := dbhelper.GetEventScheduler(db, ver)
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlDbg, "Backup verification could not read the event scheduler of %s: %s", host, err)
	if err != nil {
		return func() {}, fmt.Errorf("event scheduler of %s: %w", host, err)
	}
	if state != "ON" {
		return func() {}, nil
	}
	logs, err := dbhelper.SetEventScheduler(db, false, ver)
	cluster.LogSQL(logs, err, host, "BackupVerification", config.LvlInfo, "Backup verification could not switch off the event scheduler of %s: %s", host, err)
	if err != nil {
		return func() {}, fmt.Errorf("switch off the event scheduler of %s: %w", host, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification switched off the event scheduler of %s for the test restore", host)
	return func() {
		logs, err := dbhelper.SetEventScheduler(db, true, ver)
		cluster.LogSQL(logs, err, host, "BackupVerification", config.LvlErr, "Backup verification could not switch the event scheduler of %s back on: %s", host, err)
		if err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification switched the event scheduler of %s back on", host)
		}
	}, nil
}

// disableVerificationEvents sets the events enabled on the replica after the test restore
// to DISABLE ON SLAVE (ON REPLICA from MySQL 8.2), as replication creates them: the
// load created them ENABLE, and an enabled event runs on the replica.
func (replica *ServerMonitor) disableVerificationEvents() error {
	cluster := replica.ClusterGroup
	events, query, err := dbhelper.GetEventStatus(replica.Conn, replica.DBVersion)
	cluster.LogSQL(query, err, replica.URL, "BackupVerification", config.LvlDbg, "Backup verification could not list the events of %s: %s", replica.URL, err)
	if err != nil {
		return fmt.Errorf("events of %s: %w", replica.URL, err)
	}
	for _, ev := range events {
		if ev.Status != 1 { // 1 ENABLED, 2 DISABLED, 3 disabled on the replicas
			continue
		}
		logs, err := dbhelper.SetEventStatus(replica.Conn, ev, 3, replica.DBVersion)
		cluster.LogSQL(logs, err, replica.URL, "BackupVerification", config.LvlInfo, "Backup verification could not disable the event %s.%s on %s: %s", ev.Db, ev.Name, replica.URL, err)
		if err != nil {
			return fmt.Errorf("disable the event %s.%s on %s: %w", ev.Db, ev.Name, replica.URL, err)
		}
	}
	return nil
}

// stopLiveReplication reads the replication channels from the replica itself, stops those
// still running and reads them again: the test restore never loads while a channel applies
// changes.
func (replica *ServerMonitor) stopLiveReplication() error {
	cluster := replica.ClusterGroup
	for attempt := 0; attempt < 2; attempt++ {
		// as the monitor reads them: every channel of MariaDB, MySQL's channels
		read := dbhelper.GetChannelSlaveStatus
		if replica.DBVersion.IsMariaDB() {
			read = dbhelper.GetAllSlavesStatus
		}
		channels, query, err := read(replica.Conn, replica.DBVersion)
		cluster.LogSQL(query, err, replica.URL, "BackupVerification", config.LvlDbg, "Backup verification could not read the replication of %s: %s", replica.URL, err)
		if err != nil {
			return fmt.Errorf("replication status: %w", err)
		}
		var running []string
		for _, ch := range channels {
			if replicationThreadStopped(ch.SlaveIORunning.String) && replicationThreadStopped(ch.SlaveSQLRunning.String) {
				continue
			}
			running = append(running, "'"+ch.ConnectionName.String+"'")
			if attempt == 0 {
				logs, err := dbhelper.StopSlave(replica.Conn, ch.ConnectionName.String, replica.DBVersion)
				cluster.LogSQL(logs, err, replica.URL, "BackupVerification", config.LvlInfo, "Backup verification could not stop the channel %s of %s: %s", ch.ConnectionName.String, replica.URL, err)
			}
		}
		if len(running) == 0 {
			return nil
		}
		if attempt == 1 {
			return fmt.Errorf("channel(s) %s still running", strings.Join(running, ", "))
		}
	}
	return nil
}

// replicationThreadStopped reads a Slave_IO_Running / Slave_SQL_Running value: No (or no
// value) is stopped; Yes, Connecting and Preparing run.
func replicationThreadStopped(state string) bool {
	return state == "" || strings.EqualFold(state, "No")
}
