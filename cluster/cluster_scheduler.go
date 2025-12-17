package cluster

import "github.com/signal18/replication-manager/config"

func (cluster *Cluster) GetBackupLogicalFunction() func() {
	return func() {
		mysrv := cluster.GetBackupServer()
		if mysrv != nil {
			mysrv.JobBackupLogical()
		} else if cluster.GetMaster() != nil {
			cluster.GetMaster().JobBackupLogical()
		}
	}
}

func (cluster *Cluster) GetBackupPhysicalFunction() func() {
	return func() {
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
			s.JobRunViaSSH()
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
