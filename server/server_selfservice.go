// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/mailer"
)

// Self-service clusters on a Cloud18 infrastructure (issue #1838, decided
// 2026-09-25): a registered Cloud18 user who reaches this instance through
// peering may create a cluster here directly, without the subscription and
// email-acceptance chain; the partner is only informed. The cluster starts on
// the instance's default unit plan (DBU / APU / BKU as configured here), no
// service plan is chosen. Abuse is capped by cloud18-self-service-max-clusters-
// per-user, counted per SSO identity as the clusters where that identity is the
// sponsor. The creator becomes the sponsor of the cluster with just enough
// grants to populate, provision and drop it, nothing on other clusters.
//
// Before this change POST /api/clusters/actions/add accepted any authenticated
// user with no grant at all, and the creator was not even added to the new
// cluster. Now a local user needs cluster-create or prov-cluster (as the ACL
// table always intended), and an SSO identity goes through the self-service
// rules below.

const (
	selfServiceSponsorGrants = "cluster-create-monitor cluster-settings cluster-delete cluster-show prov app-deployment db show proxy grant-show extrole token-create"
)

// selfServiceCapable reports whether this instance can host self-service clusters.
func (repman *ReplicationManager) selfServiceCapable() (bool, string) {
	if !repman.Conf.Cloud18SelfServiceClusters {
		return false, "self-service cluster creation is disabled on this infrastructure (cloud18-self-service-clusters)"
	}
	if !repman.Conf.Cloud18 {
		return false, "this infrastructure is not registered with Cloud18"
	}
	switch repman.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes:
		return true, ""
	}
	return false, fmt.Sprintf("self-service needs an OpenSVC or Kubernetes orchestrator, this infrastructure runs %q", repman.Conf.ProvOrchestrator)
}

// countSponsoredClusters counts the clusters where the identity is the sponsor.
func (repman *ReplicationManager) countSponsoredClusters(identity string) (int, []string) {
	names := []string{}
	for _, cl := range repman.Clusters {
		if u, ok := cl.APIUsers[identity]; ok && u.Roles[config.RoleSponsor] {
			names = append(names, cl.Name)
		}
	}
	return len(names), names
}

// SelfServiceStatus is what GET /api/cloud18/self-service answers: whether the
// caller may create a cluster here and how many they already have.
type SelfServiceStatus struct {
	Enabled      bool     `json:"enabled"`
	Reason       string   `json:"reason,omitempty"`
	Orchestrator string   `json:"orchestrator"`
	MaxPerUser   int      `json:"maxClustersPerUser"`
	Identity     string   `json:"identity"`
	Used         int      `json:"used"`
	Clusters     []string `json:"clusters"`
	Remaining    int      `json:"remaining"`
	DefaultDBU   int      `json:"defaultDbu"`
	DefaultAPU   int      `json:"defaultApu"`
	DefaultBKU   int      `json:"defaultBku"`
}

func (repman *ReplicationManager) selfServiceStatusFor(identity string) SelfServiceStatus {
	ok, reason := repman.selfServiceCapable()
	used, names := repman.countSponsoredClusters(identity)
	remaining := repman.Conf.Cloud18SelfServiceMaxClustersPerUser - used
	if remaining < 0 {
		remaining = 0
	}
	return SelfServiceStatus{
		Enabled: ok, Reason: reason, Orchestrator: repman.Conf.ProvOrchestrator,
		MaxPerUser: repman.Conf.Cloud18SelfServiceMaxClustersPerUser,
		Identity:   identity, Used: used, Clusters: names, Remaining: remaining,
		DefaultDBU: repman.Conf.ProvDbDbu, DefaultAPU: repman.Conf.ProvServicePlanApu, DefaultBKU: repman.Conf.ProvServicePlanBku,
	}
}

// requestIdentity returns the caller's identity and whether it is an external
// SSO (Cloud18 / GitLab) identity rather than a local account.
func (repman *ReplicationManager) requestIdentity(r *http.Request) (identity string, sso bool) {
	claims, err := repman.GetJWTClaims(r)
	if err != nil {
		return "", false
	}
	return claims["User"], claims["AuthType"] == "SSO"
}

// selfServiceCheck decides whether identity may create one more cluster here.
func (repman *ReplicationManager) selfServiceCheck(identity string) error {
	if ok, reason := repman.selfServiceCapable(); !ok {
		return errors.New(reason)
	}
	used, names := repman.countSponsoredClusters(identity)
	if used >= repman.Conf.Cloud18SelfServiceMaxClustersPerUser {
		return fmt.Errorf("%s already sponsors %d cluster(s) here (%s), the limit is %d per user (cloud18-self-service-max-clusters-per-user); drop one to free a slot",
			identity, used, strings.Join(names, ", "), repman.Conf.Cloud18SelfServiceMaxClustersPerUser)
	}
	return nil
}

// CreateSelfServiceSponsorForm is the creator's account on their own cluster:
// the sponsor role plus what populating, provisioning and dropping it needs.
func (repman *ReplicationManager) CreateSelfServiceSponsorForm(identity string) cluster.UserForm {
	return cluster.UserForm{
		Username: identity,
		Roles:    config.RoleSponsor,
		Grants:   selfServiceSponsorGrants,
	}
}

// attachSelfServiceSponsor makes the SSO identity the sponsor of the new cluster.
// Password stays empty so the account remains SSO-only (IsLocalOnlyAccount).
func (repman *ReplicationManager) attachSelfServiceSponsor(cl *cluster.Cluster, identity string) error {
	form := repman.CreateSelfServiceSponsorForm(identity)
	u := cluster.APIUser{User: identity, Password: "", Grants: map[string]bool{}, Roles: map[string]bool{}}
	cl.SetUserGrants(&u, form.Grants)
	cl.SetUserRoles(&u, form.Roles)
	u.Roles[config.RoleSponsor] = true // the limit counts on this role
	cl.APIUsers[identity] = u
	// Persist through the external ACL so the account survives a reload.
	if cl.Conf.APIUsersACLAllowExternal != "" {
		cl.Conf.APIUsersACLAllowExternal += ","
	}
	cl.Conf.APIUsersACLAllowExternal += identity + ":" + form.Grants + ":" + form.Roles
	cl.SaveAcls()
	return nil
}

// notifySelfServiceCluster informs the partner: security log, cluster log and,
// when mail is configured, a message to mail-to. Nothing to accept.
func (repman *ReplicationManager) notifySelfServiceCluster(cl *cluster.Cluster, identity string, remote string) {
	msg := fmt.Sprintf("Self-service cluster %s created on %s by %s (default unit plan DBU %d / APU %d / BKU %d)",
		cl.Name, repman.registeredInstanceURI(), identity, repman.Conf.ProvDbDbu, repman.Conf.ProvServicePlanApu, repman.Conf.ProvServicePlanBku)
	repman.logSecurityEvent("cloud18_self_service_cluster", identity, remote, msg)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "%s", msg)
	if repman.Conf.MailTo != "" && repman.Conf.MailSMTPAddr != "" {
		go func() {
			if err := repman.SendMail(mailer.Email{Subject: "[replication-manager] " + msg, Message: msg, To: repman.Conf.MailTo}); err != nil {
				repman.Logrus.Warnf("self-service notification mail failed: %v", err)
			}
		}()
	}
}

// handlerMuxSelfServiceStatus — GET /api/cloud18/self-service: for the caller.
//
// @Summary Self-service cluster status for the caller
// @Description Whether this instance accepts self-service cluster creation from Cloud18 users, the per-user limit, and how many clusters the caller already sponsors here.
// @Tags Cloud18
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} SelfServiceStatus
// @Router /api/cloud18/self-service [get]
func (repman *ReplicationManager) handlerMuxSelfServiceStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	identity, _ := repman.requestIdentity(r)
	if identity == "" {
		http.Error(w, "User is not valid", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repman.selfServiceStatusFor(identity))
}
