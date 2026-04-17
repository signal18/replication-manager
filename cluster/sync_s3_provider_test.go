// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// newTestClusterForSync builds a minimal test cluster with one provider and one app.
// The app has id "app-a", one S3 mount "media", and the cluster has one provider
// "minio-prod" (custom mode) pointing to "https://minio.example.com".
func newTestClusterForSync(t *testing.T) *Cluster {
	t.Helper()
	cl, _ := newTestClusterForS3(t)
	if err := os.MkdirAll(filepath.Join(cl.WorkingDir, "apps"), 0755); err != nil {
		t.Fatalf("create apps dir: %v", err)
	}
	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "minio-prod",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://minio.example.com",
		Region:         "us-east-1",
		AccessKey:      "AK_PROVIDER",
		SecretKey:      "SK_PROVIDER",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}
	app := &App{
		Id:   "app-a",
		Name: "app-a",
		Port: "8080",
		AppConfig: &config.AppConfig{
			AppHost: "app-a",
			AppPort: "8080",
			Deployment: &config.Deployment{
				Storages: config.StorageMapping{
					S3Mounts: []*config.S3Mount{
						{
							Name:         "media",
							Endpoint:     "https://old.example.com",
							Region:       "eu-west-1",
							AccessKey:    "AK_OLD",
							SecretKey:    "SK_OLD",
							Bucket:       "media-bucket",
							MountDir:     "/data/media",
							VolumeName:   "vol1",
							VolumeDir:    "/vol1",
							ProviderName: "minio-prod",
						},
					},
				},
			},
		},
	}
	cl.Apps = appList([]*App{app})
	return cl
}

func previewTokenForTargets(cl *Cluster, providerName string, targets []SyncTarget) string {
	return cl.PreviewS3ProviderSync(providerName, targets).RevisionToken
}

// ---- Preview tests ----

// TestPreviewS3ProviderSync_WillChange verifies that when mount values differ from
// the provider, preview returns SyncStatusWillChange with a non-empty Changes slice
// and does NOT mutate the mount.
func TestPreviewS3ProviderSync_WillChange(t *testing.T) {
	cl := newTestClusterForSync(t)
	mountBefore := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	if resp.DryRun != true {
		t.Errorf("DryRun: got %v, want true", resp.DryRun)
	}
	if resp.ProviderName != "minio-prod" {
		t.Errorf("ProviderName: got %q, want %q", resp.ProviderName, "minio-prod")
	}
	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncStatusWillChange {
		t.Errorf("Status: got %q, want %q", r.Status, SyncStatusWillChange)
	}
	if len(r.Changes) == 0 {
		t.Error("Changes must be non-empty for will_change")
	}
	if len(r.Warnings) < 2 {
		t.Fatalf("Warnings: got %d, want at least 2", len(r.Warnings))
	}
	if r.Warnings[0] != "Local provider-managed fields will be overwritten by provider values." {
		t.Errorf("Warnings[0]: got %q", r.Warnings[0])
	}
	if r.Warnings[1] != "This mount has been customized from the provider template." {
		t.Errorf("Warnings[1]: got %q", r.Warnings[1])
	}

	// Verify summary counts.
	if resp.Summary.WillChange != 1 {
		t.Errorf("Summary.WillChange: got %d, want 1", resp.Summary.WillChange)
	}
	if resp.Summary.Unchanged != 0 {
		t.Errorf("Summary.Unchanged: got %d, want 0", resp.Summary.Unchanged)
	}

	// Verify preserved fields are present in the response.
	if r.PreservedFields["bucket"] != "media-bucket" {
		t.Errorf("preservedFields.bucket: got %q, want %q", r.PreservedFields["bucket"], "media-bucket")
	}
	if r.PreservedFields["mountdir"] != "/data/media" {
		t.Errorf("preservedFields.mountdir: got %q, want %q", r.PreservedFields["mountdir"], "/data/media")
	}

	// CRITICAL: preview must NOT mutate the mount.
	mountAfter := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if mountAfter.Endpoint != mountBefore.Endpoint {
		t.Errorf("Endpoint was mutated by preview: got %q, want %q", mountAfter.Endpoint, mountBefore.Endpoint)
	}
	if mountAfter.AccessKey != mountBefore.AccessKey {
		t.Errorf("AccessKey was mutated by preview: got %q, want %q", mountAfter.AccessKey, mountBefore.AccessKey)
	}
}

// TestPreviewS3ProviderSync_NoChange verifies that when mount values already match
// the provider, preview returns SyncStatusNoChange with an empty Changes slice.
func TestPreviewS3ProviderSync_NoChange(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Align mount with provider values.
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	m.Endpoint = "https://minio.example.com"
	m.Region = "us-east-1"
	m.AccessKey = "AK_PROVIDER"
	m.SecretKey = "SK_PROVIDER"

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncStatusNoChange {
		t.Errorf("Status: got %q, want %q", r.Status, SyncStatusNoChange)
	}
	if len(r.Changes) != 0 {
		t.Errorf("Changes must be empty for no_change; got %v", r.Changes)
	}
	if resp.Summary.Unchanged != 1 {
		t.Errorf("Summary.Unchanged: got %d, want 1", resp.Summary.Unchanged)
	}
}

// TestPreviewS3ProviderSync_ProviderMissing verifies AC6: when the named provider
// does not exist, every target gets SyncStatusProviderMissing and no state changes.
func TestPreviewS3ProviderSync_ProviderMissing(t *testing.T) {
	cl := newTestClusterForSync(t)
	mountBefore := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]

	resp := cl.PreviewS3ProviderSync("nonexistent", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncStatusProviderMissing {
		t.Errorf("Status: got %q, want %q", resp.Results[0].Status, SyncStatusProviderMissing)
	}

	// No mutation.
	mountAfter := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if mountAfter.Endpoint != mountBefore.Endpoint {
		t.Errorf("Endpoint was mutated by provider-missing preview")
	}
}

// TestPreviewS3ProviderSync_UnknownApp verifies that an unknown appId produces
// SyncStatusError and does not panic.
func TestPreviewS3ProviderSync_UnknownApp(t *testing.T) {
	cl := newTestClusterForSync(t)

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "does-not-exist", MountName: "media"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncStatusError {
		t.Errorf("Status: got %q, want %q", resp.Results[0].Status, SyncStatusError)
	}
}

// TestPreviewS3ProviderSync_UnknownMount verifies that an unknown mountName
// produces SyncStatusError.
func TestPreviewS3ProviderSync_UnknownMount(t *testing.T) {
	cl := newTestClusterForSync(t)

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "does-not-exist"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncStatusError {
		t.Errorf("Status: got %q, want %q", resp.Results[0].Status, SyncStatusError)
	}
}

// TestPreviewS3ProviderSync_CredentialsMasked verifies that accessKey and secretKey
// values are replaced with credentialMask in preview Changes (never leaked in diffs).
func TestPreviewS3ProviderSync_CredentialsMasked(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Mount has different creds from provider — they should appear in Changes but masked.

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncStatusWillChange {
		t.Fatalf("Status: got %q, want %q", r.Status, SyncStatusWillChange)
	}

	for _, change := range r.Changes {
		if change.Field == "accessKey" || change.Field == "secretKey" {
			if change.Before != credentialMask {
				t.Errorf("Changes[%s].Before = %q; want %q (must be masked)", change.Field, change.Before, credentialMask)
			}
			if change.After != credentialMask {
				t.Errorf("Changes[%s].After = %q; want %q (must be masked)", change.Field, change.After, credentialMask)
			}
		}
	}
}

// TestPreviewS3ProviderSync_UnchangedFields verifies that provider-managed fields
// that already match the provider are listed in UnchangedFields (AC2).
func TestPreviewS3ProviderSync_UnchangedFields(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Align region and credentials with provider; leave endpoint and region differing.
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	m.Region = "us-east-1"
	m.AccessKey = "AK_PROVIDER"
	m.SecretKey = "SK_PROVIDER"
	// endpoint still differs ("https://old.example.com" vs "https://minio.example.com")

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncStatusWillChange {
		t.Fatalf("Status: got %q, want %q", r.Status, SyncStatusWillChange)
	}

	// region, accessKey, secretKey should be in UnchangedFields.
	unchangedSet := make(map[string]bool, len(r.UnchangedFields))
	for _, f := range r.UnchangedFields {
		unchangedSet[f] = true
	}
	for _, expected := range []string{"region", "accessKey", "secretKey"} {
		if !unchangedSet[expected] {
			t.Errorf("UnchangedFields missing %q; got %v", expected, r.UnchangedFields)
		}
	}
	// endpoint must NOT be in UnchangedFields — it differs.
	if unchangedSet["endpoint"] {
		t.Errorf("UnchangedFields must not contain %q (it differs)", "endpoint")
	}
}

// ---- Apply tests ----

// TestApplyS3ProviderSync_ChangesProviderManagedFields verifies AC3 and AC7: the
// apply operation overwrites endpoint, region, accessKey, and secretKey from the
// provider and returns SyncApplyStatusChanged with the field list.
func TestApplyS3ProviderSync_ChangesProviderManagedFields(t *testing.T) {
	cl := newTestClusterForSync(t)

	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	resp := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))

	if resp.DryRun != false {
		t.Errorf("DryRun: got %v, want false", resp.DryRun)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncApplyStatusChanged {
		t.Errorf("Status: got %q, want %q", r.Status, SyncApplyStatusChanged)
	}
	if len(r.ChangesApplied) == 0 {
		t.Error("ChangesApplied must be non-empty")
	}

	// Verify provider-managed fields were updated in the mount.
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if m.Endpoint != "https://minio.example.com" {
		t.Errorf("Endpoint: got %q, want %q", m.Endpoint, "https://minio.example.com")
	}
	if m.Region != "us-east-1" {
		t.Errorf("Region: got %q, want %q", m.Region, "us-east-1")
	}
	if m.AccessKey != "AK_PROVIDER" {
		t.Errorf("AccessKey: got %q, want %q", m.AccessKey, "AK_PROVIDER")
	}
	if m.SecretKey != "SK_PROVIDER" {
		t.Errorf("SecretKey: got %q, want %q", m.SecretKey, "SK_PROVIDER")
	}

	if resp.Summary.Changed != 1 {
		t.Errorf("Summary.Changed: got %d, want 1", resp.Summary.Changed)
	}
}

// TestApplyS3ProviderSync_PreservesMountSpecificFields verifies AC4: name, bucket,
// mountdir, volumename, volumedir, and providerName are never changed by apply.
func TestApplyS3ProviderSync_PreservesMountSpecificFields(t *testing.T) {
	cl := newTestClusterForSync(t)

	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))

	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if m.Name != "media" {
		t.Errorf("Name was changed: got %q, want %q", m.Name, "media")
	}
	if m.Bucket != "media-bucket" {
		t.Errorf("Bucket was changed: got %q, want %q", m.Bucket, "media-bucket")
	}
	if m.MountDir != "/data/media" {
		t.Errorf("MountDir was changed: got %q, want %q", m.MountDir, "/data/media")
	}
	if m.VolumeName != "vol1" {
		t.Errorf("VolumeName was changed: got %q, want %q", m.VolumeName, "vol1")
	}
	if m.VolumeDir != "/vol1" {
		t.Errorf("VolumeDir was changed: got %q, want %q", m.VolumeDir, "/vol1")
	}
	if m.ProviderName != "minio-prod" {
		t.Errorf("ProviderName was changed: got %q, want %q", m.ProviderName, "minio-prod")
	}
}

// TestApplyS3ProviderSync_NoChangeWhenAlreadyMatches verifies AC5: when the mount
// already matches the provider, apply returns SyncApplyStatusUnchanged.
func TestApplyS3ProviderSync_NoChangeWhenAlreadyMatches(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Align mount with provider values first.
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	m.Endpoint = "https://minio.example.com"
	m.Region = "us-east-1"
	m.AccessKey = "AK_PROVIDER"
	m.SecretKey = "SK_PROVIDER"

	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	resp := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncApplyStatusUnchanged {
		t.Errorf("Status: got %q, want %q", resp.Results[0].Status, SyncApplyStatusUnchanged)
	}
	if resp.Summary.Unchanged != 1 {
		t.Errorf("Summary.Unchanged: got %d, want 1", resp.Summary.Unchanged)
	}
}

// TestApplyS3ProviderSync_ProviderMissing verifies AC6: when the provider does not
// exist, apply returns SyncStatusProviderMissing and does NOT mutate the mount.
func TestApplyS3ProviderSync_ProviderMissing(t *testing.T) {
	cl := newTestClusterForSync(t)
	mountBefore := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]

	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	resp := cl.ApplyS3ProviderSync("nonexistent", targets, previewTokenForTargets(cl, "nonexistent", targets))

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncStatusProviderMissing {
		t.Errorf("Status: got %q, want %q", resp.Results[0].Status, SyncStatusProviderMissing)
	}

	mountAfter := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if mountAfter.Endpoint != mountBefore.Endpoint {
		t.Errorf("Endpoint mutated by provider-missing apply")
	}
}

// TestApplyS3ProviderSync_MultipleTargets verifies that batched apply processes
// all targets and returns the correct summary.
func TestApplyS3ProviderSync_MultipleTargets(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Add a second mount to the same app that already matches the provider.
	m2 := &config.S3Mount{
		Name:         "archive",
		Endpoint:     "https://minio.example.com",
		Region:       "us-east-1",
		AccessKey:    "AK_PROVIDER",
		SecretKey:    "SK_PROVIDER",
		Bucket:       "archive-bucket",
		ProviderName: "minio-prod",
	}
	cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts = append(
		cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts, m2)

	targets := []SyncTarget{
		{AppId: "app-a", MountName: "media"},   // will change
		{AppId: "app-a", MountName: "archive"}, // already matches
	}
	resp := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))

	if resp.Summary.Total != 2 {
		t.Errorf("Summary.Total: got %d, want 2", resp.Summary.Total)
	}
	if resp.Summary.Changed != 1 {
		t.Errorf("Summary.Changed: got %d, want 1", resp.Summary.Changed)
	}
	if resp.Summary.Unchanged != 1 {
		t.Errorf("Summary.Unchanged: got %d, want 1", resp.Summary.Unchanged)
	}
}

// TestApplyS3ProviderSync_SaveFailureRollsBack verifies that when persistence
// fails, apply returns SyncStatusError and provider-managed field mutations are
// rolled back (no in-memory/disk divergence retry dead-end).
func TestApplyS3ProviderSync_SaveFailureRollsBack(t *testing.T) {
	cl := newTestClusterForSync(t)
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	beforeEndpoint := m.Endpoint
	beforeRegion := m.Region
	beforeAccessKey := m.AccessKey
	beforeSecretKey := m.SecretKey

	// Force SaveApp failure by pointing WorkingDir to a path where /apps does not exist.
	cl.WorkingDir = "/proc"

	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	resp := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Status != SyncStatusError {
		t.Errorf("Status: got %q, want %q", r.Status, SyncStatusError)
	}
	if r.ErrorMessage == "" {
		t.Error("ErrorMessage must be set when persistence fails")
	}
	if len(r.ChangesApplied) != 0 {
		t.Errorf("ChangesApplied must be empty on rollback; got %v", r.ChangesApplied)
	}

	if resp.Summary.Failed != 1 {
		t.Errorf("Summary.Failed: got %d, want 1", resp.Summary.Failed)
	}
	if resp.Summary.Changed != 0 {
		t.Errorf("Summary.Changed: got %d, want 0", resp.Summary.Changed)
	}

	// Verify provider-managed fields were rolled back.
	if m.Endpoint != beforeEndpoint {
		t.Errorf("Endpoint rollback failed: got %q, want %q", m.Endpoint, beforeEndpoint)
	}
	if m.Region != beforeRegion {
		t.Errorf("Region rollback failed: got %q, want %q", m.Region, beforeRegion)
	}
	if m.AccessKey != beforeAccessKey {
		t.Errorf("AccessKey rollback failed: got %q, want %q", m.AccessKey, beforeAccessKey)
	}
	if m.SecretKey != beforeSecretKey {
		t.Errorf("SecretKey rollback failed: got %q, want %q", m.SecretKey, beforeSecretKey)
	}
}

// TestApplyS3ProviderSync_SaveFailureThenRetry verifies the dead-end scenario is
// prevented: after a failed save (with rollback), a later retry can still persist
// the change and report SyncApplyStatusChanged.
func TestApplyS3ProviderSync_SaveFailureThenRetry(t *testing.T) {
	cl := newTestClusterForSync(t)
	initialWorkingDir := cl.WorkingDir

	// First attempt: force SaveApp failure.
	cl.WorkingDir = "/proc"
	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	respFail := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))
	if len(respFail.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(respFail.Results))
	}
	if respFail.Results[0].Status != SyncStatusError {
		t.Fatalf("first apply status: got %q, want %q", respFail.Results[0].Status, SyncStatusError)
	}

	// Second attempt: restore writable dir; apply should now succeed (not unchanged).
	cl.WorkingDir = initialWorkingDir
	respRetry := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))
	if len(respRetry.Results) != 1 {
		t.Fatalf("retry results len: got %d, want 1", len(respRetry.Results))
	}
	if respRetry.Results[0].Status != SyncApplyStatusChanged {
		t.Fatalf("retry apply status: got %q, want %q", respRetry.Results[0].Status, SyncApplyStatusChanged)
	}
	if respRetry.Summary.Changed != 1 || respRetry.Summary.Failed != 0 {
		t.Errorf("retry summary unexpected: changed=%d failed=%d", respRetry.Summary.Changed, respRetry.Summary.Failed)
	}

	// Third attempt: now already in sync.
	respThird := cl.ApplyS3ProviderSync("minio-prod", targets, previewTokenForTargets(cl, "minio-prod", targets))
	if respThird.Results[0].Status != SyncApplyStatusUnchanged {
		t.Fatalf("third apply status: got %q, want %q", respThird.Results[0].Status, SyncApplyStatusUnchanged)
	}
}

// TestPreviewS3ProviderSync_ChangeFieldList verifies that the Changes slice lists
// exactly the fields that differ (endpoint, region, accessKey, secretKey).
func TestPreviewS3ProviderSync_ChangeFieldList(t *testing.T) {
	cl := newTestClusterForSync(t)
	// Make only endpoint differ.
	m := cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	m.Region = "us-east-1"
	m.AccessKey = "AK_PROVIDER"
	m.SecretKey = "SK_PROVIDER"
	// endpoint remains "https://old.example.com" (differs from provider)

	resp := cl.PreviewS3ProviderSync("minio-prod", []SyncTarget{{AppId: "app-a", MountName: "media"}})

	r := resp.Results[0]
	if r.Status != SyncStatusWillChange {
		t.Fatalf("Status: got %q, want %q", r.Status, SyncStatusWillChange)
	}
	if len(r.Changes) != 1 {
		t.Errorf("Changes len: got %d, want 1; changes = %v", len(r.Changes), r.Changes)
	}
	if len(r.Changes) > 0 && r.Changes[0].Field != "endpoint" {
		t.Errorf("Changes[0].Field: got %q, want %q", r.Changes[0].Field, "endpoint")
	}
}

func TestPreviewS3ProviderSync_RevisionTokenDeterministic(t *testing.T) {
	cl := newTestClusterForSync(t)
	targetsA := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	targetsB := []SyncTarget{{MountName: "media", AppId: "app-a"}}

	respA := cl.PreviewS3ProviderSync("minio-prod", targetsA)
	respB := cl.PreviewS3ProviderSync("minio-prod", targetsB)

	if respA.RevisionToken == "" || !IsValidS3SyncRevisionToken(respA.RevisionToken) {
		t.Fatalf("expected a valid revision token, got %q", respA.RevisionToken)
	}
	if respA.RevisionToken != respB.RevisionToken {
		t.Fatalf("expected deterministic revision token, got %q and %q", respA.RevisionToken, respB.RevisionToken)
	}

	driftedRegion := cl.GetS3ProvidersSnapshot()[0]
	driftedRegion.Region = "us-west-2"
	if err := cl.UpdateS3Provider(driftedRegion); err != nil {
		t.Fatalf("UpdateS3Provider (drift): %v", err)
	}
	respAfterProviderChange := cl.PreviewS3ProviderSync("minio-prod", targetsA)
	if respAfterProviderChange.RevisionToken == respA.RevisionToken {
		t.Fatalf("expected revision token to change after provider drift")
	}
}

func TestApplyS3ProviderSync_StaleStateOnProviderDrift(t *testing.T) {
	cl := newTestClusterForSync(t)
	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	token := previewTokenForTargets(cl, "minio-prod", targets)
	mountBefore := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]

	driftedEndpoint := cl.GetS3ProvidersSnapshot()[0]
	driftedEndpoint.Endpoint = "https://drifted.example.com"
	if err := cl.UpdateS3Provider(driftedEndpoint); err != nil {
		t.Fatalf("UpdateS3Provider (drift): %v", err)
	}

	resp := cl.ApplyS3ProviderSync("minio-prod", targets, token)
	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncApplyStatusStale {
		t.Fatalf("status: got %q, want %q", resp.Results[0].Status, SyncApplyStatusStale)
	}
	if resp.Summary.Failed != 1 {
		t.Fatalf("summary failed: got %d, want 1", resp.Summary.Failed)
	}

	mountAfter := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if mountAfter.Endpoint != mountBefore.Endpoint || mountAfter.Region != mountBefore.Region || mountAfter.AccessKey != mountBefore.AccessKey || mountAfter.SecretKey != mountBefore.SecretKey {
		t.Fatalf("mount was mutated on stale-state apply")
	}
}

func TestApplyS3ProviderSync_StaleStateOnMountDrift(t *testing.T) {
	cl := newTestClusterForSync(t)
	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	token := previewTokenForTargets(cl, "minio-prod", targets)

	cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0].Region = "drifted-region"
	resp := cl.ApplyS3ProviderSync("minio-prod", targets, token)

	if len(resp.Results) != 1 {
		t.Fatalf("Results len: got %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Status != SyncApplyStatusStale {
		t.Fatalf("status: got %q, want %q", resp.Results[0].Status, SyncApplyStatusStale)
	}
}

func TestApplyS3ProviderSync_RejectsMissingOrMalformedRevisionToken(t *testing.T) {
	cl := newTestClusterForSync(t)
	targets := []SyncTarget{{AppId: "app-a", MountName: "media"}}
	mountBefore := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]

	respMissing := cl.ApplyS3ProviderSync("minio-prod", targets, "")
	if respMissing.Results[0].Status != SyncStatusError {
		t.Fatalf("missing token status: got %q, want %q", respMissing.Results[0].Status, SyncStatusError)
	}

	respMalformed := cl.ApplyS3ProviderSync("minio-prod", targets, "bad-token")
	if respMalformed.Results[0].Status != SyncStatusError {
		t.Fatalf("malformed token status: got %q, want %q", respMalformed.Results[0].Status, SyncStatusError)
	}

	mountAfter := *cl.Apps[0].AppConfig.Deployment.Storages.S3Mounts[0]
	if mountAfter.Endpoint != mountBefore.Endpoint || mountAfter.Region != mountBefore.Region || mountAfter.AccessKey != mountBefore.AccessKey || mountAfter.SecretKey != mountBefore.SecretKey {
		t.Fatalf("mount mutated despite invalid revision token")
	}
}
