import { createSlice, createAsyncThunk, isAnyOf } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { settingsService } from '../services/settingsService'

export const switchSetting = createAsyncThunk('settings/switchSetting', async ({ clusterName, setting }, thunkAPI) => {
  try {
    const { data, status } = await settingsService.switchSettings(clusterName, setting)
    showSuccessBanner(`Switching ${setting} successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Switching ${setting} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const changeTopology = createAsyncThunk(
  'settings/changeTopology',
  async ({ clusterName, topology }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.changeTopology(clusterName, topology)
      showSuccessBanner(`Topology changed to ${topology} successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Changing topology to ${setting} failed!`, error, thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

export const setSetting = createAsyncThunk('settings/setSetting', async ({ clusterName, setting, value }, thunkAPI) => {
  try {
    const { data, status } = await settingsService.setSetting(clusterName, setting, value)
    showSuccessBanner(`${setting} changed successfully!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner(`Changing ${setting} failed!`, error.toString(), thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const updateGraphiteWhiteList = createAsyncThunk(
  'settings/updateGraphiteWhiteList',
  async ({ clusterName, whiteListValue }, thunkAPI) => {
    try {
      const { data, status } = await settingsService.updateGraphiteWhiteList(clusterName, whiteListValue)
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
      const { data, status } = await settingsService.updateGraphiteBlackList(clusterName, blackListValue)
      showSuccessBanner(`Graphite BlackList Regexp updated successfully!`, status, thunkAPI)
      return { data, status }
    } catch (error) {
      showErrorBanner(`Updating Graphite BlackList Regexp failed!`, error.toString(), thunkAPI)
      handleError(error, thunkAPI)
    }
  }
)

const initialState = {}

export const settingsSlice = createSlice({
  name: 'settings',
  initialState,
  reducers: {
    clearSettings: (state, action) => {
      Object.assign(state, initialState)
    }
  }
})

export const { clearSettings } = settingsSlice.actions

// this is for configureStore
export default settingsSlice.reducer
