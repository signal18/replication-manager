import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { settingsService } from '../services/settingsService'

export const switchSetting = createAsyncThunk('settings/switchSetting', async ({ clusterName, setting }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.switchSettings(clusterName, setting, baseURL)
    if (status === 200) {
      showSuccessBanner(`Switching ${setting} successful!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Switching ${setting} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const changeTopology = createAsyncThunk(
  'settings/changeTopology',
  async ({ clusterName, topology }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await settingsService.changeTopology(clusterName, topology, baseURL)
      showSuccessBanner(`Topology changed to ${topology} successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Changing topology to ${topology} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setSetting = createAsyncThunk('settings/setSetting', async ({ clusterName, setting, value }, thunkAPI) => {
  try {
    // showLoaderBanner(`${setting} `, thunkAPI)
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = value !== "" ? await settingsService.setSetting(clusterName, setting, value, baseURL) : await settingsService.clearSetting(clusterName, setting, baseURL)
    if (status === 200) {
      showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Changing ${setting} failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const updateGraphiteWhiteList = createAsyncThunk(
  'settings/updateGraphiteWhiteList',
  async ({ clusterName, whiteListValue }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await settingsService.updateGraphiteWhiteList(clusterName, whiteListValue, baseURL)
      showSuccessBanner(`Graphite Whitelist Regexp updated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Updating Graphite Whitelist Regexp failed!`, error.toString(), thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const updateGraphiteBlackList = createAsyncThunk(
  'settings/updateGraphiteBlackList',
  async ({ clusterName, blackListValue }, thunkAPI) => {
    try {
      const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
      const { data, status } = await settingsService.updateGraphiteBlackList(clusterName, blackListValue, baseURL)
      showSuccessBanner(`Graphite BlackList Regexp updated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Updating Graphite BlackList Regexp failed!`, error.toString(), thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setAppSetting = createAsyncThunk('settings/setAppSetting', async ({ clusterName, appId, setting, value }, thunkAPI) => {
  try {
    // showLoaderBanner(`${setting} `, thunkAPI)
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = value !== "" ? await settingsService.setAppSetting(clusterName, appId, setting, value, baseURL) : await settingsService.clearAppSetting(clusterName, setting, baseURL)
    if (status === 200) {
      showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Changing ${setting} failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const switchAppSetting = createAsyncThunk('settings/switchAppSetting', async ({ clusterName, appId, setting }, thunkAPI) => {
  try {
    // showLoaderBanner(`${setting} `, thunkAPI)
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.switchAppSettings(clusterName, appId, setting, baseURL)
    if (status === 200) {
      showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Changing ${setting} failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const resetAppFromTemplate = createAsyncThunk('settings/resetAppFromTemplate', async ({ clusterName, appId, template, forceRefresh = false }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.resetAppFromTemplate(clusterName, appId, template, forceRefresh, baseURL)
    if (status === 200) {
      showSuccessBanner(forceRefresh ? `App template refreshed and reset successfully!` : `App reset from template successfully!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Resetting app from template failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const previewResetAppTemplateImpact = createAsyncThunk('settings/previewResetAppTemplateImpact', async ({ clusterName, appId, template, forceRefresh = false }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.previewResetAppTemplateImpact(clusterName, appId, template, forceRefresh, baseURL)
    if (status === 200) {
      return { data, status }
    }
    throw new Error(data)
  } catch (error) {
    showErrorBanner(`Loading reset impact preview failed!`, error.toString(), thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const previewAppTemplateContent = createAsyncThunk('settings/previewAppTemplateContent', async ({ clusterName, templateName, forceRefresh = false }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.getAppTemplateContent(clusterName, templateName, forceRefresh, baseURL)
    if (status === 200) {
      return { data, status }
    }
    throw new Error(data)
  } catch (error) {
    showErrorBanner(`Loading template preview failed!`, error.toString(), thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const saveAppTemplateContent = createAsyncThunk('settings/saveAppTemplateContent', async ({ clusterName, templateName, content }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.saveAppTemplateContent(clusterName, templateName, content, baseURL)
    if (status === 200) {
      showSuccessBanner(`Template saved successfully!`, status, thunkAPI)
      return { data, status }
    }
    throw new Error(data)
  } catch (error) {
    showErrorBanner(`Saving template failed!`, error.toString(), thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const deleteAppTemplateContent = createAsyncThunk('settings/deleteAppTemplateContent', async ({ clusterName, templateName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.deleteAppTemplateContent(clusterName, templateName, baseURL)
    if (status === 200) {
      showSuccessBanner(`Template deleted successfully!`, status, thunkAPI)
      return { data, status }
    }
    throw new Error(data)
  } catch (error) {
    showErrorBanner(`Deleting template failed!`, error.toString(), thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const createLocalAppTemplateCopy = createAsyncThunk('settings/createLocalAppTemplateCopy', async ({ clusterName, templateName, localTemplateName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.createLocalAppTemplateCopy(clusterName, templateName, localTemplateName, baseURL)
    if (status === 200) {
      showSuccessBanner(`Local template copy created successfully!`, status, thunkAPI)
      return { data, status }
    }
    throw new Error(data)
  } catch (error) {
    showErrorBanner(`Creating local template copy failed!`, error.toString(), thunkAPI)
    return handleError(error, thunkAPI)
  }
})

export const saveAppAsTemplate = createAsyncThunk('settings/saveAppAsTemplate', async ({ clusterName, appId, template }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await settingsService.saveAppAsTemplate(clusterName, appId, template, baseURL)
    if (status === 200) {
      showSuccessBanner(`App saved as template successfully!`, status, thunkAPI)
      return { data, status }
    } else {
      throw new Error(data)
    }
  } catch (error) {
    showErrorBanner(`Saving app as template failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

const initialState = {}

export const settingsSlice = createSlice({
  name: 'settings',
  initialState,
  reducers: {
    clearSettings: (state) => {
      Object.assign(state, initialState)
    }
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
