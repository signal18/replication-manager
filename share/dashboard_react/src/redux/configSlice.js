import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { configService } from '../services/configService'

export const addDBTag = createAsyncThunk('configs/addDBTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const { data, status } = await configService.addDBTag(clusterName, tag)
    showSuccessBanner(`Db tag ${tag} added successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Adding db tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})
export const dropDBTag = createAsyncThunk('configs/dropDBTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const { data, status } = await configService.dropDBTag(clusterName, tag)
    showSuccessBanner(`Db tag ${tag} dropped successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Dropping db tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const addProxyTag = createAsyncThunk('configs/addProxyTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const { data, status } = await configService.addProxyTag(clusterName, tag)
    showSuccessBanner(`Proxy tag ${tag} added successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Adding proxy tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const dropProxyTag = createAsyncThunk('configs/dropProxyTag', async ({ clusterName, tag }, thunkAPI) => {
  try {
    const { data, status } = await configService.dropProxyTag(clusterName, tag)
    showSuccessBanner(`Proxy tag ${tag} dropped successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Dropping proxy tag ${tag} failed!`, error, thunkAPI)
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
