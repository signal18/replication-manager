import { getApi } from './apiHelper'

export const configService = {
  addDBTag,
  dropDBTag,
  addProxyTag,
  dropProxyTag,
  generateConfig,
  generateAllConfigs,
  preserveConfigPath,
}

function addDBTag(clusterName, tag, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/add-db-tag/${tag}`)
}

function dropDBTag(clusterName, tag, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/drop-db-tag/${tag}`)
}

function addProxyTag(clusterName, tag, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/add-proxy-tag/${tag}`)
}

function dropProxyTag(clusterName, tag, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/drop-proxy-tag/${tag}`)
}

function generateConfig(clusterName, host, port, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${host}/${port}/config-gen`)
}

function generateAllConfigs(clusterName, type, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/generate-configs/${type}`)
}

function preserveConfigPath(clusterName, dbId, preserve, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/servers/${dbId}/config-path-preserve/${preserve}`)
}
