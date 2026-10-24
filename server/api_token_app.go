// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/signal18/replication-manager/config"
)

// App API token (#2158): an app whose template references {{app.api.token}} gets an API token
// minted at add, so it can read its own cluster without a token pasted by hand.
//   - owner: the cluster's admin user (the token shows in that user's API tokens);
//   - scope: the app's cluster only;
//   - grants: appAPITokenGrants, fixed here, read-only;
//   - lifetime: app-api-token-expire-days (long), renewed at provision when close to expiry.

// appAPITokenOwner is the cluster's admin user (AddLocalAdminUserACL, CheckDefaultUser).
const appAPITokenOwner = "admin"

// appAPITokenGrants is the token's grant set: read-only. The topology reads an app does
// (apps with their routes, servers, proxies) need no grant beyond a valid token in scope,
// and the token store embeds at least one grant: the route view is the one the app reads.
var appAPITokenGrants = config.GrantClusterShowRoutes

// issueAppAPIToken mints the API token of app appName of cluster clusterName.
func (repman *ReplicationManager) issueAppAPIToken(clusterName, appName string) (string, string, time.Time, error) {
	if !repman.apiTokensEnabled() {
		return "", "", time.Time{}, errors.New("API tokens are off (api-user-tokens): an app cannot get {{app.api.token}}")
	}
	days := repman.Conf.AppAPITokenExpireDays
	if days < 0 {
		return "", "", time.Time{}, fmt.Errorf("app-api-token-expire-days is %d: a lifetime in days, 0 for never", days)
	}
	if days == 0 {
		days = -1 // the token form reads 0 as the user default (120 days): 0 here means never
	}
	label := fmt.Sprintf("app %s/%s", clusterName, appName)
	// the store takes 64 bytes at most: cut on a character boundary, never inside a UTF-8 sequence
	for len(label) > 64 {
		_, size := utf8.DecodeLastRuneInString(label)
		label = label[:len(label)-size]
	}
	t, err := repman.createAPITokenAs(appAPITokenOwner, "Local", APITokenForm{
		Label:      label,
		Grants:     appAPITokenGrants,
		Clusters:   []string{clusterName},
		ExpireDays: days,
	}, "app "+clusterName+"/"+appName)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("API token of app %s/%s: %w", clusterName, appName, err)
	}
	return t.ID, t.Token, t.ExpiresAt, nil
}

// revokeAppAPIToken revokes an app's token as its owner. An unknown token (already removed
// from the store) is not an error: there is nothing left to revoke.
func (repman *ReplicationManager) revokeAppAPIToken(id string) error {
	if id == "" {
		return nil
	}
	if _, err := repman.revokeAPIToken(id, appAPITokenOwner, nil); err != nil && !errors.Is(err, errAPITokenNotFound) {
		return err
	}
	return nil
}

// refuseAPITokenOutOfScope answers 403 when the request carries an API token whose cluster
// scope excludes clusterName, and tells the caller to stop. The topology reads check no grant
// (any valid login reads them); a token is scoped, and an app's token must not read another
// cluster. A login (no API token) is not affected.
func (repman *ReplicationManager) refuseAPITokenOutOfScope(w http.ResponseWriter, r *http.Request, clusterName string) bool {
	t, ok := repman.parseAPITokenFromRequest(r)
	if !ok || t == nil || t.InScope(clusterName) {
		return false
	}
	http.Error(w, "this API token is not scoped to cluster "+clusterName, http.StatusForbidden)
	return true
}
