import { meetApi } from './apiHelper';

export const meetService = {
    getMeetInfo,
    postMeetMessageOnChannel,
    getMeetMessageFromChannel,
    setMessageViewOnChannel
}


async function getMeetInfo() {
    try {
        const response = await meetApi.get('info');
        return response;
    } catch (error) {
        console.error('Error fetching meet info:', error);
        throw error;
    }
}

async function postMeetMessageOnChannel(channelId, message) {
    try {
        const response = await meetApi.post(`post/${channelId}`, { message });
        // Vérifier si la réponse est valide
        if (!response.data || !response.data.message || !response.data.user || !response.data.channel) {
            throw new Error('Invalid response from API');
        }

        console.log('Response from API:', response.data);
        return response.data;
        //return response;
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