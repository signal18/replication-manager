import { getApi } from './apiHelper'

export const configService = {
  addDBTag,
  dropDBTag,
  addProxyTag,
  dropProxyTag,
  generateConfig,
  generateAllConfigs,
  preserveConfigPath,
  getPreservedVarsCnf,
  savePreservedVarsCnf,
  getTagContent,
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

function getPreservedVarsCnf(clusterName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/preserved-variables-cnf`)
}

function savePreservedVarsCnf(clusterName, content, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/save-preserved-variables-cnf`, { content })
}

function getTagContent(clusterName, tagName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/configurator/tags/${tagName}/content`)
}
