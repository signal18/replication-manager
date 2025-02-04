import { getApi } from './apiHelper'

export const clusterService = {
  // Cluster data APIs
  getClusterData,
  getClusterAlerts,
  getClusterMaster,
  getClusterServers,
  getClusterProxies,
  getClusterCertificates,
  getTopProcess,
  getBackupSnapshot,
  getBackupStats,
  getJobs,
  getShardSchema,
  getQueryRules,

  // Cluster management APIs
  checksumAllTables,
  switchOverCluster,
  failOverCluster,
  resetFailOverCounter,
  resetSLA,
  toggleTraffic,
  addServer,
  dropServer,
  provisionCluster,
  unProvisionCluster,
  setCredentials,
  rotateDBCredential,
  rollingOptimize,
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
  reloadStagingScript,

  // Server management APIs
  setMaintenanceMode,
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

  // Database service APIs
  getDatabaseService,
  updateLongQueryTime,
  toggleDatabaseActions,
  checksumTable,

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

function getClusterCertificates(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/certificates`)
}

function getTopProcess(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/top`)
}

function getBackupSnapshot(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/backups`)
}

function getBackupStats(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/backups/stats`)
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

function addServer(clusterName, host, port, dbType, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/addserver/${host}/${port}/${dbType}`)
}

function dropServer(clusterName, host, port, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/dropserver/${host}/${port}`)
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

function reloadStagingScript(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/actions/staging-reload-script`)
}
//#endregion Cluster management APIs

//#region Server management APIs
function setMaintenanceMode(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/maintenance`)
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
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/toogle-read-only`)
}

function resetMaster(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reset-master`)
}

function resetSlaveAll(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/reset-slave-all`)
}

function cancelServerJob(clusterName, serverId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${serverId}/actions/cancel-job`)
}
//#endregion Server management APIs

//#region Proxy management APIs
function provisionProxy(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/actions/provision`)
}

function unprovisionProxy(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/actions/unprovision`)
}

function startProxy(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/actions/start`)
}

function stopProxy(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/proxies/actions/stop`)
}
//#endregion Proxy management APIs

//#region Database service APIs
function getDatabaseService(clusterName, serviceName, dbId, baseURL) {
  return getApi(baseURL).getRequest(`clusters/${clusterName}/servers/${dbId}/${serviceName}`)
}

function updateLongQueryTime(clusterName, dbId, time, baseURL) {
  return getApi(baseURL).getRequest(`clusters/${clusterName}/servers/${dbId}/actions/set-long-query-time/${time}`)
}

function toggleDatabaseActions(clusterName, serviceName, dbId, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/actions/${serviceName}`)
}

function checksumTable(clusterName, schema, table, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/schema/${schema}/${table}/actions/checksum-table`)
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
  return getApi(baseURL).post(`clusters/${clusterName}/users/add`, {username, grants, roles})
}

function updateGrants(clusterName, username, grants, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/update`, {username, grants, roles})
}

function dropUser(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/drop`, {username})
}

function sendCredentials(clusterName, username, type, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/users/send-credentials`,{username, type})
}

//#Peer subscription APIs
function clusterSubscribe(username, password, clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/subscribe`,{username, password})
}

function clusterUnsubscribe(username, clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/unsubscribe`,{username})
}

function acceptSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/accept-subscription`,{username})
}

function rejectSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/refuse-subscription`,{username})
}

function endSubscription(clusterName, username, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/sales/end-subscription`,{username})
}

function subscribeExternalRole(clusterName, username, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/subscribe`, {username, roles})
}

function quoteExternalRole(clusterName, username, roles, cost, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/quote`, {username, roles, cost})
}

function acceptExternalRole(clusterName, username, roles, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/accept`, {username, roles})
}

function refuseExternalRole(clusterName, username, roles, reason, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/refuse`, {username, roles, reason})
}

function endExternalRole(clusterName, username, roles, reason, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/ext-role/end`, {username, roles, reason})
}

//#endregion User management APIs
