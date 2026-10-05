package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestAppRandomPasswordWanted(t *testing.T) {
	for content, want := range map[string]bool{
		`value = "{{app.randompassword}}"`:                                          true,
		`value = "{{ app.randompassword }}"`:                                        true,
		`value = "{{apps.#(name==pg1).randompassword}}"`:                            false, // a sibling's: nothing to generate here
		`value = "{{apps.#(config.provAppConfigurator==postgres).randompassword}}"`: false,
		`value = "{{app.db.password}}"`:                                             false,
		`value = "plain"`:                                                           false,
	} {
		if got := appRandomPasswordWanted([]byte(content)); got != want {
			t.Fatalf("%s: wanted=%v, expected %v", content, got, want)
		}
	}
}

// The password is generated once, stored encrypted, and kept on a second call.
func TestApplyAppRandomPassword(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{}}
	cnf := &config.AppConfig{AppHost: "pg1"}
	if err := c.ApplyAppRandomPassword(cnf); err != nil || cnf.AppRandomPassword == "" {
		t.Fatalf("generated: %q %v", cnf.AppRandomPassword, err)
	}
	first := cnf.AppRandomPassword
	plain := c.Conf.GetDecryptedPassword("app-random-password", first)
	if len(plain) < 16 || plain == defaultDatabasePassword {
		t.Fatalf("a real password is expected, got %d characters", len(plain))
	}
	if err := c.ApplyAppRandomPassword(cnf); err != nil || cnf.AppRandomPassword != first {
		t.Fatal("an existing password is never replaced")
	}
	other := &config.AppConfig{AppHost: "pg2"}
	c.ApplyAppRandomPassword(other)
	if c.Conf.GetDecryptedPassword("app-random-password", other.AppRandomPassword) == plain {
		t.Fatal("two apps get two passwords")
	}
}

func TestAppEngineSuperuser(t *testing.T) {
	a := &App{Name: "pg1", AppConfig: &config.AppConfig{ProvAppConfigurator: "postgres", Deployment: config.NewDeploymentConfig()}}
	if appEngineSuperuser(a) != "postgres" {
		t.Fatalf("default superuser: %s", appEngineSuperuser(a))
	}
	a.AppConfig.Deployment.Variables = append(a.AppConfig.Deployment.Variables, config.VariableMapping{Name: "POSTGRES_USER", Value: "admin"})
	if appEngineSuperuser(a) != "admin" {
		t.Fatalf("the template's own superuser: %s", appEngineSuperuser(a))
	}
	plain := &App{Name: "web", AppConfig: &config.AppConfig{}}
	if appEngineSuperuser(plain) != "" {
		t.Fatal("an ordinary app has no engine superuser")
	}
}

// Adoption conditions that need no live cluster: only a monitored engine app with a
// generated password, and only over the default (or no) cluster password.
func TestAdoptEngineAppCredentialRefusals(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{}}
	c.Conf.Secrets = map[string]config.Secret{"db-servers-credential": {Value: "root:mariadb"}}
	a := &App{Name: "pg1", Host: "pg1.c.svc.x", Port: "5432", AppConfig: &config.AppConfig{ProvAppConfigurator: "postgres", Deployment: config.NewDeploymentConfig()}}
	if c.adoptEngineAppCredential(a) {
		t.Fatal("no generated password, nothing to adopt")
	}
	c.ApplyAppRandomPassword(a.AppConfig)
	if c.adoptEngineAppCredential(a) {
		t.Fatal("the app is not a monitored server of the cluster: it is an app next to the database")
	}
	c.Servers = []*ServerMonitor{{Host: "pg1.c.svc.x", Port: "5432", ClusterGroup: c}}
	c.Conf.Secrets["db-servers-credential"] = config.Secret{Value: "root:SetByTheOwner123"}
	if c.adoptEngineAppCredential(a) {
		t.Fatal("a credential somebody set is never replaced")
	}
	if !strings.HasSuffix(c.Conf.Secrets["db-servers-credential"].Value, "SetByTheOwner123") {
		t.Fatal("the credential must be untouched")
	}
}

// The keys the PostgreSQL templates use resolve against the substitution object: the app's
// own generated password, the first PostgreSQL app's for a standby or a peer, and the
// first monitored server as the primary.
func TestRandomPasswordTemplateKeysResolve(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{}}
	sub := `{"app":{"name":"pg2","randompassword":"OWN"},` +
		`"apps":[{"name":"web","config":{"provAppConfigurator":""}},{"name":"pg1","config":{"provAppConfigurator":"postgres"},"randompassword":"FIRST"},{"name":"pg2","config":{"provAppConfigurator":"postgres"},"randompassword":"OWN"}],` +
		`"servers":[{"host":"pg1.c.svc.x","port":"5432"}]}`
	for key, want := range map[string]string{
		"{{app.randompassword}}": "OWN",
		"{{apps.#(config.provAppConfigurator==postgres).randompassword}}": "FIRST",
		"{{servers.0.host}}:{{servers.0.port}}":                           "pg1.c.svc.x:5432",
	} {
		got, err := c.ParseAppTemplate(key, sub)
		if err != nil || got != want {
			t.Fatalf("%s resolved to %q (%v), want %q", key, got, err, want)
		}
	}
}
