import { meetApi } from './apiHelper';

export const meetService = {
    getMeetInfo,
    postMeetMessageOnChannel,
    getMeetMessageFromChannel,
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
        return response;
    } catch (error) {
        console.error('Error posting message:', error);
        throw error;
    }
}

async function getMeetMessageFromChannel(channelId, page = 0) {
    try {
        const response = await meetApi.get(`read/${channelId}/${page}`);
        console.log('Messages from meetService:', response.data.Messages, page);
        return response.data.Messages;
    } catch (error) {
        console.error('Error fetching messages:', error);
        throw error;
    }
}
