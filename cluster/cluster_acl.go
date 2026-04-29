// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
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

func (cluster *Cluster) IsValidACL(strUser string, strPassword string, URL string, AuthMethod string) bool {
	if user, ok := cluster.APIUsers[strUser]; ok {
		if user.Password == cluster.Conf.GetDecryptedPassword("api-credentials", strPassword) || AuthMethod == "oidc" {
			// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL URL check for user %s ", strUser)
			return cluster.IsURLPassACL(strUser, URL, true)
		}
		return false
	}

	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ACL failed, user not found %s ", strUser)
	return false
}

func (cluster *Cluster) GetAPIUser(strUser string, strPassword string) (APIUser, error) {
	if user, ok := cluster.APIUsers[strUser]; ok {
		if user.Password == strPassword {
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
	credentials := strings.Split(cluster.Conf.Secrets["api-credentials"].Value+","+cluster.Conf.Secrets["api-credentials-external"].Value, ",")
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
			acls := strings.Split(discardACL.ACLs, " ")
			for _, acl := range acls {
				newapiuser.Grants[acl] = false
			}
		}

		// Apply main config ACLs last using strict override — main config is always authoritative
		// and cannot be escalated by cluster-level configuration.
		if userACL, ok := listACLs[newapiuser.User]; ok {
			cluster.SetUserGrantsStrict(&newapiuser, userACL.ACLs)
			cluster.SetUserRoles(&newapiuser, userACL.Roles)
		}

		if discardACL, ok := listDiscard[newapiuser.User]; ok {
			acls := strings.Split(discardACL.ACLs, " ")
			for _, acl := range acls {
				newapiuser.Grants[acl] = false
			}
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

	cluster.APIUsers = meUsers
	return nil
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

	msg := fmt.Sprintf("user %q granted: [%s]", u.User, strings.Join(granted, ", "))

	cluster.LogSecurity.Add(s18log.HttpMessage{
		Group:     cluster.Name,
		Level:     config.LvlInfo,
		Timestamp: time.Now().Format("2006/01/02 15:04:05"),
		Text:      msg,
	})

	if cluster.SecurityLogrus != nil {
		cluster.SecurityLogrus.WithField("user", u.User).Info(msg)
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
	case "/api/auth/callback":
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
	}

	// Configurator tag content and doc help — read-only, no grant required beyond auth
	if strings.HasPrefix(URL, "/api/clusters/"+cluster.Name+"/configurator/tags/") {
		return true
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
