package cluster

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func TestAppAPITokenWanted(t *testing.T) {
	for content, want := range map[string]bool{
		`value = "{{app.api.token}}"`:                true,
		`value = "{{ app.api.token }}"`:              true,
		`value = "{{app.api.url}}"`:                  false, // the URL needs no token
		`value = "{{apps.#(name==kuma).api.token}}"`: false, // a sibling's: nothing to mint here
		`value = "{{app.randompassword}}"`:           false,
		`value = "plain"`:                            false,
	} {
		if got := appAPITokenWanted([]byte(content)); got != want {
			t.Fatalf("%s: wanted=%v, expected %v", content, got, want)
		}
	}
}

func TestAppAPITokenNeedsRenewal(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for expires, want := range map[string]bool{
		"": false, // never expires
		now.Add(400 * 24 * time.Hour).Format(time.RFC3339): false,
		now.Add(89 * 24 * time.Hour).Format(time.RFC3339):  true,
		now.Add(-time.Hour).Format(time.RFC3339):           true, // expired
		"not a date":                                       true,
	} {
		if got := appAPITokenNeedsRenewal(expires, now); got != want {
			t.Fatalf("%q: renew=%v, expected %v", expires, got, want)
		}
	}
}

// testKeyConf is a config with an encryption key, as a running replication-manager has: the
// generated secrets are then stored encrypted ("hash_…").
func testKeyConf() *config.Config {
	return &config.Config{SecretKey: []byte("0123456789abcdef0123456789abcdef")}
}

// fakeTokenStore stands for the server's token store behind ServerGlobals.
type fakeTokenStore struct {
	n       int
	revoked []string
	refuse  error
}

func (f *fakeTokenStore) globals(expires time.Time) *ServerGlobals {
	return &ServerGlobals{
		IssueAppAPIToken: func(clusterName, appName string) (string, string, time.Time, error) {
			if f.refuse != nil {
				return "", "", time.Time{}, f.refuse
			}
			f.n++
			return "id" + string(rune('0'+f.n)), "token-" + clusterName + "-" + appName + "-" + string(rune('0'+f.n)), expires, nil
		},
		RevokeAppAPIToken: func(id string) error {
			f.revoked = append(f.revoked, id)
			return nil
		},
	}
}

// Minted once, stored encrypted with its id and expiry, kept on a second call.
func TestApplyAppAPIToken(t *testing.T) {
	store := &fakeTokenStore{}
	expires := time.Now().Add(1825 * 24 * time.Hour)
	c := &Cluster{Name: "crm", Conf: testKeyConf(), ServerGlobals: store.globals(expires)}
	cnf := &config.AppConfig{AppHost: "status"}
	if err := c.ApplyAppAPIToken(cnf); err != nil {
		t.Fatal(err)
	}
	if cnf.AppAPITokenID != "id1" || cnf.AppAPITokenExpires == "" {
		t.Fatalf("id %q expires %q", cnf.AppAPITokenID, cnf.AppAPITokenExpires)
	}
	if cnf.AppAPIToken == "token-crm-status-1" {
		t.Fatal("the token is stored encrypted, never in clear")
	}
	if got := c.Conf.GetDecryptedPassword("app-api-token", cnf.AppAPIToken); got != "token-crm-status-1" {
		t.Fatalf("decrypted token %q", got)
	}
	first := cnf.AppAPIToken
	if err := c.ApplyAppAPIToken(cnf); err != nil || cnf.AppAPIToken != first || store.n != 1 {
		t.Fatal("an existing token is never replaced by an add")
	}
}

// Without a token store (no server), or when the store refuses, the add gets the error.
func TestApplyAppAPITokenRefused(t *testing.T) {
	c := &Cluster{Name: "crm", Conf: &config.Config{}}
	if err := c.ApplyAppAPIToken(&config.AppConfig{AppHost: "status"}); err == nil {
		t.Fatal("no token store: refused")
	}
	store := &fakeTokenStore{refuse: errors.New("admin has no token-create grant")}
	c.ServerGlobals = store.globals(time.Time{})
	cnf := &config.AppConfig{AppHost: "status"}
	if err := c.ApplyAppAPIToken(cnf); err == nil || cnf.AppAPIToken != "" {
		t.Fatal("a refused token leaves the app without one")
	}
}

// Close to expiry: a new token, the variable holding the old one updated, the old one revoked.
// Far from expiry: nothing changes.
func TestRenewAppAPIToken(t *testing.T) {
	store := &fakeTokenStore{}
	conf := testKeyConf()
	conf.WorkingDir = t.TempDir()
	c := &Cluster{Name: "crm", Conf: conf, ServerGlobals: store.globals(time.Now().Add(1825 * 24 * time.Hour))}
	cnf := &config.AppConfig{AppHost: "status", Deployment: config.NewDeploymentConfig()}
	setAppAPIToken(c.Conf, cnf, "old", "old-token", time.Now().Add(30*24*time.Hour))
	cnf.Deployment.Variables = config.VariableMaps{
		{Name: "REPMAN_API_TOKEN", Type: "secret", Value: cnf.AppAPIToken},
		{Name: "OTHER", Type: "env", Value: "x"},
	}
	app := &App{Name: "status", AppConfig: cnf, Mutex: &sync.Mutex{}}

	renewed, err := c.RenewAppAPIToken(app)
	if err != nil || !renewed {
		t.Fatalf("renewed=%v err=%v", renewed, err)
	}
	if cnf.AppAPITokenID != "id1" || len(store.revoked) != 1 || store.revoked[0] != "old" {
		t.Fatalf("id %q revoked %v", cnf.AppAPITokenID, store.revoked)
	}
	if v := cnf.Deployment.Variables[0].Value; v != cnf.AppAPIToken || c.Conf.GetDecryptedEmbedded("REPMAN_API_TOKEN", v) != "token-crm-status-1" {
		t.Fatal("the variable carries the new token")
	}
	if cnf.Deployment.Variables[1].Value != "x" {
		t.Fatal("other variables are left alone")
	}

	renewed, err = c.RenewAppAPIToken(app)
	if err != nil || renewed || store.n != 1 {
		t.Fatal("a token far from expiry is not renewed")
	}
}

// The wire view never carries the generated secrets; it carries the routes.
func TestAppAPIViewMasksSecretsAndShowsRoutes(t *testing.T) {
	c := &Cluster{Name: "crm", Conf: &config.Config{}}
	cnf := &config.AppConfig{AppHost: "status", Deployment: config.NewDeploymentConfig()}
	setAppAPIToken(c.Conf, cnf, "id1", "secret-token", time.Time{})
	cnf.AppRandomPassword = c.Conf.GetEncryptedString("secret-pass")
	cnf.Deployment.Routes = config.Routes{{CName: "status.example", Protocol: "https", DestinationPort: "3001", Monitor: &config.RouteMonitor{Path: "/api/entry-page", ExpectStatus: "200"}}}
	app := &App{Name: "status", AppConfig: cnf, Mutex: &sync.Mutex{}}
	b, err := json.Marshal(app.GetAppAPIView())
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Config map[string]interface{} `json:"config"`
		Routes []config.Route         `json:"routes"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Config["appApiToken"] != "*****" || out.Config["appRandomPassword"] != "*****" {
		t.Fatalf("secrets masked: token %v password %v", out.Config["appApiToken"], out.Config["appRandomPassword"])
	}
	if len(out.Routes) != 1 || out.Routes[0].Monitor == nil || out.Routes[0].Monitor.Path != "/api/entry-page" {
		t.Fatalf("routes %+v", out.Routes)
	}
}

// {{app.api.url}} and {{app.api.token}} resolve from the substitution view.
func TestAppAPISubstitution(t *testing.T) {
	conf := testKeyConf()
	conf.MonitorAddress, conf.APIPort = "repman.example", "10005"
	c := &Cluster{Name: "crm", Conf: conf}
	cnf := &config.AppConfig{AppHost: "status"}
	setAppAPIToken(c.Conf, cnf, "id1", "secret-token", time.Time{})
	app := &App{Name: "status", AppConfig: cnf, Mutex: &sync.Mutex{}, ClusterGroup: c}
	b, err := json.Marshal(map[string]interface{}{"app": app.buildAppSubstitutionView()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.ParseAppTemplate(`url = "{{app.api.url}}" token = "{{app.api.token}}"`, string(b))
	if err != nil {
		t.Fatal(err)
	}
	want := `url = "https://repman.example:10005" token = "` + cnf.AppAPIToken + `"`
	if out != want {
		t.Fatalf("got %s", out)
	}
	if c.Conf.GetDecryptedEmbedded("TOKEN", out) == out {
		t.Fatal("the substituted token decrypts at push")
	}
}
