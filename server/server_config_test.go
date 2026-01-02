package server

import (
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/viper"
	"os/user"
)

func TestEnvOverridesConfig(t *testing.T) {
	t.Setenv("REPLICATION_MANAGER_DEFAULT_API_PORT", "10001")

	repman := &ReplicationManager{}
	conf := config.Config{APIPort: "10000"}
	keys := []string{"api-port"}

	if err := repman.applyViperOverrides(&conf, envViperForScope("DEFAULT"), keys); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}

	if conf.APIPort != "10001" {
		t.Fatalf("expected api-port from env, got %q", conf.APIPort)
	}
}

func TestCLIOverridesEnv(t *testing.T) {
	t.Setenv("REPLICATION_MANAGER_DEFAULT_API_PORT", "10001")

	repman := &ReplicationManager{}
	conf := config.Config{APIPort: "10000"}
	keys := []string{"api-port"}

	if err := repman.applyViperOverrides(&conf, envViperForScope("DEFAULT"), keys); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}

	cli := viper.New()
	cli.Set("api-port", "10002")

	if err := repman.applyViperOverrides(&conf, cli, keys); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}

	if conf.APIPort != "10002" {
		t.Fatalf("expected api-port from CLI, got %q", conf.APIPort)
	}
}

func TestSkipConfigEnv(t *testing.T) {
	t.Setenv("REPLICATION_MANAGER_DEFAULT_SKIP_CONFIG", "true")

	envViper := envViperForScope("DEFAULT")
	if !envViper.GetBool("skip-config") {
		t.Fatal("expected skip-config to be true from env")
	}
}

func TestClusterEnvOverridesConfig(t *testing.T) {
	t.Setenv("REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_HOSTS", "db1,db2")

	repman := &ReplicationManager{}
	conf := config.Config{Hosts: "db0"}
	keys := []string{"db-servers-hosts"}

	if err := repman.applyViperOverrides(&conf, envViperForScope("cluster1"), keys); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}

	if conf.Hosts != "db1,db2" {
		t.Fatalf("expected db-servers-hosts from env, got %q", conf.Hosts)
	}
}

func TestFallbackWorkingDirForNonRoot(t *testing.T) {
	repman := &ReplicationManager{
		OsUser: &user.User{
			Uid:     "1000",
			HomeDir: "/home/tester",
		},
	}
	conf := config.Config{WorkingDir: "/var/lib/replication-manager"}
	defaultViper := viper.New()

	if repman.hasExplicitWorkingDir(defaultViper, false) {
		t.Fatal("expected no explicit working dir")
	}

	if repman.hasExplicitWorkingDir(defaultViper, true) {
		t.Fatal("expected no explicit working dir when skip-config is true")
	}

	configKeys := []string{}
	if err := repman.applyViperOverrides(&conf, envViperForScope("DEFAULT"), configKeys); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}
	if err := repman.applyViperOverrides(&conf, viper.GetViper(), repman.CommandLineFlag); err != nil {
		t.Fatalf("applyViperOverrides failed: %v", err)
	}

	if !repman.hasExplicitWorkingDir(defaultViper, false) {
		conf.WorkingDir = filepath.Join(repman.OsUser.HomeDir, ".local", "replication-manager", "data")
	}

	if conf.WorkingDir != "/home/tester/.local/replication-manager/data" {
		t.Fatalf("expected fallback working dir, got %q", conf.WorkingDir)
	}
}
