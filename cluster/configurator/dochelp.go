// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// dochelp loads an enterprise variable→documentation mapping JSON and provides
// an on-demand lookup for the configurator tag content viewer.
//
// The JSON is pushed by the back office to paid instances via the git pull repo
// (plugins/data/enterprise-dochelp-variables.json). A compiled-in default is
// embedded via share.EmbededDbModuleFS as a baseline.
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
	"sync/atomic"
	"unsafe"

	"github.com/signal18/replication-manager/share"
)

// DocHelpVariable is one variable→documentation mapping entry.
type DocHelpVariable struct {
	Name        string        `json:"name"`
	MariaDBURL  string        `json:"mariadb_url"`
	MySQLURL    string        `json:"mysql_url"`
	Description string        `json:"description"`
	Blogs       []DocHelpBlog `json:"blogs,omitempty"`
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
// It uses an atomic pointer to the entries map so Reload is lock-free and
// there are no data races between concurrent LookupVariables and Reload calls.
type DocHelp struct {
	entries  unsafe.Pointer // *map[string]docHelpEntry — swapped atomically
	dataDir  string
	loadOnce sync.Once
}

// NewDocHelp creates a new DocHelp instance. pluginDataDir is the path to
// the directory where the back-office-deployed JSON may be found.
func NewDocHelp(pluginDataDir string) *DocHelp {
	return &DocHelp{dataDir: pluginDataDir}
}

// buildEntries reads and parses the JSON, returning a normalised entries map.
func (dh *DocHelp) buildEntries() *map[string]docHelpEntry {
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

	entries := make(map[string]docHelpEntry)
	var data docHelpFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return &entries
	}
	// Re-index by normalised key so lookups are case/hyphen/prefix-insensitive.
	for key, entry := range data.Variables {
		entries[NormaliseVariableName(key)] = entry
	}
	return &entries
}

func (dh *DocHelp) getEntries() map[string]docHelpEntry {
	p := atomic.LoadPointer(&dh.entries)
	if p == nil {
		return nil
	}
	return *(*map[string]docHelpEntry)(p)
}

// ensureLoaded performs a one-time load of the entries map.
func (dh *DocHelp) ensureLoaded() {
	dh.loadOnce.Do(func() {
		entries := dh.buildEntries()
		atomic.StorePointer(&dh.entries, unsafe.Pointer(entries))
	})
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
// Thread-safe: uses atomic load to read the entries map.
func (dh *DocHelp) LookupVariables(names []string) (matched []DocHelpVariable, unknown []string) {
	dh.ensureLoaded()
	entries := dh.getEntries()
	if entries == nil {
		unknown = names
		return
	}

	for _, name := range names {
		normalised := NormaliseVariableName(name)
		if entry, ok := entries[normalised]; ok {
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
// Thread-safe: builds a new map and swaps it atomically.
func (dh *DocHelp) Reload() {
	entries := dh.buildEntries()
	atomic.StorePointer(&dh.entries, unsafe.Pointer(entries))
}
