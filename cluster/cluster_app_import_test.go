package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	toml "github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// newTestAppWithRuntimeHostMismatch builds an App whose runtime host
// (app.Host / app.GetHost()) differs from its persisted host
// (app.AppConfig.AppHost) — the same divergence NewApp() produces when
// ProvNetCNI rewrites app.Host with a cluster-svc suffix for routing while
// AppConfig.AppHost (and app.Name, used by SaveApp() for the file name)
// stays pinned to the original persisted host.
func newTestAppWithRuntimeHostMismatch(persistedHost, runtimeHost, port string) *App {
	return &App{
		Name:  persistedHost,
		Host:  runtimeHost,
		Port:  port,
		Mutex: &sync.Mutex{},
		AppConfig: &config.AppConfig{
			AppHost:    persistedHost,
			AppPort:    port,
			Deployment: config.NewDeploymentConfig(),
		},
	}
}

func newTestClusterForAppImport(t *testing.T, name string) *Cluster {
	t.Helper()
	cl := newTestClusterForAddApp(t, name)
	cl.WorkingDir = t.TempDir()
	// Measurement-required fields (ProvAppMem/ProvAppDisk) fall back to these
	// cluster defaults in NewAppConfig; LoadAppConfig's measurement parser
	// rejects an app config missing both.
	cl.Conf.ProvMem = "256"
	cl.Conf.ProvDisk = "1"
	return cl
}

// exportTOMLForImport mirrors what handlerMuxAppPeerImportExport produces on
// the peer side: marshal a live AppConfig with the same toml package used by
// SaveAppConfigFile.
func exportTOMLForImport(t *testing.T, cl *Cluster, host, port string) string {
	t.Helper()
	appcnf := cl.NewAppConfig(host, port)
	data, err := toml.Marshal(appcnf)
	if err != nil {
		t.Fatalf("failed to marshal app config: %v", err)
	}
	return string(data)
}

func TestHasAppHostAndHasAppHostPort(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	app := newTestAppForAddApp("app1", "8080", "")
	cl.Apps = []*App{app}

	if !cl.HasAppHost("app1") {
		t.Fatalf("expected HasAppHost(app1) to be true")
	}
	if cl.HasAppHost("app2") {
		t.Fatalf("expected HasAppHost(app2) to be false")
	}
	if !cl.HasAppHostPort("app1", "8080") {
		t.Fatalf("expected HasAppHostPort(app1,8080) to be true")
	}
	if cl.HasAppHostPort("app1", "9090") {
		t.Fatalf("expected HasAppHostPort(app1,9090) to be false (different port)")
	}
}

func TestImportAppConfig_Success(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	content := exportTOMLForImport(t, cl, "peerapp1", "8080")

	if err := cl.ImportAppConfig("peerapp1", "8080", content); err != nil {
		t.Fatalf("ImportAppConfig failed: %v", err)
	}

	if !cl.HasAppHostPort("peerapp1", "8080") {
		t.Fatalf("expected imported app to be loaded into cluster.Apps")
	}

	filePath := filepath.Join(cl.WorkingDir, "apps", "peerapp1.toml")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected app config file to be written at %s: %v", filePath, err)
	}
}

func TestImportAppConfig_RejectsSameHostDifferentPort(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	cl.Apps = []*App{newTestAppForAddApp("app1", "8080", "")}

	content := exportTOMLForImport(t, cl, "app1", "9090")
	err := cl.ImportAppConfig("app1", "9090", content)
	if err == nil {
		t.Fatalf("expected ImportAppConfig to reject same-host/different-port import")
	}

	if _, statErr := os.Stat(filepath.Join(cl.WorkingDir, "apps", "app1.toml")); statErr == nil {
		t.Fatalf("expected no file to be written when import is rejected")
	}
}

// TestImportAppConfig_RollsBackOnLoadFailure exercises the LoadAppConfig
// append-then-fail path: LoadAppConfig appends to cluster.Conf.Apps before
// ParseConfigMeasurement can reject the config (missing prov-app-memory /
// prov-app-disk-size). ImportAppConfig must undo the write and the in-memory
// append on that failure — a rejected import must never leave the file on
// disk (which would block any retry) or a stale entry in cluster.Conf.Apps.
func TestImportAppConfig_RollsBackOnLoadFailure(t *testing.T) {
	// Deliberately no ProvMem/ProvDisk on this fixture: NewAppConfig will
	// produce an AppConfig with empty ProvAppMem/ProvAppDisk, which
	// ParseConfigMeasurement rejects as required-but-missing.
	cl := newTestClusterForAddApp(t, "c1")
	cl.WorkingDir = t.TempDir()

	content := exportTOMLForImport(t, cl, "badapp", "8080")

	err := cl.ImportAppConfig("badapp", "8080", content)
	if err == nil {
		t.Fatalf("expected ImportAppConfig to fail on missing measurement-required fields")
	}

	filePath := filepath.Join(cl.WorkingDir, "apps", "badapp.toml")
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected rollback to remove the written file, stat err=%v", statErr)
	}

	for _, a := range cl.Conf.Apps {
		if a.AppHost == "badapp" {
			t.Fatalf("expected rollback to remove the Conf.Apps entry for badapp, found: %+v", a)
		}
	}

	if cl.HasAppHost("badapp") {
		t.Fatalf("expected HasAppHost(badapp) to be false after rollback")
	}

	// A retry must fail for the same reason again, not be blocked by a
	// leftover file from the failed attempt.
	retryErr := cl.ImportAppConfig("badapp", "8080", content)
	if retryErr == nil {
		t.Fatalf("expected retry to fail again (still missing measurement fields)")
	}
	if retryErr.Error() == `app config file already exists for host "badapp"` {
		t.Fatalf("retry blocked by leftover file: rollback did not clean up: %v", retryErr)
	}
}

func TestImportAppConfig_RejectsMissingHostOrPort(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")

	if err := cl.ImportAppConfig("", "8080", "app-host = \"x\""); err == nil {
		t.Fatalf("expected error for missing host")
	}
	if err := cl.ImportAppConfig("host1", "", "app-host = \"x\""); err == nil {
		t.Fatalf("expected error for missing port")
	}
}

// TestImportAppConfig_RejectsPathTraversalHost guards the filePath :=
// filepath.Join(dirname, host+".toml") write in ImportAppConfig: host comes
// from a peer's inventory response over the network and must never be
// trusted to build a filesystem path without validation.
func TestImportAppConfig_RejectsPathTraversalHost(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")

	unsafeHosts := []string{
		"../../../etc/cron.d/evil",
		"..",
		".",
		"a/b",
		"a\\b",
		"",
	}
	for _, host := range unsafeHosts {
		content := exportTOMLForImport(t, cl, host, "8080")
		if err := cl.ImportAppConfig(host, "8080", content); err == nil {
			t.Fatalf("expected ImportAppConfig to reject unsafe host %q", host)
		}
	}

	// Confirm nothing escaped the apps directory.
	entries, err := os.ReadDir(filepath.Join(cl.WorkingDir, "apps"))
	if err == nil {
		for _, e := range entries {
			t.Fatalf("expected apps dir to stay empty, found %q", e.Name())
		}
	}
}

// TestImportAppConfig_RejectsHostPortIdentityMismatch covers a peer-import
// payload whose own app-host/app-port disagree with the request: since
// LoadAppConfig only overwrites app-host when it's empty/templated/unsafe
// (not merely different), a mismatched-but-safe value would otherwise be
// trusted, leaving apps/<requested-host>.toml on disk describing a different
// identity than its own file name — breaking the file-name-is-identity
// invariant HasAppHost/SaveApp depend on.
func TestImportAppConfig_RejectsHostPortIdentityMismatch(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")

	// TOML declares "other-host" but the caller requested "requested-host".
	mismatchedHostContent := exportTOMLForImport(t, cl, "other-host", "8080")
	if err := cl.ImportAppConfig("requested-host", "8080", mismatchedHostContent); err == nil {
		t.Fatalf("expected ImportAppConfig to reject a host mismatch between request and content")
	}
	if cl.HasAppHost("requested-host") || cl.HasAppHost("other-host") {
		t.Fatalf("expected no app to be registered after a host-mismatch rejection")
	}
	if _, statErr := os.Stat(filepath.Join(cl.WorkingDir, "apps", "requested-host.toml")); !os.IsNotExist(statErr) {
		t.Fatalf("expected rejected import to leave no file on disk, stat err=%v", statErr)
	}

	// TOML declares the right host but a different port than requested.
	mismatchedPortContent := exportTOMLForImport(t, cl, "portmismatch", "9999")
	if err := cl.ImportAppConfig("portmismatch", "8080", mismatchedPortContent); err == nil {
		t.Fatalf("expected ImportAppConfig to reject a port mismatch between request and content")
	}
	if cl.HasAppHost("portmismatch") {
		t.Fatalf("expected no app to be registered after a port-mismatch rejection")
	}
	if _, statErr := os.Stat(filepath.Join(cl.WorkingDir, "apps", "portmismatch.toml")); !os.IsNotExist(statErr) {
		t.Fatalf("expected rejected import to leave no file on disk, stat err=%v", statErr)
	}
}

// TestImportAppConfig_RejectsDedupSkipOrphan covers the case where
// tomlContent's declared identity already matches an app previously loaded
// under a *different* file name: LoadAppConfig's own dedup-skip logic
// returns nil without appending to Conf.Apps, which — before this check
// existed — would silently leave apps/<host>.toml on disk as an orphaned
// duplicate of an already-loaded app.
func TestImportAppConfig_RejectsDedupSkipOrphan(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	dirname := filepath.Join(cl.WorkingDir, "apps")
	if err := os.MkdirAll(dirname, 0750); err != nil {
		t.Fatalf("failed to create apps dir: %v", err)
	}

	// Pre-load "existing-app" under its own file, the normal way.
	preloadContent := exportTOMLForImport(t, cl, "existing-app", "8080")
	if err := os.WriteFile(filepath.Join(dirname, "existing-app.toml"), []byte(preloadContent), 0640); err != nil {
		t.Fatalf("failed to write preload app config: %v", err)
	}
	if err := cl.LoadAppConfig(dirname, "existing-app"); err != nil {
		t.Fatalf("failed to preload existing-app: %v", err)
	}

	// Now "import" a second file, requested under a different host, whose
	// content declares the same host:port as the one already loaded.
	dupContent := exportTOMLForImport(t, cl, "existing-app", "8080")
	if err := cl.ImportAppConfig("second-host", "8080", dupContent); err == nil {
		t.Fatalf("expected ImportAppConfig to reject content that dedups against an already-loaded app")
	}
	if _, statErr := os.Stat(filepath.Join(dirname, "second-host.toml")); !os.IsNotExist(statErr) {
		t.Fatalf("expected rejected import to leave no orphan file on disk, stat err=%v", statErr)
	}
}

// TestLoadAppConfig_UnsafeAppHostFallsBackToFilename covers the TOML-file
// load path (not just peer import): a hand-edited or otherwise tampered
// app-host value in an on-disk apps/*.toml must not be trusted verbatim,
// since it flows back into ImportAppConfig/SaveApp path joins on re-export.
func TestLoadAppConfig_UnsafeAppHostFallsBackToFilename(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	dirname := filepath.Join(cl.WorkingDir, "apps")
	if err := os.MkdirAll(dirname, 0750); err != nil {
		t.Fatalf("failed to create apps dir: %v", err)
	}

	tomlContent := "app-host = \"../../../etc/cron.d/evil\"\napp-port = \"8080\"\nprov-app-memory = \"256\"\nprov-app-disk-size = \"1\"\n"
	filename := filepath.Join(dirname, "safehost.toml")
	if err := os.WriteFile(filename, []byte(tomlContent), 0640); err != nil {
		t.Fatalf("failed to write app config: %v", err)
	}

	if err := cl.LoadAppConfig(dirname, "safehost"); err != nil {
		t.Fatalf("LoadAppConfig failed: %v", err)
	}

	// LoadAppConfig only appends to cluster.Conf.Apps (newAppList() is what
	// later populates cluster.Apps/HasAppHost), so assert against Conf.Apps.
	var loaded *config.AppConfig
	for _, a := range cl.Conf.Apps {
		if a.AppPort == "8080" {
			loaded = a
		}
	}
	if loaded == nil {
		t.Fatalf("expected app config to be loaded into cluster.Conf.Apps")
	}
	if loaded.AppHost != "safehost" {
		t.Fatalf("expected app-host to fall back to the safe filename \"safehost\", got %q", loaded.AppHost)
	}
}

// TestHasAppHost_UsesPersistedHostNotRuntimeHost is the ProvNetCNI mismatch
// case: when app.GetHost() (runtime, CNI-rewritten) differs from
// app.AppConfig.AppHost (persisted), the collision guards must key off the
// persisted identity — matching against the runtime host would silently
// miss a real collision (or flag a false one).
func TestHasAppHost_UsesPersistedHostNotRuntimeHost(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	app := newTestAppWithRuntimeHostMismatch("peerapp1", "peerapp1.c1.svc.k8s", "8080")
	cl.Apps = []*App{app}

	if app.GetHost() == app.AppConfig.AppHost {
		t.Fatalf("test fixture invalid: runtime host must differ from persisted host")
	}

	if !cl.HasAppHost("peerapp1") {
		t.Fatalf("expected HasAppHost to match the persisted host even though runtime host differs")
	}
	if cl.HasAppHost("peerapp1.c1.svc.k8s") {
		t.Fatalf("expected HasAppHost to NOT match the runtime-rewritten host")
	}
	if !cl.HasAppHostPort("peerapp1", "8080") {
		t.Fatalf("expected HasAppHostPort to match the persisted host+port")
	}
	if cl.HasAppHostPort("peerapp1.c1.svc.k8s", "8080") {
		t.Fatalf("expected HasAppHostPort to NOT match the runtime-rewritten host+port")
	}
}

// TestImportThenSave_UsesPersistedHostForFileName imports under ProvNetCNI,
// then runs a normal save cycle (SaveApp, as the monitoring loop would), and
// verifies exactly one file exists — named after the persisted host, never a
// second file named after the runtime-rewritten host.
func TestImportThenSave_UsesPersistedHostForFileName(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	cl.Conf.ProvNetCNI = true
	cl.Conf.ProvOrchestratorCluster = "k8s"

	content := exportTOMLForImport(t, cl, "peerapp1", "8080")
	if err := cl.ImportAppConfig("peerapp1", "8080", content); err != nil {
		t.Fatalf("ImportAppConfig failed: %v", err)
	}
	if len(cl.Apps) != 1 {
		t.Fatalf("expected exactly one app after import, got %d", len(cl.Apps))
	}

	app := cl.Apps[0]
	wantRuntimeHost := "peerapp1." + cl.Name + ".svc.k8s"
	if app.GetHost() != wantRuntimeHost {
		t.Fatalf("test fixture invalid: expected ProvNetCNI runtime host %q, got %q", wantRuntimeHost, app.GetHost())
	}
	if app.AppConfig.AppHost != "peerapp1" {
		t.Fatalf("expected persisted host to stay %q, got %q", "peerapp1", app.AppConfig.AppHost)
	}

	if _, err := cl.SaveApp(app, ""); err != nil {
		t.Fatalf("SaveApp failed: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(cl.WorkingDir, "apps"))
	if err != nil {
		t.Fatalf("failed to read apps dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "peerapp1.toml" {
		t.Fatalf("expected exactly one file apps/peerapp1.toml (persisted host), got: %v", names)
	}
}

// TestImportAppConfig_RollbackUsesPersistedHostUnderProvNetCNI proves the
// same-host collision guard still fires under ProvNetCNI: an existing local
// app persisted under host "app1" (whose runtime host is rewritten to
// "app1.c1.svc.k8s") must still block a peer import of persisted host
// "app1" on a different port. If the guard mistakenly compared against the
// runtime host instead, "app1" would never match "app1.c1.svc.k8s" and the
// unsafe same-host/different-port import would be silently accepted.
func TestImportAppConfig_RollbackUsesPersistedHostUnderProvNetCNI(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	cl.Conf.ProvNetCNI = true
	cl.Conf.ProvOrchestratorCluster = "k8s"

	existing := newTestAppWithRuntimeHostMismatch("app1", "app1.c1.svc.k8s", "8080")
	cl.Apps = []*App{existing}

	content := exportTOMLForImport(t, cl, "app1", "9090")
	if err := cl.ImportAppConfig("app1", "9090", content); err == nil {
		t.Fatalf("expected rejection: persisted host app1 already monitored (even though runtime hosts differ)")
	}

	if _, statErr := os.Stat(filepath.Join(cl.WorkingDir, "apps", "app1.toml")); statErr == nil {
		t.Fatalf("expected no file written for a rejected same-persisted-host import")
	}
}

// TestImportAppConfig_ConcurrentImportsDoNotCorruptConfApps drives many
// ImportAppConfig calls at once — some that succeed, some engineered to hit
// the host/port identity-mismatch rollback path — and checks that
// cluster.Conf.Apps ends up with exactly the successful entries and nothing
// else. Before ImportAppConfig held cluster.Lock() across its whole
// snapshot-load-verify sequence, a length-snapshot taken outside the lock
// could be invalidated by a concurrent append/rollback happening in the
// window between snapshot and verification, corrupting Conf.Apps (losing or
// duplicating entries) under -race.
func TestImportAppConfig_ConcurrentImportsDoNotCorruptConfApps(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			host := fmt.Sprintf("host%d", i)
			if i%2 == 0 {
				// Good import: content identity matches the request.
				content := exportTOMLForImport(t, cl, host, "8080")
				_ = cl.ImportAppConfig(host, "8080", content)
			} else {
				// Engineered mismatch: content declares a different host,
				// forcing the rollback path on every call.
				content := exportTOMLForImport(t, cl, host+"-wrong", "8080")
				_ = cl.ImportAppConfig(host, "8080", content)
			}
		}()
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, a := range cl.Conf.Apps {
		key := a.AppHost + ":" + a.AppPort
		if seen[key] {
			t.Fatalf("duplicate entry in Conf.Apps for %s", key)
		}
		seen[key] = true
	}

	for i := 0; i < n; i++ {
		host := fmt.Sprintf("host%d", i)
		want := i%2 == 0
		got := seen[host+":8080"]
		if got != want {
			t.Fatalf("host %s: expected present=%v, got present=%v (Conf.Apps=%v)", host, want, got, seen)
		}
		// A rejected mismatched import must never leave its declared
		// (wrong-host) identity behind either.
		if seen[host+"-wrong:8080"] {
			t.Fatalf("mismatched identity %s-wrong:8080 leaked into Conf.Apps", host)
		}
	}
}
