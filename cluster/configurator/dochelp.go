// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// dochelp loads an enterprise variable→documentation mapping JSON and provides
// an on-demand lookup for the configurator tag content viewer.
//
// The JSON is pushed by the back office to paid instances via the git pull repo
// (plugins/data/enterprise-dochelp-variables.json). A compiled-in default is
// embedded via go:embed as a baseline.
//
// This is NOT a logplugin — it's a stateless lookup used by the API handler
// when the user clicks the "Doc Help" button on a config tag.
package configurator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/share"
)

// DocHelpVariable is one variable→documentation mapping entry.
type DocHelpVariable struct {
	Name        string          `json:"name"`
	MariaDBURL  string          `json:"mariadb_url"`
	MySQLURL    string          `json:"mysql_url"`
	Description string          `json:"description"`
	Blogs       []DocHelpBlog   `json:"blogs,omitempty"`
}

// DocHelpBlog is a reference to a community blog post about a variable.
type DocHelpBlog struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// DocHelpResult is returned by LookupTagDocHelp.
type DocHelpResult struct {
	Tag              string            `json:"tag"`
	Variables        []DocHelpVariable `json:"variables"`
	UnknownVariables []string          `json:"unknown_variables,omitempty"`
}

type docHelpFile struct {
	Variables map[string]docHelpEntry `json:"variables"`
}

type docHelpEntry struct {
	MariaDBURL  string        `json:"mariadb_url"`
	MySQLURL    string        `json:"mysql_url"`
	Description string        `json:"description"`
	Blogs       []DocHelpBlog `json:"blogs,omitempty"`
}

// DocHelp is a thread-safe variable documentation lookup loaded from JSON.
type DocHelp struct {
	mu       sync.RWMutex
	entries  map[string]docHelpEntry
	dataDir  string
	loadOnce sync.Once
}

// NewDocHelp creates a new DocHelp instance. pluginDataDir is the path to
// the directory where the back-office-deployed JSON may be found.
func NewDocHelp(pluginDataDir string) *DocHelp {
	return &DocHelp{dataDir: pluginDataDir}
}

func (dh *DocHelp) load() {
	var raw []byte

	// Prefer on-disk file pushed by the back office.
	if dh.dataDir != "" {
		dataFile := filepath.Join(dh.dataDir, "enterprise-dochelp-variables.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
		}
	}

	// Fall back to the embedded default in share/plugins/data/.
	if raw == nil {
		raw, _ = share.EmbededDbModuleFS.ReadFile("plugins/data/enterprise-dochelp-variables.json")
	}

	var data docHelpFile
	if err := json.Unmarshal(raw, &data); err != nil {
		dh.entries = make(map[string]docHelpEntry)
		return
	}
	// Re-index by normalised key so lookups are case/hyphen/prefix-insensitive.
	dh.entries = make(map[string]docHelpEntry, len(data.Variables))
	for key, entry := range data.Variables {
		dh.entries[NormaliseVariableName(key)] = entry
	}
}

// NormaliseVariableName applies MySQL/MariaDB variable name normalisation:
//   - lowercase
//   - hyphens → underscores (MySQL treats them as equivalent)
//   - dots → underscores (MySQL 8.0 component notation: validate_password.policy → validate_password_policy)
//   - strip "loose_" prefix (MySQL loose option prefix — variable is the part after)
//
// This must be used consistently everywhere: parsing cnf, building the
// dochelp index, and looking up variables.
func NormaliseVariableName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.TrimPrefix(name, "loose_")
	return name
}

// LookupVariables returns documentation for the given variable names.
// Unknown variables are returned in a separate list.
func (dh *DocHelp) LookupVariables(names []string) (matched []DocHelpVariable, unknown []string) {
	dh.loadOnce.Do(dh.load)
	dh.mu.RLock()
	defer dh.mu.RUnlock()

	for _, name := range names {
		normalised := NormaliseVariableName(name)
		if entry, ok := dh.entries[normalised]; ok {
			matched = append(matched, DocHelpVariable{
				Name:        normalised,
				MariaDBURL:  entry.MariaDBURL,
				MySQLURL:    entry.MySQLURL,
				Description: entry.Description,
				Blogs:       entry.Blogs,
			})
		} else {
			unknown = append(unknown, normalised)
		}
	}
	return
}

// Reload forces a re-read of the JSON data file (e.g. after a git pull).
func (dh *DocHelp) Reload() {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	dh.load()
	// Reset loadOnce so future calls to LookupVariables don't skip the load.
	dh.loadOnce = sync.Once{}
}
