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
    showErrorBanner("Error while sending message", thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

export const postJitsiMeeting = createAsyncThunk('meet/postJitsiMeeting', async ({ channelId, meetingId }, thunkAPI) => {
  try {
    const response = await meetService.postJitsiMeetingOnChannel(channelId, meetingId);
    return {
      response
    };
  } catch (error) {
    handleError(error, thunkAPI);
    showErrorBanner("Error while sending jitsi meeting", thunkAPI);
    throw error; // Ensure the error is thrown to trigger the rejected state
  }
});

//to load messages the first time when selected a channel
export const fetchMessages = createAsyncThunk(
  'meet/fetchMessages',
  async ({ channelId }, thunkAPI) => {
    try {
      const messages = await meetService.getMeetMessageFromChannel(channelId, 0);
      return { channelId, messages };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error; 
    }
    
  }
);

//to monitor the appearance of new messages in the selected channel
export const fetchNewMessages = createAsyncThunk(
  'meet/fetchNewMessages',
  async ({ channelId }, thunkAPI) => {
    try {
      const messages = await meetService.getMeetMessageFromChannel(channelId, 0);
      return { channelId, messages };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error; 
    }
  }
);

//to load history messages when scrolling to the top
export const loadHistoryMessages = createAsyncThunk(
  'meet/loadHistoryMessages',
  async ({ channelId, page }, thunkAPI) => {
    try {
      const messages = await meetService.getMeetMessageFromChannel(channelId,page);
      return { channelId, messages };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error; 
    }
  }
);

//to set messages view on the selected channel
export const viewMessagesOnChannel = createAsyncThunk(
  'meet/viewMessagesOnChannel',
  async ({ channelId }, thunkAPI) => {
    try {
      const response = await meetService.setMessageViewOnChannel(channelId);
      return { response };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error;
    }
  }
);

export const logoutFromMeet = createAsyncThunk(
  'meet/logout',
  async ( thunkAPI) => {
    try {
      const response = await meetService.logoutFromMeet();
      return { response };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error;
    }
  }
);

export const uploadFileOnChannel = createAsyncThunk(
  'meet/uploadFileOnChannel',
  async ({ channelId, formData }, thunkAPI) => {
    try {
      const response = await meetService.postFileOnChannel(channelId, formData);
      return { response };
    } catch (error) { 
      handleError(error, thunkAPI);
      throw error;
    }
  }
);

export const downloadFileFromChannel = createAsyncThunk(
  'meet/dowloadFileFromChannel',
  async ({ fileId }, thunkAPI) => {
    try {
      const response = await meetService.downloadFileFromChannel(fileId);
      return { response };
    } catch (error) {
      handleError(error, thunkAPI);
      throw error;
    }
  }
);

export const createDirectChannel = createAsyncThunk(
  'meet/createDirectChannel',
  async ({ UserId }, thunkAPI) => {
    try {
      const response = await meetService.createDirectChannel(UserId);
      showSuccessBanner("Direct Channel created");
      return { newChannelId : response.channelId, newChannelName : response.channelName};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while creating channel", error, thunkAPI);
      throw error;
    }
  }
);

export const createPublicChannel = createAsyncThunk(
  'meet/createPublicChannel',
  async ({ ChannelName }, thunkAPI) => {
    try {
      const response = await meetService.createPublicChannel(ChannelName);
      showSuccessBanner("Public Channel created");
      return { newChannelId : response.channelId, newChannelName : response.channelName};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while creating channel", error,  thunkAPI);
      throw error;
    }
  }
);

export const createPrivateChannel = createAsyncThunk(
  'meet/createPrivateChannel',
  async ({ ChannelName }, thunkAPI) => {
    try {
      const response = await meetService.createPrivateChannel(ChannelName);
      showSuccessBanner("Private Channel created", response.status,  thunkAPI);
      return { newChannelId : response.channelId, newChannelName : response.channelName};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while creating channel", error,  thunkAPI);
      throw error;
    }
  }
);

export const deleteChannel = createAsyncThunk(
  'meet/deleteChannel',
  async ({ ChannelId }, thunkAPI) => {
    try {
      const response = await meetService.deleteChannel(ChannelId);
      showSuccessBanner("Channel deleted", response.status, thunkAPI);
      return { deleteChannelId : response.channelId};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while deleting channel", error, thunkAPI);
      throw error;
    }
  }
);

export const leaveChannel = createAsyncThunk(
  'meet/leaveChannel',
  async ({ ChannelId }, thunkAPI) => {
    try {
      const response = await meetService.leaveChannel(ChannelId);
      showSuccessBanner("You leave the channel", response.status, thunkAPI);
      return { deleteChannelId : response.channelId};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while leaving channel", error, thunkAPI);
      throw error;
    }
  }
);

export const addUserChannel = createAsyncThunk(
  'meet/addUserChannel',
  async ({ ChannelId, UserId }, thunkAPI) => {
    try {
      const response = await meetService.addUserChannel(ChannelId, UserId);
      showSuccessBanner("User added to channel", response.status, thunkAPI);
      return { ChannelId : response.channelId};
    } catch (error) {
      handleError(error, thunkAPI);
      showErrorBanner("Error while adding user to channel", error, thunkAPI);
      throw error;
    }
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
    channels: [],
  },
  reducers: {
  },
  extraReducers: (builder) => {
    builder
      .addCase(getMeetInfo.fulfilled, (state, action) => {
        state.meetInfo = action.payload.data;
        localStorage.setItem('userID', state.meetInfo?.user_id);
        state.unreadMessagesByChannel = action.payload.data.unread_messages_by_channel || {};
        state.channels = [
          ...Object.entries(action.payload.data?.channel_ids_open).map(([name, id]) => ({ name, id, type: 'O' })),
          ...Object.entries(action.payload.data?.channel_ids_private).map(([name, id]) => ({ name, id, type: 'P' })),
          ...Object.entries(action.payload.data?.channel_ids_direct).map(([name, id]) => ({ name, id, type: 'D' })),
        ]
      })
      .addCase(logoutFromMeet.pending, (state) => {
        state.loading = true;
      })
      .addCase(logoutFromMeet.fulfilled, (state, action) => {
        state.loading = false;
        state.meetInfo = null;
        state.messages = {};
        state.unreadMessagesByChannel = {};
        state.channels = [];
        localStorage.removeItem('userID');
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
        if (messages.length > 0) {
          state.messages[channelId] = messages;
        }
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
    
        // Mettre à jour l'état des messages avec les nouveaux messages si il existe
        if (newMessages.length > 0) {
          state.unreadMessagesByChannel[channelId] = (state.unreadMessagesByChannel[channelId] || 0) + newMessages.length;
          state.messages[channelId] = [...newMessages, ...state.messages[channelId]];
        }
        
        
      })
      .addCase(loadHistoryMessages.fulfilled, (state, action) => {
        state.loading = false;
        const { channelId, messages } = action.payload;
        if (!state.messages[channelId]) {
          state.messages[channelId] = [];
        }
    
        // Ajouter les messages chargés à l'historique si il existe 
        if (messages.length > 0) {
          state.messages[channelId] = [...state.messages[channelId], ...messages];
        }
      })
      .addCase(createDirectChannel.fulfilled, (state, action) => {
        state.channels.push({ name: action.payload.newChannelName, id: action.payload.newChannelId, type: 'D' });
      })
      .addCase(createPrivateChannel.fulfilled, (state, action) => {
        state.channels.push({ name: action.payload.newChannelName, id: action.payload.newChannelId, type: 'P' });
      })
      .addCase(createPublicChannel.fulfilled, (state, action) => {
        state.channels.push({ name: action.payload.newChannelName, id: action.payload.newChannelId, type: 'O' });
      })
      .addCase(deleteChannel.fulfilled, (state, action) => {
        state.channels = state.channels.filter(channel => channel.id !== action.payload.deleteChannelId);
      })
      .addCase(leaveChannel.fulfilled, (state, action) => {
        state.channels = state.channels.filter(channel => channel.id !== action.payload.deleteChannelId);
      });
  },
});

export const { clearMeetInfo, clearMessages } = meetSlice.actions;

// this is for configureStore
export default meetSlice.reducer;