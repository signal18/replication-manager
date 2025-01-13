import { meetApi } from './apiHelper';
//import { getMeetApi, meetApi } from './apiHelper';

/*const BASE_URL = 'https://meet.signal18.io/api/v4/'; // Our server url*/
//on possède le MMAUTH qui correpond au cookie de session mattermost obtenue lors de l'auth a gitlab, stock sous le nom 'meet_token'


export const meetService = {
    getMeetInfo,
    postMeetMessageOnChannel,
    getMeetMessageFromChannel,
}
/*
function loginMeet() {
  return getMeetApi.post('users/login', null, 1, true)
}*/

// To get user info (channelID, all users ID, etc)
function getMeetInfo(){
    return meetApi().get('info');
};


//peut être mettre une valeur par défaut à page et per_page ?? page = 0 correspond à la dernière page 
function getMeetMessageFromChannel(channelId){
    return meetApi().get('read/' + channelId);
};

function postMeetMessageOnChannel(channelId, message){
    return meetApi().post('post/' + channelId, { message });
};

