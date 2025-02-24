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

var meetClient *MeetChatClient = nil

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
	ID            string
	UserId        string
	ChannelID     string
	Message       string
	CreateAt      int64
	FileIds       []string
	FileMetadata  []MeetFile
	AlertMetadata []MeetAlert
}

type MeetFile struct {
	ID        string
	UserId    string
	ChannelID string
	Name      string
	CreateAt  int64
	Extension string
	Size      int
	MimeType  string
	FileLink  string
}

type MeetAlert struct {
	ID       string
	Color    string
	Text     string
	Field    []MeetAlertField
	UserName string
}

type MeetAlertField struct {
	Title string
	Value string
}

// create a client for mattermost and set user info
func GetMeetClient(isLogSupport bool) (*MeetChatClient, error) {
	if meetToken == "" {
		return nil, fmt.Errorf("Meet token is not set")
	}
	//to recreate the client if undefined or if the user session is expired
	if meetClient == nil || meetClient.Client == nil || meetClient.UserID == "" {
		meetClient = CreateMeetClient()
	}
	var err error
	meetClient.UserID, err = meetClient.GetMeetUserInfo() //to get user info from mattermost serv
	if (err != nil || meetClient.UserID == "") && isLogSupport {
		fmt.Println("GetMeetClient Error:", err)
		return nil, err
	}
	meetClient.TeamIds = meetClient.GetTeamIDs()
	meetClient.AllUser = meetClient.GetAllUsers()                                                                                                       //to get teamIDs
	meetClient.ChannelIdsOpen, meetClient.ChannelIdsPrivate, meetClient.ChannelIdsDirect, meetClient.UnReadMessagesByChannel = meetClient.GetChannels() //to get the channels for the user
	meetClient.StatusUsers = meetClient.GetStatusUsers()
	meetClient.SetUserStatusOnline()
	return meetClient, err
}

func CreateMeetClient() *MeetChatClient {
	//to recreate the client if undefined or if the user session is expired
	if meetToken != "" {
		client := model.NewAPIv4Client(meetUrl)
		client.SetOAuthToken(meetToken)
		meetClient = &MeetChatClient{
			Client: client,
			URL:    meetUrl,
			Token:  meetToken,
		}
		return meetClient
	}
	return nil
}

// Follow the flow of the browser: log to mm using your gitlab account
func GetMeetToken(gitlabUser string, gitlabPassword string, isLogSupport bool) error {
	gitlabHost := "https://gitlab.signal18.io"

	//cookie jar
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// 1. Login gitlab
	// 1.1 get the csrf from login page
	gitlabLoginPageURL := fmt.Sprintf("%s/users/sign_in", gitlabHost)

	body, err := misc.GetRequest(client, gitlabLoginPageURL)
	if err != nil {
		return err
	}

	gitCsrfToken, err := misc.ExtractValue(body, "name=\"authenticity_token\" value=\"([^\"]+)\"")
	if err != nil && isLogSupport {
		fmt.Println("GetMeetToken: cannot extract CSFR token from gitlab:", err)
		return err
	}

	//1.2 Send login credentials
	form := url.Values{
		"user[login]":        {gitlabUser},
		"user[password]":     {gitlabPassword},
		"authenticity_token": {gitCsrfToken},
	}
	_, err = misc.PostRequest(client, gitlabLoginPageURL, form, nil)
	if err != nil && isLogSupport {
		fmt.Println("GetMeetToken: cannot post gitlab login credentials:", err)
		return err
	}

	// 2. Login meet
	// 2.1 Request login with gitlab account
	meetLoginPageURL := fmt.Sprintf("%s/oauth/gitlab/login?redirect_to=/signal18/channels/test", meetUrl)

	body, err = misc.GetRequest(client, meetLoginPageURL)
	if err != nil && isLogSupport {
		fmt.Println("GetMeetToken: cannot get login request from mattermost:", err)
		return err
	}

	// 2.2 Extract RedirectUrl and unescape it
	redirectUrl, err := misc.ExtractValue(body, "href=\"([^\"]+)")
	if err != nil && isLogSupport {
		fmt.Println("GetMeetToken: cannot extract redirectUrl from mattermost login request", err)
		return err
	}

	decodedValue, _ := url.QueryUnescape(redirectUrl)
	meetAuthURL := html.UnescapeString(decodedValue)

	// 2.3 Forward Oauth code to meet
	_, err = misc.GetRequest(client, meetAuthURL)
	if err != nil && isLogSupport {
		fmt.Println("GetMeetToken: Oauth code forwarding failed:", err)
		return err
	}

	// 2.4 Get MMAUTHTOKEN from cookies
	meetParsedUrl, _ := url.Parse(meetUrl)
	for _, cookie := range jar.Cookies(meetParsedUrl) {
		if cookie.Name == "MMAUTHTOKEN" {
			meetToken = cookie.Value
		}
	}

	if meetToken == "" && isLogSupport {
		fmt.Println("GetMeetToken: Failed to retrieve meet token")
		return fmt.Errorf("Failed to retrieve meet token")
	}
	return nil
}

func (c *MeetChatClient) GetMeetUserInfo() (string, error) {
	user, resp, err := c.Client.GetMe("")
	if err != nil {
		fmt.Println("Meet Error:", err, resp.StatusCode, c.Client, c.UserID)
		return "", err
	}

	return user.Id, err
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
		if channel.Type == "D" && channel.DeleteAt == 0 {
			directChannelId := strings.Replace(channel.Name, "__", "", 1)
			directChannelId = strings.Replace(directChannelId, c.UserID, "", 1)
			channelName := c.AllUser[directChannelId]
			if channelName != "" {
				channelsMapD[channelName] = channel.Id
			}
		}
		unReadMessages := c.GetUnReadMessages(channel.Id)
		unReadMessagesByChannel[channel.Id] = unReadMessages
	}

	return channelsMapO, channelsMapP, channelsMapD, unReadMessagesByChannel
}

// to set file and meta data
func (c *MeetChatClient) GetFilesMessagesMetadata(metadata []MeetFile, fileId string) []MeetFile {
	fileInfo, _, err := c.Client.GetFileInfo(fileId)

	if err == nil {
		fileInfo := MeetFile{
			ID:        fileInfo.Id,
			UserId:    fileInfo.CreatorId,
			ChannelID: fileInfo.ChannelId,
			Name:      fileInfo.Name,
			CreateAt:  fileInfo.CreateAt,
			Extension: fileInfo.Extension,
			MimeType:  fileInfo.MimeType,
			Size:      int(fileInfo.Size),
		}
		fileLink, _, err := c.Client.GetFileLink(fileId)
		if err == nil {
			fileInfo.FileLink = fileLink
		}
		metadata = append(metadata, fileInfo)
	}
	return metadata
}

func (c *MeetChatClient) ReadMessages(channelID string, page int) (*MeetChannelMessages, error) {
	posts, resp, err := c.Client.GetPostsForChannel(channelID, page, 30, "", true)
	if err != nil {
		fmt.Println("ReadMessages Mattermost Error:", err, resp.StatusCode)
		return nil, err
	}

	messages := make([]MeetMessage, 0)
	for _, post := range posts.ToSlice() {
		message := MeetMessage{
			UserId:    post.UserId,
			ChannelID: post.ChannelId,
			Message:   post.Message,
			ID:        post.Id,
			CreateAt:  post.CreateAt,
		}
		if len(post.FileIds) > 0 {
			for _, fileId := range post.FileIds {
				message.FileMetadata = c.GetFilesMessagesMetadata(message.FileMetadata, fileId)
			}
		}
		if post.Type == "slack_attachment" {
			attachments := post.Attachments()
			props := post.GetProps()

			for _, attachment := range attachments {
				alert := MeetAlert{
					ID:       post.Id,
					Color:    attachment.Color,
					Text:     attachment.Fallback,
					UserName: props["override_username"].(string),
				}
				fields := attachment.Fields
				for _, field := range fields {
					alertField := MeetAlertField{
						Title: field.Title,
						Value: field.Value.(string),
					}
					alert.Field = append(alert.Field, alertField)
				}
				message.AlertMetadata = append(message.AlertMetadata, alert)
			}

		}
		messages = append(messages, message)

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

func getValue(m map[string]string, value string) string {
	for k, v := range m {
		if v == value {
			return k
		}
	}
	return ""
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
	users, resp, err := c.Client.GetUsersInTeam(c.TeamIds[0], 0, 100, "")

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
func (c *MeetChatClient) CreateDirectChannel(userID string) (string, string, error) {
	channel, resp, err := c.Client.CreateDirectChannel(c.UserID, userID)
	if err != nil {
		fmt.Println("CreateDirectChannel Mattermost Error:", err, resp.StatusCode)
		return "", "", err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return channel.Id, getValue(c.ChannelIdsDirect, channel.Id), nil
}

func (c *MeetChatClient) CreateOpenChannel(channelName string) (string, string, error) {
	channel, resp, err := c.Client.CreateChannel(&model.Channel{
		CreatorId:   c.UserID,
		Name:        channelName,
		DisplayName: channelName,
		Type:        model.ChannelTypeOpen,
		TeamId:      c.TeamIds[0],
	})

	if err != nil {
		fmt.Println("CreateOpenChannel Mattermost Error:", err, resp.StatusCode)
		return "", "", err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return channel.Id, getValue(c.ChannelIdsOpen, channel.Id), nil
}

func (c *MeetChatClient) CreatePrivateChannel(channelName string) (string, string, error) {
	channel, resp, err := c.Client.CreateChannel(&model.Channel{
		CreatorId:   c.UserID,
		Name:        channelName,
		DisplayName: channelName,
		Type:        model.ChannelTypePrivate,
		TeamId:      c.TeamIds[0],
	})

	if err != nil {
		fmt.Println("CreatePrivateChannel Mattermost Error:", err, resp.StatusCode)
		return "", "", err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return channel.Id, getValue(c.ChannelIdsPrivate, channel.Id), nil
}

func (c *MeetChatClient) DeleteChannel(channelID string) error {
	resp, err := c.Client.DeleteChannel(channelID)
	if err != nil {
		fmt.Println("DeleteChannel Mattermost Error:", err, resp.StatusCode)
		return err
	}

	fmt.Println("DeleteChannel Mattermost :", resp.StatusCode)

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return nil
}

func (c *MeetChatClient) LeaveChannel(channelID string) error {
	resp, err := c.Client.RemoveUserFromChannel(channelID, c.UserID)
	if err != nil {
		fmt.Println("LeaveChannel Mattermost Error:", err, resp.StatusCode)
		return err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return nil
}

func (c *MeetChatClient) AddUserChannel(channelID string, userID string) error {
	_, resp, err := c.Client.AddChannelMember(channelID, userID)
	if err != nil {
		fmt.Println("LeaveChannel Mattermost Error:", err, resp.StatusCode)
		return err
	}

	//update the direct channels
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.UnReadMessagesByChannel = c.GetChannels()

	return nil
}

// to set user as online
func (c *MeetChatClient) SetUserStatusOnline() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "online"})
	if err != nil {
		fmt.Println("SetUserStatusOnline Mattermost Error:", err)
	}
	return err
}

// to set user as offline
func (c *MeetChatClient) SetUserStatusOffline() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "offline"})
	if err != nil {
		fmt.Println("SetUserStatusOffLine Mattermost Error:", err)
	}
	return err
}

// to set user as away
func (c *MeetChatClient) SetUserStatusAway() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "away"})
	if err != nil {
		fmt.Println("SetUserStatusAway Mattermost Error:", err)
	}
	return err
}

// to get status of users from direct channel
func (c *MeetChatClient) GetStatusUsers() map[string]string {
	statusUsers := make(map[string]string)
	for userName, _ := range c.ChannelIdsDirect {
		userId := getValue(c.AllUser, userName)
		status, _, _ := c.Client.GetUserStatus(userId, "")
		if status != nil {
			statusUsers[userName] = status.Status
		}
	}
	return statusUsers
}

func ClearMeetClient() {
	meetClient = nil
	meetToken = ""
	return
}

// to upload a file on a channel
func (c *MeetChatClient) UploadFileOnChannel(channelID string, fileBytes []byte, fileName string, message string) error {
	fileRes, _, err := c.Client.UploadFile(fileBytes, channelID, fileName)

	if fileRes == nil || err != nil {
		fmt.Println("UploadFile Mattermost Error:", err)
		return err
	}

	// Create a post with the file attached
	post := &model.Post{
		ChannelId: channelID,
		Message:   message,
		FileIds:   []string{fileRes.FileInfos[0].Id}, // Attach the uploaded file to the post
	}

	// Post the message with the file attachment
	if _, _, err := c.Client.CreatePost(post); err != nil {
		fmt.Println("CreatePost Mattermost Error:", err)
		return err
	}

	return nil
}

// to download a file with the fileId
func (c *MeetChatClient) DownloadFileFromChannel(fileId string) ([]byte, string, error) {
	fileBytes, _, err := c.Client.GetFile(fileId)

	if err != nil {
		fmt.Println("Download file Mattermost Error:", err)
		return nil, "", err
	}

	fileInfo, _, err := c.Client.GetFileInfo(fileId)

	if err != nil {
		fmt.Println("Download file Mattermost Error:", err)
		return nil, "", err
	}

	return fileBytes, fileInfo.Name, nil
}
