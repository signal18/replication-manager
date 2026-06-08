package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MigrateStorageToCanonical translates legacy storages+paths into the canonical
// AppVolumes/AppSources/AppMounts model and stamps the deployment with v2 metadata.
//
// This is a *behavior*-based migration, not a row-based one: the legacy
// runtime (pre-canonical OpenSVCGetAppVolumeSections) grouped Storages.Volumes
// by PoolName and provisioned exactly one physical OpenSVC volume per pool —
// named "{appname}-{pool}" — merging every legacy volume's directory into it.
// Converting each legacy Volume row into its own canonical AppVolume would
// not reproduce that: it would change what gets merged at runtime, both for
// the migrated data (risking orphaned physical volumes) and for any volume
// added later (which would unexpectedly merge into the old pool group).
//
// Rules:
//  1. If canonical model already exists (StorageLayoutVersion >= V2), this is a no-op.
//  2. Otherwise: legacy Volumes are grouped by pool exactly as the old runtime
//     did; each pool group becomes exactly one canonical AppVolume (with a
//     RuntimeName override carrying the old "{appname}-{pool}" identity) plus
//     one synthesized directory AppSource per legacy volume that fed the group.
//     legacy GitClone → AppSource{Type:git}.
//     legacy S3Mount  → AppSource{Type:s3}.
//     legacy PathMapping → AppMount (parent/child graphs are flattened).
//  3. Migrated deployments get PhysicalVolumeStrategyPerVolume: the merge is
//     captured once, in the canonical model, and the runtime never re-merges
//     by pool again — RuntimeName keeps already-provisioned physical volumes
//     intact without requiring a cutover.
//  4. The function is idempotent.
//
// On error the Deployment is not modified so the legacy persisted config remains intact.
func MigrateStorageToCanonical(d *Deployment, defaultSize string) error {
	if d == nil {
		return nil
	}
	if d.StorageLayoutVersion >= StorageLayoutV2 {
		return nil
	}

	newVolumes, newSources, newMounts, err := deriveCanonicalFromLegacy(d, defaultSize)
	if err != nil {
		return err
	}

	// --- Commit ---
	d.AppVolumes = newVolumes
	d.AppSources = newSources
	d.AppMounts = newMounts
	d.StorageLayoutVersion = StorageLayoutV2
	d.PhysicalVolumeStrategy = PhysicalVolumeStrategyPerVolume
	d.CanonicalStorageOrigin = CanonicalStorageOriginLegacyPooledV1

	// Legacy fields (Storages.Volumes, GitClones, S3Mounts, Paths) are intentionally
	// preserved as compatibility shadows so existing legacy readers (GetGitClone,
	// GetS3Mount, legacy API handlers) continue to function without change.
	// They must not be used as write targets after migration — canonical fields are
	// the sole write target for all new edits and provisioning generation.

	return nil
}

// deriveCanonicalFromLegacy converts d's legacy Storages/Paths fields into a
// canonical AppVolumes/AppSources/AppMounts triple, replaying the old
// runtime's effective behavior (see MigrateStorageToCanonical doc comment),
// and validates the result. It does not mutate d. Used both by
// MigrateStorageToCanonical (fresh V1 → V2 migration) and
// RepairLegacyMigrationShape (correcting configs produced by the old,
// direct row-to-row migration).
func deriveCanonicalFromLegacy(d *Deployment, defaultSize string) (AppVolumes, AppSources, AppMounts, error) {
	// --- Pre-seed reserved names with git and s3 source names ---
	// Must happen before synthesizing directory sources to avoid collisions.
	usedSourceNames := make(map[string]bool)
	for _, gc := range d.Storages.GitClones {
		if gc.Name != "" {
			usedSourceNames[gc.Name] = true
		}
	}
	for _, s3m := range d.Storages.S3Mounts {
		if s3m.Name != "" {
			usedSourceNames[s3m.Name] = true
		}
	}

	newVolumes := make(AppVolumes, 0, len(d.Storages.Volumes))
	newSources := make(AppSources, 0)

	// --- Volumes + synthesized directory sources ---
	// Maps legacy volume name to the synthesized directory-source name for use
	// when flattening path mappings.
	volToSrcName := make(map[string]string)

	size := NormalizeSizeWithUnit(defaultSize)

	// Replay the legacy runtime's pool-grouping behavior (pre-canonical
	// OpenSVCGetAppVolumeSections grouped Storages.Volumes by PoolName via
	// Volumes.GroupByPool() and provisioned exactly one OpenSVC volume per
	// pool — named "{appname}-{pool}" — merging every legacy volume's
	// directory into that single physical volume).
	//
	// Migrating must preserve that *effective* layout, not the legacy row
	// shape: each pool group becomes exactly one canonical AppVolume, with
	// one directory AppSource per legacy volume that fed it. The merged
	// AppVolume's RuntimeName carries the old "{appname}-{pool}" identity so
	// an already-provisioned physical volume is never orphaned by switching
	// to per-volume strategy. Pools containing a single legacy volume
	// degrade naturally into a one-volume/one-source group — same shape as
	// a direct conversion, just produced via the same merged-group path.
	var poolOrder []string
	poolVolumes := make(map[string][]*Volume)
	for _, lv := range d.Storages.Volumes {
		if lv.Name == "" {
			continue
		}
		if _, ok := poolVolumes[lv.PoolName]; !ok {
			poolOrder = append(poolOrder, lv.PoolName)
		}
		poolVolumes[lv.PoolName] = append(poolVolumes[lv.PoolName], lv)
	}

	// Legacy GitClones/S3Mounts/Paths reference legacy volumes by their
	// original row name (VolumeName). Since pool-grouping now collapses
	// several legacy volume names into one merged AppVolume named after the
	// pool, those references must be remapped to the merged volume's name.
	legacyVolToCanonicalVol := make(map[string]string, len(d.Storages.Volumes))

	for _, pool := range poolOrder {
		vol := &AppVolume{
			Name:            pool,
			Pool:            pool,
			Size:            size,
			CanonicalOrigin: CanonicalStorageOriginLegacyPooledV1,
			RuntimeName:     fmt.Sprintf("{name}-%s", pool),
		}
		newVolumes = append(newVolumes, vol)

		for _, lv := range poolVolumes[pool] {
			legacyVolToCanonicalVol[lv.Name] = vol.Name

			if lv.VolumeDir == "" {
				continue
			}
			bp := normalizePath(lv.VolumeDir)
			if bp == "" {
				bp = "/" + lv.VolumeDir
			}
			srcName := synthesizeSourceName(lv.Name, usedSourceNames)
			usedSourceNames[srcName] = true
			volToSrcName[lv.Name] = srcName

			src := &AppSource{
				Type:       AppSourceDirectory,
				Name:       srcName,
				VolumeName: vol.Name,
				BasePath:   bp,
			}
			newSources = append(newSources, src)
		}
	}

	// --- Git clones → AppSource{Type:git} ---
	for _, gc := range d.Storages.GitClones {
		if gc.Name == "" {
			continue
		}
		basePath := normalizePath(gc.VolumeDir)
		if basePath == "" {
			basePath = "/" + gc.Name
		}

		volName := gc.VolumeName
		if cv, ok := legacyVolToCanonicalVol[volName]; ok {
			volName = cv
		}

		src := &AppSource{
			Type:       AppSourceGit,
			Name:       gc.Name,
			VolumeName: volName,
			BasePath:   basePath,
			Repo:       gc.GitRepo,
			Branch:     gc.GitBranch,
			User:       gc.GitUser,
			Pass:       gc.GitPass,
		}
		newSources = append(newSources, src)
	}

	// --- S3 mounts → AppSource{Type:s3} ---
	for _, s3m := range d.Storages.S3Mounts {
		if s3m.Name == "" {
			continue
		}
		basePath := normalizePath(s3m.VolumeDir)
		if basePath == "" {
			basePath = "/" + s3m.Name
		}

		volName := s3m.VolumeName
		if cv, ok := legacyVolToCanonicalVol[volName]; ok {
			volName = cv
		}

		src := &AppSource{
			Type:         AppSourceS3,
			Name:         s3m.Name,
			VolumeName:   volName,
			BasePath:     basePath,
			Endpoint:     s3m.Endpoint,
			Bucket:       s3m.Bucket,
			Region:       s3m.Region,
			AccessKey:    s3m.AccessKey,
			SecretKey:    s3m.SecretKey,
			ProviderName: s3m.ProviderName,
			MountDir:     s3m.MountDir,
		}
		newSources = append(newSources, src)
	}

	// Build source-name index (all newly synthesized sources) for mount resolution.
	// git/s3 sources are reachable by their own Name.
	allSrcNames := make(map[string]bool)
	for _, s := range newSources {
		allSrcNames[s.Name] = true
	}

	// --- Paths → AppMount ---
	// Build a path-by-name map to flatten parent/child chains.
	pathByName := make(map[string]*PathMapping, len(d.Paths))
	for _, p := range d.Paths {
		if p.Name != "" {
			pathByName[p.Name] = p
		}
	}

	newMounts := make(AppMounts, 0, len(d.Paths))
	seenTargets := make(map[string]bool)

	for _, p := range d.Paths {
		targetPath := p.DockerPath
		if targetPath != "" && !strings.Contains(targetPath, "..") {
			targetPath = normalizeCanonicalMountTargetPath(targetPath)
		}
		if targetPath == "" || seenTargets[targetPath] {
			continue
		}

		sourceName, subPath := resolveLegacyPathSource(p, pathByName, volToSrcName, allSrcNames)
		if sourceName == "" {
			continue
		}

		mount := &AppMount{
			Name:          p.Name,
			SourceName:    sourceName,
			SourceSubPath: subPath,
			TargetPath:    targetPath,
		}
		newMounts = append(newMounts, mount)
		seenTargets[targetPath] = true
	}

	// --- Validate before returning ---
	testDep := &Deployment{
		AppVolumes: newVolumes,
		AppSources: newSources,
		AppMounts:  newMounts,
	}
	if err := testDep.ValidateCanonicalStorage(); err != nil {
		return nil, nil, nil, fmt.Errorf("storage migration validation failed: %w", err)
	}

	return newVolumes, newSources, newMounts, nil
}

// EnsureCanonicalStorage runs MigrateStorageToCanonical when needed.
// It is safe to call on every load/save boundary. The returned bool reports
// whether a migration actually rewrote the deployment, so callers can decide
// whether the result needs to be persisted.
func EnsureCanonicalStorage(d *Deployment, defaultSize string) (bool, error) {
	if d == nil || d.StorageLayoutVersion >= StorageLayoutV2 {
		return false, nil
	}
	if err := MigrateStorageToCanonical(d, defaultSize); err != nil {
		return false, err
	}
	return true, nil
}

// HasLegacyMigrationShape reports whether d's canonical storage model was
// produced by the old, row-based migration (one AppVolume per legacy Volume
// row, stamped PhysicalVolumeStrategyLegacyPooled) rather than the current
// behavior-based one (one AppVolume per effective old pool-group, with a
// RuntimeName override, stamped PhysicalVolumeStrategyPerVolume).
//
// The check is deliberately conservative — it must not flag genuine
// hand-authored v2 configs. The combined signals below only co-occur as a
// side effect of the old migration's mechanical row-by-row conversion, never
// from intentional authoring (a hand-authored config has no reason to set the
// now-exceptional legacy-pooled strategy AND mirror the legacy
// Storages.Volumes shape AND omit every RuntimeName — and indeed nothing in
// the current write paths, API or UI, ever sets PhysicalVolumeStrategy to
// legacy-pooled; it is exposed read-only):
//
//  1. PhysicalVolumeStrategy == legacy-pooled (the old migration's fixed stamp;
//     the corrected migration always produces per-volume, and no write path
//     sets this value going forward).
//  2. No AppVolume carries a RuntimeName (the old migration never set it; the
//     corrected one always does).
//  3. The canonical shape mirrors the preserved legacy shadow row-for-row:
//     same count, each AppVolume's Name/Pool match a legacy Volume's
//     Name/PoolName, and each legacy VolumeDir still has the synthesized
//     directory-root AppSource signature the old migration emitted
//     (VolumeName=<row name>, BasePath=<volumedir>, source name prefixed by
//     "<row name>-root"). This extra source-shape check keeps the detector
//     focused on truly legacy-derived rows and avoids re-repairing configs
//     that were already edited under V2 semantics (for example, a user-added
//     same-pool volume with an arbitrary source name).
//
// Note this deliberately does NOT require any pool to currently be shared by
// more than one legacy volume. The old migration produced this exact shape
// regardless of sharing — and a config stuck in this shape stays stamped
// legacy-pooled forever, so OpenSVCGetAppCanonicalLegacyPooledVolumeSections
// keeps grouping its (current) AppVolumes by pool on every provisioning run.
// Any same-pool AppVolume added later — entirely normal under the per-volume
// model — would then silently merge into the old pooled physical volume,
// which is exactly the forever-re-merge bug this migration rewrite exists to
// eliminate. Such configs must be repaired (renamed to the pool identity,
// RuntimeName set, strategy flipped to per-volume) just like merged-group ones.
func (d *Deployment) HasLegacyMigrationShape() bool {
	if !d.IsCanonical() || d.PhysicalVolumeStrategy != PhysicalVolumeStrategyLegacyPooled || d.CanonicalStorageOrigin != "" {
		return false
	}
	if len(d.AppVolumes) == 0 || len(d.AppVolumes) != len(d.Storages.Volumes) {
		return false
	}

	legacyByName := make(map[string]*Volume, len(d.Storages.Volumes))
	for _, lv := range d.Storages.Volumes {
		if lv.Name == "" {
			return false
		}
		legacyByName[lv.Name] = lv
	}

	dirRootsByVolume := make(map[string]map[string]bool, len(d.AppSources))
	for _, src := range d.AppSources {
		if src == nil || src.Type != AppSourceDirectory || src.VolumeName == "" {
			continue
		}
		if !strings.HasPrefix(src.Name, src.VolumeName+"-root") {
			continue
		}
		basePath := normalizePath(src.BasePath)
		if basePath == "" && src.BasePath != "" {
			basePath = "/" + src.BasePath
		}
		if _, ok := dirRootsByVolume[src.VolumeName]; !ok {
			dirRootsByVolume[src.VolumeName] = make(map[string]bool)
		}
		dirRootsByVolume[src.VolumeName][basePath] = true
	}

	for _, v := range d.AppVolumes {
		if v.RuntimeName != "" {
			return false
		}
		lv, ok := legacyByName[v.Name]
		if !ok || lv.PoolName != v.Pool {
			return false
		}
		if lv.VolumeDir != "" {
			basePath := normalizePath(lv.VolumeDir)
			if basePath == "" {
				basePath = "/" + lv.VolumeDir
			}
			if !dirRootsByVolume[v.Name][basePath] {
				return false
			}
		}
	}
	return true
}

// RepairLegacyMigrationShape corrects a deployment whose canonical storage
// model was produced by the old, row-based migration (see
// HasLegacyMigrationShape) by rebuilding it from the preserved legacy
// shadow using the current behavior-based algorithm — one merged AppVolume
// per effective old pool-group, RuntimeName preserving the old pooled
// physical-volume identity, strategy per-volume.
//
// Returns false, nil when no repair is needed (or possible). On error the
// deployment is left untouched.
//
// Safe to invoke regardless of provisioned state. Rebuilding the canonical
// model changes which AppVolume rows exist (fewer, merged ones), but
// RuntimeName preserves the *physical* OpenSVC volume identity
// ({appname}-{pool}) for each merged group, so an already-running app's
// physical volumes and mounted directory contributions are unaffected — this
// is exactly the same deriveCanonicalFromLegacy transition that
// MigrateStorageToCanonical already performs ungated on every load for
// V1-to-canonical migrations. Leaving already-provisioned apps unrepaired
// would strand precisely the configs that need correcting most in
// legacy-pooled's forever-re-merge behavior.
func RepairLegacyMigrationShape(d *Deployment, defaultSize string) (bool, error) {
	if d == nil || !d.HasLegacyMigrationShape() {
		return false, nil
	}

	newVolumes, newSources, newMounts, err := deriveCanonicalFromLegacy(d, defaultSize)
	if err != nil {
		return false, err
	}

	d.AppVolumes = newVolumes
	d.AppSources = newSources
	d.AppMounts = newMounts
	d.PhysicalVolumeStrategy = PhysicalVolumeStrategyPerVolume
	d.CanonicalStorageOrigin = CanonicalStorageOriginLegacyPooledV1

	return true, nil
}

// synthesizeSourceName returns a unique source name derived from volName that
// does not collide with any key already in used.
func synthesizeSourceName(volName string, used map[string]bool) string {
	base := volName + "-root"
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-root-%d", volName, i)
		if !used[candidate] {
			return candidate
		}
	}
}

// normalizePath ensures a path starts with "/" and applies filepath.Clean to
// remove double slashes, trailing slashes, and single-dot components.
// It does not resolve ".." — callers must reject ".." before calling this.
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return filepath.Clean(p)
}

// resolveLegacyPathSource walks the full parent chain of p to determine the
// canonical source name and subpath for the resulting AppMount.
func resolveLegacyPathSource(
	p *PathMapping,
	pathByName map[string]*PathMapping,
	volToSrcName map[string]string,
	allSrcNames map[string]bool,
) (sourceName, subPath string) {
	// Walk the parent chain to reach the root entry.
	chain := []*PathMapping{p}
	current := p
	for current.ParentName != "" {
		parent, ok := pathByName[current.ParentName]
		if !ok {
			break
		}
		chain = append(chain, parent)
		current = parent
	}
	// Reverse so chain[0] is the root.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	root := chain[0]

	switch root.SourceType {
	case SourceVolume:
		sn, ok := volToSrcName[root.SourceName]
		if !ok {
			return "", ""
		}
		sourceName = sn
	case SourceGit, SourceS3:
		if !allSrcNames[root.SourceName] {
			return "", ""
		}
		sourceName = root.SourceName
	default:
		if root.VolumeName != "" {
			sn, ok := volToSrcName[root.VolumeName]
			if !ok {
				return "", ""
			}
			sourceName = sn
		} else {
			return "", ""
		}
	}

	// Accumulate path segments from all chain entries.
	var parts []string
	for _, entry := range chain {
		seg := entry.SourcePath
		if seg == "" || seg == "." {
			continue
		}
		parts = append(parts, seg)
	}

	if len(parts) > 0 {
		joined := filepath.Join(parts...)
		if !strings.HasPrefix(joined, "/") {
			joined = "/" + joined
		}
		subPath = joined
	}

	return sourceName, subPath
}
