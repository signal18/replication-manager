package splitdump

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const manifestVersion = 1
const manifestFileName = "manifest"

// ErrManifestInvalid is returned when a manifest file exists but fails validation.
var ErrManifestInvalid = errors.New("splitdump manifest invalid")

// Manifest records the artifact creation order produced by a split operation.
// Phase slices use basename-only entries (no directory component).
// Schema holds table/view/routine/system artifacts; Data holds row-data artifacts
// (including shards); Post holds trigger and event artifacts.
type Manifest struct {
	Version int
	Schema  []string
	Data    []string
	Post    []string
}

// ReadManifest reads and parses the manifest file inside backupPath.
// Returns os.ErrNotExist (wrapped) when no manifest file is present.
func ReadManifest(backupPath string) (*Manifest, error) {
	manifestPath := filepath.Join(backupPath, manifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	return parseManifest(string(data))
}

// WriteManifest serialises m to backupPath/manifest, creating the file if needed.
func WriteManifest(outputDir string, m *Manifest) error {
	manifestPath := filepath.Join(outputDir, manifestFileName)
	f, err := os.Create(manifestPath)
	if err != nil {
		return fmt.Errorf("splitdump: create manifest %s: %w", manifestPath, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "version = %d\n\n", m.Version)
	writeManifestSection(w, "schema", m.Schema)
	writeManifestSection(w, "data", m.Data)
	writeManifestSection(w, "post", m.Post)
	return w.Flush()
}

func writeManifestSection(w *bufio.Writer, section string, entries []string) {
	fmt.Fprintf(w, "[%s]\n", section)
	for _, e := range entries {
		fmt.Fprintf(w, "%s\n", e)
	}
	fmt.Fprintf(w, "\n")
}

func parseManifest(content string) (*Manifest, error) {
	m := &Manifest{}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line[1 : len(line)-1])
			continue
		}
		if section == "" && strings.HasPrefix(line, "version") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				v := strings.TrimSpace(parts[1])
				var n int
				if _, scanErr := fmt.Sscanf(v, "%d", &n); scanErr == nil {
					m.Version = n
				}
			}
			continue
		}
		switch section {
		case "schema":
			m.Schema = append(m.Schema, line)
		case "data":
			m.Data = append(m.Data, line)
		case "post":
			m.Post = append(m.Post, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: scan error: %v", ErrManifestInvalid, err)
	}
	return m, nil
}

// ValidateManifest checks that m is internally consistent and references only
// basename-only, unique, non-cross-phase entries at a supported version.
// Each entry is also classified via classifyFile to ensure it is placed in the
// correct phase: schema entries must classify as fileCategorySchema, data entries
// as fileCategoryData, and post entries as fileCategoryPost.
func ValidateManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrManifestInvalid)
	}
	if m.Version != manifestVersion {
		return fmt.Errorf("%w: unsupported version %d (want %d)", ErrManifestInvalid, m.Version, manifestVersion)
	}
	seen := make(map[string]string) // basename → phase name
	phases := []struct {
		name        string
		entries     []string
		expectedCat fileCategory
	}{
		{"schema", m.Schema, fileCategorySchema},
		{"data", m.Data, fileCategoryData},
		{"post", m.Post, fileCategoryPost},
	}
	for _, phase := range phases {
		for _, entry := range phase.entries {
			if entry != filepath.Base(entry) {
				return fmt.Errorf("%w: entry %q is not a basename (contains path separator)", ErrManifestInvalid, entry)
			}
			if prev, ok := seen[entry]; ok {
				return fmt.Errorf("%w: entry %q appears in both %q and %q phases", ErrManifestInvalid, entry, prev, phase.name)
			}
			seen[entry] = phase.name
			// Classify with restoreUser=true so mysql.system-all is accepted in the schema
			// phase (the splitter always records it there).
			cat, ok := classifyFile(entry, true)
			if !ok {
				return fmt.Errorf("%w: entry %q in phase %q is not a recognised splitdump artifact", ErrManifestInvalid, entry, phase.name)
			}
			if cat != phase.expectedCat {
				return fmt.Errorf("%w: entry %q is declared in phase %q but classifies as phase %q",
					ErrManifestInvalid, entry, phase.name, categoryName(cat))
			}
		}
	}
	return nil
}

// categoryName returns a human-readable label for a fileCategory, used in error messages.
func categoryName(cat fileCategory) string {
	switch cat {
	case fileCategorySchema:
		return "schema"
	case fileCategoryData:
		return "data"
	case fileCategoryPost:
		return "post"
	default:
		return "unknown"
	}
}
