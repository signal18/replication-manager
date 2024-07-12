import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { clusterService } from '../services/clusterService'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'

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

export const getClusterServers = createAsyncThunk('cluster/getClusterServers', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusterServers(clusterName)
    return { data, status }
  } catch (error) {
    handleError(error, thunkAPI)
  }
})

export const getClusterProxies = createAsyncThunk('cluster/getClusterProxies', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.getClusterProxies(clusterName)
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
    showSuccessBanner('Rolling optimize successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling optimize failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rollingRestart = createAsyncThunk('cluster/rollingRestart', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rollingRestart(clusterName)
    showSuccessBanner('Rolling restart successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rolling restart failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const rotateCertificates = createAsyncThunk('cluster/rotateCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.rotateCertificates(clusterName)
    showSuccessBanner('Rotate certificates successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Rotate certificates failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const reloadCertificates = createAsyncThunk('cluster/reloadCertificates', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.reloadCertificates(clusterName)
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
      const { data, status } = await clusterService.cancelRollingRestart(clusterName)
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
      const { data, status } = await clusterService.cancelRollingReprov(clusterName)
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
      const { data, status } = await clusterService.bootstrapMasterSlave(clusterName)
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
      const { data, status } = await clusterService.bootstrapMasterSlaveNoGtid(clusterName)
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
      const { data, status } = await clusterService.bootstrapMultiMaster(clusterName)
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
      const { data, status } = await clusterService.bootstrapMultiMasterRing(clusterName)
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
      const { data, status } = await clusterService.bootstrapMultiTierSlave(clusterName)
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
    const { data, status } = await clusterService.configReload(clusterName)
    showSuccessBanner('Config is reloaded!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Config reload failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const configDiscoverDB = createAsyncThunk('cluster/configDiscoverDB', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.configDiscoverDB(clusterName)
    showSuccessBanner('Databse discover config successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Databse discover config failed!', error.message, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const configDynamic = createAsyncThunk('cluster/configDynamic', async ({ clusterName }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.configDynamic(clusterName)
    showSuccessBanner('Databse apply dynamic config successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Databse apply dynamic config failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const setMaintenanceMode = createAsyncThunk(
  'cluster/setMaintenanceMode',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.setMaintenanceMode(clusterName, serverId)
      showSuccessBanner('Maintenance mode is set!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Setting Maintenance mode failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)
export const promoteToLeader = createAsyncThunk(
  'cluster/promoteToLeader',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.promoteToLeader(clusterName, serverId)
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
    const { data, status } = await clusterService.setAsUnrated(clusterName, serverId)
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
      const { data, status } = await clusterService.setAsPreferred(clusterName, serverId)
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
    const { data, status } = await clusterService.setAsIgnored(clusterName, serverId)
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
      const { data, status } = await clusterService.reseedLogicalFromBackup(clusterName, serverId)
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
      const { data, status } = await clusterService.reseedLogicalFromMaster(clusterName, serverId)
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
      const { data, status } = await clusterService.reseedPhysicalFromBackup(clusterName, serverId)
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
    const { data, status } = await clusterService.flushLogs(clusterName, serverId)
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
      const { data, status } = await clusterService.physicalBackupMaster(clusterName, serverId)
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
    const { data, status } = await clusterService.logicalBackup(clusterName, serverId)
    showSuccessBanner('Logical backup successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Logical backup failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopDatabase = createAsyncThunk('cluster/stopDatabase', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.stopDatabase(clusterName, serverId)
    showSuccessBanner('Database is stopped!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping database failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const startDatabase = createAsyncThunk('cluster/startDatabase', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.startDatabase(clusterName, serverId)
    showSuccessBanner('Database has started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    console.log('error in startDatabase::', error)
    showErrorBanner('Starting database failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const provisionDatabase = createAsyncThunk(
  'cluster/provisionDatabase',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.provisionDatabase(clusterName, serverId)
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
      const { data, status } = await clusterService.unprovisionDatabase(clusterName, serverId)
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
    const { data, status } = await clusterService.runRemoteJobs(clusterName, serverId)
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
      const { data, status } = await clusterService.optimizeServer(clusterName, serverId)
      showSuccessBanner('Database optimize successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Database optimize failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const skip1ReplicationEvent = createAsyncThunk(
  'cluster/skip1ReplicationEvent',
  async ({ clusterName, serverId }, thunkAPI) => {
    try {
      const { data, status } = await clusterService.skip1ReplicationEvent(clusterName, serverId)
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
      const { data, status } = await clusterService.toggleInnodbMonitor(clusterName, serverId)
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
      const { data, status } = await clusterService.toggleSlowQueryCapture(clusterName, serverId)
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
    const { data, status } = await clusterService.startSlave(clusterName, serverId)
    showSuccessBanner('Slave has started!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting slave failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopSlave = createAsyncThunk('cluster/stopSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.stopSlave(clusterName, serverId)
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
      const { data, status } = await clusterService.toggleReadOnly(clusterName, serverId)
      showSuccessBanner('Toggle readonly successful!', status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner('Toggle readonly failed!', error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const resetMaster = createAsyncThunk('cluster/resetMaster', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.resetMaster(clusterName, serverId)
    showSuccessBanner('Reset Master successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Reset Master failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const resetSlave = createAsyncThunk('cluster/resetSlave', async ({ clusterName, serverId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.resetSlave(clusterName, serverId)
    showSuccessBanner('Reset Slave successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Reset Slave failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const provisionProxy = createAsyncThunk('cluster/provisionProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.provisionProxy(clusterName, proxyId)
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
      const { data, status } = await clusterService.unprovisionProxy(clusterName, proxyId)
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
    const { data, status } = await clusterService.startProxy(clusterName, proxyId)
    showSuccessBanner('Starting proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Starting proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const stopProxy = createAsyncThunk('cluster/stopProxy', async ({ clusterName, proxyId }, thunkAPI) => {
  try {
    const { data, status } = await clusterService.stopProxy(clusterName, proxyId)
    showSuccessBanner('Stopping proxy successful!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Stopping proxy failed!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

const initialState = {
  loading: false,
  error: null,
  clusters: null,
  monitor: null,
  clusterData: null,
  clusterAlerts: null,
  clusterMaster: null,
  clusterServers: null,
  clusterProxies: null,
  refreshInterval: 0,
  loadingStates: {
    switchOver: false,
    failOver: false,
    menuActions: false
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
    setCluster: (state, action) => {
      state.clusterData = action.payload.data
    },
    clearCluster: (state, action) => {
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

    builder.addMatcher(
      isAnyOf(
        getClusterData.fulfilled,
        getClusterAlerts.fulfilled,
        getClusterMaster.fulfilled,
        getClusterServers.fulfilled,
        getClusterProxies.fulfilled
      ),
      (state, action) => {
        if (action.type.includes('getClusterData')) {
          state.clusterData = action.payload.data
        } else if (action.type.includes('getClusterAlerts')) {
          state.clusterAlerts = action.payload.data
        } else if (action.type.includes('getClusterMaster')) {
          state.clusterMaster = action.payload.data
        } else if (action.type.includes('getClusterServers')) {
          state.clusterServers = action.payload.data
        } else if (action.type.includes('getClusterProxies')) {
          state.clusterProxies = action.payload.data
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
        provisionDatabase.pending,
        unprovisionDatabase.pending,
        runRemoteJobs.pending,
        optimizeServer.pending,
        skip1ReplicationEvent.pending,
        toggleInnodbMonitor.pending,
        toggleSlowQueryCapture.pending,
        startSlave.pending,
        stopSlave.pending,
        toggleReadOnly.pending,
        resetMaster.pending,
        resetSlave.pending,
        provisionProxy.pending,
        unprovisionProxy.pending,
        startProxy.pending,
        stopProxy.pending
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
        provisionDatabase.fulfilled,
        unprovisionDatabase.fulfilled,
        runRemoteJobs.fulfilled,
        optimizeServer.fulfilled,
        skip1ReplicationEvent.fulfilled,
        toggleInnodbMonitor.fulfilled,
        toggleSlowQueryCapture.fulfilled,
        startSlave.fulfilled,
        stopSlave.fulfilled,
        toggleReadOnly.fulfilled,
        resetMaster.fulfilled,
        resetSlave.fulfilled,
        provisionProxy.fulfilled,
        unprovisionProxy.fulfilled,
        startProxy.fulfilled,
        stopProxy.fulfilled
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
        provisionDatabase.rejected,
        unprovisionDatabase.rejected,
        runRemoteJobs.rejected,
        optimizeServer.rejected,
        skip1ReplicationEvent.rejected,
        toggleInnodbMonitor.rejected,
        toggleSlowQueryCapture.rejected,
        startSlave.rejected,
        stopSlave.rejected,
        toggleReadOnly.rejected,
        resetMaster.rejected,
        resetSlave.rejected,
        provisionProxy.rejected,
        unprovisionProxy.rejected,
        startProxy.rejected,
        stopProxy.rejected
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

export const { setRefreshInterval, setCluster, clearCluster } = clusterSlice.actions

// this is for configureStore
export default clusterSlice.reducer
