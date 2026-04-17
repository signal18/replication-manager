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
}

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
					res.UnresolvedParentReferences = append(res.UnresolvedParentReferences, parentName)
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

	levelsChanged := applyPathLevels(paths, &res)
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

func applyPathLevels(paths []any, res *AppTemplateCanonicalizationResult) int {
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

	changed := 0
	for _, item := range paths {
		pm, ok := asAnyMap(item)
		if !ok {
			continue
		}

		newLevel := 0
		parentName := asTrimmedString(pm["parentname"])
		if parentName != "" {
			parent, found := nameToPath[parentName]
			if !found {
				res.UnresolvedParentReferences = append(res.UnresolvedParentReferences, parentName)
				continue
			}
			newLevel = pmToInt(parent["level"], 0) + 1
		}

		oldLevel := pmToInt(pm["level"], -1)
		if oldLevel != newLevel {
			pm["level"] = newLevel
			changed++
		}
	}

	return changed
}

func asAnyMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	if m, ok := v.(map[string]interface{}); ok {
		return map[string]any(m), true
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
