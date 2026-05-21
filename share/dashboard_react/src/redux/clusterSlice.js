import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { get, isEqual } from 'lodash'

const pendingKeySelectors = {
  'cluster/getJobs': (arg) => arg?.clusterName,
  'cluster/getDatabaseService': (arg) => arg?.serviceName,
  'cluster/getAppService': (arg) => arg?.serviceName
}

const getThunkTypePrefix = (actionType) => actionType.replace(/\/(pending|fulfilled|rejected)$/, '')

const getPendingKey = (typePrefix, arg) => {
  const selector = pendingKeySelectors[typePrefix]
  if (selector) {
    const suffix = selector(arg)
    if (suffix) {
      return `${typePrefix}:${suffix}`
    }
  }
  return typePrefix
}

const shouldTrackThunk = (action) => {
  if (!action?.meta?.requestId) {
    return false
  }
  const typePrefix = getThunkTypePrefix(action.type)
  return typePrefix.startsWith('cluster/') || typePrefix.startsWith('auth/cluster')
}

const createGuardedAsyncThunk = (typePrefix, payloadCreator, options = {}) => {
  const { condition, ...restOptions } = options
  return createAsyncThunk(typePrefix, payloadCreator, {
    ...restOptions,
    condition: (arg, api) => {
      const pendingKey = getPendingKey(typePrefix, arg)
      if (get(api.getState(), ['cluster', 'pendingThunks', pendingKey])) {
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

const buildClusterStateSignature = (items) =>
  Array.isArray(items) ? items.map((item) => `${item?.state}-${item?.isVirtualMaster}`).join(',') : ''

const buildProxyStagingList = (proxies) =>
  proxies
    ?.filter((proxy) => proxy.isStaging)
    .map((proxy) => proxy.name)
    .join(',') || ''

const fulfilledHandlers = {
  'cluster/getClusterData': (state, action) => {
    state.clusterData = action.payload.data
  },
  'cluster/getClusterAlerts': (state, action) => {
    state.clusterAlerts = action.payload.data
  },
  'cluster/getClusterMaster': (state, action) => {
    state.clusterMaster = action.payload.data
  },
  'cluster/getClusterCertificates': (state, action) => {
    state.clusterCertificates = action.payload.data
  },
  'cluster/getTopProcess': (state, action) => {
    state.topProcess = action.payload.data
  },
  'cluster/getOpenSVCStats': (state, action) => {
    state.opensvcStats = action.payload.data
  },
  'cluster/getOpenSVCPools': (state, action) => {
    state.opensvcPools = action.payload.data
  },
  'cluster/getBackups': (state, action) => {
    state.backups.list = action.payload.data
  },
  'cluster/getBackupStats': (state, action) => {
    state.backups.stats = action.payload.data
  },
  'cluster/getResticSnapshot': (state, action) => {
    state.restic.snapshots = action.payload?.data?.snapshots || []
    state.restic.stats = action.payload?.data?.stats || null
    state.restic.repoPath = action.payload?.data?.repo_path || ''
  },
  'cluster/getResticStats': (state, action) => {
    state.restic.stats = action.payload.data
  },
  'cluster/getResticCurrentTask': (state, action) => {
    state.restic.currentTask = action.payload?.data?.current_task || null
    state.restic.queue = action.payload?.data?.queue || []
  },
  'cluster/getResticQueue': (state, action) => {
    state.restic.queue = action.payload.data
  },
  'cluster/getShardSchema': (state, action) => {
    state.shardSchema = action.payload.data
  },
  'cluster/getQueryRules': (state, action) => {
    state.queryRules = action.payload.data
  },
  'cluster/getJobs': (state, action) => {
    if (action.meta?.arg?.clusterName === state.clusterData?.name) {
      state.jobs = action.payload.data
    }
  }
}

const handleDatabaseServiceFulfilled = (state, action) => {
  const { serviceName } = action.meta.arg
  switch (serviceName) {
    case 'processlist':
      state.database.processList = action.payload.data
      break
    case 'slow-queries':
      state.database.slowQueries = action.payload.data
      break
    case 'errorlog':
      state.database.errors = action.payload.data
      break
    case 'sqlerrorlog':
      state.database.sqlerrors = action.payload.data
      break
    case 'auditlog':
      state.database.auditlogs = action.payload.data
      break
    case 'digest-statements-pfs':
      state.database.digestQueries = action.payload.data
      break
    case 'tables':
      if (!isEqual(state.database.tables, action.payload?.data)) {
        state.database.tables = action.payload?.data
      }
      break
    case 'status-delta':
      state.database.status.statusDelta = action.payload.data
      break
    case 'status-innodb':
      state.database.status.statusInnoDB = action.payload.data
      break
    case 'variables':
      state.database.variables = action.payload.status == 200 ? action.payload.data : []
      break
    case 'service-opensvc':
      state.database.serviceOpensvc = action.payload.data
      break
    case 'meta-data-locks':
      state.database.metadataLocks = action.payload.data
      break
    case 'query-response-time':
      state.database.responsetime = action.payload.data
      break
    default:
      break
  }
}

export const getClusterData = createGuardedAsyncThunk('cluster/getClusterData', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterData(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getClusterAlerts = createGuardedAsyncThunk(
  'cluster/getClusterAlerts',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterAlerts(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getClusterLogs = createGuardedAsyncThunk('cluster/getClusterLogs', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterLogs(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getClusterMaster = createGuardedAsyncThunk(
  'cluster/getClusterMaster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterMaster(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getClusterServers = createGuardedAsyncThunk(
  'cluster/getClusterServers',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterServers(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getClusterProxies = createGuardedAsyncThunk(
  'cluster/getClusterProxies',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterProxies(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getClusterCertificates = createGuardedAsyncThunk(
  'cluster/getClusterCertificates',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterCertificates(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getTopProcess = createGuardedAsyncThunk('cluster/getTopProcess', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getTopProcess(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getOpenSVCStats = createGuardedAsyncThunk('cluster/getOpenSVCStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getOpenSVCStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getOpenSVCPools = createGuardedAsyncThunk('cluster/getOpenSVCPools', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getOpenSVCPools(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getBackups = createGuardedAsyncThunk('cluster/getBackups', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getBackups(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getBackupStats = createGuardedAsyncThunk('cluster/getBackupStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getBackupStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const deleteBackup = createGuardedAsyncThunk('cluster/deleteBackup', async ({ clusterName, backupId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.deleteBackup(clusterName, backupId, baseURL)
    if (status === 200) {
      thunkAPI.dispatch(getBackups({ clusterName }))
      thunkAPI.dispatch(getBackupStats({ clusterName }))
    }
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getResticSnapshot = createGuardedAsyncThunk(
  'cluster/getResticSnapshot',
  async ({ clusterName, filter = 'latest-per-session' }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getResticSnapshot(clusterName, baseURL, filter)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getResticStats = createGuardedAsyncThunk('cluster/getResticStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getResticStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const purgeResticSnapshot = createGuardedAsyncThunk(
  'cluster/purgeResticSnapshot',
  async ({ clusterName, snapshotId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.purgeResticSnapshot(clusterName, snapshotId, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const purgeResticByPolicy = createGuardedAsyncThunk(
  'cluster/purgeResticByPolicy',
  async ({ clusterName, dryRun }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.purgeResticByPolicy(clusterName, baseURL, { dryRun })
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getResticQueue = createGuardedAsyncThunk('cluster/getResticQueue', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getResticQueue(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getResticCurrentTask = createGuardedAsyncThunk(
  'cluster/getResticCurrentTask',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getResticCurrentTask(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resticQueueCancel = createGuardedAsyncThunk(
  'cluster/resticQueueCancel',
  async ({ clusterName, taskId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resticQueueCancel(clusterName, taskId, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resticQueueMove = createGuardedAsyncThunk(
  'cluster/resticQueueMove',
  async ({ clusterName, taskId, direction, afterId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resticQueueMove(clusterName, taskId, direction, afterId, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resticQueuePause = createGuardedAsyncThunk(
  'cluster/resticQueuePause',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resticQueuePause(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resticQueueResume = createGuardedAsyncThunk(
  'cluster/resticQueueResume',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resticQueueResume(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resticInitRepo = createGuardedAsyncThunk(
  'cluster/resticInitRepo',
  async ({ clusterName, force = false, allowEmptyPrefix = false }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resticInitRepo(
        clusterName,
        force,
        { allowEmptyPrefix },
        baseURL
      )
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getJobs = createGuardedAsyncThunk('cluster/getJobs', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getJobs(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getShardSchema = createGuardedAsyncThunk('cluster/getShardSchema', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getShardSchema(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const getQueryRules = createGuardedAsyncThunk('cluster/getQueryRules', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getQueryRules(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const switchOverCluster = createGuardedAsyncThunk(
  'cluster/switchOverCluster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.switchOverCluster(clusterName, baseURL)
      showSuccessBanner('Switchover Successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Switchover Failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const failOverCluster = createGuardedAsyncThunk('cluster/failOverCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.failOverCluster(clusterName, baseURL)
    showSuccessBanner('Failover Successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failover Failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const resetFailOverCounter = createGuardedAsyncThunk(
  'cluster/resetFailOverCounter',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resetFailOverCounter(clusterName, baseURL)
      showSuccessBanner('Failover counter reset!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover counter reset failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)
export const resetSLA = createGuardedAsyncThunk('cluster/resetSLA', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resetSLA(clusterName, baseURL)
    showSuccessBanner('SLA reset!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('SLA reset failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const toggleTraffic = createGuardedAsyncThunk('cluster/toggleTraffic', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.toggleTraffic(clusterName, baseURL)
    showSuccessBanner('Traffic toggle done!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Traffic toggle failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const toggleTrafficStaging = createGuardedAsyncThunk(
  'cluster/toggleTrafficStaging',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleTrafficStaging(clusterName, baseURL)
      showSuccessBanner('Traffic staging toggle done!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Traffic staging toggle failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const addServer = createGuardedAsyncThunk(
  'cluster/addServer',
  async ({ clusterName, host, port, monitorType, tag, dockerRegistry }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.addServer(
        clusterName,
        host,
        port,
        monitorType,
        tag,
        dockerRegistry,
        baseURL
      )
      showSuccessBanner('New server added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new server', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const dropServer = createGuardedAsyncThunk(
  'cluster/dropServer',
  async ({ clusterName, host, port, type }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropServer(clusterName, host, port, type, baseURL)
      showSuccessBanner('New server dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a new server', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const dropServerByName = createGuardedAsyncThunk(
  'cluster/dropServerByName',
  async ({ clusterName, serverName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropServerByName(clusterName, serverName, baseURL)
      showSuccessBanner('New server dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a new server', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const dropApp = createGuardedAsyncThunk('cluster/dropApp', async ({ clusterName, host, port }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.dropApp(clusterName, host, port, baseURL)
    showSuccessBanner('New app dropped!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Error while dropping a new app', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const provisionCluster = createGuardedAsyncThunk(
  'cluster/provisionCluster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.provisionCluster(clusterName, baseURL)
      showSuccessBanner('Cluster provision successful', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Cluster provision failed', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const unProvisionCluster = createGuardedAsyncThunk(
  'cluster/unProvisionCluster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unProvisionCluster(clusterName, baseURL)
      showSuccessBanner('Cluster unprovision successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Cluster unprovision failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const setCredentials = createGuardedAsyncThunk(
  'cluster/setCredentials',
  async ({ clusterName, credentialType, credential }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setCredentials(clusterName, credentialType, credential, baseURL)
      showSuccessBanner(`Credentials for ${credentialType} set!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Setting credentials for ${credentialType} failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const sendCredentials = createGuardedAsyncThunk(
  'cluster/sendCredentials',
  async ({ clusterName, username, type }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.sendCredentials(clusterName, username, type, baseURL)
      showSuccessBanner('Credentials sent to email!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Sending credentials email failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const rotateDBCredential = createGuardedAsyncThunk(
  'cluster/rotateDBCredential',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.rotateDBCredential(clusterName, baseURL)
      showSuccessBanner('Database rotation successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Database rotation failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const rollingOptimize = createGuardedAsyncThunk('cluster/rollingOptimize', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rollingOptimize(clusterName, baseURL)
    showSuccessBanner('Rolling optimize successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling optimize failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

const rollingActionLabels = {
  restart: 'Rolling restart',
  reprov: 'Rolling reprov',
  upgrade: 'Rolling upgrade',
  'jobs-upgrade': 'Rolling jobs upgrade'
}

export const rollingAction = createGuardedAsyncThunk(
  'cluster/rollingAction',
  async ({ clusterName, action }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.rollingAction(clusterName, action, baseURL)
      showSuccessBanner(`${rollingActionLabels[action] ?? 'Rolling action'} successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`${rollingActionLabels[action] ?? 'Rolling action'} failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  },
  {
    condition: (_, { getState }) => {
      const { cluster } = getState()
      if (cluster.loadingStates.rollingAction) {
        return false
      }
    }
  }
)

export const rotateCertificates = createGuardedAsyncThunk(
  'cluster/rotateCertificates',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.rotateCertificates(clusterName, baseURL)
      showSuccessBanner('Rotate certificates successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Rotate certificates failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const reloadCertificates = createGuardedAsyncThunk(
  'cluster/reloadCertificates',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reloadCertificates(clusterName, baseURL)
      showSuccessBanner('Reload certificates successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reload certificates failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const cancelRollingRestart = createGuardedAsyncThunk(
  'cluster/cancelRollingRestart',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelRollingRestart(clusterName, baseURL)
      showSuccessBanner('Rolling restart cancelled!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Rolling restart cancellation failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const cancelRollingReprov = createGuardedAsyncThunk(
  'cluster/cancelRollingReprov',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelRollingReprov(clusterName, baseURL)
      showSuccessBanner('Rolling reprov cancelled!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Rolling reprov cancellation failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMasterSlave = createGuardedAsyncThunk(
  'cluster/bootstrapMasterSlave',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMasterSlave(clusterName, baseURL)
      showSuccessBanner('Master slave bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Master slave bootstrap failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMasterSlaveNoGtid = createGuardedAsyncThunk(
  'cluster/bootstrapMasterSlaveNoGtid',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMasterSlaveNoGtid(clusterName, baseURL)
      showSuccessBanner('Master slave positional bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Master slave positional bootstrap failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiMaster = createGuardedAsyncThunk(
  'cluster/bootstrapMultiMaster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiMaster(clusterName, baseURL)
      showSuccessBanner('Multi master bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multi master bootstrap failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiMasterRing = createGuardedAsyncThunk(
  'cluster/bootstrapMultiMasterRing',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiMasterRing(clusterName, baseURL)
      showSuccessBanner('Multi master ring bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multi master ring bootstrap failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiTierSlave = createGuardedAsyncThunk(
  'cluster/bootstrapMultiTierSlave',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiTierSlave(clusterName, baseURL)
      showSuccessBanner('Multitier slave bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multitier slave bootstrap failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const configReload = createGuardedAsyncThunk('cluster/configReload', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.configReload(clusterName, baseURL)
    showSuccessBanner('Config is reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Config reload failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const configDiscoverDB = createGuardedAsyncThunk(
  'cluster/configDiscoverDB',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.configDiscoverDB(clusterName, baseURL)
      showSuccessBanner('Databse discover config successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Databse discover config failed!', error.message, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const configDynamic = createGuardedAsyncThunk('cluster/configDynamic', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.configDynamic(clusterName, baseURL)
    showSuccessBanner('Databse apply dynamic config successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Databse apply dynamic config failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const checksumAllTables = createGuardedAsyncThunk(
  'cluster/checksumAllTables',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumAllTables(clusterName, baseURL)
      showSuccessBanner('Checksum all tables successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Checksum all tables failed!', error.message, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const checksumRepairAllTables = createGuardedAsyncThunk(
  'cluster/checksumRepairAllTables',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumRepairAllTables(clusterName, baseURL)
      showSuccessBanner('Repair Checksum all tables successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Repair Checksum all tables failed!', error.message, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)


export const setMaintenanceMode = createGuardedAsyncThunk(
  'cluster/setMaintenanceMode',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setMaintenanceMode(clusterName, serverId, baseURL)
      showSuccessBanner('Maintenance mode is set!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Setting Maintenance mode failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const jobsUpgrade = createGuardedAsyncThunk(
  'cluster/jobsUpgrade',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.jobsUpgrade(clusterName, serverId, baseURL)
      showSuccessBanner('Jobs upgrade initiated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Jobs upgrade failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const promoteToLeader = createGuardedAsyncThunk(
  'cluster/promoteToLeader',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.promoteToLeader(clusterName, serverId, baseURL)
      showSuccessBanner('Promote to leader successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Promote to leader failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const setAsUnrated = createGuardedAsyncThunk(
  'cluster/setAsUnrated',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setAsUnrated(clusterName, serverId, baseURL)
      showSuccessBanner('Failover candidate set as unrated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover candidate failed to set as unrated', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const setAsPreferred = createGuardedAsyncThunk(
  'cluster/setAsPreferred',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setAsPreferred(clusterName, serverId, baseURL)
      showSuccessBanner('Failover candidate set as preferred!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover candidate failed to set as preferred', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const setAsIgnored = createGuardedAsyncThunk(
  'cluster/setAsIgnored',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setAsIgnored(clusterName, serverId, baseURL)
      showSuccessBanner('Failover candidate set as ignored!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover candidate failed to set as ignored', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const reseedLogicalFromBackup = createGuardedAsyncThunk(
  'cluster/reseedLogicalFromBackup',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedLogicalFromBackup(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed logical from backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed logical from backup failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const reseedLogicalFromMaster = createGuardedAsyncThunk(
  'cluster/reseedLogicalFromMaster',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedLogicalFromMaster(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed logical from master successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed logical from master failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const reseedPhysicalFromBackup = createGuardedAsyncThunk(
  'cluster/reseedPhysicalFromBackup',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedPhysicalFromBackup(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed physical from backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed physical from backup failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const reseedFromResticSnapshot = createGuardedAsyncThunk(
  'cluster/reseedFromResticSnapshot',
  async ({ clusterName, serverId, snapshotId, method, strategy, cleanup, tempDir }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const payload = {
        clusterName,
        serverId,
        snapshotId,
        method: method || 'logical',
        strategy,
        cleanup,
        tempDir
      }
      const { data, status } = await clusterService.reseedFromResticSnapshot(
        payload.clusterName,
        payload.serverId,
        payload.snapshotId,
        payload.method,
        payload.strategy,
        payload.cleanup,
        payload.tempDir,
        baseURL
      )
      showSuccessBanner('Restic reseed initiated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Restic reseed failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const flushLogs = createGuardedAsyncThunk('cluster/flushLogs', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.flushLogs(clusterName, serverId, baseURL)
    showSuccessBanner('Logs flush successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Logs flush failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const physicalBackupMaster = createGuardedAsyncThunk(
  'cluster/physicalBackupMaster',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.physicalBackupMaster(clusterName, serverId, baseURL)
      showSuccessBanner('Physical master backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Physical master backup failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const logicalBackup = createGuardedAsyncThunk(
  'cluster/logicalBackup',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.logicalBackup(clusterName, serverId, baseURL)
      showSuccessBanner('Logical backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Logical backup failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const stopDatabase = createGuardedAsyncThunk(
  'cluster/stopDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.stopDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Database is stopped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Stopping database failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const abortDatabase = createGuardedAsyncThunk(
  'cluster/abortDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.abortDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Database orchestration aborted!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Aborting database orchestration failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const clearDatabase = createGuardedAsyncThunk(
  'cluster/clearDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.clearDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Database instance state cleared!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Clearing database instance state failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const startDatabase = createGuardedAsyncThunk(
  'cluster/startDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.startDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Database has started!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      console.log('error in startDatabase::', error)
      showErrorBanner('Starting database failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const restartDatabase = createGuardedAsyncThunk(
  'cluster/restartDatabase',
  async ({ clusterName, serverId, rid }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.restartDatabase(clusterName, serverId, rid, baseURL)
      showSuccessBanner('Database has restarted!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      console.log('error in restartDatabase::', error)
      showErrorBanner('Restarting database failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const upgradeDatabase = createGuardedAsyncThunk(
  'cluster/upgradeDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.upgradeDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Database upgrade started!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      console.log('error in upgradeDatabase::', error)
      showErrorBanner('Database upgrade failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const provisionDatabase = createGuardedAsyncThunk(
  'cluster/provisionDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.provisionDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Provision database successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Provision database failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const unprovisionDatabase = createGuardedAsyncThunk(
  'cluster/unprovisionDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Unprovision database successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision database failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const updateOpensvcTemplate = createGuardedAsyncThunk(
  'cluster/updateOpensvcTemplate',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateOpensvcTemplate(clusterName, serverId, baseURL)
      showSuccessBanner('OpenSVC template updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('OpenSVC template update failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const runRemoteJobs = createGuardedAsyncThunk(
  'cluster/runRemoteJobs',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.runRemoteJobs(clusterName, serverId, baseURL)
      showSuccessBanner('Remote jobs started!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Remote jobs failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const optimizeServer = createGuardedAsyncThunk(
  'cluster/optimizeServer',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.optimizeServer(clusterName, serverId, baseURL)
      showSuccessBanner('Database optimize successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Database optimize failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const skipReplicationEvent = createGuardedAsyncThunk(
  'cluster/skipReplicationEvent',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.skipReplicationEvent(clusterName, serverId, baseURL)
      showSuccessBanner('Replication event skipped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Skipping Replication event failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const toggleInnodbMonitor = createGuardedAsyncThunk(
  'cluster/toggleInnodbMonitor',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleInnodbMonitor(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle Innodb Monitor successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle Innodb Monitor failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const toggleSlowQueryCapture = createGuardedAsyncThunk(
  'cluster/toggleSlowQueryCapture',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleSlowQueryCapture(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle Slow Query Capture successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle Slow Query Capture failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const startSlave = createGuardedAsyncThunk('cluster/startSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startSlave(clusterName, serverId, baseURL)
    showSuccessBanner('Slave has started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting slave failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const stopSlave = createGuardedAsyncThunk('cluster/stopSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopSlave(clusterName, serverId, baseURL)
    showSuccessBanner('Slave has stopped!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting slave failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const toggleReadOnly = createGuardedAsyncThunk(
  'cluster/toggleReadOnly',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleReadOnly(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle readonly successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle readonly failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const killThread = createGuardedAsyncThunk(
  'cluster/killThread',
  async ({ clusterName, serverId, queryDigest }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.killThread(clusterName, serverId, queryDigest, baseURL)
      if (status === 200) {
        showSuccessBanner('Thread killed successfully!', status, thunkAPI)
      } else {
        showErrorBanner('Thread kill failed!', status, thunkAPI)
      }
      return { data, status }
    } catch (error) {
      showErrorBanner('Thread kill failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const killQuery = createGuardedAsyncThunk(
  'cluster/killQuery',
  async ({ clusterName, serverId, queryDigest }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.killQuery(clusterName, serverId, queryDigest, baseURL)
      if (status === 200) {
        showSuccessBanner('Query killed successfully!', status, thunkAPI)
      } else {
        showErrorBanner('Query kill failed!', status, thunkAPI)
      }
      return { data, status }
    } catch (error) {
      showErrorBanner('Query kill failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const resetMaster = createGuardedAsyncThunk(
  'cluster/resetMaster',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resetMaster(clusterName, serverId, baseURL)
      showSuccessBanner('Reset Master successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reset Master failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const resetSlaveAll = createGuardedAsyncThunk(
  'cluster/resetSlaveAll',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resetSlaveAll(clusterName, serverId, baseURL)
      showSuccessBanner('Reset Slave successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reset Slave failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const cancelServerJob = createGuardedAsyncThunk(
  'cluster/cancelServerJob',
  async ({ clusterName, serverId, taskName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelServerJob(clusterName, serverId, taskName, baseURL)
      showSuccessBanner(`Job ${taskName} cancelled successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Cancellation of job ${taskName} failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const provisionProxy = createGuardedAsyncThunk(
  'cluster/provisionProxy',
  async ({ clusterName, proxyId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.provisionProxy(clusterName, proxyId, baseURL)
      showSuccessBanner('Provision proxy successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Provision proxy failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const unprovisionProxy = createGuardedAsyncThunk(
  'cluster/unprovisionProxy',
  async ({ clusterName, proxyId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionProxy(clusterName, proxyId, baseURL)
      showSuccessBanner('Unprovision proxy successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision proxy failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const startProxy = createGuardedAsyncThunk('cluster/startProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Starting proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting proxy failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const stopProxy = createGuardedAsyncThunk('cluster/stopProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Stopping proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping proxy failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const abortProxy = createGuardedAsyncThunk('cluster/abortProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.abortProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Proxy orchestration aborted!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Aborting proxy orchestration failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const clearProxy = createGuardedAsyncThunk('cluster/clearProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.clearProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Proxy instance state cleared!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Clearing proxy instance state failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const stagingProxy = createGuardedAsyncThunk(
  'cluster/stagingProxy',
  async ({ clusterName, proxyId, staging }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.stagingProxy(clusterName, proxyId, staging, baseURL)
      showSuccessBanner('Staging proxy successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Staging proxy failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const runSysBench = createGuardedAsyncThunk('cluster/runSysBench', async ({ clusterName, thread }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.runSysbench(clusterName, thread, baseURL)
    showSuccessBanner('Sysbench ran successfuly!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Sysbench failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const runRegressionTests = createGuardedAsyncThunk(
  'cluster/runRegressionTests',
  async ({ clusterName, testName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.runRegressionTests(clusterName, testName, baseURL)
      showSuccessBanner('Regression test ran successfuly!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Regression test failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const getDatabaseService = createGuardedAsyncThunk(
  'cluster/getDatabaseService',
  async ({ clusterName, serviceName, dbId, queryParams = {} }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getDatabaseService(
        clusterName,
        serviceName,
        dbId,
        baseURL,
        queryParams
      )
      if (status === 200) {
        return { data, status }
      }

      throw new Error(data)
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const preserveVariable = createGuardedAsyncThunk(
  'cluster/preserveVariable',
  async ({ clusterName, dbId, variableName, action }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.preserveVariable(clusterName, dbId, variableName, action, baseURL)
      if (status === 200) {
        showSuccessBanner(
          `Variable ${variableName} ${action === 'preserve' ? 'preserved' : action === 'accept' ? 'accepted' : 'cleared'} successfully!`,
          status,
          thunkAPI
        )
        return { data, status, dbId }
      }

      throw new Error(data)
    } catch (error) {
      showErrorBanner(`Failed to ${action} variable ${variableName}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const setCustomVariableValue = createGuardedAsyncThunk(
  'cluster/setCustomVariableValue',
  async ({ clusterName, dbId, variableName, customValue }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setCustomVariableValue(
        clusterName,
        dbId,
        variableName,
        customValue,
        baseURL
      )
      if (status === 200) {
        showSuccessBanner(`Variable ${variableName} custom value set successfully!`, status, thunkAPI)
        return { data, status, dbId }
      }

      throw new Error(data)
    } catch (error) {
      showErrorBanner(`Failed to set custom value for variable ${variableName}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const updateLongQueryTime = createGuardedAsyncThunk(
  'cluster/updateLongQueryTime',
  async ({ clusterName, dbId, time }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateLongQueryTime(clusterName, dbId, time, baseURL)
      showSuccessBanner('Long query time updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Long query time update failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const checksumTable = createGuardedAsyncThunk(
  'cluster/checksumTable',
  async ({ clusterName, schema, table }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumTable(clusterName, schema, table, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema} and table ${table}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema} and table ${table}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const checksumRepairTable = createGuardedAsyncThunk(
  'cluster/checksumRepairTable',
  async ({ clusterName, schema, table }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumRepairTable(clusterName, schema, table, baseURL)
      showSuccessBanner(`Rapair done for schema ${schema} and table ${table}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum Repair failed for schema ${schema} and table ${table}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const checksumSchema = createGuardedAsyncThunk(
  'cluster/checksumSchema',
  async ({ clusterName, schema, Schema }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumSchema(clusterName, schema, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const checksumRepairSchema = createGuardedAsyncThunk(
  'cluster/checksumRepairSchema',
  async ({ clusterName, schema, Schema }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumRepairSchema(clusterName, schema, baseURL)
      showSuccessBanner(`Checksum Repair done for schema ${schema}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum Repair failed for schema ${schema}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)


export const toggleDatabaseActions = createGuardedAsyncThunk(
  'cluster/toggleDatabaseActions',
  async ({ clusterName, dbId, serviceName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleDatabaseActions(clusterName, serviceName, dbId, baseURL)
      showSuccessBanner(`Toggle ${serviceName} successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Toggle ${serviceName} failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const analyzeAllTables = createGuardedAsyncThunk(
  'cluster/analyzeAllTables',
  async ({ clusterName, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeAllTables(clusterName, persistent, baseURL)
      showSuccessBanner(`Checksum done for all schema!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for all schema!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const analyzeSchema = createGuardedAsyncThunk(
  'cluster/analyzeSchema',
  async ({ clusterName, schema, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeSchema(clusterName, schema, persistent, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const analyzeTable = createGuardedAsyncThunk(
  'cluster/analyzeTable',
  async ({ clusterName, schema, table, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeTable(clusterName, schema, table, persistent, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema} and table ${table}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema} and table ${table}!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const addUser = createGuardedAsyncThunk(
  'cluster/addUser',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.addUser(clusterName, username, grants, roles, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const updateGrants = createGuardedAsyncThunk(
  'cluster/updateGrants',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateGrants(clusterName, username, grants, roles, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const dropUser = createGuardedAsyncThunk(
  'cluster/dropUser',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropUser(clusterName, username, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const clusterSubscribe = createGuardedAsyncThunk(
  'auth/clusterSubscribe',
  async ({ clusterName, baseURL }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.clusterSubscribe(clusterName, baseURL)
      if (status === 200) {
        showSuccessBanner(`Register user to peer cluster sent!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Register user to peer cluster failed!`, error, thunkAPI)
      const errorMessage = error.message || 'Request failed'
      const errorStatus = error.errorStatus || 500 // Default error status if not provided
      // Handle errors (including custom errorStatus)
      return thunkAPI.rejectWithValue({ errorMessage, errorStatus }) // Pass the entire Error object to the rejected action
    }
  }
)

export const acceptSubscription = createGuardedAsyncThunk(
  'cluster/acceptSubscription',
  async ({ clusterName, username }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.acceptSubscription(clusterName, username, baseURL)
      if (status === 200) {
        showSuccessBanner(`Subscription accepted successfully!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Accept subscription failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const rejectSubscription = createGuardedAsyncThunk(
  'cluster/rejectSubscription',
  async ({ clusterName, username }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.rejectSubscription(clusterName, username, baseURL)
      showSuccessBanner(`Subscription rejected successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Reject subscription failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const endSubscription = createGuardedAsyncThunk(
  'cluster/endSubscription',
  async ({ clusterName, username }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.endSubscription(clusterName, username, baseURL)
      showSuccessBanner(`Subscription ended successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Failed to end subscription!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const refreshStaging = createGuardedAsyncThunk('cluster/refreshStaging', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.refreshStaging(clusterName, baseURL)
    showSuccessBanner(`Refresh staging initiated successfully!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Failed to initiate refresh staging!`, error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const reseedStagingFromParent = createGuardedAsyncThunk(
  'cluster/reseedStagingFromParent',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedStagingFromParent(clusterName, baseURL)
      showSuccessBanner(`Staging script reloaded successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Failed to reload staging script!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const subscribeExternalRole = createGuardedAsyncThunk(
  'cluster/subscribeExternalRole',
  async ({ clusterName, username, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.subscribeExternalRole(clusterName, username, roles, baseURL)
      if (status === 200) {
        showSuccessBanner(`Role '${roles}' for '${username}' is requested successful!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`request external role '${roles}' for '${username}' failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const quoteExternalRole = createGuardedAsyncThunk(
  'cluster/quoteExternalRole',
  async ({ clusterName, username, roles, cost }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.quoteExternalRole(clusterName, username, roles, cost, baseURL)
      if (status === 200) {
        showSuccessBanner(`Role '${roles}' for '${username}' is quoteed successful!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Failed sending quotation to external role '${roles}' for '${username}'!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const acceptExternalRole = createGuardedAsyncThunk(
  'cluster/acceptExternalRole',
  async ({ clusterName, username, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.acceptExternalRole(clusterName, username, roles, baseURL)
      if (status === 200) {
        showSuccessBanner(`Role '${roles}' for '${username}' is accepted successful!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Failed to accept external role '${roles}' for '${username}'!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const refuseExternalRole = createGuardedAsyncThunk(
  'cluster/refuseExternalRole',
  async ({ clusterName, username, roles, reason }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.refuseExternalRole(clusterName, username, roles, reason, baseURL)
      if (status === 200) {
        showSuccessBanner(`Role '${roles}' for '${username}' is refused !`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Failed to refuse role '${roles}' for '${username}'!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const endExternalRole = createGuardedAsyncThunk(
  'cluster/endExternalRole',
  async ({ clusterName, username, roles, reason }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.endExternalRole(clusterName, username, roles, reason, baseURL)
      if (status === 200) {
        showSuccessBanner(`Role '${roles}' is deactivated from '${username}'!`, status, thunkAPI)
        return { data, status }
      } else {
        throw new Error(data)
      }
    } catch (error) {
      showErrorBanner(`Failed to deactivate external role '${roles}' from '${username}'!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const addClusterShard = createGuardedAsyncThunk(
  'cluster/addClusterShard',
  async ({ clusterName, clusterShard, formdata }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.addClusterShard(clusterName, clusterShard, formdata)
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
  }
)

export const getClusterApps = createGuardedAsyncThunk('cluster/getClusterApps', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterApps(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    return handleError(error, thunkAPI)
  }
})

export const provisionApp = createGuardedAsyncThunk(
  'cluster/provisionApp',
  async ({ clusterName, appId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.provisionApp(clusterName, appId, baseURL)
      showSuccessBanner('Provision app successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Provision app failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const unprovisionApp = createGuardedAsyncThunk(
  'cluster/unprovisionApp',
  async ({ clusterName, appId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionApp(clusterName, appId, baseURL)
      showSuccessBanner('Unprovision app successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision app failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const updateRoutesApp = createGuardedAsyncThunk(
  'cluster/updateRoutesApp',
  async ({ clusterName, appId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateRoutesApp(clusterName, appId, baseURL)
      showSuccessBanner('Update routes successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Update routes failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const startApp = createGuardedAsyncThunk('cluster/startApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startApp(clusterName, appId, baseURL)
    showSuccessBanner('Starting app successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting app failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const stopApp = createGuardedAsyncThunk('cluster/stopApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopApp(clusterName, appId, baseURL)
    showSuccessBanner('Stopping app successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping app failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const abortApp = createGuardedAsyncThunk('cluster/abortApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.abortApp(clusterName, appId, baseURL)
    showSuccessBanner('App orchestration aborted!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Aborting app orchestration failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const clearApp = createGuardedAsyncThunk('cluster/clearApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.clearApp(clusterName, appId, baseURL)
    showSuccessBanner('App instance state cleared!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Clearing app instance state failed!', error, thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const restartApp = createGuardedAsyncThunk(
  'cluster/restartApp',
  async ({ clusterName, appId, rid = '' }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.restartApp(clusterName, appId, rid, baseURL)
      showSuccessBanner('Restarting app successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Restarting app failed!', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const getAppService = createGuardedAsyncThunk(
  'cluster/getAppService',
  async ({ clusterName, serviceName, appId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getAppService(clusterName, serviceName, appId, baseURL)
      if (status === 200) {
        return { data, status }
      }

      throw new Error(data)
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const resolveTemplateVariables = createGuardedAsyncThunk(
  'cluster/resolveTemplateVariables',
  async ({ clusterName, appId, rawValue }, thunkAPI) => {
    try {
      console.log('resolveTemplateVariables', clusterName, appId, rawValue)
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resolveTemplateVariables(clusterName, appId, rawValue, baseURL)
      // showSuccessBanner(`Template variables resolved!`, status, thunkAPI)
      if (status !== 200) {
        throw new Error(data)
      }
      return { data, status }
    } catch (error) {
      showErrorBanner(`Resolving template variables failed!`, error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const deploymentFieldChange = createGuardedAsyncThunk(
  'cluster/deploymentFieldChange',
  async ({ clusterName, appId, field, index, key, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.deploymentFieldChange(
        clusterName,
        appId,
        field,
        index,
        key,
        value,
        baseURL
      )
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('Deployment field updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while updating deployment field', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const deploymentFieldIndexAdd = createGuardedAsyncThunk(
  'cluster/deploymentFieldIndexAdd',
  async ({ clusterName, appId, field, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.deploymentFieldIndexAdd(clusterName, appId, field, value, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('New deployment field row added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new deployment field row', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)
export const deploymentFieldIndexDrop = createGuardedAsyncThunk(
  'cluster/deploymentFieldIndexDrop',
  async ({ clusterName, appId, field, index }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.deploymentFieldIndexDrop(clusterName, appId, field, index, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('Deployment field row dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a deployment field row', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const storageFieldChange = createGuardedAsyncThunk(
  'cluster/storageFieldChange',
  async ({ clusterName, appId, field, index, key, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldChange(
        clusterName,
        appId,
        field,
        index,
        key,
        value,
        baseURL
      )
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('storage field updated!', status, thunkAPI)
      thunkAPI.dispatch(getAppService({ clusterName, serviceName: 'deployment', appId }))
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while updating storage field', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const storageFieldIndexAdd = createGuardedAsyncThunk(
  'cluster/storageFieldIndexAdd',
  async ({ clusterName, appId, field, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldIndexAdd(clusterName, appId, field, value, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('New storage field row added!', status, thunkAPI)
      thunkAPI.dispatch(getAppService({ clusterName, serviceName: 'deployment', appId }))
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new storage field row', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)
export const storageFieldIndexDrop = createGuardedAsyncThunk(
  'cluster/storageFieldIndexDrop',
  async ({ clusterName, appId, field, index }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldIndexDrop(clusterName, appId, field, index, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('storage field row dropped!', status, thunkAPI)
      thunkAPI.dispatch(getAppService({ clusterName, serviceName: 'deployment', appId }))
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a storage field row', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const connectDockerRegistry = createGuardedAsyncThunk(
  'cluster/connectDockerRegistry',
  async ({ clusterName, dockerRegistry }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.connectDockerRegistry(clusterName, dockerRegistry, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('New server added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new server', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

export const monitorAllSchemas = createGuardedAsyncThunk(
  'cluster/monitorAllSchemas',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.monitorAllSchemas(clusterName, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('All schemas are now monitored!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while monitoring all schemas', error, thunkAPI)
      return handleError(error, thunkAPI)
    }
  }
)

// S3 provider CRUD thunks
export const addS3Provider = createGuardedAsyncThunk(
  'cluster/addS3Provider',
  async ({ clusterName, payload }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.addS3Provider(clusterName, payload, baseURL)
      if (status !== 200 && status !== 201) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      let refreshError = null
      try {
        await thunkAPI.dispatch(getClusterData({ clusterName })).unwrap()
      } catch (err) {
        refreshError = err?.errorMessage || err?.message || String(err)
      }
      return { data, status, refreshError }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const modifyS3Provider = createGuardedAsyncThunk(
  'cluster/modifyS3Provider',
  async ({ clusterName, name, payload }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.modifyS3Provider(clusterName, name, payload, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      thunkAPI.dispatch(getClusterData({ clusterName }))
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const dropS3Provider = createGuardedAsyncThunk(
  'cluster/dropS3Provider',
  async ({ clusterName, name }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropS3Provider(clusterName, name, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      thunkAPI.dispatch(getClusterData({ clusterName }))
      return { data, status }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

export const getS3ProviderReferences = createGuardedAsyncThunk(
  'cluster/getS3ProviderReferences',
  async ({ clusterName, providerName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getS3ProviderReferences(clusterName, providerName, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      return { data }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

// previewS3MountSync performs a dry-run diff for a single mount against its provider.
// Payload: { clusterName, providerName, appId, mountName }
// Returns the SyncPreviewResponse from the server.
export const previewS3MountSync = createGuardedAsyncThunk(
  'cluster/previewS3MountSync',
  async ({ clusterName, providerName, appId, mountName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const targets = [{ appId, mountName }]
      const { data, status } = await clusterService.syncS3ProviderPreview(clusterName, providerName, targets, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      return { data }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

// applyS3MountSync applies provider-managed fields to a single mount.
// Payload: { clusterName, providerName, appId, mountName, revisionToken }
// Returns the SyncApplyResponse from the server.
export const applyS3MountSync = createGuardedAsyncThunk(
  'cluster/applyS3MountSync',
  async ({ clusterName, providerName, appId, mountName, revisionToken }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const targets = [{ appId, mountName }]
      const { data, status } = await clusterService.syncS3ProviderApply(clusterName, providerName, targets, revisionToken, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      return { data }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

// previewS3ProviderBulkSync performs a dry-run diff for selected mounts.
// Payload: { clusterName, providerName, targets: [{ appId, mountName }] }
export const previewS3ProviderBulkSync = createGuardedAsyncThunk(
  'cluster/previewS3ProviderBulkSync',
  async ({ clusterName, providerName, targets }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.syncS3ProviderPreview(clusterName, providerName, targets, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      return { data }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

// applyS3ProviderBulkSync applies provider-managed fields for selected mounts.
// Payload: { clusterName, providerName, targets: [{ appId, mountName }], revisionToken }
export const applyS3ProviderBulkSync = createGuardedAsyncThunk(
  'cluster/applyS3ProviderBulkSync',
  async ({ clusterName, providerName, targets, revisionToken }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.syncS3ProviderApply(clusterName, providerName, targets, revisionToken, baseURL)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : JSON.stringify(data))
      }
      return { data }
    } catch (error) {
      return handleError(error, thunkAPI)
    }
  }
)

const initialState = {
  loading: false,
  pendingThunks: {},
  error: null,
  clusterApps: null,
  clusterAppStates: null,
  clusterData: null,
  clusterAlerts: null,
  clusterLogs: {
    general: null,
    task: null,
    ddl: null,
    'variable-change': null
  },
  clusterMaster: null,
  clusterServers: null,
  clusterProxies: null,
  clusterProxiesStaging: null,
  clusterCertificates: null,
  clusterStates: null,
  backups: {
    list: null,
    stats: null
  },
  restic: {
    snapshots: null,
    stats: null,
    queue: null,
    currentTask: null,
    repoPath: ''
  },
  topProcess: null,
  opensvcStats: null,
  opensvcPools: null,
  jobs: null,
  shardSchema: null,
  queryRules: null,
  refreshInterval: 0,
  loadingStates: {
    switchOver: false,
    failOver: false,
    menuActions: false,
    rollingAction: false
  },
  app: {
    substitution: null,
    deployment: null,
    serviceOpensvc: null
  },
  database: {
    processList: null,
    status: {
      statusDelta: null,
      statusInnoDB: null
    },
    slowQueries: null,
    digestQueries: null,
    tables: null,
    errors: null,
    sqlerrors: null,
    auditlogs: null,
    variables: null,
    serviceOpensvc: null,
    metadataLocks: null,
    responsetime: null
  }
}

export const clusterSlice = createSlice({
  name: 'cluster',
  initialState,
  reducers: {
    setRefreshInterval: (state, action) => {
      localStorage.setItem('refresh_interval', action.payload.interval)
      state.refreshInterval = action.payload.interval
    },
    pauseAutoReload: (state, action) => {
      if (action.payload.isPaused) {
        localStorage.setItem('pause_auto_reload', true)
      } else {
        localStorage.removeItem('pause_auto_reload')
      }
    },
    setCluster: (state, action) => {
      state.clusterData = action.payload.data
    },
    clearCluster: (state, action) => {
      Object.assign(state, initialState)
    }
  },
  extraReducers: (builder) => {
    builder.addCase(preserveVariable.fulfilled, (state, action) => {
      // Refresh the variables after preserve/accept/clear action
      // This will be handled by the component dispatching getDatabaseService again
    })

    builder.addMatcher(
      (action) => action.type.endsWith('/pending') && shouldTrackThunk(action),
      (state, action) => {
        const typePrefix = getThunkTypePrefix(action.type)
        const pendingKey = getPendingKey(typePrefix, action.meta?.arg)
        state.pendingThunks[pendingKey] = true
      }
    )

    builder.addMatcher(
      isAnyOf(
        getClusterData.fulfilled,
        getClusterAlerts.fulfilled,
        getClusterLogs.fulfilled,
        getClusterMaster.fulfilled,
        getClusterServers.fulfilled,
        getClusterProxies.fulfilled,
        getClusterApps.fulfilled,
        getClusterCertificates.fulfilled,
        getDatabaseService.fulfilled,
        getTopProcess.fulfilled,
        getOpenSVCStats.fulfilled,
        getOpenSVCPools.fulfilled,
        getShardSchema.fulfilled,
        getQueryRules.fulfilled,
        getBackups.fulfilled,
        getBackupStats.fulfilled,
        getResticSnapshot.fulfilled,
        getResticStats.fulfilled,
        getResticCurrentTask.fulfilled,
        getResticQueue.fulfilled,
        getJobs.fulfilled
      ),
      (state, action) => {
        const typePrefix = getThunkTypePrefix(action.type)
        const handler = fulfilledHandlers[typePrefix]
        if (handler) {
          handler(state, action)
          return
        }

        if (typePrefix === 'cluster/getClusterLogs') {
          state.clusterLogs = action.payload.data || {}
          return
        }

        if (typePrefix === 'cluster/getClusterServers') {
          if (action.payload?.data && action.meta.arg?.clusterName == state.clusterData?.name) {
            state.clusterServers = action.payload.data
            state.clusterStates = buildClusterStateSignature(action.payload?.data)
          }
          return
        }

        if (typePrefix === 'cluster/getClusterApps') {
          state.clusterApps = action.payload?.data
          state.clusterAppStates = buildClusterStateSignature(action.payload?.data)
          return
        }

        if (typePrefix === 'cluster/getClusterProxies') {
          state.clusterProxies = action.payload?.data
          state.clusterProxiesStaging = buildProxyStagingList(action.payload?.data)
          return
        }

        if (typePrefix === 'cluster/getDatabaseService') {
          handleDatabaseServiceFulfilled(state, action)
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
        dropServer.pending,
        dropApp.pending,
        toggleTraffic.pending,
        toggleTrafficStaging.pending,
        provisionCluster.pending,
        unProvisionCluster.pending,
        sendCredentials.pending,
        rotateDBCredential.pending,
        rollingOptimize.pending,
        rotateCertificates.pending,
        reloadCertificates.pending,
        cancelRollingRestart.pending,
        cancelRollingReprov.pending,
        bootstrapMasterSlave.pending,
        bootstrapMasterSlaveNoGtid.pending,
        bootstrapMultiMaster.pending,
        bootstrapMultiMasterRing.pending,
        bootstrapMultiTierSlave.pending,
        configReload.pending,
        configDiscoverDB.pending,
        configDynamic.pending,
        setMaintenanceMode.pending,
        promoteToLeader.pending,
        setAsUnrated.pending,
        setAsPreferred.pending,
        setAsIgnored.pending,
        reseedLogicalFromBackup.pending,
        reseedLogicalFromMaster.pending,
        reseedPhysicalFromBackup.pending,
        reseedFromResticSnapshot.pending,
        flushLogs.pending,
        physicalBackupMaster.pending,
        logicalBackup.pending,
        stopDatabase.pending,
        abortDatabase.pending,
        clearDatabase.pending,
        startDatabase.pending,
        restartDatabase.pending,
        provisionDatabase.pending,
        unprovisionDatabase.pending,
        updateOpensvcTemplate.pending,
        runRemoteJobs.pending,
        optimizeServer.pending,
        skipReplicationEvent.pending,
        toggleInnodbMonitor.pending,
        toggleSlowQueryCapture.pending,
        startSlave.pending,
        stopSlave.pending,
        toggleReadOnly.pending,
        resetMaster.pending,
        resetSlaveAll.pending,
        provisionProxy.pending,
        unprovisionProxy.pending,
        startProxy.pending,
        stopProxy.pending,
        abortProxy.pending,
        clearProxy.pending,
        provisionApp.pending,
        unprovisionApp.pending,
        updateRoutesApp.pending,
        startApp.pending,
        stopApp.pending,
        restartApp.pending,
        abortApp.pending,
        clearApp.pending,
        refreshStaging.pending,
        killThread.pending,
        killQuery.pending,
        monitorAllSchemas.pending
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
        dropServer.fulfilled,
        dropApp.fulfilled,
        toggleTrafficStaging.fulfilled,
        provisionCluster.fulfilled,
        unProvisionCluster.fulfilled,
        sendCredentials.fulfilled,
        rotateDBCredential.fulfilled,
        rollingOptimize.fulfilled,
        rotateCertificates.fulfilled,
        reloadCertificates.fulfilled,
        cancelRollingRestart.fulfilled,
        cancelRollingReprov.fulfilled,
        bootstrapMasterSlave.fulfilled,
        bootstrapMasterSlaveNoGtid.fulfilled,
        bootstrapMultiMaster.fulfilled,
        bootstrapMultiMasterRing.fulfilled,
        bootstrapMultiTierSlave.fulfilled,
        configReload.fulfilled,
        configDiscoverDB.fulfilled,
        configDynamic.fulfilled,
        setMaintenanceMode.fulfilled,
        promoteToLeader.fulfilled,
        setAsUnrated.fulfilled,
        setAsPreferred.fulfilled,
        setAsIgnored.fulfilled,
        reseedLogicalFromBackup.fulfilled,
        reseedLogicalFromMaster.fulfilled,
        reseedPhysicalFromBackup.fulfilled,
        reseedFromResticSnapshot.fulfilled,
        flushLogs.fulfilled,
        physicalBackupMaster.fulfilled,
        logicalBackup.fulfilled,
        stopDatabase.fulfilled,
        abortDatabase.fulfilled,
        clearDatabase.fulfilled,
        startDatabase.fulfilled,
        restartDatabase.fulfilled,
        provisionDatabase.fulfilled,
        unprovisionDatabase.fulfilled,
        updateOpensvcTemplate.fulfilled,
        runRemoteJobs.fulfilled,
        optimizeServer.fulfilled,
        skipReplicationEvent.fulfilled,
        toggleInnodbMonitor.fulfilled,
        toggleSlowQueryCapture.fulfilled,
        startSlave.fulfilled,
        stopSlave.fulfilled,
        toggleReadOnly.fulfilled,
        resetMaster.fulfilled,
        resetSlaveAll.fulfilled,
        provisionProxy.fulfilled,
        unprovisionProxy.fulfilled,
        startProxy.fulfilled,
        stopProxy.fulfilled,
        abortProxy.fulfilled,
        clearProxy.fulfilled,
        provisionApp.fulfilled,
        unprovisionApp.fulfilled,
        updateRoutesApp.fulfilled,
        startApp.fulfilled,
        stopApp.fulfilled,
        restartApp.fulfilled,
        abortApp.fulfilled,
        clearApp.fulfilled,
        refreshStaging.fulfilled,
        killThread.fulfilled,
        killQuery.fulfilled,
        monitorAllSchemas.fulfilled
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
        dropServer.rejected,
        dropApp.rejected,
        toggleTraffic.rejected,
        toggleTrafficStaging.rejected,
        provisionCluster.rejected,
        unProvisionCluster.rejected,
        sendCredentials.rejected,
        rotateDBCredential.rejected,
        rollingOptimize.rejected,
        rotateCertificates.rejected,
        reloadCertificates.rejected,
        cancelRollingRestart.rejected,
        cancelRollingReprov.rejected,
        bootstrapMasterSlave.rejected,
        bootstrapMasterSlaveNoGtid.rejected,
        bootstrapMultiMaster.rejected,
        bootstrapMultiMasterRing.rejected,
        bootstrapMultiTierSlave.rejected,
        configReload.rejected,
        configDiscoverDB.rejected,
        configDynamic.rejected,
        setMaintenanceMode.rejected,
        promoteToLeader.rejected,
        setAsUnrated.rejected,
        setAsPreferred.rejected,
        setAsIgnored.rejected,
        reseedLogicalFromBackup.rejected,
        reseedLogicalFromMaster.rejected,
        reseedPhysicalFromBackup.rejected,
        reseedFromResticSnapshot.rejected,
        flushLogs.rejected,
        physicalBackupMaster.rejected,
        logicalBackup.rejected,
        stopDatabase.rejected,
        abortDatabase.rejected,
        clearDatabase.rejected,
        startDatabase.rejected,
        restartDatabase.rejected,
        provisionDatabase.rejected,
        unprovisionDatabase.rejected,
        updateOpensvcTemplate.rejected,
        runRemoteJobs.rejected,
        optimizeServer.rejected,
        skipReplicationEvent.rejected,
        toggleInnodbMonitor.rejected,
        toggleSlowQueryCapture.rejected,
        startSlave.rejected,
        stopSlave.rejected,
        toggleReadOnly.rejected,
        resetMaster.rejected,
        resetSlaveAll.rejected,
        provisionProxy.rejected,
        unprovisionProxy.rejected,
        startProxy.rejected,
        stopProxy.rejected,
        abortProxy.rejected,
        clearProxy.rejected,
        provisionApp.rejected,
        unprovisionApp.rejected,
        updateRoutesApp.rejected,
        startApp.rejected,
        stopApp.rejected,
        restartApp.rejected,
        abortApp.rejected,
        clearApp.rejected,
        refreshStaging.rejected,
        killThread.rejected,
        killQuery.rejected,
        monitorAllSchemas.rejected
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
    builder.addMatcher(isAnyOf(rollingAction.pending), (state) => {
      state.loadingStates.rollingAction = true
    })
    builder.addMatcher(isAnyOf(rollingAction.fulfilled, rollingAction.rejected), (state) => {
      state.loadingStates.rollingAction = false
    })
    builder.addMatcher(isAnyOf(getAppService.fulfilled), (state, action) => {
      const { serviceName } = action.meta.arg
      if (serviceName === 'deployment') {
        if (!state.app.deployment || !isEqual(state.app.deployment, action.payload.data)) {
          state.app.deployment = action.payload.data
        }
      } else if (serviceName === 'substitution') {
        if (!state.app.substitution || !isEqual(state.app.substitution, action.payload.data)) {
          state.app.substitution = action.payload.data
        }
      } else if (serviceName === 'service-opensvc') {
        if (!state.app.serviceOpensvc || !isEqual(state.app.serviceOpensvc, action.payload.data)) {
          state.app.serviceOpensvc = action.payload.data
        }
      }
    })
    builder.addMatcher(
      (action) => (action.type.endsWith('/fulfilled') || action.type.endsWith('/rejected')) && shouldTrackThunk(action),
      (state, action) => {
        const typePrefix = getThunkTypePrefix(action.type)
        const pendingKey = getPendingKey(typePrefix, action.meta?.arg)
        state.pendingThunks[pendingKey] = false
      }
    )
  }
})

export const { setRefreshInterval, setCluster, clearCluster, pauseAutoReload } = clusterSlice.actions

// Selector for cluster-level saved S3 providers (credentials already masked by backend).
// Returns an empty array when cluster data is not yet loaded.
export const selectClusterS3Providers = (state) => state.cluster?.clusterData?.clusterS3Providers ?? []

// this is for configureStore
export default clusterSlice.reducer
