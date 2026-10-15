// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/proxysql"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
)

func (cluster *Cluster) AddSeededServer(srv string) error {
	fmt.Printf("ADD SEEDED SERVER\n")

	if strings.Contains(cluster.Conf.Hosts, srv) {
		return errors.New("Server already exists")
	}
	if host, port, ok := strings.Cut(srv, ":"); ok {
		if err := cluster.RunAddMonitorScript(cluster.monitorHookDatabase(host, port, nil)); err != nil {
			return err
		}
	}

	hosts := strings.Split(cluster.Conf.Hosts, ",")

	//Remove empty slices
	n := 0
	for i := range hosts {
		if hosts[i] != "" {
			hosts[n] = hosts[i]
			n++
		}
	}
	hosts = hosts[:n]
	hosts = append(hosts, srv)

	newHosts := strings.Join(hosts, ",")

	cluster.StateMachine.SetFailoverState()
	cluster.SetDbServerHosts(newHosts)
	cluster.newServerList()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.TopologyDiscover(wg)
	wg.Wait()
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) AddDBTagConfig(tag string) {
	if !cluster.Configurator.HaveDBTag(tag) {
		cluster.Configurator.AddDBTag(tag)
		cluster.Conf.ProvTags = cluster.Configurator.GetConfigDBTags()
		cluster.SetClusterCredentialsFromConfig()
	}
}

// SetDBTagsConfig replaces the database tags of a cluster whose servers are not
// provisioned yet: a new child takes its parent's (#2013). Nothing to apply live and no
// restart: the provisioning renders them.
func (cluster *Cluster) SetDBTagsConfig(tags []string) {
	cluster.Configurator.SetDBTags(append([]string(nil), tags...))
	cluster.Conf.ProvTags = cluster.Configurator.GetConfigDBTags()
	cluster.SetClusterCredentialsFromConfig()
}

// addColumnstoreTag: the columnstore topology's servers (the UM) run ColumnStore,
// whose configuration is the mcs tag's ruleset (#2002), and load the replication plugin's
// checkpoints with LOAD DATA LOCAL INFILE, which the localinfile tag allows (#2015; the
// default security configuration sets local_infile = 0)
func (cluster *Cluster) addColumnstoreTag() {
	if !cluster.HasConfigTopoColumnstore() {
		return
	}
	for _, tag := range []string{"mcs", "localinfile"} {
		if !cluster.Configurator.HaveDBTag(tag) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Adding database tag %s for topology %s", tag, config.TopoColumnstore)
			cluster.AddDBTagConfig(tag)
		}
	}
}

// ErrColumnstoreNoDynamic: the columnstore topology turns down prov-db-dynamic-resource and
// prov-db-apply-dynamic-config, which size and tune InnoDB (buffer pool, thread pool, IO
// threads): a ColumnStore server keeps its plan's resources, its memory sized by ColumnStore
// from the container's cgroup (#2018)
var ErrColumnstoreNoDynamic = errors.New("the columnstore topology has no dynamic resource nor dynamic configuration: its servers keep the plan's resources")

// disableColumnstoreDynamic turns the dynamic resource and configuration off on the
// columnstore topology, at load and when the topology is chosen.
func (cluster *Cluster) disableColumnstoreDynamic() {
	if !cluster.HasConfigTopoColumnstore() || (!cluster.Conf.ProvDBDynamicResource && !cluster.Conf.ProvDBApplyDynamicConfig) {
		return
	}
	cluster.Conf.ProvDBDynamicResource = false
	cluster.Conf.ProvDBApplyDynamicConfig = false
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Topology %s: prov-db-dynamic-resource and prov-db-apply-dynamic-config off", config.TopoColumnstore)
}

// ColumnstoreMinDBU: a ColumnStore server runs both roles (#2021), 1 DBU for the UM side
// (the server, DML and DDL processing) and 1 for the PM side (PrimProc, WriteEngineServer).
const ColumnstoreMinDBU = 2

// raiseColumnstoreDBU raises prov-db-dbu to ColumnstoreMinDBU when the columnstore topology
// is chosen, through the plan change (its pot and script decide, the resources follow the
// ratio); before provisioning only, never the contract of a running cluster.
func (cluster *Cluster) raiseColumnstoreDBU() {
	if cluster.IsProvision || cluster.Conf.ProvDbDbu >= ColumnstoreMinDBU {
		return
	}
	if err := cluster.ChangePlanUnits(PlanUnitDBU, ColumnstoreMinDBU-cluster.Conf.ProvDbDbu); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Topology %s needs %d DBU per server, prov-db-dbu stays %d: %s", config.TopoColumnstore, ColumnstoreMinDBU, cluster.Conf.ProvDbDbu, err)
	}
}

func (cluster *Cluster) AddDBTag(tag string, dynamic bool) {

	if !cluster.Configurator.HaveDBTag(tag) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Adding database tag %s ", tag)
		cluster.AddDBTagConfig(tag)
		if cluster.Conf.ProvDBApplyDynamicConfig || dynamic {
			for _, srv := range cluster.Servers {
				cmd := "mariadb_command"
				if !srv.IsMariaDB() {
					cmd = "mysql_command"
				}
				srv.GetDatabaseConfig()
				_, needrestart := srv.ExecScriptSQL(strings.Split(srv.GetDatabaseDynamicConfig(tag, cmd), ";"))
				if needrestart {
					if tag == "nosplitpath" {
						srv.SetConfigPathCookie()
					} else {
						srv.SetRestartCookie()
					}
				}
			}
		} else {
			cluster.SetDBRestartCookie()
		}

		cluster.SetConfigChangeCookie()
	}

}

func (cluster *Cluster) AddProxyTag(tag string) {
	cluster.Configurator.AddProxyTag(tag)
	cluster.Conf.ProvProxTags = cluster.Configurator.GetConfigProxyTags()
	cluster.SetClusterCredentialsFromConfig()
	cluster.SetProxiesRestartCookie()
}

func (cluster *Cluster) AddSeededProxy(prx string, srv string, port string, user string, password string) error {
	if err := cluster.RunAddMonitorScript(cluster.monitorHookProxy(prx, srv, port, nil)); err != nil {
		return err
	}
	switch prx {
	case config.ConstProxyHaproxy:
		cluster.Conf.HaproxyOn = true
		if strings.Contains(cluster.Conf.HaproxyHosts, srv) {
			return errors.New("Proxy already exists")
		}
		// The flag default "127.0.0.1" is the on-premise local proxy, a placeholder for a
		// cluster created through the API (it inherits the global configuration): the first
		// real proxy REPLACES it, else the orchestrator refuses the name ("invalid name
		// 127.0.0.1 (rfc952)") and the real one is never provisioned (tamarin, 2026-10-07).
		if strings.TrimSpace(cluster.Conf.HaproxyHosts) == "127.0.0.1" {
			cluster.Conf.HaproxyHosts = ""
		}
		if cluster.Conf.HaproxyHosts != "" {
			cluster.Conf.HaproxyHosts = cluster.Conf.HaproxyHosts + "," + srv
		} else {
			cluster.Conf.HaproxyHosts = srv
		}
	case config.ConstProxyMaxscale:
		cluster.Conf.MxsOn = true
		cluster.Conf.MxsPort = port
		if strings.Contains(cluster.Conf.MxsHost, srv) {
			return errors.New("Proxy already exists")
		}
		if user != "" || password != "" {
			cluster.Conf.MxsUser = user
			cluster.Conf.MxsPass = password
		}
		if cluster.Conf.MxsHost != "" {
			cluster.Conf.MxsHost = cluster.Conf.MxsHost + "," + srv
		} else {
			cluster.Conf.MxsHost = srv
		}
	case config.ConstProxySqlproxy:
		cluster.Conf.ProxysqlOn = true
		cluster.Conf.ProxysqlPort = port
		if strings.Contains(cluster.Conf.ProxysqlHosts, srv) {
			return errors.New("Proxy already exists")
		}
		if user != "" || password != "" {
			cluster.Conf.ProxysqlUser = user
			cluster.Conf.ProxysqlPassword = password
		}

		if cluster.Conf.ProxysqlHosts != "" {
			cluster.Conf.ProxysqlHosts = cluster.Conf.ProxysqlHosts + "," + srv
		} else {
			cluster.Conf.ProxysqlHosts = srv
		}
	case config.ConstProxySpider:
		if strings.Contains(cluster.Conf.MdbsProxyHosts, srv+":"+port) {
			return errors.New("Proxy already exists")
		}
		if user != "" || password != "" {
			cluster.Conf.MdbsProxyCredential = user + ":" + password
		}
		cluster.Conf.MdbsProxyOn = true
		if cluster.Conf.MdbsProxyHosts != "" {
			cluster.Conf.MdbsProxyHosts = cluster.Conf.MdbsProxyHosts + "," + srv + ":" + port
		} else {
			cluster.Conf.MdbsProxyHosts = srv + ":" + port
		}
	}
	cluster.SetClusterProxyCredentialsFromConfig()
	cluster.StateMachine.SetFailoverState()
	cluster.Lock()
	cluster.newProxyList()
	cluster.Unlock()
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

type UserForm struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Roles    string `json:"roles"`
	Grants   string `json:"grants"`
}

func (cluster *Cluster) FilterGrants(grants string, delegator *APIUser) string {
	user := new(APIUser)
	cluster.SetUserGrants(user, grants)

	for grant, v := range delegator.Grants {
		if !v {
			user.Grants[grant] = false
		}
	}

	allow, _ := config.GetCompactGrants(user.Grants)

	return strings.Join(allow, " ")
}

func (cluster *Cluster) AppendGrants(grants string, user *APIUser) string {
	cluster.SetUserGrants(user, grants)
	allow, _ := config.GetCompactGrants(user.Grants)
	return strings.Join(allow, " ")
}

func (cluster *Cluster) AppendRoles(roles string, user *APIUser) string {
	// visitor is the default of a user without any other role (LoadAPIUsers):
	// kept next to a real role it would be saved in the entry and hide the
	// operator buttons (#1971). A real role drops it, and it is never added
	// next to one.
	for _, role := range strings.Fields(roles) {
		if role == config.RoleVisitor {
			continue
		}
		user.Roles[role] = true
		delete(user.Roles, config.RoleVisitor)
	}
	if len(user.Roles) == 0 && slices.Contains(strings.Fields(roles), config.RoleVisitor) {
		user.Roles[config.RoleVisitor] = true
	}

	return strings.Join(config.GetCompactRoles(user.Roles), " ")
}

func (cluster *Cluster) AddUser(userform UserForm, delegator string, reloadACL bool) error {
	return cluster.addUser(userform, delegator, reloadACL, true)
}

// AddSSOOnlyUser adds an account with no password: it can only act through an
// SSO (GitLab) login, never with a local password (isValidACL,
// IsLocalOnlyAccount). Used for self-service sponsors (issue #1838).
func (cluster *Cluster) AddSSOOnlyUser(userform UserForm, delegator string, reloadACL bool) error {
	userform.Password = ""
	return cluster.addUser(userform, delegator, reloadACL, false)
}

func (cluster *Cluster) addUser(userform UserForm, delegator string, reloadACL bool, generatePassword bool) error {
	user := userform.Username
	roles := userform.Roles
	grants := userform.Grants
	// Honour an explicitly-provided password (e.g. the derived system API key, or the
	// password secretLoginHandler already passes); only generate one when none is given.
	// Backward-compatible: callers that leave Password empty still get a random password,
	// unless the caller asked for a passwordless (SSO-only) account.
	pass := userform.Password
	if pass == "" && generatePassword {
		pass, _ = cluster.GeneratePassword()
	}

	if delegator != "admin" {
		duser, dok := cluster.APIUsers[delegator]
		if !dok {
			return fmt.Errorf("Delegator %s is not exist in cluster", delegator)
		}

		r := duser.Roles[config.RoleSysOps]
		if !r {
			grants = cluster.FilterGrants(grants, &duser)
		}
	}

	if _, ok := cluster.APIUsers[user]; ok {
		return fmt.Errorf("User %s already exist ", user)
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "User %s already exist ", user)
	} else {
		if cluster.Conf.GetDecryptedValue("api-credentials-external") == "" {
			cluster.Conf.APIUsersExternal = user + ":" + pass
		} else {
			cluster.Conf.APIUsersExternal = cluster.Conf.GetDecryptedValue("api-credentials-external") + "," + user + ":" + pass
		}
		var new_secret config.Secret
		new_secret.Value = cluster.Conf.APIUsersExternal
		new_secret.OldValue = cluster.Conf.GetDecryptedValue("api-credentials-external")
		cluster.Conf.Secrets["api-credentials-external"] = new_secret

		// Assign ACL before reloading
		new_acl := user + ":" + grants + ":" + cluster.Name
		if roles != "" {
			new_acl = new_acl + ":" + roles
		}

		if cluster.Conf.APIUsersACLAllowExternal == "" {
			cluster.Conf.APIUsersACLAllowExternal = new_acl
		} else {
			cluster.Conf.APIUsersACLAllowExternal = cluster.Conf.APIUsersACLAllowExternal + "," + new_acl
		}

		if reloadACL {
			cluster.LoadAPIUsers()
			cluster.SaveAcls()
			cluster.ConfigManager.SaveConfig(cluster, false)
		}

		msg := fmt.Sprintf("user %q added to cluster %q by delegator %q with grants [%s]", user, cluster.Name, delegator, grants)
		cluster.LogSecurity.Add(s18log.HttpMessage{
			Group:     cluster.Name,
			Level:     config.LvlInfo,
			Timestamp: time.Now().Format("2006/01/02 15:04:05"),
			Text:      msg,
			Module:    config.ConstLogModUncategorized,
		})
		if cluster.SecurityLogrus != nil {
			cluster.SecurityLogrus.WithField("user", user).WithField("delegator", delegator).Info(msg)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[security] %s", msg)
		}
	}

	return nil
}

func (cluster *Cluster) UpdateUser(userform UserForm, delegator string, reloadACL bool) error {
	list := cluster.Conf.APIUsersACLAllowExternal
	xlist := cluster.Conf.APIUsersACLDiscardExternal

	user := userform.Username
	roles := userform.Roles
	grants := userform.Grants

	auser, ok := cluster.APIUsers[user]

	if delegator != "admin" {
		duser, dok := cluster.APIUsers[delegator]
		if !dok {
			return fmt.Errorf("Delegator %s is not exist in cluster", delegator)
		}

		r := duser.Roles[config.RoleSysOps]
		if !r {
			// The entry below now persists (it is appended when missing), so a
			// delegator below sysops must not rewrite a sysops account (admin, the
			// Cloud18 git user): its change used to vanish at the next reload.
			if ok && auser.Roles[config.RoleSysOps] {
				return fmt.Errorf("Delegator %s is not sysops: unable to update sysops user %s", delegator, user)
			}
			grants = cluster.FilterGrants(grants, &duser)
		}
	}

	if !ok {
		return fmt.Errorf("User %s is not exist in cluster. Unable to update roles and grants", user)
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "User %s is not exist in cluster. Unable to update roles and grants", user)
	} else {
		user_acl := user + ":" + grants + ":" + cluster.Name
		if roles != "" {
			user_acl = user_acl + ":" + roles
		}

		// A user holding credentials without an entry of its own (admin, from the
		// main api-credentials, once a new cluster blanked its external ACL) gets
		// one appended: rewriting only an existing entry left the update in memory,
		// and the next LoadAPIUsers rebuilt the user as a visitor.
		new_acls := make([]string, 0)
		found := false
		for _, acl := range strings.Split(list, ",") {
			if acl == "" {
				continue
			}
			useracl, _, _, _ := misc.SplitAcls(acl)
			if useracl == user {
				acl = user_acl
				found = true
			}
			new_acls = append(new_acls, acl)
		}
		if !found {
			new_acls = append(new_acls, user_acl)
		}

		cluster.Conf.APIUsersACLAllowExternal = strings.Join(new_acls, ",")

		new_acls = make([]string, 0)
		for _, xacl := range strings.Split(xlist, ",") {
			if xacl == "" {
				continue
			}
			useracl, _, _, _ := misc.SplitAcls(xacl)
			if useracl != user {
				new_acls = append(new_acls, xacl)
			}
		}

		cluster.Conf.APIUsersACLDiscardExternal = strings.Join(new_acls, ",")

		cluster.SetUserGrants(&auser, grants)
		cluster.SetUserRoles(&auser, roles)
		cluster.APIUsers[user] = auser

		if reloadACL {
			cluster.LoadAPIUsers()
			cluster.SaveAcls()
			cluster.ConfigManager.SaveConfig(cluster, false)
		}

		msg := fmt.Sprintf("user %q updated in cluster %q by delegator %q with grants [%s]", user, cluster.Name, delegator, grants)
		cluster.LogSecurity.Add(s18log.HttpMessage{
			Group:     cluster.Name,
			Level:     config.LvlInfo,
			Timestamp: time.Now().Format("2006/01/02 15:04:05"),
			Text:      msg,
			Module:    config.ConstLogModUncategorized,
		})
		if cluster.SecurityLogrus != nil {
			cluster.SecurityLogrus.WithField("user", user).WithField("delegator", delegator).Info(msg)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[security] %s", msg)
		}
	}

	return nil
}

func (cluster *Cluster) DropUser(userform UserForm, reloadACL bool) error {
	user := userform.Username

	if _, ok := cluster.APIUsers[user]; !ok {
		return fmt.Errorf("User %s is not exist in cluster", user)
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "User %s is not exist in cluster. Unable to update roles and grants", user)
	} else {
		new_cred := make([]string, 0)
		list := strings.Split(cluster.Conf.APIUsersExternal, ",")
		for _, cred := range list {
			auser, _ := misc.SplitPair(cred)
			if auser != user {
				new_cred = append(new_cred, cred)
			}
		}

		delete(cluster.APIUsers, user)
		cluster.Conf.APIUsersExternal = strings.Join(new_cred, ",")
		if reloadACL {
			cluster.SaveAcls()
			cluster.ConfigManager.SaveConfig(cluster, false)
		}

		msg := fmt.Sprintf("user %q removed from cluster %q", user, cluster.Name)
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

	return nil
}

func (cluster *Cluster) AddShardingHostGroup(proxy *MariadbShardProxy) error {
	if cluster.Conf.ClusterHead != "" {
		return nil
	}
	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*ProxySQLProxy); ok {
			cluster.AddShardProxy(pr, proxy)
		}
	}
	return nil
}

func (cluster *Cluster) AddShardingQueryRules(schema string, table string) error {
	if cluster.Conf.ClusterHead != "" {
		return nil
	}
	for _, pri := range cluster.Proxies {
		if pr, ok := pri.(*ProxySQLProxy); ok {
			var qr proxysql.QueryRule
			var qrs []proxysql.QueryRule
			qr.Id = misc.Hash("dml." + schema + "." + table)
			qr.Active = 1
			qr.Match_Pattern.String = "SELECT|DELETE|UPDATE|INSERT|REPLACE .* " + table + " .*"
			qr.Apply = 1
			qr.DestinationHostgroup.Int64 = 999
			qrs = append(qrs, qr)
			pr.AddQueryRulesProxysql(qrs)
		}
	}
	return nil
}
