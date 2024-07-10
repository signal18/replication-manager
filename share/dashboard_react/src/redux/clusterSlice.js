import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'

export const getClusters = createAsyncThunk('cluster/getClusters', async ({}, thunkAPI) => {
  try {
    const response = await clusterService.getClusters()
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const getMonitoredData = createAsyncThunk('cluster/getMonitoredData', async ({}, thunkAPI) => {
  try {
    const response = await clusterService.getMonitoredData()
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const getClusterData = createAsyncThunk('cluster/getClusterData', async ({ clusterName }, thunkAPI) => {
  try {
    const response = await clusterService.getClusterData(clusterName)
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const getClusterAlerts = createAsyncThunk('cluster/getClusterAlerts', async ({ clusterName }, thunkAPI) => {
  try {
    const response = await clusterService.getClusterAlerts(clusterName)
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const getClusterMaster = createAsyncThunk('cluster/getClusterMaster', async ({ clusterName }, thunkAPI) => {
  try {
    const response = await clusterService.getClusterMaster(clusterName)
    return response
  } catch (error) {
    const errorMessage = error.message || 'Request failed'
    const errorStatus = error.errorStatus || 500 // Default error status if not provided
    // Handle errors (including custom errorStatus)
    return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
  }
})

export const clusterSlice = createSlice({
  name: 'cluster',
  initialState: {
    loading: false,
    error: null,
    clusters: null,
    monitor: null,
    clusterData: null,
    clusterAlerts: null,
    clusteraMaster: null,
    selectedCluster: null,
    refreshInterval: 0
  },
  reducers: {
    setRefreshInterval: (state, action) => {
      localStorage.setItem('refresh_interval', action.payload.interval)
      state.refreshInterval = action.payload.interval
    },
    setCurrentCluster: (state, action) => {
      state.selectedCluster = action.payload.cluster
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(getClusters.pending, (state) => {
        state.loading = true
      })
      .addCase(getClusters.fulfilled, (state, action) => {
        state.loading = false
        state.clusters = action.payload
      })
      .addCase(getClusters.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(getMonitoredData.pending, (state) => {})
      .addCase(getMonitoredData.fulfilled, (state, action) => {
        state.monitor = action.payload
      })
      .addCase(getMonitoredData.rejected, (state, action) => {
        state.error = action.error
      })

    builder.addMatcher(
      isAnyOf(getClusterData.fulfilled, getClusterAlerts.fulfilled, getClusterMaster.fulfilled),
      (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.clusterData = action.payload
        } else if (action.type.includes('getClusterAlerts')) {
          state.clusterAlerts = action.payload
        } else if (action.type.includes('getClusterMaster')) {
          state.clusteraMaster = action.payload
        }
      }
    )
  }
})

export const { setRefreshInterval, setCurrentCluster } = clusterSlice.actions

// this is for configureStore
export default clusterSlice.reducer
