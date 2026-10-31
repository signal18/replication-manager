// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/subtle"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
	"google.golang.org/grpc/codes"
)

type APIUser struct {
	User       string          `json:"user"`
	Password   string          `json:"-"`
	GitToken   string          `json:"-"`
	GitUser    string          `json:"-"`
	IsExternal bool            `json:"isExternal"`
	Roles      map[string]bool `json:"roles"`
	Grants     map[string]bool `json:"grants"`
}

func (cluster *Cluster) SetUserGrants(u *APIUser, grant string) {
	if u.Grants == nil {
		u.Grants = map[string]bool{}
	}

	acls := strings.Split(grant, " ")
	for key, value := range cluster.Grants {
		found := false
		for _, acl := range acls {
			if strings.HasPrefix(key, acl) && acl != "" {
				found = true
				break
			}
		}

		_, ok := u.Grants[value]
		if !ok || found {
			u.Grants[value] = found
		}
	}
}

// SetUserGrantsStrict applies grants and always overwrites any previously set value.
// Used for the main config ACL which must take precedence over cluster-level overrides.
func (cluster *Cluster) SetUserGrantsStrict(u *APIUser, grant string) {
	if u.Grants == nil {
		u.Grants = map[string]bool{}
	}

	acls := strings.Split(grant, " ")
	for key, value := range cluster.Grants {
		found := false
		for _, acl := range acls {
			if strings.HasPrefix(key, acl) && acl != "" {
				found = true
				break
			}
		}
		u.Grants[value] = found
	}
}

func (cluster *Cluster) SetUserRoles(u *APIUser, roles string) {
	if u.Roles == nil {
		u.Roles = map[string]bool{}
	}

	list := strings.Split(roles, " ")

	for _, role := range cluster.Roles {
		if slices.Contains(list, role) {
			u.Roles[role] = true
		}
	}
}

func (u *APIUser) Granted(grant string) error {
	if value, ok := u.Grants[grant]; ok {
		if !value {
			return v3.NewErrorResource(codes.PermissionDenied, v3.ErrUserNotGranted, "user", u.User).Err()
		}
		return nil
	}

	return v3.NewErrorResource(codes.PermissionDenied, v3.ErrGrantNotFound, "grant not found", "").Err()
}

// setAPIUser sets one API user, copy on write (apiUsersMu): a new map is built and swapped in.
func (cluster *Cluster) setAPIUser(name string, u APIUser) {
	cluster.apiUsersMu.Lock()
	defer cluster.apiUsersMu.Unlock()
	m := make(map[string]APIUser, len(cluster.APIUsers)+1)
	for k, v := range cluster.APIUsers {
		m[k] = v
	}
	m[name] = u
	cluster.APIUsers = m
}

// deleteAPIUser removes one API user, copy on write (apiUsersMu).
func (cluster *Cluster) deleteAPIUser(name string) {
	cluster.apiUsersMu.Lock()
	defer cluster.apiUsersMu.Unlock()
	m := make(map[string]APIUser, len(cluster.APIUsers))
	for k, v := range cluster.APIUsers {
		if k != name {
			m[k] = v
		}
	}
	cluster.APIUsers = m
}

// IsValidACL authenticates strUser for URL. The auth model is keyed on whether
// the account carries a local password:
//
//   - password present  => LOCAL account. Authenticated by password only; SSO is
//     refused (a same-named GitLab identity must never ride a local account's
//     ACL). The registering owner (Cloud18GitUser) is the one SSO account that
//     legitimately carries a credential, so it is exempt.
//   - no password        => SSO identity. Authenticated by OIDC only; local
//     password auth is refused — an empty stored or submitted password must
//     never authenticate, closing the blank-password bypass.
//
// IsLocalOnlyAccount reports whether strUser is a local (password-protected)
// account that must authenticate by password only — SSO must never bind or
// authenticate it, or a same-named GitLab identity would ride its ACL. The
// registering owner (Cloud18GitUser) is exempt: it authenticates via SSO despite
// carrying a GitLab password. Single source of truth for the auth-model
// invariant, used by IsValidACL's OIDC branch.
func (cluster *Cluster) IsLocalOnlyAccount(strUser string) bool {
	user, ok := cluster.APIUsers[strUser]
	if !ok {
		return false
	}
	return user.Password != "" && strUser != cluster.Conf.Cloud18GitUser
}

// AuthMethodSubmittedPassword is the auth method of a password a client typed (/api/login,
// HTTP Basic): IsValidACL compares it as received, never decrypted.
const AuthMethodSubmittedPassword = "submitted-password"

func (cluster *Cluster) IsValidACL(strUser string, strPassword string, URL string, AuthMethod string) bool {
	return cluster.isValidACL(strUser, strPassword, URL, AuthMethod, true)
}

// IsValidACLQuiet behaves exactly like IsValidACL (same password re-verification,
// same grant check) but does not log denied checks. Intended for aggregate/probing
// callers, e.g. the global jobs dashboard, that deliberately test many clusters per
// request and would otherwise flood cluster logs with expected denials for clusters
// the caller can't see.
func (cluster *Cluster) IsValidACLQuiet(strUser string, strPassword string, URL string, AuthMethod string) bool {
	return cluster.isValidACL(strUser, strPassword, URL, AuthMethod, false)
}

func (cluster *Cluster) isValidACL(strUser string, strPassword string, URL string, AuthMethod string, errorPrint bool) bool {
	// An API token (issue #1835) carries no password: the server already verified
	// its signature and store record, and strUser is the token principal name that
	// GetACLUser resolves to the narrowed grant set. Only the URL ACL runs.
	if AuthMethod == "token" {
		if !IsTokenPrincipal(strUser) {
			return false
		}
		if _, ok := cluster.GetACLUser(strUser); !ok {
			return false
		}
		return cluster.IsURLPassACL(strUser, URL, errorPrint)
	}
	user, ok := cluster.APIUsers[strUser]
	if !ok {
		return false
	}

	if AuthMethod == "oidc" {
		// A password-protected account is local; SSO cannot authenticate it
		// (except the registering owner). Same rule the collision guard uses.
		if cluster.IsLocalOnlyAccount(strUser) {
			return false
		}
		return cluster.IsURLPassACL(strUser, URL, errorPrint)
	}

	// Local password auth: passwordless accounts are SSO-only, and a blank
	// submitted password never authenticates.
	if user.Password == "" || strPassword == "" {
		return false
	}
	// A session JWT carries the password encrypted ("password"); a password a client typed
	// (login, HTTP Basic) is compared as received: decrypting it would let an encrypted
	// value read from any JWT or saved configuration log in as its user.
	submitted := strPassword
	if AuthMethod != AuthMethodSubmittedPassword {
		submitted = cluster.Conf.GetDecryptedPassword("api-credentials", strPassword)
	}
	if subtle.ConstantTimeCompare([]byte(user.Password), []byte(submitted)) == 1 {
		return cluster.IsURLPassACL(strUser, URL, errorPrint)
	}
	return false
}

func (cluster *Cluster) GetAPIUser(strUser string, strPassword string) (APIUser, error) {
	if user, ok := cluster.APIUsers[strUser]; ok {
		// A passwordless (SSO-provisioned) account must never authenticate by a
		// blank local password (blank-password bypass).
		if user.Password == "" || strPassword == "" {
			return APIUser{}, fmt.Errorf("incorrect password")
		}
		if subtle.ConstantTimeCompare([]byte(user.Password), []byte(strPassword)) == 1 {
			return user, nil
		}
		return APIUser{}, fmt.Errorf("incorrect password")
	}

	return APIUser{}, fmt.Errorf("user not found")
}

func (cluster *Cluster) SaveUserAcls(user string) (string, string) {
	granted, discarded := config.GetCompactGrants(cluster.APIUsers[user].Grants)
	return strings.Join(granted, " "), strings.Join(discarded, " ")
}

func (cluster *Cluster) SaveUserRoles(user string) string {
	var aEnabledRoles []string
	for role, value := range cluster.APIUsers[user].Roles {
		if value {
			aEnabledRoles = append(aEnabledRoles, role)
		}
	}
	// Map iteration order is random: an unsorted role list makes the ACL
	// string differ between saves without any real change, flooding the
	// config event log with phantom acl-allow-external updates.
	sort.Strings(aEnabledRoles)
	return strings.Join(aEnabledRoles, " ")
}

func (cluster *Cluster) SaveAcls() {
	credentials := strings.Split(cluster.Conf.GetDecryptedValue("api-credentials")+","+cluster.Conf.GetDecryptedValue("api-credentials-external"), ",")
	aUserAcls := make([]string, 0)
	aUserDiscardAcls := make([]string, 0)
	aUserList := make([]string, 0)
	for _, credential := range credentials {
		user, _ := misc.SplitPair(credential)
		if _, ok := cluster.APIUsers[user]; !ok || slices.Contains(aUserList, user) {
			continue
		}
		aUserList = append(aUserList, user)
		enabledAcls, discardedAcls := cluster.SaveUserAcls(user)
		enabledRoles := cluster.SaveUserRoles(user)
		userACL := user + ":" + enabledAcls + ":" + cluster.Name
		if enabledRoles != "" {
			userACL = userACL + ":" + enabledRoles
		}
		aUserAcls = append(aUserAcls, userACL)

		if discardedAcls != "" {
			aUserDiscardAcls = append(aUserDiscardAcls, user+":"+discardedAcls)
		}
	}
	cluster.Conf.APIUsersACLAllowExternal = strings.Join(aUserAcls, ",")
	cluster.Conf.APIUsersACLDiscardExternal = strings.Join(aUserDiscardAcls, ",")
}

// func (cluster *Cluster) SetGrant(user string, grant string, enable bool) {
// 	if _, ok := cluster.APIUsers[user].Grants[grant]; ok {
// 		cluster.APIUsers[user].Grants[grant] = enable
// 	} else {
// 		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed grant not found for user %s, grant %s ", user, grant)
// 	}

//		cluster.SaveAcls()
//	}
type ListUserACL struct {
	User  string
	ACLs  string
	Roles string
}

func (cluster *Cluster) GetClusterUserAllowACLs(acls string) map[string]ListUserACL {
	results := make(map[string]ListUserACL)
	usersAllowACL := strings.Split(acls, ",")

	for _, userACL := range usersAllowACL {
		if userACL == "" {
			continue
		}

		acl := ListUserACL{}
		useracl, listacls, list1, list2 := misc.SplitAcls(userACL)
		slices1 := strings.Split(list1, " ")
		slices2 := strings.Split(list2, " ")

		if (list1 == "" && list2 == "") || slices.Contains(slices1, cluster.Name) || slices.Contains(slices2, cluster.Name) {
			acl.User = useracl
			acl.ACLs = listacls
			if slices.Contains(slices1, cluster.Name) && list2 != "" {
				acl.Roles = list2
			} else if slices.Contains(slices2, cluster.Name) && list1 != "" {
				acl.Roles = list1
			}

			results[useracl] = acl
		}
	}

	return results
}

func (cluster *Cluster) GetClusterUserDiscardACLs(acls string) map[string]ListUserACL {
	results := make(map[string]ListUserACL)
	usersDiscardACL := strings.Split(acls, ",")

	for _, userACL := range usersDiscardACL {
		if userACL == "" {
			continue
		}

		acl := ListUserACL{}
		useracl, listacls, _, listcluster := misc.SplitAcls(userACL)
		cluster_acls := strings.Split(listcluster, " ")

		if listcluster == "" || slices.Contains(cluster_acls, cluster.Name) {
			acl.User = useracl
			acl.ACLs = listacls

			results[useracl] = acl
		}
	}

	return results
}

func (cluster *Cluster) LoadAPIUsers() error {
	meUsers := make(map[string]APIUser)
	credentials := strings.Split(cluster.Conf.Secrets.Get("api-credentials").Value+","+cluster.Conf.Secrets.Get("api-credentials-external").Value, ",")
	listACLs := cluster.GetClusterUserAllowACLs(cluster.Conf.APIUsersACLAllow)
	listDiscard := cluster.GetClusterUserDiscardACLs(cluster.Conf.APIUsersACLDiscard)
	listACLsExt := cluster.GetClusterUserAllowACLs(cluster.Conf.APIUsersACLAllowExternal)
	listDiscardExt := cluster.GetClusterUserDiscardACLs(cluster.Conf.APIUsersACLDiscardExternal)

	for _, credential := range credentials {
		// Prevent empty credentials
		if credential == "" {
			continue
		}

		// Assign User Credentials
		var newapiuser APIUser
		newapiuser.User, newapiuser.Password = misc.SplitPair(credential)
		if _, ok := meUsers[newapiuser.User]; ok {
			continue
		}

		newapiuser.Password = cluster.Conf.GetDecryptedPassword("api-credentials", newapiuser.Password)
		newapiuser.Grants = make(map[string]bool)
		newapiuser.Roles = make(map[string]bool)

		// Apply cluster-level external ACLs first (lower priority)
		if userACL, ok := listACLsExt[newapiuser.User]; ok {
			cluster.SetUserGrants(&newapiuser, userACL.ACLs)
			cluster.SetUserRoles(&newapiuser, userACL.Roles)
		}

		if discardACL, ok := listDiscardExt[newapiuser.User]; ok {
			cluster.DiscardUserGrants(&newapiuser, discardACL.ACLs)
		}

		// Apply main config ACLs last using strict override — main config is always authoritative
		// and cannot be escalated by cluster-level configuration.
		if userACL, ok := listACLs[newapiuser.User]; ok {
			cluster.SetUserGrantsStrict(&newapiuser, userACL.ACLs)
			cluster.SetUserRoles(&newapiuser, userACL.Roles)
		}

		if discardACL, ok := listDiscard[newapiuser.User]; ok {
			cluster.DiscardUserGrants(&newapiuser, discardACL.ACLs)
		}

		// No Roles
		visitor := true
		for role, v := range newapiuser.Roles {
			if role == config.RoleVisitor {
				continue
			}
			if v {
				visitor = false
				break
			}
		}

		if visitor {
			newapiuser.Roles[config.RoleVisitor] = true
		}

		cluster.logUserGrantAssignment(newapiuser)
		meUsers[newapiuser.User] = newapiuser
	}

	// Computed on the fresh map, read by the tick (WARN0251) without touching APIUsers.
	issuer := false
	for name, u := range meUsers {
		if name != "system" && u.Grants[config.GrantTokenCreate] {
			issuer = true
			break
		}
	}
	cluster.apiTokenIssuer.Store(issuer)
	cluster.apiUsersMu.Lock()
	cluster.APIUsers = meUsers
	cluster.apiUsersMu.Unlock()
	// Every (re)load reinstates the persisted `system` password, which can be stale
	// (an older binary stored a random one before AddUser honoured an explicit password;
	// a SecretKey change would also drift it). Force it back to the current derived key
	// here so a config reload can never break the compute sensor's login.
	cluster.reconcileSystemServicePassword()
	cluster.reconcileSystemServiceGrants()
	return nil
}

// reconcileSystemServiceGrants sets the `system` service account to its grants exactly
// (SystemServiceGrants) on every load, in memory: a machine identity authenticating with a
// derived key holds what the sensors and the jobs need and nothing else, whatever an ACL
// string, an older secret-login ("db proxy", #1967) or the user-management GUI handed it; it
// never mints or manages bearer credentials (issue #1835).
func (cluster *Cluster) reconcileSystemServiceGrants() {
	u, ok := cluster.APIUsers["system"]
	if !ok || cluster.hasSystemServiceGrants(u) {
		return
	}
	u.Grants = cluster.systemServiceGrantMap() // a new map: readers hold the old one
	cluster.setAPIUser("system", u)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
		"Set the `system` service account to its grants (%s): a machine identity holds what the sensors and the jobs need, never tokens", SystemServiceGrants())
}

// checkAPITokenIssuers runs with the config states: WARN0251 while api-user-tokens
// is on and no user holds token-create on the cluster (#2083). It reads the flag
// LoadAPIUsers computes, never the APIUsers map that HTTP handlers write.
func (cluster *Cluster) checkAPITokenIssuers() {
	if !cluster.Conf.APIUserTokens || cluster.apiTokenIssuer.Load() {
		return
	}
	cluster.ConfigStateMachine.AddState("WARN0251", state.State{ErrType: "WARNING", ErrKey: "WARN0251", ErrFrom: "CONF",
		ErrDesc: clusterError["WARN0251"]})
}

// DiscardUserGrants removes a discard list from a user's grants. A full grant name
// removes that grant only (cluster-checksum keeps cluster-checksum-repair); any other
// name is a prefix like in the allow lists, so token removes token-create and
// token-manage, db every db-* grant (#2083).
func (cluster *Cluster) DiscardUserGrants(u *APIUser, discard string) {
	if u.Grants == nil {
		u.Grants = map[string]bool{}
	}
	for _, acl := range strings.Fields(discard) {
		if _, full := cluster.Grants[acl]; full {
			u.Grants[acl] = false
			continue
		}
		// A prefix removed nothing before 3.1.45 (#2083): an old entry such as "admin:db"
		// now takes every db-* grant, said once per load in the security log.
		var removed []string
		for key, value := range cluster.Grants {
			if strings.HasPrefix(key, acl) {
				if u.Grants[value] {
					removed = append(removed, value)
				}
				u.Grants[value] = false
			}
		}
		if len(removed) > 0 {
			sort.Strings(removed)
			cluster.logSecurity(u.User, fmt.Sprintf("user %q: discard %q removes the grants [%s]", u.User, acl, strings.Join(removed, ", ")))
		}
	}
}

// logUserGrantAssignment writes the resolved grant set for a user to the
// security log (both the HTTP buffer shown in the dashboard and SecurityLogrus
// which writes to security.log on disk).
func (cluster *Cluster) logUserGrantAssignment(u APIUser) {
	var granted []string
	for grant, enabled := range u.Grants {
		if enabled {
			granted = append(granted, grant)
		}
	}
	sort.Strings(granted)

	cluster.logSecurity(u.User, fmt.Sprintf("user %q granted: [%s]", u.User, strings.Join(granted, ", ")))
}

// logSecurity writes msg about user to the security log: the dashboard's buffer and
// security.log on disk (SecurityLogrus).
func (cluster *Cluster) logSecurity(user, msg string) {
	cluster.LogSecurity.Add(s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     config.LvlInfo,
		Timestamp: time.Now().Format("2006/01/02 15:04:05"),
		Text:      msg,
		Module:    config.ConstLogModUncategorized,
	})

	if cluster.SecurityLogrus != nil {
		cluster.SecurityLogrus.WithField("user", user).Info(msg)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[security] %s", msg)
	}
}

var dbloglist = []string{
	"/processlist",
	"/status-innodb",
	"/auditlog",
	"/errorlog",
	"/sqlerrorlog",
	"/slow-queries",
	"/query-response-time",
	"/meta-data-locks",
	"/digest-statements-pfs",
	"/digest-statements-slow",
	"/actions/toggle-sql-error-log",
	"/actions/toggle-server-audit-log",
	"/actions/toggle-query-response-time",
	"/actions/toggle-meta-data-locks",
	"/actions/toggle-slow-query-table",
	"/actions/toggle-slow-query-capture",
	"/actions/toggle-slow-query",
	"/actions/set-long-query-time",
	"/actions/toggle-pfs-slow-query",
	"/actions/toggle-innodb-monitor",
	"/actions/explain-pfs",
	"/actions/explain-slowlog",
}

func (cluster *Cluster) IsURLPassDatabasesACL(strUser string, URL string) bool {
	// Check against rule-based ACL system with improved logging
	if cluster.matchACLRules(strUser, URL, databaseACLRules) {
		return true
	}

	// Check database log access
	if cluster.checkDBLogAccess(strUser, URL) {
		return true
	}

	// Final fallback log (only if no rules matched)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL check failed for user %s : %s (no matching database rules)", strUser, URL)
	return false
}

func (cluster *Cluster) IsURLPassProxiesACL(strUser string, URL string) bool {
	// Check against rule-based ACL system with improved logging
	if cluster.matchACLRules(strUser, URL, proxyACLRules) {
		return true
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL proxy check failed for user %s : %s (no matching proxy rules)", strUser, URL)
	return false
}

func (cluster *Cluster) IsURLPassACL(strUser string, URL string, errorPrint bool) bool {
	// Public endpoints - no authentication required
	switch URL {
	case "/api/login":
		return true
	case "/api/clusters":
		return true
	case "/api/monitor":
		return true
	case "/api/health":
		return true
	case "/api/clusters/" + cluster.Name + "/actions/waitdatabases":
		return true
	case "/api/clusters/" + cluster.Name:
		return true
	case "/api/clusters/" + cluster.Name + "/diffvariables":
		return true
	case "/api/clusters/" + cluster.Name + "/opensvc-stats":
		return true
	case "/api/clusters/" + cluster.Name + "/actions/refresh-apps-template":
		return true
	case "/api/clusters/" + cluster.Name + "/topology/http-logs":
		return true
	case "/api/clusters/" + cluster.Name + "/price":
		// The cluster's month statement: read-only, for any authenticated user of the cluster.
		return true
	}

	// Configurator read-only endpoints — no specific grant required beyond auth.
	// Write endpoints under /configurator/ must be added to clusterACLRules.
	if strings.HasPrefix(URL, "/api/clusters/"+cluster.Name+"/configurator/") {
		if strings.HasSuffix(URL, "/content") {
			return true
		}
	}

	// Terminal ACL - special handling
	if strings.HasPrefix(URL, "/api/terminal") {
		if cluster.matchACLRules(strUser, URL, terminalACLRules) {
			return true
		}
	}

	// Check GLOBAL settings FIRST (before specific cluster routing)
	// Global settings URLs: /api/clusters/settings/... (not cluster-specific)
	// These require GrantGlobalSettings and should NOT fall through to cluster rules
	// Metering/pricing settings are decided by their exclusive rule, before any generic
	// settings table can grant them (see pricingACLRules).
	for _, rule := range pricingACLRules {
		if strings.Contains(URL, rule.URLPattern) {
			if cluster.matchACLRules(strUser, URL, []ACLRule{rule}) {
				return true
			}
			if errorPrint {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL pricing check failed for user %s : %s (requires %s)", strUser, URL, config.GrantSalesPricing)
			}
			return false
		}
	}
	if strings.HasPrefix(URL, "/api/clusters/settings") {
		return cluster.matchACLRules(strUser, URL, globalSettingsACLRules)
	}

	// Route to specialized ACL checks for SPECIFIC clusters
	// These URLs contain the cluster name: /api/clusters/{name}/...
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/servers") {
		return cluster.IsURLPassDatabasesACL(strUser, URL)
	}
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/proxies") {
		return cluster.IsURLPassProxiesACL(strUser, URL)
	}
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/apps") {
		return cluster.IsURLPassAppsACL(strUser, URL)
	}
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/templates/apps") {
		return cluster.IsURLPassAppsACL(strUser, URL)
	}

	// Check general cluster-level rules
	// Apply cluster rules if:
	// 1. URL contains this cluster's name: /api/clusters/{name}/... (cluster-specific)
	// 2. URL is /api/clusters/actions/... (global cluster operations like add/delete)
	// 3. URL doesn't start with /api/clusters/ at all (other endpoints)
	if strings.Contains(URL, "/api/clusters/"+cluster.Name+"/") ||
		(strings.HasPrefix(URL, "/api/clusters/") && !strings.Contains(URL, "/settings")) ||
		!strings.HasPrefix(URL, "/api/clusters/") {
		if cluster.matchACLRules(strUser, URL, clusterACLRules) {
			return true
		}
	}

	// Print error with no valid ACL
	if errorPrint {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL check failed for user %s : %s ", strUser, URL)
	}
	return false
}

func (cluster *Cluster) IsURLPassAppsACL(strUser string, URL string) bool {
	// App settings actions must only be evaluated against app settings rules.
	// This prevents fallback from /settings/actions/* to generic /actions/* rules.
	if strings.Contains(URL, "/settings/actions/") {
		settingsRules := make([]ACLRule, 0)
		for _, rule := range appACLRules {
			if strings.Contains(rule.URLPattern, "/settings/actions/") {
				settingsRules = append(settingsRules, rule)
			}
		}

		if cluster.matchACLRules(strUser, URL, settingsRules) {
			return true
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL app settings check failed for user %s : %s", strUser, URL)
		return false
	}

	// Check against rule-based ACL system with improved logging
	if cluster.matchACLRules(strUser, URL, appACLRules) {
		return true
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL app check failed for user %s : %s (no matching app rules)", strUser, URL)
	return false
}

// RouteViewerEmails lists, sorted, the users of the cluster that hold the read-only route
// grant (cluster-show-routes) and are known by an email: the people an app in front of the
// cluster (the status template's SSO, #2158) lets in. Local accounts without an email
// (admin, system) are not listed.
func (cluster *Cluster) RouteViewerEmails() []string {
	emails := make([]string, 0)
	for name, u := range cluster.APIUsers {
		if !u.Grants[config.GrantClusterShowRoutes] || !strings.Contains(name, "@") || IsTokenPrincipal(name) {
			continue
		}
		emails = append(emails, strings.ToLower(name))
	}
	sort.Strings(emails)
	return emails
}
