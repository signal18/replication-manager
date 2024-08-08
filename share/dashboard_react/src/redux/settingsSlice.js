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
    showErrorBanner(`Changing ${setting} failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const updateGraphiteWhiteList = createAsyncThunk(
  'cluster/updateGraphiteWhiteList',
  async ({ clusterName, whiteListValue }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.updateGraphiteWhiteList(clusterName, whiteListValue)
      showSuccessBanner(`Graphite Whitelist Regexp updated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Updating Graphite Whitelist Regexp failed!`, error.toString(), thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const updateGraphiteBlackList = createAsyncThunk(
  'cluster/updateGraphiteBlackList',
  async ({ clusterName, blackListValue }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.updateGraphiteBlackList(clusterName, blackListValue)
      showSuccessBanner(`Graphite BlackList Regexp updated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Updating Graphite BlackList Regexp failed!`, error.toString(), thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

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
      })
      .addCase(switchSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
      })
      .addCase(switchSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
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
      })
      .addCase(setSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
      })
      .addCase(setSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
      })
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
