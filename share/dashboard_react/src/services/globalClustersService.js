import { getApi } from './apiHelper'

export const globalClustersService = {
  getClusters,
  getClusterPeers,
  getClusterForSale,
  getMonitoredData,
  getTermsData,
  switchGlobalSetting,
  setGlobalSetting,
  clearGlobalSetting,
  addCluster,
  dropCluster,
  renameCluster,
  reloadClustersPlan
}

function getClusters(baseURL) {
  return getApi(baseURL).get('clusters')
}

function getMonitoredData(baseURL) {
  return getApi(baseURL).get('monitor')
}

function getTermsData(baseURL) {
  return getApi(baseURL).get('terms')
}

function getClusterPeers() {
  return getApi().get('clusters/peers')
}

function getClusterForSale() {
  return getApi().get('clusters/for-sale')
}

function switchGlobalSetting(setting) {
  return getApi().get(`clusters/settings/actions/switch/${setting}`)
}

function setGlobalSetting(setting, value) {
  return getApi().get(`clusters/settings/actions/set/${setting}/${value}`)
}

function clearGlobalSetting(setting) {
  return getApi().get(`clusters/settings/actions/clear/${setting}`)
}

function addCluster(clusterName, formdata) {
  return getApi().post(`clusters/actions/add/${clusterName}`, formdata)
}

function dropCluster(clusterName) {
  return getApi().post(`clusters/actions/delete/${clusterName}`)
}

function renameCluster(clusterName, newClusterName) {
  return getApi().post(`clusters/actions/rename/${clusterName}/${newClusterName}`)
}

function reloadClustersPlan() {
  return getApi().get(`clusters/settings/actions/reload-clusters-plans`)
}
