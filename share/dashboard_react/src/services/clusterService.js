import { getApi } from './apiHelper'

export const clusterService = {
  // Cluster data APIs
  getClusterData,
  getClusterAlerts,
  getClusterLogs,
  getClusterMaster,
  getClusterServers,
  getClusterProxies,
  getClusterCertificates,
  getTopProcess,
  getOpenSVCStats,
  getBackups,
  getBackupStats,
  getJobs,
  getShardSchema,
  getQueryRules,
  
  // Restic management APIs
  getResticSnapshot,
  getResticStats,
  purgeResticSnapshot,
  purgeResticByPolicy,
  getResticQueue,
  resticQueueResume,
  resticQueuePause,
  resticQueueMove,
  resticQueueCancel,
  resticQueueReset,

  // Cluster management APIs
  checksumAllTables,
  analyzeAllTables,
  switchOverCluster,
  failOverCluster,
  resetFailOverCounter,
  resetSLA,
  toggleTraffic,
  toggleTrafficStaging,
  addServer,
  dropServer,
  dropServerByName,
  provisionCluster,
  unProvisionCluster,
  setCredentials,
  rotateDBCredential,
  rollingOptimize,
  rollingJobsUpgrade,
  rollingRestart,
  rotateCertificates,
  reloadCertificates,
  cancelRollingRestart,
  cancelRollingReprov,
  bootstrapMasterSlave,
  bootstrapMasterSlaveNoGtid,
  bootstrapMultiMaster,
  bootstrapMultiMasterRing,
  bootstrapMultiTierSlave,
  configReload,
  configDiscoverDB,
  configDynamic,
  refreshStaging,
  reseedStagingFromParent,

  // Server management APIs
  setMaintenanceMode,
  jobsUpgrade,
  promoteToLeader,
  setAsUnrated,
  setAsPreferred,
  setAsIgnored,
  reseedLogicalFromBackup,
  reseedLogicalFromMaster,
  reseedPhysicalFromBackup,
  flushLogs,
  physicalBackupMaster,
  logicalBackup,
  stopDatabase,
  startDatabase,
  restartDatabase,
  provisionDatabase,
  unprovisionDatabase,
  runRemoteJobs,
  optimizeServer,
  skipReplicationEvent,
  toggleInnodbMonitor,
  toggleSlowQueryCapture,
  startSlave,
  stopSlave,
  toggleReadOnly,
  resetMaster,
  resetSlaveAll,
  cancelServerJob,

  // Proxy management APIs
  provisionProxy,
  unprovisionProxy,
  startProxy,
  stopProxy,
  stagingProxy,

  // Database service APIs
  getDatabaseService,
  preserveVariable,
  setCustomVariableValue,
  updateLongQueryTime,
  toggleDatabaseActions,
  checksumTable,
  checksumSchema,
  analyzeTable,
  analyzeSchema,
  killThread,
  killQuery,
  monitorAllSchemas,

  // Test run APIs
  runSysbench,
  runRegressionTests,

  // User management APIs
  addUser,
  updateGrants,
  dropUser,
  clusterSubscribe,
  clusterUnsubscribe,
  acceptSubscription,
  rejectSubscription,
  sendCredentials,
  endSubscription,

  subscribeExternalRole,
  quoteExternalRole,
  acceptExternalRole,
  refuseExternalRole,
  endExternalRole,

  addClusterShard,

  // App management APIs
  dropApp,
  getClusterApps,
  provisionApp,
  unprovisionApp,
  startApp,
  stopApp,
  getAppService,
  resolveTemplateVariables,
  addDeployment,
  dropDeployment,
  deploymentFieldChange,
  deploymentFieldIndexAdd,
  deploymentFieldIndexDrop,
  storageFieldChange,
  storageFieldIndexAdd,
  storageFieldIndexDrop,

  connectDockerRegistry
}

//#region Cluster data APIs
function getClusterData(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}`)
}

function getClusterAlerts(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/alerts`)
}

function getClusterMaster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/master`)
}

function getClusterServers(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/servers`)
}

function getClusterProxies(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/proxies`)
}

function getClusterLogs(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/http-logs`)
}

function getClusterCertificates(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/certificates`)
}

function getTopProcess(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/top`)
}

function getOpenSVCStats(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/opensvc-stats`)
}

function getBackups(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/backups`)
}

function getBackupStats(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/backups/stats`)
}


function getResticSnapshot(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/snapshots`)
}

function getResticStats(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/stats`)
}

function getJobs(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/jobs`)
}

function getShardSchema(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema`)
}

function getQueryRules(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/queryrules`)
}
//#endregion Cluster data APIs

//#region Cluster management APIs
function checksumAllTables(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/checksum-all-tables`)
}

function analyzeAllTables(clusterName, persistent, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/analyze-all-tables/${persistent}`)
}

function switchOverCluster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/switchover`)
}

function failOverCluster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/failover`)
}

function resetFailOverCounter(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/reset-failover-control`)
}

function resetSLA(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/reset-sla`)
}

function toggleTraffic(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/switch/database-heartbeat`)
}

function toggleTrafficStaging(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/switch/database-heartbeat-staging`)
}

function addServer(clusterName, host, port, monitorType, tag, dockerRegistry = {}, baseURL) {
  if (monitorType === 'app') {
    return getApi(baseURL).post(`clusters/${clusterName}/actions/addserver/${host}/${port}/${monitorType}/${tag}`, { ...dockerRegistry })
  } else if (!monitorType) {
    return getApi(baseURL).get(`clusters/${clusterName}/actions/addserver/${host}/${port}`)
  } else if (!tag) {
    return getApi(baseURL).get(`clusters/${clusterName}/actions/addserver/${host}/${port}/${monitorType}`)
  } else {
    return getApi(baseURL).get(`clusters/${clusterName}/actions/addserver/${host}/${port}/${monitorType}/${tag}`)
  }
}

function dropServer(clusterName, host, port, type, baseURL) {
  if (type) {
    return getApi(baseURL).get(`clusters/${clusterName}/actions/dropserver/${host}/${port}/${type}`)
  }
  return getApi(baseURL).get(`clusters/${clusterName}/actions/dropserver/${host}/${port}`)
}

function dropServerByName(clusterName, serverName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/dropserver/${serverName}`)
}

function dropApp(clusterName, host, port, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${host}/${port}/actions/drop`)
}

function provisionCluster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/services/actions/provision`)
}

function unProvisionCluster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/services/actions/unprovision`)
}

function setCredentials(clusterName, credentialType, credential, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set/${credentialType}/${credential}`)
}

function rotateDBCredential(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/rotate-passwords`)
}

function rollingOptimize(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/optimize`)
}

function rollingJobsUpgrade(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/jobs-upgrade`)
}

function rollingRestart(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/rolling`)
}

function rotateCertificates(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/certificates-rotate`)
}

function reloadCertificates(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/certificates-reload`)
}

function cancelRollingRestart(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/cancel-rolling-restart`)
}

function cancelRollingReprov(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/cancel-rolling-reprov`)
}

function bootstrapMasterSlave(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/replication/bootstrap/master-slave`)
}

function bootstrapMasterSlaveNoGtid(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/replication/bootstrap/master-slave-no-gtid`)
}

function bootstrapMultiMaster(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/replication/bootstrap/multi-master`)
}

function bootstrapMultiMasterRing(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/replication/bootstrap/multi-master-ring`)
}

function bootstrapMultiTierSlave(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/replication/bootstrap/multi-tier-slave`)
}

function configReload(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/reload`)
}

function configDiscoverDB(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/discover`)
}

function configDynamic(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/apply-dynamic-config`)
}

function refreshStaging(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/staging-refresh`)
}

function reseedStagingFromParent(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/staging-reseed-from-parent`)
}
//#endregion Cluster management APIs

//#region Server management APIs
function setMaintenanceMode(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/maintenance`)
}

function jobsUpgrade(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/jobs-upgrade`)
}

function promoteToLeader(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/switchover`)
}

function setAsUnrated(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/set-unrated`)
}

function setAsPreferred(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/set-prefered`)
}

function setAsIgnored(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/set-ignored`)
}

function reseedLogicalFromBackup(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reseed/logicalbackup`)
}

function reseedLogicalFromMaster(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reseed/logicalmaster`)
}

function reseedPhysicalFromBackup(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reseed/physicalbackup`)
}

function flushLogs(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/flush-logs`)
}

function physicalBackupMaster(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/backup-physical`)
}

function logicalBackup(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/backup-logical`)
}

function stopDatabase(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/stop`)
}

function startDatabase(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/start`)
}

function restartDatabase(clusterName, serverId, rid, baseURL) {
  const params = rid ? `?rid=${encodeURIComponent(rid)}` : ''
  return getApi(baseURL).post(`clusters/${clusterName}/servers/${serverId}/actions/restart${params}`)
}

function provisionDatabase(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/provision`)
}

function unprovisionDatabase(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/unprovision`)
}

function runRemoteJobs(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/run-jobs`)
}

function optimizeServer(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/optimize`)
}

function skipReplicationEvent(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/skip-replication-event`)
}

function toggleInnodbMonitor(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/toggle-innodb-monitor`)
}

function toggleSlowQueryCapture(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/toggle-slow-query-capture`)
}

function startSlave(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/start-slave`)
}

function stopSlave(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/stop-slave`)
}

function toggleReadOnly(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/toggle-read-only`)
}

function resetMaster(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reset-master`)
}

function resetSlaveAll(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reset-slave-all`)
}

function cancelServerJob(clusterName, serverId, taskName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/job-cancel/${taskName}`)
}
//#endregion Server management APIs

//#region Proxy management APIs
function provisionProxy(clusterName, proxyId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/${proxyId}/actions/provision`)
}

function unprovisionProxy(clusterName, proxyId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/${proxyId}/actions/unprovision`)
}

function startProxy(clusterName, proxyId, cfgAction, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/${proxyId}/actions/start/${cfgAction}`)
}

function stopProxy(clusterName, proxyId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/${proxyId}/actions/stop`)
}

function stagingProxy(clusterName, proxyId, isStaging, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/${proxyId}/actions/staging/${isStaging}`)
}
//#endregion Proxy management APIs

//#region Database service APIs
function getDatabaseService(clusterName, serviceName, dbId, baseURL, queryParams = {}) {
  let path = `clusters/${clusterName}/servers/${dbId}/${serviceName}`
  
  // Build query string from queryParams object
  const queryString = Object.keys(queryParams)
    .filter(key => queryParams[key] !== undefined && queryParams[key] !== null && queryParams[key] !== '')
    .map(key => `${encodeURIComponent(key)}=${encodeURIComponent(queryParams[key])}`)
    .join('&')
  
  if (queryString) {
    path += `?${queryString}`
    }
  
  return getApi(baseURL).get(path)
}

function preserveVariable(clusterName, dbId, variableName, action, baseURL) {
  // action can be 'preserve', 'accept', or 'clear'
  return getApi(baseURL).post(`clusters/${clusterName}/servers/${dbId}/variables-${action}?variableName=${variableName}`)
}

function setCustomVariableValue(clusterName, dbId, variableName, customValue, baseURL) {
  // Set a custom preserved value for a variable
  return getApi(baseURL).post(`clusters/${clusterName}/servers/${dbId}/variables-set-custom?variableName=${encodeURIComponent(variableName)}&customValue=${encodeURIComponent(customValue)}`)
}

function updateLongQueryTime(clusterName, dbId, time, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/actions/set-long-query-time/${time}`)
}

function toggleDatabaseActions(clusterName, serviceName, dbId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/actions/${serviceName}`)
}

function killThread(clusterName, dbId, queryDigest, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/queries/${queryDigest}/actions/kill-thread`)
}

function killQuery(clusterName, dbId, queryDigest, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/queries/${queryDigest}/actions/kill-query`)
}

function checksumTable(clusterName, schema, table, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema/${schema}/${table}/actions/checksum-table`)
}

function checksumSchema(clusterName, schema, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema/${schema}/all/actions/checksum-schema`)
}

function analyzeTable(clusterName, schema, table, persistent, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema/${schema}/${table}/actions/analyze-table/${persistent}`)
}

function analyzeSchema(clusterName, schema, persistent, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema/${schema}/all/actions/analyze-schema/${persistent}`)
}

function monitorAllSchemas(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/monitor-schemas`)
}

//#endregion Database service APIs

//#region Test run APIs
function runSysbench(clusterName, threads, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/sysbench?threads=${threads}`)
}

function runRegressionTests(clusterName, testName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/tests/actions/run/${testName}`)
}
//#endregion Test run APIs

//#region User management APIs
function addUser(clusterName, username, grants, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/add`, { username, grants, roles })
}

function updateGrants(clusterName, username, grants, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/update`, { username, grants, roles })
}

function dropUser(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/drop`, { username })
}

function sendCredentials(clusterName, username, type, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/send-credentials`, { username, type })
}

//#Peer subscription APIs
function clusterSubscribe(clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/subscribe`)
}

function clusterUnsubscribe(username, clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/unsubscribe`, { username })
}

function acceptSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/accept-subscription`, { username })
}

function rejectSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/refuse-subscription`, { username })
}

function endSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/end-subscription`, { username })
}

function subscribeExternalRole(clusterName, username, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/subscribe`, { username, roles })
}

function quoteExternalRole(clusterName, username, roles, cost, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/quote`, { username, roles, cost })
}

function acceptExternalRole(clusterName, username, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/accept`, { username, roles })
}

function refuseExternalRole(clusterName, username, roles, reason, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/refuse`, { username, roles, reason })
}

function endExternalRole(clusterName, username, roles, reason, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/end`, { username, roles, reason })
}

function addClusterShard(clusterName, shardClusterName, formdata, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/actions/add/${shardClusterName}`, formdata)
}

//#endregion User management APIs

function getClusterApps(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/topology/apps`)
}

//#region App management APIs
function provisionApp(clusterName, appId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/actions/provision`)
}

function unprovisionApp(clusterName, appId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/actions/unprovision`)
}

function startApp(clusterName, appId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/actions/start`)
}

function stopApp(clusterName, appId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/actions/stop`)
}

function getAppService(clusterName, serviceName, appId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/${serviceName}`)
}

function resolveTemplateVariables(clusterName, appId, rawValue, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/resolve-template`, { data: rawValue })
}

function addDeployment(clusterName, appId, deployment, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/deployment/add`, deployment)
}

function dropDeployment(clusterName, appId, deployName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/deployment/drop/${deployName}`)
}

function deploymentFieldChange(clusterName, appId, field, index, key, value, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/deployment/${field}/index/${index}/${key}/modify`, { value })
}
function deploymentFieldIndexAdd(clusterName, appId, field, value, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/deployment/${field}/add`, value)
}
function deploymentFieldIndexDrop(clusterName, appId, field, index, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/deployment/${field}/index/${index}/drop`)
}

function storageFieldChange(clusterName, appId, field, index, key, value, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/storages/${field}/index/${index}/${key}/modify`, { value })
}
function storageFieldIndexAdd(clusterName, appId, field, value, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/storages/${field}/add`, value)
}
function storageFieldIndexDrop(clusterName, appId, field, index, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/storages/${field}/index/${index}/drop`)
}
//#endregion App management APIs

function connectDockerRegistry(clusterName, dockerRegistry = {}, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/actions/docker/actions/registry-connect`, { ...dockerRegistry })
}


// Restic functions
function purgeResticSnapshot(clusterName, snapshotId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/purge/${snapshotId}`)
}

function purgeResticByPolicy(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/purge/policy`)
}

function getResticQueue(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue`)
}

function resticQueueResume(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/resume`)
}

function resticQueuePause(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/pause`)
}

function resticQueueMove(clusterName, moveType, taskID, afterID, baseURL) {
  if (moveType == "after") {
    return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/move/${moveType}/${taskID}/${afterID}`)
  } else {
    return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/move/${moveType}/${taskID}`)
  }
}

function resticQueueCancel(clusterName, taskID,  baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/cancel/${taskID}`)
}

function resticQueueReset(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/restic/task-queue/reset`)
}

// Utility functions