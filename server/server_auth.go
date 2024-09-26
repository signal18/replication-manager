// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"strings"

	"github.com/signal18/replication-manager/auth/user"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (repman *ReplicationManager) LoadAPIUsers(cluster string, conf *config.Config) error {

	credentials := strings.Split(conf.Secrets["api-credentials"].Value+","+conf.Secrets["api-credentials-external"].Value, ",")
	for _, cred := range credentials {
		if cred == "" || cred == "<nil>" {
			continue
		}
		u := user.NewUser()
		u.User, u.Password = misc.SplitPair(cred)
		u.Password = conf.GetDecryptedPassword("api-credentials", u.Password)
		repman.Auth.Users.LoadOrStore(u.User, u)
	}

	repman.LoadAPIUsersACL(cluster, conf)

	return nil
}

func (repman *ReplicationManager) LoadAPIUsersACL(cluster string, conf *config.Config) error {
	usersAllowACL := strings.Split(conf.APIUsersACLAllow, ",")
	for _, userACL := range usersAllowACL {
		useracl, listacls := misc.SplitPair(userACL)
		acls := strings.Split(listacls, " ")
		u, ok := repman.Auth.Users.CheckAndGet(useracl)
		if ok {
			u.SetClusterPermissions(cluster, acls, true)
		}
	}

	usersDiscardACL := strings.Split(conf.APIUsersACLDiscard, ",")
	for _, userACL := range usersDiscardACL {
		useracl, listacls := misc.SplitPair(userACL)
		acls := strings.Split(listacls, " ")
		u, ok := repman.Auth.Users.CheckAndGet(useracl)
		if ok {
			u.SetClusterPermissions(cluster, acls, false)
		}
	}

	return nil
}
