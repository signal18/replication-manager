import { getRequest } from './apiHelper'

export const settingsService = {
  //general
  switchSettings,
  changeTopology
}

//#region general settings
function switchSettings(clusterName, setting) {
  return getRequest(`clusters/${clusterName}/settings/actions/switch/${setting}`)
}

function changeTopology(clusterName, topology) {
  return getRequest(`clusters/${clusterName}/settings/actions/set/topology-target/${topology}`)
}
