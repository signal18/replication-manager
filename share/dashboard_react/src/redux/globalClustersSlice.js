import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { globalClustersService } from '../services/globalClustersService'
import { Link } from '@chakra-ui/react'

export const getClusters = createAsyncThunk('globalClusters/getClusters', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusters()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const addCluster = createAsyncThunk('globalClusters/addCluster', async ({ clusterName, formdata }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.addCluster(clusterName, formdata)
    if (status === 200) {
      showSuccessBanner("Add cluster '" + clusterName + "' is successful!", status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner("Add cluster '" + clusterName + "' is failed!", error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const dropCluster = createAsyncThunk('globalClusters/dropCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.dropCluster(clusterName)
    if (status === 200) {
      showSuccessBanner("Drop cluster '" + clusterName + "' is successful!", status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner("Drop cluster '" + clusterName + "' is failed!", error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const renameCluster = createAsyncThunk('globalClusters/renameCluster', async ({ clusterName, newClusterName }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.renameCluster(clusterName, newClusterName)
    if (status === 200) {
      showSuccessBanner("Rename cluster '" + clusterName + "' is successful!", status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner("Rename cluster '" + clusterName + "' is failed!", error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const getClusterPeers = createAsyncThunk('globalClusters/getClusterPeers', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusterPeers()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterForSale = createAsyncThunk('globalClusters/getClusterForSale', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusterForSale()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getMonitoredData = createAsyncThunk('globalClusters/getMonitoredData', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getMonitoredData()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const switchGlobalSetting = createAsyncThunk(
  'globalClusters/switchGlobalSetting',
  async ({ setting, errMsgFunc }, thunkAPI) => {
    try {
      const { data, status } = await globalClustersService.switchGlobalSetting(setting)
      if (status === 200) {
        showSuccessBanner('Global setting switch is successful!', status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      console.log('error::', error)
      if (errMsgFunc) {
        showErrorBanner('Global setting switch is failed!', errMsgFunc(error), thunkAPI)
      } else {
        showErrorBanner('Global setting switch is failed!', error, thunkAPI)
      }
      handleError(error, thunkAPI)
    }
  }
)

export const setGlobalSetting = createAsyncThunk(
  'globalClusters/setGlobalSetting',
  async ({ setting, value, errMsgFunc }, thunkAPI) => {
    try {
      const { data, status } = value !== "" ? await globalClustersService.setGlobalSetting(setting, value) : await globalClustersService.clearGlobalSetting(setting)
      if (status === 200) {
        showSuccessBanner('Global setting is successfully changed!', status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      console.log('error::', error)
      if (errMsgFunc) {
        showErrorBanner('Global setting change is failed!', errMsgFunc(error), thunkAPI)
      } else {
        showErrorBanner('Global setting change is failed!', error, thunkAPI)
      }
      handleError(error, thunkAPI)
    }
  }
)

export const reloadClustersPlan = createAsyncThunk('globalClusters/reloadClustersPlan', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.reloadClustersPlan()
    showSuccessBanner('All clusters plan reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error::', error)
    showErrorBanner('Failed to reload clusters plans!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
}
)

export const getTermsData = createAsyncThunk('globalClusters/getTermsData', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getTermsData()
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

const initialState = {
  loading: false,
  error: null,
  clusters: null,
  isDownList: {},
  isFailableList: {},
  clusterPeers: null,
  clusterForSale: null,
  monitor: null,
  terms: null
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
        state.isDownList = action.payload.data.reduce((acc, cluster) => {
          acc[cluster.name] = cluster.isDown
          return acc
        }, {})
        state.isFailableList = action.payload.data.reduce((acc, cluster) => {
          acc[cluster.name] = cluster.isFailable
          return acc
        }, {})
      })
      .addCase(getClusters.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(getMonitoredData.pending, (state) => { })
      .addCase(getMonitoredData.fulfilled, (state, action) => {
        state.monitor = action.payload.data
      })
      .addCase(getMonitoredData.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getTermsData.pending, (state) => { })
      .addCase(getTermsData.fulfilled, (state, action) => {
        state.terms = action.payload.data
      })
      .addCase(getTermsData.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getClusterPeers.pending, (state) => { })
      .addCase(getClusterPeers.fulfilled, (state, action) => {
        state.clusterPeers = action.payload.data
      })
      .addCase(getClusterPeers.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getClusterForSale.pending, (state) => { })
      .addCase(getClusterForSale.fulfilled, (state, action) => {
        state.clusterForSale = action.payload.data
      })
      .addCase(getClusterForSale.rejected, (state, action) => {
        state.error = action.error
      })
  }
})

export const { clearClusters } = globalClustersSlice.actions

// this is for configureStore
export default globalClustersSlice.reducer
