package cluster

import (
	"hash/crc64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestAppDbIdentifier(t *testing.T) {
	cases := map[string]string{"erp-backend": "erp_backend", "Valkey1": "valkey1", "1app": "app_1app", "--": "app",
		"a-very-long-application-name-that-exceeds-the-limit": "a_very_long_application_name_tha"}
	for in, want := range cases {
		if got := appDbIdentifier(in); got != want {
			t.Errorf("appDbIdentifier(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAppDbProvisionDecision(t *testing.T) {
	if err := appDbProvisionDecision(false, false, false, "s", "u"); err != nil {
		t.Fatalf("fresh objects must be allowed: %v", err)
	}
	if err := appDbProvisionDecision(true, true, true, "s", "u"); err != nil {
		t.Fatalf("owned objects must be allowed: %v", err)
	}
	for _, c := range []struct{ schema, user bool }{{true, false}, {false, true}, {true, true}} {
		if err := appDbProvisionDecision(false, c.schema, c.user, "s", "u"); err == nil {
			t.Errorf("existing schema=%v user=%v without ownership must be refused", c.schema, c.user)
		}
	}
}

func TestAppDbDefaultsAndTemplateKeys(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "dbapp.toml")
	os.MkdirAll(filepath.Dir(localPath), 0o755)
	template := "app-host = \"{{app.name}}\"\napp-port = \"8000\"\nprov-app-docker-img = \"frappe/erpnext:v16\"\n\n[deployment]\n" +
		"  [[deployment.variables]]\n    name = \"DB_NAME\"\n    type = \"env\"\n    value = \"{{app.db.schema}}\"\n" +
		"  [[deployment.variables]]\n    name = \"DB_USER\"\n    type = \"env\"\n    value = \"{{app.db.user}}\"\n" +
		"  [[deployment.variables]]\n    name = \"DB_PASSWORD\"\n    type = \"secret\"\n    value = \"{{app.db.password}}\"\n"
	os.WriteFile(localPath, []byte(template), 0o644)
	cluster := &Cluster{Name: "test-cluster", WorkingDir: workingDir, crcTable: crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{WorkingDir: workingDir, Apps: make([]*config.AppConfig, 0)}}
	if err := cluster.AddSeededApp("erp-backend", "8000", "", "dbapp"); err != nil {
		t.Fatalf("AddSeededApp: %v", err)
	}
	if len(cluster.Conf.Apps) != 1 {
		t.Fatalf("expected one app, got %d", len(cluster.Conf.Apps))
	}
	cnf := cluster.Conf.Apps[0]
	if !cnf.AppDbAutoCreate || cnf.AppDbSchema != "erp_backend" || cnf.AppDbUser != "erp_backend" || cnf.AppDbPass == "" {
		t.Fatalf("defaults not applied: auto=%v schema=%q user=%q pass set=%v", cnf.AppDbAutoCreate, cnf.AppDbSchema, cnf.AppDbUser, cnf.AppDbPass != "")
	}
	if cnf.AppDbOwned {
		t.Fatalf("ownership must only be set by a provision")
	}
	got := map[string]string{}
	for _, v := range cnf.Deployment.Variables {
		got[v.Name] = v.Value
	}
	if got["DB_NAME"] != "erp_backend" || got["DB_USER"] != "erp_backend" || got["DB_PASSWORD"] != cnf.AppDbPass || strings.Contains(got["DB_PASSWORD"], "{{") {
		t.Fatalf("template keys not substituted: %v", got)
	}
	// a sibling can reference the owner's database
	sibling := "app-host = \"{{app.name}}\"\napp-port = \"9000\"\nprov-app-docker-img = \"frappe/erpnext:v16\"\n\n[deployment]\n" +
		"  [[deployment.variables]]\n    name = \"DB_USER\"\n    type = \"env\"\n    value = \"{{apps.#(name==erp-backend).db.user}}\"\n"
	os.WriteFile(filepath.Join(workingDir, ".templates", "apps", "dbsib.toml"), []byte(sibling), 0o644)
	if err := cluster.AddSeededApp("erp-worker", "9000", "", "dbsib"); err != nil {
		t.Fatalf("AddSeededApp sibling: %v", err)
	}
	w := cluster.Conf.Apps[1]
	if w.AppDbAutoCreate {
		t.Fatalf("a sibling referencing another app's database must not ask for its own")
	}
	if w.Deployment.Variables[0].Value != "erp_backend" {
		t.Fatalf("sibling reference not substituted: %q", w.Deployment.Variables[0].Value)
	}
	// without a database request the keys stay unresolved and the add is refused
	os.WriteFile(filepath.Join(workingDir, ".templates", "apps", "nodb.toml"), []byte("app-host = \"{{app.name}}\"\napp-port = \"1\"\nprov-app-docker-img = \"x\"\n"), 0o644)
	if err := cluster.AddSeededApp("plain", "1", "", "nodb"); err != nil {
		t.Fatalf("plain app: %v", err)
	}
	if cluster.Conf.Apps[2].AppDbAutoCreate {
		t.Fatalf("plain app must not get a database")
	}
}
