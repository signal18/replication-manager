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
    const response = await meetService.postMeetMessageOnChannel(channelId, message);
    return {
      channelId: response.channel,
      message: {
        UserId: response.user,
        ChannelID: response.channel,
        Message: response.message,
      },
    };
  } catch (error) {
    handleError(error, thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

export const fetchNewMessages = createAsyncThunk(
  'meet/fetchNewMessages',
  async ({ channelId }, thunkAPI) => {
    const messages = await meetService.getMeetMessageFromChannel(channelId, 0);
    return { channelId, messages };
  }
);

export const loadMoreMessages = createAsyncThunk(
  'meet/loadMoreMessages',
  async ({ channelId, page }, thunkAPI) => {
    const messages = await meetService.getMeetMessageFromChannel(channelId,page);
    return { channelId, messages };
  }
);

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

        const existingMessages = state.messages[channelId];
        const newMessages = messages.filter(msg =>
          !existingMessages.some(existingMsg => existingMsg.MessageId === msg.MessageId)
        );

        if (page === 0) {
          // Premier chargement, remplacer les messages existants
          //state.messages[channelId] = messages;

          // Mettre à jour l'état des messages avec les nouveaux messages
          state.messages[channelId] = [...newMessages, ...existingMessages];
          //state.unreadMessagesByChannel[channelId] = unreadCount;
        } else {
          // Ajouter les nouveaux messages en haut lors du scroll
          //state.messages[channelId] = [ ...existingMessages, ...newMessages];
          state.messages[channelId] = [ ...state.messages[channelId], ...newMessages];
        }
      })
      .addCase(readMeetMessages.rejected, (state, action) => {
        state.loading = false;
        state.error = action.error.message;
      })
      .addCase(postMeetMessage.pending, (state) => {
        state.loading = true;
      })
      .addCase(postMeetMessage.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, message } = action.payload;
        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        }
        state.messages[channelId] = [message, ...state.messages[channelId]];
      })
      .addCase(postMeetMessage.rejected, (state, action) => {
        state.loading = false;
        state.error = action.error.message;
      })
      .addCase(fetchNewMessages.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, messages } = action.payload;
        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        }
    
        // Comparer les messages existants avec les nouveaux messages
        const existingMessages = state.messages[channelId];
        const newMessages = messages.filter(msg =>
          !existingMessages.some(existingMsg => existingMsg.MessageId === msg.MessageId)
        );
    
        // Mettre à jour l'état des messages avec les nouveaux messages
        state.messages[channelId] = [...newMessages, ...state.messages[channelId]];
        //state.unreadMessagesByChannel[channelId] = unreadCount;
      })
      .addCase(loadMoreMessages.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, messages } = action.payload;
        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        }
    
        // Ajouter les messages chargés à l'historique
        state.messages[channelId] = [ ...state.messages[channelId], ...messages];
      });
  },
});

export const { clearMeetInfo, clearMessages } = meetSlice.actions;

// this is for configureStore
export default meetSlice.reducer;