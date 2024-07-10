import { getRequest } from './apiHelper'

export const clusterService = {
  getClusters,
  getMonitoredData,
  getClusterData,
  getClusterAlerts,
  getClusterMaster,
  getClusterServers
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
