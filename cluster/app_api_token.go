// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// App API token (#2158). A template that reads its own cluster from the replication-manager
// API references {{app.api.url}} and {{app.api.token}}: the URL is the one the app's sensor is
// given, the token is minted when the app is added (ServerGlobals.IssueAppAPIToken: owned by the
// cluster's admin user, read-only, scoped to the cluster), stored encrypted in the app setting
// app-api-token and substituted encrypted, so a secret-type variable receives it decrypted (as
// {{app.randompassword}}). It lives long (app-api-token-expire-days) and is renewed at
// provision when under appAPITokenRenewBefore remains; it is revoked when the app is removed.

// appAPITokenRenewBefore is the remaining lifetime under which a provision renews the token.
const appAPITokenRenewBefore = 90 * 24 * time.Hour

// appAPITokenRe matches the app's OWN key, not a sibling's ({{apps.#(...).api.token}}).
var appAPITokenRe = regexp.MustCompile(`\{\{\s*app\.api\.token\s*\}\}`)

func appAPITokenWanted(content []byte) bool {
	return appAPITokenRe.Match(content)
}

// ApplyAppAPIToken mints the app's token when it has none. An existing token is kept:
// re-adding or re-rendering an app never mints a second one. Without an encryption key the
// token would be saved in clear: refused.
func (cluster *Cluster) ApplyAppAPIToken(app *App) error {
	if app == nil || app.AppConfig == nil {
		return nil
	}
	app.Lock()
	has := strings.TrimSpace(app.AppConfig.AppAPIToken) != ""
	app.Unlock()
	if has {
		return nil
	}
	if cluster.ServerGlobals == nil || cluster.ServerGlobals.IssueAppAPIToken == nil {
		return errors.New("no API token store: the app cannot get {{app.api.token}}")
	}
	if !cluster.hasSecretKey() {
		return errors.New("no encryption key: the app's API token would be saved in clear, {{app.api.token}} refused")
	}
	// minted unlocked: the token store has its own lock and writes its file
	id, token, expires, err := cluster.ServerGlobals.IssueAppAPIToken(cluster.Name, app.AppConfig.AppHost)
	if err != nil {
		return err
	}
	app.Lock()
	setAppAPIToken(cluster.Conf, app.AppConfig, id, token, expires)
	app.Unlock()
	return nil
}

// hasSecretKey tells whether secrets are stored encrypted (GetEncryptedString returns its
// input unchanged without a key).
func (cluster *Cluster) hasSecretKey() bool {
	if cluster.Conf.SecretKey == nil {
		cluster.Conf.LoadEncrytionKey()
	}
	return cluster.Conf.SecretKey != nil
}

func setAppAPIToken(conf *config.Config, appcnf *config.AppConfig, id, token string, expires time.Time) {
	appcnf.AppAPIToken = conf.GetEncryptedString(token)
	appcnf.AppAPITokenID = id
	appcnf.AppAPITokenExpires = ""
	if !expires.IsZero() {
		appcnf.AppAPITokenExpires = expires.UTC().Format(time.RFC3339)
	}
}

// appAPITokenNeedsRenewal tells whether a token expiring at expires (RFC3339, empty = never)
// is due for renewal at now.
func appAPITokenNeedsRenewal(expires string, now time.Time) bool {
	if strings.TrimSpace(expires) == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return true // unreadable: renew rather than let it lapse unnoticed
	}
	return at.Sub(now) < appAPITokenRenewBefore
}

// RenewAppAPIToken replaces the app's token when it expires within appAPITokenRenewBefore:
// a new token is minted, the deployment variables holding the old one get the new one, the
// app config is saved, and only then the old one is revoked: a failed save keeps the old token
// valid (the saved config still holds it) and revokes the new one. It returns whether it renewed.
func (cluster *Cluster) RenewAppAPIToken(app *App) (bool, error) {
	if app == nil || app.AppConfig == nil {
		return false, nil
	}
	appcnf := app.AppConfig
	app.Lock()
	oldID, oldValue, oldExpires := appcnf.AppAPITokenID, appcnf.AppAPIToken, appcnf.AppAPITokenExpires
	app.Unlock()
	if strings.TrimSpace(oldID) == "" || !appAPITokenNeedsRenewal(oldExpires, time.Now()) {
		return false, nil
	}
	if cluster.ServerGlobals == nil || cluster.ServerGlobals.IssueAppAPIToken == nil || cluster.ServerGlobals.RevokeAppAPIToken == nil {
		return false, errors.New("no API token store: the app's token cannot be renewed")
	}
	if !cluster.hasSecretKey() {
		return false, errors.New("no encryption key: the app's API token is not renewed in clear")
	}
	id, token, expires, err := cluster.ServerGlobals.IssueAppAPIToken(cluster.Name, app.Name)
	if err != nil {
		return false, err
	}
	swap := func(fromValue, toID, toValue, toExpires string) {
		app.Lock()
		appcnf.AppAPIToken, appcnf.AppAPITokenID, appcnf.AppAPITokenExpires = toValue, toID, toExpires
		if d := appcnf.Deployment; d != nil {
			d.Mutex.Lock()
			for i := range d.Variables {
				if d.Variables[i].Value == fromValue {
					d.Variables[i].Value = toValue
				}
			}
			d.Mutex.Unlock()
		}
		app.Unlock()
	}
	newcnf := &config.AppConfig{}
	setAppAPIToken(cluster.Conf, newcnf, id, token, expires)
	swap(oldValue, id, newcnf.AppAPIToken, newcnf.AppAPITokenExpires)
	if _, err := cluster.SaveApp(app, ""); err != nil {
		swap(newcnf.AppAPIToken, oldID, oldValue, oldExpires)
		if rerr := cluster.ServerGlobals.RevokeAppAPIToken(id); rerr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: the unsaved API token %s is not revoked: %s", app.Name, id, rerr)
		}
		return false, fmt.Errorf("app %s: saving the renewed API token: %w", app.Name, err)
	}
	if err := cluster.ServerGlobals.RevokeAppAPIToken(oldID); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: the replaced API token %s is not revoked: %s", app.Name, oldID, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "App %s: API token renewed, valid until %s", app.Name, newcnf.AppAPITokenExpires)
	return true, nil
}

// WarnAppAPITokenExpiry logs a warning for each app whose token is due for renewal: renewal
// happens at provision, an app never provisioned again would lose its token unnoticed.
func (cluster *Cluster) WarnAppAPITokenExpiry() {
	for _, app := range cluster.Apps {
		if app == nil || app.AppConfig == nil {
			continue
		}
		app.Lock()
		id, expires := app.AppConfig.AppAPITokenID, app.AppConfig.AppAPITokenExpires
		app.Unlock()
		if strings.TrimSpace(id) != "" && appAPITokenNeedsRenewal(expires, time.Now()) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: its API token expires %s, a provision of the app renews it", app.Name, expires)
		}
	}
}

// revokeAppAPIToken revokes the token an app config holds, when it holds one: an app removed,
// or an add rolled back. A failure is logged; the token stays listed for a revoke by hand.
func (cluster *Cluster) revokeAppAPIToken(appcnf *config.AppConfig) {
	if appcnf == nil || strings.TrimSpace(appcnf.AppAPITokenID) == "" {
		return
	}
	if cluster.ServerGlobals == nil || cluster.ServerGlobals.RevokeAppAPIToken == nil {
		return
	}
	if err := cluster.ServerGlobals.RevokeAppAPIToken(appcnf.AppAPITokenID); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: its API token %s is not revoked: %s", appcnf.AppHost, appcnf.AppAPITokenID, err)
		return
	}
	appcnf.AppAPIToken, appcnf.AppAPITokenID, appcnf.AppAPITokenExpires = "", "", ""
}
