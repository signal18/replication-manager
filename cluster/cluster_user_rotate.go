// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// Where an API user's password lives: the instance's list (api-credentials, shared by every
// cluster) or the cluster's own list (api-credentials-external).
const (
	APIUserScopeInstance = "instance"
	APIUserScopeCluster  = "cluster"
)

// ErrAPIUserUnknown: the user is in neither list of this cluster.
var ErrAPIUserUnknown = errors.New("no such user with a local password on this cluster")

// apiUserListKey is the setting holding a scope's list.
func apiUserListKey(scope string) string {
	if scope == APIUserScopeInstance {
		return apiCredentialsKey
	}
	return "api-credentials-external"
}

// APIUserScope says which list holds user's local password on this cluster, "" when none.
func (cluster *Cluster) APIUserScope(user string) string {
	if user == "" {
		return ""
	}
	if config.APIUserListHas(cluster.Conf.GetDecryptedValue(apiCredentialsKey), user) {
		return APIUserScopeInstance
	}
	if config.APIUserListHas(cluster.Conf.GetDecryptedValue("api-credentials-external"), user) {
		return APIUserScopeCluster
	}
	return ""
}

// CanRotateAPIUserPassword says why user's password cannot be rotated here, nil when it can:
// `system` (its key is derived from the instance key), the registered Cloud18 user and the
// accounts without a local password (they sign in with Cloud18: rotate at the identity
// provider), or a list no store can take a new value of (set in a configuration file with
// no store keeping versions).
func (cluster *Cluster) CanRotateAPIUserPassword(user string) error {
	scope := cluster.APIUserScope(user)
	if scope == "" {
		return ErrAPIUserUnknown
	}
	if user == "system" {
		return errors.New("`system` has no password of its own: its key is derived from the instance key")
	}
	if cluster.Conf.Cloud18GitUser != "" && user == cluster.Conf.Cloud18GitUser {
		return errors.New("the registered Cloud18 user signs in with Cloud18: rotate its password at the identity provider")
	}
	if u, ok := cluster.GetAPIUsers()[user]; !ok || u.Password == "" {
		return errors.New(user + " has no local password: it signs in with Cloud18")
	}
	if scope == APIUserScopeInstance {
		// a server secret: the server's chain takes it, checked by the server
		return nil
	}
	if err := cluster.CredentialChain().CanSet(apiUserListKey(scope)); err != nil {
		return fmt.Errorf("%s cannot take a new value here: %s", apiUserListKey(scope), credstore.Reason(err))
	}
	return nil
}

// CanRotateFor says whether caller may rotate target's password: its own, or a user holding no
// grant the caller does not hold. The new password is returned to the caller, so rotating a
// user with more rights would hand them over.
func (cluster *Cluster) CanRotateFor(caller, target string) error {
	if caller == target {
		return nil
	}
	c, ok := cluster.GetAPIUsers()[caller]
	if !ok {
		return errors.New(caller + " is not a user of cluster " + cluster.Name)
	}
	t, ok := cluster.GetAPIUsers()[target]
	if !ok {
		return ErrAPIUserUnknown
	}
	for g, on := range t.Grants {
		if on && !c.Grants[g] {
			return fmt.Errorf("%s holds the grant %s that %s does not hold on %s", target, g, caller, cluster.Name)
		}
	}
	return nil
}

// ErrInvalidListPassword refuses a password a user:password list cannot hold.
var ErrInvalidListPassword = errors.New("a password in a user:password list cannot be empty nor hold ',', ':' or a space")

// ValidListPassword: pass can stand in a user:password list.
func ValidListPassword(pass string) error {
	if pass == "" || strings.ContainsAny(pass, ",: \t\n") {
		return ErrInvalidListPassword
	}
	return nil
}

// SetAPIUserPassword writes pass as user's password in the cluster's own list
// (api-credentials-external), through the credential chain (a new version where a store keeps
// versions), and reloads the accounts: a session carrying the old password is refused from the
// next request. Every other user of the list keeps its password. The instance's list is a
// server secret: the server changes it (SetServerSecret).
func (cluster *Cluster) SetAPIUserPassword(user, scope, pass string) error {
	if err := ValidListPassword(pass); err != nil {
		return err
	}
	if scope == APIUserScopeInstance {
		return credstore.ErrServerSecret
	}
	key := apiUserListKey(scope)
	list, found := config.APIUserListWithPassword(cluster.Conf.GetDecryptedValue(key), user, pass)
	if !found {
		return ErrAPIUserUnknown
	}
	if err := cluster.CredentialChain().Set(key, list); err != nil {
		return fmt.Errorf("%s not changed: %s", key, credstore.Reason(err))
	}
	cluster.MarkSecretVersionStoreDirty()
	cluster.LoadAPIUsers()
	return nil
}

// SetCredential writes key through the cluster's chain. A server secret is refused: it is the
// instance's, changed in the global settings by its owners (ErrServerSecret), never through
// one cluster's settings.
func (cluster *Cluster) SetCredential(key, value string) error {
	if cluster.isServerSecret(key) {
		return fmt.Errorf("%s: %w", key, credstore.ErrServerSecret)
	}
	return cluster.CredentialChain().Set(key, value)
}

// ApplyServerSecret has the cluster read key, a server secret the server just changed, from
// the server's chain, and use it: the global settings' fan-out. Nothing is written here.
func (cluster *Cluster) ApplyServerSecret(key string) {
	cluster.CredentialChain().Refresh()
	v := cluster.Conf.GetDecryptedValue(key)
	switch key {
	case "mail-smtp-password":
		cluster.Conf.MailSMTPPassword = v
		if cluster.Mailer != nil {
			cluster.Mailer.UpdateAuth(cluster.Conf.MailSMTPUser, v)
		}
	case "cloud18-gitlab-password":
		cluster.Conf.Cloud18GitPassword = v
	case "cloud18-domain-secret":
		cluster.Conf.Cloud18DomainSecret = v
	case "api-credentials":
		cluster.LoadAPIUsers()
	}
}
