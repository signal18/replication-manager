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

// S3MountReferenceSnapshot captures a stable copy of one app mount used by
// provider reference/read-only APIs.
type S3MountReferenceSnapshot struct {
	AppID        string
	AppName      string
	MountName    string
	ProviderName string
	Endpoint     string
	Region       string
	Bucket       string
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

type syncMountFieldSpec struct {
	name    string
	current string
	desired string
}

// S3MountReferencesSnapshot returns a read-safe snapshot of app mounts that
// reference the requested provider name.
func (cluster *Cluster) S3MountReferencesSnapshot(providerName string) []S3MountReferenceSnapshot {
	cluster.Lock()
	apps := make([]*App, len(cluster.Apps))
	copy(apps, cluster.Apps)
	cluster.Unlock()

	refs := make([]S3MountReferenceSnapshot, 0)
	for _, app := range apps {
		if app == nil {
			continue
		}
		locked := false
		if app.Mutex != nil {
			app.Lock()
			locked = true
		}
		if app.AppConfig == nil || app.AppConfig.Deployment == nil || app.AppConfig.Deployment.Storages.S3Mounts == nil {
			if locked {
				app.Unlock()
			}
			continue
		}
		for _, s3m := range app.AppConfig.Deployment.Storages.S3Mounts {
			if s3m == nil || s3m.ProviderName != providerName {
				continue
			}
			refs = append(refs, S3MountReferenceSnapshot{
				AppID:        app.GetId(),
				AppName:      app.Name,
				MountName:    s3m.Name,
				ProviderName: s3m.ProviderName,
				Endpoint:     s3m.Endpoint,
				Region:       s3m.Region,
				Bucket:       s3m.Bucket,
			})
		}
		if locked {
			app.Unlock()
		}
	}
	return refs
}

// resolveProviderEffectiveValues returns the effective endpoint, region, accessKey,
// and secretKey for a provider. For app-mode providers the endpoint is derived from
// the sibling app's host:port; credentials are always empty for app mode.
func (cluster *Cluster) resolveProviderEffectiveValues(p *config.S3Provider) (endpoint, region, accessKey, secretKey string) {
	switch p.ProviderSource {
	case config.S3ProviderSourceApp:
		if providerEndpoint, ok := cluster.appEndpointByURL(p.ProviderApp); ok {
			endpoint = providerEndpoint
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

// ResolveS3ProviderEffectiveValues returns effective provider values for
// read-only API consumers using the same logic as sync preview/apply.
func (cluster *Cluster) ResolveS3ProviderEffectiveValues(p config.S3Provider) (endpoint, region, accessKey, secretKey string) {
	cp := p
	return cluster.resolveProviderEffectiveValues(&cp)
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

// mountLooksCustomizedForProviderManagedFields returns true when at least one
// provider-managed field differs from desired and the mount carries a non-empty
// local value for that differing field. Empty differing values are treated as
// likely fresh/default placeholders and do not trigger the "customized" warning.
func mountLooksCustomizedForProviderManagedFields(specs []syncMountFieldSpec) bool {
	for _, f := range specs {
		if f.current == f.desired {
			continue
		}
		if strings.TrimSpace(f.current) != "" {
			return true
		}
	}
	return false
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

func normalizeAppURLForLookup(url string) (string, bool) {
	normalized := strings.TrimSpace(url)
	if normalized == "" {
		return "", false
	}

	if strings.Contains(normalized, "://") {
		parts := strings.SplitN(normalized, "://", 2)
		if len(parts) == 2 {
			normalized = parts[1]
		}
	}

	parts := strings.SplitN(normalized, ":", 2)
	if len(parts) == 2 {
		return parts[0] + ":" + parts[1], true
	}
	return parts[0] + ":80", true
}

func (cluster *Cluster) syncAppIndexesSnapshot() (map[string]*App, map[string]string, uint64) {
	appByID := make(map[string]*App)
	idByURL := make(map[string]string)

	cluster.Lock()
	defer cluster.Unlock()
	appListVersion := cluster.appListVersion()

	for _, app := range cluster.Apps {
		if app == nil {
			continue
		}
		appID := app.GetId()
		if appID == "" {
			continue
		}
		appByID[appID] = app

		key, ok := normalizeAppURLForLookup(app.Host + ":" + app.Port)
		if ok {
			idByURL[key] = appID
		}
	}

	return appByID, idByURL, appListVersion
}

func appIDByURLFromIndex(url string, idByURL map[string]string) (string, bool) {
	key, ok := normalizeAppURLForLookup(url)
	if !ok {
		return "", false
	}
	appID, ok := idByURL[key]
	if !ok || appID == "" {
		return "", false
	}
	return appID, true
}

func (cluster *Cluster) appByIDUnsafe(appID string) *App {
	for _, app := range cluster.Apps {
		if app.GetId() == appID {
			return app
		}
	}
	return nil
}

func (cluster *Cluster) appByID(appID string) *App {
	cluster.Lock()
	defer cluster.Unlock()
	app := cluster.appByIDUnsafe(appID)
	if app != nil && app.Mutex == nil {
		return nil
	}
	return app
}

func (cluster *Cluster) appEndpointByURL(url string) (string, bool) {
	cluster.Lock()
	app, _ := cluster.GetAppByURL(url)
	cluster.Unlock()
	if app == nil {
		return "", false
	}
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	if app.Host == "" || app.Port == "" {
		return "", false
	}
	return app.Host + ":" + app.Port, true
}

// appEndpointByURLFromLockedApps resolves an app endpoint without acquiring any
// app mutexes. Caller must have locked the relevant app(s) and provided them in
// lockedApps (typically via lockAppsByID in apply path).
func (cluster *Cluster) appEndpointByURLFromLockedApps(url string, idByURL map[string]string, lockedApps map[string]*App) (string, bool) {
	appID, ok := appIDByURLFromIndex(url, idByURL)
	if !ok {
		return "", false
	}
	app, ok := lockedApps[appID]
	if !ok || app == nil {
		return "", false
	}
	if app.Host == "" || app.Port == "" {
		return "", false
	}
	return app.Host + ":" + app.Port, true
}

func (cluster *Cluster) syncApplyLockAppIDs(provider *config.S3Provider, targets []SyncTarget, idByURL map[string]string) []string {
	idSet := make(map[string]struct{})
	for _, t := range normalizeSyncTargets(targets) {
		if strings.TrimSpace(t.AppId) != "" {
			idSet[t.AppId] = struct{}{}
		}
	}

	if provider != nil && provider.ProviderSource == config.S3ProviderSourceApp && strings.TrimSpace(provider.ProviderApp) != "" {
		if providerAppID, ok := appIDByURLFromIndex(provider.ProviderApp, idByURL); ok {
			idSet[providerAppID] = struct{}{}
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
func (cluster *Cluster) lockAppsByID(orderedIDs []string, appSnapshotByID map[string]*App) (map[string]*App, func()) {
	appByID := make(map[string]*App)
	for _, appID := range orderedIDs {
		if app := appSnapshotByID[appID]; app != nil && app.Mutex != nil {
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

func (cluster *Cluster) resolveSyncTargetMountSnapshot(target SyncTarget) (*config.S3Mount, string, bool) {
	targetApp := cluster.appByID(target.AppId)
	if targetApp == nil {
		return nil, fmt.Sprintf("app %q not found", target.AppId), false
	}

	targetApp.Lock()
	defer targetApp.Unlock()
	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		return nil, fmt.Sprintf("app %q has no deployment config", target.AppId), false
	}

	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			cp := *s3m
			return &cp, "", true
		}
	}
	return nil, fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId), true
}

// resolveSyncTargetMountSnapshotFromLockedApps resolves a target mount snapshot
// from already-locked apps without re-locking app mutexes.
func (cluster *Cluster) resolveSyncTargetMountSnapshotFromLockedApps(target SyncTarget, lockedApps map[string]*App) (*config.S3Mount, string, bool) {
	targetApp := lockedApps[target.AppId]
	if targetApp == nil {
		return nil, fmt.Sprintf("app %q not found", target.AppId), false
	}

	if targetApp.AppConfig == nil || targetApp.AppConfig.Deployment == nil {
		return nil, fmt.Sprintf("app %q has no deployment config", target.AppId), false
	}

	for _, s3m := range targetApp.AppConfig.Deployment.Storages.S3Mounts {
		if s3m != nil && s3m.Name == target.MountName {
			cp := *s3m
			return &cp, "", true
		}
	}
	return nil, fmt.Sprintf("mount %q not found in app %q", target.MountName, target.AppId), true
}

// resolveProviderEffectiveValuesFromLockedApps returns provider effective values
// without re-locking app mutexes. For app-source providers, endpoint resolution
// must come from lockedApps.
func (cluster *Cluster) resolveProviderEffectiveValuesFromLockedApps(p *config.S3Provider, idByURL map[string]string, lockedApps map[string]*App) (endpoint, region, accessKey, secretKey string) {
	switch p.ProviderSource {
	case config.S3ProviderSourceApp:
		if providerEndpoint, ok := cluster.appEndpointByURLFromLockedApps(p.ProviderApp, idByURL, lockedApps); ok {
			endpoint = providerEndpoint
		}
		region = p.Region
	case config.S3ProviderSourceCustom:
		endpoint = p.Endpoint
		region = p.Region
		accessKey = p.AccessKey
		secretKey = p.SecretKey
	}
	return
}

func (cluster *Cluster) computeS3SyncRevisionTokenWithSnapshotUsingResolvers(
	providerName string,
	targets []SyncTarget,
	providers []config.S3Provider,
	resolveProvider func(*config.S3Provider) (endpoint, region, accessKey, secretKey string),
	resolveTargetMount func(SyncTarget) (*config.S3Mount, string, bool),
) string {
	normalizedTargets := normalizeSyncTargets(targets)
	snapshot := syncRevisionSnapshot{
		Version:      "v1",
		ProviderName: providerName,
		Targets:      make([]syncRevisionTargetSnapshot, 0, len(normalizedTargets)),
	}

	provider := findProviderByName(providers, providerName)
	if provider != nil {
		effectiveEndpoint, effectiveRegion, effectiveAccessKey, effectiveSecretKey := resolveProvider(provider)
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
		targetMount, errMessage, appResolved := resolveTargetMount(t)
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

func (cluster *Cluster) computeS3SyncRevisionTokenWithSnapshot(providerName string, targets []SyncTarget, providers []config.S3Provider) string {
	return cluster.computeS3SyncRevisionTokenWithSnapshotUsingResolvers(
		providerName,
		targets,
		providers,
		cluster.resolveProviderEffectiveValues,
		cluster.resolveSyncTargetMountSnapshot,
	)
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

	targetMount, errMessage, _ := cluster.resolveSyncTargetMountSnapshot(target)
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
	specs := []syncMountFieldSpec{
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
		}
		if mountLooksCustomizedForProviderManagedFields(specs) {
			result.Warnings = append(result.Warnings, "This mount has been customized from the provider template.")
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
	appSnapshotByID, appIDByURL, appSnapshotVersion := cluster.syncAppIndexesSnapshot()

	provider := findProviderByName(providerSnapshot, providerName)
	lockAppIDs := cluster.syncApplyLockAppIDs(provider, targets, appIDByURL)
	lockedApps, unlockApps := cluster.lockAppsByID(lockAppIDs, appSnapshotByID)
	defer unlockApps()

	// Lock contract (apply path): once lockAppsByID has acquired app mutexes,
	// no downstream helper is allowed to lock app mutexes again. Revision-token
	// revalidation and app-source endpoint resolution therefore use locked-app
	// variants that only read from already-locked app state.
	computeTokenFromLockedState := func() string {
		return cluster.computeS3SyncRevisionTokenWithSnapshotUsingResolvers(
			providerName,
			targets,
			providerSnapshot,
			func(p *config.S3Provider) (endpoint, region, accessKey, secretKey string) {
				return cluster.resolveProviderEffectiveValuesFromLockedApps(p, appIDByURL, lockedApps)
			},
			func(t SyncTarget) (*config.S3Mount, string, bool) {
				return cluster.resolveSyncTargetMountSnapshotFromLockedApps(t, lockedApps)
			},
		)
	}

	if computeTokenFromLockedState() != strings.TrimSpace(revisionToken) {
		for _, t := range targets {
			resp.Results = append(resp.Results, SyncApplyResult{Target: t, Status: SyncApplyStatusStale, ErrorMessage: "sync preview is stale; run preview again before apply"})
		}
		resp.Summary.Total = len(resp.Results)
		resp.Summary.Failed = len(resp.Results)
		return resp
	}

	if cluster.appListVersion() != appSnapshotVersion {
		for _, t := range targets {
			resp.Results = append(resp.Results, SyncApplyResult{Target: t, Status: SyncApplyStatusStale, ErrorMessage: "sync target app list changed during apply; run preview again"})
		}
		resp.Summary.Total = len(resp.Results)
		resp.Summary.Failed = len(resp.Results)
		return resp
	}

	for _, t := range targets {
		targetApp := lockedApps[t.AppId]
		r := cluster.applySingleMountSyncLocked(provider, providerName, targetApp, t, appIDByURL, lockedApps)
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
func (cluster *Cluster) applySingleMountSyncLocked(provider *config.S3Provider, providerName string, targetApp *App, target SyncTarget, idByURL map[string]string, lockedApps map[string]*App) SyncApplyResult {
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
		cluster.resolveProviderEffectiveValuesFromLockedApps(provider, idByURL, lockedApps)
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
