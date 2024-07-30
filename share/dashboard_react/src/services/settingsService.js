import { getRequest } from './apiHelper'

export const settingsService = {
  switchSettings,
  changeTopology,
  setSettingsNullable
}

function switchSettings(clusterName, setting) {
  return getRequest(`clusters/${clusterName}/settings/actions/switch/${setting}`)
}

function changeTopology(clusterName, topology) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/topology-target/${topology}`)
}

function setSettingsNullable(clusterName, setting, value) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/${setting}/${value}`)
}
