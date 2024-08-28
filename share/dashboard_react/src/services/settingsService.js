import { getRequest, postRequest } from './apiHelper'

export const settingsService = {
  switchSettings,
  changeTopology,
  setSetting,
  updateGraphiteWhiteList,
  updateGraphiteBlackList
}

function switchSettings(clusterName, setting) {
  return getRequest(`clusters/${clusterName}/settings/actions/switch/${setting}`)
}

function changeTopology(clusterName, topology) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/topology-target/${topology}`)
}

function setSetting(clusterName, setting, value) {
  if (setting === 'reset-graphite-filterlist') {
    return getRequest(`clusters/${clusterName}/settings/actions/${setting}/${value}`)
  } else if (setting.includes('-cron')) {
    return getRequest(`clusters/${clusterName}/settings/actions/set-cron/${setting}/${encodeURIComponent(value)}`)
  } else {
    return getRequest(`clusters/${clusterName}/settings/actions/set/${setting}/${value}`)
  }
}

function updateGraphiteWhiteList(clusterName, whiteListValue) {
  return postRequest(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/whitelist`, {
    whitelist: whiteListValue
  })
}

function updateGraphiteBlackList(clusterName, blackListValue) {
  return postRequest(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/blacklist`, {
    blacklist: blackListValue
  })
}
