// plugin-enterprise-security surfaces security advisories maintained by the
// Signal18 back office.  It reads a JSON file (enterprise-security-issues.json)
// that lists known CVEs, MDEV tickets, and internal GitHub issues together with
// the database version range in which each issue is present.
//
// The plugin emits a Finding for every issue whose version range matches the
// server being analysed.  Once the server is upgraded past the fixed_in version
// the finding disappears automatically — no manual suppression needed.
//
// JSON file location (in priority order):
//  1. req.PluginDataDir/enterprise-security-issues.json  (back-office deployed)
//  2. Built-in copy compiled into the binary via go:embed   (shipping default)
//
// JSON schema:
//
//	{
//	  "version": "1",
//	  "generated_at": "2026-04-25T00:00:00Z",
//	  "source": "signal18-backoffice",
//	  "issues": [
//	    {
//	      "id":            "ENT0001",          // unique id → becomes Finding.ErrKey
//	      "cve":           "CVE-2022-27458",   // optional, for display
//	      "mariadb_jira":  "MDEV-26281",       // optional
//	      "github_issue":  "signal18/replication-manager#1234", // optional
//	      "severity":      "SECURITY",         // SECURITY | WARNING | ERROR
//	      "title":         "...",
//	      "description":   "Server {server_url} running {flavor} {version} ...",
//	      "flavor":        "MariaDB",          // MariaDB | MySQL | Percona | repman | "" (all)
//	      "affected_from": "10.7.0",           // "" = all versions from the start
//	      "fixed_in":      "10.7.3",           // "" = not yet fixed (always emit)
//	      "remediations": [...]
//	    }
//	  ]
//	}
//
// Description placeholders:
//
//	{server_url} — req.ServerURL
//	{flavor}     — req.ServerVersion.Flavor
//	{version}    — "major.minor.release"
//
// Error key range: ENT0001 – ENT9999  (enterprise back-office advisories)
//                  GHENT1  – GHENT999 (auto-imported GitHub security issues)
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

//go:embed enterprise-security-issues.json
var defaultIssuesData []byte

// issueFile is the top-level JSON structure.
type issueFile struct {
	Version     string  `json:"version"`
	GeneratedAt string  `json:"generated_at"`
	Source      string  `json:"source"`
	Issues      []issue `json:"issues"`
}

// issue is one advisory entry.
type issue struct {
	ID           string            `json:"id"`
	CVE          string            `json:"cve"`
	MariaDBJira  string            `json:"mariadb_jira"`
	GithubIssue  string            `json:"github_issue"`
	Severity     string            `json:"severity"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Flavor       string            `json:"flavor"`  // "" = any flavor
	AffectedFrom string            `json:"affected_from"` // "" = from the beginning
	FixedIn      string            `json:"fixed_in"`      // "" = not yet fixed
	Remediations []wire.Remediation `json:"remediations,omitempty"`
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// Load issues: prefer on-disk file so the back office can push updates
	// without rebuilding the plugin binary.
	raw := defaultIssuesData
	source := "built-in"
	if req.PluginDataDir != "" {
		dataFile := filepath.Join(req.PluginDataDir, "enterprise-security-issues.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
			source = dataFile
		}
	}

	var data issueFile
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Fprintf(os.Stderr, "bad enterprise-security-issues.json (%s): %v\n", source, err)
		os.Exit(1)
	}

	sv := req.ServerVersion
	serverVerStr := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	var findings []wire.Finding

	for _, iss := range data.Issues {
		if !matchIssue(iss.Flavor, iss.AffectedFrom, iss.FixedIn, sv, req.ClusterContext.ToolVersions) {
			continue
		}

		description := expandPlaceholders(iss.Description, req.ServerURL, sv.Flavor, serverVerStr)

		// Annotate with CVE / Jira / GitHub references when present.
		var refs []string
		if iss.CVE != "" {
			refs = append(refs, iss.CVE)
		}
		if iss.MariaDBJira != "" {
			refs = append(refs, "MariaDB "+iss.MariaDBJira)
		}
		if iss.GithubIssue != "" {
			refs = append(refs, "GitHub "+iss.GithubIssue)
		}
		if iss.FixedIn != "" {
			refs = append(refs, "fixed in "+iss.Flavor+" "+iss.FixedIn)
		}
		if len(refs) > 0 {
			description += " [" + strings.Join(refs, "; ") + "]"
		}

		findings = append(findings, wire.Finding{
			ErrKey:       iss.ID,
			Severity:     iss.Severity,
			Description:  description,
			Remediations: iss.Remediations,
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

// matchIssue checks whether an advisory matches the current environment.
func matchIssue(flavor, affectedFrom, fixedIn string, sv wire.ServerVersion, toolVersions map[string]string) bool {
	fl := strings.ToLower(strings.TrimSpace(flavor))
	if fl == "" {
		return true
	}
	dbFlavors := map[string]bool{"mariadb": true, "mysql": true, "percona": true, "postgresql": true}
	if dbFlavors[fl] {
		if !strings.EqualFold(flavor, sv.Flavor) {
			return false
		}
		return versionInRange([3]int{sv.Major, sv.Minor, sv.Release}, affectedFrom, fixedIn)
	}
	toolVer, ok := toolVersions[fl]
	if !ok || toolVer == "" {
		return false
	}
	return versionInRange(parseVersion(strings.TrimPrefix(toolVer, "v")), affectedFrom, fixedIn)
}

func versionInRange(cur [3]int, affectedFrom, fixedIn string) bool {
	if affectedFrom != "" && versionLess(cur, parseVersion(affectedFrom)) {
		return false
	}
	if fixedIn != "" && !versionLess(cur, parseVersion(fixedIn)) {
		return false
	}
	return true
}

// expandPlaceholders replaces {server_url}, {flavor}, {version} in s.
func expandPlaceholders(s, serverURL, flavor, version string) string {
	s = strings.ReplaceAll(s, "{server_url}", serverURL)
	s = strings.ReplaceAll(s, "{flavor}", flavor)
	s = strings.ReplaceAll(s, "{version}", version)
	return s
}

// parseVersion converts "major.minor.release" to [3]int.
// Missing components default to 0.
func parseVersion(v string) [3]int {
	if v == "" {
		return [3]int{}
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		fmt.Sscanf(strings.TrimRight(p, "-+abcdefghijklmnopqrstuvwxyz"), "%d", &out[i])
	}
	return out
}

// versionLess returns true if a < b (lexicographic on [major, minor, release]).
func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false // equal
}
