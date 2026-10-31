// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// issueGlobalSettingsTestJWT is the session JWT of a local login of user.
func issueGlobalSettingsTestJWT(t *testing.T, repman *ReplicationManager, user string) string {
	t.Helper()
	tok, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
	}{user, "Member", "enc"}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return tok
}

// #2262: a global setting is changed by the local admin account only: another local account, a
// Cloud18 (SSO) login or no login is refused before any change, with a 403.
func TestGlobalSettingsLocalAdminOnly(t *testing.T) {
	repman := &ReplicationManager{Clusters: map[string]*cluster.Cluster{}, Conf: &config.Config{TokenTimeout: 1, OAuthProvider: "https://gitlab.example"}, Logrus: log.New()}
	repman.initKeys()
	sso, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
		Email    string `json:"email"`
		Profile  string `json:"profile"`
	}{"person@acme.example", "Member", "", "person@acme.example", "https://gitlab.example"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		token string
		want  int
	}{
		"local admin":   {issueGlobalSettingsTestJWT(t, repman, "admin"), http.StatusInternalServerError}, // past the gate: no cluster
		"local viewer":  {issueGlobalSettingsTestJWT(t, repman, "viewer"), http.StatusForbidden},
		"Cloud18 login": {sso, http.StatusForbidden},
		"no login":      {"", http.StatusForbidden},
	} {
		for _, h := range []struct {
			route   string
			handler func(http.ResponseWriter, *http.Request)
		}{
			{"set", repman.handlerMuxSetGlobalSettings},
			{"switch", repman.handlerMuxSwitchGlobalSettings},
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/"+h.route+"/terminal-session-enabled/on", nil)
			req = setMuxVars(req, map[string]string{"settingName": "terminal-session-enabled", "settingValue": "true", "state": "on"})
			if c.token != "" {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
			rr := httptest.NewRecorder()
			h.handler(rr, req)
			if rr.Code != c.want {
				t.Errorf("%s, %s: got %d, want %d (%s)", name, h.route, rr.Code, c.want, rr.Body.String())
			}
			if marked := rr.Header().Get(adminRequiredHeader) == "true"; marked != (c.want == http.StatusForbidden) {
				t.Errorf("%s, %s: admin-required header %v on %d", name, h.route, marked, rr.Code)
			}
		}
	}
}

// #2262: a setting the immutable configuration sets (the configuration file, the command line, the
// environment) is never changed through the API, not even in memory; another one is.
func TestImmutableGlobalSettingRefused(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{Secrets: config.NewSecrets(nil)}, ConfigManager: newConfigManagerForTest()}
	repman.Conf.SetImmuableFlag("terminal-session-enabled", false)

	if err := repman.setServerSetting("admin", "/api/clusters/settings/actions/set", "terminal-session-enabled", "true"); !errors.Is(err, errImmutableGlobalSetting) {
		t.Fatalf("set of an immutable setting: %v", err)
	}
	if err := repman.switchServerSetting("admin", "/api/clusters/settings/actions/switch", "terminal-session-enabled", ""); !errors.Is(err, errImmutableGlobalSetting) {
		t.Fatalf("switch of an immutable setting: %v", err)
	}
	if repman.Conf.TerminalSessionEnabled {
		t.Fatal("the immutable setting changed in memory")
	}
	if err := repman.immutableGlobalSetting("terminal-session-resume"); err != nil {
		t.Fatalf("a setting the immutable configuration does not set: %v", err)
	}
}

// #2262: the MCP's cluster setting tools never change a global key (they would skip the admin
// gate and the immutable check); a cluster key goes on as before.
func TestClusterSettingToolsRefuseGlobalKeys(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	if err := repman.SetClusterSetting(nil, "terminal-session-enabled", "true"); !errors.Is(err, errGlobalSettingThroughCluster) {
		t.Errorf("set of a global key through a cluster tool: %v", err)
	}
	if err := repman.SwitchClusterSetting(nil, "terminal-session-enabled"); !errors.Is(err, errGlobalSettingThroughCluster) {
		t.Errorf("switch of a global key through a cluster tool: %v", err)
	}
	if !config.IsScope("terminal-session-enabled", "server") || config.IsScope("failover-mode", "server") {
		t.Fatal("scopes changed: the test keys no longer fit")
	}
}
