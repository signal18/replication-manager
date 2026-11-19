// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "strings"

// credentialUserLabel is how a log line or a state names the user of a credential: by the
// credential's key, never by the user string (F10). The settings API refuses a value without
// its colon; one already stored that way (a config file, a vault, a version written before
// that check) is all user, so its "user" is the secret itself. The label protects those.
func credentialUserLabel(key string) string {
	return "<" + key + " user>"
}

// redactCredentialUsers replaces, in a message a database returned, the users of the
// monitoring and replication credentials by their labels. The database names the user in an
// access denied ("Access denied for user 'u'@'h'") and the replication in its IO error
// ("error connecting to master 'u@h:p'"); only those quoted forms are replaced, so a short user
// name elsewhere in the message is left as is. extraDbUsers are other users of the monitoring
// credential (a server's own copy of it).
func (cluster *Cluster) redactCredentialUsers(msg string, extraDbUsers ...string) string {
	if msg == "" || cluster == nil || cluster.Conf == nil || cluster.Conf.Secrets == nil {
		return msg
	}
	type credentialUser struct{ key, user string }
	users := []credentialUser{{"db-servers-credential", cluster.GetDbUser()}, {"replication-credential", cluster.GetRplUser()}}
	for _, u := range extraDbUsers {
		users = append(users, credentialUser{"db-servers-credential", u})
	}
	for _, c := range users {
		if c.user == "" {
			continue
		}
		label := credentialUserLabel(c.key)
		msg = strings.ReplaceAll(msg, "'"+c.user+"'@", "'"+label+"'@")
		msg = strings.ReplaceAll(msg, "'"+c.user+"@", "'"+label+"@")
	}
	return msg
}

// redactCredentialError is redactCredentialUsers for an error, "" for none.
func (cluster *Cluster) redactCredentialError(err error, extraDbUsers ...string) string {
	if err == nil {
		return ""
	}
	return cluster.redactCredentialUsers(err.Error(), extraDbUsers...)
}
