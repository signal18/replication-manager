// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// Cloud18 requests are declarative (#2159): the instance writes what it wants in its config
// repository (the requested plan, cloud18-subscription-plan; an unregistration, cloud18-unregister)
// and the back office, from GitLab, answers in the pull repository's cloud18.toml (the plan it
// applied, the state, a message). No CRM call, no GitLab password.

// cloud18PullState is the back office's answer, read from the pull repository: runtime only, never
// saved nor pushed with the instance's config.
type cloud18PullState struct {
	mu                         sync.Mutex
	planActive, state, message string
}

// readCloud18PullState reads the back office's answer, the pull repository's cloud18.toml: the
// three values from one section (top level, [default] or [saved-default]); none when the file is
// not there (any more).
func (repman *ReplicationManager) readCloud18PullState() {
	var planActive, state, message string
	path := filepath.Join(repman.Conf.WorkingDir, ".pull", "cloud18.toml")
	if t, err := toml.LoadFile(path); err == nil {
		for _, prefix := range []string{"", "default.", "saved-default."} {
			get := func(key string) string {
				if v := t.Get(prefix + key); v != nil {
					return fmt.Sprintf("%v", v)
				}
				return ""
			}
			if planActive, state, message = get("cloud18-subscription-plan-active"), get("cloud18-subscription-state"), get("cloud18-subscription-message"); planActive != "" || state != "" {
				break
			}
		}
	} else if !os.IsNotExist(err) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Cloud18: the back office's cloud18.toml cannot be read: %s", err)
		return
	}
	p := &repman.cloud18Pull
	p.mu.Lock()
	p.planActive, p.state, p.message = planActive, state, message
	p.mu.Unlock()
}

// cloud18SubscriptionView is the subscription as the instance knows it: the plan it asked for
// and what the back office answered. The state answers the requested plan: "active" for another
// plan than the one requested means the request is not handled yet; a refusal keeps the previous
// plan active.
func (repman *ReplicationManager) cloud18SubscriptionView() map[string]string {
	c := repman.Conf
	p := &repman.cloud18Pull
	p.mu.Lock()
	planActive, state, message := p.planActive, p.state, p.message
	p.mu.Unlock()
	if state == "" || (state == "active" && planActive != c.Cloud18SubscriptionPlan) {
		state = "pending"
	}
	return map[string]string{
		"email": c.Cloud18GitUser, "uri": repman.registeredInstanceURI(),
		"plan": c.Cloud18SubscriptionPlan, "planActive": planActive, "state": state, "message": message,
	}
}

var errCloud18NotRegistered = errors.New("not registered to Cloud18")

// cloud18RequestPlan records the requested plan in the config and pushes it (no wait): the back
// office applies or refuses it.
func (repman *ReplicationManager) cloud18RequestPlan(plan string) (map[string]string, error) {
	if !repman.Conf.Cloud18 {
		return nil, errCloud18NotRegistered
	}
	plan = strings.TrimSpace(strings.ToLower(plan))
	if !isAllowedOfflineInstancePlan(plan) {
		return nil, fmt.Errorf("invalid plan %q — must be one of free, support, support-services, partner, developer", plan)
	}
	repman.Conf.SetDynamicFlag("cloud18-subscription-plan", plan)
	repman.persistInstanceSubscriptionPlan(plan, repman.registeredInstanceURI())
	if repman.ConfigManager != nil {
		repman.ConfigManager.GitPush(repman.Conf, repman.ClusterList, false)
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18: plan %s requested, for the back office", plan)
	return repman.cloud18SubscriptionView(), nil
}

// errCloud18UnregisterNotPushed: the request is recorded but GitLab has not received it.
var errCloud18UnregisterNotPushed = errors.New("unregistration recorded but not pushed to GitLab yet: Cloud18 stays on, try again (it is also sent with the next push)")

// cloud18RequestUnregister records the request to unregister, pushes it, and stops Cloud18 on the
// instance only once GitLab has it (else the back office would never see it); cloud18 = false is
// written where the value lives, so a restart keeps it off.
func (repman *ReplicationManager) cloud18RequestUnregister() (map[string]string, error) {
	if !repman.Conf.Cloud18 {
		return nil, errCloud18NotRegistered
	}
	conf := repman.Conf
	uri := repman.registeredInstanceURI()
	conf.Cloud18Unregister = true
	conf.SetDynamicFlag("cloud18-unregister", true)
	if repman.ConfigManager != nil {
		repman.ConfigManager.SaveConfig(repman, true)
		repman.ConfigManager.GitPush(conf, repman.ClusterList, true)
	}
	// the git manager pushes to "origin" (go-git's default remote), the remote GitHeadPushed reads
	pushed, err := conf.GitHeadPushed(conf.WorkingDir, conf.GitUsername, conf.GetDecryptedValue("git-acces-token"))
	if !pushed {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Cloud18: unregistration of %s not pushed yet (%v)", uri, err)
		return nil, errCloud18UnregisterNotPushed
	}
	conf.Cloud18 = false
	if _, immutable := conf.GetImmuableFlag("cloud18"); immutable {
		conf.SetImmuableFlag("cloud18", false)
	} else {
		conf.SetDynamicFlag("cloud18", false)
	}
	repman.Save()
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Cloud18: unregistration of %s pushed for the back office; Cloud18 stopped on this instance", uri)
	return map[string]string{"state": "requested", "uri": uri,
		"message": "unregistration requested: the back office ends the subscription and removes the repositories"}, nil
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
// back office's answer (no CRM call).
func (repman *ReplicationManager) handlerGetSubscription(w http.ResponseWriter, r *http.Request) {
	if !repman.cloud18RequestGuard(w, r, "") {
		return
	}
	repman.readCloud18PullState()
	json.NewEncoder(w).Encode(repman.cloud18SubscriptionView())
}

// handlerChangeSubscription — POST /api/register/subscription (admin): the requested plan, pushed
// to the config repository; the back office applies or refuses it (202).
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
	view, err := repman.cloud18RequestPlan(req.Plan)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errCloud18NotRegistered) {
			code = http.StatusPreconditionFailed
		}
		writeJSONError(w, code, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(view)
}

// handlerUnregister — POST /api/register/unregister (admin): pushes the request to unregister,
// then stops Cloud18 on the instance; 503 while GitLab has not received it.
func (repman *ReplicationManager) handlerUnregister(w http.ResponseWriter, r *http.Request) {
	if !repman.cloud18RequestGuard(w, r, http.MethodPost) {
		return
	}
	out, err := repman.cloud18RequestUnregister()
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	json.NewEncoder(w).Encode(out)
}
