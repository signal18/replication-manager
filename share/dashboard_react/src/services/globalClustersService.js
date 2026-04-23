import { getApi } from './apiHelper'

export const globalClustersService = {
  getClusters,
  getClusterPeers,
  getClusterForSale,
  getMonitoredData,
  getTermsData,
  getGlobalAlerts,
  getGlobalMetrics,
  getGlobalLogs,
  switchGlobalSetting,
  setGlobalSetting,
  clearGlobalSetting,
  addCluster,
  dropCluster,
  renameCluster,
  reloadClustersPlan,
  reloadClustersPlanInfo,
  refreshAppTemplateRepo,
  getAppTemplateStructureGuide,
  register,
  confirmRegister,
  getRegisterStatus,
  getSubscription,
  changeSubscription,
  getSubscriptionPlans
}

function getClusters(baseURL) {
  return getApi(baseURL).get('clusters')
}

function getMonitoredData(baseURL) {
  return getApi(baseURL).get('monitor')
}

function getGlobalAlerts(baseURL) {
  return getApi(baseURL).get('global/alerts')
}

function getGlobalMetrics(baseURL) {
  return getApi(baseURL).get('global/metrics')
}

function getGlobalLogs(baseURL) {
  return getApi(baseURL).get('global/http-logs')
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

function reloadClustersPlan(download = true) {
  return getApi().post(`clusters/settings/actions/reload-clusters-plans`, { download })
}

function reloadClustersPlanInfo(download = true) {
  return getApi().post(`clusters/settings/actions/reload-clusters-plan-info`, { download })
}

function refreshAppTemplateRepo(clusterName, baseURL, forceRefresh = false) {
	const suffix = forceRefresh ? '?forceRefresh=true' : ''
	return getApi(baseURL).get(`clusters/${clusterName}/templates/apps${suffix}`)
}

function getAppTemplateStructureGuide(clusterName, baseURL) {
	return getApi(baseURL).get(`clusters/${clusterName}/templates/apps/structure-guide`)
}

function register(email, password, uri) {
  return getApi().post('register', { email, password, uri })
}

function confirmRegister(email, password, uri) {
  return getApi().post('register/confirm', { email, password, uri })
}

function getRegisterStatus() {
  return getApi().get('register/status')
}

function getSubscription() {
  return getApi().get('register/subscription')
}

function changeSubscription(plan) {
  return getApi().post('register/subscription', { plan })
}

function getSubscriptionPlans() {
  return getApi().get('register/subscription/plans')
}
