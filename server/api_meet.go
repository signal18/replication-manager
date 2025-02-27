// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/meethelper"
)

func (repman *ReplicationManager) apiMeetProtectedHandler(router *mux.Router) {
	// Meet Handler
	// router.PathPrefix("/meet/").Handler(http.StripPrefix("/meet/", repman.proxyToURL("https://meet.signal18.io/api/v4")))
	router.Handle("/meet/info", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.MeetInfoHandler)),
	))
	router.Handle("/meet/read/{channelId}/{page}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.ReadMeetMessageHandler)),
	))
	router.Handle("/meet/post/{channelId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.PostMeetHandler)),
	))
	router.Handle("/meet/post/jitsi/{channelId}/{meetingId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.PostJitsiMeetingHandler)),
	))
	router.Handle("/meet/view/{channelId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.ViewMeetHandler)),
	))
	router.Handle("/meet/create/direct/{userId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.CreateDirectChannelMeetHandler)),
	))
	router.Handle("/meet/create/private/{channelName}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.CreatePrivateChannelMeetHandler)),
	))
	router.Handle("/meet/create/public/{channelName}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.CreatePublicChannelMeetHandler)),
	))
	router.Handle("/meet/delete/{channelId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.DeleteChannelMeetHandler)),
	))
	router.Handle("/meet/leave/{channelId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.LeaveChannelMeetHandler)),
	))
	router.Handle("/meet/add/{channelId}/{userId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.AddUserChannelMeetHandler)),
	))
	router.Handle("/meet/logout", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.LogoutMeetHandler)),
	))
	router.Handle("/meet/upload/{channelId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.UploadFileMeetHandler)),
	))
	router.Handle("/meet/download/{fileId}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.DownloadFileMeetHandler)),
	))
	// /////////////////////////
}

// Meet Handler
// to send user info to the front (userID, All available channels, allusers id for private chat)
func (repman *ReplicationManager) MeetInfoHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "MeetInfo: No user ID in request header")
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil || meetClient == nil || meetClient.UserID == "" {
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "MeetInfo: Error getting meet client: %e", err)
		}
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		return
	}
	info := struct {
		UserID                  string            `json:"user_id"`
		ChannelIdsOpen          map[string]string `json:"channel_ids_open"`
		ChannelIdsPrivate       map[string]string `json:"channel_ids_private"`
		ChannelIdsDirect        map[string]string `json:"channel_ids_direct"`
		UnReadMessagesByChannel map[string]int    `json:"unread_messages_by_channel"`
		AllUsers                map[string]string `json:"all_users"`
		StatusUsers             map[string]string `json:"status_users"`
	}{
		UserID:                  meetClient.UserID,
		ChannelIdsOpen:          meetClient.ChannelIdsOpen,
		ChannelIdsPrivate:       meetClient.ChannelIdsPrivate,
		ChannelIdsDirect:        meetClient.ChannelIdsDirect,
		UnReadMessagesByChannel: meetClient.UnReadMessagesByChannel,
		AllUsers:                meetClient.AllUser,
		StatusUsers:             meetClient.StatusUsers,
	}

	err = json.NewEncoder(w).Encode(info)
	if err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlDbg, "MeetInfo: Success to get meetInfo")

}

func (repman *ReplicationManager) ReadMeetMessageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "MeetInfo: No user ID in request header")
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil || meetClient == nil {
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "ReadMeetMessage: Error getting meet client")
		}
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	channelID := vars["channelId"]
	str_page := vars["page"]
	page := 0

	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "ReadMeetMessage: Channel ID is required")
		}
		return
	}

	if str_page != "" {
		page, _ = strconv.Atoi(str_page)
	}

	messages, err := meetClient.ReadMessages(channelID, page)
	if messages == nil || err != nil {
		http.Error(w, "Error reading messages API", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "MeetInfo: failed to get message from channel %s : %e", channelID, err)
		}
		return
	}

	err = json.NewEncoder(w).Encode(messages)
	if err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlDbg, "MeetInfo: Success to get message from channel %s", channelID)

}

func (repman *ReplicationManager) PostMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "MeetInfo: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "PostMeetMessage: Error getting meet client")
		}
		return
	}
	var request struct {
		Message string `json:"message"`
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMessage: Channel ID is required")
		}
		return
	}

	err = json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	message_id, err := meetClient.PostMessage(channelID, request.Message)
	if err != nil {
		http.Error(w, "Error posting message API", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMessage: error posting message to channel %s", channelID)
		}
		return
	}

	response := map[string]string{
		"status":     "success",
		"message":    request.Message,
		"channel":    channelID,
		"user":       meetClient.UserID,
		"message_id": message_id,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "PostMeetMessage: Success to post message on channel %s", channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) PostJitsiMeetingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMeeting: No user ID in request header")
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "PostMeetMeeting: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMeeting: Channel ID is required")
		}
		return
	}

	meetingID := vars["meetingId"]
	if channelID == "" {
		http.Error(w, "Jitsi Meeting ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMeeting: Channel ID is required")
		}
		return
	}

	message_id, err := meetClient.PostMeetingLink(channelID, meetingID)
	if err != nil {
		http.Error(w, "Error posting message API", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "PostMeetMeeting: error posting meeting link to channel %s", channelID)
		}
		return
	}

	response := map[string]string{
		"status":     "success",
		"channel":    channelID,
		"user":       meetClient.UserID,
		"message_id": message_id,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "PostMeetMeeting: Success to post meeting link on channel %s", channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) ViewMeetHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "ViewMeetMessage: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "ViewMeetMessage: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" && repman.Conf.LogSupport {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "ViewMeetMessage: Channel ID is required")
		return
	}

	err = meetClient.ViewMessages(channelID)

	if err != nil {
		http.Error(w, "Error view message API", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "ViewMeetMessage: error view message from channel %s", channelID)
		}
		return
	}

	response := map[string]string{
		"status":  "success",
		"channel": channelID,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlDbg, "PostMeetMeeting: Success to view message on channel %s", channelID)

	json.NewEncoder(w).Encode(response)

}

func (repman *ReplicationManager) CreateDirectChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreateDChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))

	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "CreateDChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	userId := vars["userId"]
	if userId == "" {
		http.Error(w, "User Id is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreateDChannelMeet: User ID is required")
		}
		return
	}

	newChannelId, newChannelName, err := meetClient.CreateDirectChannel(userId)

	if err != nil {
		http.Error(w, "Error view message API", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreateDChannelMeet: User ID is required")
		}
		return
	}

	response := map[string]string{
		"status":      "success",
		"channelId":   newChannelId,
		"channelName": newChannelName,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "CreateDChannelMeet: Success to create a direct channel %s, %s", newChannelName, newChannelId)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) CreatePrivateChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreatePrivateChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "CreatePrivateChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelName := vars["channelName"]
	if channelName == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreatePrivateChannelMeet: Channel name is required")
		}
		return
	}

	newChannelId, newChannelName, err := meetClient.CreatePrivateChannel(channelName)

	if err != nil {
		http.Error(w, "Error view message API", http.StatusInternalServerError)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreatePrivateChannelMeet: Enable to create channel: %e", err)
		return
	}

	response := map[string]string{
		"status":      "success",
		"channelId":   newChannelId,
		"channelName": newChannelName,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "CreatePrivateChannelMeet: Success to create a private channel %s, %s", newChannelName, newChannelId)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) CreatePublicChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreatePublicChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "CreatePublicChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelName := vars["channelName"]
	if channelName == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "CreatePublicChannelMeet: Channel name is required")
		}
		return
	}

	newChannelId, newChannelName, err := meetClient.CreateOpenChannel(channelName)

	if err != nil {
		http.Error(w, "Error view message API", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"status":      "success",
		"channelId":   newChannelId,
		"channelName": newChannelName,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "CreatePublicChannelMeet: Success to create a public channel %s, %s", newChannelName, newChannelId)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) DeleteChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DeleteChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "DeleteChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DeleteChannelMeet: Channel ID is required")
		}
		return
	}

	err = meetClient.DeleteChannel(channelID)

	if err != nil {
		http.Error(w, "Error delete channel", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DeleteChannelMeet: error delete channel %s", channelID)
		}
		return
	}

	response := map[string]string{
		"status":    "success",
		"channelId": channelID,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "DeleteChannelMeet: Success to delete %s channel", channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) LeaveChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "LeaveChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "LeaveChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "LeaveChannelMeet: Channel ID is required")
		}
		return
	}

	err = meetClient.LeaveChannel(channelID)

	if err != nil {
		http.Error(w, "Error leave channel", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "LeaveChannelMeet: error leave channel %s", channelID)
		}
		return
	}

	response := map[string]string{
		"status":    "success",
		"channelId": channelID,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "LeaveChannelMeet: Success to leave channel %s", channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) AddUserChannelMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "AddUserChannelMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "AddUserChannelMeet: Error getting meet client")
		}
		return
	}

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "AddUserChannelMeet: Channel ID is required")
		}
		return
	}
	userId := vars["userId"]
	if userId == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "AddUserChannelMeet: User ID is required")
		}
		return
	}

	err = meetClient.AddUserChannel(channelID, userId)

	if err != nil {
		http.Error(w, "Error add user to channel", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "AddUserChannelMeet: error add user (%s) to channel (%s)", userId, channelID)
		}
		return
	}

	response := map[string]string{
		"status":    "success",
		"channelId": channelID,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "AddUserChannelMeet: Success to add user %s to channel %s", userId, channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) LogoutMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "LogoutMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "LogoutMeet: Error getting meet client")
		}
		return
	}

	userId := meetClient.UserID

	err = meetClient.SetUserStatusOffline()

	if err != nil {
		http.Error(w, "Error set user status while logout", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "LogoutMeet: Error set user status while logout")
		}
		return
	}

	response := map[string]string{
		"status": "success",
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "LogoutMeet: Success user %s to logout from mattermost serv", userId)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) UploadFileMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	vars := mux.Vars(r)
	channelID := vars["channelId"]
	if channelID == "" {
		http.Error(w, "Channel ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "UploadFileMeet: Channel ID is required")
		}
		return
	}

	err := r.ParseMultipartForm(10 << 20) // Limite la taille du fichier à 10MB
	if err != nil {
		http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading the file", http.StatusInternalServerError)
		return
	}

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "UploadFileMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "UploadFileMeet: Error getting meet client")
		}
		return
	}

	message := r.FormValue("message")

	err = meetClient.UploadFileOnChannel(channelID, fileBytes, handler.Filename, message)

	if err != nil {
		http.Error(w, "Error sending the file to mattermost serv", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "UploadFileMeet: Error sending the file to mattermost serv")
		}
		return
	}

	response := map[string]string{
		"status":   "success",
		"fileName": handler.Filename,
		"channel":  channelID,
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "UploadFileMeet: Success to upload a file on %s channel", channelID)

	json.NewEncoder(w).Encode(response)
}

func (repman *ReplicationManager) DownloadFileMeetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	vars := mux.Vars(r)
	fileId := vars["fileId"]
	if fileId == "" {
		http.Error(w, "File ID is required", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DownloadFileMeet: File ID is required")
		}
		return
	}

	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		http.Error(w, "Missing user ID in header", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DownloadFileMeet: No user ID in request header")
		}
		return
	}

	meetClient, err := meethelper.GetMeetClient(userID, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		http.Error(w, "Error getting meet client", http.StatusUnauthorized)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "DownloadFileMeet: Error getting meet client")
		}
		return
	}

	fileBytes, fileName, err := meetClient.DownloadFileFromChannel(fileId)

	if err != nil {
		http.Error(w, "Error sending the file to mattermost serv", http.StatusInternalServerError)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DownloadFileMeet: Error sending the file to mattermost serv")
		}
		return
	}

	// Set the headers for the file download
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName)) // Remplacez par le nom réel du fichier
	w.Header().Set("Content-Type", "application/octet-stream")

	// Write the file bytes to the response
	_, err = w.Write(fileBytes)

	if err != nil {
		http.Error(w, "Error sending download file data to front", http.StatusBadRequest)
		if repman.Conf.LogSupport {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "DownloadFileMeet: Error sending download file data to front : %s", err)
		}
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "DownloadFileMeet: Success to retrieve and send download file data")
}
