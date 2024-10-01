// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/auth/user"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

type UserResponse struct {
	Message string     `json:"msg"`
	User    *user.User `json:"user"`
}

func (repman *ReplicationManager) LoadAPIUsers(conf *config.Config) error {

	credentials := strings.Split(conf.Secrets["api-credentials"].Value+","+conf.Secrets["api-credentials-external"].Value, ",")
	for _, cred := range credentials {
		if cred == "" || cred == "<nil>" {
			continue
		}
		username, passwd := misc.SplitPair(cred)
		passwd = conf.GetDecryptedPassword("api-credentials", passwd)
		repman.Auth.LoadOrStoreUser(username, user.NewUser(username, passwd))
	}

	repman.LoadAPIUsersACL(conf)

	res, _ := json.Marshal(repman.Auth.Users)
	fmt.Printf("Users: " + string(res))

	return nil
}

func (repman *ReplicationManager) LoadAPIUsersACL(conf *config.Config) error {
	if conf.APIUsersACLAllow != "" {
		usersAllowACL := strings.Split(conf.APIUsersACLAllow, ",")
		for _, userACL := range usersAllowACL {
			useracl, listacls, cluster, role := misc.SplitACL(userACL)
			acls := strings.Split(listacls, " ")
			u, ok := repman.Auth.LoadUser(useracl)
			if ok {
				if cluster != "*" {
					u.SetClusterRole(cluster, role)
					u.SetClusterPermissions(cluster, acls, true)
				} else {
					for _, cl := range repman.Clusters {
						u.SetClusterRole(cl.Name, role)
						u.SetClusterPermissions(cl.Name, acls, true)
					}
				}
			}
		}
	}

	if conf.APIUsersACLDiscard != "" {
		usersDiscardACL := strings.Split(conf.APIUsersACLDiscard, ",")
		for _, userACL := range usersDiscardACL {
			useracl, listacls, cluster, _ := misc.SplitACL(userACL)
			acls := strings.Split(listacls, " ")
			u, ok := repman.Auth.LoadUser(useracl)
			if ok {
				if cluster != "*" {
					u.SetClusterPermissions(cluster, acls, false)
				} else {
					for _, cl := range repman.Clusters {
						u.SetClusterPermissions(cl.Name, acls, false)
					}
				}
			}
		}
	}

	return nil
}

func (repman *ReplicationManager) AddUser(cred user.UserForm) (*user.User, error) {
	_, ok := repman.Auth.LoadUser(cred.Username)
	if ok {
		return nil, fmt.Errorf("Error in request: User is already exists in this server")
	}

	u := user.NewUser(cred.Username, cred.Password)

	if cred.Clusters == "" {
		return nil, fmt.Errorf("Error in creating grants: clustername is not registered")
	}

	cList := make([]string, 0)
	if cred.Clusters == "*" {
		cList = repman.ClusterList
	} else {
		cList = strings.Split(cred.Clusters, ",")
	}

	for _, cl := range cList {
		mycluster := repman.getClusterByName(cl)
		if mycluster == nil {
			return nil, fmt.Errorf("Error in creating grants: clustername is not registered")
		}

		u.SetClusterRole(cl, cred.Role)

		if cred.Grants != "" {
			perms := strings.Split(cred.Grants, ",")
			u.SetClusterPermissions(cl, perms, true)
		}
	}

	repman.Auth.AddUser(cred.Username, u)
	return u, nil
}

func (repman *ReplicationManager) AddUserACL(cred user.UserForm, u *user.User) error {
	if cred.Clusters == "" {
		return fmt.Errorf("Error in creating grants: clustername is empty")
	}

	cList := make([]string, 0)
	if cred.Clusters == "*" {
		cList = repman.ClusterList
	} else {
		cList = strings.Split(cred.Clusters, ",")
	}

	for _, cl := range cList {
		mycluster := repman.getClusterByName(cl)
		if mycluster == nil {
			return fmt.Errorf("Error in creating grants: clustername is not registered")
		}

		if cred.Role != "" {
			u.SetClusterRole(cl, cred.Role)
		}

		if cred.Grants != "" {
			perms := strings.Split(cred.Grants, ",")
			u.SetClusterPermissions(cl, perms, true)
		}
	}

	return nil
}

func (repman *ReplicationManager) RevokeUserACL(cred user.UserForm, u *user.User) error {
	if cred.Clusters == "" {
		return fmt.Errorf("Error in revoking grants: clustername is empty")
	}

	cList := make([]string, 0)
	if cred.Clusters == "*" {
		cList = repman.ClusterList
	} else {
		cList = strings.Split(cred.Clusters, ",")
	}

	for _, cl := range cList {
		mycluster := repman.getClusterByName(cl)
		if mycluster == nil {
			return fmt.Errorf("Error in revoking grants: clustername is not registered")
		}

		if cred.Role != "" {
			u.SetClusterRole(cl, cred.Role)
		}

		if cred.Grants != "" {
			perms := strings.Split(cred.Grants, ",")
			u.SetClusterPermissions(cl, perms, false)
		}
	}

	return nil
}

func (repman *ReplicationManager) DropUserACL(clusters string, u *user.User) error {
	cList := make([]string, 0)
	if clusters == "" {
		return fmt.Errorf("Error in dropping ACL: clustername is empty")
	}
	if clusters == "*" {
		cList = repman.ClusterList
	} else {
		cList = strings.Split(clusters, ",")
	}

	for _, cl := range cList {
		mycluster := repman.getClusterByName(cl)
		if mycluster == nil {
			return fmt.Errorf("Error in dropping ACL: clustername is not registered")
		}

		u.DropClusterACL(mycluster.Name)
	}

	return nil
}
