package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/sirupsen/logrus"
)

func newConfigManagerForTest() *manager.ConfigManager {
	return manager.NewConfigManager(config.NewLogrusWrapper(&config.Config{}, logrus.New()))
}

// TestSetClusterSetting_ProvAppTemplateRepo verifies that the five
// prov-app-template-repo* keys are accepted and persisted by setClusterSetting.
func TestSetClusterSetting_ProvAppTemplateRepo(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	cases := []struct {
		setting string
		value   string
		check   func(*config.Config) bool
	}{
		{
			setting: "prov-app-template-repo",
			value:   "https://github.com/example/templates",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepo == "https://github.com/example/templates" },
		},
		{
			setting: "prov-app-template-repo-branch",
			value:   "main",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoBranch == "main" },
		},
		{
			setting: "prov-app-template-repo-user",
			value:   "gituser",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoUser == "gituser" },
		},
		{
			setting: "prov-app-template-repo-timeout",
			value:   "30",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoTimeout == 30 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			if err := repman.setClusterSetting(cl, tc.setting, tc.value); err != nil {
				t.Fatalf("setClusterSetting(%q): unexpected error: %v", tc.setting, err)
			}
			if !tc.check(cl.Conf) {
				t.Fatalf("setClusterSetting(%q) = %q: config field not updated", tc.setting, tc.value)
			}
		})
	}
}

// TestSetClusterSetting_ProvAppTemplateRepoPassword verifies password base64
// decode, assignment, and secret registration.
func TestSetClusterSetting_ProvAppTemplateRepoPassword(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	plain := "supersecret"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	if err := repman.setClusterSetting(cl, "prov-app-template-repo-password", encoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cl.Conf.ProvAppTemplateRepoPassword != plain {
		t.Errorf("expected password %q, got %q", plain, cl.Conf.ProvAppTemplateRepoPassword)
	}
	sec, ok := cl.Conf.Secrets["prov-app-template-repo-password"]
	if !ok {
		t.Fatal("secret not registered in Secrets map")
	}
	if sec.Value != plain {
		t.Errorf("secret.Value = %q, want %q", sec.Value, plain)
	}
}

// TestSetClusterSetting_ProvAppTemplateRepoPassword_InvalidBase64 verifies that
// an invalid base64 value is rejected with an error.
func TestSetClusterSetting_ProvAppTemplateRepoPassword_InvalidBase64(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	err := repman.setClusterSetting(cl, "prov-app-template-repo-password", "!!not-base64!!")
	if err == nil {
		t.Fatal("expected error for invalid base64 password, got nil")
	}
}

// TestSetRepmanSetting_ProvAppTemplateRepo verifies that the five
// prov-app-template-repo* keys are accepted and persisted by setRepmanSetting.
func TestSetRepmanSetting_ProvAppTemplateRepo(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
		ConfigManager: newConfigManagerForTest(),
	}

	cases := []struct {
		setting string
		value   string
		check   func(*config.Config) bool
	}{
		{
			setting: "prov-app-template-repo",
			value:   "https://github.com/org/global-templates",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepo == "https://github.com/org/global-templates" },
		},
		{
			setting: "prov-app-template-repo-branch",
			value:   "release",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoBranch == "release" },
		},
		{
			setting: "prov-app-template-repo-user",
			value:   "admin",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoUser == "admin" },
		},
		{
			setting: "prov-app-template-repo-timeout",
			value:   "60",
			check:   func(c *config.Config) bool { return c.ProvAppTemplateRepoTimeout == 60 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			if err := repman.setRepmanSetting(tc.setting, tc.value); err != nil {
				t.Fatalf("setRepmanSetting(%q): unexpected error: %v", tc.setting, err)
			}
			if !tc.check(repman.Conf) {
				t.Fatalf("setRepmanSetting(%q) = %q: config field not updated", tc.setting, tc.value)
			}
		})
	}
}

// TestSetRepmanSetting_ProvAppTemplateRepoPassword verifies password
// decoding, assignment, and secret registration.
func TestSetRepmanSetting_ProvAppTemplateRepoPassword(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
		ConfigManager: newConfigManagerForTest(),
	}

	plain := "globalpass"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	if err := repman.setRepmanSetting("prov-app-template-repo-password", encoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repman.Conf.ProvAppTemplateRepoPassword != plain {
		t.Errorf("expected %q, got %q", plain, repman.Conf.ProvAppTemplateRepoPassword)
	}
	sec, ok := repman.Conf.Secrets["prov-app-template-repo-password"]
	if !ok {
		t.Fatal("secret not registered in Secrets map")
	}
	if sec.Value != plain {
		t.Errorf("secret.Value = %q, want %q", sec.Value, plain)
	}
}

// TestSetRepmanSetting_ProvAppTemplateRepoPassword_InvalidBase64 rejects
// malformed base64.
func TestSetRepmanSetting_ProvAppTemplateRepoPassword_InvalidBase64(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
		ConfigManager: newConfigManagerForTest(),
	}
	err := repman.setRepmanSetting("prov-app-template-repo-password", "!!!bad!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

// TestSetClusterSetting_LegacyAppTemplateRepoKeysRejected confirms legacy
// app-template-global-repo-* keys are rejected by setClusterSetting.
func TestSetClusterSetting_LegacyAppTemplateRepoKeysRejected(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	legacyKeys := []string{
		"app-template-global-repo",
		"app-template-global-repo-branch",
		"app-template-global-repo-user",
		"app-template-global-repo-timeout",
	}
	for _, k := range legacyKeys {
		t.Run(k, func(t *testing.T) {
			err := repman.setClusterSetting(cl, k, "somevalue")
			if err == nil {
				t.Errorf("expected 'setting not found' error for legacy key, got nil")
			}
		})
	}
}

func TestSetClusterSetting_ProvAppTemplateRepo_OverrideDisabled_SingleKey(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.Conf.ProvAppTemplateRepoAllowOverride = false
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	err := repman.setClusterSetting(cl, "prov-app-template-repo", "https://github.com/example/override")
	if err == nil {
		t.Fatal("expected override rejection error, got nil")
	}
	if err.Error() != "cluster override is disabled for prov-app-template-repo* settings" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetClusterSetting_ProvAppTemplateRepo_OverrideDisabled_AllManagedKeys(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.Conf.ProvAppTemplateRepoAllowOverride = false
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	cases := []struct {
		setting string
		value   string
	}{
		{setting: "prov-app-template-repo", value: "https://github.com/example/override"},
		{setting: "prov-app-template-repo-branch", value: "main"},
		{setting: "prov-app-template-repo-user", value: "bot"},
		{setting: "prov-app-template-repo-password", value: base64.StdEncoding.EncodeToString([]byte("secret"))},
		{setting: "prov-app-template-repo-timeout", value: "30"},
	}

	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			err := repman.setClusterSetting(cl, tc.setting, tc.value)
			if err == nil {
				t.Fatal("expected override rejection error, got nil")
			}
			if err.Error() != "cluster override is disabled for prov-app-template-repo* settings" {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSetClusterSetting_ProvAppTemplateRepoTimeout_Invalid(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = make(map[string]config.Secret)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	err := repman.setClusterSetting(cl, "prov-app-template-repo-timeout", "0")
	if err == nil {
		t.Fatal("expected timeout validation error, got nil")
	}
}

func TestSetRepmanSetting_ProvAppTemplateRepoTimeout_Invalid(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
		ConfigManager: newConfigManagerForTest(),
	}

	err := repman.setRepmanSetting("prov-app-template-repo-timeout", "9999")
	if err == nil {
		t.Fatal("expected timeout validation error, got nil")
	}
}

func TestSetServerSetting_ProvAppTemplateRepo_GlobalDefaultDoesNotFanOut(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.ProvAppTemplateRepo = "https://github.com/example/cluster-local"

	repman := &ReplicationManager{
		Conf: &config.Config{
			Secrets:             make(map[string]config.Secret),
			ProvAppTemplateRepo: "https://github.com/example/default-before",
		},
		ConfigManager: newConfigManagerForTest(),
		Clusters: map[string]*cluster.Cluster{
			cl.Name: cl,
		},
	}

	err := repman.setServerSetting("", "/api/clusters/settings/actions/set/prov-app-template-repo/new", "prov-app-template-repo", "https://github.com/example/default-after")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repman.Conf.ProvAppTemplateRepo != "https://github.com/example/default-after" {
		t.Fatalf("repman default not updated: %q", repman.Conf.ProvAppTemplateRepo)
	}
	if cl.Conf.ProvAppTemplateRepo != "https://github.com/example/cluster-local" {
		t.Fatalf("cluster value should not be overwritten, got: %q", cl.Conf.ProvAppTemplateRepo)
	}
}

func TestSwitchServerSetting_ProvAppTemplateRepo_GlobalDefaultDoesNotFanOut(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.ProvAppTemplateRepo = "https://github.com/example/cluster-local"

	repman := &ReplicationManager{
		Conf: &config.Config{
			Secrets:             make(map[string]config.Secret),
			ProvAppTemplateRepo: "https://github.com/example/default-before",
		},
		ConfigManager: newConfigManagerForTest(),
		Clusters: map[string]*cluster.Cluster{
			cl.Name: cl,
		},
	}

	err := repman.switchServerSetting("", "/api/clusters/settings/actions/set/prov-app-template-repo/new", "prov-app-template-repo", "https://github.com/example/default-after")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repman.Conf.ProvAppTemplateRepo != "https://github.com/example/default-after" {
		t.Fatalf("repman default not updated: %q", repman.Conf.ProvAppTemplateRepo)
	}
	if cl.Conf.ProvAppTemplateRepo != "https://github.com/example/cluster-local" {
		t.Fatalf("cluster value should not be overwritten, got: %q", cl.Conf.ProvAppTemplateRepo)
	}
}

// TestHandlerMuxSetGlobalSettings_ProvAppTemplateRepoAllowlistedClusterDefaults
// verifies that the global endpoint scope guard explicitly allows the
// prov-app-template-repo* key family.
func TestHandlerMuxSetGlobalSettings_ProvAppTemplateRepoAllowlistedClusterDefaults(t *testing.T) {
	repman := &ReplicationManager{Clusters: map[string]*cluster.Cluster{}}

	cases := []struct {
		setting string
		value   string
	}{
		{setting: "prov-app-template-repo", value: "https://github.com/example/defaults"},
		{setting: "prov-app-template-repo-branch", value: "main"},
		{setting: "prov-app-template-repo-user", value: "bot"},
		{setting: "prov-app-template-repo-password", value: base64.StdEncoding.EncodeToString([]byte("pw"))},
		{setting: "prov-app-template-repo-timeout", value: "30"},
	}

	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/set/"+tc.setting+"/x", nil)
			req = setMuxVars(req, map[string]string{"settingName": tc.setting, "settingValue": tc.value})
			rr := httptest.NewRecorder()

			repman.handlerMuxSetGlobalSettings(rr, req)

			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("expected request to pass scope check and fail on missing cluster with 500, got %d", rr.Code)
			}
		})
	}
}

func TestHandlerMuxSetGlobalSettings_RejectsNonAllowlistedClusterScopedKey(t *testing.T) {
	repman := &ReplicationManager{Clusters: map[string]*cluster.Cluster{}}

	req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/set/prov-app-agents/value", nil)
	req = setMuxVars(req, map[string]string{"settingName": "prov-app-agents", "settingValue": "value"})
	rr := httptest.NewRecorder()

	repman.handlerMuxSetGlobalSettings(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d for non-allowlisted cluster key, got %d", http.StatusNotImplemented, rr.Code)
	}
}
