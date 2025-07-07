
import { getApi } from './apiHelper'

export const pathService = {
  getDockerDirectoryTree,
  getGitDirectoryTree
}

function getDockerDirectoryTree(clusterName, dockerImage, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/docker/images/${encodeURIComponent(dockerImage)}/browse`)
}

function getGitDirectoryTree(clusterName, appId, gitName, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/git/${encodeURIComponent(gitName)}/actions/get-repo-tree`)
}