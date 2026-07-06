package cluster

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func (cluster *Cluster) RefreshToolVersions() {
	if err := cluster.RefreshDBClientVersion(); err != nil {
		cluster.SetState("WARN0117", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0117"], err), ErrFrom: "CLUSTER"})
	}

	if err := cluster.RefreshDBClientDumpVersion(); err != nil {
		cluster.SetState("WARN0118", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0118"], err), ErrFrom: "CLUSTER"})
	}

	if err := cluster.RefreshDBClientBinlogVersion(); err != nil {
		cluster.SetState("WARN0119", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0119"], err), ErrFrom: "CLUSTER"})
	}

	if err := cluster.RefreshMyDumperVersion(); err != nil {
		cluster.SetState("WARN0120", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0120"], err), ErrFrom: "CLUSTER"})
	}

	if cluster.shouldRefreshSysbenchVersionPeriodically() {
		if err := cluster.RefreshSysbenchVersion(); err != nil {
			cluster.SetState("WARN0167", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0167"], err), ErrFrom: "CLUSTER"})
		}
	}

	if cluster.Conf.BackupRestic {
		if err := cluster.RefreshResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		}
	}
}

// CheckComplianceUpdate checks if new compliance files are available in
// PluginDataDir or if the embedded module changed (binary upgrade).
// Always raises WARN0168 when a change is detected so it's visible in logs.
// When prov-trust-compliance-changes is true (default), auto-accepts
// immediately after, which clears the warning.
func (cluster *Cluster) CheckComplianceUpdate() {
	pluginDataDir := cluster.Conf.ShareDir + "/plugins/data"
	pending := cluster.Configurator.CheckComplianceUpdate(pluginDataDir)
	if !pending {
		cluster.ConfigStateMachine.DeleteState("WARN0168")
		return
	}

	// Always raise the warning so the change is visible in cluster alerts/logs.
	desc := fmt.Sprintf(clusterError["WARN0168"],
		cluster.Configurator.ActiveDBCRC, cluster.Configurator.PendingDBCRC,
		cluster.Configurator.ActivePrxCRC, cluster.Configurator.PendingPrxCRC)
	cluster.ConfigStateMachine.AddState("WARN0168", state.State{
		ErrType: "WARNING",
		ErrKey:  "WARN0168",
		ErrDesc: desc,
		ErrFrom: "CLUSTER",
	})

	if cluster.Conf.ProvAutoUpdateCompliance {
		// Auto-accept — clears WARN0168 immediately.
		if err := cluster.AcceptComplianceUpdate(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Auto-accept compliance update failed: %s", err)
		}
	}
}

// AcceptComplianceUpdate loads the pending compliance from PluginDataDir,
// replaces the active modules, clears the pending state and WARN0168.
func (cluster *Cluster) AcceptComplianceUpdate() error {
	pluginDataDir := cluster.Conf.ShareDir + "/plugins/data"
	if err := cluster.Configurator.AcceptComplianceUpdate(pluginDataDir); err != nil {
		return err
	}
	cluster.ConfigStateMachine.DeleteState("WARN0168")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Compliance update accepted — configurator reloaded with new modules (DB CRC: %08x, Proxy CRC: %08x)",
		cluster.Configurator.ActiveDBCRC, cluster.Configurator.ActivePrxCRC)

	cluster.SetConfigRefreshCookie() // Flag for config refresh
	return nil
}

// ReloadDockerRepos checks if a BO-pushed repos.json exists in PluginDataDir
// and reloads the docker image tag list. Called periodically from the monitoring loop.
// ReloadDockerRepos checks if a BO-pushed repos.json exists in PluginDataDir
// and updates the cluster's DockerRepos list. The server-level ServiceRepos
// is updated by the caller if needed.
func (cluster *Cluster) ReloadDockerRepos() {
	repos, err := cluster.Conf.GetDockerRepos("", false)
	if err != nil {
		return
	}
	if len(repos) > 0 && len(repos) != len(cluster.DockerRepos) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Docker image repos updated: %d repos loaded from back office plugins/data/repos.json", len(repos))
	}
	if len(repos) > 0 {
		cluster.DockerRepos = repos
	}
}

func (cluster *Cluster) SetDBClientVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("client", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0117")

	return nil
}

func (cluster *Cluster) RefreshDBClientVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("client")
	if oldV == nil {
		cstring = "discovered"
	}

	// Return if mysql client not found
	out, err := exec.Command(cluster.GetMysqlclientPath(), "--version").Output()
	if err != nil {
		return err
	}

	// Clean up the output to remove paths and extract version info
	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "DB client raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "DB client cleaned version string: %s", vstring)

	v, _, _ := version.NewFullVersionFromString(version.ParseDBFlavor(vstring), vstring)
	if v == nil {
		return fmt.Errorf("unable to parse database client version from string: %s (raw: %s)", vstring, rawOutput)
	}

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetDBClientVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "DB client version %s: %s", cstring, v.ToFullString())
		return nil
	}

	return nil
}

func (cluster *Cluster) SetDBClientDumpVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("client-dump", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0118")

	return nil
}

func (cluster *Cluster) RefreshDBClientDumpVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("client-dump")
	if oldV == nil {
		cstring = "discovered"
	}

	// Return if mysqldump not found
	out, err := exec.Command(cluster.GetMysqlDumpPath(), "--version").Output()
	if err != nil {
		return err
	}

	// Clean up the output to remove paths and extract version info
	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Dump client raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Dump client cleaned version string: %s", vstring)

	flavor := version.ParseDBFlavor(vstring)

	// Mysqldump should be consistent with client since it's distributed together
	v, _, _ := version.NewFullVersionFromString(flavor, vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetDBClientDumpVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Dump client version %s: %s", cstring, v.ToFullString())
		return nil
	}

	return nil
}

func (cluster *Cluster) SetDBClientBinlogVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("client-binlog", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0119")

	return nil
}

func (cluster *Cluster) RefreshDBClientBinlogVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("client-binlog")
	if oldV == nil {
		cstring = "discovered"
	}
	// Return if mysqlbinlog not found
	out, err := exec.Command(cluster.GetMysqlBinlogPath(), "--version").Output()
	if err != nil {
		return err
	}

	// Clean up the output to remove paths and extract version info
	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Binlog client raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Binlog client cleaned version string: %s", vstring)

	flavor := version.ParseDBFlavor(vstring)

	// Mysqlbinlog should be consistent with client since it's distributed together
	v, _, _ := version.NewFullVersionFromString(flavor, vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetDBClientBinlogVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Binlog client version %s: %s", cstring, v.ToFullString())
		return nil
	}

	return nil
}

func (cluster *Cluster) SetMyDumperVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("mydumper", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0120")

	return nil
}

func (cluster *Cluster) RefreshMyDumperVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("mydumper")
	if oldV == nil {
		cstring = "discovered"
	}
	// Return if mydumper not found
	out, err := exec.Command(cluster.GetMyDumperPath(), "--version").Output()
	if err != nil {
		return err
	}

	// Clean up the output to remove paths and extract version info
	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "MyDumper raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "MyDumper cleaned version string: %s", vstring)

	v, _ := version.NewVersionFromString("mydumper", vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetMyDumperVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "MyDumper version %s: %s", cstring, v.ToFullString())
	}

	return nil
}

func (cluster *Cluster) SetSysbenchVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}
	cluster.VersionsMap.Set("sysbench", v)
	cluster.GetStateMachine().DeleteState("WARN0167")

	return nil
}

func (cluster *Cluster) shouldRefreshSysbenchVersionPeriodically() bool {
	path := strings.TrimSpace(cluster.Conf.SysbenchBinaryPath)

	if path != "" && path != "/usr/bin/sysbench" {
		return true
	}

	if cluster.Conf.Test || cluster.Conf.TestInjectTraffic || cluster.Conf.TestInjectTrafficStaging {
		return true
	}

	return false
}

func (cluster *Cluster) RefreshSysbenchVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("sysbench")
	if oldV == nil {
		cstring = "discovered"
	}

	out, err := exec.Command(cluster.Conf.SysbenchBinaryPath, "--version").CombinedOutput()
	if err != nil {
		return err
	}

	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Sysbench raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Sysbench cleaned version string: %s", vstring)

	v, tokens := version.NewVersionFromString("sysbench", vstring)
	if tokens == 0 {
		return fmt.Errorf("unable to parse sysbench version from string: %s (raw: %s)", vstring, rawOutput)
	}

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		if err := cluster.SetSysbenchVersion(v); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sysbench version %s: %s", cstring, v.ToFullString())
	}

	return nil
}

func (cluster *Cluster) SetResticVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}
	cluster.VersionsMap.Set("restic", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0121")

	return nil
}

func (cluster *Cluster) RefreshResticVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("restic")
	if oldV == nil {
		cstring = "discovered"
	}
	// Return if restic not found
	out, err := exec.Command(cluster.Conf.BackupResticBinaryPath, "version").Output()
	if err != nil {
		return err
	}

	// Clean up the output to remove paths and extract version info
	rawOutput := string(out)
	vstring := version.ExtractVersionFromOutput(rawOutput)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Restic raw output: %s", rawOutput)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Restic cleaned version string: %s", vstring)

	v, _ := version.NewVersionFromString("restic", vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetResticVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic version %s: %s", cstring, v.ToFullString())
	}

	return nil
}
