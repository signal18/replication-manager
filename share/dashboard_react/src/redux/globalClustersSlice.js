import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { globalClustersService } from '../services/globalClustersService'

export const getClusters = createAsyncThunk('globalClusters/getClusters', async ({}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusters()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterPeers = createAsyncThunk('globalClusters/getClusterPeers', async ({}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusterPeers()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getMonitoredData = createAsyncThunk('globalClusters/getMonitoredData', async ({}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getMonitoredData()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const switchGlobalSetting = createAsyncThunk(
  'globalClusters/switchGlobalSetting',
  async ({ setting }, thunkAPI) => {
    try {
      const { data, status } = await globalClustersService.switchGlobalSetting(setting)
      showSuccessBanner('Cloud18 setting switch is successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      console.log('error::', error)
      showErrorBanner('Cloud18 setting switch is failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setGlobalSetting = createAsyncThunk(
  'globalClusters/setGlobalSetting',
  async ({ setting, value }, thunkAPI) => {
    try {
      const { data, status } = await globalClustersService.setGlobalSetting(setting, value)
      showSuccessBanner('Cloud18 change setting is successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      console.log('error::', error)
      showErrorBanner('Cloud18 change setting is failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

const initialState = {
  loading: false,
  error: null,
  clusters: null,
  clusterPeers: null,
  monitor: null
}

export const globalClustersSlice = createSlice({
  name: 'globalClusters',
  initialState,
  reducers: {
    clearClusters: (state, action) => {
      Object.assign(state, initialState)
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(getClusters.pending, (state) => {
        state.loading = true
      })
      .addCase(getClusters.fulfilled, (state, action) => {
        state.loading = false
        state.clusters = action.payload.data
      })
      .addCase(getClusters.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(getMonitoredData.pending, (state) => {})
      .addCase(getMonitoredData.fulfilled, (state, action) => {
        state.monitor = action.payload.data
      })
      .addCase(getMonitoredData.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getClusterPeers.pending, (state) => {})
      .addCase(getClusterPeers.fulfilled, (state, action) => {
        state.clusterPeers = action.payload.data
      })
      .addCase(getClusterPeers.rejected, (state, action) => {
        state.error = action.error
      })
  }
})

export const { clearClusters } = globalClustersSlice.actions

// this is for configureStore
export default globalClustersSlice.reducer
