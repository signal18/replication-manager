import { getRequest, postRequest } from './apiHelper'

export const configService = {
  addDBTag,
  dropDBTag,
  addProxyTag,
  dropProxyTag
}

function addDBTag(clusterName, tag) {
  return getRequest(`clusters/${clusterName}/settings/actions/add-db-tag/${tag}`)
}

function dropDBTag(clusterName, tag) {
  return getRequest(`clusters/${clusterName}/settings/actions/drop-db-tag/${tag}`)
}

function addProxyTag(clusterName, tag) {
  return getRequest(`clusters/${clusterName}/settings/actions/add-proxy-tag/${tag}`)
}

function dropProxyTag(clusterName, tag) {
  return getRequest(`clusters/${clusterName}/settings/actions/drop-proxy-tag/${tag}`)
}
