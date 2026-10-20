package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) ReloadStagingScript() error {
	var script string
	var err error
	var content []byte

	filename := "staging_refresh.sh"
	template := "scripts/" + filename

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Reload staging script")

	script = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + filename

	if cluster.Conf.WithEmbed == "ON" {
		content, err = share.EmbededDbModuleFS.ReadFile(template)
	} else {
		content, err = os.ReadFile(cluster.Conf.ShareDir + "/" + template)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error reading default staging script. %s", err)
		return err
	}

	os.Remove(script)

	err = os.WriteFile(script, content, 0755)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error writing default staging script. %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) RefreshStaging(source *Cluster, masterGTIDList string) error {
	var err error

	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging not enabled")
		return nil
	}

	if cluster.IsRefreshStaging {
		return nil
	}

	if cluster.StagingServer == nil || cluster.StagingServer.State != stateUnconn {
		for _, srv := range cluster.Servers {
			if srv.State == stateUnconn {
				cluster.SetStagingServer(srv)
				break
			}
		}
	}

	bcksrv := source.GetBackupServer()
	if cluster == source {
		if bcksrv != nil && !bcksrv.HasBackupLogicalCookie() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Current master has no backup. Create logical backup for refresh staging on %s", bcksrv.URL)

			err = bcksrv.JobBackupLogical(context.Background())
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Create logical backup for refresh staging on %s failed: %s", bcksrv.URL, err)
				return err
			}
		}
	}

	cluster.IsNeedStagingChange = true
	cluster.IsRefreshStaging = true
	defer func() {
		cluster.IsRefreshStaging = false
	}()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging initiated")

	if cluster.Conf.TopologyStagingRefreshScript != "" {
		return cluster.RefreshStagingScript()
	}

	NB_SLAVES := len(cluster.GetSlaves())

	if NB_SLAVES == 2 {
		STG := cluster.GetSlaveByIndex(0)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Refresh staging initiated")

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Stopping slave %s replication", STG.Name)
		STG.StopSlave()

		// Wait until the slave is stopped
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Waiting for slave %s to stop replication", STG.URL)
		waitstart := time.Now()
		for STG.State == stateSlave {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to stop replication", STG.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s replication stopped", STG.URL)

		fileio, err := os.OpenFile(STG.Datadir+"/replications.json", os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error opening replication info file: %s", err)
		} else {
			defer fileio.Close()
			encoder := json.NewEncoder(fileio)
			encoder.SetIndent("", "\t")
			err = encoder.Encode(STG.Replications)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error encoding replication info: %s", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication info saved to %s", fileio.Name())
			}
		}

		_, err = STG.ResetSlave()
		if err != nil {
			return err
		}

		waitstart = time.Now()
		for STG.State != stateUnconn {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to be reset", STG.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s is now STANDALONE", STG.URL)
	} else if NB_SLAVES == 1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Picking a slave and standalone")

		STG := cluster.StagingServer
		if STG == nil {
			STG, err = cluster.GetStandaloneServerByIndex(0)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error getting standalone server: %s", err)
				return err
			}
		}

		if STG == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No standalone server found")
			return fmt.Errorf("no standalone server found")
		}

		SL := cluster.GetSlaveByIndex(0)
		if SL == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave server found")
			return fmt.Errorf("no slave server found")
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Switching staging from STANDALONE %s to SLAVE %s", STG.URL, SL.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Stopping slave %s replication", SL.URL)

		SL.StopSlave()

		// Wait until the slave is stopped
		waitstart := time.Now()
		for SL.State == stateSlave {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to stop replication", SL.URL)
				return err
			}

			time.Sleep(1 * time.Second)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s replication stopped", SL.URL)

		fileio, err := os.OpenFile(SL.Datadir+"/replications.json", os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error opening replication info file: %s", err)
		} else {
			defer fileio.Close()
			encoder := json.NewEncoder(fileio)
			encoder.SetIndent("", "\t")
			err = encoder.Encode(SL.Replications)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error encoding replication info: %s", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication info saved to %s", fileio.Name())
			}
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Resetting master position on slave %s", SL.URL)
		SL.StopSlave()
		_, err = SL.ResetSlave()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error resetting slave: %s", err)
			return err
		}

		waitstart = time.Now()
		for SL.State != stateUnconn {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for slave %s to be reset", SL.URL)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error resetting slave: %s", err)
				return err
			}

			time.Sleep(1 * time.Second)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Slave %s is now STANDALONE", SL.URL)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Restoring STANDALONE %s as SLAVE", STG.URL)

		// Set as read only
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Setting STANDALONE %s to read only", STG.URL)
		STG.SetReadOnly()

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Resetting master position on standalone %s", STG.URL)
		_, err = STG.ResetMaster()
		if err != nil {
			return err
		}

		if cluster == source {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Reseed standalone %s", STG.URL)
			err = STG.JobReseedLogicalBackup(context.Background(), "default")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for refresh staging on %s failed: %s", STG.URL, err)
				return err
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Reseed standalone %s from parent cluster %s backup", STG.URL, source.Name)
			_, err = cluster.ReseedFromParentCluster(source, STG, masterGTIDList) // Sync GTID with cluster master
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for refresh staging on %s failed: %s", STG.URL, err)
				return err
			}

			STG.ChangeMasterTo(cluster.GetMaster(), "SLAVE_POS")
		}

		waitstart = time.Now()
		for STG.State != stateSlave && STG.State != stateSlaveLate {
			if waitstart.Add(60 * time.Second).Before(time.Now()) {
				err = fmt.Errorf("timeout waiting for standalone %s to be reseeded", STG.URL)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for refresh staging on %s failed: %s", STG.URL, err)
				return err
			}

			time.Sleep(1 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Standalone %s is now a slave", STG.URL)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[STAGING] Refresh staging completed")
	}

	return nil
}

func (cluster *Cluster) RefreshStagingScript() error {
	script := cluster.Conf.TopologyStagingRefreshScript

	if _, err := os.Stat(script); os.IsNotExist(err) {
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Error checking script %s: %s", script, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Run refresh staging script %s", script)

	cmd := exec.Command(script)
	cmd.Env = cluster.GetExecEnv()
	stdoutIn, err := cmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}
	stderrIn, err := cmd.StderrPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}

	if err := cmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed refresh staging command : %s %s", cmd.Path, err)
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		cluster.CopyLogs(stdoutIn, config.ConstLogModTask, config.LvlInfo, "staging")
		wg.Done()
	}()

	go func() {
		cluster.CopyLogs(stderrIn, config.ConstLogModTask, config.LvlInfo, "staging")
		wg.Done()
	}()

	wg.Wait()

	err = cmd.Wait()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "%s\n", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh staging script completed")

	return nil
}

func (cluster *Cluster) PostDetachStaging(host, port, newstate, oldstate string) error {
	if cluster.Conf.TopologyStaging && cluster.Conf.TopologyStagingPostDetachScript != "" {
		script := cluster.Conf.TopologyStagingPostDetachScript

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Run post detach staging script %s", script)

		cmd := exec.Command(script, cluster.Name, host, port, newstate, oldstate, cluster.GetDbUser(), cluster.GetDbPass())
		cmd.Env = cluster.GetExecEnv()
		stdoutIn, _ := cmd.StdoutPipe()
		stderrIn, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "Failed post detach command : %s %s", cmd.Path, err)
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			cluster.CopyLogs(stdoutIn, config.ConstLogModTask, config.LvlInfo, "staging")
			wg.Done()
		}()

		go func() {
			cluster.CopyLogs(stderrIn, config.ConstLogModTask, config.LvlInfo, "staging")
			wg.Done()
		}()

		wg.Wait()

		err := cmd.Wait()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlErr, "%s\n", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) AddProxyToStagingHosts(prx DatabaseProxy) {
	if prx == nil {
		return
	}

	prx.SetStaging(true)

	if cluster.Conf.StagingProxyHosts == "" {
		cluster.Conf.StagingProxyHosts = prx.GetName()
	} else {
		cluster.Conf.StagingProxyHosts = cluster.Conf.StagingProxyHosts + "," + prx.GetName()
	}
}

func (cluster *Cluster) DelProxyFromStagingHosts(prx DatabaseProxy) {
	prx.SetStaging(false)

	// Remove from staging hosts
	cluster.Conf.StagingProxyHosts = misc.RemoveFromList(cluster.Conf.StagingProxyHosts, prx.GetName())
}

func (cluster *Cluster) GetStagingProxyHosts() []string {
	if cluster.Conf.StagingProxyHosts == "" {
		return []string{}
	}
	return strings.Split(cluster.Conf.StagingProxyHosts, ",")
}

// mariadbDumpWritesAccounts reports whether MariaDB dump options write the accounts as
// statements (--system=all or a list holding users), and leave their tables out of the dump.
// Another --system value (timezones, plugins, ...) dumps the account tables as data.
func mariadbDumpWritesAccounts(dumpOptions string) bool {
	for _, opt := range strings.Fields(dumpOptions) {
		if !strings.HasPrefix(opt, "--system=") {
			continue
		}
		for _, v := range strings.Split(strings.TrimPrefix(opt, "--system="), ",") {
			if v == "all" || v == "users" {
				return true
			}
		}
	}
	return false
}

// reseedGTIDSlavePos is the gtid_slave_pos of a MariaDB server seeded from a backup at pos:
// the backup's GTID for the domains the backup carries, current's for the other domains.
func reseedGTIDSlavePos(pos string, current *gtid.List) string {
	slavePos := *gtid.NewList(pos)
	backupDomains := make(map[uint64]bool)
	for _, g := range slavePos {
		backupDomains[g.DomainID] = true
	}
	if current != nil {
		for _, g := range *current {
			if !backupDomains[g.DomainID] {
				slavePos = append(slavePos, g)
			}
		}
	}
	return slavePos.Sprint()
}

// setSeedGTIDSlavePos positions a MariaDB server seeded from a backup at pos and returns the
// gtid_slave_pos it set: the backup's domains from the backup, every other domain from the
// server's live gtid_current_pos, as the parent seed always merged them (CurrentGtid.Merge).
// gtid_current_pos, not gtid_slave_pos: it holds the server's own binlog domain, which a child
// cluster's master has (its provisioning and its restore are written there) and without which
// gtid_strict_mode refuses the set (error 1948, MDEV-24234); the source accepts a position
// with domains it does not have, as a multi-source replica's (#2124).
func (server *ServerMonitor) setSeedGTIDSlavePos(pos string) (string, string, error) {
	current, logs, err := dbhelper.GetVariableByName(server.Conn, "GTID_CURRENT_POS", server.DBVersion)
	if err != nil {
		return "", logs, err
	}
	value := reseedGTIDSlavePos(pos, gtid.NewList(current))
	logs, err = dbhelper.SetGTIDSlavePos(server.Conn, value)
	return value, logs, err
}

func (cluster *Cluster) ReseedFromParentCluster(parent *Cluster, target *ServerMonitor, masterGTIDList string) (string, error) {
	var err error
	var logs, masterCurrentGTID string

	if parent == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Parent cluster cannot be nil")
		return "", fmt.Errorf("parent cluster cannot be nil")
	}

	backtype := parent.Conf.BackupLogicalType

	if target == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No target server found")
		return "", fmt.Errorf("no target server found")
	}

	// The parent's logical backup is the only source (#1989): checked before anything
	// is touched, with an error naming what is missing.
	backupfile, bcksrv, err := parent.ParentLogicalBackup()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Reseed of %s from parent cluster %s refused: %s", cluster.Name, parent.Name, err)
		return "", err
	}
	// a dump that stops and starts replication with its position as a comment would start
	// the channel from the old position: refused before the channel is stopped or repointed
	if backtype == config.ConstBackupLogicalTypeMysqldump {
		if unpositioned, derr := dumpStartsReplicationUnpositioned(backupfile); derr == nil && unpositioned {
			return "", errDumpStartsUnpositioned(backupfile)
		}
	}

	pmaster := parent.GetMaster()
	if pmaster == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master server found")
		return "", fmt.Errorf("no master server found")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModExternalScript, config.LvlInfo, "Refresh data from parent cluster initiated")

	if target.HasAnyReseedingState() {
		return "", fmt.Errorf("Server is in reseeding state by %s", target.IsReseeding)
	}

	task := "reseed" + parent.Conf.BackupLogicalType
	target.SetInReseedBackup(task)
	defer func() {
		if target.HasReseedingState(task) {
			target.SetInReseedBackup("")
		}
	}()

	// Stamp the real start time now, before the potentially slow work below
	// (StopSlaveChannel, ChangeMaster) -- but only for a type the dispatch
	// below actually executes. Stamping unconditionally here would risk
	// leaving an unsupported type stuck at "processing" forever, since the
	// dispatch below has no case for it; only stamp when we know a terminal
	// call is guaranteed to follow, without changing what counts as a
	// supported type. No JobInsertTask call anywhere in this function, so
	// there is no DB row either way.
	if backtype == config.ConstBackupLogicalTypeMysqldump || backtype == config.ConstBackupLogicalTypeMydumper {
		target.JobsUpdateStateRuntimeOnly(task, "processing", 1, 0)
	}

	//Delete wait logical backup cookie
	target.DelWaitLogicalBackupCookie()

	// The channel from the parent's master carries the PARENT's replication-source-name,
	// the same name on both sides: the parent adopts the child master through it
	// (AddChildServers). The child's own replication-source-name names its internal
	// channel (child master to child replicas) and must differ.
	channel := parent.Conf.MasterConn
	if target.IsMaster() {
		// Set replication master to current master if not PITR
		logs, err = target.StopSlaveChannel(channel)
		if err != nil {
			cluster.LogSQL(logs, err, target.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", target.URL, err)
		}

		changeOpt := parent.GetChangeMasterBaseOptForSlave(target, pmaster, false)
		changeOpt.Channel = channel
		changeOpt.Mode = "SLAVE_POS"

		if target.DBVersion.IsMySQLOrPercona() {
			if target.HasMySQLGTID() {
				changeOpt.Mode = "MASTER_AUTO_POSITION"
			} else {
				changeOpt.Mode = "POSITIONAL"
			}
		}

		target.ChangeMaster(changeOpt) // Ignore error
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backtype, target.URL)

	if backtype == config.ConstBackupLogicalTypeMysqldump {
		// a splitdump (#2011): the Go splitdump loader, schema then data, the GTID from
		// its metadata, as a logical reseed does; ColumnStore conversion included (#2010)
		meta := bcksrv.LastBackupMeta.Logical
		split, _ := isSplitDumpDir(backupfile)
		if split {
			// a ColumnStore child (#2015): the restore feeds replplugin-columnstore its
			// ColumnStore tables' rows, the plugin streams the binlog after it
			feed := cluster.HasConfigTopoColumnstore() && target.IsMaster()
			if feed {
				if err := cluster.startColumnstoreChild(parent, target, channel, backupfile); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ColumnStore child %s: %s", cluster.Name, err)
					return "", fmt.Errorf("ColumnStore child preparation: %w", err)
				}
			}
			err = target.reseedMysqldumpWithMetadata(context.Background(), backupfile, false, meta)
			if feed {
				if ferr := cluster.finishColumnstoreChild(target, channel, backupfile, err == nil); ferr != nil && err == nil {
					err = fmt.Errorf("ColumnStore child: %w", ferr)
				}
			}
		} else {
			// Without a split user, the parent's accounts are in the dump: as mysql table data on
			// MySQL/Percona, or on MariaDB without --system=all or users, which the restore would
			// load over the child's own. A MariaDB dump with --system=all or users writes them as
			// statements, left out of a restore without restore-user
			// (reseedMysqldumpSystemReplaySource). The backup metadata does not record the dump
			// options: the parent's are read.
			if meta != nil && !meta.SplitUser && !(target.IsMariaDB() && mariadbDumpWritesAccounts(parent.getDumpParameter())) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Reseed mysqldump with no split user is not supported")
				target.JobsUpdateStateRuntimeOnly(task, "Reseed mysqldump with no split user is not supported", 5, 1)
				return "", fmt.Errorf("Reseed mysqldump with no split user is not supported")
			}
			err = target.JobReseedMysqldump(backupfile, false)
			// MariaDB: the dump writes the parent's position as a comment only, so the target keeps
			// its old gtid_slave_pos and its channel would resume from it. It takes the backup's GTID
			// for the domains the backup has, and keeps the other domains it replicates.
			if err == nil && target.IsMariaDB() {
				// an older backup that starts replication itself from the position it sets
				// (--apply-slave-statements with an active position) set gtid_slave_pos and started
				// the channel: the server refuses another set while it runs (error 1198)
				dumpStarted, derr := dumpStartsReplication(backupfile)
				if derr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Can not read the replication statements of %s: %s", backupfile, derr)
				}
				if dumpStarted {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed from parent cluster: gtid_slave_pos of %s set by the restored dump %s, which started replication", target.URL, backupfile)
				} else if pos, _, _ := logicalReseedPosition(target.logicalRestorePosition.Load(), meta); pos != "" {
					// the restore replaced only the data of the backup's domains: the others are
					// the server's (setSeedGTIDSlavePos). Only the managed channel was stopped
					// before the load (JobReseedMysqldump): another channel running on the target
					// refuses the set (error 1198). err, not a local: a seed whose position is not
					// set fails, its channel would resume from the old position
					if masterGTIDList, logs, err = target.setSeedGTIDSlavePos(pos); err != nil {
						cluster.LogSQL(logs, err, target.URL, "Reseed", config.LvlErr, "Failed to set gtid_slave_pos on %s: %s", target.URL, err)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed from parent cluster: gtid_slave_pos of %s set to %s (backup %s)", target.URL, masterGTIDList, pos)
					}
				}
			}
		}
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		// the data only: the staging cluster keeps its own accounts and system tables
		err = target.JobReseedMyLoaderFromParent(backupfile)
		meta, err2 := cluster.JobMyLoaderParseMeta(backupfile)
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
			err = err2
		} else {
			// Set GTID position for MariaDB
			if target.IsMariaDB() && (target.HaveMariaDBGTID || (target.IsMaster() && target.SlaveGtid != nil && target.SlaveGtid.Sprint() != "")) {
				if target.IsMaster() {
					metaGTID := gtid.NewList(meta.BinLogUuid)
					newGTID := target.SlaveGtid.Merge(*metaGTID)
					newCurrentGTID := target.CurrentGtid.Merge(*metaGTID)
					masterGTIDList = newGTID.Sprint()
					masterCurrentGTID = newCurrentGTID.Sprint()
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata with GTID %s, meta %s", masterGTIDList, meta.BinLogUuid)
				target.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+masterGTIDList+"'", time.Second)
			}
		}
	} else {
		return "", fmt.Errorf("Unknown backup type %s", backtype)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backtype, target.URL, err.Error())
		target.MarkReseedFailed(err)
		target.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Reseed logical backup %s from parent cluster failed on %s", backtype, target.URL)
		return "", err
	}

	target.JobsUpdateStateRuntimeOnly(task, "Reseed completed", 3, 1)

	if target.IsMaster() {
		_, err2 := target.StartSlaveChannel(channel)
		if err2 != nil {
			cluster.LogSQL(logs, err, target.URL, "Rejoin", config.LvlErr, "Failed start slave on server: %s %s", target.URL, err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave on %s", target.URL)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed logical backup %s from parent cluster completed on %s", backtype, target.URL)

	return masterCurrentGTID, nil
}

// ParentLogicalBackup returns the logical backup a child cluster (staging) is seeded
// from: the file or directory of this cluster's backup-logical-type on its backup
// server. An error names what is missing (#1989): no backup server, no logical backup
// taken, a type the reseed cannot load, or the file gone from disk.
func (cluster *Cluster) ParentLogicalBackup() (string, *ServerMonitor, error) {
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		return "", nil, fmt.Errorf("parent cluster %s has no backup server", cluster.Name)
	}
	if !bcksrv.HasBackupLogicalCookie() {
		return "", bcksrv, fmt.Errorf("parent cluster %s has no logical backup on %s: run a logical backup first", cluster.Name, bcksrv.URL)
	}
	var dest string
	switch cluster.Conf.BackupLogicalType {
	case config.ConstBackupLogicalTypeMysqldump, "script":
		dest = "mysqldump.sql.gz"
		// a splitdump parent backs up into a directory (#2011, preprod belair); the
		// single file is the fallback when the directory is not there
		if cluster.Conf.BackupMysqldumpSplitDump {
			if st, err := os.Stat(bcksrv.GetMyBackupDirectory() + "splitdump"); err == nil && st.IsDir() {
				dest = "splitdump"
			}
		}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	default:
		return "", bcksrv, fmt.Errorf("parent cluster %s backup-logical-type %q cannot seed a child cluster (mysqldump or mydumper)", cluster.Name, cluster.Conf.BackupLogicalType)
	}
	backupfile := bcksrv.GetMyBackupDirectory() + dest
	if _, err := os.Stat(backupfile); err != nil {
		return "", bcksrv, fmt.Errorf("parent cluster %s logical backup %s not found: %w", cluster.Name, backupfile, err)
	}
	return backupfile, bcksrv, nil
}

// SeedFromParent copies the parent's data into this staging cluster (the "Bootstrap
// Staging From Parent" action, and the last step of create-staging, #1988): the
// master is reseeded from the parent's logical backup and keeps replicating from the
// parent, the first replica waits to be in sync, then the staging refresh detaches it
// and a logical backup of the staging master is started. Every stop returns its
// reason.
func (cluster *Cluster) SeedFromParent(parent *Cluster) error {
	return cluster.seedFromParent(parent, true)
}

// SeedChildFromParent copies the parent's data into a child cluster (create-child,
// #1997): the same reseed of the master, which keeps replicating from the parent and
// passes the data on to its replicas, every replica in sync, no detach.
func (cluster *Cluster) SeedChildFromParent(parent *Cluster) error {
	return cluster.seedFromParent(parent, false)
}

func (cluster *Cluster) seedFromParent(parent *Cluster, staging bool) error {
	cmaster := cluster.GetMaster()
	if cmaster == nil {
		return fmt.Errorf("no master on %s", cluster.Name)
	}
	masterGTIDList, err := cluster.ReseedFromParentCluster(parent, cmaster, "") // the master combines its GTID list with the parent's
	if err != nil {
		return fmt.Errorf("reseed of %s from %s: %w", cmaster.URL, parent.Name, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed from parent cluster %s done", parent.Name)

	replicas := cluster.GetSlaves()
	if staging {
		// the first replica only: the one the refresh detaches
		slave := cluster.GetSlaveByIndex(0)
		if slave == nil {
			return fmt.Errorf("no replica on %s to detach as the staging server", cluster.Name)
		}
		replicas = []*ServerMonitor{slave}
	}
	for _, slave := range replicas {
		starttime := time.Now()
		for slave.State == stateSlaveLate && time.Since(starttime) < 2*time.Minute {
			time.Sleep(1 * time.Second)
		}
		if slave.State != stateSlave {
			return fmt.Errorf("replica %s is %s after 2 minutes, not in sync", slave.URL, slave.State)
		}
	}
	if staging {
		if err := cluster.RefreshStaging(parent, masterGTIDList); err != nil {
			return fmt.Errorf("refresh staging: %w", err)
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Seed of %s from parent cluster %s done, starting a logical backup", cluster.Name, parent.Name)
	go cmaster.JobBackupLogical(context.Background())
	return nil
}
