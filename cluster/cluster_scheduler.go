package cluster

import (
	"context"

	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) GetBackupLogicalFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
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
		if !cluster.IsActive() {
			return
		}
		mysrv := cluster.GetBackupServer()
		if mysrv != nil {
			mysrv.JobBackupPhysical()
		} else if cluster.GetMaster() != nil {
			cluster.GetMaster().JobBackupPhysical()
		}
	}
}

func (cluster *Cluster) GetRotateLogsFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.RotateLogs()
	}
}

func (cluster *Cluster) GetBackupLogsFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.BackupLogs()
	}
}

func (cluster *Cluster) GetRollingOptimizeFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.RollingOptimize()
	}
}

func (cluster *Cluster) GetRollingRestartFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.RollingRestart()
	}
}

func (cluster *Cluster) GetRollingReprovFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.RollingReprov()
	}
}

func (cluster *Cluster) GetAnalyzeFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
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
		if !cluster.IsActive() {
			return
		}
		for _, s := range cluster.Servers {
			if s == nil {
				continue
			}
			go s.JobRunViaSSH()
		}
	}
}

// GetRotatePasswordsFunction is the scheduled password rotation (#2269): the same rotation as
// the button, on the active monitor only.
func (cluster *Cluster) GetRotatePasswordsFunction() func() {
	return func() {
		if !cluster.IsActive() {
			return
		}
		cluster.ScheduledPasswordRotation()
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
		if !cluster.IsActive() {
			return
		}
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
	case "rotatepasswords":
		return "Password Rotation"
	case "monitor-schemas":
		return "Monitor Schemas"
	case "archivepurge":
		return "Archive Purge"
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

func (cluster *Cluster) SetSchedulerRotatePasswords() {
	cluster.SetScheduler(cluster.Conf.SchedulerRotatePasswords, "rotatepasswords", cluster.Conf.SchedulerRotatePasswordsCron, cluster.GetRotatePasswordsFunction())
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

// SetSchedulerArchivePurge: with backup-archive-purge-trigger schedule, the attached archive
// is asked to apply the retention policy at scheduler-archive-purge-cron (#2091).
func (cluster *Cluster) SetSchedulerArchivePurge() {
	cluster.SetScheduler(cluster.Conf.ArchivePurgeOnSchedule(), "archivepurge", cluster.Conf.BackupArchivePurgeCron, func() { cluster.ResticPurgeRepo(false) })
}
