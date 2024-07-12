import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'
import { showErrorToast, showSuccessToast } from './toastSlice'

const handleError = (error, thunkAPI) => {
  const errorMessage = error.message || 'Request failed'
  const errorStatus = error.errorStatus || 500 // Default error status if not provided
  // Handle errors (including custom errorStatus)
  return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
}

const showSuccessBanner = (message, responseStatus, thunkAPI) => {
  thunkAPI.dispatch(
    showSuccessToast({
      status: 'success',
      title: message
    })
  )
}
const showErrorBanner = (message, error, thunkAPI) => {
  thunkAPI.dispatch(
    showErrorToast({
      status: 'error',
      title: message,
      description: error
    })
  )
}

export const getClusters = createAsyncThunk('cluster/getClusters', async ({}, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusters()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getMonitoredData = createAsyncThunk('cluster/getMonitoredData', async ({}, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getMonitoredData()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterData = createAsyncThunk('cluster/getClusterData', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusterData(clusterName)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterAlerts = createAsyncThunk('cluster/getClusterAlerts', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusterAlerts(clusterName)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterMaster = createAsyncThunk('cluster/getClusterMaster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusterMaster(clusterName)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const switchOverCluster = createAsyncThunk('cluster/switchOverCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.switchOverCluster(clusterName)
    showSuccessBanner('Switchover Successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Switchover Failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const failOverCluster = createAsyncThunk('cluster/failOverCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.failOverCluster(clusterName)
    showSuccessBanner('Failover Successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failover Failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const resetFailOverCounter = createAsyncThunk(
  'cluster/resetFailOverCounter',
  async ({ clusterName }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.resetFailOverCounter(clusterName)
      showSuccessBanner('Failover counter reset!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover counter reset failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)
export const resetSLA = createAsyncThunk('cluster/resetSLA', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.resetSLA(clusterName)
    showSuccessBanner('SLA reset!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('SLA reset failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const toggleTraffic = createAsyncThunk('cluster/toggleTraffic', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.toggleTraffic(clusterName)
    showSuccessBanner('Traffic toggle done!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Traffic toggle failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const addServer = createAsyncThunk(
  'cluster/addServer',
  async ({ clusterName, host, port, dbType }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.addServer(clusterName, host, port, dbType)
      showSuccessBanner('New server added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new server', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const provisionCluster = createAsyncThunk('cluster/provisionCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.provisionCluster(clusterName)
    showSuccessBanner('Cluster provision successful', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Cluster provision failed', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const unProvisionCluster = createAsyncThunk('cluster/unProvisionCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.unProvisionCluster(clusterName)
    showSuccessBanner('Cluster unprovision successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Cluster unprovision failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setDBCredential = createAsyncThunk('cluster/setDBCredential', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.setDBCredential(clusterName)
    showSuccessBanner('Database credentials set!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Setting Database credentials failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setReplicationCredential = createAsyncThunk(
  'cluster/setReplicationCredential',
  async ({ clusterName }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.setReplicationCredential(clusterName)
      showSuccessBanner('Replication credentials set!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Setting Replication credentials failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const rotateDBCredential = createAsyncThunk('cluster/rotateDBCredential', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rotateDBCredential(clusterName)
    showSuccessBanner('Database rotation successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Database rotation failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rollingOptimize = createAsyncThunk('cluster/rollingOptimize', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rollingOptimize(clusterName)
    thunkAPI.dispatch(
      showSuccessToast({
        status: 'success',
        title: 'Rolling optimize successful!'
      })
    )
    return { data, status }
  } catch (error) {
    thunkAPI.dispatch(
      showErrorToast({
        status: 'error',
        title: 'Rolling optimize failed!'
      })
    )
    handleError(error, thunkAPI)
  }
})

export const rollingRestart = createAsyncThunk('cluster/rollingRestart', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rollingRestart(clusterName)
    thunkAPI.dispatch(
      showSuccessToast({
        status: 'success',
        title: 'Rolling restart successful!'
      })
    )
    return { data, status }
  } catch (error) {
    thunkAPI.dispatch(
      showErrorToast({
        status: 'error',
        title: 'Rolling restart failed!'
      })
    )
    handleError(error, thunkAPI)
  }
})

export const rotateCertificates = createAsyncThunk('cluster/rotateCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rotateCertificates(clusterName)
    thunkAPI.dispatch(
      showSuccessToast({
        status: 'success',
        title: 'Rotate certificates successful!'
      })
    )
    return { data, status }
  } catch (error) {
    thunkAPI.dispatch(
      showErrorToast({
        status: 'error',
        title: 'Rotate certificates failed!'
      })
    )
    handleError(error, thunkAPI)
  }
})

export const reloadCertificates = createAsyncThunk('cluster/reloadCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.reloadCertificates(clusterName)
    thunkAPI.dispatch(
      showSuccessToast({
        status: 'success',
        title: 'Reload certificates successful!'
      })
    )
    return { data, status }
  } catch (error) {
    thunkAPI.dispatch(
      showErrorToast({
        status: 'error',
        title: 'Reload certificates failed!'
      })
    )
    handleError(error, thunkAPI)
  }
})

export const cancelRollingRestart = createAsyncThunk(
  'cluster/cancelRollingRestart',
  async ({ clusterName }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.cancelRollingRestart(clusterName)
      thunkAPI.dispatch(
        showSuccessToast({
          status: 'success',
          title: 'Rolling restart cancelled!'
        })
      )
      return { data, status }
    } catch (error) {
      thunkAPI.dispatch(
        showErrorToast({
          status: 'error',
          title: 'Rolling restart cancellation failed!'
        })
      )
      handleError(error, thunkAPI)
    }
  }
)

export const cancelRollingReprov = createAsyncThunk(
  'cluster/cancelRollingReprov',
  async ({ clusterName }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.cancelRollingReprov(clusterName)
      thunkAPI.dispatch(
        showSuccessToast({
          status: 'success',
          title: 'Rolling reprov cancelled!'
        })
      )
      return { data, status }
    } catch (error) {
      thunkAPI.dispatch(
        showErrorToast({
          status: 'error',
          title: 'Rolling reprov cancellation failed!'
        })
      )
      handleError(error, thunkAPI)
    }
  }
)

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
    refreshInterval: 0,
    loadingStates: {
      switchOver: false,
      failOver: false,
      menuActions: false
    }
  },
  reducers: {
    setRefreshInterval: (state, action) => {
      localStorage.setItem('refresh_interval', action.payload.interval)
      state.refreshInterval = action.payload.interval
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

    builder.addMatcher(
      isAnyOf(getClusterData.fulfilled, getClusterAlerts.fulfilled, getClusterMaster.fulfilled),
      (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.clusterData = action.payload.data
        } else if (action.type.includes('getClusterAlerts')) {
          state.clusterAlerts = action.payload.data
        } else if (action.type.includes('getClusterMaster')) {
          state.clusteraMaster = action.payload.data
        }
      }
    )

    builder.addMatcher(
      isAnyOf(
        switchOverCluster.pending,
        failOverCluster.pending,
        resetFailOverCounter.pending,
        resetSLA.pending,
        addServer.pending,
        toggleTraffic.pending,
        provisionCluster.pending,
        unProvisionCluster.pending,
        setDBCredential.pending,
        setReplicationCredential.pending,
        rotateDBCredential.pending,
        rollingOptimize.pending,
        rollingRestart.pending,
        rotateCertificates.pending,
        reloadCertificates.pending,
        cancelRollingRestart.pending,
        cancelRollingReprov.pending
      ),
      (state, action) => {
        if (action.type.includes('switchOverCluster')) {
          state.loadingStates.switchOver = true
        } else if (action.type.includes('failOverCluster')) {
          state.loadingStates.failOver = true
        } else {
          state.loadingStates.menuActions = true
        }
      }
    )
    builder.addMatcher(
      isAnyOf(
        switchOverCluster.fulfilled,
        failOverCluster.fulfilled,
        resetFailOverCounter.fulfilled,
        resetSLA.fulfilled,
        addServer.fulfilled,
        toggleTraffic.fulfilled,
        provisionCluster.fulfilled,
        unProvisionCluster.fulfilled,
        setDBCredential.fulfilled,
        setReplicationCredential.fulfilled,
        rotateDBCredential.fulfilled,
        rollingOptimize.fulfilled,
        rollingRestart.fulfilled,
        rotateCertificates.fulfilled,
        reloadCertificates.fulfilled,
        cancelRollingRestart.fulfilled,
        cancelRollingReprov.fulfilled
      ),
      (state, action) => {
        if (action.type.includes('switchOverCluster')) {
          state.loadingStates.switchOver = false
        } else if (action.type.includes('failOverCluster')) {
          state.loadingStates.failOver = false
        } else {
          state.loadingStates.menuActions = false
        }
      }
    )
    builder.addMatcher(
      isAnyOf(
        switchOverCluster.rejected,
        failOverCluster.rejected,
        resetFailOverCounter.rejected,
        resetSLA.rejected,
        addServer.rejected,
        toggleTraffic.rejected,
        provisionCluster.rejected,
        unProvisionCluster.rejected,
        setDBCredential.rejected,
        setReplicationCredential.rejected,
        rotateDBCredential.rejected,
        rollingOptimize.rejected,
        rollingRestart.rejected,
        rotateCertificates.rejected,
        reloadCertificates.rejected,
        cancelRollingRestart.rejected,
        cancelRollingReprov.rejected
      ),
      (state, action) => {
        if (action.type.includes('switchOverCluster')) {
          state.loadingStates.switchOver = false
        } else if (action.type.includes('failOverCluster')) {
          state.loadingStates.failOver = false
        } else {
          state.loadingStates.menuActions = false
        }
      }
    )
  }
})

export const { setRefreshInterval } = clusterSlice.actions

// this is for configureStore
export default clusterSlice.reducer
