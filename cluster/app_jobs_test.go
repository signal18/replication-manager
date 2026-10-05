package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func jobsTestApp(engine string, volumes ...*config.Volume) (*Cluster, *App) {
	c := &Cluster{Name: "pgtest", Conf: &config.Config{ProvType: "docker"}}
	a := &App{Name: "pg1", ClusterGroup: c, AppConfig: &config.AppConfig{ProvAppConfigurator: engine, Deployment: config.NewDeploymentConfig()}}
	a.AppConfig.Deployment.Storages.Volumes = volumes
	return c, a
}

// A PostgreSQL app gets the client init container and the jobs sidecar, on the jobs
// directory of its first volume; an app without an engine jobs script gets neither.
func TestAppJobsSections(t *testing.T) {
	c, a := jobsTestApp("postgres", &config.Volume{Name: "pg1-drbd"})
	if !strings.Contains(appJobsScript(a), "pg_dumpall") || !strings.Contains(appJobsScript(a), "job needs") {
		t.Fatal("the PostgreSQL jobs script is embedded")
	}
	if start := appStartScript(a); !strings.Contains(start, "pg_basebackup") || !strings.Contains(start, "APP_CONFIGURATOR_SCRIPT") {
		t.Fatal("the PostgreSQL start script is embedded: configuration, standby seeding")
	}
	sections := map[string]map[string]string{"volume#1": {"name": "pg1-drbd", "directories": "data"}}
	n := 1
	c.openSVCAddAppJobsSections(sections, a, &n)
	if sections["volume#1"]["directories"] != "data jobs" {
		t.Fatalf("jobs directory added to the first volume: %q", sections["volume#1"]["directories"])
	}
	init, jobs := sections["container#02initjobs"], sections["container#jobs"]
	if init == nil || jobs == nil || n != 2 {
		t.Fatalf("init container and sidecar expected: %v", sections)
	}
	if init["detach"] != "false" || !strings.Contains(init["volume_mounts"], "pg1-drbd/jobs:/jobs") ||
		!strings.Contains(init["command"], "/static/configurator/bin/replication-manager-cli") || init["configs_environment"] != "env/REPLICATION_MANAGER_URL" {
		t.Fatalf("init container: %v", init)
	}
	if jobs["image"] != "{env.app_img}" || jobs["netns"] != "container#01" || jobs["configs_environment"] != "env/REPLICATION_MANAGER_URL pg1/*" || !strings.Contains(jobs["environment"], "REPLICATION_MANAGER_HOST_NAME={svcname}.{namespace}.svc.{clustername}") || jobs["secrets_environment"] != "pg1/*" ||
		!strings.Contains(jobs["volume_mounts"], "pg1-drbd/jobs:/jobs") || !strings.Contains(jobs["command"], "printenv APP_JOBS_SCRIPT") {
		t.Fatalf("jobs sidecar: %v", jobs)
	}
	// rendering twice does not duplicate the directory
	c.openSVCAddAppJobsSections(sections, a, &n)
	if sections["volume#1"]["directories"] != "data jobs" {
		t.Fatalf("directory duplicated: %q", sections["volume#1"]["directories"])
	}

	for name, app := range map[string]func() (*Cluster, *App){
		"no engine":               func() (*Cluster, *App) { return jobsTestApp("", &config.Volume{Name: "v"}) },
		"engine without script":   func() (*Cluster, *App) { return jobsTestApp("nosuchengine", &config.Volume{Name: "v"}) },
		"postgres without volume": func() (*Cluster, *App) { return jobsTestApp("postgres") },
	} {
		c, a := app()
		s := map[string]map[string]string{"volume#1": {"directories": "data"}}
		k := 1
		c.openSVCAddAppJobsSections(s, a, &k)
		if len(s) != 1 || k != 1 || s["volume#1"]["directories"] != "data" {
			t.Fatalf("%s: nothing must be added: %v", name, s)
		}
	}
}

// A database engine app takes the cluster's database credential and is never provisioned
// on the shipped default password.
func TestAppEngineCredential(t *testing.T) {
	c, a := jobsTestApp("postgres", &config.Volume{Name: "v"})
	if u, p := appEngineCredentialEnv(a); u != "POSTGRES_USER" || p != "POSTGRES_PASSWORD" {
		t.Fatalf("postgres superuser variables: %s %s", u, p)
	}
	c.Conf.Secrets = map[string]config.Secret{"db-servers-credential": {Value: "root:mariadb"}}
	if err := c.appEngineCredentialError(a); err == nil || !strings.Contains(err.Error(), "db-servers-credential") {
		t.Fatalf("the default password refuses the provisioning: %v", err)
	}
	c.Conf.Secrets = map[string]config.Secret{"db-servers-credential": {Value: "root:"}}
	if err := c.appEngineCredentialError(a); err == nil {
		t.Fatal("an empty password refuses the provisioning")
	}
	c.Conf.Secrets = map[string]config.Secret{"db-servers-credential": {Value: "root:Xy7-long-random-value"}}
	if err := c.appEngineCredentialError(a); err != nil {
		t.Fatalf("a set password is accepted: %v", err)
	}
	_, plain := jobsTestApp("", &config.Volume{Name: "v"})
	if u, p := appEngineCredentialEnv(plain); u != "" || p != "" {
		t.Fatal("an ordinary app carries its own variables")
	}
	c.Conf.Secrets = map[string]config.Secret{"db-servers-credential": {Value: "root:mariadb"}}
	if err := c.appEngineCredentialError(plain); err != nil {
		t.Fatalf("an ordinary app is not concerned: %v", err)
	}
}
