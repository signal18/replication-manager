import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { settingsService } from '../services/settingsService'
import { getClusterData } from './clusterSlice'

export const switchSetting = createAsyncThunk('cluster/switchSetting', async ({ clusterName, setting }, thunkAPI) => {
  try {
    const { data, status } = await settingsService.switchSettings(clusterName, setting)
    thunkAPI.dispatch(getClusterData({ clusterName }))
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
      thunkAPI.dispatch(getClusterData({ clusterName }))
      showSuccessBanner(`Topology changed to ${topology} successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Changing topology to ${setting} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setSettingsNullable = createAsyncThunk(
  'cluster/setSettingsNullable',
  async ({ clusterName, setting, value }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.setSettingsNullable(clusterName, setting, value)
      thunkAPI.dispatch(getClusterData({ clusterName }))
      showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Changing ${setting} failed!`, error, thunkAPI)
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
  verboseLoading: false,
  monSaveConfigLoading: false,
  monPauseLoading: false,
  monCaptureLoading: false,
  monSchemaChangeLoading: false,
  monInnoDBLoading: false,
  monVarDiffLoading: false,
  monProcessListLoading: false,
  captureTriggerLoading: false,
  monIgnoreErrLoading: false
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
        if (setting === 'failover-mode') state.failoverLoading = true
        if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = true
        if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = true
        if (setting === 'test') state.testLoading = true
        if (setting === 'verbose') state.verboseLoading = true
        if (setting === 'monitoring-save-config') state.monSaveConfigLoading = true
        if (setting === 'monitoring-pause') state.monPauseLoading = true
        if (setting === 'monitoring-capture') state.monCaptureLoading = true
        if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = true
        if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = true
        if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = true
        if (setting === 'monitoring-processlist') state.monProcessListLoading = true
      })
      .addCase(switchSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'failover-mode') state.failoverLoading = false
        if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        if (setting === 'test') state.testLoading = false
        if (setting === 'verbose') state.verboseLoading = false
        if (setting === 'monitoring-save-config') state.monSaveConfigLoading = false
        if (setting === 'monitoring-pause') state.monPauseLoading = false
        if (setting === 'monitoring-capture') state.monCaptureLoading = false
        if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = false
        if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = false
        if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = false
        if (setting === 'monitoring-processlist') state.monProcessListLoading = false
      })
      .addCase(switchSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'failover-mode') state.failoverLoading = false
        if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        if (setting === 'test') state.testLoading = false
        if (setting === 'verbose') state.verboseLoading = false
        if (setting === 'monitoring-save-config') state.monSaveConfigLoading = false
        if (setting === 'monitoring-pause') state.monPauseLoading = false
        if (setting === 'monitoring-capture') state.monCaptureLoading = false
        if (setting === 'monitoring-schema-change') state.monSchemaChangeLoading = false
        if (setting === 'monitoring-innodb-status') state.monInnoDBLoading = false
        if (setting === 'monitoring-variable-diff') state.monVarDiffLoading = false
        if (setting === 'monitoring-processlist') state.monProcessListLoading = false
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
      .addCase(setSettingsNullable.pending, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = true
        if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = true
      })
      .addCase(setSettingsNullable.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = false
        if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = false
      })
      .addCase(setSettingsNullable.rejected, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'monitoring-capture-trigger') state.captureTriggerLoading = false
        if (setting === 'monitoring-ignore-errors') state.monIgnoreErrLoading = false
      })
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
