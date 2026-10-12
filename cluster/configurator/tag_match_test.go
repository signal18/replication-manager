package configurator

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// loadDBModule loads the database moduleset replication-manager ships.
func loadDBModule(t *testing.T) config.Compliance {
	t.Helper()
	b, err := os.ReadFile("../../share/opensvc/moduleset_mariadb.svc.mrm.db.json")
	if err != nil {
		t.Fatal(err)
	}
	var m config.Compliance
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A tag selects a filter only as its whole last segment (#1975).
func TestFilterMatchesTag(t *testing.T) {
	for _, c := range []struct {
		filter, tag string
		want        bool
	}{
		{"mariadb.security.ssl", "ssl", true},
		{"mariadb.security.strictssl", "ssl", false},
		{"mariadb.security.strictssl", "strictssl", true},
		{"mariadb.network.nobind", "bind", false},
		{"mariadb.replication.nologslavestatement", "statement", false},
		{"proxy.route.readonmaster", "readonmaster", true},
		{"ssl", "ssl", true},
		{"mariadb.security.ssl", "", false},
	} {
		if got := filterMatchesTag(c.filter, c.tag); got != c.want {
			t.Errorf("filterMatchesTag(%q, %q) = %v, want %v", c.filter, c.tag, got, c.want)
		}
	}
}

// The ssl tag renders the ssl file only, never strictssl's require_secure_transport.
func TestSSLTagDoesNotSelectStrictSSL(t *testing.T) {
	c := &Configurator{DBTags: []string{"ssl"}, DBModule: loadDBModule(t)}
	selected := map[string]bool{}
	for _, rule := range c.DBModule.Rulesets {
		if rule.Filter != "" && c.IsFilterInDBTags(rule.Filter) {
			selected[rule.Filter] = true
		}
	}
	if !selected["mariadb.security.ssl"] {
		t.Fatalf("the ssl tag must select mariadb.security.ssl: %v", selected)
	}
	if selected["mariadb.security.strictssl"] {
		t.Fatalf("the ssl tag must not select mariadb.security.strictssl: %v", selected)
	}
}

// Every variable of the module's cnf rulesets is replication-manager's, whatever the tags.
func TestTagManagedVariables(t *testing.T) {
	managed := (&Configurator{DBModule: loadDBModule(t)}).TagManagedVariables()
	for _, v := range []string{"ssl", "ssl_ca", "ssl_cert", "ssl_key", "require_secure_transport"} {
		if !managed[v] {
			t.Errorf("%s is rendered by a tag ruleset", v)
		}
	}
	if managed["my_custom_operator_var"] {
		t.Error("an operator variable is not managed")
	}
}
