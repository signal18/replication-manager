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
  getGlobalLogHistory,
  getGlobalJobs,
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
  unregister,
  getSubscription,
  changeSubscription,
  getSubscriptionPlans,
  setServerActiveStatus,
  fetchDynamicClustersFromGit
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

// getGlobalLogHistory reads on-disk log history (beyond the in-memory ring
// buffer) by adding since/until to the same endpoint as getGlobalLogs -
// server/api_global.go's handlerMuxGlobalLogs switches to a bounded on-disk
// scan whenever since/until is present. params: { since, until, level,
// module, text, limit }.
function getGlobalLogHistory(params, baseURL) {
  return getApi(baseURL).get('global/http-logs', params)
}

function getGlobalJobs(baseURL) {
  return getApi(baseURL).get('global/jobs')
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

function getClusterPeerNodes() {
  return getApi().get('peers')
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

function unregister() {
  return getApi().post('register/unregister')
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

function setServerActiveStatus(baseURL) {
  return getApi(baseURL).get('actions/set-active-status')
}

function fetchDynamicClustersFromGit() {
  return getApi().post('clusters/actions/fetch-dynamic-from-git')
}
