// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package meethelper

import (
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/signal18/replication-manager/utils/misc"
)

const meetUrl string = "https://meet.signal18.io"

var meetToken string = ""

// Test Channel Id : ranzunsjkfrftnregi789br13e

//to create a mattermost client
//meetClient := meethelper.CreateMeetClient()

//to read a message
//meetClient.ReadMessage(ChannelID)
//peut être rajouter arg pour modifier le nombre de message à lire ?

//to send a message to mattermost
//meetClient.PostMessage(meetClient.ChannelIdsDirect["1iz5dy9i6j8iuf8b91bacj3zcw__ktzrdgfrmfdxxg7xkiqtgb17fr"], "Test from repman!")

type MeetChatClient struct {
	Client                  *model.Client4
	UserID                  string
	TeamIds                 []string
	ChannelIdsOpen          map[string]string //public channel
	ChannelIdsPrivate       map[string]string //private channel
	ChannelIdsDirect        map[string]string //direct channel (to talk to other users)
	URL                     string
	Token                   string
	AllUser                 map[string]string //to store id and name of all users (for direct chat)
	StatusUsers             map[string]string //to store status of users in direct chat
	UnReadMessagesByChannel map[string]int
}

type MeetChannelMessages struct {
	ChannelId      string
	ChannelType    string // O:Open, P:Private, D:Direct
	Messages       []MeetMessage
	UnReadMessages int
}

type MeetMessage struct {
	UserId    string
	ChannelID string
	Message   string
	ID        string
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
	c.UserID = c.GetMeetUserInfo() //to get user info from mattermost serv
	c.TeamIds = c.GetTeamIDs()
	c.AllUser = c.GetAllUsers()                                                                            //to get teamIDs
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels() //to get the channels for the user
	c.StatusUsers = c.GetStatusUsers()
	//c.SetUserStatusOnline()
	return c
}

// Follow the flow of the browser: log to mm using your gitlab account
func GetMeetToken(gitlabUser string, gitlabPassword string) {
	gitlabHost := "https://gitlab.signal18.io"

	//cookie jar
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// 1. Login gitlab
	// 1.1 get the csrf from login page
	gitlabLoginPageURL := fmt.Sprintf("%s/users/sign_in", gitlabHost)

	body, err := misc.GetRequest(client, gitlabLoginPageURL)
	if err != nil {
		return
	}

	gitCsrfToken, err := misc.ExtractValue(body, "name=\"authenticity_token\" value=\"([^\"]+)\"")
	if err != nil {
		fmt.Println("GetMeetToken: cannot extract CSFR token from gitlab:", err)
		return
	}

	//1.2 Send login credentials
	form := url.Values{
		"user[login]":        {gitlabUser},
		"user[password]":     {gitlabPassword},
		"authenticity_token": {gitCsrfToken},
	}
	_, err = misc.PostRequest(client, gitlabLoginPageURL, form, nil)
	if err != nil {
		fmt.Println("GetMeetToken: cannot post gitlab login credentials:", err)
		return
	}

	// 2. Login meet
	// 2.1 Request login with gitlab account
	meetLoginPageURL := fmt.Sprintf("%s/oauth/gitlab/login?redirect_to=/signal18/channels/test", meetUrl)

	body, err = misc.GetRequest(client, meetLoginPageURL)
	if err != nil {
		fmt.Println("GetMeetToken: cannot get login request from mattermost:", err)
		return
	}

	// 2.2 Extract RedirectUrl and unescape it
	redirectUrl, err := misc.ExtractValue(body, "href=\"([^\"]+)")
	if err != nil {
		fmt.Println("GetMeetToken: cannot extract redirectUrl from mattermost login request", err)
		return
	}

	decodedValue, _ := url.QueryUnescape(redirectUrl)
	meetAuthURL := html.UnescapeString(decodedValue)

	// 2.3 Forward Oauth code to meet
	_, err = misc.GetRequest(client, meetAuthURL)
	if err != nil {
		fmt.Println("GetMeetToken: Oauth code forwarding failed:", err)
		return
	}

	// 2.4 Get MMAUTHTOKEN from cookies
	meetParsedUrl, _ := url.Parse(meetUrl)
	for _, cookie := range jar.Cookies(meetParsedUrl) {
		if cookie.Name == "MMAUTHTOKEN" {
			meetToken = cookie.Value
		}
	}

	if meetToken == "" {
		fmt.Println("GetMeetToken: Failed to retrieve meet token")
	}
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

func (c *MeetChatClient) GetChannels() (map[string]string, map[string]string, map[string]string, map[string]int) {
	channels, resp, err := c.Client.GetChannelsForUserWithLastDeleteAt(c.UserID, 0)
	channelsMapO := make(map[string]string)
	channelsMapP := make(map[string]string)
	channelsMapD := make(map[string]string)
	unReadMessagesByChannel := make(map[string]int)

	if err != nil {
		fmt.Println("GetChannels Error:", err, resp.StatusCode)
		return channelsMapO, channelsMapP, channelsMapD, unReadMessagesByChannel
	}

	for _, channel := range channels {

		if channel.Type == "O" {
			channelsMapO[channel.Name] = channel.Id
		}
		if channel.Type == "P" {
			channelsMapP[channel.Name] = channel.Id
		}
		if channel.Type == "D" {
			directChannelName := strings.Replace(channel.Name, "__", "", 1)
			directChannelName = strings.Replace(directChannelName, c.UserID, "", 1)
			channelsMapD[c.AllUser[directChannelName]] = channel.Id
		}
		unReadMessages := c.GetUnReadMessages(channel.Id)
		unReadMessagesByChannel[channel.Id] = unReadMessages
	}

	return channelsMapO, channelsMapP, channelsMapD, unReadMessagesByChannel
}

func (c *MeetChatClient) ReadMessages(channelID string, page int) (*MeetChannelMessages, error) {
	posts, resp, err := c.Client.GetPostsForChannel(channelID, page, 30, "", true)
	if err != nil {
		fmt.Println("ReadMessages Mattermost Error:", err, resp.StatusCode)
		return nil, err
	}

	messages := make([]MeetMessage, 0)
	for _, post := range posts.ToSlice() {
		messages = append(messages, MeetMessage{
			UserId:    post.UserId,
			ChannelID: post.ChannelId,
			Message:   post.Message,
			ID:        post.Id,
		})
	}

	//A modifier, peut être inverser key et value dans les map des channels ???
	var channelType string
	if containsValue(c.ChannelIdsOpen, channelID) {
		channelType = "O"
	} else if containsValue(c.ChannelIdsPrivate, channelID) {
		channelType = "P"
	} else if containsValue(c.ChannelIdsDirect, channelID) {
		channelType = "D"
	} else {
		channelType = "Unknown"
	}

	channelMessages := &MeetChannelMessages{
		ChannelId:   channelID,
		ChannelType: channelType,
		Messages:    messages,
	}

	return channelMessages, nil
}

func containsValue(m map[string]string, value string) bool {
	for _, v := range m {
		if v == value {
			return true
		}
	}
	return false
}

func (c *MeetChatClient) PostMessage(channelID, message string) (string, error) {

	post := &model.Post{
		ChannelId: channelID,
		Message:   message,
	}

	post_mod, resp, err := c.Client.CreatePost(post)
	if err != nil {
		fmt.Println("PostMessage Mattermost Error:", err, resp.StatusCode)
		return "", err
	}

	//fmt.Println("Message posted successfully on Mattermost", post_mod.Id)

	//if a user post a message, it means he read the channel
	c.ViewMessages(channelID)

	return post_mod.Id, nil
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

func (c *MeetChatClient) GetUnReadMessages(channelID string) int {

	channelUnread, _, err := c.Client.GetChannelUnread(channelID, c.UserID)
	if err != nil {
		fmt.Println("GetUnReadMessages Mattermost Error:", err)
		return 0
	}

	return int(channelUnread.MsgCount)
}

// to update the channels to load unread messages
func (c *MeetChatClient) UpdateChannels() {
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()
}

// to set message as read when a user see it, delete the channel from the map of unread messages
func (c *MeetChatClient) ViewMessages(channelID string) error {
	_, _, err := c.Client.ViewChannel(c.UserID, &model.ChannelView{
		ChannelId:                 channelID,
		CollapsedThreadsSupported: true,
	})

	if err != nil {
		fmt.Println("ViewMessages Mattermost Error:", err)
		return err
	}

	c.UnReadMessagesByChannel[channelID] = 0
	return nil
}

// to create direct channel with a user
func (c *MeetChatClient) CreateDirectChannel(userID string) (string, error) {
	channel, resp, err := c.Client.CreateDirectChannel(c.UserID, userID)
	if err != nil {
		fmt.Println("CreateDirectChannel Mattermost Error:", err, resp.StatusCode)
		return "", err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return channel.Id, nil
}

// to set user as online
func (c *MeetChatClient) SetUserStatusOnline() {
	c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "online"})
}

// to set user as offline
func (c *MeetChatClient) SetUserStatusOffline() {
	c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "offline"})
}

// to set user as away
func (c *MeetChatClient) SetUserStatusAway() {
	c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "away"})
}

// to get status of users from direct channel
func (c *MeetChatClient) GetStatusUsers() map[string]string {
	statusUsers := make(map[string]string)
	for _, userId := range c.ChannelIdsDirect {
		status, _, _ := c.Client.GetUserStatus(userId, "")
		if status != nil {
			statusUsers[userId] = status.Status
		}
	}
	return statusUsers
}
