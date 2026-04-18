// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

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
	SyncApplyStatusStale     = "stale_state"
	// provider_missing and error are shared with preview; reuse the preview constants.
)

const syncRevisionTokenPrefix = "s3sync:v1:"

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
//	SyncApplyStatusStale |
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
	ProviderName  string              `json:"providerName"`
	DryRun        bool                `json:"dryRun"`
	RevisionToken string              `json:"revisionToken"`
	Summary       SyncPreviewSummary  `json:"summary"`
	Results       []SyncPreviewResult `json:"results"`
}

// SyncApplyResponse is the full response for an apply request.
type SyncApplyResponse struct {
	ProviderName string            `json:"providerName"`
	DryRun       bool              `json:"dryRun"`
	Summary      SyncApplySummary  `json:"summary"`
	Results      []SyncApplyResult `json:"results"`
}

type syncRevisionProviderSnapshot struct {
	Name               string `json:"name"`
	ProviderSource     string `json:"providerSource"`
	ProviderApp        string `json:"providerApp"`
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	AccessKey          string `json:"accessKey"`
	SecretKey          string `json:"secretKey"`
	EffectiveEndpoint  string `json:"effectiveEndpoint"`
	EffectiveRegion    string `json:"effectiveRegion"`
	EffectiveAccessKey string `json:"effectiveAccessKey"`
	EffectiveSecretKey string `json:"effectiveSecretKey"`
}

type syncRevisionTargetSnapshot struct {
	AppId             string `json:"appId"`
	MountName         string `json:"mountName"`
	AppState          string `json:"appState"`
	MountState        string `json:"mountState"`
	MountProviderName string `json:"mountProviderName"`
	Endpoint          string `json:"endpoint"`
	Region            string `json:"region"`
	AccessKey         string `json:"accessKey"`
	SecretKey         string `json:"secretKey"`
}

type syncRevisionSnapshot struct {
	Version      string                        `json:"version"`
	ProviderName string                        `json:"providerName"`
	Provider     *syncRevisionProviderSnapshot `json:"provider,omitempty"`
	Targets      []syncRevisionTargetSnapshot  `json:"targets"`
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

// IsValidS3SyncRevisionToken validates the expected revision token shape.
func IsValidS3SyncRevisionToken(token string) bool {
	t := strings.TrimSpace(token)
	if !strings.HasPrefix(t, syncRevisionTokenPrefix) {
		return false
	}
	h := strings.TrimPrefix(t, syncRevisionTokenPrefix)
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

func normalizeSyncTargets(targets []SyncTarget) []SyncTarget {
	normalized := make([]SyncTarget, len(targets))
	copy(normalized, targets)
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].AppId != normalized[j].AppId {
			return normalized[i].AppId < normalized[j].AppId
		}
		return normalized[i].MountName < normalized[j].MountName
	})
	return normalized
}

func (cluster *Cluster) appByID(appID string) *App {
	for _, app := range cluster.Apps {
		if app.GetId() == appID {
			return app
		}
	}
	return nil
}

func (cluster *Cluster) syncApplyLockAppIDs(provider *config.S3Provider, targets []SyncTarget) []string {
	idSet := make(map[string]struct{})
	for _, t := range normalizeSyncTargets(targets) {
		if strings.TrimSpace(t.AppId) != "" {
			idSet[t.AppId] = struct{}{}
		}
	}

	if provider != nil && provider.ProviderSource == config.S3ProviderSourceApp && strings.TrimSpace(provider.ProviderApp) != "" {
		if providerApp, _ := cluster.GetAppByURL(provider.ProviderApp); providerApp != nil {
			idSet[providerApp.GetId()] = struct{}{}
		}
	}

	orderedIDs := make([]string, 0, len(idSet))
	for appID := range idSet {
		orderedIDs = append(orderedIDs, appID)
	}
	sort.Strings(orderedIDs)
	return orderedIDs
}

// lockAppsByID returns (locked app map, unlock callback) for app IDs locked in
// deterministic AppId order to avoid deadlocks across concurrent callers.
func (cluster *Cluster) lockAppsByID(orderedIDs []string) (map[string]*App, func()) {
	appByID := make(map[string]*App)
	for _, appID := range orderedIDs {
		if app := cluster.appByID(appID); app != nil {
			if app.Mutex == nil {
				app.Mutex = &sync.Mutex{}
			}
			appByID[appID] = app
		}
	}

	lockedApps := make([]*App, 0, len(orderedIDs))
	for _, appID := range orderedIDs {
		app, ok := appByID[appID]
		if !ok {
			continue
		}
		app.Lock()
		lockedApps = append(lockedApps, app)
	}

	unlock := func() {
		for i := len(lockedApps) - 1; i >= 0; i-- {
			lockedApps[i].Unlock()
		}
	}

	return appByID, unlock
}

func (cluster *Cluster) resolveSyncTargetMountInApp(targetApp *App, target SyncTarget) (*config.S3Mount, string) {
	if targetApp == nil {
		return nil, fmt.Sprintf("app %q not found", target.AppId)
	}
	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		return nil, fmt.Sprintf("app %q has no deployment config", target.AppId)
	}

	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			return s3m, ""
		}
	}
	return nil, fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId)
}

func (cluster *Cluster) resolveSyncTargetMount(target SyncTarget) (*App, *config.S3Mount, string, bool) {
	var targetApp *App
	for _, app := range cluster.Apps {
		if app.GetId() == target.AppId {
			targetApp = app
			break
		}
	}
	if targetApp == nil {
		return nil, nil, fmt.Sprintf("app %q not found", target.AppId), false
	}
	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		return targetApp, nil, fmt.Sprintf("app %q has no deployment config", target.AppId), false
	}

	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			return targetApp, s3m, "", true
		}
	}
	return targetApp, nil, fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId), true
}

func (cluster *Cluster) computeS3SyncRevisionTokenWithSnapshot(providerName string, targets []SyncTarget, providers []config.S3Provider) string {
	normalizedTargets := normalizeSyncTargets(targets)
	snapshot := syncRevisionSnapshot{
		Version:      "v1",
		ProviderName: providerName,
		Targets:      make([]syncRevisionTargetSnapshot, 0, len(normalizedTargets)),
	}

	provider := findProviderByName(providers, providerName)
	if provider != nil {
		effectiveEndpoint, effectiveRegion, effectiveAccessKey, effectiveSecretKey := cluster.resolveProviderEffectiveValues(provider)
		snapshot.Provider = &syncRevisionProviderSnapshot{
			Name:               provider.Name,
			ProviderSource:     string(provider.ProviderSource),
			ProviderApp:        provider.ProviderApp,
			Endpoint:           provider.Endpoint,
			Region:             provider.Region,
			AccessKey:          provider.AccessKey,
			SecretKey:          provider.SecretKey,
			EffectiveEndpoint:  effectiveEndpoint,
			EffectiveRegion:    effectiveRegion,
			EffectiveAccessKey: effectiveAccessKey,
			EffectiveSecretKey: effectiveSecretKey,
		}
	}

	for _, t := range normalizedTargets {
		entry := syncRevisionTargetSnapshot{
			AppId:      t.AppId,
			MountName:  t.MountName,
			AppState:   "ok",
			MountState: "ok",
		}
		_, targetMount, errMessage, appResolved := cluster.resolveSyncTargetMount(t)
		if errMessage != "" {
			if !appResolved {
				entry.AppState = "missing_or_invalid"
				entry.MountState = "unknown"
			} else {
				entry.MountState = "missing_or_invalid"
			}
			snapshot.Targets = append(snapshot.Targets, entry)
			continue
		}

		entry.MountProviderName = targetMount.ProviderName
		entry.Endpoint = targetMount.Endpoint
		entry.Region = targetMount.Region
		entry.AccessKey = targetMount.AccessKey
		entry.SecretKey = targetMount.SecretKey
		snapshot.Targets = append(snapshot.Targets, entry)
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		sum := sha256.Sum256([]byte(snapshot.ProviderName))
		return syncRevisionTokenPrefix + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(payload)
	return syncRevisionTokenPrefix + hex.EncodeToString(sum[:])
}

func (cluster *Cluster) computeS3SyncRevisionToken(providerName string, targets []SyncTarget) string {
	return cluster.computeS3SyncRevisionTokenWithSnapshot(providerName, targets, cluster.GetS3ProvidersSnapshot())
}

// planSingleMountPreview computes a preview diff for one target mount against the
// given provider (which must already be resolved and non-nil). It returns the
// result without touching any mutable state. Credential values in the Changes
// array are masked; unchangedFields lists provider-managed fields already in sync.
func (cluster *Cluster) planSingleMountPreview(provider *config.S3Provider, target SyncTarget) SyncPreviewResult {
	result := SyncPreviewResult{Target: target}

	_, targetMount, errMessage, _ := cluster.resolveSyncTargetMount(target)
	if errMessage != "" {
		result.Status = SyncStatusError
		result.ErrorMessage = errMessage
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
	providerSnapshot := cluster.GetS3ProvidersSnapshot()
	resp := SyncPreviewResponse{
		ProviderName:  providerName,
		DryRun:        true,
		RevisionToken: cluster.computeS3SyncRevisionTokenWithSnapshot(providerName, targets, providerSnapshot),
		Results:       make([]SyncPreviewResult, 0, len(targets)),
	}

	provider := findProviderByName(providerSnapshot, providerName)

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
func (cluster *Cluster) ApplyS3ProviderSync(providerName string, targets []SyncTarget, revisionToken string) SyncApplyResponse {
	resp := SyncApplyResponse{
		ProviderName: providerName,
		DryRun:       false,
		Results:      make([]SyncApplyResult, 0, len(targets)),
	}

	if strings.TrimSpace(revisionToken) == "" {
		for _, t := range targets {
			resp.Results = append(resp.Results, SyncApplyResult{Target: t, Status: SyncStatusError, ErrorMessage: "revisionToken is required; run preview again"})
		}
		resp.Summary.Total = len(resp.Results)
		resp.Summary.Failed = len(resp.Results)
		return resp
	}
	if !IsValidS3SyncRevisionToken(revisionToken) {
		for _, t := range targets {
			resp.Results = append(resp.Results, SyncApplyResult{Target: t, Status: SyncStatusError, ErrorMessage: "revisionToken is malformed; run preview again"})
		}
		resp.Summary.Total = len(resp.Results)
		resp.Summary.Failed = len(resp.Results)
		return resp
	}

	// Serialize apply operations while avoiding a broad cluster lifecycle lock.
	cluster.s3SyncApplyMu.Lock()
	defer cluster.s3SyncApplyMu.Unlock()

	// Keep provider set stable for token revalidation + apply.
	cluster.s3Providers.mu.RLock()
	defer cluster.s3Providers.mu.RUnlock()
	providerSnapshot := make([]config.S3Provider, len(cluster.s3Providers.providers))
	copy(providerSnapshot, cluster.s3Providers.providers)

	provider := findProviderByName(providerSnapshot, providerName)
	lockAppIDs := cluster.syncApplyLockAppIDs(provider, targets)
	lockedApps, unlockApps := cluster.lockAppsByID(lockAppIDs)
	defer unlockApps()

	if cluster.computeS3SyncRevisionTokenWithSnapshot(providerName, targets, providerSnapshot) != strings.TrimSpace(revisionToken) {
		for _, t := range targets {
			resp.Results = append(resp.Results, SyncApplyResult{Target: t, Status: SyncApplyStatusStale, ErrorMessage: "sync preview is stale; run preview again before apply"})
		}
		resp.Summary.Total = len(resp.Results)
		resp.Summary.Failed = len(resp.Results)
		return resp
	}

	for _, t := range targets {
		targetApp := lockedApps[t.AppId]
		if current := cluster.appByID(t.AppId); targetApp != nil && current != nil && current != targetApp {
			resp.Results = append(resp.Results, SyncApplyResult{
				Target:       t,
				Status:       SyncApplyStatusStale,
				ErrorMessage: "sync target app changed during apply; run preview again",
			})
			continue
		}
		r := cluster.applySingleMountSyncLocked(provider, providerName, targetApp, t)
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

// applySingleMountSyncLocked mutates one mount's provider-managed fields to
// match the provider and persists the parent app. provider may be nil
// (provider_missing). Caller must hold lockSyncTargetApps(targets) and
// s3Providers.mu.RLock().
// When SaveApp fails, provider-managed field mutations are rolled back and the
// caller receives SyncStatusError with ErrorMessage.
func (cluster *Cluster) applySingleMountSyncLocked(provider *config.S3Provider, providerName string, targetApp *App, target SyncTarget) SyncApplyResult {
	result := SyncApplyResult{Target: target}

	if provider == nil {
		result.Status = SyncStatusProviderMissing
		result.ErrorMessage = fmt.Sprintf("provider %q not found", providerName)
		return result
	}

	targetMount, errMessage := cluster.resolveSyncTargetMountInApp(targetApp, target)
	if errMessage != "" {
		result.Status = SyncStatusError
		result.ErrorMessage = errMessage
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
