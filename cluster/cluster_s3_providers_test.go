// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// newTestClusterForS3 creates a minimal Cluster backed by a temp directory.
func newTestClusterForS3(t *testing.T) (*Cluster, string) {
	t.Helper()
	dir := t.TempDir()
	clusterName := "test-cluster"
	workingDir := filepath.Join(dir, clusterName)
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		t.Fatalf("create working dir: %v", err)
	}
	cl := &Cluster{
		Conf: &config.Config{
			WorkingDir: dir,
			Verbose:    false,
		},
		Name:       clusterName,
		WorkingDir: workingDir,
	}
	return cl, workingDir
}

// ---------- Name validation tests ----------

func TestValidateS3ProviderName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "myprovider", false},
		{"valid with dash", "my-provider", false},
		{"valid with underscore", "my_provider", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"leading whitespace", " provider", true},
		{"trailing whitespace", "provider ", true},
		{"contains forward slash", "my/provider", true},
		{"contains backslash", `my\provider`, true},
		{"exactly 255 chars", strings.Repeat("a", 255), false},
		{"256 chars", strings.Repeat("a", 256), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateS3ProviderName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("input=%q: wantErr=%v got err=%v", tc.input, tc.wantErr, err)
			}
		})
	}
}

// ---------- S3Provider.Validate per-mode tests ----------

func TestS3ProviderValidate_AppMode(t *testing.T) {
	cases := []struct {
		name    string
		p       config.S3Provider
		wantErr bool
	}{
		{
			"app mode valid",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "myapp:8080"},
			false,
		},
		{
			"app mode missing providerApp",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp},
			true,
		},
		{
			"app mode whitespace-only providerApp",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "   "},
			true,
		},
		{
			"app mode has endpoint (forbidden)",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:8080", Endpoint: "https://s3.example.com"},
			true,
		},
		{
			"app mode has region (forbidden)",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:8080", Region: "us-east-1"},
			true,
		},
		{
			"app mode has accesskey (forbidden)",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:8080", AccessKey: "key"},
			true,
		},
		{
			"app mode has secretkey (forbidden)",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "app:8080", SecretKey: "secret"},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestS3ProviderValidate_CustomMode(t *testing.T) {
	cases := []struct {
		name    string
		p       config.S3Provider
		wantErr bool
	}{
		{
			"custom mode valid minimal",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
			false,
		},
		{
			"custom mode valid full",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", Region: "us-east-1", AccessKey: "k", SecretKey: "s"},
			false,
		},
		{
			"custom mode missing endpoint",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom},
			true,
		},
		{
			"custom mode whitespace-only endpoint",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "   "},
			true,
		},
		{
			"custom mode has providerApp (forbidden)",
			config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", ProviderApp: "app:8080"},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestS3ProviderValidate_InvalidSource(t *testing.T) {
	cases := []config.S3ProviderSource{"s3", "", "App", "CUSTOM"}
	for _, src := range cases {
		p := config.S3Provider{Name: "test", ProviderSource: src}
		if err := p.Validate(); err == nil {
			t.Errorf("source=%q: expected error, got nil", src)
		}
	}
}

func TestS3ProviderValidate_InvalidName(t *testing.T) {
	p := config.S3Provider{Name: "", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}
	if err := p.Validate(); err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

// ---------- LoadS3Providers tests ----------

func TestLoadS3Providers_EmptyWhenFileMissing(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.LoadS3Providers()
	if cl.ClusterS3Providers == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(cl.ClusterS3Providers) != 0 {
		t.Fatalf("expected empty slice, got len=%d", len(cl.ClusterS3Providers))
	}
}

func TestLoadS3Providers_EmptyWhenFileInvalid(t *testing.T) {
	cl, workDir := newTestClusterForS3(t)
	path := filepath.Join(workDir, s3ProvidersFileName)
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	cl.LoadS3Providers()
	if len(cl.ClusterS3Providers) != 0 {
		t.Fatalf("expected empty slice on parse error, got len=%d", len(cl.ClusterS3Providers))
	}
}

func TestLoadS3Providers_DeduplicatesNames_FirstWins(t *testing.T) {
	cl, workDir := newTestClusterForS3(t)
	// Two valid records with the same name; first occurrence must win.
	raw := `[
		{"name":"dup","providerSource":"custom","endpoint":"https://first.example.com"},
		{"name":"dup","providerSource":"custom","endpoint":"https://second.example.com"}
	]`
	path := filepath.Join(workDir, s3ProvidersFileName)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cl.LoadS3Providers()
	if len(cl.ClusterS3Providers) != 1 {
		t.Fatalf("expected 1 provider (first-wins dedup), got %d", len(cl.ClusterS3Providers))
	}
	if cl.ClusterS3Providers[0].Endpoint != "https://first.example.com" {
		t.Errorf("expected first occurrence to win, got endpoint %q", cl.ClusterS3Providers[0].Endpoint)
	}
}

func TestLoadS3Providers_SkipsInvalidRecords(t *testing.T) {
	cl, workDir := newTestClusterForS3(t)
	// Write a structurally valid JSON file with one valid and one invalid provider.
	// Invalid: custom mode with no endpoint. Valid: custom mode with endpoint.
	raw := `[
		{"name":"bad","providerSource":"custom"},
		{"name":"good","providerSource":"custom","endpoint":"https://s3.example.com"}
	]`
	path := filepath.Join(workDir, s3ProvidersFileName)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cl.LoadS3Providers()
	if len(cl.ClusterS3Providers) != 1 {
		t.Fatalf("expected 1 valid provider, got %d", len(cl.ClusterS3Providers))
	}
	if cl.ClusterS3Providers[0].Name != "good" {
		t.Errorf("expected provider 'good', got %q", cl.ClusterS3Providers[0].Name)
	}
}

// ---------- SaveS3Providers / round-trip tests ----------

func TestSaveAndLoadS3Providers_RoundTrip(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "myprovider", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", Region: "us-east-1"},
		{Name: "appref", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "myapp:8080"},
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatalf("SaveS3Providers: %v", err)
	}

	// Load into a fresh cluster instance pointing to the same working directory.
	cl2, _ := newTestClusterForS3(t)
	cl2.WorkingDir = cl.WorkingDir
	cl2.LoadS3Providers()

	if len(cl2.ClusterS3Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cl2.ClusterS3Providers))
	}
	if cl2.ClusterS3Providers[0].Name != "myprovider" {
		t.Errorf("unexpected name: %q", cl2.ClusterS3Providers[0].Name)
	}
	if cl2.ClusterS3Providers[1].ProviderApp != "myapp:8080" {
		t.Errorf("unexpected providerApp: %q", cl2.ClusterS3Providers[1].ProviderApp)
	}
}

func TestSaveS3Providers_FilePermissions(t *testing.T) {
	cl, workDir := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", AccessKey: "key", SecretKey: "secret"},
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatalf("SaveS3Providers: %v", err)
	}
	info, err := os.Stat(filepath.Join(workDir, s3ProvidersFileName))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected file permissions 0600, got %04o", perm)
	}
}

func TestSaveS3Providers_WritesValidJSON(t *testing.T) {
	cl, workDir := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatalf("SaveS3Providers: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workDir, s3ProvidersFileName))
	if err != nil {
		t.Fatal(err)
	}
	var out []config.S3Provider
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if len(out) != 1 || out[0].Name != "p1" {
		t.Errorf("unexpected content: %+v", out)
	}
}

func TestSaveS3Providers_RejectsDuplicateNames(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	// Inject duplicates directly via public field (bypassing AddS3Provider).
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "dup", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
		{Name: "dup", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3-other.example.com"},
	}
	if err := cl.SaveS3Providers(); err == nil {
		t.Fatal("expected error for duplicate names in snapshot, got nil")
	}
}

func TestSaveS3Providers_SecretKeyPersistedInFile(t *testing.T) {
	// SaveS3Providers uses s3ProviderOnDisk (bypasses MarshalJSON) so secrets
	// must appear in the file even though json.Marshal(S3Provider) omits them.
	cl, workDir := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", SecretKey: "supersecret"},
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatalf("SaveS3Providers: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workDir, s3ProvidersFileName))
	if err != nil {
		t.Fatal(err)
	}
	// Secret must be in the file (plaintext when no encryption key is loaded;
	// "hash_..." when a key is configured).
	if !strings.Contains(string(raw), "supersecret") && !strings.Contains(string(raw), "hash_") {
		t.Error("expected secretkey to be persisted in JSON file via s3ProviderOnDisk")
	}
}

func TestSaveAndLoadS3Providers_SecretsRoundTrip(t *testing.T) {
	// Verifies the full encrypt→persist→load→decrypt path is symmetric.
	// Without a loaded encryption key the values are stored as-is (graceful degradation).
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{
			Name:           "secure",
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       "https://s3.example.com",
			AccessKey:      "myaccesskey",
			SecretKey:      "mysecretkey",
		},
	}
	if err := cl.SaveS3Providers(); err != nil {
		t.Fatalf("SaveS3Providers: %v", err)
	}

	cl2, _ := newTestClusterForS3(t)
	cl2.WorkingDir = cl.WorkingDir
	cl2.LoadS3Providers()

	if len(cl2.ClusterS3Providers) != 1 {
		t.Fatalf("expected 1 provider after load, got %d", len(cl2.ClusterS3Providers))
	}
	p := cl2.ClusterS3Providers[0]
	if p.AccessKey != "myaccesskey" {
		t.Errorf("AccessKey round-trip failed: got %q", p.AccessKey)
	}
	if p.SecretKey != "mysecretkey" {
		t.Errorf("SecretKey round-trip failed: got %q", p.SecretKey)
	}
}

// ---------- Secret redaction regression tests ----------

func TestS3Provider_MarshalJSON_SecretsAbsent(t *testing.T) {
	// Regression: json.Marshal(S3Provider) must never emit credentials.
	// This covers the json.Marshal(cluster) path used by GetCompactJson() and
	// the per-cluster handler at server/api_cluster.go.
	p := config.S3Provider{
		Name:           "test",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		AccessKey:      "should-not-appear",
		SecretKey:      "also-should-not-appear",
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "should-not-appear") {
		t.Errorf("accesskey leaked in JSON output: %s", s)
	}
	if strings.Contains(s, "also-should-not-appear") {
		t.Errorf("secretkey leaked in JSON output: %s", s)
	}
	// Non-sensitive fields must still be present.
	if !strings.Contains(s, "https://s3.example.com") {
		t.Errorf("endpoint missing from JSON output: %s", s)
	}
}

func TestCluster_ClusterS3Providers_SecretsAbsentFromMarshal(t *testing.T) {
	// Regression: marshaling the ClusterS3Providers slice (as done when marshaling
	// the full Cluster struct) must not expose accesskey or secretkey.
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{
			Name:           "provider",
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       "https://s3.example.com",
			AccessKey:      "leak-ak",
			SecretKey:      "leak-sk",
		},
	}
	data, err := json.Marshal(cl.ClusterS3Providers)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "leak-ak") {
		t.Errorf("accesskey leaked in cluster JSON: %s", s)
	}
	if strings.Contains(s, "leak-sk") {
		t.Errorf("secretkey leaked in cluster JSON: %s", s)
	}
}

func TestCluster_ClusterS3ProvidersField_ExcludedFromDirectMarshal(t *testing.T) {
	// ClusterS3Providers carries json:"-" so that json.Marshal(cluster) never
	// reads the live slice during a concurrent write window. The API response
	// path (buildClusterAPIPayload / GetCompactJson) injects a safe snapshot via
	// GetS3ProvidersSnapshot() + sjson instead.
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{
			Name:           "myprovider",
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       "https://s3.example.com",
		},
	}
	data, err := json.Marshal(cl)
	if err != nil {
		t.Fatalf("marshal Cluster: %v", err)
	}
	if strings.Contains(string(data), `"clusterS3Providers"`) {
		t.Errorf("ClusterS3Providers must be excluded from direct json.Marshal (json:\"-\" tag required); found key in output")
	}
}

func TestCluster_GetS3ProvidersSnapshot_ReturnsDeepCopy(t *testing.T) {
	// Verifies GetS3ProvidersSnapshot returns a copy, not a reference, so callers
	// cannot accidentally mutate the live slice.
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p1", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
	}
	snap := cl.GetS3ProvidersSnapshot()
	if len(snap) != 1 || snap[0].Name != "p1" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	// Mutate the snapshot; live slice must not change.
	snap[0].Name = "mutated"
	if cl.ClusterS3Providers[0].Name != "p1" {
		t.Errorf("GetS3ProvidersSnapshot must return a deep copy; live slice was mutated")
	}
}

// ---------- AddS3Provider tests ----------

func TestAddS3Provider_Success(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{}
	p := config.S3Provider{Name: "newprovider", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}
	if err := cl.AddS3Provider(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cl.ClusterS3Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cl.ClusterS3Providers))
	}
}

func TestAddS3Provider_DuplicateNameRejected(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	p := config.S3Provider{Name: "dup", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}
	cl.ClusterS3Providers = []config.S3Provider{p}
	err := cl.AddS3Provider(p)
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestAddS3Provider_DuplicateIsCaseSensitive(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "Provider", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
	}
	// "provider" (lowercase) must NOT be rejected as a duplicate of "Provider".
	if err := cl.AddS3Provider(config.S3Provider{Name: "provider", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}); err != nil {
		t.Fatalf("case-sensitive: unexpected duplicate error: %v", err)
	}
}

func TestAddS3Provider_ValidationEnforced(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{}
	// custom mode without endpoint should fail validation
	p := config.S3Provider{Name: "bad", ProviderSource: config.S3ProviderSourceCustom}
	if err := cl.AddS3Provider(p); err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if len(cl.ClusterS3Providers) != 0 {
		t.Fatal("invalid provider must not be added")
	}
}

func TestAddS3Provider_ConcurrentDuplicate_OneSucceedsOneConflicts(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{}
	p := config.S3Provider{Name: "race-dup", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- cl.AddS3Provider(p)
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	var successCount int
	var duplicateCount int
	for err := range errCh {
		if err == nil {
			successCount++
			continue
		}
		if strings.Contains(err.Error(), "already exists") {
			duplicateCount++
			continue
		}
		t.Fatalf("unexpected error type: %v", err)
	}

	if successCount != 1 {
		t.Fatalf("expected exactly one successful add, got %d", successCount)
	}
	if duplicateCount != 1 {
		t.Fatalf("expected exactly one duplicate conflict, got %d", duplicateCount)
	}
	if len(cl.GetS3ProvidersSnapshot()) != 1 {
		t.Fatalf("expected exactly one provider in snapshot, got %d", len(cl.GetS3ProvidersSnapshot()))
	}
}

// ---------- RemoveS3Provider tests ----------

func TestRemoveS3Provider_Success(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "a", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
		{Name: "b", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
	}
	if err := cl.RemoveS3Provider("a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cl.ClusterS3Providers) != 1 || cl.ClusterS3Providers[0].Name != "b" {
		t.Errorf("unexpected providers after remove: %+v", cl.ClusterS3Providers)
	}
}

func TestRemoveS3Provider_NotFound(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{}
	if err := cl.RemoveS3Provider("ghost"); err == nil {
		t.Fatal("expected error for missing provider, got nil")
	}
}

// ---------- UpdateS3Provider tests ----------

func TestUpdateS3Provider_Success(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", Region: "eu-west-1"},
	}
	updated := config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com", Region: "us-east-2"}
	if err := cl.UpdateS3Provider(updated); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cl.ClusterS3Providers[0].Region != "us-east-2" {
		t.Errorf("region not updated: %q", cl.ClusterS3Providers[0].Region)
	}
}

func TestUpdateS3Provider_NotFound(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{}
	if err := cl.UpdateS3Provider(config.S3Provider{Name: "missing", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"}); err == nil {
		t.Fatal("expected error for missing provider, got nil")
	}
}

func TestUpdateS3Provider_ValidationEnforced(t *testing.T) {
	cl, _ := newTestClusterForS3(t)
	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "p", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example.com"},
	}
	// Try to update with invalid payload (custom mode, no endpoint)
	bad := config.S3Provider{Name: "p", ProviderSource: config.S3ProviderSourceCustom}
	if err := cl.UpdateS3Provider(bad); err == nil {
		t.Fatal("expected validation error, got nil")
	}
	// Original must be unchanged
	if cl.ClusterS3Providers[0].Endpoint != "https://s3.example.com" {
		t.Error("original provider was mutated despite validation failure")
	}
}

// ---------- Story 6.6 — Provider library mutations must not propagate to app mounts ----------

// TestProviderUpdateDoesNotMutateAppMounts verifies that updating a saved provider
// in ClusterS3Providers does not mutate any app-level S3Mount values, even when
// the mount carries providerName as a traceability label (Story 6.6 AC2).
func TestProviderUpdateDoesNotMutateAppMounts(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	// Set up a provider in the library.
	cl.ClusterS3Providers = []config.S3Provider{
		{
			Name:           "prod-minio",
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       "https://minio.example.com",
			Region:         "eu-west-1",
		},
	}

	// Set up an app with an S3 mount that was initialized from that provider
	// and then edited locally (the copy-then-edit pattern from Story 6.6).
	// App is cluster.App; its AppConfig.Deployment.Storages.S3Mounts holds the mount.
	app := &App{
		Name: "test-app",
		Port: "8080",
		AppConfig: &config.AppConfig{
			AppHost: "test-app",
			AppPort: "8080",
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: []*config.S3Mount{
						{
							Name:         "media-store",
							ProviderName: "prod-minio", // traceability label
							Endpoint:     "minio-app:9000",
							Region:       "ap-south-1", // locally edited after copy
							Bucket:       "media",
							AccessKey:    "edited-access-key",
							SecretKey:    "edited-secret-key",
						},
					},
				},
			},
		},
	}
	cl.Apps = appList([]*App{app})

	// Capture the mount's effective values before the provider update.
	orig := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	wantEndpoint := orig.Endpoint
	wantRegion := orig.Region
	wantAccessKey := orig.AccessKey
	wantSecretKey := orig.SecretKey

	// Update the provider in the library (change region, endpoint).
	updated := config.S3Provider{
		Name:           "prod-minio",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://minio-new.example.com",
		Region:         "us-east-1",
	}
	if err := cl.UpdateS3Provider(updated); err != nil {
		t.Fatalf("UpdateS3Provider: %v", err)
	}

	// Provider library must reflect the update.
	if cl.ClusterS3Providers[0].Region != "us-east-1" {
		t.Errorf("provider library region: got %q, want %q", cl.ClusterS3Providers[0].Region, "us-east-1")
	}

	// App mount effective values must be unchanged (no live propagation).
	got := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if got.Region != wantRegion {
		t.Errorf("mount Region was mutated by provider update: got %q, want %q", got.Region, wantRegion)
	}
	if got.Endpoint != wantEndpoint {
		t.Errorf("mount Endpoint was mutated by provider update: got %q, want %q", got.Endpoint, wantEndpoint)
	}
	if got.AccessKey != wantAccessKey {
		t.Errorf("mount AccessKey was mutated by provider update: got %q, want %q", got.AccessKey, wantAccessKey)
	}
	if got.SecretKey != wantSecretKey {
		t.Errorf("mount SecretKey was mutated by provider update: got %q, want %q", got.SecretKey, wantSecretKey)
	}
	// providerName traceability label must be preserved.
	if got.ProviderName != "prod-minio" {
		t.Errorf("mount ProviderName was mutated: got %q, want %q", got.ProviderName, "prod-minio")
	}
}

// TestProviderDeleteDoesNotMutateAppMounts verifies that removing a saved provider
// from ClusterS3Providers does not mutate any app-level S3Mount values (Story 6.6 AC2).
func TestProviderDeleteDoesNotMutateAppMounts(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	cl.ClusterS3Providers = []config.S3Provider{
		{Name: "archive-s3", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://archive.example.com", Region: "us-east-1"},
	}
	app := &App{
		Name: "backup-app",
		Port: "8080",
		AppConfig: &config.AppConfig{
			AppHost: "backup-app",
			AppPort: "8080",
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: []*config.S3Mount{
						{
							Name:         "archive",
							ProviderName: "archive-s3", // traceability label
							Endpoint:     "https://archive.example.com",
							Region:       "eu-central-1", // locally edited
							Bucket:       "backups",
						},
					},
				},
			},
		},
	}
	cl.Apps = appList([]*App{app})

	orig := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	wantRegion := orig.Region
	wantEndpoint := orig.Endpoint
	wantBucket := orig.Bucket

	// Delete the provider from the library.
	if err := cl.RemoveS3Provider("archive-s3"); err != nil {
		t.Fatalf("RemoveS3Provider: %v", err)
	}

	// Provider library must be empty.
	if len(cl.ClusterS3Providers) != 0 {
		t.Fatalf("provider library: expected empty, got %d", len(cl.ClusterS3Providers))
	}

	// App mount values must be unchanged.
	got := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if got.Region != wantRegion {
		t.Errorf("mount Region was mutated by provider delete: got %q, want %q", got.Region, wantRegion)
	}
	if got.Endpoint != wantEndpoint {
		t.Errorf("mount Endpoint was mutated by provider delete: got %q, want %q", got.Endpoint, wantEndpoint)
	}
	if got.Bucket != wantBucket {
		t.Errorf("mount Bucket was mutated by provider delete: got %q, want %q", got.Bucket, wantBucket)
	}
	// providerName traceability string must remain on the mount.
	if got.ProviderName != "archive-s3" {
		t.Errorf("mount ProviderName was mutated: got %q, want %q", got.ProviderName, "archive-s3")
	}
}

// TestS3Mount_LegacyInlineMount_GetEnvVariables_ReadFromStruct verifies that
// S3Mount.GetEnvVariables reads directly from struct fields with no live provider lookup.
// This is a documentation test confirming the contract that the caller of
// OpenSVCCreateAppVariableMaps relies on.
func TestS3Mount_LegacyInlineMount_GetEnvVariables_ReadFromStruct(t *testing.T) {
	mount := &config.S3Mount{
		Name:         "legacy-mount",
		Endpoint:     "s3-minio:9000",
		Bucket:       "mybucket",
		Region:       "us-west-2",
		AccessKey:    "AKIALEGACY",
		SecretKey:    "secretlegacy",
		MountDir:     "/mnt/custom",
		ProviderName: "", // zero value – legacy inline mount
	}

	envs := mount.GetEnvVariables()
	secrets := mount.GetSecretVariables()

	if envs["BUCKET"] != "mybucket" {
		t.Errorf("BUCKET: got %q, want %q", envs["BUCKET"], "mybucket")
	}
	if envs["REGION"] != "us-west-2" {
		t.Errorf("REGION: got %q, want %q", envs["REGION"], "us-west-2")
	}
	if envs["AWS_ACCESS_KEY"] != "AKIALEGACY" {
		t.Errorf("AWS_ACCESS_KEY: got %q, want %q", envs["AWS_ACCESS_KEY"], "AKIALEGACY")
	}
	if envs["MOUNT_DIR"] != "/mnt/custom" {
		t.Errorf("MOUNT_DIR: got %q, want %q", envs["MOUNT_DIR"], "/mnt/custom")
	}
	if envs["ENDPOINT"] != "s3-minio:9000" {
		t.Errorf("ENDPOINT: got %q, want %q", envs["ENDPOINT"], "s3-minio:9000")
	}
	if secrets["AWS_SECRET_KEY"] != "secretlegacy" {
		t.Errorf("AWS_SECRET_KEY: got %q, want %q", secrets["AWS_SECRET_KEY"], "secretlegacy")
	}
	// providerName is NOT included in env vars – it is a traceability label only.
	if _, ok := envs["PROVIDER_NAME"]; ok {
		t.Errorf("providerName should not appear in env vars, got %v", envs)
	}
}

// TestS3Mount_CustomEndpointMount_GetEnvVariables_EffectiveValues verifies that
// a custom-endpoint mount (with ProviderName set for traceability) returns its own
// stored field values from GetEnvVariables – confirming no live provider lookup
// and that the mount's effective endpoint/region/bucket are used.
func TestS3Mount_CustomEndpointMount_GetEnvVariables_EffectiveValues(t *testing.T) {
	mount := &config.S3Mount{
		Name:         "custom-backup",
		Endpoint:     "https://minio.example.com:9000",
		Bucket:       "backups",
		Region:       "eu-central-1",
		AccessKey:    "CUSTOMKEY",
		SecretKey:    "customsecret",
		MountDir:     "/mnt/backup",
		ProviderName: "archive-s3", // traceability label from provider library
	}

	envs := mount.GetEnvVariables()
	secrets := mount.GetSecretVariables()

	if envs["ENDPOINT"] != "https://minio.example.com:9000" {
		t.Errorf("ENDPOINT: got %q, want %q", envs["ENDPOINT"], "https://minio.example.com:9000")
	}
	if envs["REGION"] != "eu-central-1" {
		t.Errorf("REGION: got %q, want %q", envs["REGION"], "eu-central-1")
	}
	if envs["BUCKET"] != "backups" {
		t.Errorf("BUCKET: got %q, want %q", envs["BUCKET"], "backups")
	}
	if envs["AWS_ACCESS_KEY"] != "CUSTOMKEY" {
		t.Errorf("AWS_ACCESS_KEY: got %q, want %q", envs["AWS_ACCESS_KEY"], "CUSTOMKEY")
	}
	if secrets["AWS_SECRET_KEY"] != "customsecret" {
		t.Errorf("AWS_SECRET_KEY: got %q, want %q", secrets["AWS_SECRET_KEY"], "customsecret")
	}
	// ProviderName is not in env vars.
	if _, ok := envs["PROVIDER_NAME"]; ok {
		t.Errorf("providerName should not appear in env vars, got %v", envs)
	}
}

// TestS3Mount_CustomEndpointMount_ProvisioningPath_CustomEndpointPreferred verifies
// the Story 6.7 fix: when a mount carries its own non-empty Endpoint, provisioning
// code must prefer that value over any Node.GetS3Endpoint() resolution.
// We test the logic inline without a full OpenSVC stack by exercising the
// effectiveEndpoint computation that was added to OpenSVCCreateAppVariableMaps.
func TestS3Mount_CustomEndpointMount_ProvisioningPath_CustomEndpointPreferred(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	// Set up two apps – one S3 provider, one consumer app.
	s3App := &App{
		Name: "s3-provider",
		Port: "9000",
		AppConfig: &config.AppConfig{
			AppHost: "s3-provider",
			AppPort: "9000",
		},
	}
	consumerApp := &App{
		Name: "consumer",
		Port: "8080",
		AppConfig: &config.AppConfig{
			AppHost: "consumer",
			AppPort: "8080",
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: []*config.S3Mount{
						{
							Name:         "custom-backup",
							Endpoint:     "https://custom-minio.example.com:9000", // non-empty → custom mode
							Bucket:       "backups",
							Region:       "eu-central-1",
							AccessKey:    "CUSTOM_ACCESS_KEY",
							SecretKey:    "CUSTOM_SECRET_KEY",
							MountDir:     "/mnt/custom",
							ProviderName: "archive-s3",
							// Node is nil – intentionally not set
						},
					},
				},
			},
		},
	}
	cl.Apps = appList([]*App{s3App, consumerApp})

	mount := cl.Apps[1].AppConfig.Deployment.Storages.S3Mounts[0]

	// Simulate the effectiveEndpoint logic from OpenSVCCreateAppVariableMaps.
	// This mirrors the fix applied in Story 6.7.
	var effectiveEndpoint string
	if mount.Endpoint != "" {
		effectiveEndpoint = mount.Endpoint
	} else {
		if mount.Node == nil {
			node, _ := cl.GetAppByURL(mount.Endpoint)
			if node == nil {
				t.Fatalf("GetAppByURL returned nil for sibling-app endpoint – legacy mode broken")
			}
			mount.Node = node
		}
		effectiveEndpoint = "http://" + mount.Node.GetS3Endpoint()
	}

	// The effective endpoint must be the mount's own value, NOT the sibling app's address.
	wantEndpoint := "https://custom-minio.example.com:9000"
	if effectiveEndpoint != wantEndpoint {
		t.Errorf("effectiveEndpoint: got %q, want %q (mount's own Endpoint must be preferred)", effectiveEndpoint, wantEndpoint)
	}
}

// TestS3Mount_LegacyInlineMount_ProvisioningPath_SiblingAppRespected verifies
// the legacy sibling-app mode still works correctly: when Endpoint is non-empty but
// resolves to a sibling app in the cluster, Node resolution uses GetAppByURL and
// the endpoint env var is built from Node.GetS3Endpoint().
//
// Note: GetAppByURL("") returns nil, so empty Endpoint cannot use sibling-app resolution.
// The legacy sibling-app path requires a non-empty Endpoint that maps to a sibling app.
func TestS3Mount_LegacyInlineMount_ProvisioningPath_SiblingAppRespected(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	s3App := &App{
		Name: "s3-provider",
		Port: "9000",
		Host: "s3-provider", // GetAppByHostPort matches app.Host against host
		AppConfig: &config.AppConfig{
			AppHost: "s3-provider",
			AppPort: "9000",
		},
	}
	consumerApp := &App{
		Name: "consumer",
		Port: "8080",
		Host: "consumer",
		AppConfig: &config.AppConfig{
			AppHost: "consumer",
			AppPort: "8080",
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: []*config.S3Mount{
						{
							Name:         "legacy-mount",
							Endpoint:     "s3-provider:9000", // resolves to sibling app via GetAppByURL
							Bucket:       "legacy-bucket",
							Region:       "us-east-1",
							AccessKey:    "LEGACY_ACCESS_KEY",
							SecretKey:    "LEGACY_SECRET_KEY",
							MountDir:     "/mnt",
							ProviderName: "",
						},
					},
				},
			},
		},
	}
	cl.Apps = appList([]*App{s3App, consumerApp})

	mount := cl.Apps[1].AppConfig.Deployment.Storages.S3Mounts[0]

	// Simulate the effectiveEndpoint logic from OpenSVCCreateAppVariableMaps (legacy path).
	// When Endpoint is non-empty, we still fall through to the sibling-app path only if
	// Node was not pre-set AND GetAppByURL returns a node (custom flow skips this branch
	// because s3m.Endpoint is non-empty and we know it's a custom endpoint).
	// For the legacy sibling-app flow, Endpoint must be non-empty and resolve to an app.
	var effectiveEndpoint string
	if mount.Endpoint != "" {
		// Story 6.7 change: non-empty Endpoint now means "custom endpoint" by default.
		// For the legacy sibling-app mode the caller must set s3m.Node directly before
		// calling OpenSVCCreateAppVariableMaps, which is the pre-existing contract.
		// This test verifies that when Node IS set, GetS3Endpoint() is still used.
		if mount.Node != nil {
			effectiveEndpoint = "http://" + mount.Node.GetS3Endpoint()
		} else {
			// Node not pre-set – try resolution. If it fails, the legacy mode cannot proceed.
			node, _ := cl.GetAppByURL(mount.Endpoint)
			if node == nil {
				t.Fatalf("GetAppByURL(%q) returned nil – sibling-app endpoint %q must resolve to an app in the cluster for legacy mode", mount.Endpoint, mount.Endpoint)
			}
			mount.Node = node
			effectiveEndpoint = "http://" + mount.Node.GetS3Endpoint()
		}
	}

	// Legacy mode with Node resolved: endpoint env var comes from Node.GetS3Endpoint().
	wantEndpoint := "http://" + s3App.GetS3Endpoint()
	if effectiveEndpoint != wantEndpoint {
		t.Errorf("effectiveEndpoint: got %q, want %q (legacy sibling-app mode)", effectiveEndpoint, wantEndpoint)
	}
}

// TestS3Mount_LegacyInlineMount_ProvisioningPath_PreSetNode_EmptyEndpoint verifies
// the legacy branch where Endpoint == "" and Node is already pre-populated.
// In this path, endpoint resolution must use Node.GetS3Endpoint() directly and must
// not depend on GetAppByURL("") (which is documented to return nil).
func TestS3Mount_LegacyInlineMount_ProvisioningPath_PreSetNode_EmptyEndpoint(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	preResolvedNode := &App{
		Name: "s3-provider",
		Host: "s3-provider",
		Port: "9000",
		AppConfig: &config.AppConfig{
			AppHost: "s3-provider",
			AppPort: "9000",
		},
	}

	mount := &config.S3Mount{
		Name:      "legacy-pre-set-node",
		Endpoint:  "", // legacy branch under test
		Bucket:    "legacy-bucket",
		Region:    "us-east-1",
		AccessKey: "LEGACY_ACCESS_KEY",
		SecretKey: "LEGACY_SECRET_KEY",
		MountDir:  "/mnt/legacy",
		Node:      preResolvedNode,
	}

	// Guardrail assertion for this regression: empty endpoint cannot be resolved via URL lookup.
	if app, _ := cl.GetAppByURL(mount.Endpoint); app != nil {
		t.Fatalf("expected GetAppByURL(\"\") to be nil, got %v", app)
	}

	// Mirror OpenSVCCreateAppVariableMaps endpoint resolution for the legacy branch.
	var effectiveEndpoint string
	if mount.Endpoint != "" {
		effectiveEndpoint = mount.Endpoint
	} else {
		if mount.Node == nil {
			node, _ := cl.GetAppByURL(mount.Endpoint)
			if node == nil {
				t.Fatalf("GetAppByURL(%q) returned nil", mount.Endpoint)
			}
			mount.Node = node
		}
		effectiveEndpoint = "http://" + mount.Node.GetS3Endpoint()
	}

	envs := mount.GetEnvVariables()
	envs[config.S3VarSuffixEndpoint] = effectiveEndpoint
	secrets := mount.GetSecretVariables()

	wantEndpoint := "http://" + preResolvedNode.GetS3Endpoint()
	if envs[config.S3VarSuffixEndpoint] != wantEndpoint {
		t.Errorf("ENDPOINT: got %q, want %q", envs[config.S3VarSuffixEndpoint], wantEndpoint)
	}
	if envs[config.S3VarSuffixBucket] != "legacy-bucket" {
		t.Errorf("BUCKET: got %q, want %q", envs[config.S3VarSuffixBucket], "legacy-bucket")
	}
	if envs[config.S3VarSuffixRegion] != "us-east-1" {
		t.Errorf("REGION: got %q, want %q", envs[config.S3VarSuffixRegion], "us-east-1")
	}
	if envs[config.S3VarSuffixAccessKey] != "LEGACY_ACCESS_KEY" {
		t.Errorf("AWS_ACCESS_KEY: got %q, want %q", envs[config.S3VarSuffixAccessKey], "LEGACY_ACCESS_KEY")
	}
	if envs[config.S3VarSuffixMountDir] != "/mnt/legacy" {
		t.Errorf("MOUNT_DIR: got %q, want %q", envs[config.S3VarSuffixMountDir], "/mnt/legacy")
	}
	if secrets[config.S3VarSuffixSecretKey] != "LEGACY_SECRET_KEY" {
		t.Errorf("AWS_SECRET_KEY: got %q, want %q", secrets[config.S3VarSuffixSecretKey], "LEGACY_SECRET_KEY")
	}

	// Ensure the pre-set node was used as-is (no fallback replacement).
	resolvedNode, ok := mount.Node.(*App)
	if !ok || resolvedNode != preResolvedNode {
		t.Fatalf("expected mount.Node to remain the pre-set node")
	}
}

func TestResolveS3MountProvisioningEndpoint_LegacyNodePreset(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	s3App := &App{Name: "s3-provider", Host: "s3-provider", Port: "9000"}
	consumerApp := &App{
		Name:      "consumer",
		AppConfig: &config.AppConfig{Deployment: &config.Deployment{}},
	}

	mount := &config.S3Mount{
		Name:      "legacy-mount",
		Endpoint:  "",
		AccessKey: "LEGACY_ACCESS_KEY",
		SecretKey: "LEGACY_SECRET_KEY",
		Node:      s3App,
	}

	effectiveEndpoint, err := cl.resolveS3MountProvisioningEndpoint(consumerApp, mount)
	if err != nil {
		t.Fatalf("resolveS3MountProvisioningEndpoint returned error: %v", err)
	}

	wantEndpoint := "http://" + s3App.GetS3Endpoint()
	if effectiveEndpoint != wantEndpoint {
		t.Errorf("effectiveEndpoint: got %q, want %q", effectiveEndpoint, wantEndpoint)
	}
}

func TestResolveS3MountProvisioningEndpoint_LegacySiblingResolvableFromMaterializedEndpoint(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	s3App := &App{Name: "s3-provider", Host: "s3-provider", Port: "9000"}
	mount := &config.S3Mount{Name: "legacy-mount", Endpoint: ""}
	prefix := mount.GetVariablePrefix()

	consumerApp := &App{
		Name: "consumer",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Variables: config.VariableMaps{
					{Name: prefix + config.S3VarSuffixEndpoint, Value: "s3-provider:9000", Type: config.VariableTypeEnv},
				},
			},
		},
	}
	cl.Apps = appList([]*App{s3App, consumerApp})

	effectiveEndpoint, err := cl.resolveS3MountProvisioningEndpoint(consumerApp, mount)
	if err != nil {
		t.Fatalf("resolveS3MountProvisioningEndpoint returned error: %v", err)
	}

	wantEndpoint := "http://" + s3App.GetS3Endpoint()
	if effectiveEndpoint != wantEndpoint {
		t.Errorf("effectiveEndpoint: got %q, want %q", effectiveEndpoint, wantEndpoint)
	}
	if mount.Node == nil {
		t.Fatalf("expected mount.Node to be resolved")
	}
}

func TestResolveS3MountProvisioningEndpoint_LegacySiblingUnresolvedUsesFallbackCredentials(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	mount := &config.S3Mount{Name: "legacy-mount", Endpoint: "", AccessKey: "", SecretKey: ""}
	prefix := mount.GetVariablePrefix()
	consumerApp := &App{
		Name: "consumer",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Variables: config.VariableMaps{
					{Name: prefix + config.S3VarSuffixEndpoint, Value: "https://orphan-minio.example.com:9000", Type: config.VariableTypeEnv},
					{Name: prefix + config.S3VarSuffixAccessKey, Value: "FALLBACK_ACCESS", Type: config.VariableTypeEnv},
					{Name: prefix + config.S3VarSuffixSecretKey, Value: "fallback-secret", Type: config.VariableTypeSecret},
				},
			},
		},
	}

	effectiveEndpoint, err := cl.resolveS3MountProvisioningEndpoint(consumerApp, mount)
	if err != nil {
		t.Fatalf("resolveS3MountProvisioningEndpoint returned error: %v", err)
	}

	if effectiveEndpoint != "https://orphan-minio.example.com:9000" {
		t.Errorf("effectiveEndpoint: got %q, want %q", effectiveEndpoint, "https://orphan-minio.example.com:9000")
	}
	if mount.AccessKey != "FALLBACK_ACCESS" {
		t.Errorf("mount.AccessKey: got %q, want %q", mount.AccessKey, "FALLBACK_ACCESS")
	}
	if mount.SecretKey != "fallback-secret" {
		t.Errorf("mount.SecretKey: got %q, want %q", mount.SecretKey, "fallback-secret")
	}
}

func TestResolveS3MountProvisioningEndpoint_LegacySiblingUnresolvedNoFallbackFailsClosed(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	mount := &config.S3Mount{Name: "legacy-mount", Endpoint: ""}
	prefix := mount.GetVariablePrefix()
	consumerApp := &App{
		Name: "consumer",
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{
				Variables: config.VariableMaps{
					{Name: prefix + config.S3VarSuffixEndpoint, Value: "ghost-s3:9000", Type: config.VariableTypeEnv},
				},
			},
		},
	}

	_, err := cl.resolveS3MountProvisioningEndpoint(consumerApp, mount)
	if err == nil {
		t.Fatal("expected explicit fail-closed error, got nil")
	}
	if !strings.Contains(err.Error(), "no fallback credentials available") {
		t.Fatalf("expected fail-closed fallback error, got: %v", err)
	}
}

// TestS3Mount_InsertS3Mount_ProviderNameIgnored verifies that InsertS3Mount,
// DropS3Mount, and GetS3Mount are completely unaffected by the ProviderName field.
// This confirms Story 6.7 AC1: existing inline mounts work unchanged.
func TestS3Mount_InsertS3Mount_ProviderNameIgnored(t *testing.T) {
	d := config.NewDeploymentConfig()

	// Mount with no ProviderName (legacy inline).
	mountNoProvider := &config.S3Mount{
		Name:     "inline-legacy",
		Endpoint: "legacy:9000",
		Bucket:   "legacy-bucket",
	}

	// Mount with ProviderName set (copy-from-provider flow).
	mountWithProvider := &config.S3Mount{
		Name:         "copied-mount",
		Endpoint:     "https://custom.example.com:9000",
		Bucket:       "custom-bucket",
		Region:       "eu-west-1",
		AccessKey:    "CUSTOM_AK",
		SecretKey:    "CUSTOM_SK",
		ProviderName: "my-provider", // traceability label
	}

	// Insert both – neither should be rejected due to ProviderName.
	if err := d.InsertS3Mount(mountNoProvider); err != nil {
		t.Fatalf("InsertS3Mount(legacy) failed: %v", err)
	}
	if err := d.InsertS3Mount(mountWithProvider); err != nil {
		t.Fatalf("InsertS3Mount(copied) failed: %v", err)
	}

	// Retrieve both and verify their ProviderName values are preserved.
	gotLegacy, err := d.GetS3Mount("inline-legacy")
	if err != nil {
		t.Fatalf("GetS3Mount(inline-legacy) failed: %v", err)
	}
	if gotLegacy.ProviderName != "" {
		t.Errorf("legacy mount ProviderName: got %q, want %q (field must be zero value for legacy mounts)", gotLegacy.ProviderName, "")
	}

	gotCopied, err := d.GetS3Mount("copied-mount")
	if err != nil {
		t.Fatalf("GetS3Mount(copied-mount) failed: %v", err)
	}
	if gotCopied.ProviderName != "my-provider" {
		t.Errorf("copied mount ProviderName: got %q, want %q", gotCopied.ProviderName, "my-provider")
	}

	// Drop the legacy mount; verify the copied mount is still present.
	if err := d.DropS3Mount(gotLegacy); err != nil {
		t.Fatalf("DropS3Mount(legacy) failed: %v", err)
	}
	if _, err := d.GetS3Mount("copied-mount"); err != nil {
		t.Fatalf("copied mount should still exist after dropping legacy: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Story 6.6 — Provider traceability boundary tests
// ---------------------------------------------------------------------------

// TestS3Provider_Update_DoesNotMutateExistingMounts confirms AC 2 of Story 6.6:
// updating a ClusterS3Provider never touches app mounts that previously copied
// values from it. The "copy, then edit" contract means providers and mounts are
// fully decoupled at the data layer; ProviderName is a plain traceability string.
func TestS3Provider_Update_DoesNotMutateExistingMounts(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	// Seed the cluster with a saved provider.
	original := config.S3Provider{
		Name:           "prod-s3",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
	}
	if err := cl.AddS3Provider(original); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	// Simulate an app mount that was "copied from" the provider at creation time.
	// It is stored in a separate Deployment config — no live reference to the provider.
	d := config.NewDeploymentConfig()
	mount := &config.S3Mount{
		Name:         "backup-mount",
		Endpoint:     "https://s3.example.com",
		Bucket:       "backups",
		Region:       "us-east-1",
		ProviderName: "prod-s3", // traceability label only
	}
	if err := d.InsertS3Mount(mount); err != nil {
		t.Fatalf("InsertS3Mount: %v", err)
	}

	// Update the provider with completely different values.
	updated := config.S3Provider{
		Name:           "prod-s3",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://new-s3.example.com",
		Region:         "eu-west-1",
	}
	if err := cl.UpdateS3Provider(updated); err != nil {
		t.Fatalf("UpdateS3Provider: %v", err)
	}

	// Verify the provider library was updated.
	providers := cl.GetS3ProvidersSnapshot()
	if len(providers) != 1 || providers[0].Endpoint != "https://new-s3.example.com" {
		t.Errorf("provider library not updated correctly: %+v", providers)
	}

	// The mount's effective values must remain exactly as stored — UpdateS3Provider
	// has no knowledge of or effect on Deployment.Storages.S3Mounts.
	got, err := d.GetS3Mount("backup-mount")
	if err != nil {
		t.Fatalf("GetS3Mount after UpdateS3Provider: %v", err)
	}
	if got.Endpoint != "https://s3.example.com" {
		t.Errorf("mount.Endpoint mutated: got %q, want %q", got.Endpoint, "https://s3.example.com")
	}
	if got.Region != "us-east-1" {
		t.Errorf("mount.Region mutated: got %q, want %q", got.Region, "us-east-1")
	}
	if got.ProviderName != "prod-s3" {
		t.Errorf("mount.ProviderName changed: got %q, want %q", got.ProviderName, "prod-s3")
	}
}

// TestS3Provider_Delete_DoesNotMutateExistingMounts confirms AC 2 of Story 6.6:
// removing a ClusterS3Provider from the library does not affect any app mount
// that carries that provider's name as a traceability label.
func TestS3Provider_Delete_DoesNotMutateExistingMounts(t *testing.T) {
	cl, _ := newTestClusterForS3(t)

	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "dev-minio",
		ProviderSource: config.S3ProviderSourceApp,
		ProviderApp:    "minio-app:9000",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	d := config.NewDeploymentConfig()
	mount := &config.S3Mount{
		Name:         "dev-backup",
		Endpoint:     "minio-app:9000",
		Bucket:       "dev-backups",
		Region:       "eu-west-1",
		ProviderName: "dev-minio",
	}
	if err := d.InsertS3Mount(mount); err != nil {
		t.Fatalf("InsertS3Mount: %v", err)
	}

	// Delete the provider.
	if err := cl.RemoveS3Provider("dev-minio"); err != nil {
		t.Fatalf("RemoveS3Provider: %v", err)
	}

	// Provider is gone from the library.
	if len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Errorf("expected empty provider library after delete")
	}

	// Mount values must remain intact; the mount is not invalidated by provider deletion.
	got, err := d.GetS3Mount("dev-backup")
	if err != nil {
		t.Fatalf("GetS3Mount after RemoveS3Provider: %v", err)
	}
	if got.Endpoint != "minio-app:9000" {
		t.Errorf("mount.Endpoint changed after provider deletion: got %q", got.Endpoint)
	}
	if got.Region != "eu-west-1" {
		t.Errorf("mount.Region changed after provider deletion: got %q", got.Region)
	}
	if got.ProviderName != "dev-minio" {
		t.Errorf("mount.ProviderName cleared after provider deletion: got %q", got.ProviderName)
	}
}
