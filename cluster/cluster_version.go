package cluster

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// extractVersionFromOutput extracts the version string from command output,
// removing paths and other noise that might confuse version parsing.
// It looks for common version markers like "Ver", "version", "from", etc.
func extractVersionFromOutput(output string) string {
	// Remove leading/trailing whitespace and newlines
	output = strings.TrimSpace(output)

	// For outputs with multiple lines, take only the first line
	if lines := strings.Split(output, "\n"); len(lines) > 0 {
		output = lines[0]
		output = strings.TrimSpace(output)
	}

	// Remove any file paths from the beginning (e.g., /usr/bin/mysql Ver 8.0.28...)
	// Look for patterns like /path/to/binary and remove them
	pathRegex := regexp.MustCompile(`^[/\w\-\.]+/[\w\-]+\s+`)
	output = pathRegex.ReplaceAllString(output, "")

	// Look for version markers and extract from that point onwards
	// These markers indicate where the actual version information starts
	versionMarkers := []struct {
		marker      string
		stripMarker bool
	}{
		{"Ver ", true},
		{"version ", true},
		{"from ", true},
		{"v", true},
	}

	for _, m := range versionMarkers {
		if idx := strings.Index(output, m.marker); idx != -1 {
			// Extract from the marker onwards
			if m.stripMarker {
				output = output[idx+len(m.marker):]
			} else {
				output = output[idx:]
			}
			break
		}
	}

	// Remove common tool names from the beginning if they appear without version markers
	// (e.g., "mydumper 0.11.5" -> "0.11.5", "restic 0.15.0" -> "0.15.0")
	toolNames := []string{"mydumper ", "restic ", "mysql ", "mysqldump ", "mysqlbinlog "}
	for _, tool := range toolNames {
		if strings.HasPrefix(output, tool) {
			output = strings.TrimPrefix(output, tool)
			break
		}
	}

	return strings.TrimSpace(output)
}

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

	if cluster.Conf.BackupRestic {
		if err := cluster.RefreshResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		}
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
	vstring := extractVersionFromOutput(rawOutput)

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
	vstring := extractVersionFromOutput(rawOutput)

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
	vstring := extractVersionFromOutput(rawOutput)

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
	vstring := extractVersionFromOutput(rawOutput)

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
	vstring := extractVersionFromOutput(rawOutput)

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
