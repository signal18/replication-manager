// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) BashScriptAlert(alert alert.Alert) error {
	if cluster.Conf.AlertScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling alert script")
		var out []byte
		out, err := exec.Command(cluster.Conf.AlertScript, alert.Cluster, alert.Host, alert.PrevState, alert.State).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Alert script complete: %s", string(out))
	}
	return nil
}

// Cloud18ManagedSuffix returns the exact FQDN suffix that marks a hostname as
// managed by this cluster (e.g. ".mycluster.ovh-fr-2.signal18.cloud18.io").
// Returns "" when the cloud18 integration is not configured.
func (cluster *Cluster) Cloud18ManagedSuffix() string {
	if cluster.Conf.Cloud18Domain == "" || cluster.Conf.Cloud18SubDomain == "" {
		return ""
	}
	subdomain := cluster.Conf.Cloud18SubDomain
	if cluster.Conf.Cloud18SubDomainZone != "" {
		subdomain = subdomain + "-" + cluster.Conf.Cloud18SubDomainZone
	}
	return "." + cluster.Name + "." + subdomain + "." + cluster.Conf.Cloud18Domain + ".cloud18.io"
}

// ManagedHostCNAME returns the short-form label (everything except the trailing
// ".cloud18.io") and true when fullCname ends with the exact configured managed
// application suffix for this cluster.  The short form is what the DNS provider
// scripts expect; any provider-specific conversion happens inside the scripts.
func (cluster *Cluster) ManagedHostCNAME(fullCname string) (shortCname string, managed bool) {
	suffix := cluster.Cloud18ManagedSuffix()
	if suffix == "" {
		return "", false
	}
	lower := strings.ToLower(strings.TrimRight(fullCname, "."))
	if !strings.HasSuffix(lower, strings.ToLower(suffix)) {
		return "", false
	}
	// Strip the last two components (.cloud18.io) — that is the label the
	// DNS provider scripts expect as their third argument.
	slice := strings.Split(lower, ".")
	if len(slice) <= 2 {
		return "", false
	}
	return strings.Join(slice[:len(slice)-2], "."), true
}

func (cluster *Cluster) BashScriptProvDNS(cname string) error {
	if cluster.Conf.Cloud18GatewayDomainName == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", "Empty gateway for cloud18-gateway-domain-name")
		return errors.New("Empty gateway for cloud18-gateway-domain-name")
	}
	if cluster.Conf.Cloud18DomainAddScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling provision add domain script")
		var out []byte
		out, err := exec.Command(cluster.Conf.Cloud18DomainAddScript, cluster.Conf.Cloud18DomainUser, cluster.Conf.GetDecryptedValue("cloud18-domain-secret"), cname, cluster.Conf.Cloud18GatewayDomainName).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
			return fmt.Errorf("domain add script failed for %s: %w", cname, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling provision add domain script: %s", string(out))
	}
	return nil
}

// BashScriptDeprovDNS calls the cloud18-domain-drop-script to remove a managed
// CNAME entry that is no longer needed (route dropped or renamed).
func (cluster *Cluster) BashScriptDeprovDNS(cname string) error {
	if cluster.Conf.Cloud18GatewayDomainName == "" {
		return errors.New("empty gateway for cloud18-gateway-domain-name")
	}
	if cluster.Conf.Cloud18DomainDropScript == "" {
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling provision drop domain script for %s", cname)
	out, err := exec.Command(cluster.Conf.Cloud18DomainDropScript, cluster.Conf.Cloud18DomainUser, cluster.Conf.GetDecryptedValue("cloud18-domain-secret"), cname, cluster.Conf.Cloud18GatewayDomainName).CombinedOutput()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		return fmt.Errorf("domain drop script failed for %s: %w", cname, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Provision drop domain script complete: %s", string(out))
	return nil
}

// BashScriptSchemaChange calls the monitoring-schema-change-script when a table
// change is detected. The diff of column definitions is piped to stdin.
// Args: $1=cluster $2=server_url $3=schema $4=table $5=change_type(new/altered/dropped)
// The script runs with a 30-second timeout to avoid stalling the monitoring loop.
func (cluster *Cluster) BashScriptSchemaChange(serverURL, schema, table, changeType string, oldCols, newCols []dbhelper.Column) error {
	if cluster.Conf.MonitorSchemaChangeScript == "" {
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Calling schema change script for %s.%s (%s) on %s", schema, table, changeType, serverURL)

	diff := columnDiff(schema, table, oldCols, newCols)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cluster.Conf.MonitorSchemaChangeScript, cluster.Name, serverURL, schema, table, changeType)
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Schema change script timed out after 30s for %s.%s", schema, table)
		return ctx.Err()
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Schema change script error: %s", err)
	}
	if len(out) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Schema change script output: %s", string(out))
	}
	return nil
}

// BashScriptVariableChange calls the monitoring-variable-change-script when
// server variables change over time. The diff is piped to stdin.
// Args: $1=cluster $2=server_url
// Runs with a 30-second timeout to avoid stalling the monitoring loop.
func (cluster *Cluster) BashScriptVariableChange(serverURL, diff string) error {
	if cluster.Conf.MonitorVariableChangeScript == "" {
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Calling variable change script for %s", serverURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cluster.Conf.MonitorVariableChangeScript, cluster.Name, serverURL)
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Variable change script timed out after 30s for %s", serverURL)
		return ctx.Err()
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Variable change script error for %s: %s %s", serverURL, err, string(out))
		return err
	}
	if len(out) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Variable change script output: %s", string(out))
	}
	return nil
}

// columnDiff produces a unified-style diff of column definitions between old and new.
func columnDiff(schema, table string, oldCols, newCols []dbhelper.Column) string {
	fqn := schema + "." + table
	var b strings.Builder
	b.WriteString("--- " + fqn + " (before)\n")
	b.WriteString("+++ " + fqn + " (after)\n")

	oldMap := make(map[string]dbhelper.Column)
	for _, c := range oldCols {
		oldMap[c.Name] = c
	}
	newMap := make(map[string]dbhelper.Column)
	for _, c := range newCols {
		newMap[c.Name] = c
	}

	for _, c := range oldCols {
		if _, exists := newMap[c.Name]; !exists {
			b.WriteString("- " + formatColumn(c) + "\n")
		}
	}

	for _, c := range newCols {
		old, exists := oldMap[c.Name]
		if !exists {
			b.WriteString("+ " + formatColumn(c) + "\n")
		} else if formatColumn(old) != formatColumn(c) {
			b.WriteString("- " + formatColumn(old) + "\n")
			b.WriteString("+ " + formatColumn(c) + "\n")
		}
	}

	return b.String()
}

func formatColumn(c dbhelper.Column) string {
	s := c.Name + " " + c.Type
	if !c.Nullable {
		s += " NOT NULL"
	}
	if c.Default != nil {
		s += " DEFAULT " + *c.Default
	}
	if c.Extra != "" {
		s += " " + c.Extra
	}
	return s
}
func (cluster *Cluster) BashScriptOpenSate(state state.State) error {
	if cluster.Conf.MonitoringOpenStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling open state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.MonitoringOpenStateScript, cluster.Name, state.ServerUrl, state.ErrKey).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Open state script complete: %s", string(out))
	}
	return nil
}
func (cluster *Cluster) BashScriptCloseSate(state state.State) error {
	if cluster.Conf.MonitoringCloseStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling close state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.MonitoringCloseStateScript, cluster.Name, state.ServerUrl, state.ErrKey).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Close state script complete %s:", string(out))
	}
	return nil
}

func (cluster *Cluster) BashScriptDbServersChangeState(srv *ServerMonitor, newState string, oldState string) error {
	if cluster.IsRefreshStaging && cluster.IsNeedStagingChange && newState == stateUnconn && srv != cluster.StagingServer {

		if cluster.Conf.TopologyStagingPostDetachScript != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling staging post detach script")
			cluster.PostDetachStaging(srv.Host, srv.Port, newState, oldState)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No staging post detach script. Using default")
			if cluster.StagingServer != nil {
				cluster.StagingServer.SetReadOnly() // Set the old staging server to read only
			}

			cluster.SetStagingServer(srv)        // Set the new staging server as the new staging server
			cluster.StagingServer.SetReadWrite() // Set the new staging server to read write for proxysql read-only checks
		}

		cluster.IsNeedStagingChange = false // Reset the flag to avoid multiple calls
	}

	if cluster.Conf.DbServersChangeStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling database change state script")
		var out []byte
		out, err := exec.Command(cluster.Conf.DbServersChangeStateScript, cluster.Name, srv.Host, srv.Port, newState, oldState).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Database change state script %s:", string(out))
	}
	return nil
}

func (cluster *Cluster) BashScriptPrxServersChangeState(srv DatabaseProxy, newState string, oldState string) error {
	if cluster.Conf.PRXServersChangeStateScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling proxy change state script")
		var out []byte
		master := cluster.GetMaster()
		if master == nil {
			return errors.New("No leader found in bash script Proxy Servers Change State ")
		}
		out, err := exec.Command(cluster.Conf.PRXServersChangeStateScript, cluster.Name, srv.GetHost(), srv.GetPort(), newState, oldState, master.State).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Proxy change state script %s:", string(out))
	}
	return nil
}

func (cluster *Cluster) failoverPostScript(fail bool) {
	if cluster.Conf.PostScript != "" {

		var out []byte
		var err error

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling post-failover script")
		failtype := "failover"
		if !fail {
			failtype = "switchover"
		}
		out, err = exec.Command(cluster.Conf.PostScript, cluster.oldMaster.Host, cluster.GetMaster().Host, cluster.oldMaster.Port, cluster.GetMaster().Port, cluster.oldMaster.MxsServerName, cluster.GetMaster().MxsServerName, failtype).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Post-failover script complete %s", string(out))
	}
}

func (cluster *Cluster) failoverPreScript(fail bool) {
	// Call pre-failover script
	if cluster.Conf.PreScript != "" {
		failtype := "failover"
		if !fail {
			failtype = "switchover"
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling pre-failover script")
		var out []byte
		var err error
		out, err = exec.Command(cluster.Conf.PreScript, cluster.oldMaster.Host, cluster.GetMaster().Host, cluster.oldMaster.Port, cluster.GetMaster().Port, cluster.oldMaster.MxsServerName, cluster.GetMaster().MxsServerName, failtype).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pre-failover script complete:", string(out))
	}
}

func (cluster *Cluster) BinlogRotationScript(srv *ServerMonitor) error {
	if cluster.Conf.BinlogRotationScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling binlog rotation script")
		var out []byte
		out, err := exec.Command(cluster.Conf.BinlogRotationScript, cluster.Name, srv.Host, srv.Port, srv.BinaryLogFile, srv.BinaryLogFilePrevious, srv.BinaryLogFileOldest).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Binlog rotation script complete: %s", string(out))
	}
	return nil
}

func (cluster *Cluster) BinlogCopyScript(server *ServerMonitor, binlog string, isPurge bool) error {
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
		if cluster.IsInBackup() {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)
			return cluster.BinlogCopyScript(server, binlog, isPurge)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlog)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	if cluster.Conf.BinlogCopyScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Calling binlog copy script on %s. Binlog: %s", server.URL, binlog)
		var out []byte
		out, err := exec.Command(cluster.Conf.BinlogCopyScript, cluster.Name, server.Host, server.Port, strconv.Itoa(cluster.Conf.OnPremiseSSHPort), server.GetBinaryLogDir(), server.GetMyBackupDirectory(), binlog).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		} else {
			// Skip backup to restic if in purge binlog
			if !isPurge {
				if idx := slices.Index(server.BinaryLogMetaToWrite, binlog); idx == -1 {
					server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlog)
				}
				server.WriteBackupBinlogMetadata()
				// Backup to restic when no error (defer to prevent unfinished physical copy)
				backtype := "binlog"
				defer server.BackupRestic(backupmgr.BackupMethodLogical, false, server.GetMyBackupDirectory(), server.BuildResticTags(backtype, "", backupmgr.BackupLineDefault, nil)...)
			}
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Binlog copy script complete: %s", string(out))
	}
	return nil
}

func (cluster *Cluster) BackupPostScript(server *ServerMonitor, backtype backupmgr.BackupMethod, filepath string) error {
	switch backtype {
	case backupmgr.BackupMethodLogical:
		if cluster.Conf.BackupLogicalPostScript != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling backup logical post script at %s", cluster.Conf.BackupLogicalPostScript)
			var out []byte
			out, err := exec.Command(cluster.Conf.BackupLogicalPostScript, cluster.Name, server.Host, server.Port, filepath).CombinedOutput()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
				return err
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup logical post script done: %s", string(out))
		}
	case backupmgr.BackupMethodPhysical:
		if cluster.Conf.BackupPhysicalPostScript != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling backup physical post script at %s", cluster.Conf.BackupPhysicalPostScript)
			var out []byte
			out, err := exec.Command(cluster.Conf.BackupPhysicalPostScript, cluster.Name, server.Host, server.Port, filepath).CombinedOutput()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
				return err
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup physical post script done: %s", string(out))
		}
	}
	return nil
}
