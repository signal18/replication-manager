// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
)

// Status constants for SyncPreviewResult.Status.
const (
	SyncStatusWillChange      = "will_change"
	SyncStatusNoChange        = "no_change"
	SyncStatusProviderMissing = "provider_missing"
	SyncStatusError           = "error"
)

// Status constants for SyncApplyResult.Status.
const (
	SyncApplyStatusChanged   = "changed"
	SyncApplyStatusUnchanged = "unchanged"
	// provider_missing and error are shared with preview; reuse the preview constants.
)

// SyncMaxTargets is the maximum number of targets accepted in a single sync request.
const SyncMaxTargets = 100

// SyncTarget identifies a single S3 mount to sync: the app by ID and mount by name.
type SyncTarget struct {
	AppId     string `json:"appId"`
	MountName string `json:"mountName"`
}

// SyncFieldChange describes one provider-managed field that differs between the
// mount's current value and the desired provider value. Credential fields
// (accessKey, secretKey) have their before/after values masked.
type SyncFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// SyncPreviewResult is the per-mount result for a dry-run preview operation.
//
// Status values: SyncStatusWillChange | SyncStatusNoChange |
//
//	SyncStatusProviderMissing | SyncStatusError
//
// Changes:        provider-managed fields that WILL change (credentials masked).
// UnchangedFields: provider-managed fields that already match the provider.
// PreservedFields: mount-specific fields that are never overwritten by sync.
type SyncPreviewResult struct {
	Target          SyncTarget        `json:"target"`
	Status          string            `json:"status"`
	Warnings        []string          `json:"warnings,omitempty"`
	Changes         []SyncFieldChange `json:"changes,omitempty"`
	UnchangedFields []string          `json:"unchangedFields,omitempty"`
	PreservedFields map[string]string `json:"preservedFields,omitempty"`
	ErrorMessage    string            `json:"errorMessage,omitempty"`
}

// SyncApplyResult is the per-mount result for an apply operation.
// Status values: SyncApplyStatusChanged | SyncApplyStatusUnchanged |
//
//	SyncStatusProviderMissing | SyncStatusError
type SyncApplyResult struct {
	Target         SyncTarget `json:"target"`
	Status         string     `json:"status"`
	ChangesApplied []string   `json:"changesApplied,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
}

// SyncPreviewSummary aggregates counts for a preview response.
type SyncPreviewSummary struct {
	Total      int `json:"total"`
	WillChange int `json:"willChange"`
	Unchanged  int `json:"unchanged"`
	Failed     int `json:"failed"`
}

// SyncApplySummary aggregates counts for an apply response.
type SyncApplySummary struct {
	Total     int `json:"total"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	Failed    int `json:"failed"`
}

// SyncPreviewResponse is the full response for a preview (dry-run) request.
type SyncPreviewResponse struct {
	ProviderName string              `json:"providerName"`
	DryRun       bool                `json:"dryRun"`
	Summary      SyncPreviewSummary  `json:"summary"`
	Results      []SyncPreviewResult `json:"results"`
}

// SyncApplyResponse is the full response for an apply request.
type SyncApplyResponse struct {
	ProviderName string            `json:"providerName"`
	DryRun       bool              `json:"dryRun"`
	Summary      SyncApplySummary  `json:"summary"`
	Results      []SyncApplyResult `json:"results"`
}

// credentialMask is substituted for the before/after values of credential fields
// in preview responses so that secrets are never exposed in the API response.
const credentialMask = "***"

// credentialFields is the set of provider-managed fields whose values must be
// masked in preview responses.
var credentialFields = map[string]bool{
	"accessKey": true,
	"secretKey": true,
}

// resolveProviderEffectiveValues returns the effective endpoint, region, accessKey,
// and secretKey for a provider. For app-mode providers the endpoint is derived from
// the sibling app's host:port; credentials are always empty for app mode.
func (cluster *Cluster) resolveProviderEffectiveValues(p *config.S3Provider) (endpoint, region, accessKey, secretKey string) {
	switch p.ProviderSource {
	case config.S3ProviderSourceApp:
		if app, _ := cluster.GetAppByURL(p.ProviderApp); app != nil && app.Host != "" && app.Port != "" {
			endpoint = app.Host + ":" + app.Port
		}
		region = p.Region
		// App-mode providers carry no credentials.
	case config.S3ProviderSourceCustom:
		endpoint = p.Endpoint
		region = p.Region
		accessKey = p.AccessKey
		secretKey = p.SecretKey
	}
	return
}

// findProviderByName returns a pointer to the named provider in a snapshot, or nil.
// The returned pointer addresses a heap-allocated copy of the slice element.
func findProviderByName(providers []config.S3Provider, name string) *config.S3Provider {
	for i := range providers {
		if providers[i].Name == name {
			p := providers[i]
			return &p
		}
	}
	return nil
}

// maskChangeValue returns credentialMask when the field name is a credential field,
// or the original value otherwise. This prevents secrets from appearing in preview diffs.
func maskChangeValue(field, value string) string {
	if credentialFields[field] && value != "" {
		return credentialMask
	}
	return value
}

// planSingleMountPreview computes a preview diff for one target mount against the
// given provider (which must already be resolved and non-nil). It returns the
// result without touching any mutable state. Credential values in the Changes
// array are masked; unchangedFields lists provider-managed fields already in sync.
func (cluster *Cluster) planSingleMountPreview(provider *config.S3Provider, target SyncTarget) SyncPreviewResult {
	result := SyncPreviewResult{Target: target}

	// Locate app.
	var targetApp *App
	for _, app := range cluster.Apps {
		if app.GetId() == target.AppId {
			targetApp = app
			break
		}
	}
	if targetApp == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("app %q not found", target.AppId)
		return result
	}
	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("app %q has no deployment config", target.AppId)
		return result
	}

	// Locate mount.
	var targetMount *config.S3Mount
	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			targetMount = s3m
			break
		}
	}
	if targetMount == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId)
		return result
	}
	if targetMount.ProviderName != provider.Name {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("mount %q is linked to provider %q, not %q", target.MountName, targetMount.ProviderName, provider.Name)
		return result
	}

	// Compute desired effective values from provider.
	desiredEndpoint, desiredRegion, desiredAccessKey, desiredSecretKey :=
		cluster.resolveProviderEffectiveValues(provider)
	if provider.ProviderSource == config.S3ProviderSourceApp && desiredEndpoint == "" {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("provider %q app endpoint is unavailable", provider.Name)
		return result
	}

	// Diff provider-managed fields. Credential values are masked in the output.
	type fieldSpec struct {
		name    string
		current string
		desired string
	}
	specs := []fieldSpec{
		{"endpoint", targetMount.Endpoint, desiredEndpoint},
		{"region", targetMount.Region, desiredRegion},
		{"accessKey", targetMount.AccessKey, desiredAccessKey},
		{"secretKey", targetMount.SecretKey, desiredSecretKey},
	}

	var changes []SyncFieldChange
	var unchangedFields []string
	for _, f := range specs {
		if f.current != f.desired {
			changes = append(changes, SyncFieldChange{
				Field:  f.name,
				Before: maskChangeValue(f.name, f.current),
				After:  maskChangeValue(f.name, f.desired),
			})
		} else {
			unchangedFields = append(unchangedFields, f.name)
		}
	}

	if len(changes) == 0 {
		result.Status = SyncStatusNoChange
	} else {
		result.Status = SyncStatusWillChange
		result.Changes = changes
		result.Warnings = []string{
			"Local provider-managed fields will be overwritten by provider values.",
			"This mount has been customized from the provider template.",
		}
	}

	if len(unchangedFields) > 0 {
		result.UnchangedFields = unchangedFields
	}

	result.PreservedFields = map[string]string{
		"name":         targetMount.Name,
		"bucket":       targetMount.Bucket,
		"mountdir":     targetMount.MountDir,
		"volumename":   targetMount.VolumeName,
		"volumedir":    targetMount.VolumeDir,
		"providerName": targetMount.ProviderName,
	}
	return result
}

// PreviewS3ProviderSync performs a dry-run diff for each requested target mount
// against the named provider. No state is mutated. When the provider does not
// exist every target gets status SyncStatusProviderMissing.
func (cluster *Cluster) PreviewS3ProviderSync(providerName string, targets []SyncTarget) SyncPreviewResponse {
	resp := SyncPreviewResponse{
		ProviderName: providerName,
		DryRun:       true,
		Results:      make([]SyncPreviewResult, 0, len(targets)),
	}

	provider := findProviderByName(cluster.GetS3ProvidersSnapshot(), providerName)

	for _, t := range targets {
		var r SyncPreviewResult
		if provider == nil {
			r = SyncPreviewResult{
				Target:       t,
				Status:       SyncStatusProviderMissing,
				ErrorMessage: fmt.Sprintf("provider %q not found", providerName),
			}
		} else {
			r = cluster.planSingleMountPreview(provider, t)
		}
		resp.Results = append(resp.Results, r)
	}

	// Build summary.
	resp.Summary.Total = len(resp.Results)
	for _, r := range resp.Results {
		switch r.Status {
		case SyncStatusWillChange:
			resp.Summary.WillChange++
		case SyncStatusNoChange:
			resp.Summary.Unchanged++
		default:
			resp.Summary.Failed++
		}
	}
	return resp
}

// ApplyS3ProviderSync applies provider-managed field values to each requested
// target mount. Mount-specific fields (name, bucket, mountdir, volumename,
// volumedir, providerName) are never touched. After each successful mutation
// the app is persisted via SaveApp.
func (cluster *Cluster) ApplyS3ProviderSync(providerName string, targets []SyncTarget) SyncApplyResponse {
	resp := SyncApplyResponse{
		ProviderName: providerName,
		DryRun:       false,
		Results:      make([]SyncApplyResult, 0, len(targets)),
	}

	provider := findProviderByName(cluster.GetS3ProvidersSnapshot(), providerName)

	for _, t := range targets {
		r := cluster.applySingleMountSync(provider, providerName, t)
		resp.Results = append(resp.Results, r)
	}

	// Build summary.
	resp.Summary.Total = len(resp.Results)
	for _, r := range resp.Results {
		switch r.Status {
		case SyncApplyStatusChanged:
			resp.Summary.Changed++
		case SyncApplyStatusUnchanged:
			resp.Summary.Unchanged++
		default:
			resp.Summary.Failed++
		}
	}
	return resp
}

// applySingleMountSync mutates one mount's provider-managed fields to match the
// provider and persists the parent app. provider may be nil (provider_missing).
// When SaveApp fails, provider-managed field mutations are rolled back and the
// caller receives SyncStatusError with ErrorMessage.
func (cluster *Cluster) applySingleMountSync(provider *config.S3Provider, providerName string, target SyncTarget) SyncApplyResult {
	result := SyncApplyResult{Target: target}

	if provider == nil {
		result.Status = SyncStatusProviderMissing
		result.ErrorMessage = fmt.Sprintf("provider %q not found", providerName)
		return result
	}

	// Serialize mutate/save/rollback for sync operations to avoid interleaving
	// updates on the same app/mount during concurrent apply requests.
	cluster.Lock()
	defer cluster.Unlock()

	// Locate app.
	var targetApp *App
	for _, app := range cluster.Apps {
		if app.GetId() == target.AppId {
			targetApp = app
			break
		}
	}
	if targetApp == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("app %q not found", target.AppId)
		return result
	}
	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("app %q has no deployment config", target.AppId)
		return result
	}

	// Locate mount (pointer — mutations are in-place on the stored struct).
	var targetMount *config.S3Mount
	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			targetMount = s3m
			break
		}
	}
	if targetMount == nil {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId)
		return result
	}
	if targetMount.ProviderName != provider.Name {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("mount %q is linked to provider %q, not %q", target.MountName, targetMount.ProviderName, provider.Name)
		return result
	}

	// Compute desired effective values from provider.
	desiredEndpoint, desiredRegion, desiredAccessKey, desiredSecretKey :=
		cluster.resolveProviderEffectiveValues(provider)
	if provider.ProviderSource == config.S3ProviderSourceApp && desiredEndpoint == "" {
		result.Status = SyncStatusError
		result.ErrorMessage = fmt.Sprintf("provider %q app endpoint is unavailable", provider.Name)
		return result
	}

	// Apply provider-managed fields only.
	type fieldPair struct {
		name    string
		current *string
		desired string
	}
	// Keep original values so we can roll back if persistence fails.
	originalEndpoint := targetMount.Endpoint
	originalRegion := targetMount.Region
	originalAccessKey := targetMount.AccessKey
	originalSecretKey := targetMount.SecretKey
	pairs := []fieldPair{
		{"endpoint", &targetMount.Endpoint, desiredEndpoint},
		{"region", &targetMount.Region, desiredRegion},
		{"accessKey", &targetMount.AccessKey, desiredAccessKey},
		{"secretKey", &targetMount.SecretKey, desiredSecretKey},
	}

	var changesApplied []string
	for _, f := range pairs {
		if *f.current != f.desired {
			*f.current = f.desired
			changesApplied = append(changesApplied, f.name)
		}
	}

	if len(changesApplied) == 0 {
		result.Status = SyncApplyStatusUnchanged
		return result
	}

	result.Status = SyncApplyStatusChanged
	result.ChangesApplied = changesApplied

	// Persist the updated app configuration. If persistence fails, roll back the
	// in-memory mutation to avoid reporting a durable change that never persisted.
	if _, err := cluster.SaveApp(targetApp, ""); err != nil {
		targetMount.Endpoint = originalEndpoint
		targetMount.Region = originalRegion
		targetMount.AccessKey = originalAccessKey
		targetMount.SecretKey = originalSecretKey
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"S3 sync apply: SaveApp failed for mount %q in app %q; rolled back provider-managed fields: %v",
			target.MountName, target.AppId, err)
		result.Status = SyncStatusError
		result.ChangesApplied = nil
		result.ErrorMessage = fmt.Sprintf("failed to persist sync changes (rolled back): %v", err)
	}
	return result
}
