// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
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
	switch prx {
	case config.ConstProxyHaproxy:
		cluster.Conf.HaproxyOn = true
		if strings.Contains(cluster.Conf.HaproxyHosts, srv) {
			return errors.New("Proxy already exists")
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
	for _, role := range strings.Split(roles, " ") {
		user.Roles[role] = true
	}

	return strings.Join(config.GetCompactRoles(user.Roles), " ")
}

func (cluster *Cluster) AddUser(userform UserForm, delegator string, reloadACL bool) error {
	user := userform.Username
	roles := userform.Roles
	grants := userform.Grants
	pass, _ := cluster.GeneratePassword()

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

	auser, ok := cluster.APIUsers[user]
	if !ok {
		return fmt.Errorf("User %s is not exist in cluster. Unable to update roles and grants", user)
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "User %s is not exist in cluster. Unable to update roles and grants", user)
	} else {
		new_acls := make([]string, 0)
		acls := strings.Split(list, ",")

		for _, acl := range acls {
			useracl, _, _, _ := misc.SplitAcls(acl)
			if useracl == user {
				acl = user + ":" + grants + ":" + cluster.Name
				if roles != "" {
					acl = acl + ":" + roles
				}
				new_acls = append(new_acls, acl)
			} else {
				new_acls = append(new_acls, acl)
			}
		}

		cluster.Conf.APIUsersACLAllowExternal = strings.Join(new_acls, ",")

		new_acls = make([]string, 0)
		acls = strings.Split(xlist, ",")
		for _, xacl := range acls {
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
