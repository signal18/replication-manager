import { getRequest } from './apiHelper'

export const clusterService = {
  getClusters
}

function getClusters() {
  return getRequest('clusters')
}
