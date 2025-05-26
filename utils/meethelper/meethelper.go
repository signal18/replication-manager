// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package meethelper

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/signal18/replication-manager/utils/misc"
)

const meetUrl string = "https://meet.signal18.io"

var meetToken string = ""

var meetClients []*MeetChatClient

var meetUsers []MeetUser

type MeetUser struct {
	UserName string
	Password string
	UserID   string
	Token    string
}

type MeetChatClient struct {
	Client                  *model.Client4
	UserID                  string
	TeamIds                 []string
	ChannelIdsOpen          map[string]string //public channel
	ChannelIdsPrivate       map[string]string //private channel
	ChannelIdsDirect        map[string]string //direct channel (to talk to other users)
	ChannelIdsOpenJoin      map[string]string //public channel to join
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
	ID               string
	UserId           string
	OverriveUserName string
	ChannelID        string
	Message          string
	CreateAt         int64
	FileIds          []string
	FileMetadata     []MeetFile
	AlertMetadata    []MeetAlert
	MeetingMetadata  []MeetingData
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
}

type MeetAlert struct {
	ID       string
	Color    string
	Text     string
	Fields   []MeetAlertField
	UserName string
}

type MeetingData struct {
	ID       string
	MeetLink string
	MeetName string
	Text     string
}

type MeetAlertField struct {
	Title string
	Value string
}

func CreateMeetUserClient(userName string, password string, isLogSupport bool) (string, error) {
	if IsMeetUserExist(userName) {
		if IsMeetClientExist(GetUserID(userName)) {
			return GetUserID(userName), nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		meetTok, err := GetMeetToken(userName, password, isLogSupport)
		if err != nil {
			errChan <- err
			return
		}
		tokenChan <- meetTok
	}()

	select {
	case meetTok := <-tokenChan:
		client := CreateMeetClient(meetTok)
		if client == nil {
			return "", fmt.Errorf("Can't create meet client")
		}
		userID, err := client.GetMeetUserInfo()
		if err != nil {
			return "", err
		}

		client.UserID = userID

		meetUser := MeetUser{
			UserName: userName,
			Password: password,
			Token:    meetTok,
			UserID:   userID,
		}

		meetUsers = append(meetUsers, meetUser)
		meetClients = append(meetClients, client)
		return userID, nil

	case err := <-errChan:
		return "", fmt.Errorf("error while getting MeetToken: %w", err)

	case <-ctx.Done():
		return "", fmt.Errorf("timeout while getting MeetToken: %w", ctx.Err())
	}
}

func IsMeetUserExist(userName string) bool {
	for _, user := range meetUsers {
		if user.UserName == userName {
			return true
		}
	}
	return false
}

func IsMeetClientExist(userID string) bool {
	for _, client := range meetClients {
		if client.UserID == userID {
			return true
		}
	}
	return false
}

func GetUserClient(userID string) (*MeetChatClient, error) {
	for _, client := range meetClients {
		if client.UserID == userID {
			return client, nil
		}
	}
	return nil, fmt.Errorf("Meet client not found")
}

func GetUserID(userName string) string {
	for _, user := range meetUsers {
		if user.UserName == userName {
			return user.UserID
		}
	}
	return ""
}

func RefreshUserClient(userID string, isLogSupport bool) (*MeetChatClient, error) {
	for _, user := range meetUsers {
		if user.UserID == userID {
			for _, client := range meetClients {
				if client.UserID == userID {
					newTok, err := GetMeetToken(user.UserName, user.Password, isLogSupport)
					if err != nil {
						return nil, err
					}
					user.Token = newTok
					client = CreateMeetClient(newTok)
					return client, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Meet user not found")
}

// create a client for mattermost and set user info
func GetMeetClient(userID string, isLogSupport bool) (*MeetChatClient, error) {
	meetClient, err := GetUserClient(userID)

	//to recreate the client if undefined or if the user session is expired
	if meetClient == nil || meetClient.Client == nil || meetClient.UserID == "" || err != nil {
		meetClient, err = RefreshUserClient(userID, isLogSupport)
		if err != nil || meetClient == nil {
			return nil, err
		}
	}

	meetClient.UserID, err = meetClient.GetMeetUserInfo() //to get user info from mattermost serv
	if err != nil || meetClient.UserID == "" {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to get user info : %e", err)
		}
		return meetClient, err
	}
	meetClient.TeamIds, err = meetClient.GetTeamIDs()
	if err != nil {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to get user team : %e", err)
		}
		return meetClient, err
	}
	meetClient.AllUser, err = meetClient.GetAllUsers()
	if err != nil {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to get all users : %e", err)
		}
		return meetClient, err
	}
	err = meetClient.UpdateChannels()
	if err != nil {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to get all channels : %e", err)
		}
		return meetClient, err
	}
	meetClient.StatusUsers, err = meetClient.GetStatusUsers()
	if err != nil {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to get users status : %e", err)
		}
		return meetClient, err
	}
	err = meetClient.SetUserStatusOnline()
	if err != nil {
		if isLogSupport {
			fmt.Printf("GetMeetClient Error: failed to set user as online : %e", err)
		}
		return meetClient, err
	}
	return meetClient, err
}

func CreateMeetClient(meetToken string) *MeetChatClient {
	//to recreate the client if undefined or if the user session is expired
	if meetToken != "" {
		client := model.NewAPIv4Client(meetUrl)
		client.SetOAuthToken(meetToken)
		meetClient := &MeetChatClient{
			Client: client,
			URL:    meetUrl,
			Token:  meetToken,
		}
		return meetClient
	}
	return nil
}

// Follow the flow of the browser: log to mm using your gitlab account
func GetMeetToken(gitlabUser string, gitlabPassword string, isLogSupport bool) (string, error) {
	gitlabHost := "https://gitlab.signal18.io"

	//cookie jar
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// 1. Login gitlab
	// 1.1 get the csrf from login page
	gitlabLoginPageURL := fmt.Sprintf("%s/users/sign_in", gitlabHost)

	body, err := misc.GetRequest(client, gitlabLoginPageURL)
	if err != nil {
		return "", err
	}

	gitCsrfToken, err := misc.ExtractValue(body, "name=\"authenticity_token\" value=\"([^\"]+)\"")
	if err != nil {
		if isLogSupport {
			fmt.Println("GetMeetToken: cannot extract CSFR token from gitlab:", err)
		}
		return "", err
	}

	//1.2 Send login credentials
	form := url.Values{
		"user[login]":        {gitlabUser},
		"user[password]":     {gitlabPassword},
		"authenticity_token": {gitCsrfToken},
	}
	_, err = misc.PostRequest(client, gitlabLoginPageURL, form, nil)
	if err != nil {
		if isLogSupport {
			fmt.Println("GetMeetToken: cannot post gitlab login credentials:", err)
		}
		return "", err
	}

	// 2. Login meet
	// 2.1 Request login with gitlab account
	meetLoginPageURL := fmt.Sprintf("%s/oauth/gitlab/login?redirect_to=/signal18/channels/test", meetUrl)

	body, err = misc.GetRequest(client, meetLoginPageURL)
	if err != nil {
		if isLogSupport {
			fmt.Println("GetMeetToken: cannot get login request from mattermost:", err)
		}
		return "", err
	}

	// 2.2 Extract RedirectUrl and unescape it
	redirectUrl, err := misc.ExtractValue(body, "href=\"([^\"]+)")
	if err != nil {
		if isLogSupport {
			fmt.Println("GetMeetToken: cannot extract redirectUrl from mattermost login request", err)
		}
		return "", err
	}

	decodedValue, _ := url.QueryUnescape(redirectUrl)
	meetAuthURL := html.UnescapeString(decodedValue)

	// 2.3 Forward Oauth code to meet
	_, err = misc.GetRequest(client, meetAuthURL)
	if err != nil {
		if isLogSupport {
			fmt.Println("GetMeetToken: Oauth code forwarding failed:", err)
		}
		return "", err
	}

	// 2.4 Get MMAUTHTOKEN from cookies
	meetParsedUrl, _ := url.Parse(meetUrl)
	for _, cookie := range jar.Cookies(meetParsedUrl) {
		if cookie.Name == "MMAUTHTOKEN" {
			meetToken = cookie.Value
		}
	}

	if meetToken == "" {
		if isLogSupport {
			fmt.Println("GetMeetToken: Failed to retrieve meet token")
		}
		return "", fmt.Errorf("Failed to retrieve meet token")
	}
	return meetToken, nil
}

func (c *MeetChatClient) GetMeetUserInfo() (string, error) {
	user, _, err := c.Client.GetMe("")
	if err != nil {
		return "", err
	}

	return user.Id, err
}

func (c *MeetChatClient) GetTeamIDs() ([]string, error) {
	teams, _, err := c.Client.GetTeamsForUser(c.UserID, "")
	if len(teams) == 0 {
		err := c.JoinSignal18Team()
		if err != nil {
			return nil, err
		}
		teams, _, err = c.Client.GetTeamsForUser(c.UserID, "")
	}

	teamIDs := make([]string, len(teams))
	if err != nil {
		return teamIDs, err
	}

	for i, team := range teams {
		teamIDs[i] = team.Id
	}

	return teamIDs, nil
}

func (c *MeetChatClient) JoinSignal18Team() error {
	_, _, err := c.Client.AddTeamMember("ugnppqx3jjribmik881k4pi1bh", c.UserID)
	return err
}

func (c *MeetChatClient) GetChannels() (map[string]string, map[string]string, map[string]string, map[string]string, map[string]int, error) {
	channels, _, err := c.Client.GetChannelsForUserWithLastDeleteAt(c.UserID, 0)
	channelsMapO := make(map[string]string)
	channelsMapP := make(map[string]string)
	channelsMapD := make(map[string]string)
	channelsMapOJoin := make(map[string]string)
	unReadMessagesByChannel := make(map[string]int)

	if err != nil {
		return channelsMapO, channelsMapP, channelsMapD, channelsMapOJoin, unReadMessagesByChannel, err
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
		unReadMessages, err := c.GetUnReadMessages(channel.Id)
		if err == nil {
			unReadMessagesByChannel[channel.Id] = unReadMessages
		}

	}

	for _, team := range c.TeamIds {
		channels, _, err = c.Client.GetPublicChannelsForTeam(team, 0, 50, "")
		if err != nil {
			return channelsMapO, channelsMapP, channelsMapD, channelsMapOJoin, unReadMessagesByChannel, err
		}
		for _, channel := range channels {
			if !containsValue(channelsMapO, channel.Id) {
				channelsMapOJoin[channel.Name] = channel.Id
			}
		}
	}

	return channelsMapO, channelsMapP, channelsMapD, channelsMapOJoin, unReadMessagesByChannel, nil
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
		metadata = append(metadata, fileInfo)
	}
	return metadata
}

func (c *MeetChatClient) ReadMessages(channelID string, page int) (*MeetChannelMessages, error) {
	posts, _, err := c.Client.GetPostsForChannel(channelID, page, 30, "", true)
	if err != nil {
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
					alert.Fields = append(alert.Fields, alertField)
				}
				message.OverriveUserName = props["override_username"].(string)
				message.AlertMetadata = append(message.AlertMetadata, alert)
			}

		}
		if post.Type == "custom_jitsi" {
			attachments := post.Attachments()
			props := post.GetProps()

			for _, attachment := range attachments {
				meetingData := MeetingData{
					ID:       props["meeting_id"].(string),
					Text:     attachment.Fallback,
					MeetLink: props["meeting_link"].(string),
					MeetName: props["default_meeting_topic"].(string),
				}

				message.MeetingMetadata = append(message.MeetingMetadata, meetingData)
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

	post_mod, _, err := c.Client.CreatePost(post)
	if err != nil {
		return "", err
	}

	//fmt.Println("Message posted successfully on Mattermost", post_mod.Id)

	//if a user post a message, it means he read the channel
	err = c.ViewMessages(channelID)

	return post_mod.Id, err
}

func (c *MeetChatClient) PostAdvancedMessage(post *model.Post, markAsRead bool) (string, error) {

	post_mod, resp, err := c.Client.CreatePost(post)
	if err != nil {
		fmt.Println("PostMessage Mattermost Error:", err, resp.StatusCode)
		return "", err
	}

	if markAsRead {
		err = c.ViewMessages(post.ChannelId)
	}

	return post_mod.Id, err
}

func (c *MeetChatClient) PostMeetingLink(channelID, meetingId string) (string, error) {

	fallback := c.AllUser[c.UserID] + " has started a meeting"
	meetingLink := "https://jitsi.opensvc.com/" + meetingId + "#config.callDisplayName=%22Jitsi%20Meeting%22"

	post := &model.Post{
		ChannelId: channelID,
		UserId:    c.UserID,
		Type:      "custom_jitsi",
		Props: map[string]interface{}{
			"meeting_link":          meetingLink,
			"meeting_id":            meetingId,
			"default_meeting_topic": "Jitsi Meeting",
			"attachments": []model.SlackAttachment{
				{Fallback: fallback},
			},
		},
	}

	post_mod, _, err := c.Client.CreatePost(post)
	if err != nil {
		return "", err
	}

	//fmt.Println("Message posted successfully on Mattermost", post_mod.Id)

	//if a user post a message, it means he read the channel
	err = c.ViewMessages(channelID)

	return post_mod.Id, err
}

func (c *MeetChatClient) GetAllUsers() (map[string]string, error) {
	if len(c.TeamIds) == 0 {
		return nil, fmt.Errorf("GetAllUsers Mattermost Error: No team for user")
	}
	users, _, err := c.Client.GetUsersInTeam(c.TeamIds[0], 0, 100, "")

	usersMap := make(map[string]string)

	if err != nil {
		return usersMap, err
	}

	for _, user := range users {
		usersMap[user.Id] = user.Username
	}

	return usersMap, nil
}

func (c *MeetChatClient) GetUnReadMessages(channelID string) (int, error) {

	channelUnread, _, err := c.Client.GetChannelUnread(channelID, c.UserID)
	if err != nil {
		return 0, err
	}

	return int(channelUnread.MsgCount), nil
}

// to update the channels to load unread messages
func (c *MeetChatClient) UpdateChannels() error {
	var err error
	c.ChannelIdsOpen, c.ChannelIdsPrivate, c.ChannelIdsDirect, c.ChannelIdsOpenJoin, c.UnReadMessagesByChannel, err = c.GetChannels()
	return err
}

// to set message as read when a user see it, delete the channel from the map of unread messages
func (c *MeetChatClient) ViewMessages(channelID string) error {
	_, _, err := c.Client.ViewChannel(c.UserID, &model.ChannelView{
		ChannelId:                 channelID,
		CollapsedThreadsSupported: true,
	})

	if err != nil {
		return err
	}

	c.UnReadMessagesByChannel[channelID] = 0
	return nil
}

// to create direct channel with a user
func (c *MeetChatClient) CreateDirectChannel(userID string) (string, string, error) {
	channel, _, err := c.Client.CreateDirectChannel(c.UserID, userID)
	if err != nil {
		return "", "", err
	}

	//update the direct channels
	err = c.UpdateChannels()

	return channel.Id, getValue(c.ChannelIdsDirect, channel.Id), err
}

func (c *MeetChatClient) CreateOpenChannel(channelName string) (string, string, error) {
	channel, _, err := c.Client.CreateChannel(&model.Channel{
		CreatorId:   c.UserID,
		Name:        channelName,
		DisplayName: channelName,
		Type:        model.ChannelTypeOpen,
		TeamId:      c.TeamIds[0],
	})

	if err != nil {
		return "", "", err
	}

	//update the direct channels
	err = c.UpdateChannels()

	return channel.Id, getValue(c.ChannelIdsOpen, channel.Id), err
}

func (c *MeetChatClient) CreatePrivateChannel(channelName string) (string, string, error) {
	channel, _, err := c.Client.CreateChannel(&model.Channel{
		CreatorId:   c.UserID,
		Name:        channelName,
		DisplayName: channelName,
		Type:        model.ChannelTypePrivate,
		TeamId:      c.TeamIds[0],
	})

	if err != nil {
		return "", "", err
	}

	//update the direct channels
	err = c.UpdateChannels()

	return channel.Id, getValue(c.ChannelIdsPrivate, channel.Id), err
}

func (c *MeetChatClient) DeleteChannel(channelID string) error {
	_, err := c.Client.DeleteChannel(channelID)
	if err != nil {
		return err
	}

	//update the direct channels
	err = c.UpdateChannels()

	return err
}

func (c *MeetChatClient) LeaveChannel(channelID string) error {
	_, err := c.Client.RemoveUserFromChannel(channelID, c.UserID)
	if err != nil {
		return err
	}

	//update the direct channels
	err = c.UpdateChannels()

	return err
}

func (c *MeetChatClient) AddUserChannel(channelID string, userID string) error {
	_, _, err := c.Client.AddChannelMember(channelID, userID)
	if err != nil {
		return err
	}

	//update the direct channels
	err = c.UpdateChannels()
	return err
}

// to set user as online
func (c *MeetChatClient) SetUserStatusOnline() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "online"})
	return err
}

// to set user as offline
func (c *MeetChatClient) SetUserStatusOffline() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "offline"})
	return err
}

// to set user as away
func (c *MeetChatClient) SetUserStatusAway() error {
	_, _, err := c.Client.UpdateUserStatus(c.UserID, &model.Status{UserId: c.UserID, Status: "away"})
	return err
}

// to get status of users from direct channel
func (c *MeetChatClient) GetStatusUsers() (map[string]string, error) {
	statusUsers := make(map[string]string)
	for userName, _ := range c.ChannelIdsDirect {
		userId := getValue(c.AllUser, userName)
		status, _, err := c.Client.GetUserStatus(userId, "")
		if err != nil {
			return statusUsers, err
		}
		if status != nil {
			statusUsers[userName] = status.Status
		}
	}
	return statusUsers, nil
}

func ClearMeetClient(userID string, isSupportLog bool) {
	/*client, err := GetMeetClient(userID, isSupportLog)
	if err != nil {
		return
	}
	client.Client = nil
	client.Token = ""*/
	return
}

// to upload a file on a channel
func (c *MeetChatClient) UploadFileOnChannel(channelID string, fileBytes []byte, fileName string, message string) error {
	fileRes, _, err := c.Client.UploadFile(fileBytes, channelID, fileName)

	if fileRes == nil || err != nil {
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
		return err
	}

	return nil
}

// to download a file with the fileId
func (c *MeetChatClient) DownloadFileFromChannel(fileId string) ([]byte, string, error) {
	fileBytes, _, err := c.Client.GetFile(fileId)

	if err != nil {
		return nil, "", err
	}

	fileInfo, _, err := c.Client.GetFileInfo(fileId)

	if err != nil {
		return nil, "", err
	}

	return fileBytes, fileInfo.Name, nil
}
