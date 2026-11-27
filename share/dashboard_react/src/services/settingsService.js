import { getApi } from './apiHelper'

function normalizeTemplateName(template) {
  if (typeof template === 'string') {
    return template
  }

  if (template && typeof template === 'object') {
    const candidate = template.value || template.name || template.label || ''
    return typeof candidate === 'string' ? candidate : ''
  }

  return ''
}

export const settingsService = {
  switchSettings,
  changeTopology,
  setSetting,
  setSettingInBody,
  changePlanUnits,
  clearSetting,
  updateGraphiteWhiteList,
  updateGraphiteBlackList,
  setAppSetting,
  switchAppSettings,
  clearAppSetting,
  resetAppFromTemplate,
  previewResetAppTemplateImpact,
  getAppTemplateContent,
  saveAppTemplateContent,
  deleteAppTemplateContent,
  createLocalAppTemplateCopy,
  saveAppAsTemplate,
  reloadPlanInfo
}

function switchSettings(clusterName, setting, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/switch/${setting}`)
}

function changeTopology(clusterName, topology, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set/topology-target/${topology}`)
}

function setSetting(clusterName, setting, value, baseURL) {
  if (setting === 'reset-graphite-filterlist') {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/${setting}/${value}`)
  } else if (setting.includes('-cron')) {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set-cron/${setting}/${encodeURIComponent(value)}`)
  } else {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/set/${setting}/${encodeURIComponent(value)}`)
  }
}

// the value as the POST body, the URL ending at the setting name: for a value that must not sit in
// a URL (a private key), which proxies log
function setSettingInBody(clusterName, setting, value, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/set/${setting}/`, value, { headers: { 'Content-Type': 'text/plain' } })
}

function changePlanUnits(clusterName, unit, delta, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/change-plan-units/${unit}/${delta}`)
}

function clearSetting(clusterName, setting, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/settings/actions/clear/${setting}`)
}

function updateGraphiteWhiteList(clusterName, whiteListValue, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/whitelist`, {
    whitelist: whiteListValue
  })
}

function updateGraphiteBlackList(clusterName, blackListValue, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/set-graphite-filterlist/blacklist`, {
    blacklist: blackListValue
  })
}

function setAppSetting(clusterName, appId, setting, value, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/set/${setting}/${encodeURIComponent(value)}`)
}

function switchAppSettings(clusterName, appId, setting, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/switch/${setting}`)
}

function clearAppSetting(clusterName, appId, setting, baseURL) {
    return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/clear/${setting}`)
}

function resetAppFromTemplate(clusterName, appId, template, forceRefresh = false, baseURL) {
  const payload = { template: normalizeTemplateName(template) }
  if (forceRefresh) {
    payload.forceRefresh = true
  }
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/settings/actions/reset-from-template`, payload)
}

function previewResetAppTemplateImpact(clusterName, appId, template, forceRefresh = false, baseURL) {
  const normalizedTemplate = normalizeTemplateName(template)
  return getApi(baseURL).post(`clusters/${clusterName}/apps/${appId}/settings/actions/reset-from-template/preview`, {
    template: normalizedTemplate,
    forceRefresh
  })
}

function getAppTemplateContent(clusterName, templateName, forceRefresh = false, baseURL) {
	const query = forceRefresh ? '?forceRefresh=true' : ''
	return getApi(baseURL).get(`clusters/${clusterName}/templates/apps/${templateName}/content${query}`)
}

function saveAppTemplateContent(clusterName, templateName, content, baseURL) {
	return getApi(baseURL).post(`clusters/${clusterName}/templates/apps/${templateName}/content/actions/save`, {
		content
	})
}

function deleteAppTemplateContent(clusterName, templateName, baseURL) {
	return getApi(baseURL).post(`clusters/${clusterName}/templates/apps/${templateName}/content/actions/delete`)
}

function createLocalAppTemplateCopy(clusterName, templateName, localTemplateName, baseURL) {
	return getApi(baseURL).post(`clusters/${clusterName}/templates/apps/${templateName}/content/actions/create-local-copy`, {
		localTemplateName
	})
}

function saveAppAsTemplate( clusterName, appId, template, baseURL) {
  return getApi(baseURL).get(`clusters/${clusterName}/apps/${appId}/settings/actions/save-as-template/${template}`)
}

function reloadPlanInfo(clusterName, baseURL) {
  return getApi(baseURL).post(`clusters/${clusterName}/settings/actions/reload-plan-info`)
}
