import { createSlice, createAsyncThunk } from '@reduxjs/toolkit';
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common';
import { meetService } from '../services/meetService';

export const getMeetInfo = createAsyncThunk('meet/getMeetInfo', async (_, thunkAPI) => {
  try {
    const { data, status } = await meetService.getMeetInfo();
    return { data, status };
  } catch (error) {
    showErrorBanner('Failed to retrieve meet info!', error, thunkAPI);
    handleError(error, thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

export const readMeetMessages = createAsyncThunk('meet/readMeetMessages', async ({ channelId, page = 0 }, thunkAPI) => {
  try {
    const messages = await meetService.getMeetMessageFromChannel(channelId,page);
    return { channelId, messages };
  } catch (error) {
    showErrorBanner('Failed to retrieve messages!', error, thunkAPI);
    handleError(error, thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

export const postMeetMessage = createAsyncThunk('meet/postMeetMessage', async ({ channelId, message }, thunkAPI) => {
  try {
    const { data, status } = await meetService.postMeetMessageOnChannel(channelId, message);
    return { data, status };
  } catch (error) {
    handleError(error, thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

const initialState = {
  loading: false,
  error: null,
  meetInfo: null,
  messages: {},
};

const meetSlice = createSlice({
  name: 'meet',
  initialState: {
    meetInfo: null,
    messages: {},  // Stocke les messages par channelId
    loading: false,
    error: null,
  },
  reducers: {},
  extraReducers: (builder) => {
    builder
      .addCase(getMeetInfo.fulfilled, (state, action) => {
        state.meetInfo = action.payload.data;
      })
      .addCase(readMeetMessages.pending, (state) => {
        state.loading = true;
      })
      .addCase(readMeetMessages.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, messages, page } = action.payload;

        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        }

        if (page === 0) {
          // Premier chargement, remplacer les messages existants
          state.messages[channelId] = messages;
        } else {
          // Ajouter les nouveaux messages en haut lors du scroll
          state.messages[channelId] = [ ...state.messages[channelId], ...messages];
        }
      })
      .addCase(readMeetMessages.rejected, (state, action) => {
        state.loading = false;
        state.error = action.error.message;
      });
  },
});

export const { clearMeetInfo, clearMessages } = meetSlice.actions;

// this is for configureStore
export default meetSlice.reducer;