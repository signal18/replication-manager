// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// Cloud18 requests are declarative (#2159): the instance writes what it wants in its config
// repository (the requested plan, cloud18-subscription-plan; an unregistration, cloud18-unregister)
// and the back office, from GitLab, answers in the pull repository's cloud18.toml (the plan it
// applied and its state). No CRM call, no GitLab password.

// readCloud18PullState reads the back office's answer, the pull repository's cloud18.toml.
func (repman *ReplicationManager) readCloud18PullState() {
	path := filepath.Join(repman.Conf.WorkingDir, ".pull", "cloud18.toml")
	if _, err := os.Stat(path); err != nil {
		return
	}
	t, err := toml.LoadFile(path)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Cloud18: the back office's cloud18.toml cannot be read: %s", err)
		return
	}
	get := func(key string) string {
		for _, k := range []string{key, "default." + key, "saved-default." + key} {
			if v := t.Get(k); v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	}
	repman.Conf.Cloud18SubscriptionPlanActive = get("cloud18-subscription-plan-active")
	repman.Conf.Cloud18SubscriptionState = get("cloud18-subscription-state")
	repman.Conf.Cloud18SubscriptionMessage = get("cloud18-subscription-message")
}

// cloud18SubscriptionView is the subscription as the instance knows it: the plan it asked for
// and what the back office answered.
func (repman *ReplicationManager) cloud18SubscriptionView() map[string]string {
	c := repman.Conf
	// the back office's state answers the requested plan: "active" for another plan than the one
	// requested means it has not handled the request yet; a refusal keeps the previous plan active
	state := c.Cloud18SubscriptionState
	if state == "" || (state == "active" && c.Cloud18SubscriptionPlanActive != c.Cloud18SubscriptionPlan) {
		state = "pending"
	}
	return map[string]string{
		"email": c.Cloud18GitUser, "uri": repman.registeredInstanceURI(),
		"plan": c.Cloud18SubscriptionPlan, "planActive": c.Cloud18SubscriptionPlanActive,
		"state": state, "message": c.Cloud18SubscriptionMessage,
	}
}

func (repman *ReplicationManager) cloud18RequestGuard(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if method != "" && r.Method != method {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return false
	}
	if !repman.isCloud18Admin(r) {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return false
	}
	if !repman.Conf.Cloud18 {
		http.Error(w, `{"error":"not registered to Cloud18"}`, http.StatusPreconditionFailed)
		return false
	}
	return true
}

// handlerGetSubscription — GET /api/register/subscription (admin): the requested plan and the
// back office's answer, from the config (no CRM call).
func (repman *ReplicationManager) handlerGetSubscription(w http.ResponseWriter, r *http.Request) {
	if !repman.cloud18RequestGuard(w, r, "") {
		return
	}
	repman.readCloud18PullState()
	json.NewEncoder(w).Encode(repman.cloud18SubscriptionView())
}

// handlerChangeSubscription — POST /api/register/subscription (admin): records the requested plan
// in the config, pushed to the config repository; the back office applies or refuses it.
func (repman *ReplicationManager) handlerChangeSubscription(w http.ResponseWriter, r *http.Request) {
	if !repman.cloud18RequestGuard(w, r, http.MethodPost) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	var req changeSubPayload
	if json.Unmarshal(body, &req) != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	req.Plan = strings.TrimSpace(strings.ToLower(req.Plan))
	if !isAllowedOfflineInstancePlan(req.Plan) {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid plan %q — must be one of free, support, support-services, partner, developer", req.Plan))
		return
	}
	repman.Conf.SetDynamicFlag("cloud18-subscription-plan", req.Plan)
	repman.persistInstanceSubscriptionPlan(req.Plan, repman.registeredInstanceURI())
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18: plan %s requested, for the back office", req.Plan)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(repman.cloud18SubscriptionView())
}

// handlerUnregister — POST /api/register/unregister (admin): pushes the request to unregister to
// the config repository, then stops Cloud18 on the instance (the URI fields become editable). The
// back office ends the subscription and removes the repositories.
func (repman *ReplicationManager) handlerUnregister(w http.ResponseWriter, r *http.Request) {
	if !repman.cloud18RequestGuard(w, r, http.MethodPost) {
		return
	}
	uri := repman.registeredInstanceURI()
	repman.Conf.Cloud18Unregister = true
	repman.Conf.SetDynamicFlag("cloud18-unregister", true)
	if repman.ConfigManager != nil {
		repman.ConfigManager.SaveConfig(repman, true)
		repman.ConfigManager.GitPush(repman.Conf, repman.ClusterList, true) // the request reaches GitLab before Cloud18 stops
	}
	// cloud18 = false where the value lives (a config file's value is immutable): a restart keeps it off
	repman.Conf.Cloud18 = false
	if _, immutable := repman.Conf.GetImmuableFlag("cloud18"); immutable {
		repman.Conf.SetImmuableFlag("cloud18", false)
	} else {
		repman.Conf.SetDynamicFlag("cloud18", false)
	}
	repman.Save()
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Cloud18: unregistration of %s requested, for the back office; Cloud18 stopped on this instance", uri)
	json.NewEncoder(w).Encode(map[string]string{"state": "requested", "uri": uri,
		"message": "unregistration requested: the back office ends the subscription and removes the repositories"})
}
