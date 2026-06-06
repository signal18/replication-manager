package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MigrateStorageToCanonical translates legacy storages+paths into the canonical
// AppVolumes/AppSources/AppMounts model and stamps the deployment with v2 metadata.
//
// Rules:
//  1. If canonical model already exists (StorageLayoutVersion >= V2), this is a no-op.
//  2. Otherwise: legacy Volume → AppVolume + synthesized directory AppSource.
//     legacy GitClone → AppSource{Type:git}.
//     legacy S3Mount  → AppSource{Type:s3}.
//     legacy PathMapping → AppMount (parent/child graphs are flattened).
//  3. Migrated legacy apps always get PhysicalVolumeStrategyLegacyPooled so the
//     existing pooled OpenSVC layout is preserved until an explicit cutover.
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

	for _, lv := range d.Storages.Volumes {
		if lv.Name == "" {
			continue
		}
		vol := &AppVolume{
			Name: lv.Name,
			Pool: lv.PoolName,
			Size: size,
		}
		newVolumes = append(newVolumes, vol)

		// Synthesize a directory source for the volume's legacy root dir.
		if lv.VolumeDir != "" {
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
				VolumeName: lv.Name,
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

		src := &AppSource{
			Type:       AppSourceGit,
			Name:       gc.Name,
			VolumeName: gc.VolumeName,
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

		src := &AppSource{
			Type:         AppSourceS3,
			Name:         s3m.Name,
			VolumeName:   s3m.VolumeName,
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

	// --- Validate before writing ---
	testDep := &Deployment{
		AppVolumes: newVolumes,
		AppSources: newSources,
		AppMounts:  newMounts,
	}
	if err := testDep.ValidateCanonicalStorage(); err != nil {
		return fmt.Errorf("storage migration validation failed: %w", err)
	}

	// --- Commit ---
	d.AppVolumes = newVolumes
	d.AppSources = newSources
	d.AppMounts = newMounts
	d.StorageLayoutVersion = StorageLayoutV2
	d.PhysicalVolumeStrategy = PhysicalVolumeStrategyLegacyPooled

	// Legacy fields (Storages.Volumes, GitClones, S3Mounts, Paths) are intentionally
	// preserved as compatibility shadows so existing legacy readers (GetGitClone,
	// GetS3Mount, legacy API handlers) continue to function without change.
	// They must not be used as write targets after migration — canonical fields are
	// the sole write target for all new edits and provisioning generation.

	return nil
}

// EnsureCanonicalStorage runs MigrateStorageToCanonical when needed.
// It is safe to call on every load/save boundary.
func EnsureCanonicalStorage(d *Deployment, defaultSize string) error {
	if d == nil || d.StorageLayoutVersion >= StorageLayoutV2 {
		return nil
	}
	return MigrateStorageToCanonical(d, defaultSize)
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
