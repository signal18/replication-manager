import { getRequest } from './apiHelper'

export const globalClustersService = {
  getClusters,
  getClusterPeers,
  getMonitoredData,
  switchGlobalSetting
}

function getClusterPeers() {
  return getRequest('clusters/peers')
}
function getClusters() {
  return getRequest('clusters')
}

function getMonitoredData() {
  return getRequest('monitor')
}

function switchGlobalSetting(setting) {
  return getRequest(`clusters/settings/actions/switch/${setting}`)
}
