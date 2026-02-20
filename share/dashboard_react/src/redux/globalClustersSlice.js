import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { globalClustersService } from '../services/globalClustersService'

const getThunkTypePrefix = (actionType) => actionType.replace(/\/(pending|fulfilled|rejected)$/, '')

const getPendingKey = (typePrefix) => typePrefix

const shouldTrackThunk = (action) => {
  if (!action?.meta?.requestId) {
    return false
  }
  return getThunkTypePrefix(action.type).startsWith('globalClusters/')
}

const createGuardedAsyncThunk = (typePrefix, payloadCreator, options = {}) => {
  const { condition, ...restOptions } = options
  return createAsyncThunk(typePrefix, payloadCreator, {
    ...restOptions,
    condition: (arg, api) => {
      const pendingKey = getPendingKey(typePrefix)
      if (api.getState()?.globalClusters?.pendingThunks?.[pendingKey]) {
        return false
      }
      if (condition) {
        const result = condition(arg, api)
        if (result === false) {
          return false
        }
      }
      return true
    }
  })
}

export const getClusters = createGuardedAsyncThunk('globalClusters/getClusters', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusters()
    if (status !== 200) {
      throw new Error(data)
    }
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
});

export const addCluster = createGuardedAsyncThunk('globalClusters/addCluster', async ({ clusterName, formdata }, thunkAPI) => {
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
    return handleError(error, thunkAPI)
  }
})

export const dropCluster = createGuardedAsyncThunk('globalClusters/dropCluster', async ({ clusterName }, thunkAPI) => {
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
    return handleError(error, thunkAPI)
  }
})

export const renameCluster = createGuardedAsyncThunk('globalClusters/renameCluster', async ({ clusterName, newClusterName }, thunkAPI) => {
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
    return handleError(error, thunkAPI)
  }
})

export const getClusterPeers = createGuardedAsyncThunk('globalClusters/getClusterPeers', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusterPeers()
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
});

export const getClusterForSale = createGuardedAsyncThunk('globalClusters/getClusterForSale', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getClusterForSale()
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
});

export const getMonitoredData = createGuardedAsyncThunk('globalClusters/getMonitoredData', async ({ }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getMonitoredData()
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
});

export const switchGlobalSetting = createGuardedAsyncThunk(
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
      return handleError(error, thunkAPI)
    }
  }
)

export const setGlobalSetting = createGuardedAsyncThunk(
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
      return handleError(error, thunkAPI)
    }
  }
)

export const reloadClustersPlan = createGuardedAsyncThunk('globalClusters/reloadClustersPlan', async ({ download = true }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.reloadClustersPlan(download)
    showSuccessBanner('All clusters plan reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error::', error)
    showErrorBanner('Failed to reload clusters plans!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
}
)

export const reloadClustersPlanInfo = createGuardedAsyncThunk('globalClusters/reloadClustersPlanInfo', async ({ download = true }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.reloadClustersPlanInfo(download)
    showSuccessBanner('All clusters plan info reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error::', error)
    showErrorBanner('Failed to reload clusters plan info!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
}
)

export const getTermsData = createGuardedAsyncThunk('globalClusters/getTermsData', async ({ baseURL = '' }, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getTermsData(baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const refreshAppTemplateRepo = createGuardedAsyncThunk(
  'globalClusters/refreshAppTemplateRepo',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await globalClustersService.refreshAppTemplateRepo(clusterName, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('App template repository refresh initiated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while refreshing app template repository', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

const initialState = {
  loading: false,
  pendingThunks: {},
  error: null,
  clusters: [],
  isDownList: {},
  isFailableList: {},
  clusterPeers: null,
  clusterForSale: null,
  monitor: null,
  terms: ``
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
        state.isDownList = action.payload.status == 200 ? action.payload.data?.reduce((acc, cluster) => {
          acc[cluster.name] = cluster.isDown
          return acc
        }, {}) : {}
        state.isFailableList = action.payload.status == 200 ? action.payload.data?.reduce((acc, cluster) => {
          acc[cluster.name] = cluster.isFailable
          return acc
        }, {}) : {}
      })
      .addCase(getClusters.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(getMonitoredData.fulfilled, (state, action) => {
        state.monitor = action.payload.data
      })
      .addCase(getMonitoredData.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getTermsData.fulfilled, (state, action) => {
        state.terms = action.payload.data
      })
      .addCase(getTermsData.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getClusterPeers.fulfilled, (state, action) => {
        state.clusterPeers = action.payload.data
      })
      .addCase(getClusterPeers.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getClusterForSale.fulfilled, (state, action) => {
        state.clusterForSale = action.payload.data
      })
      .addCase(getClusterForSale.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(refreshAppTemplateRepo.fulfilled, (state, action) => {
        state.monitor.serviceTemplates = action.payload.data
      })
      .addCase(refreshAppTemplateRepo.rejected, (state, action) => {
        state.error = action.error
      })

    builder.addMatcher(
      (action) => action.type.endsWith('/pending') && shouldTrackThunk(action),
      (state, action) => {
        const typePrefix = getThunkTypePrefix(action.type)
        const pendingKey = getPendingKey(typePrefix)
        state.pendingThunks[pendingKey] = true
      }
    )

    builder.addMatcher(
      (action) =>
        (action.type.endsWith('/fulfilled') || action.type.endsWith('/rejected')) && shouldTrackThunk(action),
      (state, action) => {
        const typePrefix = getThunkTypePrefix(action.type)
        const pendingKey = getPendingKey(typePrefix)
        state.pendingThunks[pendingKey] = false
      }
    )
  }
})

export const { clearClusters } = globalClustersSlice.actions

// this is for configureStore
export default globalClustersSlice.reducer
