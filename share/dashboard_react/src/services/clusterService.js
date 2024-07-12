import { getRequest } from './apiHelper'

export const clusterService = {
  getClusters,
  getMonitoredData,
  getClusterData,
  getClusterAlerts,
  getClusterMaster,
  getClusterServers,
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
  cancelRollingReprov
}

function getClusters() {
  return getRequest('clusters')
}

function getMonitoredData() {
  return getRequest('monitor')
}

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
