import { getRequest } from './apiHelper'

export const clusterService = {
  getClusters,
  getMonitoredData,
  getClusterData,
  getClusterAlerts,
  getClusterMaster,
  getClusterServers,
  getClusterProxies,
  switchOverCluster,
  failOverCluster,
  resetFailOverCounter,
  resetSLA,
  toggleTraffic,
  addServer,
  provisionCluster,
  unProvisionCluster,
  setDBCredential,
  setReplicationCredential,
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
  skip1ReplicationEvent,
  toggleInnodbMonitor,
  toggleSlowQueryCapture,
  startSlave,
  stopSlave,
  toggleReadOnly,
  resetMaster,
  resetSlave
}

//#region main
function getClusters() {
  return getRequest('clusters')
}

function getMonitoredData() {
  return getRequest('monitor')
}
//#endregion main

//#region cluster apis
function getClusterData(clusterName) {
  return getRequest(`clusters/${clusterName}`)
}

function getClusterAlerts(clusterName) {
  return getRequest(`clusters/${clusterName}/topology/alerts`)
}

function getClusterMaster(clusterName) {
  return getRequest(`clusters/${clusterName}/topology/master`)
}

function getClusterServers(clusterName) {
  return getRequest(`clusters/${clusterName}/topology/servers`)
}

function getClusterProxies(clusterName) {
  return getRequest(`clusters/${clusterName}/topology/proxies`)
}

function switchOverCluster(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/switchover`)
}
function failOverCluster(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/failover`)
}

function resetFailOverCounter(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/reset-failover-control`)
}
function resetSLA(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/reset-sla`)
}

function toggleTraffic(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/switch/database-heartbeat`)
}

function addServer(clusterName, host, port, dbType) {
  return getRequest(`clusters/${clusterName}/actions/addserver/${host}/${port}/${dbType}`)
}

function provisionCluster(clusterName) {
  return getRequest(`clusters/${clusterName}/services/actions/provision`)
}

function unProvisionCluster(clusterName) {
  return getRequest(`clusters/${clusterName}/services/actions/unprovision`)
}

function setDBCredential(clusterName, credential) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/db-servers-credential/${credential}`)
}

function setReplicationCredential(clusterName, credential) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/replication-credential/${credential}`)
}

function rotateDBCredential(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/rotate-passwords`)
}

function rollingOptimize(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/optimize`)
}

function rollingRestart(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/rolling`)
}

function rotateCertificates(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/certificates-rotate`)
}

function reloadCertificates(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/certificates-reload`)
}

function cancelRollingRestart(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/cancel-rolling-restart`)
}

function cancelRollingReprov(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/cancel-rolling-reprov`)
}

function bootstrapMasterSlave(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/replication/bootstrap/master-slave`)
}

function bootstrapMasterSlaveNoGtid(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/replication/bootstrap/master-slave-no-gtid`)
}

function bootstrapMultiMaster(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/replication/bootstrap/multi-master`)
}

function bootstrapMultiMasterRing(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/replication/bootstrap/multi-master-ring`)
}

function bootstrapMultiTierSlave(clusterName) {
  return getRequest(`clusters/${clusterName}/actions/replication/bootstrap/multi-tier-slave`)
}

function configReload(clusterName) {
  return getRequest(`clusters/${clusterName}/settings/actions/reload`)
}

function configDiscoverDB(clusterName) {
  return getRequest(`clusters/${clusterName}/settings/actions/discover`)
}

function configDynamic(clusterName) {
  return getRequest(`clusters/${clusterName}/settings/actions/apply-dynamic-config`)
}
//#endregion cluster apis

//#region cluster>servers apis
function setMaintenanceMode(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/maintenance`)
}

function promoteToLeader(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/switchover`)
}

function setAsUnrated(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/set-unrated`)
}

function setAsPreferred(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/set-prefered`)
}

function setAsIgnored(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/set-ignored`)
}

function reseedLogicalFromBackup(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/reseed/logicalbackup`)
}

function reseedLogicalFromMaster(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/reseed/logicalmaster`)
}

function reseedPhysicalFromBackup(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/reseed/physicalbackup`)
}

function flushLogs(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/flush-logs`)
}

function physicalBackupMaster(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/master-physical-backup`)
}

function logicalBackup(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/backup-logical`)
}

function stopDatabase(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/stop`)
}

function startDatabase(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/start`)
}

function provisionDatabase(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/provision`)
}

function unprovisionDatabase(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/unprovision`)
}

function runRemoteJobs(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/run-jobs`)
}

function optimizeServer(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/optimize`)
}

function skip1ReplicationEvent(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/skip-replication-event`)
}

function toggleInnodbMonitor(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/toogle-innodb-monitor`)
}

function toggleSlowQueryCapture(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/toogle-slow-query-capture`)
}

function startSlave(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/start-slave`)
}

function stopSlave(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/stop-slave`)
}

function toggleReadOnly(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/toogle-read-only`)
}

function resetMaster(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/reset-master`)
}

function resetSlave(clusterName, serverId) {
  return getRequest(`clusters/${clusterName}/servers/${serverId}/actions/reset-slave-all`)
}
//#endregion cluster>servers apis
