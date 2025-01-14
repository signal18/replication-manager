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

async function getMeetMessageFromChannel(channelId) {
    try {
        const response = await meetApi.get(`read/${channelId}`);
        return response.data;
    } catch (error) {
        console.error('Error fetching messages:', error);
        throw error;
    }
}
