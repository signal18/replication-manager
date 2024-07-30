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

const initialState = {
  failoverLoading: false,
  targetTopologyLoading: false,
  allowUnsafeClusterLoading: false,
  allowMultitierSlaveLoading: false,
  testLoading: false,
  verboseLoading: false
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
      })
      .addCase(switchSetting.fulfilled, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'failover-mode') state.failoverLoading = false
        if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        if (setting === 'test') state.testLoading = false
        if (setting === 'verbose') state.verboseLoading = false
      })
      .addCase(switchSetting.rejected, (state, action) => {
        const setting = action.meta.arg.setting
        if (setting === 'failover-mode') state.failoverLoading = false
        if (setting === 'multi-master-ring-unsafe') state.allowUnsafeClusterLoading = false
        if (setting === 'replication-no-relay') state.allowMultitierSlaveLoading = false
        if (setting === 'test') state.testLoading = false
        if (setting === 'verbose') state.verboseLoading = false
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
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
