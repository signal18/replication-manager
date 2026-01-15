import { getApi, postApi, deleteApi } from './apiHelper'

function executeSQLScript(clusterName, payload) {
  return postApi(`clusters/${clusterName}/actions/execute-sql-script`, payload)
}

function triggerScheduledScripts(clusterName) {
  return postApi(`clusters/${clusterName}/actions/trigger-scheduled-sql-scripts`, {})
}

function getSQLScriptJobs(clusterName) {
  return getApi(`clusters/${clusterName}/sql-jobs`)
}

function saveSQLScriptJob(clusterName, job) {
  return postApi(`clusters/${clusterName}/sql-jobs/save`, job)
}

function deleteSQLScriptJob(clusterName, jobName) {
  return deleteApi(`clusters/${clusterName}/sql-jobs/${jobName}`)
}

export const sqlScriptService = {
  executeSQLScript,
  triggerScheduledScripts,
  getSQLScriptJobs,
  saveSQLScriptJob,
  deleteSQLScriptJob
}
