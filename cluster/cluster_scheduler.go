package cluster

import (
	"context"
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) GetBackupLogicalFunction() func() {
	return func() {
		cluster.waitForBackupSlot()
		defer cluster.ServerGlobals.ReleaseBackupSlot()

		mysrv := cluster.GetBackupServer()
		if mysrv != nil {
			mysrv.JobBackupLogical(context.Background())
		} else if cluster.GetMaster() != nil {
			cluster.GetMaster().JobBackupLogical(context.Background())
		}
	}
}

func (cluster *Cluster) GetBackupPhysicalFunction() func() {
	return func() {
		cluster.waitForBackupSlot()
		defer cluster.ServerGlobals.ReleaseBackupSlot()

		mysrv := cluster.GetBackupServer()
		if mysrv != nil {
			mysrv.JobBackupPhysical()
		} else if cluster.GetMaster() != nil {
			cluster.GetMaster().JobBackupPhysical()
		}
	}
}

// waitForBackupSlot opens a WARN0174 state while waiting for a global backup
// slot, then clears it once the slot is acquired.
func (cluster *Cluster) waitForBackupSlot() {
	if cluster.ServerGlobals == nil || cluster.ServerGlobals.BackupSemaphore == nil {
		return
	}
	inUse := len(cluster.ServerGlobals.BackupSemaphore)
	total := cap(cluster.ServerGlobals.BackupSemaphore)
	if inUse >= total {
		cluster.SetState("WARN0174", state.State{
			ErrType: "WARNING",
			ErrDesc: fmt.Sprintf(config.ClusterError["WARN0174"], inUse, total),
			ErrFrom: "BACKUP",
		})
	}
	cluster.ServerGlobals.AcquireBackupSlot()
	cluster.StateMachine.DeleteState("WARN0174")
}

func (cluster *Cluster) GetRotateLogsFunction() func() {
	return func() {
		cluster.RotateLogs()
	}
}

func (cluster *Cluster) GetBackupLogsFunction() func() {
	return func() {
		cluster.BackupLogs()
	}
}

func (cluster *Cluster) GetRollingOptimizeFunction() func() {
	return func() {
		cluster.RollingOptimize()
	}
}

func (cluster *Cluster) GetRollingRestartFunction() func() {
	return func() {
		cluster.RollingRestart()
	}
}

func (cluster *Cluster) GetRollingReprovFunction() func() {
	return func() {
		cluster.RollingReprov()
	}
}

func (cluster *Cluster) GetAnalyzeFunction() func() {
	return func() {
		cluster.JobAnalyzeSQL(cluster.Conf.AnalyzeUsePersistent)
	}
}

func (cluster *Cluster) GetSlaRotateFunction() func() {
	return func() {
		cluster.SetEmptySla()
	}
}

func (cluster *Cluster) GetDbJobsSshFunction() func() {
	return func() {
		for _, s := range cluster.Servers {
			if s == nil {
				continue
			}
			go s.JobRunViaSSH()
		}
	}
}

func (cluster *Cluster) GetAlertDisableFunction() func() {
	return func() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Alerting is disabled from scheduler")
		cluster.IsAlertDisable = true
		go cluster.WaitAlertDisable()
	}
}

func (cluster *Cluster) GetMonitorSchemasFunction() func() {
	return func() {
		go cluster.MonitorSchema()
	}
}

func (cluster *Cluster) GetMonitorChecksumFunction() func() {
	return func() {
		go cluster.CheckAllTableChecksum()
	}
}

func scheduleDescription(title string) string {
	switch title {
	case "backuplogical":
		return "Database Logical Backup"
	case "backupphysical":
		return "Database Physical Backup"
	case "logstablerotate":
		return "Database Logs Table Rotate"
	case "errorlogs":
		return "Database Error Logs Backup"
	case "optimize":
		return "Database Optimize"
	case "analyze":
		return "Database Analyze"
	case "rollingrestart":
		return "Rolling Restart"
	case "rollingreprov":
		return "Rolling Reprovisioning"
	case "slarotate":
		return "SLA Rotate"
	case "sshdbjobs":
		return "DB Jobs SSH Execution"
	case "alertdisable":
		return "Alert Disable"
	case "monitor-schemas":
		return "Monitor Schemas"
	default:
		return title
	}
}

func (cluster *Cluster) SetScheduler(switcher bool, title, schedule string, scheduleFunc func()) {
	if cluster.scheduler == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler is disable cancel")
		return
	}

	entry := cluster.GetSchedule(title)
	if entry != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Disable %s ", scheduleDescription(title))
		cluster.scheduler.Remove(entry.ID)
		delete(cluster.Schedule, title)
	}

	if switcher {
		var err error
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Schedule %s time at: %s", scheduleDescription(title), schedule)
		entryID, err := cluster.scheduler.AddFunc(schedule, scheduleFunc)
		if err == nil {
			cluster.Schedule[title] = cluster.scheduler.Entry(entryID)
		}
	}
}

func (cluster *Cluster) SetSchedulerBackupLogical() {
	cluster.SetScheduler(cluster.Conf.SchedulerBackupLogical, "backuplogical", cluster.Conf.BackupLogicalCron, cluster.GetBackupLogicalFunction())
}

func (cluster *Cluster) SetSchedulerBackupPhysical() {
	cluster.SetScheduler(cluster.Conf.SchedulerBackupPhysical, "backupphysical", cluster.Conf.BackupPhysicalCron, cluster.GetBackupPhysicalFunction())
}

func (cluster *Cluster) SetSchedulerLogsTableRotate() {
	cluster.SetScheduler(cluster.Conf.SchedulerDatabaseLogsTableRotate, "logstablerotate", cluster.Conf.SchedulerDatabaseLogsTableRotateCron, cluster.GetRotateLogsFunction())
}

func (cluster *Cluster) SetSchedulerBackupLogs() {
	cluster.SetScheduler(cluster.Conf.SchedulerDatabaseLogs, "backuplogs", cluster.Conf.BackupDatabaseLogCron, cluster.GetBackupLogsFunction())
}

func (cluster *Cluster) SetSchedulerOptimize() {
	cluster.SetScheduler(cluster.Conf.SchedulerDatabaseOptimize, "optimize", cluster.Conf.BackupDatabaseOptimizeCron, cluster.GetRollingOptimizeFunction())
}

func (cluster *Cluster) SetSchedulerAnalyze() {
	cluster.SetScheduler(cluster.Conf.SchedulerDatabaseAnalyze, "analyze", cluster.Conf.BackupDatabaseAnalyzeCron, cluster.GetAnalyzeFunction())
}

func (cluster *Cluster) SetSchedulerRollingRestart() {
	cluster.SetScheduler(cluster.Conf.SchedulerRollingRestart, "rollingrestart", cluster.Conf.SchedulerRollingRestartCron, cluster.GetRollingRestartFunction())
}

func (cluster *Cluster) SetSchedulerRollingReprov() {
	cluster.SetScheduler(cluster.Conf.SchedulerRollingReprov, "rollingreprov", cluster.Conf.SchedulerRollingReprovCron, cluster.GetRollingReprovFunction())
}

func (cluster *Cluster) SetSchedulerSlaRotate() {
	cluster.SetScheduler(true, "slarotate", cluster.Conf.SchedulerSLARotateCron, cluster.GetSlaRotateFunction())
}

func (cluster *Cluster) SetSchedulerDbJobsSsh() {
	cluster.SetScheduler(cluster.Conf.SchedulerJobsSSH, "sshdbjobs", cluster.Conf.SchedulerJobsSSHCron, cluster.GetDbJobsSshFunction())
}

func (cluster *Cluster) SetSchedulerAlertDisable() {
	cluster.SetScheduler(cluster.Conf.SchedulerAlertDisable, "alertdisable", cluster.Conf.SchedulerAlertDisableCron, cluster.GetAlertDisableFunction())
}

func (cluster *Cluster) SetSchedulerMonitorSchema() {
	cluster.SetScheduler(cluster.Conf.MonitorSchemaScheduler, "monitorschema", cluster.Conf.MonitorSchemaSchedulerCron, cluster.GetMonitorSchemasFunction())
}

func (cluster *Cluster) SetSchedulerMonitorChecksum() {
	cluster.SetScheduler(cluster.Conf.MonitorChecksumScheduler, "monitorchecksum", cluster.Conf.MonitorChecksumSchedulerCron, cluster.GetMonitorChecksumFunction())
}
