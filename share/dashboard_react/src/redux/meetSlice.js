import { createSlice, createAsyncThunk } from '@reduxjs/toolkit'
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common'
import { meetService } from '../services/meetService'

export const getMeetInfo = createAsyncThunk('meet/getMeetInfo', async (_, thunkAPI) => {
  try {
    const { data, status } = await meetService.getMeetInfo()
    showSuccessBanner('Meet info retrieved successfully!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failed to retrieve meet info!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const readMeetMessages = createAsyncThunk('meet/readMeetMessages', async ({ channelId }, thunkAPI) => {
  try {
    const { data, status } = await meetService.getMeetMessageFromChannel(channelId)
    showSuccessBanner('Messages retrieved successfully!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failed to retrieve messages!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

export const postMeetMessage = createAsyncThunk('meet/postMeetMessage', async ({ channelId, message }, thunkAPI) => {
  try {
    const { data, status } = await meetService.postMeetMessageOnChannel(channelId, message)
    showSuccessBanner('Message posted successfully!', status, thunkAPI)
    return { data, status }
  } catch (error) {
    showErrorBanner('Failed to post message!', error, thunkAPI)
    handleError(error, thunkAPI)
  }
})

const initialState = {
  loading: false,
  error: null,
  meetInfo: null,
  messages: null
}

export const meetSlice = createSlice({
  name: 'meet',
  initialState,
  reducers: {
    clearMeetInfo: (state) => {
      state.meetInfo = null
    },
    clearMessages: (state) => {
      state.messages = null
    }
  },
  extraReducers: (builder) => {
    builder
      .addCase(getMeetInfo.pending, (state) => {
        state.loading = true
      })
      .addCase(getMeetInfo.fulfilled, (state, action) => {
        state.loading = false
        state.meetInfo = action.payload.data
      })
      .addCase(getMeetInfo.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(readMeetMessages.pending, (state) => {
        state.loading = true
      })
      .addCase(readMeetMessages.fulfilled, (state, action) => {
        state.loading = false
        state.messages = action.payload.data
      })
      .addCase(readMeetMessages.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
      .addCase(postMeetMessage.pending, (state) => {
        state.loading = true
      })
      .addCase(postMeetMessage.fulfilled, (state, action) => {
        state.loading = false
      })
      .addCase(postMeetMessage.rejected, (state, action) => {
        state.loading = false
        state.error = action.error
      })
  }
})

export const { clearMeetInfo, clearMessages } = meetSlice.actions

// this is for configureStore
export default meetSlice.reducer