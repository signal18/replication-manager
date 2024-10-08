import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { meetService } from '../services/meetService'

export const getMeet = createAsyncThunk('meet/getMeet', async ({}, thunkAPI) => {
  try {
    const { data, status } = await meetService.getMeet()
    //  showSuccessBanner(`Db tag ${tag} added successful!`, status, thunkAPI)
    return { data, status }
  } catch (error) {
    // showErrorBanner(`Adding db tag ${tag} failed!`, error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

const initialState = {}

export const meetSlice = createSlice({
  name: 'meet',
  initialState,
  reducers: {}
})

export default meetSlice.reducer
