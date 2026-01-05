import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { get, isEqual } from 'lodash';

export const getClusterData = createAsyncThunk('cluster/getClusterData', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterData(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},
  // Add a condition to prevent the action from being dispatched if the user is already fetching the info
  {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.cluster) {
        return false;
      }
    }
  });

export const getClusterAlerts = createAsyncThunk('cluster/getClusterAlerts', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterAlerts(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.alerts) {
      return false;
    }
  }
});

export const getClusterLogs = createAsyncThunk('cluster/getClusterLogs', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterLogs(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.logs) {
      return false;
    }
  }
});

export const getClusterMaster = createAsyncThunk('cluster/getClusterMaster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterMaster(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},
  // Add a condition to prevent the action from being dispatched if the user is already fetching the info
  {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.master) {
        return false;
      }
    }
  });

export const getClusterServers = createAsyncThunk('cluster/getClusterServers', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterServers(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},
  // Add a condition to prevent the action from being dispatched if the user is already fetching the info
  {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.servers) {
        return false;
      }
    }
  });

export const getClusterProxies = createAsyncThunk('cluster/getClusterProxies', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterProxies(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},
  // Add a condition to prevent the action from being dispatched if the user is already fetching the info
  {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.proxies) {
        return false;
      }
    }
  });

export const getClusterCertificates = createAsyncThunk(
  'cluster/getClusterCertificates',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getClusterCertificates(clusterName, baseURL)
      return { data, status }
    } catch (error) {
      handleError(error, thunkAPI)
    }
  }
)

export const getTopProcess = createAsyncThunk('cluster/getTopProcess', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getTopProcess(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.top) {
      return false;
    }
  }
});

export const getOpenSVCStats = createAsyncThunk('cluster/getOpenSVCStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getOpenSVCStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.opensvcStats) {
      return false;
    }
  }
});

export const getBackups = createAsyncThunk('cluster/getBackups', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getBackups(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.backups.list) {
        return false;
      }
    }
  }
)

export const getBackupStats = createAsyncThunk('cluster/getBackupStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getBackupStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.backups.stats) {
      return false;
    }
  }
})

export const getResticSnapshot = createAsyncThunk('cluster/getResticSnapshot', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getResticSnapshot(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},{
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.restic.snapshots) {
      return false;
    }
  }
})

export const getResticStats = createAsyncThunk('cluster/getResticStats', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getResticStats(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.restic.stats) {
      return false;
    }
  }
})

export const purgeResticSnapshot = createAsyncThunk('cluster/purgeResticSnapshot', async ({ clusterName, snapshotId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.purgeResticSnapshot(clusterName, snapshotId, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const purgeResticByPolicy = createAsyncThunk('cluster/purgeResticByPolicy', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.purgeResticByPolicy(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getResticQueue = createAsyncThunk('cluster/getResticQueue', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getResticQueue(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.isFetching.restic.queue) {
      return false;
    }
  }
})

export const resticQueueCancel = createAsyncThunk('cluster/resticQueueCancel', async ({ clusterName, taskId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resticQueueCancel(clusterName, taskId, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const resticQueueMove = createAsyncThunk('cluster/resticQueueMove', async ({ clusterName, taskId, direction, afterId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resticQueueMove(clusterName, taskId, direction, afterId, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const resticQueuePause = createAsyncThunk('cluster/resticQueuePause', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resticQueuePause(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const resticQueueResume = createAsyncThunk('cluster/resticQueueResume', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resticQueueResume(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getJobs = createAsyncThunk('cluster/getJobs', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getJobs(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getShardSchema = createAsyncThunk('cluster/getShardSchema', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getShardSchema(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getQueryRules = createAsyncThunk('cluster/getQueryRules', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getQueryRules(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const switchOverCluster = createAsyncThunk('cluster/switchOverCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.switchOverCluster(clusterName, baseURL)
    showSuccessBanner('Switchover Successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Switchover Failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const failOverCluster = createAsyncThunk('cluster/failOverCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.failOverCluster(clusterName, baseURL)
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
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.resetFailOverCounter(clusterName, baseURL)
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
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resetSLA(clusterName, baseURL)
    showSuccessBanner('SLA reset!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('SLA reset failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const toggleTraffic = createAsyncThunk('cluster/toggleTraffic', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.toggleTraffic(clusterName, baseURL)
    showSuccessBanner('Traffic toggle done!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Traffic toggle failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const toggleTrafficStaging = createAsyncThunk('cluster/toggleTrafficStaging', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.toggleTrafficStaging(clusterName, baseURL)
    showSuccessBanner('Traffic staging toggle done!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Traffic staging toggle failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const addServer = createAsyncThunk(
  'cluster/addServer',
  async ({ clusterName, host, port, monitorType, tag, dockerRegistry }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.addServer(clusterName, host, port, monitorType, tag, dockerRegistry, baseURL)
      showSuccessBanner('New server added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new server', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const dropServer = createAsyncThunk(
  'cluster/dropServer',
  async ({ clusterName, host, port, type }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropServer(clusterName, host, port, type, baseURL)
      showSuccessBanner('New server dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a new server', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const dropServerByName = createAsyncThunk(
  'cluster/dropServerByName',
  async ({ clusterName, serverName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropServerByName(clusterName, serverName, baseURL)
      showSuccessBanner('New server dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a new server', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const dropApp = createAsyncThunk(
  'cluster/dropApp',
  async ({ clusterName, host, port }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropApp(clusterName, host, port, baseURL)
      showSuccessBanner('New app dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a new app', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const provisionCluster = createAsyncThunk('cluster/provisionCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.provisionCluster(clusterName, baseURL)
    showSuccessBanner('Cluster provision successful', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Cluster provision failed', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const unProvisionCluster = createAsyncThunk('cluster/unProvisionCluster', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.unProvisionCluster(clusterName, baseURL)
    showSuccessBanner('Cluster unprovision successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Cluster unprovision failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setCredentials = createAsyncThunk(
  'cluster/setCredentials',
  async ({ clusterName, credentialType, credential }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setCredentials(clusterName, credentialType, credential, baseURL)
      showSuccessBanner(`Credentials for ${credentialType} set!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Setting credentials for ${credentialType} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const sendCredentials = createAsyncThunk(
  'cluster/sendCredentials',
  async ({ clusterName, username, type }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.sendCredentials(clusterName, username, type, baseURL)
      showSuccessBanner('Credentials sent to email!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Sending credentials email failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const rotateDBCredential = createAsyncThunk('cluster/rotateDBCredential', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rotateDBCredential(clusterName, baseURL)
    showSuccessBanner('Database rotation successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Database rotation failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rollingOptimize = createAsyncThunk('cluster/rollingOptimize', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rollingOptimize(clusterName, baseURL)
    showSuccessBanner('Rolling optimize successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling optimize failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rollingJobsUpgrade = createAsyncThunk('cluster/rollingJobsUpgrade', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rollingJobsUpgrade(clusterName, baseURL)
    showSuccessBanner('Rolling jobs upgrade successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling jobs upgrade failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
}, {
  condition: (_, { getState }) => {
    const { cluster } = getState();
    if (cluster.loadingStates.menuActions) {
      return false;
    }
  }
})


export const rollingRestart = createAsyncThunk('cluster/rollingRestart', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rollingRestart(clusterName, baseURL)
    showSuccessBanner('Rolling restart successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling restart failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rotateCertificates = createAsyncThunk('cluster/rotateCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.rotateCertificates(clusterName, baseURL)
    showSuccessBanner('Rotate certificates successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rotate certificates failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const reloadCertificates = createAsyncThunk('cluster/reloadCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.reloadCertificates(clusterName, baseURL)
    showSuccessBanner('Reload certificates successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Reload certificates failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const cancelRollingRestart = createAsyncThunk(
  'cluster/cancelRollingRestart',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelRollingRestart(clusterName, baseURL)
      showSuccessBanner('Rolling restart cancelled!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Rolling restart cancellation failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const cancelRollingReprov = createAsyncThunk(
  'cluster/cancelRollingReprov',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelRollingReprov(clusterName, baseURL)
      showSuccessBanner('Rolling reprov cancelled!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Rolling reprov cancellation failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMasterSlave = createAsyncThunk(
  'cluster/bootstrapMasterSlave',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMasterSlave(clusterName, baseURL)
      showSuccessBanner('Master slave bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Master slave bootstrap failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMasterSlaveNoGtid = createAsyncThunk(
  'cluster/bootstrapMasterSlaveNoGtid',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMasterSlaveNoGtid(clusterName, baseURL)
      showSuccessBanner('Master slave positional bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Master slave positional bootstrap failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiMaster = createAsyncThunk(
  'cluster/bootstrapMultiMaster',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiMaster(clusterName, baseURL)
      showSuccessBanner('Multi master bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multi master bootstrap failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiMasterRing = createAsyncThunk(
  'cluster/bootstrapMultiMasterRing',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiMasterRing(clusterName, baseURL)
      showSuccessBanner('Multi master ring bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multi master ring bootstrap failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const bootstrapMultiTierSlave = createAsyncThunk(
  'cluster/bootstrapMultiTierSlave',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.bootstrapMultiTierSlave(clusterName, baseURL)
      showSuccessBanner('Multitier slave bootstrap successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Multitier slave bootstrap failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const configReload = createAsyncThunk('cluster/configReload', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.configReload(clusterName, baseURL)
    showSuccessBanner('Config is reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Config reload failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const configDiscoverDB = createAsyncThunk('cluster/configDiscoverDB', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.configDiscoverDB(clusterName, baseURL)
    showSuccessBanner('Databse discover config successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Databse discover config failed!', error.message, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const configDynamic = createAsyncThunk('cluster/configDynamic', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.configDynamic(clusterName, baseURL)
    showSuccessBanner('Databse apply dynamic config successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Databse apply dynamic config failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const checksumAllTables = createAsyncThunk('cluster/checksumAllTables', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.checksumAllTables(clusterName, baseURL)
    showSuccessBanner('Checksum all tables successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Checksum all tables failed!', error.message, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setMaintenanceMode = createAsyncThunk('cluster/setMaintenanceMode',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setMaintenanceMode(clusterName, serverId, baseURL)
      showSuccessBanner('Maintenance mode is set!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Setting Maintenance mode failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const jobsUpgrade = createAsyncThunk('cluster/jobsUpgrade',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.jobsUpgrade(clusterName, serverId, baseURL)
      showSuccessBanner('Jobs upgrade initiated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Jobs upgrade failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const promoteToLeader = createAsyncThunk('cluster/promoteToLeader',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.promoteToLeader(clusterName, serverId, baseURL)
      showSuccessBanner('Promote to leader successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Promote to leader failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setAsUnrated = createAsyncThunk('cluster/setAsUnrated', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.setAsUnrated(clusterName, serverId, baseURL)
    showSuccessBanner('Failover candidate set as unrated!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failover candidate failed to set as unrated', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setAsPreferred = createAsyncThunk(
  'cluster/setAsPreferred',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setAsPreferred(clusterName, serverId, baseURL)
      showSuccessBanner('Failover candidate set as preferred!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Failover candidate failed to set as preferred', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setAsIgnored = createAsyncThunk('cluster/setAsIgnored', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.setAsIgnored(clusterName, serverId, baseURL)
    showSuccessBanner('Failover candidate set as ignored!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failover candidate failed to set as ignored', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const reseedLogicalFromBackup = createAsyncThunk(
  'cluster/reseedLogicalFromBackup',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedLogicalFromBackup(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed logical from backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed logical from backup failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const reseedLogicalFromMaster = createAsyncThunk(
  'cluster/reseedLogicalFromMaster',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedLogicalFromMaster(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed logical from master successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed logical from master failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const reseedPhysicalFromBackup = createAsyncThunk(
  'cluster/reseedPhysicalFromBackup',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedPhysicalFromBackup(clusterName, serverId, baseURL)
      showSuccessBanner('Reseed physical from backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Reseed physical from backup failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const flushLogs = createAsyncThunk('cluster/flushLogs', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.flushLogs(clusterName, serverId, baseURL)
    showSuccessBanner('Logs flush successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Logs flush failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const physicalBackupMaster = createAsyncThunk(
  'cluster/physicalBackupMaster',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.physicalBackupMaster(clusterName, serverId, baseURL)
      showSuccessBanner('Physical master backup successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Physical master backup failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const logicalBackup = createAsyncThunk('cluster/logicalBackup', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.logicalBackup(clusterName, serverId, baseURL)
    showSuccessBanner('Logical backup successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Logical backup failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopDatabase = createAsyncThunk('cluster/stopDatabase', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopDatabase(clusterName, serverId, baseURL)
    showSuccessBanner('Database is stopped!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping database failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const startDatabase = createAsyncThunk('cluster/startDatabase', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startDatabase(clusterName, serverId, baseURL)
    showSuccessBanner('Database has started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error in startDatabase::', error)
    showErrorBanner('Starting database failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const restartDatabase = createAsyncThunk('cluster/restartDatabase', async ({ clusterName, serverId, rid }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.restartDatabase(clusterName, serverId, rid, baseURL)
    showSuccessBanner('Database has restarted!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error in restartDatabase::', error)
    showErrorBanner('Restarting database failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const provisionDatabase = createAsyncThunk(
  'cluster/provisionDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.provisionDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Provision database successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Provision database failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const unprovisionDatabase = createAsyncThunk(
  'cluster/unprovisionDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionDatabase(clusterName, serverId, baseURL)
      showSuccessBanner('Unprovision database successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision database failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const runRemoteJobs = createAsyncThunk('cluster/runRemoteJobs', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.runRemoteJobs(clusterName, serverId, baseURL)
    showSuccessBanner('Remote jobs started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Remote jobs failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const optimizeServer = createAsyncThunk(
  'cluster/optimizeServer',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.optimizeServer(clusterName, serverId, baseURL)
      showSuccessBanner('Database optimize successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Database optimize failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const skipReplicationEvent = createAsyncThunk(
  'cluster/skipReplicationEvent',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.skipReplicationEvent(clusterName, serverId, baseURL)
      showSuccessBanner('Replication event skipped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Skipping Replication event failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const toggleInnodbMonitor = createAsyncThunk(
  'cluster/toggleInnodbMonitor',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleInnodbMonitor(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle Innodb Monitor successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle Innodb Monitor failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const toggleSlowQueryCapture = createAsyncThunk(
  'cluster/toggleSlowQueryCapture',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleSlowQueryCapture(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle Slow Query Capture successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle Slow Query Capture failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const startSlave = createAsyncThunk('cluster/startSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startSlave(clusterName, serverId, baseURL)
    showSuccessBanner('Slave has started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting slave failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopSlave = createAsyncThunk('cluster/stopSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopSlave(clusterName, serverId, baseURL)
    showSuccessBanner('Slave has stopped!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting slave failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const toggleReadOnly = createAsyncThunk(
  'cluster/toggleReadOnly',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleReadOnly(clusterName, serverId, baseURL)
      showSuccessBanner('Toggle readonly successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle readonly failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const killThread = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const killQuery = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const resetMaster = createAsyncThunk('cluster/resetMaster', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resetMaster(clusterName, serverId, baseURL)
    showSuccessBanner('Reset Master successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Reset Master failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const resetSlaveAll = createAsyncThunk('cluster/resetSlaveAll', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.resetSlaveAll(clusterName, serverId, baseURL)
    showSuccessBanner('Reset Slave successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Reset Slave failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const cancelServerJob = createAsyncThunk(
  'cluster/cancelServerJob',
  async ({ clusterName, serverId, taskName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.cancelServerJob(clusterName, serverId, taskName, baseURL)
      showSuccessBanner(`Job ${taskName} cancelled successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Cancellation of job ${taskName} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const provisionProxy = createAsyncThunk('cluster/provisionProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.provisionProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Provision proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Provision proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const unprovisionProxy = createAsyncThunk(
  'cluster/unprovisionProxy',
  async ({ clusterName, proxyId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionProxy(clusterName, proxyId, baseURL)
      showSuccessBanner('Unprovision proxy successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision proxy failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const startProxy = createAsyncThunk('cluster/startProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Starting proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopProxy = createAsyncThunk('cluster/stopProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopProxy(clusterName, proxyId, baseURL)
    showSuccessBanner('Stopping proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stagingProxy = createAsyncThunk('cluster/stagingProxy', async ({ clusterName, proxyId, staging }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stagingProxy(clusterName, proxyId, staging, baseURL)
    showSuccessBanner('Staging proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Staging proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const runSysBench = createAsyncThunk('cluster/runSysBench', async ({ clusterName, thread }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.runSysbench(clusterName, thread, baseURL)
    showSuccessBanner('Sysbench ran successfuly!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Sysbench failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const runRegressionTests = createAsyncThunk(
  'cluster/runRegressionTests',
  async ({ clusterName, testName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.runRegressionTests(clusterName, testName, baseURL)
      showSuccessBanner('Regression test ran successfuly!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Regression test failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const getDatabaseService = createAsyncThunk(
  'cluster/getDatabaseService',
  async ({ clusterName, serviceName, dbId, queryParams = {} }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.getDatabaseService(clusterName, serviceName, dbId, baseURL, queryParams)
      if (status === 200) {
        return { data, status }
      }

      throw new Error(data)
    } catch (error) {
      handleError(error, thunkAPI)
    }
  },
  {
    condition: ({ serviceName }, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.apps) {
        return false;
      }
    }
  });

export const preserveVariable = createAsyncThunk(
  'cluster/preserveVariable',
  async ({ clusterName, dbId, variableName, action }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.preserveVariable(clusterName, dbId, variableName, action, baseURL)
      if (status === 200) {
        showSuccessBanner(`Variable ${variableName} ${action === 'preserve' ? 'preserved' : action === 'accept' ? 'accepted' : 'cleared'} successfully!`, status, thunkAPI)
        return { data, status, dbId }
      }

      throw new Error(data)
    } catch (error) {
      showErrorBanner(`Failed to ${action} variable ${variableName}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setCustomVariableValue = createAsyncThunk(
  'cluster/setCustomVariableValue',
  async ({ clusterName, dbId, variableName, customValue }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.setCustomVariableValue(clusterName, dbId, variableName, customValue, baseURL)
      if (status === 200) {
        showSuccessBanner(`Variable ${variableName} custom value set successfully!`, status, thunkAPI)
        return { data, status, dbId }
      }

      throw new Error(data)
    } catch (error) {
      showErrorBanner(`Failed to set custom value for variable ${variableName}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const updateLongQueryTime = createAsyncThunk(
  'cluster/updateLongQueryTime',
  async ({ clusterName, dbId, time }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateLongQueryTime(clusterName, dbId, time, baseURL)
      showSuccessBanner('Long query time updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Long query time update failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const checksumTable = createAsyncThunk(
  'cluster/checksumTable',
  async ({ clusterName, schema, table }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumTable(clusterName, schema, table, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema} and table ${table}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema} and table ${table}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const checksumSchema = createAsyncThunk(
  'cluster/checksumSchema',
  async ({ clusterName, schema, Schema }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.checksumSchema(clusterName, schema, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const toggleDatabaseActions = createAsyncThunk(
  'cluster/toggleDatabaseActions',
  async ({ clusterName, dbId, serviceName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.toggleDatabaseActions(clusterName, serviceName, dbId, baseURL)
      showSuccessBanner(`Toggle ${serviceName} successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Toggle ${serviceName} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const analyzeAllTables = createAsyncThunk(
  'cluster/analyzeAllTables',
  async ({ clusterName, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeAllTables(clusterName, persistent, baseURL)
      showSuccessBanner(`Checksum done for all schema!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for all schema!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const analyzeSchema = createAsyncThunk(
  'cluster/analyzeSchema',
  async ({ clusterName, schema, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeSchema(clusterName, schema, persistent, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const analyzeTable = createAsyncThunk(
  'cluster/analyzeTable',
  async ({ clusterName, schema, table, persistent }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.analyzeTable(clusterName, schema, table, persistent, baseURL)
      showSuccessBanner(`Checksum done for schema ${schema} and table ${table}!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Checksum failed for schema ${schema} and table ${table}!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const addUser = createAsyncThunk(
  'cluster/addUser',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.addUser(clusterName, username, grants, roles, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const updateGrants = createAsyncThunk(
  'cluster/updateGrants',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.updateGrants(clusterName, username, grants, roles, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const dropUser = createAsyncThunk(
  'cluster/dropUser',
  async ({ clusterName, username, grants, roles }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.dropUser(clusterName, username, baseURL)
      showSuccessBanner(`User is added successful!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Adding user failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const clusterSubscribe = createAsyncThunk('auth/clusterSubscribe', async ({ clusterName, baseURL }, thunkAPI) => {
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
})

export const acceptSubscription = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const rejectSubscription = createAsyncThunk(
  'cluster/rejectSubscription',
  async ({ clusterName, username }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.rejectSubscription(clusterName, username, baseURL)
      showSuccessBanner(`Subscription rejected successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Reject subscription failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const endSubscription = createAsyncThunk(
  'cluster/endSubscription',
  async ({ clusterName, username }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.endSubscription(clusterName, username, baseURL)
      showSuccessBanner(`Subscription ended successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Failed to end subscription!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const refreshStaging = createAsyncThunk(
  'cluster/refreshStaging',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.refreshStaging(clusterName, baseURL)
      showSuccessBanner(`Refresh staging initiated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Failed to initiate refresh staging!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const reseedStagingFromParent = createAsyncThunk(
  'cluster/reseedStagingFromParent',
  async ({ clusterName }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.reseedStagingFromParent(clusterName, baseURL)
      showSuccessBanner(`Staging script reloaded successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Failed to reload staging script!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const subscribeExternalRole = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const quoteExternalRole = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const acceptExternalRole = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const refuseExternalRole = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)


export const endExternalRole = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const addClusterShard = createAsyncThunk('cluster/addClusterShard', async ({ clusterName, clusterShard, formdata }, thunkAPI) => {
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
    handleError(error, thunkAPI)
  }
})

export const getClusterApps = createAsyncThunk('cluster/getClusterApps', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.getClusterApps(clusterName, baseURL)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
},
  // Add a condition to prevent the action from being dispatched if the user is already fetching the info
  {
    condition: (_, { getState }) => {
      const { cluster } = getState();
      if (cluster.isFetching.apps) {
        return false;
      }
    }
  });

export const provisionApp = createAsyncThunk('cluster/provisionApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.provisionApp(clusterName, appId, baseURL)
    showSuccessBanner('Provision app successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Provision app failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const unprovisionApp = createAsyncThunk(
  'cluster/unprovisionApp',
  async ({ clusterName, appId }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.unprovisionApp(clusterName, appId, baseURL)
      showSuccessBanner('Unprovision app successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Unprovision app failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const startApp = createAsyncThunk('cluster/startApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.startApp(clusterName, appId, baseURL)
    showSuccessBanner('Starting app successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting app failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopApp = createAsyncThunk('cluster/stopApp', async ({ clusterName, appId }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await clusterService.stopApp(clusterName, appId, baseURL)
    showSuccessBanner('Stopping app successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping app failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const getAppService = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const resolveTemplateVariables = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const deploymentFieldChange = createAsyncThunk(
  'cluster/deploymentFieldChange',
  async ({ clusterName, appId, field, index, key, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.deploymentFieldChange(clusterName, appId, field, index, key, value, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('Deployment field updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while updating deployment field', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const deploymentFieldIndexAdd = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)
export const deploymentFieldIndexDrop = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const storageFieldChange = createAsyncThunk(
  'cluster/storageFieldChange',
  async ({ clusterName, appId, field, index, key, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldChange(clusterName, appId, field, index, key, value, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('storage field updated!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while updating storage field', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const storageFieldIndexAdd = createAsyncThunk(
  'cluster/storageFieldIndexAdd',
  async ({ clusterName, appId, field, value }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldIndexAdd(clusterName, appId, field, value, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('New storage field row added!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while adding a new storage field row', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)
export const storageFieldIndexDrop = createAsyncThunk(
  'cluster/storageFieldIndexDrop',
  async ({ clusterName, appId, field, index }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await clusterService.storageFieldIndexDrop(clusterName, appId, field, index, baseURL)
      if (status !== 200) {
        throw new Error(data)
      }
      showSuccessBanner('storage field row dropped!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Error while dropping a storage field row', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)


export const connectDockerRegistry = createAsyncThunk(
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
      handleError(error, thunkAPI)
    }
  }
)

export const monitorAllSchemas = createAsyncThunk('cluster/monitorAllSchemas', async ({ clusterName }, thunkAPI) => {
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
    handleError(error, thunkAPI)
  }
})

const initialState = {
  loading: false,
  isFetching: {
    apps: false,
    cluster: false,
    alerts: false,
    master: false,
    servers: false,
    proxies: false,
    certificates: false,
    top: false,
    opensvcStats: false,
    logs: false,
    database: {
      processList: false,
      slowQueries: false,
      errors: false,
      sqlerrors: false,
      auditlogs: false,
      digestQueries: false,
      tables: false,
      statusDelta: false,
      statusInnoDB: false,
      variables: false,
      serviceOpensvc: false,
      metadataLocks: false,
      responsetime: false,
    },
    backups: {
      list: false,
      stats: false
    },
    restic: {
      snapshots: false,
      stats: false,
      queue: false
    },
  },
  error: null,
  clusterApps: null,
  clusterAppStates: null,
  clusterData: null,
  clusterAlerts: null,
  clusterLogs: {
    general: null,
    task: null,
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
    queue: null
  },
  topProcess: null,
  opensvcStats: null,
  jobs: null,
  shardSchema: null,
  queryRules: null,
  refreshInterval: 0,
  loadingStates: {
    switchOver: false,
    failOver: false,
    menuActions: false
  },
  app: {
    substitution: null,
    deployment: null,
    serviceOpensvc: null,
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
        getShardSchema.fulfilled,
        getQueryRules.fulfilled,
        getBackups.fulfilled,
        getBackupStats.fulfilled,
        getResticSnapshot.fulfilled,
        getResticStats.fulfilled,
        getResticQueue.fulfilled,
        getJobs.fulfilled
      ),
      (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.clusterData = action.payload.data
          state.isFetching.cluster = false
        } else if (action.type.includes('getClusterAlerts')) {
          state.clusterAlerts = action.payload.data
          state.isFetching.alerts = false
        } else if (action.type.includes('getClusterLogs')) {
          state.clusterLogs = action.payload.data || {}
          state.isFetching.logs = false
        } else if (action.type.includes('getClusterMaster')) {
          state.clusterMaster = action.payload.data
          state.isFetching.master = false
        } else if (action.type.includes('getClusterServers')) {
          if (action.payload?.data && action.meta.arg?.clusterName == state.clusterData?.name) {
            state.clusterServers = action.payload.data
            state.clusterStates = action.payload?.data?.map((server) => `${server.state}-${server.isVirtualMaster}`).join(',') || ''
          }
          state.isFetching.servers = false
        } else if (action.type.includes('getClusterApps')) {
          state.clusterApps = action.payload?.data
          state.clusterAppStates = action.payload?.data?.map((server) => `${server.state}-${server.isVirtualMaster}`).join(',') || ''
          state.isFetching.apps = false
        } else if (action.type.includes('getClusterProxies')) {
          state.clusterProxies = action.payload?.data
          state.clusterProxiesStaging = action.payload?.data?.filter((proxy) => proxy.isStaging).map((proxy) => proxy.name).join(',') || ''
          state.isFetching.proxies = false
        } else if (action.type.includes('getClusterCertificates')) {
          state.clusterCertificates = action.payload.data
        } else if (action.type.includes('getTopProcess')) {
          state.topProcess = action.payload.data
          state.isFetching.top = false
        } else if (action.type.includes('getOpenSVCStats')) {
          state.opensvcStats = action.payload.data
          state.isFetching.opensvcStats = false
        } else if (action.type.includes('getBackups')) {
          state.backups.list = action.payload.data
          state.isFetching.backups.list = false
        } else if (action.type.includes('getBackupStats')) {
          state.backups.stats = action.payload.data
          state.isFetching.backups.stats = false
        } else if (action.type.includes('getResticSnapshot')) {
          state.restic.snapshots = action.payload.data
          state.isFetching.restic.snapshots = false
        } else if (action.type.includes('getResticStats')) {
          state.restic.stats = action.payload.data
          state.isFetching.restic.stats = false
        } else if (action.type.includes('getResticQueue')) {
          state.restic.queue = action.payload.data
          state.isFetching.restic.queue = false
        } else if (action.type.includes('getShardSchema')) {
          state.shardSchema = action.payload.data
        } else if (action.type.includes('getQueryRules')) {
          state.queryRules = action.payload.data
        } else if (action.type.includes('getJobs')) {
          state.jobs = action.payload.data
        } else if (action.type.includes('getDatabaseService')) {
          const { serviceName } = action.meta.arg
          if (serviceName === 'processlist') {
            state.database.processList = action.payload.data
            state.isFetching.database.processList = false
          } else if (serviceName === 'slow-queries') {
            state.database.slowQueries = action.payload.data
            state.isFetching.database.slowQueries = false
          } else if (serviceName === 'errorlog') {
            state.database.errors = action.payload.data
            state.isFetching.database.errors = false
          } else if (serviceName === 'sqlerrorlog') {
            state.database.sqlerrors = action.payload.data
            state.isFetching.database.sqlerrors = false
          } else if (serviceName === 'auditlog') {
            state.database.auditlogs = action.payload.data
            state.isFetching.database.auditlogs = false
          } else if (serviceName === 'digest-statements-pfs') {
            state.database.digestQueries = action.payload.data
            state.isFetching.database.digestQueries = false
          } else if (serviceName === 'tables') {
            if (!isEqual(state.database.tables, action.payload?.data)) {
              state.database.tables = action.payload?.data
            }
            state.isFetching.database.tables = false
          } else if (serviceName === 'status-delta') {
            state.database.status.statusDelta = action.payload.data
            state.isFetching.database.statusDelta = false
          } else if (serviceName === 'status-innodb') {
            state.database.status.statusInnoDB = action.payload.data
            state.isFetching.database.statusInnoDB = false
          } else if (serviceName === 'variables') {
            state.database.variables = (action.payload.status == 200) ? action.payload.data : []
            state.isFetching.database.variables = false
          } else if (serviceName === 'service-opensvc') {
            state.database.serviceOpensvc = action.payload.data
            state.isFetching.database.serviceOpensvc = false
          } else if (serviceName === 'meta-data-locks') {
            state.database.metadataLocks = action.payload.data
            state.isFetching.database.metadataLocks = false
          } else if (serviceName === 'query-response-time') {
            state.database.responsetime = action.payload.data
            state.isFetching.database.responsetime = false
          }
        }
      }
    )

    builder.addMatcher(
      isAnyOf(
        getClusterData.pending,
        getClusterLogs.pending,
        getClusterApps.pending,
        getClusterAlerts.pending,
        getClusterMaster.pending,
        getClusterServers.pending,
        getClusterProxies.pending,
        getClusterCertificates.pending,
        getTopProcess.pending,
        getOpenSVCStats.pending,
        getDatabaseService.pending,
        getResticStats.pending,
        getResticSnapshot.pending,
        getResticQueue.pending,
        getBackups.pending,
        getBackupStats.pending,
      ),
      (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.isFetching.cluster = true
        } else if (action.type.includes('getClusterLogs')) {
          state.isFetching.logs = true
        } else if (action.type.includes('getClusterAlerts')) {
          state.isFetching.alerts = true
        } else if (action.type.includes('getClusterMaster')) {
          state.isFetching.master = true
        } else if (action.type.includes('getClusterServers')) {
          state.isFetching.servers = true
        } else if (action.type.includes('getClusterProxies')) {
          state.isFetching.proxies = true
        } else if (action.type.includes('getClusterApps')) {
          state.isFetching.apps = true
        } else if (action.type.includes('getTopProcess')) {
          state.isFetching.top = true
        } else if (action.type.includes('getOpenSVCStats')) {
          state.isFetching.opensvcStats = true
        } else if (action.type.includes('getBackups')) {
          state.isFetching.backups.list = true
        } else if (action.type.includes('getBackupStats')) {
          state.isFetching.backups.stats = true
        } else if (action.type.includes('getResticSnapshot')) {
          state.isFetching.restic.snapshots = true
        } else if (action.type.includes('getResticStats')) {
          state.isFetching.restic.stats = true
        } else if (action.type.includes('getResticQueue')) {
          state.isFetching.restic.queue = true
        } else if (action.type.includes('getDatabaseService')) {
          const { serviceName } = action.meta.arg
          if (serviceName === 'processlist') {
            state.isFetching.database.processList = true
          } else if (serviceName === 'slow-queries') {
            state.isFetching.database.slowQueries = true
          } else if (serviceName === 'errorlog') {
            state.isFetching.database.errors = true
          } else if (serviceName === 'sqlerrorlog') {
            state.isFetching.database.sqlerrors = true
          } else if (serviceName === 'auditlog') {
            state.isFetching.database.auditlogs = true
          } else if (serviceName === 'digest-statements-pfs') {
            state.isFetching.database.digestQueries = true
          } else if (serviceName === 'tables') {
            state.isFetching.database.tables = true
          } else if (serviceName === 'status-delta') {
            state.isFetching.database.statusDelta = true
          } else if (serviceName === 'status-innodb') {
            state.isFetching.database.statusInnoDB = true
          } else if (serviceName === 'variables') {
            state.isFetching.database.variables = true
          } else if (serviceName === 'service-opensvc') {
            state.isFetching.database.serviceOpensvc = true
          } else if (serviceName === 'meta-data-locks') {
            state.isFetching.database.metadataLocks = true
          } else if (serviceName === 'query-response-time') {
            state.isFetching.database.responsetime = true
          }
        }
      }
    )

    builder.addMatcher(
      isAnyOf(
        getClusterData.rejected,
        getClusterLogs.rejected,
        getClusterApps.rejected,
        getClusterAlerts.rejected,
        getClusterMaster.rejected,
        getClusterServers.rejected,
        getClusterProxies.rejected,
        getClusterCertificates.rejected,
        getDatabaseService.rejected,
        getBackups.rejected,
        getBackupStats.rejected,
        getResticSnapshot.rejected,
        getResticStats.rejected,
        getResticQueue.rejected,
        getTopProcess.rejected,
        getOpenSVCStats.rejected
      ), (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.isFetching.cluster = false
        } else if (action.type.includes('getClusterLogs')) {
          state.isFetching.logs = false
        } else if (action.type.includes('getClusterAlerts')) {
          state.isFetching.alerts = false
        } else if (action.type.includes('getClusterMaster')) {
          state.isFetching.master = false
        } else if (action.type.includes('getClusterServers')) {
          state.isFetching.servers = false
        } else if (action.type.includes('getClusterProxies')) {
          state.isFetching.proxies = false
        } else if (action.type.includes('getClusterApps')) {
          state.isFetching.apps = false
        } else if (action.type.includes('getBackups')) {
          state.isFetching.backups.list = false
        } else if (action.type.includes('getBackupStats')) {
          state.isFetching.backups.stats = false
        } else if (action.type.includes('getResticSnapshot')) {
          state.isFetching.restic.snapshots = false
        } else if (action.type.includes('getResticStats')) {
          state.isFetching.restic.stats = false
        } else if (action.type.includes('getResticQueue')) {
          state.isFetching.restic.tasks = false
        } else if (action.type.includes('getTopProcess')) {
          state.isFetching.top = false
        } else if (action.type.includes('getOpenSVCStats')) {
          state.isFetching.opensvcStats = false
        } else if (action.type.includes('getDatabaseService')) {
          const { serviceName } = action.meta.arg
          if (serviceName === 'processlist') {
            state.isFetching.database.processList = false
          } else if (serviceName === 'slow-queries') {
            state.isFetching.database.slowQueries = false
          } else if (serviceName === 'errorlog') {
            state.isFetching.database.errors = false
          } else if (serviceName === 'sqlerrorlog') {
            state.isFetching.database.sqlerrors = false
          } else if (serviceName === 'auditlog') {
            state.isFetching.database.auditlogs = false
          } else if (serviceName === 'digest-statements-pfs') {
            state.isFetching.database.digestQueries = false
          } else if (serviceName === 'tables') {
            state.isFetching.database.tables = false
          } else if (serviceName === 'status-delta') {
            state.isFetching.database.statusDelta = false
          } else if (serviceName === 'status-innodb') {
            state.isFetching.database.statusInnoDB = false
          } else if (serviceName === 'variables') {
            state.isFetching.database.variables = false
          } else if (serviceName === 'service-opensvc') {
            state.isFetching.database.serviceOpensvc = false
          } else if (serviceName === 'meta-data-locks') {
            state.isFetching.database.metadataLocks = false
          } else if (serviceName === 'query-response-time') {
            state.isFetching.database.responsetime = false
          }
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
        rollingRestart.pending,
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
        flushLogs.pending,
        physicalBackupMaster.pending,
        logicalBackup.pending,
        stopDatabase.pending,
        startDatabase.pending,
        restartDatabase.pending,
        provisionDatabase.pending,
        unprovisionDatabase.pending,
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
        refreshStaging.pending,
        killThread.pending,
        killQuery.pending,
        rollingJobsUpgrade.pending,
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
        rollingRestart.fulfilled,
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
        flushLogs.fulfilled,
        physicalBackupMaster.fulfilled,
        logicalBackup.fulfilled,
        stopDatabase.fulfilled,
        startDatabase.fulfilled,
        restartDatabase.fulfilled,
        provisionDatabase.fulfilled,
        unprovisionDatabase.fulfilled,
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
        refreshStaging.fulfilled,
        killThread.fulfilled,
        killQuery.fulfilled,
        rollingJobsUpgrade.fulfilled,
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
        rollingRestart.rejected,
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
        flushLogs.rejected,
        physicalBackupMaster.rejected,
        logicalBackup.rejected,
        stopDatabase.rejected,
        startDatabase.rejected,
        restartDatabase.rejected,
        provisionDatabase.rejected,
        unprovisionDatabase.rejected,
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
        refreshStaging.rejected,
        killThread.rejected,
        killQuery.rejected,
        rollingJobsUpgrade.rejected,
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
    builder.addMatcher(
      isAnyOf(
        getAppService.fulfilled,
      ),
      (state, action) => {
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
      }
    )
    builder.addCase(preserveVariable.fulfilled, (state, action) => {
      // Refresh the variables after preserve/accept/clear action
      // This will be handled by the component dispatching getDatabaseService again
    })
  }
})

export const { setRefreshInterval, setCluster, clearCluster, pauseAutoReload } = clusterSlice.actions

// this is for configureStore
export default clusterSlice.reducer
