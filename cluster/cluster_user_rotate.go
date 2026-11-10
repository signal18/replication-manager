// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
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

// listHasUser: list (user:password,...) has an entry for user.
func listHasUser(list, user string) bool {
	for _, e := range strings.Split(list, ",") {
		if u, _, _ := strings.Cut(strings.TrimSpace(e), ":"); u == user {
			return true
		}
	}
	return false
}

// listWithPassword returns list with user's password set to pass, every other entry as it is.
func listWithPassword(list, user, pass string) (string, bool) {
	entries := strings.Split(list, ",")
	found := false
	for i, e := range entries {
		u, _, _ := strings.Cut(strings.TrimSpace(e), ":")
		if u == user {
			entries[i] = user + ":" + pass
			found = true
		}
	}
	return strings.Join(entries, ","), found
}

// APIUserScope says which list holds user's local password on this cluster, "" when none.
func (cluster *Cluster) APIUserScope(user string) string {
	if user == "" {
		return ""
	}
	if listHasUser(cluster.Conf.GetDecryptedValue(apiCredentialsKey), user) {
		return APIUserScopeInstance
	}
	if listHasUser(cluster.Conf.GetDecryptedValue("api-credentials-external"), user) {
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
	if u, ok := cluster.APIUsers[user]; !ok || u.Password == "" {
		return errors.New(user + " has no local password: it signs in with Cloud18")
	}
	if err := cluster.CredentialChain().CanSet(apiUserListKey(scope)); err != nil {
		return fmt.Errorf("%s cannot take a new value here: %s", apiUserListKey(scope), credstore.Reason(err))
	}
	return nil
}

// SetAPIUserPassword writes pass as user's password in the list of scope, through the
// credential chain (a new version where a store keeps versions), and reloads the accounts:
// a session carrying the old password is refused from the next request. Every other user
// of the list keeps its password.
func (cluster *Cluster) SetAPIUserPassword(user, scope, pass string) error {
	key := apiUserListKey(scope)
	list, found := listWithPassword(cluster.Conf.GetDecryptedValue(key), user, pass)
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

// DecryptedAPIUserList is the instance's list (api-credentials) with every password in clear,
// for the copy an infrastructure secret holds. Never logged (F10).
func (cluster *Cluster) DecryptedAPIUserList() string {
	entries := strings.Split(cluster.Conf.GetDecryptedValue(apiCredentialsKey), ",")
	for i, e := range entries {
		u, p, ok := strings.Cut(strings.TrimSpace(e), ":")
		if ok {
			entries[i] = u + ":" + cluster.Conf.GetDecryptedPassword(apiCredentialsKey, p)
		}
	}
	return strings.Join(entries, ",")
}

// WriteOpenSVCSecretKey writes one key of an OpenSVC secret object through this cluster's
// agent connection, bounded by the store timeout. The value is never logged (F10).
func (cluster *Cluster) WriteOpenSVCSecretKey(namespace, object, key, value string) error {
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		return errors.New("cluster " + cluster.Name + " is not on OpenSVC")
	}
	if cluster.openSVCStore().certNotLoaded() {
		return errOpenSVCCertNotLoaded
	}
	ctx, cancel := orchestratorCallContext(context.Background(), cluster.storeTimeout())
	defer cancel()
	svc := cluster.OpenSVCConnect()
	return svc.CreateSecretKeyValueCtx(ctx, namespace, object, key, value)
}
