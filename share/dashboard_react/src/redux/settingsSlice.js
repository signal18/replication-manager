import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { settingsService } from '../services/settingsService'
import { getClusterData } from './clusterSlice'

export const switchSetting = createAsyncThunk('cluster/switchSetting', async ({ clusterName, setting }, thunkAPI) => {
  try {
    const { data, status } = await settingsService.switchSettings(clusterName, setting)
    showSuccessBanner(`Switching ${setting} successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Switching ${setting} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const changeTopology = createAsyncThunk(
  'cluster/changeTopology',
  async ({ clusterName, topology }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.changeTopology(clusterName, topology)
      showSuccessBanner(`Topology changed to ${topology} successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Changing topology to ${setting} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setSetting = createAsyncThunk('cluster/setSetting', async ({ clusterName, setting, value }, thunkAPI) => {
  try {
    const { data, status } = await settingsService.setSetting(clusterName, setting, value)
    showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Changing ${setting} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

const initialState = {
  failoverLoading: false,
  targetTopologyLoading: false,
  allowUnsafeClusterLoading: false,
  allowMultitierSlaveLoading: false,
  testLoading: false,
  monSaveConfigLoading: false,
  monPauseLoading: false,
  monCaptureLoading: false,
  monSchemaChangeLoading: false,
  monInnoDBLoading: false,
  monVarDiffLoading: false,
  monProcessListLoading: false,
  captureTriggerLoading: false,
  monIgnoreErrLoading: false,
  verboseLoading: false,
  logSqlInMonLoading: false,
  logLevelLoading: false,
  logSysLogLoading: false,
  logTaskLoading: false,
  logWriterEleLoading: false,
  logSSTLoading: false,
  logheartbeatLoading: false,
  logConfigLoadLoading: false,
  logGitLoading: false,
  logBackupStrmLoading: false,
  logOrcheLoading: false,
  logVaultLoading: false,
  logTopologyLoading: false,
  logGraphiteLoading: false,
  logBinlogLoading: false,
  logProxyLoading: false,
  logHAProxyLoading: false,
  logProxySqlLoading: false,
  logProxyJanitorLoading: false,
  logMaxscaleLoading: false,
  failoverLimitLoading: false,
  replicationStateLoading: false,
  failoverAtSyncLoading: false,
  failoverRestartUnsfLoading: false,
  forceSlaveNoGtidModeLoading: false,
  autorejoinLoading: false,
  delayStatCaptLoading: false,
  failoverCheckDelayStatLoading: false,
  delayStatRotateLoading: false,
  printDelayStatLoading: false,
  printDelayStatHistLoading: false,
  printDelayStatInvlLoading: false,
  switchoverAtSyncLoading: false,
  failoverMaxSlaveDelayLoading: false,
  switchoverRouteChngLoading: false,
  switchoverLowerRlsLoading: false,
  forceSlaveReadonlyLoading: false,
  forceBinlogRowLoading: false,
  forceBinlogAnnoLoading: false,
  forceBinlogCompLoading: false,
  forceBinlogSlowquryLoading: false,
  forceSlaveGtidMdLoading: false,
  forceSlaveGtidMdStctLoading: false,
  forceSlaveSemisyncLoading: false,
  forceSlaveStrictLoading: false,
  forceSlaveIdemLoading: false,
  forceSlaveSerializeLoading: false,
  forceSlaveMinimalLoading: false,
  forceSlaveConservLoading: false,
  forceSlaveOptiLoading: false,
  forceSlaveAggrLoading: false,
  forceSlaveHrtbtLoading: false,
  arLoading: false,
  arBackupBinlogLoading: false,
  arFlashbackOnSyncLoading: false,
  arFlashbackLoading: false,
  arMysqldumpLoading: false,
  arLogicalBackupLoading: false,
  arPhysicalBackupLoading: false,
  arForceRestoreLoading: false,
  autoseedLoading: false
}

export const settingsSlice = createSlice({
  name: 'settings',
  initialState,
  reducers: {
    clearSettings: (state, action) => {
      Object.assign(state, initialState)
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(switchSetting.pending, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'failover-mode') state.failoverLoading = true
        // if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = true
        // if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = true
        // if (setting === 'test') state.testLoading = true
        // if (setting === 'monitoring-save-config') state.monSaveConfigLoading = true
        // if (setting === 'monitoring-pause') state.monPauseLoading = true
        // if (setting === 'monitoring-capture') state.monCaptureLoading = true
        // if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = true
        // if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = true
        // if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = true
        // if (setting === 'monitoring-processlist') state.monProcessListLoading = true
        // if (setting === 'check-replication-state') state.replicationStateLoading = true
        // if (setting === 'failover-at-sync') state.failoverAtSyncLoading = true
        // if (setting === 'failover-restart-unsafe') state.failoverRestartUnsfLoading = true
        // if (setting === 'force-slave-no-gtid-mode') state.forceSlaveNoGtidModeLoading = true
        // if (setting === 'autorejoin-slave-positional-heartbeat') state.autorejoinLoading = true
        // if (setting === 'delay-stat-capture') state.delayStatCaptLoading = true
        // if (setting === 'failover-check-delay-stat') state.failoverCheckDelayStatLoading = true
        // if (setting === 'print-delay-stat') state.printDelayStatLoading = true
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = true
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = true
        // if (setting === 'autorejoin') state.arLoading = true
        // if (setting === 'autorejoin-backup-binlog') state.arBackupBinlogLoading = true
        // if (setting === 'autorejoin-flashback-on-sync') state.arFlashbackOnSyncLoading = true
        // if (setting === 'autorejoin-flashback') state.arFlashbackLoading = true
        // if (setting === 'autorejoin-mysqldump') state.arMysqldumpLoading = true
        // if (setting === 'autorejoin-logical-backup') state.arLogicalBackupLoading = true
        // if (setting === 'autorejoin-physical-backup') state.arPhysicalBackupLoading = true
        // if (setting === 'autorejoin-force-restore') state.arForceRestoreLoading = true
        // if (setting === 'autoseed') state.autoseedLoading = true
      })
      .addCase(switchSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'failover-mode') state.failoverLoading = false
        // if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        // if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        // if (setting === 'test') state.testLoading = false
        // if (setting === 'monitoring-save-config') state.monSaveConfigLoading = false
        // if (setting === 'monitoring-pause') state.monPauseLoading = false
        // if (setting === 'monitoring-capture') state.monCaptureLoading = false
        // if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = false
        // if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = false
        // if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = false
        // if (setting === 'monitoring-processlist') state.monProcessListLoading = false
        // if (setting === 'check-replication-state') state.replicationStateLoading = false
        // if (setting === 'failover-at-sync') state.failoverAtSyncLoading = false
        // if (setting === 'failover-restart-unsafe') state.failoverRestartUnsfLoading = false
        // if (setting === 'force-slave-no-gtid-mode') state.forceSlaveNoGtidModeLoading = false
        // if (setting === 'autorejoin-slave-positional-heartbeat') state.autorejoinLoading = false
        // if (setting === 'delay-stat-capture') state.delayStatCaptLoading = false
        // if (setting === 'failover-check-delay-stat') state.failoverCheckDelayStatLoading = false
        // if (setting === 'print-delay-stat') state.printDelayStatLoading = false
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = false
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = false
        // if (setting === 'autorejoin') state.arLoading = false
        // if (setting === 'autorejoin-backup-binlog') state.arBackupBinlogLoading = false
        // if (setting === 'autorejoin-flashback-on-sync') state.arFlashbackOnSyncLoading = false
        // if (setting === 'autorejoin-flashback') state.arFlashbackLoading = false
        // if (setting === 'autorejoin-mysqldump') state.arMysqldumpLoading = false
        // if (setting === 'autorejoin-logical-backup') state.arLogicalBackupLoading = false
        // if (setting === 'autorejoin-physical-backup') state.arPhysicalBackupLoading = false
        // if (setting === 'autorejoin-force-restore') state.arForceRestoreLoading = false
        // if (setting === 'autoseed') state.autoseedLoading = false
      })
      .addCase(switchSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'failover-mode') state.failoverLoading = false
        // if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        // if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        // if (setting === 'test') state.testLoading = false
        // if (setting === 'verbose') state.verboseLoading = false
        // if (setting === 'monitoring-save-config') state.monSaveConfigLoading = false
        // if (setting === 'monitoring-pause') state.monPauseLoading = false
        // if (setting === 'monitoring-capture') state.monCaptureLoading = false
        // if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = false
        // if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = false
        // if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = false
        // if (setting === 'monitoring-processlist') state.monProcessListLoading = false
        // if (setting === 'check-replication-state') state.replicationStateLoading = false
        // if (setting === 'failover-at-sync') state.failoverAtSyncLoading = false
        // if (setting === 'failover-restart-unsafe') state.failoverRestartUnsfLoading = false
        // if (setting === 'force-slave-no-gtid-mode') state.forceSlaveNoGtidModeLoading = false
        // if (setting === 'autorejoin-slave-positional-heartbeat') state.autorejoinLoading = false
        // if (setting === 'delay-stat-capture') state.delayStatCaptLoading = false
        // if (setting === 'failover-check-delay-stat') state.failoverCheckDelayStatLoading = false
        // if (setting === 'print-delay-stat') state.printDelayStatLoading = false
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = false
        // if (setting === 'print-delay-stat-history') state.printDelayStatHistLoading = false
        // if (setting === 'autorejoin') state.arLoading = false
        // if (setting === 'autorejoin-backup-binlog') state.arBackupBinlogLoading = false
        // if (setting === 'autorejoin-flashback-on-sync') state.arFlashbackOnSyncLoading = false
        // if (setting === 'autorejoin-flashback') state.arFlashbackLoading = false
        // if (setting === 'autorejoin-mysqldump') state.arMysqldumpLoading = false
        // if (setting === 'autorejoin-logical-backup') state.arLogicalBackupLoading = false
        // if (setting === 'autorejoin-physical-backup') state.arPhysicalBackupLoading = false
        // if (setting === 'autorejoin-force-restore') state.arForceRestoreLoading = false
        // if (setting === 'autoseed') state.autoseedLoading = false
      })
      .addCase(changeTopology.pending, (state) => {
        state.targetTopologyLoading = true
      })
      .addCase(changeTopology.fulfilled, (state, action) => {
        state.targetTopologyLoading = false
      })
      .addCase(changeTopology.rejected, (state, action) => {
        state.targetTopologyLoading = false
      })
    builder
      .addCase(setSetting.pending, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = true
        // if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = true
        // if (setting === 'verbose') state.verboseLoading = true
        // if (setting === 'log-level') state.logLevelLoading = true
        // if (setting === 'log-sql-in-monitoring') state.logSqlInMonLoading = true
        // if (setting === 'log-syslog') state.logSysLogLoading = true
        // if (setting === 'log-task-level') state.logTaskLoading = true
        // if (setting === 'log-writer-election-level') state.logWriterEleLoading = true
        // if (setting === 'log-sst-level') state.logSSTLoading = true
        // if (setting === 'log-heartbeat-level') state.logheartbeatLoading = true
        // if (setting === 'log-config-load-level') state.logConfigLoadLoading = true
        // if (setting === 'log-git-level') state.logGitLoading = true
        // if (setting === 'log-backup-stream-level') state.logBackupStrmLoading = true
        // if (setting === 'log-orchestrator-level') state.logOrcheLoading = true
        // if (setting === 'log-vault-level') state.logVaultLoading = true
        // if (setting === 'log-topology-level') state.logTopologyLoading = true
        // if (setting === 'log-graphite-level') state.logGraphiteLoading = true
        // if (setting === 'log-binlog-purge-level') state.logBackupStrmLoading = true
        // if (setting === 'log-proxy-level') state.logProxyLoading = true
        // if (setting === 'haproxy-log-level') state.logHAProxyLoading = true
        // if (setting === 'proxysql-log-level') state.logProxySqlLoading = true
        // if (setting === 'proxyjanitor-log-level') state.logProxyJanitorLoading = true
        // if (setting === 'maxscale-log-level') state.logMaxscaleLoading = true
      })
      .addCase(setSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = false
        // if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = false
        // if (setting === 'verbose') state.verboseLoading = false
        // if (setting === 'log-level') state.logLevelLoading = false
        // if (setting === 'log-sql-in-monitoring') state.logSqlInMonLoading = false
        // if (setting === 'log-syslog') state.logSysLogLoading = false
        // if (setting === 'log-task-level') state.logTaskLoading = false
        // if (setting === 'log-writer-election-level') state.logWriterEleLoading = false
        // if (setting === 'log-sst-level') state.logSSTLoading = false
        // if (setting === 'log-heartbeat-level') state.logheartbeatLoading = false
        // if (setting === 'log-config-load-level') state.logConfigLoadLoading = false
        // if (setting === 'log-git-level') state.logGitLoading = false
        // if (setting === 'log-backup-stream-level') state.logBackupStrmLoading = false
        // if (setting === 'log-orchestrator-level') state.logOrcheLoading = false
        // if (setting === 'log-vault-level') state.logVaultLoading = false
        // if (setting === 'log-topology-level') state.logTopologyLoading = false
        // if (setting === 'log-graphite-level') state.logGraphiteLoading = false
        // if (setting === 'log-binlog-purge-level') state.logBackupStrmLoading = false
        // if (setting === 'log-proxy-level') state.logProxyLoading = false
        // if (setting === 'haproxy-log-level') state.logHAProxyLoading = false
        // if (setting === 'proxysql-log-level') state.logProxySqlLoading = false
        // if (setting === 'proxyjanitor-log-level') state.logProxyJanitorLoading = false
        // if (setting === 'maxscale-log-level') state.logMaxscaleLoading = false
      })
      .addCase(setSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
        // if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = false
        // if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = false
        // if (setting === 'verbose') state.verboseLoading = false
        // if (setting === 'log-level') state.logLevelLoading = false
        // if (setting === 'log-sql-in-monitoring') state.logSqlInMonLoading = false
        // if (setting === 'log-syslog') state.logSysLogLoading = false
        // if (setting === 'log-task-level') state.logTaskLoading = false
        // if (setting === 'log-writer-election-level') state.logWriterEleLoading = false
        // if (setting === 'log-sst-level') state.logSSTLoading = false
        // if (setting === 'log-heartbeat-level') state.logheartbeatLoading = false
        // if (setting === 'log-config-load-level') state.logConfigLoadLoading = false
        // if (setting === 'log-git-level') state.logGitLoading = false
        // if (setting === 'log-backup-stream-level') state.logBackupStrmLoading = false
        // if (setting === 'log-orchestrator-level') state.logOrcheLoading = false
        // if (setting === 'log-vault-level') state.logVaultLoading = false
        // if (setting === 'log-topology-level') state.logTopologyLoading = false
        // if (setting === 'log-graphite-level') state.logGraphiteLoading = false
        // if (setting === 'log-binlog-purge-level') state.logBackupStrmLoading = false
        // if (setting === 'log-proxy-level') state.logProxyLoading = false
        // if (setting === 'haproxy-log-level') state.logHAProxyLoading = false
        // if (setting === 'proxysql-log-level') state.logProxySqlLoading = false
        // if (setting === 'proxyjanitor-log-level') state.logProxyJanitorLoading = false
        // if (setting === 'maxscale-log-level') state.logMaxscaleLoading = false
      })
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
