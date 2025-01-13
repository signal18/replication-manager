// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package meethelper

import (
	"fmt"

	"github.com/mattermost/mattermost-server/v6/model"
)

const meetUrl string = "https://meet.signal18.io"
const meetToken string = "***"

// Test Channel Id : ranzunsjkfrftnregi789br13e

//to create a mattermost client
//meetClient := meethelper.CreateMeetClient()

//to read a message
//meetClient.ReadMessage(ChannelID)
//peut être rajouter arg pour modifier le nombre de message à lire ?

//to send a message to mattermost
//meetClient.PostMessage(meetClient.ChannelIdsDirect["1iz5dy9i6j8iuf8b91bacj3zcw__ktzrdgfrmfdxxg7xkiqtgb17fr"], "Test from repman!")

type MeetChatClient struct {
	Client            *model.Client4
	UserID            string
	TeamIds           []string
	ChannelIdsOpen    map[string]string //public channel
	ChannelIdsPrivate map[string]string //private channel
	ChannelIdsDirect  map[string]string //direct channel (to talk to other users)
	URL               string
	Token             string
	AllUser           map[string]string //to store id and name of all users (for direct chat)
}

// create a client for mattermost and set user info
func CreateMeetClient() *MeetChatClient {
	client := model.NewAPIv4Client(meetUrl)
	client.SetOAuthToken(meetToken)
	c := &MeetChatClient{
		Client: client,
		URL:    meetUrl,
		Token:  meetToken,
	}
	c.UserID = c.GetMeetUserInfo()                                              //to get user info from mattermost serv
	c.TeamIds = c.GetTeamIDs()                                                  //to get teamIDs
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect = c.GetChannels() //to get the channels for the user
	c.AllUser = c.GetAllUsers()
	return c
}

func (c *MeetChatClient) GetMeetUserInfo() string {
	user, resp, err := c.Client.GetMe("")
	if err != nil {
		fmt.Println("Meet Error:", err, resp.StatusCode)
		return ""
	}

	return user.Id
}

func (c *MeetChatClient) GetTeamIDs() []string {
	teams, resp, err := c.Client.GetTeamsForUser(c.UserID, "")
	teamIDs := make([]string, len(teams))
	if err != nil {
		fmt.Println("GetTeamsForUser Error:", err, resp.StatusCode)
		return teamIDs
	}

	for i, team := range teams {
		teamIDs[i] = team.Id
	}

	return teamIDs
}

func (c *MeetChatClient) GetChannels() (map[string]string, map[string]string, map[string]string) {
	channels, resp, err := c.Client.GetChannelsForTeamForUser(c.TeamIds[0], c.UserID, false, "")

	channelsMapO := make(map[string]string)
	channelsMapP := make(map[string]string)
	channelsMapD := make(map[string]string)

	if err != nil {
		fmt.Println("GetChannels Error:", err, resp.StatusCode)
		return channelsMapO, channelsMapP, channelsMapD
	}

	for _, channel := range channels {
		//fmt.Println("Channel:", channel)
		if channel.Type == "O" {
			channelsMapO[channel.Name] = channel.Id
		}
		if channel.Type == "P" {
			channelsMapP[channel.Name] = channel.Id
		}
		if channel.Type == "D" {
			channelsMapD[channel.Name] = channel.Id
		}
	}

	return channelsMapO, channelsMapP, channelsMapD
}

func (c *MeetChatClient) ReadMessages(channelID string) ([]*model.Post, error) {

	posts, resp, err := c.Client.GetPostsForChannel(channelID, 0, 50, "", true)
	if err != nil {
		fmt.Println("ReadMessages Mattermost Error:", err, resp.StatusCode)
		return nil, err
	}

	fmt.Println("Message:", posts)

	for _, post := range posts.ToSlice() {
		fmt.Println("Message:", post.Message)
		fmt.Println("Message post by:", c.AllUser[post.UserId])
	}
	return posts.ToSlice(), nil
}

func (c *MeetChatClient) PostMessage(channelID, message string) error {

	post := &model.Post{
		ChannelId: channelID,
		Message:   message,
	}

	_, resp, err := c.Client.CreatePost(post)
	if err != nil {
		fmt.Println("PostMessage Mattermost Error:", err, resp.StatusCode)
		return err
	}

	fmt.Println("Message posted successfully on Mattermost")
	return nil
}

func (c *MeetChatClient) GetAllUsers() map[string]string {
	users, resp, err := c.Client.GetUsers(0, 100, "")

	usersMap := make(map[string]string)

	if err != nil {
		fmt.Println("GetAllUsers Error: ", err, ", StatusCode: ", resp.StatusCode)
		return usersMap
	}

	for _, user := range users {
		usersMap[user.Id] = user.Username
	}

	return usersMap
}
