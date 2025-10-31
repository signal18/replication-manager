package cluster

import (
	"fmt"
	"os/exec"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func (cluster *Cluster) RefreshToolVersions() {
	if err := cluster.RefreshDBClientVersion(); err != nil {
		cluster.SetState("WARN0117", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0117"], err), ErrFrom: "CLUSTER"})
	}

	if err := cluster.RefreshMysqlDumpVersion(); err != nil {
		cluster.SetState("WARN0118", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0118"], err), ErrFrom: "CLUSTER"})
	}

	if err := cluster.RefreshMysqlBinlogVersion(); err != nil {
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

	vstring := string(out)

	v, _, _ := version.NewFullVersionFromString(version.ParseDBFlavor(vstring), vstring)
	if v == nil {
		return fmt.Errorf("unable to parse database client version from string: %s", vstring)
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

func (cluster *Cluster) SetMysqlDumpVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("client-dump", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0118")

	return nil
}

func (cluster *Cluster) RefreshMysqlDumpVersion() error {
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

	vstring := string(out)

	flavor := version.ParseDBFlavor(vstring)

	// Mysqldump should be consistent with client since it's distributed together
	v, _, _ := version.NewFullVersionFromString(flavor, vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetMysqlDumpVersion(v)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Dump client version %s: %s", cstring, v.ToFullString())
		return nil
	}

	return nil
}

func (cluster *Cluster) SetMysqlBinlogVersion(v *version.Version) error {
	if v == nil {
		return fmt.Errorf("nil version provided")
	}

	cluster.VersionsMap.Set("client-binlog", v)
	// Remove state if already get correct version
	cluster.GetStateMachine().DeleteState("WARN0119")

	return nil
}

func (cluster *Cluster) RefreshMysqlBinlogVersion() error {
	cstring := "changed"
	oldV, _ := cluster.GetToolsVersion("client-binlog")
	if oldV == nil {
		cstring = "discovered"
	}
	// Return if mysqldump not found
	out, err := exec.Command(cluster.GetMysqlBinlogPath(), "--version").Output()
	if err != nil {
		return err
	}

	vstring := string(out)

	flavor := version.ParseDBFlavor(vstring)

	// Mysqlbinlog should be consistent with client since it's distributed together
	v, _, _ := version.NewFullVersionFromString(flavor, vstring)

	hasChanged, err := version.HasVersionChanged(oldV, v)
	if err != nil {
		return err
	}

	if hasChanged {
		err := cluster.SetMysqlBinlogVersion(v)
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

	v, _ := version.NewVersionFromString("mydumper", string(out))

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
	// Return if mydumper not found
	out, err := exec.Command(cluster.Conf.BackupResticBinaryPath, "version").Output()
	if err != nil {
		return err
	}

	v, _ := version.NewVersionFromString("restic", string(out))

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
