package config

import (
	"bytes"
	"errors"
	"strings"

	"github.com/pelletier/go-toml"
)

type AppTemplateCanonicalizationResult struct {
	Changed                    bool
	UpdatedParentNames         int
	InferredParentNames        int
	UpdatedLevels              int
	UpdatedRootSourcePaths     int
	UpdatedEmptySourcePaths    int
	UnresolvedParentReferences []string

	// MergedVolumePools is the number of deployment.storages.volumes pools
	// whose legacy rows were canonicalized into a single row.
	MergedVolumePools int
	// MergedVolumeRows is the total number of legacy volume rows absorbed
	// across all merged pools (including the row that becomes canonical).
	MergedVolumeRows int
	// RewrittenVolumeReferences is the number of entries (deployment.paths,
	// git-clones, s3-mounts rows) that had at least one of their
	// srcname/volumename fields retargeted to a merged volume's canonical
	// name. An entry with both fields rewritten is still counted once.
	RewrittenVolumeReferences int

	// StampedAppConfigVersion reports whether the top-level
	// app-config-version marker was missing or stale and was (re)written to
	// AppConfigVersionV2.
	StampedAppConfigVersion bool
}

// AppConfigVersionV2 is the persisted app-config-version value that marks
// app/template TOML content as already matching the V1 -> V2 migration
// baseline implemented by CanonicalizeAppTemplateRaw/CanonicalizeAppVolumesRaw.
const AppConfigVersionV2 = 2

// appConfigVersionKey is the top-level TOML key holding the persisted
// app-config-version marker.
const appConfigVersionKey = "app-config-version"

func CanonicalizeAppTemplateTOML(content []byte) ([]byte, AppTemplateCanonicalizationResult, error) {
	var res AppTemplateCanonicalizationResult

	t, err := toml.LoadBytes(content)
	if err != nil {
		return nil, res, err
	}

	raw := t.ToMap()
	res, err = CanonicalizeAppTemplateRaw(raw)
	if err != nil {
		return nil, res, err
	}

	if !res.Changed {
		return content, res, nil
	}

	t, err = toml.TreeFromMap(raw)
	if err != nil {
		return nil, res, err
	}

	var buf bytes.Buffer
	if _, err := t.WriteTo(&buf); err != nil {
		return nil, res, err
	}

	return buf.Bytes(), res, nil
}

func CanonicalizeAppTemplateRaw(raw map[string]any) (AppTemplateCanonicalizationResult, error) {
	var res AppTemplateCanonicalizationResult
	unresolvedSet := make(map[string]struct{})

	deployment, ok := asAnyMap(raw["deployment"])
	if !ok {
		return res, nil
	}

	rawPaths, ok := deployment["paths"]
	if !ok {
		return res, nil
	}

	paths, ok := asAnySlice(rawPaths)
	if !ok {
		return res, errors.New("deployment.paths must be an array")
	}

	pathNames := make(map[string]struct{}, len(paths))
	dockerPathToName := make(map[string]string, len(paths))

	for _, p := range paths {
		pm, ok := asAnyMap(p)
		if !ok {
			continue
		}
		name := asTrimmedString(pm["name"])
		dockerPath := asTrimmedString(pm["dockerpath"])
		if name != "" {
			pathNames[name] = struct{}{}
		}
		if dockerPath != "" && name != "" {
			dockerPathToName[dockerPath] = name
		}
	}

	for idx, p := range paths {
		pm, ok := asAnyMap(p)
		if !ok {
			continue
		}

		parentName := asTrimmedString(pm["parentname"])
		if parentName != "" {
			if _, ok := pathNames[parentName]; !ok {
				if canonicalParent, found := dockerPathToName[parentName]; found {
					pm["parentname"] = canonicalParent
					parentName = canonicalParent
					res.Changed = true
					res.UpdatedParentNames++
				} else {
					appendUnresolvedParentReference(&res, unresolvedSet, parentName)
				}
			}
		} else if idx > 0 {
			dockerPath := asTrimmedString(pm["dockerpath"])
			if inferredParent := inferParentByDockerPath(paths[:idx], dockerPath); inferredParent != "" {
				pm["parentname"] = inferredParent
				res.Changed = true
				res.InferredParentNames++
			}
		}

		sourceType := asTrimmedString(pm["srctype"])
		sourceName := asTrimmedString(pm["srcname"])
		sourcePath := asTrimmedString(pm["srcpath"])
		if sourceType != "" && sourceName != "" {
			switch sourcePath {
			case "/":
				pm["srcpath"] = "."
				res.Changed = true
				res.UpdatedRootSourcePaths++
			case "":
				pm["srcpath"] = "."
				res.Changed = true
				res.UpdatedEmptySourcePaths++
			}
		}
	}

	levelsChanged := applyPathLevels(paths, &res, unresolvedSet)
	if levelsChanged > 0 {
		res.Changed = true
		res.UpdatedLevels += levelsChanged
	}

	return res, nil
}

func inferParentByDockerPath(previous []any, dockerPath string) string {
	current := strings.TrimSuffix(strings.TrimSpace(dockerPath), "/")
	if current == "" {
		return ""
	}

	bestMatch := ""
	bestLen := -1
	for _, item := range previous {
		pm, ok := asAnyMap(item)
		if !ok {
			continue
		}
		parentPath := strings.TrimSuffix(asTrimmedString(pm["dockerpath"]), "/")
		parentName := asTrimmedString(pm["name"])
		if parentPath == "" || parentName == "" {
			continue
		}
		if current == parentPath || strings.HasPrefix(current, parentPath+"/") {
			if len(parentPath) > bestLen {
				bestLen = len(parentPath)
				bestMatch = parentName
			}
		}
	}

	return bestMatch
}

func applyPathLevels(paths []any, res *AppTemplateCanonicalizationResult, unresolvedSet map[string]struct{}) int {
	nameToPath := make(map[string]map[string]any, len(paths))
	for _, item := range paths {
		pm, ok := asAnyMap(item)
		if !ok {
			continue
		}
		if name := asTrimmedString(pm["name"]); name != "" {
			nameToPath[name] = pm
		}
	}

	memo := make(map[string]int, len(nameToPath))
	visiting := make(map[string]bool, len(nameToPath))

	var levelForName func(name string) (int, bool)
	levelForName = func(name string) (int, bool) {
		if lvl, ok := memo[name]; ok {
			return lvl, true
		}
		pm, found := nameToPath[name]
		if !found {
			appendUnresolvedParentReference(res, unresolvedSet, name)
			return 0, false
		}
		if visiting[name] {
			appendUnresolvedParentReference(res, unresolvedSet, name)
			return 0, false
		}
		visiting[name] = true

		lvl := 0
		parentName := asTrimmedString(pm["parentname"])
		if parentName != "" {
			parentLevel, ok := levelForName(parentName)
			if !ok {
				delete(visiting, name)
				return 0, false
			}
			lvl = parentLevel + 1
		}

		memo[name] = lvl
		delete(visiting, name)
		return lvl, true
	}

	changed := 0
	for _, item := range paths {
		pm, ok := asAnyMap(item)
		if !ok {
			continue
		}

		newLevel := 0
		parentName := asTrimmedString(pm["parentname"])
		if parentName != "" {
			parentLevel, ok := levelForName(parentName)
			if !ok {
				continue
			}
			newLevel = parentLevel + 1
		} else if name := asTrimmedString(pm["name"]); name != "" {
			if lvl, ok := levelForName(name); ok {
				newLevel = lvl
			}
		}

		oldLevel := pmToInt(pm["level"], -1)
		if oldLevel != newLevel {
			pm["level"] = newLevel
			changed++
		}
	}

	return changed
}

func appendUnresolvedParentReference(res *AppTemplateCanonicalizationResult, seen map[string]struct{}, parentName string) {
	if res == nil {
		return
	}
	name := strings.TrimSpace(parentName)
	if name == "" {
		return
	}
	if seen != nil {
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
	}
	res.UnresolvedParentReferences = append(res.UnresolvedParentReferences, name)
}

func asAnyMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	return nil, false
}

func asAnySlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.([]map[string]any); ok {
		out := make([]any, 0, len(s))
		for _, it := range s {
			out = append(out, it)
		}
		return out, true
	}
	if s, ok := v.([]map[string]interface{}); ok {
		out := make([]any, 0, len(s))
		for _, it := range s {
			out = append(out, map[string]any(it))
		}
		return out, true
	}
	if s, ok := v.([]any); ok {
		return s, true
	}
	if s, ok := v.([]interface{}); ok {
		out := make([]any, 0, len(s))
		for _, it := range s {
			out = append(out, it)
		}
		return out, true
	}
	return nil, false
}

func asTrimmedString(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func pmToInt(v any, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return def
	}
}

// CanonicalizeAppVolumesTOML rewrites deployment.storages.volumes so that
// each OpenSVC pool is represented by a single row, named using the
// convention historically produced at runtime by App.GetAppVolumeName:
// "{name}-<pool>" for template content (appName == "") or "<appName>-<pool>"
// for resolved app content (appName != ""). References in
// deployment.paths, deployment.storages.git-clones and
// deployment.storages.s3-mounts are rewritten to the merged row's name.
//
// srcpath (on paths) and volumedir (on git-clones/s3-mounts) are left
// untouched: both are paths relative to the pool's OpenSVC volume mount
// (named via App.GetAppVolumeName(pool, ...)), which is keyed by poolname
// and therefore identical before and after merging same-pool rows.
//
// Collapsing multiple rows on the same pool into one only applies to
// unflagged/V1 content (app-config-version < AppConfigVersionV2); see
// CanonicalizeAppVolumesRaw for the V2 gate and the template-placeholder
// rename exception that still applies to V2 content.
func CanonicalizeAppVolumesTOML(content []byte, appName string) ([]byte, AppTemplateCanonicalizationResult, error) {
	var res AppTemplateCanonicalizationResult

	t, err := toml.LoadBytes(content)
	if err != nil {
		return nil, res, err
	}

	raw := t.ToMap()
	res, err = CanonicalizeAppVolumesRaw(raw, appName)
	if err != nil {
		return nil, res, err
	}

	if !res.Changed {
		return content, res, nil
	}

	t, err = toml.TreeFromMap(raw)
	if err != nil {
		return nil, res, err
	}

	var buf bytes.Buffer
	if _, err := t.WriteTo(&buf); err != nil {
		return nil, res, err
	}

	return buf.Bytes(), res, nil
}

// CanonicalizeAppVolumesRaw merges deployment.storages.volumes rows so that
// each poolname has exactly one row named per the historical
// App.GetAppVolumeName convention, and rewrites references accordingly. See
// CanonicalizeAppVolumesTOML for details.
//
// Phase 11 V2 gate: once content is flagged app-config-version >=
// AppConfigVersionV2, multiple rows sharing a poolname are intentional and
// are no longer collapsed into one. The one exception is template-variable
// resolution: canonicalizeAppVolumes still renames a lone row that is still
// named with the unresolved OpenSVC "{name}-<pool>" placeholder to
// "<appName>-<pool>" when appName is non-empty -- that is instantiating or
// resetting a template for a specific app, not an ambiguous V1 duplicate.
// Unflagged/V1 content (app-config-version < AppConfigVersionV2) always goes
// through the full merge below, regardless of row count.
func CanonicalizeAppVolumesRaw(raw map[string]any, appName string) (AppTemplateCanonicalizationResult, error) {
	var res AppTemplateCanonicalizationResult

	deployment, ok := asAnyMap(raw["deployment"])
	if !ok {
		return res, nil
	}

	storages, ok := asAnyMap(deployment["storages"])
	if !ok {
		return res, nil
	}

	rawVolumes, ok := storages["volumes"]
	if !ok {
		return res, nil
	}

	volumes, ok := asAnySlice(rawVolumes)
	if !ok {
		return res, errors.New("deployment.storages.volumes must be an array")
	}
	if len(volumes) == 0 {
		return res, nil
	}

	isV1 := AppConfigVersionFromRaw(raw) < AppConfigVersionV2

	if err := canonicalizeAppVolumes(deployment, storages, volumes, appName, isV1, &res); err != nil {
		return res, err
	}

	return res, nil
}

// canonicalVolumeName mirrors App.GetAppVolumeName: template content keeps
// the OpenSVC "{name}" placeholder, resolved app content uses the app name.
func canonicalVolumeName(appName, pool string) string {
	if appName != "" {
		return appName + "-" + pool
	}
	return "{name}-" + pool
}

func canonicalizeAppVolumes(deployment, storages map[string]any, volumes []any, appName string, isV1 bool, res *AppTemplateCanonicalizationResult) error {
	type volumeRow struct {
		raw  map[string]any
		name string
		dir  string
	}

	poolOrder := make([]string, 0, len(volumes))
	rowsByPool := make(map[string][]volumeRow, len(volumes))

	for _, v := range volumes {
		vm, ok := asAnyMap(v)
		if !ok {
			return errors.New("deployment.storages.volumes entries must be tables")
		}
		pool := asTrimmedString(vm["poolname"])
		if pool == "" {
			return errors.New("deployment.storages.volumes entry missing poolname")
		}
		if _, seen := rowsByPool[pool]; !seen {
			poolOrder = append(poolOrder, pool)
		}
		rowsByPool[pool] = append(rowsByPool[pool], volumeRow{
			raw:  vm,
			name: asTrimmedString(vm["name"]),
			dir:  asTrimmedString(vm["volumedir"]),
		})
	}

	renames := make(map[string]string)
	newVolumes := make([]any, 0, len(poolOrder))

	for _, pool := range poolOrder {
		rows := rowsByPool[pool]
		canonicalName := canonicalVolumeName(appName, pool)

		if len(rows) == 1 && rows[0].name == canonicalName {
			row := rows[0]
			if normalized := NormalizeVolumeDirs(row.dir); normalized != row.dir {
				row.raw["volumedir"] = normalized
				res.Changed = true
			}
			newVolumes = append(newVolumes, row.raw)
			continue
		}

		if !isV1 {
			// V2: the only rewrite left is resolving a lone unresolved
			// "{name}-<pool>" template placeholder to "<appName>-<pool>" for
			// a specific app. Multiple rows on this pool, or a single row
			// with an intentionally custom name, are left as-is.
			placeholderName := canonicalVolumeName("", pool)
			if !(len(rows) == 1 && appName != "" && rows[0].name == placeholderName) {
				for _, row := range rows {
					newVolumes = append(newVolumes, row.raw)
				}
				continue
			}
		}

		// V1 same-pool duplicates are treated as one ambiguous logical volume, not
		// as independently meaningful persisted objects. The first row becomes the
		// canonical baseline carrier for any extra/raw fields, while later rows
		// contribute only to the merged directory set and row-name rewrite map.
		// Unknown/raw fields present only on rows[1..n] are intentionally not
		// merged, because there is no defined V1 conflict-resolution rule for them
		// and the canonicalization goal is to collapse the duplicates to one
		// deterministic baseline row.
		merged := make(map[string]any, len(rows[0].raw))
		for k, v := range rows[0].raw {
			merged[k] = v
		}

		dirs := make([]string, 0, len(rows))
		for _, row := range rows {
			dirs = append(dirs, row.dir)
		}

		merged["name"] = canonicalName
		merged["poolname"] = pool
		merged["volumedir"] = NormalizeVolumeDirs(dirs...)

		newVolumes = append(newVolumes, merged)

		res.Changed = true
		res.MergedVolumePools++
		res.MergedVolumeRows += len(rows)

		for _, row := range rows {
			if row.name == "" || row.name == canonicalName {
				continue
			}
			renames[row.name] = canonicalName
		}
	}

	if !res.Changed {
		return nil
	}

	storages["volumes"] = newVolumes

	if rawPaths, ok := deployment["paths"]; ok {
		paths, ok := asAnySlice(rawPaths)
		if !ok {
			return errors.New("deployment.paths must be an array")
		}
		for _, p := range paths {
			pm, ok := asAnyMap(p)
			if !ok {
				continue
			}

			rewritten := false

			if SourceType(asTrimmedString(pm["srctype"])) == SourceVolume {
				if srcName := asTrimmedString(pm["srcname"]); srcName != "" {
					if newName, found := renames[srcName]; found {
						pm["srcname"] = newName
						rewritten = true
					}
				}
			}

			// volumename is rewritten independently of srctype/srcname: it is the
			// field GetOpenSVCDeploymentPathMapping actually resolves against
			// (deployment.GetVolumeByName). Paths sourced from a git-clone or
			// s3-mount, or that inherited volumename from a parent path, persist
			// their own volumename distinct from srcname (see InsertPath/ResolvePath),
			// so it must be checked against renames on its own.
			if volName := asTrimmedString(pm["volumename"]); volName != "" {
				if newName, found := renames[volName]; found {
					pm["volumename"] = newName
					rewritten = true
				}
			}

			if rewritten {
				res.RewrittenVolumeReferences++
			}
		}
	}

	if err := rewriteVolumeNameReferences(storages, "git-clones", renames, res); err != nil {
		return err
	}
	if err := rewriteVolumeNameReferences(storages, "s3-mounts", renames, res); err != nil {
		return err
	}

	return nil
}

// rewriteVolumeNameReferences retargets volumename on entries under
// deployment.storages[key] (git-clones or s3-mounts) that reference a
// renamed volume. volumedir is left untouched: it is a path relative to the
// pool's OpenSVC volume mount, which does not change when same-pool rows are
// merged.
func rewriteVolumeNameReferences(storages map[string]any, key string, renames map[string]string, res *AppTemplateCanonicalizationResult) error {
	rawEntries, ok := storages[key]
	if !ok {
		return nil
	}

	entries, ok := asAnySlice(rawEntries)
	if !ok {
		return errors.New("deployment.storages." + key + " must be an array")
	}

	for _, e := range entries {
		em, ok := asAnyMap(e)
		if !ok {
			continue
		}

		oldName := asTrimmedString(em["volumename"])
		newName, found := renames[oldName]
		if !found {
			continue
		}

		em["volumename"] = newName
		res.RewrittenVolumeReferences++
	}

	return nil
}

// AppConfigVersionFromRaw returns the top-level app-config-version marker
// from raw TOML content, or 0 if it is missing or not an integer.
func AppConfigVersionFromRaw(raw map[string]any) int {
	return pmToInt(raw[appConfigVersionKey], 0)
}

// StampAppConfigVersionRaw sets the top-level app-config-version marker to
// AppConfigVersionV2 if it is missing or older than AppConfigVersionV2.
// Already-V2 (or newer) content is left untouched. Returns true if raw was
// modified.
func StampAppConfigVersionRaw(raw map[string]any) bool {
	if AppConfigVersionFromRaw(raw) >= AppConfigVersionV2 {
		return false
	}
	raw[appConfigVersionKey] = AppConfigVersionV2
	return true
}

// StampAppConfigVersionTOML ensures content carries a top-level
// app-config-version marker at or above AppConfigVersionV2, rewriting
// content only when the marker is missing or stale. Content whose marker is
// already >= AppConfigVersionV2 is returned unchanged (byte-identical), so
// repeated canonicalization passes over already-flagged V2 content do not
// keep rewriting it.
func StampAppConfigVersionTOML(content []byte) ([]byte, AppTemplateCanonicalizationResult, error) {
	var res AppTemplateCanonicalizationResult

	t, err := toml.LoadBytes(content)
	if err != nil {
		return nil, res, err
	}

	raw := t.ToMap()
	if !StampAppConfigVersionRaw(raw) {
		return content, res, nil
	}

	res.Changed = true
	res.StampedAppConfigVersion = true

	t, err = toml.TreeFromMap(raw)
	if err != nil {
		return nil, res, err
	}

	var buf bytes.Buffer
	if _, err := t.WriteTo(&buf); err != nil {
		return nil, res, err
	}

	return buf.Bytes(), res, nil
}
