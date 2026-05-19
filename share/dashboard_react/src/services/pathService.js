
import { getApi } from './apiHelper'

export const pathService = {
  getDockerDirectoryTree,
  getGitDirectoryTree,
  checkGitRepo,
}

function getDockerDirectoryTree(clusterName, dockerImage, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/docker/browse/${encodeURIComponent(dockerImage)}`)
}

function getGitDirectoryTree(clusterName, appId, gitName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/git/${encodeURIComponent(gitName)}/actions/get-repo-tree`)
}

function checkGitRepo(clusterName, appName, payload, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appName}/git/actions/check`, payload)
}