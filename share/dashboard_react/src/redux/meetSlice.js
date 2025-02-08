import { createSlice, createAsyncThunk } from '@reduxjs/toolkit';
import { handleError, showErrorBanner, showSuccessBanner } from '../utility/common';
import { meetService } from '../services/meetService';

export const getMeetInfo = createAsyncThunk('meet/getMeetInfo', async (_, thunkAPI) => {
  try {
    const { data, status } = await meetService.getMeetInfo();
    return { data, status };
  } catch (error) {
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
        ID: response.id,
      },
    };
  } catch (error) {
    handleError(error, thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

//to load messages the first time when selected a channel
export const fetchMessages = createAsyncThunk(
  'meet/fetchMessages',
  async ({ channelId }, thunkAPI) => {
    const messages = await meetService.getMeetMessageFromChannel(channelId, 0);
    return { channelId, messages };
  }
);

//to monitor the appearance of new messages in the selected channel
export const fetchNewMessages = createAsyncThunk(
  'meet/fetchNewMessages',
  async ({ channelId }, thunkAPI) => {
    const messages = await meetService.getMeetMessageFromChannel(channelId, 0);
    return { channelId, messages };
  }
);

//to load history messages when scrolling to the top
export const loadHistoryMessages = createAsyncThunk(
  'meet/loadHistoryMessages',
  async ({ channelId, page }, thunkAPI) => {
    const messages = await meetService.getMeetMessageFromChannel(channelId,page);
    return { channelId, messages };
  }
);

//to set messages view on the selected channel
export const viewMessagesOnChannel = createAsyncThunk(
  'meet/viewMessagesOnChannel',
  async ({ channelId }, thunkAPI) => {
    const response = await meetService.setMessageViewOnChannel(channelId);
    return { response };
  }
);

export const logoutFromMeet = createAsyncThunk(
  'meet/logout',
  async ( thunkAPI) => {
    console.log("logoutFromMeet call");
    const response = await meetService.logoutFromMeet();
    return { response };
  }
);

export const uploadFileOnChannel = createAsyncThunk(
  'meet/uploadFileOnChannel',
  async ({ channelId, formData }, thunkAPI) => {
    const response = await meetService.postFileOnChannel(channelId, formData);
    return { response };
  }
);

export const downloadFileFromChannel = createAsyncThunk(
  'meet/dowloadFileFromChannel',
  async ({ fileId }, thunkAPI) => {
    const response = await meetService.downloadFileFromChannel(fileId);
    return { response };
  }
);

const meetSlice = createSlice({
  name: 'meet',
  initialState: {
    meetInfo: null,
    messages: {},  // Stocke les messages par channelId
    loading: false,
    error: null,
    unreadMessagesByChannel: {},
  },
  reducers: {},
  extraReducers: (builder) => {
    builder
      .addCase(getMeetInfo.fulfilled, (state, action) => {
        state.meetInfo = action.payload.data;
        state.unreadMessagesByChannel = action.payload.data.unread_messages_by_channel || {};
      })
      .addCase(logoutFromMeet.pending, (state) => {
        state.loading = true;
      })
      .addCase(logoutFromMeet.fulfilled, (state, action) => {
        state.loading = false;
        state.meetInfo = null;
        // Handle successful logout if needed
      })
      .addCase(logoutFromMeet.rejected, (state, action) => {
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
      })
      .addCase(postMeetMessage.rejected, (state, action) => {
        state.loading = false;
        state.error = action.error.message;
      })
      .addCase(fetchMessages.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, messages } = action.payload;
        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        } 
        state.messages[channelId] = messages;
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
          !existingMessages.some(existingMsg => existingMsg.ID === msg.ID)
        );
    
        // Mettre à jour l'état des messages avec les nouveaux messages
        state.messages[channelId] = [...newMessages, ...state.messages[channelId]];
        
      })
      .addCase(loadHistoryMessages.fulfilled, (state, action) => {
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