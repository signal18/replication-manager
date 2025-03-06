import { meetApi } from './apiHelper';

export const meetService = {
    getMeetInfo,
    postMeetMessageOnChannel,
    postJitsiMeetingOnChannel,
    getMeetMessageFromChannel,
    setMessageViewOnChannel,
    logoutFromMeet,
    postFileOnChannel,
    downloadFileFromChannel, 
    createDirectChannel,
    createPrivateChannel,
    createPublicChannel,
    deleteChannel,
    leaveChannel,
    addUserChannel,
    joinChannel
}


async function getMeetInfo() {
    try {
        const response = await meetApi.get('info');
        if (response.status !== 200) {
            throw new Error(`Error fetching meet info: ${response.status} ${response.data}`);
        }
        return response;
    } catch (error) {
        console.error('Error fetching meet info:', error);
        throw error;
    }
}

async function postMeetMessageOnChannel(channelId, message) {
    try {
        const response = await meetApi.post(`post/${channelId}`, { message });
        if (!response.data || !response.data.message || !response.data.user || !response.data.channel) {
            throw new Error('Invalid response from API');
        }
        return response.data;
    } catch (error) {
        console.error('Error posting meet message:', error);
        throw error;
    }
}

async function postJitsiMeetingOnChannel(channelId, meetingId) {
    try {
        const response = await meetApi.post(`post/jitsi/${channelId}/${meetingId}`);
        if (!response.data || !response.data.user || !response.data.channel) {
            throw new Error('Invalid response from API');
        }
        return response.data;
    } catch (error) {
        console.error('Error posting meet message:', error);
        throw error;
    }
}

async function getMeetMessageFromChannel(channelId, page = 0) {
    try {
        const response = await meetApi.get(`read/${channelId}/${page}`);
        console.log('Messages from meetService:', response.data.Messages, page);
        return response.data.Messages;
    } catch (error) {
        console.error('Error fetching meet messages:', error);
        throw error;
    }
}

async function setMessageViewOnChannel(channelId) {
    try {
        const response = await meetApi.get(`view/${channelId}`);
        return response;
    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function logoutFromMeet() {
    try {
        const response = await meetApi.get(`logout`);
        return response;
    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function postFileOnChannel(channelId, formData) {
    try {
        const response = await meetApi.post(`upload/${channelId}`, formData);
        if (response.status === 200) {
            console.log('File uploaded successfully');
        } else {
            console.error('Error uploading file');
        }
        return response;
    } catch (error) {
        console.error('Error uploading file:', error);
        throw error;
    }
}

async function downloadFileFromChannel(fileId) {
    try {
        const response = await meetApi.get(`download/${fileId}`);
        return response;
    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function createDirectChannel(UserId) {
    try {
        const response = await meetApi.get(`create/direct/${UserId}`);
        if (!response.data || !response.data.channelId || !response.data.channelName) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function createPrivateChannel(ChannelName) {
    try {
        const response = await meetApi.get(`create/private/${ChannelName}`);
        if (!response.data || !response.data.channelId || !response.data.channelName) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function createPublicChannel(ChannelName) {
    try {
        const response = await meetApi.get(`create/public/${ChannelName}`);
        if (!response.data || !response.data.channelId || !response.data.channelName) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function deleteChannel(ChannelId) {
    try {
        const response = await meetApi.get(`delete/${ChannelId}`);
        if (!response.data || !response.data.channelId) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function leaveChannel(ChannelId) {
    try {
        const response = await meetApi.get(`leave/${ChannelId}`);
        if (!response.data || !response.data.channelId) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function addUserChannel(ChannelId, UserId) {
    try {
        const response = await meetApi.get(`add/${ChannelId}/${UserId}`);
        if (!response.data || !response.data.channelId) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}

async function joinChannel(ChannelId) {
    try {
        const response = await meetApi.get(`join/${ChannelId}`);
        if (!response.data || !response.data.channelId) {
            throw new Error('Invalid response from API');
        }
        return response.data;

    } catch (error) {
        console.error('Error view meet messages:', error);
        throw error;
    }
}
