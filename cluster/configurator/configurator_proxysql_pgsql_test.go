package configurator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The proxy moduleset writes the PostgreSQL proxysql.cnf only for a cluster
// tagged pgsql, with its query rules under pgsqlrwsplit; every other cluster
// keeps the MySQL one.
func TestProxySQLConfigPgsqlTags(t *testing.T) {
	c := &Configurator{}
	if err := c.LoadProxyModules(); err != nil {
		t.Fatal(err)
	}
	render := func(tags ...string) string {
		c.ProxyTags = tags
		dir := t.TempDir()
		if err := c.GenerateProxyConfig(dir, t.TempDir(), map[string]string{}, "test"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "init/etc/proxysql/proxysql.cnf"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, c := range []struct {
		tags           []string
		want, unwanted []string
	}{
		{nil, []string{"mysql_variables=", "mysql_servers ="}, []string{"pgsql_"}},
		{[]string{"readwritesplit"}, []string{"mysql_variables="}, []string{"pgsql_"}},
		{[]string{"pgsql"}, []string{"pgsql_variables=", "pgsql_servers =", "pgsql_users:"}, []string{"mysql_variables", "mysql_servers", "pgsql_query_rules"}},
		{[]string{"pgsql", "pgsqlrwsplit"}, []string{"pgsql_variables=", "pgsql_query_rules:"}, []string{"mysql_variables"}},
	} {
		cnf := render(c.tags...)
		for _, w := range c.want {
			if !strings.Contains(cnf, w) {
				t.Errorf("tags %v: proxysql.cnf lacks %q", c.tags, w)
			}
		}
		for _, u := range c.unwanted {
			if strings.Contains(cnf, u) {
				t.Errorf("tags %v: proxysql.cnf has %q", c.tags, u)
			}
		}
	}
}
