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

export const generateAllConfig = createAsyncThunk('configs/generateConfig', async ({ clusterName, type }, thunkAPI) => {
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

const initialState = {}

export const configsSlice = createSlice({
  name: 'configs',
  initialState,
  reducers: {}
})

//export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default configsSlice.reducer
