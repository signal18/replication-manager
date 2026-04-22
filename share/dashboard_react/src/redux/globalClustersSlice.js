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

export const registerInstance = createGuardedAsyncThunk(
  'globalClusters/registerInstance',
  async ({ email, password, uri }, thunkAPI) => {
    try {
      const { data, status } = await globalClustersService.register(email, password, uri)
      if (status === 201) {
        if (data?.connect_error) {
          showSuccessBanner(
            `Registered successfully but connect failed — set cloud18=true once GitLab is reachable`,
            status,
            thunkAPI
          )
        } else {
          showSuccessBanner('Registration and Cloud18 connect succeeded!', status, thunkAPI)
        }
        return { data, status }
      } else {
        throw new Error(typeof data === 'object' ? (data?.error || JSON.stringify(data)) : data)
      }
    } catch (error) {
      showErrorBanner('Registration failed!', error, thunkAPI)
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

export const getGlobalMetrics = createGuardedAsyncThunk('globalClusters/getGlobalMetrics', async ({ baseURL = '' } = {}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getGlobalMetrics(baseURL)
    if (status !== 200) {
      throw new Error(data)
    }
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getGlobalLogs = createGuardedAsyncThunk('globalClusters/getGlobalLogs', async ({ baseURL = '' } = {}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getGlobalLogs(baseURL)
    if (status !== 200) {
      throw new Error(data)
    }
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getGlobalAlerts = createGuardedAsyncThunk('globalClusters/getGlobalAlerts', async ({ baseURL = '' } = {}, thunkAPI) => {
  try {
    const { data, status } = await globalClustersService.getGlobalAlerts(baseURL)
    if (status !== 200) {
      throw new Error(data)
    }
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const refreshAppTemplateRepo = createGuardedAsyncThunk(
  'globalClusters/refreshAppTemplateRepo',
  async ({ clusterName, silent = false, forceRefresh = false }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await globalClustersService.refreshAppTemplateRepo(clusterName, baseURL, forceRefresh)
      if (status !== 200) {
        throw new Error(data)
      }
      return { data, status }
    } catch (error) {
      if (!silent) {
        showErrorBanner('Error while refreshing app template repository', error, thunkAPI)
      }
      return handleError(error, thunkAPI)
    }
  }
)

export const getAppTemplateStructureGuide = createGuardedAsyncThunk(
  'globalClusters/getAppTemplateStructureGuide',
  async ({ clusterName, silent = false }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await globalClustersService.getAppTemplateStructureGuide(clusterName, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      return { data, status }
    } catch (error) {
      if (!silent) {
        showErrorBanner('Error while loading template structure guide', error, thunkAPI)
      }
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
  templateRepoError: null,
  templateGuideContent: '',
  templateGuideError: null,
  terms: ``,
  globalAlerts: { errors: [], warnings: [] },
  globalMetrics: null,
  globalLogs: { general: { buffer: [], len: 0, line: 0 } }
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
        const templateRows = Array.isArray(action.payload.data) ? action.payload.data : []
        const templateNames = templateRows.map((row) => row?.name).filter(Boolean)
        if (!state.monitor) {
          state.monitor = {}
        }
        state.monitor.serviceTemplates = templateNames
        state.monitor.serviceTemplateMetadata = templateRows
        state.templateRepoError = null
      })
      .addCase(refreshAppTemplateRepo.rejected, (state, action) => {
        state.templateRepoError = action.error?.message || 'Failed to refresh app template repository'
      })
      .addCase(getAppTemplateStructureGuide.fulfilled, (state, action) => {
        state.templateGuideContent = action.payload?.data?.content || ''
        state.templateGuideError = null
      })
      .addCase(getAppTemplateStructureGuide.rejected, (state, action) => {
        state.templateGuideError = action.error?.message || 'Failed to load template structure guide'
      })
      .addCase(getGlobalAlerts.fulfilled, (state, action) => {
        state.globalAlerts = {
          errors: action.payload.data?.errors ?? [],
          warnings: action.payload.data?.warnings ?? []
        }
      })
      .addCase(getGlobalAlerts.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getGlobalMetrics.fulfilled, (state, action) => {
        state.globalMetrics = action.payload.data
      })
      .addCase(getGlobalMetrics.rejected, (state, action) => {
        state.error = action.error
      })
      .addCase(getGlobalLogs.fulfilled, (state, action) => {
        state.globalLogs = action.payload.data ?? { general: { buffer: [], len: 0, line: 0 } }
      })
      .addCase(getGlobalLogs.rejected, (state, action) => {
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
