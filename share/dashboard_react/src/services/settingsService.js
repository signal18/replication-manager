import { getApi } from './apiHelper'

export const settingsService = {
  switchSettings,
  changeTopology,
  setSetting,
  clearSetting,
  updateGraphiteWhiteList,
  updateGraphiteBlackList,
  setAppSetting,
  switchAppSettings,
  clearAppSetting,
  resetAppFromTemplate,
  saveAppAsTemplate,
  reloadPlanInfo
}

function switchSettings(clusterName, setting, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/switch/${setting}`)
}

function changeTopology(clusterName, topology, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set/topology-target/${topology}`)
}

function setSetting(clusterName, setting, value, baseURL) {
  if (setting === 'reset-graphite-filterlist') {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/${setting}/${value}`)
  } else if (setting.includes('-cron')) {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set-cron/${setting}/${encodeURIComponent(value)}`)
  } else {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set/${setting}/${encodeURIComponent(value)}`)
  }
}

function clearSetting(clusterName, setting, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/clear/${setting}`)
}

function updateGraphiteWhiteList(clusterName, whiteListValue, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/whitelist`, {
    whitelist: whiteListValue
  })
}

function updateGraphiteBlackList(clusterName, blackListValue, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/blacklist`, {
    blacklist: blackListValue
  })
}

function setAppSetting(clusterName, appId, setting, value, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/set/${setting}/${encodeURIComponent(value)}`)
}

function switchAppSettings(clusterName, setting, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/switch/${setting}`)
}

function clearAppSetting(clusterName, appId, setting, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/clear/${setting}`)
}

function resetAppFromTemplate(clusterName, appId, template, forceRefresh = false, baseURL) {
  const payload = { template: template }
  if (forceRefresh) {
    payload.forceRefresh = true
  }
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/settings/actions/reset-from-template`, payload)
}

function saveAppAsTemplate( clusterName, appId, template, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/save-as-template/${template}`)
}

function reloadPlanInfo(clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/reload-plan-info`)
}
