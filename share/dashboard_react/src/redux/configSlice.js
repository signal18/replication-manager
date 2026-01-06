import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { configService } from '../services/configService'

export const addDBTag = createAsyncThunk('configs/addDBTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.addDBTag(clusterName, tag, baseURL)
    showSuccessBanner(`Db tag ${tag} added successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Adding db tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})
export const dropDBTag = createAsyncThunk('configs/dropDBTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.dropDBTag(clusterName, tag, baseURL)
    showSuccessBanner(`Db tag ${tag} dropped successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Dropping db tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const addProxyTag = createAsyncThunk('configs/addProxyTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.addProxyTag(clusterName, tag, baseURL)
    showSuccessBanner(`Proxy tag ${tag} added successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Adding proxy tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const dropProxyTag = createAsyncThunk('configs/dropProxyTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.dropProxyTag(clusterName, tag, baseURL)
    showSuccessBanner(`Proxy tag ${tag} dropped successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Dropping proxy tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const generateConfig = createAsyncThunk('configs/generateConfig', async ({ clusterName, host, port }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.generateConfig(clusterName, host, port, baseURL)
    showSuccessBanner(`Config generated successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Generating config failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const generateAllConfig = createAsyncThunk('configs/generateAllConfig', async ({ clusterName, type }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.generateAllConfigs(clusterName, type, baseURL)
    showSuccessBanner(`All configs generated successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Generating configs failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const preserveConfigPath = createAsyncThunk('configs/preserveConfigPath', async ({ clusterName, dbId, preserve }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.preserveConfigPath(clusterName, dbId, preserve, baseURL)
    showSuccessBanner(`All configs generated successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Generating configs failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const getPreservedVarsCnf = createAsyncThunk('configs/getPreservedVarsCnf', async ({ clusterName }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data } = await configService.getPreservedVarsCnf(clusterName, baseURL)
    return data
  } catch (error) {
    return thunkAPI.rejectWithValue(error.response?.data || { message: 'Failed to load preserved variables configuration' })
  }
})

export const savePreservedVarsCnf = createAsyncThunk('configs/savePreservedVarsCnf', async ({ clusterName, content }, thunkAPI) => {
  try {
    const baseURL = thunkAPI.getState()?.auth?.baseURL || ''
    const { data, status } = await configService.savePreservedVarsCnf(clusterName, content, baseURL)
    showSuccessBanner(`Preserved variables configuration saved successfully!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Saving preserved variables configuration failed!`, error, thunkAPI)
    return thunkAPI.rejectWithValue(error.response?.data || { message: 'Failed to save preserved variables configuration' })
  }
})

const initialState = {}

export const configsSlice = createSlice({
  name: 'configs',
  initialState,
  reducers: {}
})

//export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default configsSlice.reducer
