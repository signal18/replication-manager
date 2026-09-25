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
	// ResourceManager pool: what a new cluster needs and what is free.
	NeededDBU float64       `json:"neededDbu"`
	NeededAPU float64       `json:"neededApu"`
	Pool      InfraUnitPool `json:"pool"`
	PoolOK    bool          `json:"poolOk"`
	PoolNote  string        `json:"poolNote,omitempty"`
}

func (repman *ReplicationManager) selfServiceStatusFor(identity string) SelfServiceStatus {
	ok, reason := repman.selfServiceCapable()
	used, names := repman.countSponsoredClusters(identity)
	remaining := repman.Conf.Cloud18SelfServiceMaxClustersPerUser - used
	if remaining < 0 {
		remaining = 0
	}
	st := SelfServiceStatus{
		Enabled: ok, Reason: reason, Orchestrator: repman.Conf.ProvOrchestrator,
		MaxPerUser: repman.Conf.Cloud18SelfServiceMaxClustersPerUser,
		Identity:   identity, Used: used, Clusters: names, Remaining: remaining,
		DefaultDBU: repman.Conf.ProvDbDbu, DefaultAPU: repman.Conf.ProvServicePlanApu, DefaultBKU: repman.Conf.ProvServicePlanBku,
		Pool: repman.infraUnitPool(), PoolOK: true,
	}
	st.NeededDBU, st.NeededAPU = repman.selfServiceUnitsNeeded()
	if !st.Pool.Known {
		st.PoolNote = "infrastructure capacity unknown (no agent observed, no resource-manager-infra-* declared): the pool does not gate"
	} else if err := repman.selfServicePoolCheck(); err != nil {
		st.PoolOK = false
		st.PoolNote = err.Error()
		if st.Enabled {
			st.Enabled = false
			st.Reason = err.Error()
		}
	}
	return st
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

// selfServiceUnitsNeeded is what a self-service cluster reserves on creation:
// the default plan of a master and a replica (2 × prov-db-dbu) and the default
// APU (prov-service-plan-apu). BKU is storage only and not pooled.
func (repman *ReplicationManager) selfServiceUnitsNeeded() (dbu, apu float64) {
	return float64(2 * repman.Conf.ProvDbDbu), float64(repman.Conf.ProvServicePlanApu)
}

// selfServicePoolCheck asks the ResourceManager whether the infrastructure's
// free pool (capacity × quota − Σ plans sold) can hold a new default cluster.
// An unknown pool (no agent capacity observed, no resource-manager-infra-*
// declared) cannot gate: the creation goes through and the status says so.
func (repman *ReplicationManager) selfServicePoolCheck() error {
	pool := repman.infraUnitPool()
	if !pool.Known {
		return nil
	}
	needDbu, needApu := repman.selfServiceUnitsNeeded()
	if needDbu > pool.FreeDbu {
		return fmt.Errorf("no free DBU on this infrastructure for a new cluster: %.0f DBU needed (2 × prov-db-dbu), %.1f free of %.1f usable (%.1f already planned)",
			needDbu, pool.FreeDbu, pool.UsableDbu, pool.PlannedDbu)
	}
	if needApu > pool.FreeApu {
		return fmt.Errorf("no free APU on this infrastructure for a new cluster: %.0f APU needed (prov-service-plan-apu), %.1f free of %.1f usable (%.1f already planned)",
			needApu, pool.FreeApu, pool.UsableApu, pool.PlannedApu)
	}
	return nil
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
	return repman.selfServicePoolCheck()
}

// clusterAddAuthorize decides who may create a cluster (POST
// /api/clusters/actions/add): a principal holding cluster-create or
// prov-cluster anywhere (local account, SSO identity or token) creates as
// before; an SSO identity without them goes through the self-service rules.
// status is 0 when allowed, otherwise the HTTP status and reason to answer.
func (repman *ReplicationManager) clusterAddAuthorize(r *http.Request) (identity string, sso bool, selfService bool, status int, reason string) {
	identity, sso = repman.requestIdentity(r)
	if identity == "" {
		return "", false, false, http.StatusInternalServerError, "User is not valid"
	}
	if repman.UserHasGlobalGrant(r, config.GrantClusterCreate) {
		return identity, sso, false, 0, ""
	}
	// prov-cluster also opens the route (ACL table), but a sponsor holds it on
	// their own self-service clusters: only prov-cluster held elsewhere counts,
	// or the limit would end after the first cluster.
	if repman.UserHasGlobalGrant(r, config.GrantProvCluster) && (!sso || repman.holdsProvClusterOutsideSponsorship(identity)) {
		return identity, sso, false, 0, ""
	}
	if !sso {
		return identity, false, false, http.StatusForbidden, "No valid ACL: cluster-create or prov-cluster grant required"
	}
	if err := repman.selfServiceCheck(identity); err != nil {
		repman.logSecurityEvent("cloud18_self_service_denied", identity, r.RemoteAddr, err.Error())
		return identity, true, false, http.StatusForbidden, err.Error()
	}
	return identity, true, true, 0, ""
}

// holdsProvClusterOutsideSponsorship reports whether identity has prov-cluster
// on a cluster it does not sponsor.
func (repman *ReplicationManager) holdsProvClusterOutsideSponsorship(identity string) bool {
	for _, cl := range repman.Clusters {
		if u, ok := cl.APIUsers[identity]; ok && u.Grants[config.GrantProvCluster] && !u.Roles[config.RoleSponsor] {
			return true
		}
	}
	return false
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

// attachSelfServiceSponsor makes the creator the sponsor of the new cluster,
// through the same path as any external account (api-credentials-external +
// api-users-acl-allow-external, which is what SaveAcls persists and
// LoadAPIUsers reads back) but passwordless, so only an SSO login can act as it
// (a password-protected account is local-only for the oidc ACL check). An
// identity already holding cluster-settings on the cluster (e.g. the instance's
// own Cloud18 user, sysops on every cluster) is left untouched.
func (repman *ReplicationManager) attachSelfServiceSponsor(cl *cluster.Cluster, identity string) error {
	form := repman.CreateSelfServiceSponsorForm(identity)
	if u, ok := cl.APIUsers[identity]; ok {
		if u.Grants[config.GrantClusterSettings] {
			return nil
		}
		if u.Password != "" && identity != cl.Conf.Cloud18GitUser {
			return fmt.Errorf("%s is a local account on %s, an SSO identity cannot take it over", identity, cl.Name)
		}
		// Present without an ACL entry of its own (default visitor): re-create
		// it as the sponsor rather than editing an entry that may not exist.
		form.Grants = cl.AppendGrants(form.Grants, &u)
		form.Roles = cl.AppendRoles(form.Roles, &u)
		if err := cl.DropUser(cluster.UserForm{Username: identity}, false); err != nil {
			return err
		}
	}
	if err := cl.AddSSOOnlyUser(form, "admin", false); err != nil {
		return err
	}
	cl.LoadAPIUsers()
	cl.SaveAcls()
	cl.Save()
	if u, ok := cl.APIUsers[identity]; !ok || !u.Roles[config.RoleSponsor] {
		return fmt.Errorf("sponsor role not applied to %s on %s", identity, cl.Name)
	}
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
