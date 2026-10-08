// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// The infrastructure identity and the app templates it can deploy, so a client
	// plans an app and renders its URL (the template's primary route CNAME is
	// <app>.<cluster>.<subDomain>-<zone>.<domain>.cloud18.io) before creating anything.
	Domain        string   `json:"domain"`
	SubDomain     string   `json:"subDomain"`
	Zone          string   `json:"zone"`
	GatewayDomain string   `json:"gatewayDomain"`
	AppTemplates  []string `json:"appTemplates"`
	// ResourceManager pool: what a new cluster needs and what is free.
	NeededDBU float64       `json:"neededDbu"`
	NeededAPU float64       `json:"neededApu"`
	Pool      InfraUnitPool `json:"pool"`
	PoolOK    bool          `json:"poolOk"`
	PoolNote  string        `json:"poolNote,omitempty"`
	// Borrowed: the pool could not guarantee the units but the over-commit pot can lend them
	// (cloud18-self-service-clusters-can-borrow): the creation goes through without guarantee.
	Borrowed bool `json:"borrowed"`
}

// selfServiceSnapshot is the identity-independent part of the self-service status, computed
// at most once per cloud18-self-service-cache-seconds and served to every caller: the pool
// walks every cluster and the ledger, the template list reads the repositories, and the
// enabled script is a process. A flood of GET /api/cloud18/self-service (every Cloud18 peer
// reaches it, a brute force reaches it) therefore costs one computation per interval.
type selfServiceSnapshot struct {
	at        time.Time
	capable   bool
	reason    string
	pool      InfraUnitPool
	poolNote  string
	poolErr   error
	templates []string
	neededDBU float64
	neededAPU float64
}

// selfServiceVerdict is one identity's enabled-script verdict (nil = allowed) and when it
// was taken; kept on the manager, not on the snapshot, so a snapshot refresh never loses
// or re-runs it and the snapshot itself stays immutable once published.
type selfServiceVerdict struct {
	seen   time.Time
	script string // the script the verdict came from: a changed setting is a new verdict
	err    error
}

// selfServiceScriptCacheMax bounds the per-identity verdict map: beyond it the map is
// dropped, never grown (an identity is an authenticated caller, the bound is defensive).
const selfServiceScriptCacheMax = 1024

func (repman *ReplicationManager) selfServiceCacheTTL() time.Duration {
	if repman.Conf == nil || repman.Conf.Cloud18SelfServiceCacheSeconds <= 0 {
		return 0
	}
	return time.Duration(repman.Conf.Cloud18SelfServiceCacheSeconds) * time.Second
}

// selfServiceSnapshotNow returns the current snapshot, recomputed when older than the TTL
// (or at every call when the TTL is 0). The computation runs outside selfServiceMu (it
// reads the clusters and the resource manager) and once at a time: concurrent callers
// wait on the same single flight instead of computing their own.
func (repman *ReplicationManager) selfServiceSnapshotNow() *selfServiceSnapshot {
	ttl := repman.selfServiceCacheTTL()
	fresh := func() *selfServiceSnapshot {
		repman.selfServiceMu.Lock()
		defer repman.selfServiceMu.Unlock()
		if snap := repman.selfServiceSnap; snap != nil && ttl > 0 && time.Since(snap.at) < ttl {
			return snap
		}
		return nil
	}
	if snap := fresh(); snap != nil {
		return snap
	}
	v, _, _ := repman.selfServiceFlight.Do("snapshot", func() (any, error) {
		if ttl > 0 {
			if snap := fresh(); snap != nil {
				return snap, nil // computed by the flight we waited on
			}
		}
		snap := &selfServiceSnapshot{at: time.Now()}
		snap.capable, snap.reason = repman.selfServiceCapable()
		snap.pool = repman.infraUnitPool()
		snap.templates = repman.ListAppTemplates(nil)
		snap.neededDBU, snap.neededAPU = repman.selfServiceUnitsNeeded()
		if snap.pool.Known {
			snap.poolNote, snap.poolErr = repman.selfServicePoolCheck()
		}
		repman.selfServiceMu.Lock()
		repman.selfServiceSnap = snap
		repman.selfServiceMu.Unlock()
		return snap, nil
	})
	return v.(*selfServiceSnapshot)
}

// selfServiceScriptVerdict runs the enabled script for an identity, or serves the verdict
// taken within the TTL: the script is a process, it must not run per request. One run per
// identity at a time (single flight). A verdict is an allow or a veto of the script; a
// script that could not run at all (missing, not executable) refuses the creation but is
// not cached, so a repaired script answers at the next call.
func (repman *ReplicationManager) selfServiceScriptVerdict(identity string, used int, needDbu, needApu float64) error {
	script := strings.TrimSpace(repman.Conf.Cloud18SelfServiceClustersEnabledScript)
	if script == "" {
		return nil // no gate: nothing to run, nothing to cache
	}
	ttl := repman.selfServiceCacheTTL()
	if ttl > 0 {
		repman.selfServiceMu.Lock()
		v, ok := repman.selfServiceVerdicts[identity]
		repman.selfServiceMu.Unlock()
		if ok && v.script == script && time.Since(v.seen) < ttl {
			return v.err
		}
	}
	res, _, _ := repman.selfServiceFlight.Do("script:"+identity, func() (any, error) {
		err := repman.runSelfServiceEnabledScript(identity, used, needDbu, needApu)
		var veto *selfServiceVeto
		if ttl > 0 && (err == nil || errors.As(err, &veto)) {
			repman.selfServiceMu.Lock()
			if repman.selfServiceVerdicts == nil || len(repman.selfServiceVerdicts) >= selfServiceScriptCacheMax {
				repman.selfServiceVerdicts = map[string]selfServiceVerdict{}
			}
			repman.selfServiceVerdicts[identity] = selfServiceVerdict{seen: time.Now(), script: script, err: err}
			repman.selfServiceMu.Unlock()
		}
		return err, nil
	})
	if res == nil {
		return nil
	}
	return res.(error)
}

// selfServiceVeto is the enabled script's own refusal (non-zero exit, or no answer in
// time): a verdict, cached within the TTL like an allow.
type selfServiceVeto struct{ msg string }

func (v *selfServiceVeto) Error() string { return v.msg }

func (repman *ReplicationManager) selfServiceStatusFor(identity string) SelfServiceStatus {
	snap := repman.selfServiceSnapshotNow()
	used, names := repman.countSponsoredClusters(identity)
	remaining := repman.Conf.Cloud18SelfServiceMaxClustersPerUser - used
	if remaining < 0 {
		remaining = 0
	}
	st := SelfServiceStatus{
		Enabled: snap.capable, Reason: snap.reason, Orchestrator: repman.Conf.ProvOrchestrator,
		MaxPerUser: repman.Conf.Cloud18SelfServiceMaxClustersPerUser,
		Identity:   identity, Used: used, Clusters: names, Remaining: remaining,
		DefaultDBU: repman.Conf.ProvDbDbu, DefaultAPU: repman.Conf.ProvServicePlanApu, DefaultBKU: repman.Conf.ProvServicePlanBku,
		Pool: snap.pool, PoolOK: true,
		Domain: repman.Conf.Cloud18Domain, SubDomain: repman.Conf.Cloud18SubDomain, Zone: repman.Conf.Cloud18SubDomainZone,
		GatewayDomain: repman.Conf.PrimaryGatewayDomain(), AppTemplates: snap.templates,
		NeededDBU: snap.neededDBU, NeededAPU: snap.neededAPU,
	}
	if !st.Pool.Known {
		st.PoolNote = "infrastructure capacity unknown (no agent observed, no resource-manager-infra-* declared): the pool does not gate"
	} else if snap.poolErr != nil {
		st.PoolOK = false
		st.PoolNote = snap.poolErr.Error()
		if st.Enabled {
			st.Enabled = false
			st.Reason = snap.poolErr.Error()
		}
	} else if snap.poolNote != "" {
		st.Borrowed = true
		st.PoolNote = snap.poolNote
	}
	if st.Enabled {
		if err := repman.selfServiceScriptVerdict(identity, used, st.NeededDBU, st.NeededAPU); err != nil {
			st.Enabled = false
			st.Reason = err.Error()
		}
	}
	return st
}

// runSelfServiceEnabledScript is the client-overridable gate on a self-service creation
// (cloud18-self-service-clusters-enabled-script): run after the switch, the registration and
// the orchestrator checks, before the per-user limit and the pool. Argv carries the identity
// and the orchestrator; the pool figures ride env. A non-zero exit, or a timeout, vetoes the
// creation and the first output line (or the error) is the reason; an empty script allows.
// The script can only refuse more than the switch, never open what the switch closes.
func (repman *ReplicationManager) runSelfServiceEnabledScript(identity string, sponsored int, needDbu, needApu float64) error {
	script := strings.TrimSpace(repman.Conf.Cloud18SelfServiceClustersEnabledScript)
	if script == "" {
		return nil
	}
	pool := repman.infraUnitPool()
	borrowDbu, borrowApu := 0.0, 0.0
	if rm := repman.resourceManager; rm != nil {
		if l := rm.Ledger(); l.Known {
			borrowDbu, borrowApu = l.BorrowPot.Dbu, l.BorrowPot.Apu
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfServiceScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, script, identity, repman.Conf.ProvOrchestrator)
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	cmd.Env = append(os.Environ(),
		"REPMAN_IDENTITY="+identity,
		"REPMAN_ORCHESTRATOR="+repman.Conf.ProvOrchestrator,
		"REPMAN_SPONSORED_CLUSTERS="+strconv.Itoa(sponsored),
		"REPMAN_NEEDED_DBU="+f(needDbu),
		"REPMAN_NEEDED_APU="+f(needApu),
		"REPMAN_FREE_DBU="+f(pool.FreeDbu),
		"REPMAN_FREE_APU="+f(pool.FreeApu),
		"REPMAN_BORROW_DBU="+f(borrowDbu),
		"REPMAN_BORROW_APU="+f(borrowApu),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &selfServiceVeto{fmt.Sprintf("cloud18-self-service-clusters-enabled-script did not answer within %s: creation refused", selfServiceScriptTimeout)}
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		// the script did not run (missing, not executable): refused, but no verdict to cache
		return fmt.Errorf("cloud18-self-service-clusters-enabled-script could not run, creation refused: %w", err)
	}
	reason := strings.TrimSpace(string(out))
	if i := strings.IndexByte(reason, '\n'); i >= 0 {
		reason = strings.TrimSpace(reason[:i])
	}
	if reason == "" {
		reason = err.Error()
	}
	return &selfServiceVeto{fmt.Sprintf("cloud18-self-service-clusters-enabled-script refused the creation for %s: %s", identity, reason)}
}

// selfServiceScriptTimeout bounds the enabled-script: a hang is a veto, never a stuck login.
const selfServiceScriptTimeout = 30 * time.Second

// requestIdentity returns the caller's identity and whether it is an external
// SSO (Cloud18 / GitLab) identity rather than a local account.
func (repman *ReplicationManager) requestIdentity(r *http.Request) (identity string, sso bool) {
	claims, err := repman.GetJWTClaims(r)
	if err != nil {
		return "", false
	}
	// An API token issued by an SSO identity stays that identity (OwnerAuthType).
	return claims["User"], claims["AuthType"] == "SSO" || claims["OwnerAuthType"] == "SSO"
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
// With cloud18-self-service-clusters-can-borrow, a pool that cannot guarantee the units
// asks the over-commit pot instead (CanBorrow: capacity minus every plan minus what is
// already borrowed): the creation goes through on borrowed capacity and the note says so.
func (repman *ReplicationManager) selfServicePoolCheck() (borrowNote string, err error) {
	pool := repman.infraUnitPool()
	if !pool.Known {
		return "", nil
	}
	needDbu, needApu := repman.selfServiceUnitsNeeded()
	var short error
	if needDbu > pool.FreeDbu {
		short = fmt.Errorf("no free DBU on this infrastructure for a new cluster: %.0f DBU needed (2 × prov-db-dbu), %.1f free of %.1f usable (%.1f already planned)",
			needDbu, pool.FreeDbu, pool.UsableDbu, pool.PlannedDbu)
	} else if needApu > pool.FreeApu {
		short = fmt.Errorf("no free APU on this infrastructure for a new cluster: %.0f APU needed (prov-service-plan-apu), %.1f free of %.1f usable (%.1f already planned)",
			needApu, pool.FreeApu, pool.UsableApu, pool.PlannedApu)
	}
	if short == nil {
		return "", nil
	}
	if !repman.Conf.Cloud18SelfServiceClustersCanBorrow || repman.resourceManager == nil {
		return "", short
	}
	if ok, why := repman.resourceManager.CanBorrow(cluster.ProfileDatabase, needDbu); !ok {
		return "", fmt.Errorf("%s; cannot borrow either: %s", short, why)
	}
	if ok, why := repman.resourceManager.CanBorrow(cluster.ProfileCompute, needApu); !ok {
		return "", fmt.Errorf("%s; cannot borrow either: %s", short, why)
	}
	return fmt.Sprintf("created on borrowed capacity: %.0f DBU and %.0f APU not guaranteed (%s; cloud18-self-service-clusters-can-borrow)", needDbu, needApu, short), nil
}

// selfServiceBornDynamic makes a self-service cluster dynamic from its first save on the
// orchestrators that can resize live: config changes applied with SET GLOBAL and the
// resources following the load (prov-db-apply-dynamic-config, prov-db-dynamic-resource).
// The container cap follows the orchestrator: OpenSVC resizes the PG slice, so the docker
// run-args cap is dropped (or WARN0214 would stand); Kubernetes resizes the Pod in place
// only with a requests/limits pair, which that same switch declares. Other orchestrators
// are left as the instance defaults say.
func (repman *ReplicationManager) selfServiceBornDynamic(cl *cluster.Cluster) {
	switch cl.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		cl.Conf.ProvDBDockerRunArgsLimit = false
	case config.ConstOrchestratorKubernetes:
		cl.Conf.ProvDBDockerRunArgsLimit = true
	default:
		return
	}
	cl.Conf.ProvDBApplyDynamicConfig = true
	cl.Conf.ProvDBDynamicResource = true
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Self-service cluster born dynamic on %s: prov-db-apply-dynamic-config and prov-db-dynamic-resource on, prov-db-docker-run-args-limit=%t",
		cl.Conf.ProvOrchestrator, cl.Conf.ProvDBDockerRunArgsLimit)
}

// selfServiceCheck decides whether identity may create one more cluster here.
func (repman *ReplicationManager) selfServiceCheck(identity string) error {
	if ok, reason := repman.selfServiceCapable(); !ok {
		return errors.New(reason)
	}
	used, names := repman.countSponsoredClusters(identity)
	needDbu, needApu := repman.selfServiceUnitsNeeded()
	if err := repman.runSelfServiceEnabledScript(identity, used, needDbu, needApu); err != nil {
		return err
	}
	if used >= repman.Conf.Cloud18SelfServiceMaxClustersPerUser {
		return fmt.Errorf("%s already sponsors %d cluster(s) here (%s), the limit is %d per user (cloud18-self-service-max-clusters-per-user); drop one to free a slot",
			identity, used, strings.Join(names, ", "), repman.Conf.Cloud18SelfServiceMaxClustersPerUser)
	}
	_, err := repman.selfServicePoolCheck()
	return err
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

// ListAppTemplates is every app template this instance can deploy: the repository
// cache (prov-app-template-repo, refreshed lazily) and the cluster's local files
// (cl may be nil). Names are the template paths, "phpmyadmin/phpmyadmin".
func (repman *ReplicationManager) ListAppTemplates(cl *cluster.Cluster) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if cl != nil {
		if local, err := repman.GetAppTemplatesFromLocal(cl.Name); err == nil {
			for _, n := range local {
				add(n)
			}
		}
	}
	if list, err := repman.Conf.LoadAppTemplateListWithRefresh(false); err == nil {
		for _, n := range list {
			add(n)
		}
	}
	add("dummy")
	sort.Strings(out)
	return out
}

// ResolveAppTemplate maps a short app name to a template of the list: an exact
// name, "<name>/<name>", or the only template whose last path element is the name.
// "" when none matches.
func ResolveAppTemplate(name string, templates []string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	candidates := []string{}
	for _, t := range templates {
		lt := strings.ToLower(t)
		if lt == name || lt == name+"/"+name {
			return t
		}
		if strings.HasSuffix(lt, "/"+name) {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}
