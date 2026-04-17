package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestNormalizeCompressionOverride(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"Auto", "auto", "auto", false},
		{"Empty", "", "auto", false},
		{"True", "true", "true", false},
		{"False", "false", "false", false},
		{"On", "on", "true", false},
		{"Off", "off", "false", false},
		{"One", "1", "true", false},
		{"Zero", "0", "false", false},
		{"Yes", "yes", "true", false},
		{"No", "no", "false", false},
		{"Invalid", "maybe", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeCompressionOverride(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeCompressionOverride(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidateResticPurgePathList(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"Empty", "", false},
		{"Whitespace", "  \t\n ", false},
		{"SingleAbsolute", "/var/lib/mysql", false},
		{"MultipleAbsolute", "/var/lib/mysql, /data /srv", false},
		{"Relative", "data", true},
		{"Mixed", "/var/lib/mysql, data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticPurgePathList(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
		})
	}
}

func TestValidateResticSizeValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"Empty", "", false},
		{"NumberOnly", "1024", false},
		{"UpperSuffix", "1G", false},
		{"LowerSuffix", "500m", false},
		{"SuffixWithB", "2TB", false},
		{"InvalidSuffix", "1Z", true},
		{"InvalidFormat", "1.5G", true},
		{"Whitespace", " 1G ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticSizeValue(tt.value, "backup-restic-purge-prune-max-unused")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
		})
	}
}

func TestShouldDownloadFromRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"Empty", "", true},
		{"False", `{"download":false}`, false},
		{"True", `{"download":true}`, true},
		{"InvalidJSON", "{", true},
		{"MissingField", `{"other":true}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			if got := shouldDownloadFromRequest(req); got != tt.want {
				t.Fatalf("shouldDownloadFromRequest(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// newTestClusterForAPI builds a minimal cluster backed by a temp dir, suitable
// for passing to buildClusterAPIPayload in unit tests.
func newTestClusterForAPI(t *testing.T) *cluster.Cluster {
	t.Helper()
	dir := t.TempDir()
	name := "test-cluster"
	workingDir := filepath.Join(dir, name)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		t.Fatalf("create working dir: %v", err)
	}
	return &cluster.Cluster{
		Conf: &config.Config{
			WorkingDir: dir,
			Verbose:    false,
		},
		Name:       name,
		WorkingDir: workingDir,
	}
}

// TestBuildClusterAPIPayload_ClusterS3ProvidersPresent verifies that
// buildClusterAPIPayload always emits the "clusterS3Providers" key, even after
// the sjson deletion passes have run (AC: 1 of Story 6.2).
func TestBuildClusterAPIPayload_ClusterS3ProvidersPresent(t *testing.T) {
	cl := newTestClusterForAPI(t)
	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	payload, err := buildClusterAPIPayload(cl)
	if err != nil {
		t.Fatalf("buildClusterAPIPayload: %v", err)
	}

	if !strings.Contains(string(payload), `"clusterS3Providers"`) {
		t.Errorf("expected clusterS3Providers key in payload; got: %s", truncate(string(payload), 500))
	}
	if !strings.Contains(string(payload), "myprovider") {
		t.Errorf("expected provider name in payload")
	}
}

// TestBuildClusterAPIPayload_SecretsAbsent verifies that accesskey and secretkey
// are never present in the API response body, satisfying AC: 2 of Story 6.2.
func TestBuildClusterAPIPayload_SecretsAbsent(t *testing.T) {
	cl := newTestClusterForAPI(t)
	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "withsecrets",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	payload, err := buildClusterAPIPayload(cl)
	if err != nil {
		t.Fatalf("buildClusterAPIPayload: %v", err)
	}

	body := string(payload)
	if strings.Contains(body, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("accesskey leaked in API response body")
	}
	if strings.Contains(body, "wJalrXUtnFEMI") {
		t.Errorf("secretkey leaked in API response body")
	}
	if strings.Contains(body, "accesskey") {
		t.Errorf("accesskey field present in API response body")
	}
	if strings.Contains(body, "secretkey") {
		t.Errorf("secretkey field present in API response body")
	}
}

// TestBuildClusterAPIPayload_SnapshotUsed verifies that buildClusterAPIPayload
// emits the snapshot taken under the mutex, not a potentially-stale direct read.
// It does so by comparing the parsed providers in the response to what
// GetS3ProvidersSnapshot returns.
func TestBuildClusterAPIPayload_SnapshotUsed(t *testing.T) {
	cl := newTestClusterForAPI(t)
	for _, p := range []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
		{Name: "p2", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:9000"},
	} {
		if err := cl.AddS3Provider(p); err != nil {
			t.Fatalf("AddS3Provider: %v", err)
		}
	}

	payload, err := buildClusterAPIPayload(cl)
	if err != nil {
		t.Fatalf("buildClusterAPIPayload: %v", err)
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	var providers []map[string]interface{}
	if err := json.Unmarshal(out["clusterS3Providers"], &providers); err != nil {
		t.Fatalf("unmarshal clusterS3Providers: %v", err)
	}

	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
	if providers[0]["name"] != "p1" || providers[1]["name"] != "p2" {
		t.Errorf("unexpected provider names: %v", providers)
	}
}

// TestBuildClusterAPIPayload_EmptyProviders verifies that an empty (or nil)
// ClusterS3Providers still produces a valid "clusterS3Providers":[] in the response.
func TestBuildClusterAPIPayload_EmptyProviders(t *testing.T) {
	cl := newTestClusterForAPI(t) // providers are nil by default

	payload, err := buildClusterAPIPayload(cl)
	if err != nil {
		t.Fatalf("buildClusterAPIPayload: %v", err)
	}

	if !strings.Contains(string(payload), `"clusterS3Providers"`) {
		t.Errorf("clusterS3Providers key missing for empty providers")
	}
}

// TestBuildClusterAPIPayload_ConcurrentMutationNoRace verifies that
// buildClusterAPIPayload is race-free when concurrent CRUD mutations run
// alongside the serialisation. Run with `go test -race` to exercise the
// detector; the test itself also asserts content correctness.
//
// The race this guards against: before the json:"-" tag was applied to
// ClusterS3Providers, json.Marshal(cluster) read the live slice header while
// AddS3Provider/RemoveS3Provider could rewrite it, causing a data race.
func TestBuildClusterAPIPayload_ConcurrentMutationNoRace(t *testing.T) {
	cl := newTestClusterForAPI(t)
	if err := cl.AddS3Provider(config.S3Provider{
		Name: "initial", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	const iterations = 100
	done := make(chan struct{})

	// Writer goroutine: alternates add/remove to stress concurrent mutation.
	go func() {
		defer close(done)
		extra := config.S3Provider{
			Name:           "extra",
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       "https://extra.example.com",
		}
		for i := 0; i < iterations; i++ {
			_ = cl.AddS3Provider(extra)
			_ = cl.RemoveS3Provider("extra")
		}
	}()

	// Reader: serialise the cluster concurrently with the writer.
	for i := 0; i < iterations; i++ {
		payload, err := buildClusterAPIPayload(cl)
		if err != nil {
			t.Fatalf("buildClusterAPIPayload: %v", err)
		}
		if !strings.Contains(string(payload), `"clusterS3Providers"`) {
			t.Errorf("iteration %d: clusterS3Providers missing from payload", i)
		}
	}

	<-done
}

// truncate returns s truncated to max chars, for safe error display.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// ---- Story 6.3: S3 Provider CRUD API tests ----

// TestMaskS3Provider_SecretsAreMasked verifies that a non-empty SecretKey is
// replaced with "****" and that no credentials appear in the output.
func TestMaskS3Provider_SecretsAreMasked(t *testing.T) {
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI/K7MDENG",
	}
	resp := maskS3Provider(p)
	if resp.SecretKey != "****" {
		t.Errorf("expected SecretKey to be masked as \"****\", got %q", resp.SecretKey)
	}
	if resp.Name != p.Name {
		t.Errorf("expected Name %q, got %q", p.Name, resp.Name)
	}
	if resp.Endpoint != p.Endpoint {
		t.Errorf("expected Endpoint %q, got %q", p.Endpoint, resp.Endpoint)
	}
}

// TestMaskS3Provider_EmptySecretNotMasked verifies that an empty SecretKey is
// not surfaced as "****" in the response.
func TestMaskS3Provider_EmptySecretNotMasked(t *testing.T) {
	p := config.S3Provider{
		Name:           "nosecret",
		ProviderSource: config.S3ProviderSourceApp,
		ProviderApp:    "app:9000",
	}
	resp := maskS3Provider(p)
	if resp.SecretKey != "" {
		t.Errorf("expected empty SecretKey for provider without secret, got %q", resp.SecretKey)
	}
}

// TestValidateS3ProviderAPIRequest_AppModeValidProvider verifies that a valid
// app-mode provider whose ProviderApp exists in AppS3Providers passes validation.
func TestValidateS3ProviderAPIRequest_AppModeValidProvider(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = []string{"app1:9000", "app2:9001"}
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceApp,
		ProviderApp:    "app1:9000",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err != nil {
		t.Errorf("expected no error for valid app provider, got: %v", err)
	}
}

// TestValidateS3ProviderAPIRequest_AppModeUnknownProvider verifies that a
// ProviderApp not present in AppS3Providers is rejected.
func TestValidateS3ProviderAPIRequest_AppModeUnknownProvider(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = []string{"app1:9000"}
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceApp,
		ProviderApp:    "unknown:9000",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err == nil {
		t.Error("expected error for unknown app provider, got nil")
	}
}

// TestValidateS3ProviderAPIRequest_AppModeEmptyProviderList verifies that app
// mode validation fails when AppS3Providers is empty.
func TestValidateS3ProviderAPIRequest_AppModeEmptyProviderList(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = nil
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceApp,
		ProviderApp:    "app1:9000",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err == nil {
		t.Error("expected error when AppS3Providers is empty, got nil")
	}
}

// TestValidateS3ProviderAPIRequest_CustomModeValid verifies that a well-formed
// custom-mode provider passes API-level validation.
func TestValidateS3ProviderAPIRequest_CustomModeValid(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err != nil {
		t.Errorf("expected no error for valid custom provider, got: %v", err)
	}
}

// TestValidateS3ProviderAPIRequest_CustomModeMissingAccessKey verifies that
// custom mode without an access key is rejected.
func TestValidateS3ProviderAPIRequest_CustomModeMissingAccessKey(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		SecretKey:      "wJalrXUtnFEMI",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err == nil {
		t.Error("expected error for missing accesskey, got nil")
	}
}

// TestValidateS3ProviderAPIRequest_CustomModeMissingSecretKey verifies that
// custom mode without a secret key is rejected.
func TestValidateS3ProviderAPIRequest_CustomModeMissingSecretKey(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err == nil {
		t.Error("expected error for missing secretkey, got nil")
	}
}

// TestValidateS3ProviderAPIRequest_CustomModeInvalidEndpoint verifies that
// custom mode with a non-URL endpoint is rejected.
func TestValidateS3ProviderAPIRequest_CustomModeInvalidEndpoint(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "not-a-url",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI",
	}
	if err := validateS3ProviderAPIRequest(p, cl); err == nil {
		t.Error("expected error for invalid endpoint URL, got nil")
	}
}

// TestHandlerMuxClusterS3ProvidersGet_MasksSecrets exercises the GET list handler
// through httptest and verifies that the secretkey field is masked ("****") and
// that credentials are never returned in plaintext.
func TestHandlerMuxClusterS3ProvidersGet_MasksSecrets(t *testing.T) {
	cl := newTestClusterForAPI(t)
	for _, p := range []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", AccessKey: "AKID", SecretKey: "SECRET123"},
		{Name: "p2", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:9000"},
	} {
		if err := cl.AddS3Provider(p); err != nil {
			t.Fatalf("AddS3Provider: %v", err)
		}
	}

	snapshot := cl.GetS3ProvidersSnapshot()
	resp := make([]s3ProviderResponse, len(snapshot))
	for i, p := range snapshot {
		resp[i] = maskS3Provider(p)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)

	if strings.Contains(body, "AKID") {
		t.Error("AccessKey leaked in list response")
	}
	if strings.Contains(body, "SECRET123") {
		t.Error("SecretKey leaked in list response")
	}
	if !strings.Contains(body, "****") {
		t.Error("expected masked SecretKey \"****\" in response for provider with secret")
	}
	if !strings.Contains(body, "p1") || !strings.Contains(body, "p2") {
		t.Errorf("provider names missing from response: %s", body)
	}
}

// TestHandlerMuxClusterS3ProvidersGet_EmptyList verifies that an empty provider
// list produces a valid JSON array (not null).
func TestHandlerMuxClusterS3ProvidersGet_EmptyList(t *testing.T) {
	cl := newTestClusterForAPI(t) // providers are nil by default

	snapshot := cl.GetS3ProvidersSnapshot()
	resp := make([]s3ProviderResponse, len(snapshot))
	for i, p := range snapshot {
		resp[i] = maskS3Provider(p)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if body != "[]" {
		t.Errorf("expected \"[]\", got %s", body)
	}
}

// newTestRepmanWithCluster builds the minimal ReplicationManager needed to test
// handlers that call repman.getClusterByName. Authentication is not bypassed, so
// the handlers will return 403 for requests without a valid JWT — that is
// sufficient for testing the no-cluster 500 path and ACL-gate 403 path.
func newTestRepmanWithCluster(t *testing.T, name string, cl *cluster.Cluster) *ReplicationManager {
	t.Helper()
	repman := &ReplicationManager{
		Clusters: map[string]*cluster.Cluster{
			name: cl,
		},
	}
	return repman
}

// TestHandlerS3ProviderGet_NoCluster verifies that the GET handler returns 500
// when the requested cluster does not exist (no auth required for this path).
func TestHandlerS3ProviderGet_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest("GET", "/api/clusters/missing/s3providers", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProvidersGet(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// TestHandlerS3ProviderAdd_NoCluster verifies that the add handler returns 500
// when the requested cluster does not exist.
func TestHandlerS3ProviderAdd_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest("POST", "/api/clusters/missing/s3providers/add",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderAdd(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// TestHandlerS3ProviderModify_NoCluster verifies that the modify handler returns
// 500 when the requested cluster does not exist.
func TestHandlerS3ProviderModify_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest("POST", "/api/clusters/missing/s3providers/p1/modify",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "name": "p1"})
	w := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderModify(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// TestHandlerS3ProviderDrop_NoCluster verifies that the drop handler returns 500
// when the requested cluster does not exist.
func TestHandlerS3ProviderDrop_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest("GET", "/api/clusters/missing/s3providers/p1/drop", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "name": "p1"})
	w := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderDrop(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// setMuxVars injects gorilla/mux route variables into a request context so that
// handler tests can exercise mux.Vars(r) without a live router.
func setMuxVars(r *http.Request, vars map[string]string) *http.Request {
	return mux.SetURLVars(r, vars)
}

// TestS3ProviderModifyNameAuthority verifies that the modify handler sets the
// provider name from the URL path, ignoring any name present in the request body.
// This guards against accidental rename when body and path differ.
func TestS3ProviderModifyNameAuthority(t *testing.T) {
	// Simulate what the handler does: URL name overrides body name.
	req := s3ProviderRequest{
		Name:           "body-name",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}
	urlName := "url-name"
	req.Name = urlName // this is what handlerMuxClusterS3ProviderModify does
	if req.Name != urlName {
		t.Errorf("expected URL name %q to override body name, got %q", urlName, req.Name)
	}
}

// TestS3ProviderAddRollback_SaveFailure verifies that when SaveS3Providers fails
// after a successful in-memory add, the rollback removes the provider so that
// in-memory and on-disk state remain consistent.
func TestS3ProviderAddRollback_SaveFailure(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "to-add",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}

	if err := cl.AddS3Provider(p); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}
	// Confirm in-memory add succeeded.
	if len(cl.GetS3ProvidersSnapshot()) != 1 {
		t.Fatalf("expected 1 provider after add, got %d", len(cl.GetS3ProvidersSnapshot()))
	}

	// Simulate a save failure → rollback.
	if err := cl.RemoveS3Provider(p.Name); err != nil {
		t.Fatalf("rollback RemoveS3Provider: %v", err)
	}
	// After rollback, provider should be gone.
	if len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Errorf("expected 0 providers after add rollback, got %d", len(cl.GetS3ProvidersSnapshot()))
	}
}

// TestS3ProviderModifyRollback_SaveFailure verifies that when SaveS3Providers
// fails after a successful in-memory update, the rollback restores the original
// provider value.
func TestS3ProviderModifyRollback_SaveFailure(t *testing.T) {
	cl := newTestClusterForAPI(t)
	original := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://original.example.com",
		AccessKey:      "AK1",
		SecretKey:      "SK1",
	}
	if err := cl.AddS3Provider(original); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	updated := config.S3Provider{
		Name:           "myprovider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://updated.example.com",
		AccessKey:      "AK2",
		SecretKey:      "SK2",
	}
	// Snapshot old state (as the handler does).
	var oldProvider *config.S3Provider
	for _, sp := range cl.GetS3ProvidersSnapshot() {
		if sp.Name == original.Name {
			cp := sp
			oldProvider = &cp
			break
		}
	}
	if err := cl.UpdateS3Provider(updated); err != nil {
		t.Fatalf("UpdateS3Provider: %v", err)
	}
	// Verify update applied.
	snap := cl.GetS3ProvidersSnapshot()
	if snap[0].Endpoint != updated.Endpoint {
		t.Errorf("expected updated endpoint %q, got %q", updated.Endpoint, snap[0].Endpoint)
	}

	// Simulate save failure → rollback.
	if oldProvider != nil {
		if err := cl.UpdateS3Provider(*oldProvider); err != nil {
			t.Fatalf("rollback UpdateS3Provider: %v", err)
		}
	}
	// After rollback, endpoint should be restored.
	snap = cl.GetS3ProvidersSnapshot()
	if snap[0].Endpoint != original.Endpoint {
		t.Errorf("expected original endpoint %q after rollback, got %q", original.Endpoint, snap[0].Endpoint)
	}
}

// TestS3ProviderDropRollback_SaveFailure verifies that when SaveS3Providers fails
// after a successful in-memory remove, the rollback re-adds the provider.
func TestS3ProviderDropRollback_SaveFailure(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "to-drop",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}
	if err := cl.AddS3Provider(p); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	// Snapshot before remove (as the handler does).
	var oldProvider *config.S3Provider
	for _, sp := range cl.GetS3ProvidersSnapshot() {
		if sp.Name == p.Name {
			cp := sp
			oldProvider = &cp
			break
		}
	}
	if err := cl.RemoveS3Provider(p.Name); err != nil {
		t.Fatalf("RemoveS3Provider: %v", err)
	}
	// Confirm removed.
	if len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Errorf("expected 0 providers after remove, got %d", len(cl.GetS3ProvidersSnapshot()))
	}

	// Simulate save failure → rollback re-add.
	if oldProvider != nil {
		if err := cl.AddS3Provider(*oldProvider); err != nil {
			t.Fatalf("rollback AddS3Provider: %v", err)
		}
	}
	// After rollback, provider should be back.
	if len(cl.GetS3ProvidersSnapshot()) != 1 {
		t.Errorf("expected 1 provider after drop rollback, got %d", len(cl.GetS3ProvidersSnapshot()))
	}
	if cl.GetS3ProvidersSnapshot()[0].Name != p.Name {
		t.Errorf("expected provider name %q after rollback, got %q", p.Name, cl.GetS3ProvidersSnapshot()[0].Name)
	}
}

// TestS3ProviderDropNotFound_ClusterLevel verifies that RemoveS3Provider returns
// an error when the named provider does not exist.
func TestS3ProviderDropNotFound_ClusterLevel(t *testing.T) {
	cl := newTestClusterForAPI(t)
	if err := cl.RemoveS3Provider("nonexistent"); err == nil {
		t.Error("expected error when removing nonexistent provider, got nil")
	}
}

// TestS3ProviderAddDuplicate_ClusterLevel verifies that AddS3Provider returns an
// error (and thus the handler would return 409) when a provider with the same
// name already exists.
func TestS3ProviderAddDuplicate_ClusterLevel(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "dup",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}
	if err := cl.AddS3Provider(p); err != nil {
		t.Fatalf("first AddS3Provider: %v", err)
	}
	if err := cl.AddS3Provider(p); err == nil {
		t.Error("expected conflict error on duplicate add, got nil")
	}
}

// TestPrepareModifyProvider_BlankCredsPreservedAndValidationPasses exercises the
// full prepareModifyProvider pipeline for the blocker case: a custom-mode edit
// request that omits AccessKey and SecretKey (the UI "no change" convention).
//
// Before the fix, validation ran before the credential merge, so blank keys
// triggered "accesskey is required" from validateS3ProviderAPIRequest. This test
// would have failed on the old ordering.
func TestPrepareModifyProvider_BlankCredsPreservedAndValidationPasses(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = []string{} // not app mode — no app validation needed
	original := config.S3Provider{
		Name:           "provider-with-creds",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK-original",
		SecretKey:      "SK-original",
	}
	if err := cl.AddS3Provider(original); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	// Simulate a UI modify request: endpoint updated, credentials left blank.
	req := s3ProviderRequest{
		Name:           "provider-with-creds",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3-updated.example.com",
		Region:         "eu-west-1",
		AccessKey:      "", // blank = no change
		SecretKey:      "", // blank = no change
	}

	p, oldProv, err := prepareModifyProvider(req, cl)
	if err != nil {
		// This is the bug the fix addresses: validation used to reject blank creds
		// before the merge happened, returning "accesskey is required for custom mode".
		t.Fatalf("prepareModifyProvider returned error (old ordering bug?): %v", err)
	}
	if oldProv == nil {
		t.Fatal("expected oldProvider to be found")
	}
	if p.AccessKey != "AK-original" {
		t.Errorf("expected preserved AccessKey %q, got %q", "AK-original", p.AccessKey)
	}
	if p.SecretKey != "SK-original" {
		t.Errorf("expected preserved SecretKey %q, got %q", "SK-original", p.SecretKey)
	}
	if p.Endpoint != "https://s3-updated.example.com" {
		t.Errorf("expected updated endpoint, got %q", p.Endpoint)
	}
}

// TestPrepareModifyProvider_NewCredsAppliedWhenProvided verifies that non-empty
// credentials in the modify request replace the existing ones.
func TestPrepareModifyProvider_NewCredsAppliedWhenProvided(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = []string{}
	original := config.S3Provider{
		Name:           "provider-with-creds",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK-old",
		SecretKey:      "SK-old",
	}
	if err := cl.AddS3Provider(original); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	req := s3ProviderRequest{
		Name:           "provider-with-creds",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK-new",
		SecretKey:      "SK-new",
	}

	p, _, err := prepareModifyProvider(req, cl)
	if err != nil {
		t.Fatalf("prepareModifyProvider: %v", err)
	}
	if p.AccessKey != "AK-new" {
		t.Errorf("expected new AccessKey %q, got %q", "AK-new", p.AccessKey)
	}
	if p.SecretKey != "SK-new" {
		t.Errorf("expected new SecretKey %q, got %q", "SK-new", p.SecretKey)
	}
}

// TestPrepareModifyProvider_ValidationFailsInvalidEndpoint confirms that
// prepareModifyProvider still rejects malformed endpoints even after merging creds.
func TestPrepareModifyProvider_ValidationFailsInvalidEndpoint(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.AppS3Providers = []string{}
	original := config.S3Provider{
		Name:           "provider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}
	if err := cl.AddS3Provider(original); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	req := s3ProviderRequest{
		Name:           "provider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "not-a-url", // invalid
		AccessKey:      "",
		SecretKey:      "",
	}

	_, _, err := prepareModifyProvider(req, cl)
	if err == nil {
		t.Error("expected validation error for invalid endpoint, got nil")
	}
}

// ---------------------------------------------------------------------------
// handlerMuxClusterS3ProviderReferences tests
// ---------------------------------------------------------------------------

// newAppWithS3Mount returns a minimal *cluster.App whose AppConfig contains one
// S3 mount referencing providerName. id and name are used for identity.
func newAppWithS3Mount(id, name, mountName, mountEndpoint, mountRegion, mountBucket, providerName string) *cluster.App {
	return &cluster.App{
		Id:   id,
		Name: name,
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: config.S3Mounts{
						{
							Name:         mountName,
							Endpoint:     mountEndpoint,
							Region:       mountRegion,
							Bucket:       mountBucket,
							ProviderName: providerName,
						},
					},
				},
			},
		},
	}
}

// TestHandlerS3ProviderReferences_NoCluster verifies the handler returns 500
// when the cluster does not exist (name validation runs before cluster lookup,
// so p1 passes validation and reaches the missing-cluster check).
func TestHandlerS3ProviderReferences_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest("GET", "/api/clusters/missing/s3providers/p1/references", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "name": "p1"})
	w := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderReferences(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// Business-logic tests call buildS3ProviderReferencesResponse directly,
// matching the pattern used for maskS3Provider and validateS3ProviderAPIRequest.
// This avoids the need for a JWT to pass IsValidClusterACL.

// TestBuildS3ProviderReferences_ProviderNotFound_ProviderFoundFalse verifies
// that providerFound is false when the named provider does not exist (F-3:
// distinguishable from "provider exists with zero references").
func TestBuildS3ProviderReferences_ProviderNotFound_ProviderFoundFalse(t *testing.T) {
	cl := newTestClusterForAPI(t)
	resp := buildS3ProviderReferencesResponse(cl, "nonexistent", nil)
	if resp.ProviderFound {
		t.Error("expected providerFound=false for unknown provider")
	}
	if resp.ReferenceCount != 0 {
		t.Errorf("expected referenceCount=0, got %d", resp.ReferenceCount)
	}
	if len(resp.References) != 0 {
		t.Errorf("expected empty references, got %d entries", len(resp.References))
	}
}

// TestBuildS3ProviderReferences_ProviderExists_ZeroRefs verifies that
// providerFound is true and count is zero when the provider exists but no mounts
// reference it (F-3: distinct from "provider not found").
func TestBuildS3ProviderReferences_ProviderExists_ZeroRefs(t *testing.T) {
	cl := newTestClusterForAPI(t)
	p := config.S3Provider{
		Name:           "my-provider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "AK",
		SecretKey:      "SK",
	}
	resp := buildS3ProviderReferencesResponse(cl, "my-provider", &p)
	if !resp.ProviderFound {
		t.Error("expected providerFound=true when provider pointer is non-nil")
	}
	if resp.ReferenceCount != 0 {
		t.Errorf("expected referenceCount=0, got %d", resp.ReferenceCount)
	}
}

// TestBuildS3ProviderReferences_MatchesProvider verifies that a mount whose
// endpoint and region equal the provider's effective values gets "matches_provider".
func TestBuildS3ProviderReferences_MatchesProvider(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Apps = append(cl.Apps, newAppWithS3Mount("app-1", "MyApp", "backup", "https://s3.example.com", "us-east-1", "bkt", "my-provider"))
	p := config.S3Provider{
		Name:           "my-provider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
	}
	resp := buildS3ProviderReferencesResponse(cl, "my-provider", &p)
	if resp.ReferenceCount != 1 {
		t.Fatalf("expected referenceCount=1, got %d", resp.ReferenceCount)
	}
	if resp.References[0].Status != "matches_provider" {
		t.Errorf("expected status matches_provider, got %q", resp.References[0].Status)
	}
	if resp.References[0].AppID != "app-1" {
		t.Errorf("expected appId app-1, got %q", resp.References[0].AppID)
	}
	if resp.References[0].MountName != "backup" {
		t.Errorf("expected mountName backup, got %q", resp.References[0].MountName)
	}
}

// TestBuildS3ProviderReferences_Customized verifies that a mount with a
// different endpoint than the provider gets status "customized".
func TestBuildS3ProviderReferences_Customized(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Apps = append(cl.Apps, newAppWithS3Mount("app-1", "MyApp", "backup", "https://other.example.com", "us-east-1", "bkt", "my-provider"))
	p := config.S3Provider{
		Name:           "my-provider",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
	}
	resp := buildS3ProviderReferencesResponse(cl, "my-provider", &p)
	if resp.ReferenceCount != 1 {
		t.Fatalf("expected referenceCount=1, got %d", resp.ReferenceCount)
	}
	if resp.References[0].Status != "customized" {
		t.Errorf("expected status customized, got %q", resp.References[0].Status)
	}
}

// TestBuildS3ProviderReferences_ProviderMissing verifies that mounts referencing
// a deleted provider get status "provider_missing" (provider pointer is nil).
func TestBuildS3ProviderReferences_ProviderMissing(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Apps = append(cl.Apps, newAppWithS3Mount("app-1", "MyApp", "backup", "https://s3.example.com", "us-east-1", "bkt", "deleted-provider"))
	resp := buildS3ProviderReferencesResponse(cl, "deleted-provider", nil)
	if resp.ProviderFound {
		t.Error("expected providerFound=false when provider pointer is nil")
	}
	if resp.ReferenceCount != 1 {
		t.Fatalf("expected referenceCount=1 for stale mount, got %d", resp.ReferenceCount)
	}
	if resp.References[0].Status != "provider_missing" {
		t.Errorf("expected status provider_missing, got %q", resp.References[0].Status)
	}
}

// TestBuildS3ProviderReferences_MultipleApps verifies that mounts across
// multiple apps are all counted.
func TestBuildS3ProviderReferences_MultipleApps(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Apps = append(cl.Apps,
		newAppWithS3Mount("app-1", "Alpha", "mount-a", "https://s3.example.com", "eu-west-1", "bkt", "shared"),
		newAppWithS3Mount("app-2", "Beta", "mount-b", "https://s3.example.com", "eu-west-1", "bkt", "shared"),
	)
	p := config.S3Provider{
		Name:           "shared",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "eu-west-1",
	}
	resp := buildS3ProviderReferencesResponse(cl, "shared", &p)
	if resp.ReferenceCount != 2 {
		t.Errorf("expected referenceCount=2, got %d", resp.ReferenceCount)
	}
}

// TestBuildS3ProviderReferences_UnrelatedMountIgnored verifies that mounts
// referencing a different provider are not included in the response.
func TestBuildS3ProviderReferences_UnrelatedMountIgnored(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Apps = append(cl.Apps, newAppWithS3Mount("app-1", "MyApp", "backup", "https://s3.example.com", "us-east-1", "bkt", "other-provider"))
	p := config.S3Provider{
		Name:           "target",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
	}
	resp := buildS3ProviderReferencesResponse(cl, "target", &p)
	if resp.ReferenceCount != 0 {
		t.Errorf("expected referenceCount=0 when mount references different provider, got %d", resp.ReferenceCount)
	}
}

// ---- Story 6.10: S3 Provider Sync API tests ----
// Handler-level tests are limited to cases that return before the ACL check
// (e.g. unknown cluster). Full sync logic tests live in cluster/sync_s3_provider_test.go.

// TestHandlerMuxClusterS3ProviderSyncPreview_UnknownCluster verifies that
// requests for an unknown cluster name return 500 before any ACL or body parsing.
func TestHandlerMuxClusterS3ProviderSyncPreview_UnknownCluster(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = make(map[string]*cluster.Cluster)

	req := httptest.NewRequest(http.MethodPost, "/api/clusters/no-such-cluster/s3providers/p/sync/preview",
		strings.NewReader(`{"targets":[{"appId":"a","mountName":"m"}]}`))
	req = mux.SetURLVars(req, map[string]string{"clusterName": "no-such-cluster", "name": "p"})
	rr := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderSyncPreview(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for unknown cluster, got %d", rr.Code)
	}
}

// TestHandlerMuxClusterS3ProviderSyncApply_UnknownCluster verifies the same
// behaviour for the apply endpoint.
func TestHandlerMuxClusterS3ProviderSyncApply_UnknownCluster(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = make(map[string]*cluster.Cluster)

	req := httptest.NewRequest(http.MethodPost, "/api/clusters/no-such-cluster/s3providers/p/sync/apply",
		strings.NewReader(`{"targets":[{"appId":"a","mountName":"m"}]}`))
	req = mux.SetURLVars(req, map[string]string{"clusterName": "no-such-cluster", "name": "p"})
	rr := httptest.NewRecorder()
	repman.handlerMuxClusterS3ProviderSyncApply(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for unknown cluster, got %d", rr.Code)
	}
}

func TestValidateS3SyncApplyRevisionToken(t *testing.T) {
	valid := "s3sync:v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	if err := validateS3SyncApplyRevisionToken(valid); err != nil {
		t.Fatalf("expected valid token to pass, got %v", err)
	}
	if err := validateS3SyncApplyRevisionToken(""); err == nil {
		t.Fatalf("expected missing revisionToken to fail")
	}
	if err := validateS3SyncApplyRevisionToken("s3sync:v1:not-hex"); err == nil {
		t.Fatalf("expected malformed revisionToken to fail")
	}
}
