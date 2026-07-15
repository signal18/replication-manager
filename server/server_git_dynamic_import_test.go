package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// writeClusterDir creates a staged repo-root cluster directory containing a
// minimal <name>/<name>.toml, mimicking what a real dynamic cluster export
// looks like in the main config git repo.
func writeClusterDir(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	tomlPath := filepath.Join(dir, name+".toml")
	if err := os.WriteFile(tomlPath, []byte("[saved-"+name+"]\ntitle = \""+name+"\"\n"), 0644); err != nil {
		t.Fatalf("write %s: %v", tomlPath, err)
	}
}

func TestDiscoverImportableDynamicClusterDirs_ClassifiesCorrectly(t *testing.T) {
	stagedDir := t.TempDir()

	// Importable: has <name>/<name>.toml
	writeClusterDir(t, stagedDir, "cluster-a")
	writeClusterDir(t, stagedDir, "cluster-b")

	// Invalid: directory with no <name>/<name>.toml
	if err := os.MkdirAll(filepath.Join(stagedDir, "not-a-cluster"), 0755); err != nil {
		t.Fatal(err)
	}

	// Known non-cluster directories must never be classified at all.
	for _, d := range []string{".git", ".pull", ".tmp", "plugins", "graphite", "backups"} {
		if err := os.MkdirAll(filepath.Join(stagedDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// A root-level file must be ignored (only directories are candidates).
	if err := os.WriteFile(filepath.Join(stagedDir, "default.toml"), []byte("[saved-default]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := &ReplicationManager{}
	importable, invalid, err := repman.discoverImportableDynamicClusterDirs(stagedDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(importable)
	sort.Strings(invalid)

	if got, want := importable, []string{"cluster-a", "cluster-b"}; !equalStrSlices(got, want) {
		t.Errorf("importable = %v, want %v", got, want)
	}
	if got, want := invalid, []string{"not-a-cluster"}; !equalStrSlices(got, want) {
		t.Errorf("invalid = %v, want %v", got, want)
	}
}

func equalStrSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHasLocalCluster(t *testing.T) {
	workingDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workingDir, "on-disk-only"), 0755); err != nil {
		t.Fatal(err)
	}

	repman := &ReplicationManager{
		Conf: &config.Config{WorkingDir: workingDir},
		Clusters: map[string]*cluster.Cluster{
			"running-only": {},
		},
	}

	cases := []struct {
		name string
		want bool
	}{
		{"running-only", true},   // registered in repman.Clusters, no dir needed
		{"on-disk-only", true},   // dir exists in working-dir, not yet started
		{"missing-cluster", false},
	}

	for _, c := range cases {
		if got := repman.hasLocalCluster(c.name); got != c.want {
			t.Errorf("hasLocalCluster(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestImportDynamicClusterDir_CopiesAndRefusesOverwrite(t *testing.T) {
	stagedDir := t.TempDir()
	workingDir := t.TempDir()
	writeClusterDir(t, stagedDir, "cluster-a")

	repman := &ReplicationManager{Conf: &config.Config{WorkingDir: workingDir}}

	if err := repman.importDynamicClusterDir(stagedDir, "cluster-a"); err != nil {
		t.Fatalf("unexpected error importing: %v", err)
	}

	tomlPath := filepath.Join(workingDir, "cluster-a", "cluster-a.toml")
	if _, err := os.Stat(tomlPath); err != nil {
		t.Errorf("expected %s to exist after import: %v", tomlPath, err)
	}

	// No-overwrite rule: importing again into an existing destination must fail
	// and must not be silently accepted.
	if err := repman.importDynamicClusterDir(stagedDir, "cluster-a"); err == nil {
		t.Error("expected error re-importing into an existing destination, got nil")
	}
}

// TestReconstructImportedClusterConfig_MergesStagedDefaultThenClusterDelta
// proves the core happy-path reconstruction logic: the staged repo's own
// default.toml (not this instance's) supplies the baseline, the cluster's
// own delta overlay is then applied on top, and a key already immutable on
// this instance is protected from being overwritten by the staged default.
func TestReconstructImportedClusterConfig_MergesStagedDefaultThenClusterDelta(t *testing.T) {
	stagedDir := t.TempDir()
	workingDir := t.TempDir()

	// Staged repo's own default.toml: a global override only the exporting
	// instance had — reconstruction must pick it up as the baseline.
	if err := os.WriteFile(filepath.Join(stagedDir, "default.toml"), []byte("[saved-default]\nprov-orchestrator = \"onpremise\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// The cluster's own delta, already copied into the live working
	// directory by importDynamicClusterDir before this function ever runs.
	clusterDir := filepath.Join(workingDir, "my-cluster")
	if err := os.MkdirAll(clusterDir, 0755); err != nil {
		t.Fatal(err)
	}
	tomlContent := "[saved-my-cluster]\ndb-servers-hosts = \"10.0.0.1\"\nprov-orchestrator = \"opensvc\"\n"
	if err := os.WriteFile(filepath.Join(clusterDir, "my-cluster.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	repman := &ReplicationManager{
		Conf: &config.Config{
			WorkingDir:  workingDir,
			ConfRewrite: true,
			// prov-ssh-port is immutable on this instance: the staged
			// default.toml must not be able to override it.
			ImmuableFlagMap: map[string]interface{}{"prov-ssh-port": "2222"},
		},
		Logrus:           logrus.New(),
		ImmuableFlagMaps: map[string]map[string]interface{}{"default": {}},
		DynamicFlagMaps:  map[string]map[string]interface{}{"default": {}},
		VersionConfs:     map[string]*config.ConfVersion{},
	}

	clusterConf, err := repman.reconstructImportedClusterConfig(stagedDir, "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cluster's own delta always wins over the staged default baseline.
	if clusterConf.ProvOrchestrator != "opensvc" {
		t.Errorf("expected cluster delta to win, got ProvOrchestrator=%q", clusterConf.ProvOrchestrator)
	}
	if clusterConf.Hosts != "10.0.0.1" {
		t.Errorf("expected cluster delta db-servers-hosts to apply, got Hosts=%q", clusterConf.Hosts)
	}
}

// TestRegisterImportedCluster_MakesClusterVisible proves the "runtime
// visibility" half of a successful import: after registerImportedCluster
// runs, the cluster is present in every map the rest of the server (API
// listing, refreshAllPeers, subsequent hasLocalCluster checks) reads from.
// StartCluster()'s own cluster.Init() machinery is deliberately not invoked
// here — it is pre-existing code shared with AddCluster/PullCloud18Configs,
// not modified by this feature, and requires a much heavier fixture
// (ConfigManager, session/disk managers, OpenSVC certs) to run for real.
func TestRegisterImportedCluster_MakesClusterVisible(t *testing.T) {
	repman := &ReplicationManager{
		Conf:             &config.Config{WorkingDir: t.TempDir()},
		ClusterList:      []string{"existing-cluster"},
		Confs:            map[string]config.Config{},
		VersionConfs:     map[string]*config.ConfVersion{},
		ImmuableFlagMaps: map[string]map[string]interface{}{},
		DynamicFlagMaps:  map[string]map[string]interface{}{},
	}

	clusterConf := config.Config{Hosts: "10.0.0.1"}
	repman.registerImportedCluster("my-cluster", clusterConf)

	found := false
	for _, n := range repman.ClusterList {
		if n == "my-cluster" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected my-cluster in ClusterList, got %v", repman.ClusterList)
	}
	if repman.ClusterList[0] != "existing-cluster" {
		t.Errorf("expected existing entries to survive untouched, got %v", repman.ClusterList)
	}
	if got, ok := repman.Confs["my-cluster"]; !ok || got.Hosts != "10.0.0.1" {
		t.Errorf("expected Confs[my-cluster] to hold the reconstructed config, got %+v, ok=%v", got, ok)
	}
	vc, ok := repman.VersionConfs["my-cluster"]
	if !ok || vc.ConfInit.Hosts != "10.0.0.1" {
		t.Errorf("expected VersionConfs[my-cluster].ConfInit to hold the reconstructed config, got %+v, ok=%v", vc, ok)
	}
}

// TestRollbackFailedImport verifies that a partially-completed import (the
// directory was copied and the cluster was registered in memory, but
// starting it failed) is fully undone: the copied directory is removed and
// every in-memory registration is cleared, so a retry is not permanently
// blocked by hasLocalCluster seeing leftover state.
func TestRollbackFailedImport(t *testing.T) {
	workingDir := t.TempDir()
	writeClusterDir(t, workingDir, "cluster-a")

	repman := &ReplicationManager{
		Conf:             &config.Config{WorkingDir: workingDir},
		Clusters:         map[string]*cluster.Cluster{},
		ClusterList:      []string{"other-cluster", "cluster-a"},
		Confs:            map[string]config.Config{"cluster-a": {}},
		VersionConfs:     map[string]*config.ConfVersion{"cluster-a": {}},
		ImmuableFlagMaps: map[string]map[string]interface{}{"cluster-a": {}},
		DynamicFlagMaps:  map[string]map[string]interface{}{"cluster-a": {}},
	}

	repman.rollbackFailedImport("cluster-a")

	if _, err := os.Stat(filepath.Join(workingDir, "cluster-a")); !os.IsNotExist(err) {
		t.Errorf("expected working-dir/cluster-a to be removed, stat err = %v", err)
	}
	if _, ok := repman.Confs["cluster-a"]; ok {
		t.Error("expected Confs[cluster-a] to be removed")
	}
	if _, ok := repman.VersionConfs["cluster-a"]; ok {
		t.Error("expected VersionConfs[cluster-a] to be removed")
	}
	if _, ok := repman.ImmuableFlagMaps["cluster-a"]; ok {
		t.Error("expected ImmuableFlagMaps[cluster-a] to be removed")
	}
	if _, ok := repman.DynamicFlagMaps["cluster-a"]; ok {
		t.Error("expected DynamicFlagMaps[cluster-a] to be removed")
	}
	for _, n := range repman.ClusterList {
		if n == "cluster-a" {
			t.Errorf("expected cluster-a to be removed from ClusterList, got %v", repman.ClusterList)
		}
	}
	if len(repman.ClusterList) != 1 || repman.ClusterList[0] != "other-cluster" {
		t.Errorf("expected other-cluster to survive rollback untouched, got %v", repman.ClusterList)
	}

	// The whole point: a retry must no longer see this as an existing cluster.
	if repman.hasLocalCluster("cluster-a") {
		t.Error("expected hasLocalCluster to be false after rollback, retry would be blocked forever")
	}
}

// TestImportStagedDynamicClusters_FailedStartRollsBackAndAllowsRetry proves
// the rollback wiring end-to-end through the real import loop (not just the
// rollback helper in isolation): a staged cluster whose TOML is malformed
// fails inside loadAndStartImportedCluster after importDynamicClusterDir has
// already copied it into the live working directory, and the loop must
// still (a) copy the directory away, (b) leave no trace in any in-memory
// registration map, and (c) not block a subsequent retry.
func TestImportStagedDynamicClusters_FailedStartRollsBackAndAllowsRetry(t *testing.T) {
	stagedDir := t.TempDir()
	workingDir := t.TempDir()

	// Passes discoverImportableDynamicClusterDirs (the file exists) but fails
	// parsing inside loadAndStartImportedCluster's isolated Viper read.
	brokenDir := filepath.Join(stagedDir, "broken-cluster")
	if err := os.MkdirAll(brokenDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "broken-cluster.toml"), []byte("[saved-broken-cluster\nnot = valid = toml ===\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := &ReplicationManager{
		Conf:             &config.Config{WorkingDir: workingDir},
		Clusters:         map[string]*cluster.Cluster{},
		ClusterList:      []string{},
		Confs:            map[string]config.Config{},
		VersionConfs:     map[string]*config.ConfVersion{},
		ImmuableFlagMaps: map[string]map[string]interface{}{"default": {}},
		DynamicFlagMaps:  map[string]map[string]interface{}{"default": {}},
	}

	result, err := repman.importStagedDynamicClusters(stagedDir)
	if err != nil {
		t.Fatalf("unexpected whole-action error: %v", err)
	}

	if len(result.Imported) != 0 {
		t.Errorf("expected nothing imported, got %v", result.Imported)
	}
	if _, ok := result.Errors["broken-cluster"]; !ok {
		t.Errorf("expected an error entry for broken-cluster, got %+v", result.Errors)
	}

	if _, statErr := os.Stat(filepath.Join(workingDir, "broken-cluster")); !os.IsNotExist(statErr) {
		t.Errorf("expected working-dir/broken-cluster to be removed after failed start, stat err = %v", statErr)
	}
	if _, ok := repman.Confs["broken-cluster"]; ok {
		t.Error("expected no leftover Confs entry for broken-cluster")
	}
	for _, n := range repman.ClusterList {
		if n == "broken-cluster" {
			t.Errorf("expected no leftover ClusterList entry, got %v", repman.ClusterList)
		}
	}

	// Retry must not see broken-cluster as already existing.
	if repman.hasLocalCluster("broken-cluster") {
		t.Fatal("expected hasLocalCluster to be false after a failed import, retry would be blocked forever")
	}

	// And a retry against the same (still-broken) staged dir must behave
	// identically rather than being skipped as already handled.
	result2, err := repman.importStagedDynamicClusters(stagedDir)
	if err != nil {
		t.Fatalf("unexpected whole-action error on retry: %v", err)
	}
	if _, ok := result2.Errors["broken-cluster"]; !ok {
		t.Errorf("expected retry to attempt the import again and fail the same way, got %+v", result2)
	}
	if len(result2.SkippedExisting) != 0 {
		t.Errorf("expected nothing skipped as existing on retry, got %v", result2.SkippedExisting)
	}
}

func TestFetchDynamicClustersFromGit_RequiresGitURL(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	_, err := repman.FetchDynamicClustersFromGit()
	if err == nil {
		t.Fatal("expected error when git URL is not configured")
	}
}

func TestFetchDynamicClustersFromGit_RequiresGitToken(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{GitUrl: "https://example.invalid/repo.git"}}
	_, err := repman.FetchDynamicClustersFromGit()
	if err == nil {
		t.Fatal("expected error when git access token is not configured")
	}
}

// TestHandlerMuxFetchDynamicClustersFromGit_MethodNotAllowed exercises the
// handler's own internal method guard directly (bypassing the router and
// negroni middleware chain). This does not prove real routing behavior — see
// TestFetchDynamicClustersFromGitRoute_MethodAndAuth for that.
func TestHandlerMuxFetchDynamicClustersFromGit_MethodNotAllowed(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/actions/fetch-dynamic-from-git", nil)
	w := httptest.NewRecorder()
	repman.handlerMuxFetchDynamicClustersFromGit(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestHandlerMuxFetchDynamicClustersFromGit_RequiresAuth exercises the
// handler's own internal admin-claims guard directly (bypassing the router
// and validateTokenMiddleware, which would itself reject an unauthenticated
// request with 401 before the handler ever runs — see
// TestFetchDynamicClustersFromGitRoute_MethodAndAuth for that path).
func TestHandlerMuxFetchDynamicClustersFromGit_RequiresAuth(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/fetch-dynamic-from-git", nil)
	w := httptest.NewRecorder()
	repman.handlerMuxFetchDynamicClustersFromGit(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 without a JWT, got %d", w.Code)
	}
}

// TestFetchDynamicClustersFromGitRoute_MethodAndAuth verifies the real
// router + middleware dispatch (registered in apiClusterProtectedHandler):
// the route is POST-only at the mux level, so a GET never reaches the
// handler, and an unauthenticated POST is rejected by validateTokenMiddleware
// with 401 before the handler's own admin check ever runs. Mirrors
// TestS3ProviderCRUDRoutes_MethodConstraints's approach in api_cluster_test.go.
func TestFetchDynamicClustersFromGitRoute_MethodAndAuth(t *testing.T) {
	repman := &ReplicationManager{}
	router := mux.NewRouter()
	repman.apiClusterProtectedHandler(router)

	tests := []struct {
		name       string
		method     string
		wantStatus int
	}{
		{name: "GET is rejected at the router before auth", method: http.MethodGet, wantStatus: http.StatusMethodNotAllowed},
		{name: "unauthenticated POST is rejected by middleware", method: http.MethodPost, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/clusters/actions/fetch-dynamic-from-git", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("%s: got %d, want %d", tt.method, w.Code, tt.wantStatus)
			}
		})
	}
}

// makeAdminJWTForGitImport signs a JWT whose claims resolve to an authenticated "admin"
// user via GetJWTClaims's local-auth path (no "profile" key in
// CustomUserInfo, so UserInfoMap["User"] is taken directly from Name).
func makeAdminJWTForGitImport(t *testing.T) string {
	t.Helper()

	repman := &ReplicationManager{}
	repman.initKeys()

	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	claims["iss"] = "https://api.replication-manager.signal18.io"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["jti"] = "1"
	claims["CustomUserInfo"] = map[string]interface{}{
		"Name":     "admin",
		"Password": "encrypted",
		"Role":     "Admin",
	}

	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatalf("makeAdminJWTForGitImport: parse private key: %v", err)
	}

	tokenStr, err := signer.SignedString(sk)
	if err != nil {
		t.Fatalf("makeAdminJWTForGitImport: sign token: %v", err)
	}

	return tokenStr
}

// TestHandlerMuxFetchDynamicClustersFromGit_EligibilityGate exercises the
// paid-plan eligibility branch added alongside the admin check: an
// authenticated admin on an ineligible Cloud18 plan must be rejected with
// 403 before ever reaching FetchDynamicClustersFromGit, while an
// authenticated admin on an eligible plan must pass the gate and reach it.
// The "eligible" case asserts on the handler's status-code contract (403 =
// rejected by a gate, 500 = reached FetchDynamicClustersFromGit and failed
// there) rather than on FetchDynamicClustersFromGit's specific prerequisite
// error text/order, so this test doesn't couple to internals already covered
// by TestFetchDynamicClustersFromGit_RequiresGitURL/RequiresGitToken.
func TestHandlerMuxFetchDynamicClustersFromGit_EligibilityGate(t *testing.T) {
	token := makeAdminJWTForGitImport(t)

	t.Run("ineligible plan is rejected with 403", func(t *testing.T) {
		repman := &ReplicationManager{Conf: &config.Config{}}
		req := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/fetch-dynamic-from-git", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		repman.handlerMuxFetchDynamicClustersFromGit(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for ineligible plan, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "subscription plan") {
			t.Errorf("expected eligibility error message, got %s", w.Body.String())
		}
	})

	t.Run("eligible plan passes the gate", func(t *testing.T) {
		repman := &ReplicationManager{Conf: &config.Config{
			Cloud18GitUser:          "user@example.com",
			Cloud18SubscriptionPlan: "support",
		}}
		req := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/fetch-dynamic-from-git", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		repman.handlerMuxFetchDynamicClustersFromGit(w, req)

		if w.Code == http.StatusForbidden {
			t.Fatalf("eligible plan was rejected before reaching FetchDynamicClustersFromGit: %d %s", w.Code, w.Body.String())
		}
		// No git repo is configured in this test, so FetchDynamicClustersFromGit
		// must fail with 500 — proving the request reached it rather than being
		// rejected by the admin or eligibility gate.
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected request to pass the eligibility gate and reach FetchDynamicClustersFromGit (500), got %d: %s", w.Code, w.Body.String())
		}
	})
}
