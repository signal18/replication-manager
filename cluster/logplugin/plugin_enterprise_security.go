// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_enterprise_security is a built-in (in-process) plugin that surfaces
// security advisories maintained by the Signal18 back office.
//
// The advisory database is a JSON file (enterprise-security-issues.json) pushed
// by the back office into each registered instance's GitLab pull repository and
// synced to disk via the standard repman git-pull mechanism.  The back office
// only pushes the file to instances on a paid plan (support, support-services,
// or partner); free-plan instances never receive the file and the plugin emits
// no findings (the embedded default contains no CVEs to avoid shipping
// advisories to unregistered users).
//
// File resolution (first found wins):
//  1. {PluginDataDir}/enterprise-security-issues.json   (back-office deployed)
//  2. Embedded no-op default                             (no findings, not registered)
//
// JSON schema — see cluster/logplugin/plugins/plugin-enterprise-security/enterprise-security-issues.json
//
// Each issue entry has:
//
//	id            string  Finding.ErrKey (e.g. "ENT0001")
//	severity      string  "SECURITY" | "WARNING" | "ERROR"
//	title         string  Short title (used in description)
//	description   string  Supports {server_url} {flavor} {version} placeholders
//	flavor        string  "MariaDB" | "MySQL" | "Percona" | "repman" | "" (all)
//	affected_from string  "major.minor.release" or "" (all from start)
//	fixed_in      string  "major.minor.release" or "" (not yet fixed)
//	remediations  []...
//
// The finding auto-resolves once the server upgrades past fixed_in — no manual
// suppression needed.
package logplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/share"
)

// Default advisory data loaded from the shared embed FS at init time.
// Falls back to empty if the file is not found (e.g. client builds).
var enterpriseSecurityDefaultData []byte

func init() {
	if data, err := share.EmbededDbModuleFS.ReadFile("plugins/data/enterprise-security-issues.json"); err == nil {
		enterpriseSecurityDefaultData = data
	}
	Register(&EnterpriseSecurityPlugin{})
}

// EnterpriseSecurityPlugin implements LogPlugin.
type EnterpriseSecurityPlugin struct{}

func (p *EnterpriseSecurityPlugin) Name() string { return "enterprise-security" }

type entIssueFile struct {
	Issues []entIssue `json:"issues"`
}

type entIssue struct {
	ID           string        `json:"id"`
	CVE          string        `json:"cve"`
	MariaDBJira  string        `json:"mariadb_jira"`
	GithubIssue  string        `json:"github_issue"`
	Severity     string        `json:"severity"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	Flavor       string        `json:"flavor"`
	AffectedFrom string        `json:"affected_from"`
	FixedIn      string        `json:"fixed_in"`
	Remediations []Remediation `json:"remediations,omitempty"`
}

func (p *EnterpriseSecurityPlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() {
		return EvaluateResult{}
	}

	// Load advisory database: on-disk deployment takes priority over the
	// embedded no-op default so the back office can push updates without
	// rebuilding the binary.
	raw := enterpriseSecurityDefaultData
	if src.PluginDataDir != "" {
		dataFile := filepath.Join(src.PluginDataDir, "enterprise-security-issues.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
		}
	}

	var data entIssueFile
	if err := json.Unmarshal(raw, &data); err != nil {
		// Corrupted data file — surface a meta-warning rather than silently failing.
		return EvaluateResult{Findings: []Finding{{
			ErrKey:      "ENTWARN001",
			Severity:    SeverityWarning,
			Description: fmt.Sprintf("enterprise-security plugin: cannot parse enterprise-security-issues.json: %v", err),
		}}}
	}

	sv := src.ServerVersion
	serverVerStr := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	var findings []Finding

	// Free plan or unregistered: CVE advisories are not refreshed by the back office.
	// Emit a persistent error so operators know their advisory database is stale.
	plan := ConfigStr(src.Config, "cloud18-subscription-plan", "")
	if plan == "" || plan == "free" {
		findings = append(findings, Finding{
			ErrKey:   "ENTERR001",
			Severity: SeveritySecurity,
			Description: fmt.Sprintf(
				"Server %s: enterprise security advisories are not refreshed on the free plan. "+
					"CVE coverage is frozen at the version shipped with this build. "+
					"Upgrade to a support or partner plan to receive daily advisory updates from the Signal18 back office.",
				src.ServerURL),
		})
	}

	for _, iss := range data.Issues {
		if !entMatchIssue(iss.Flavor, iss.AffectedFrom, iss.FixedIn, sv, src.ClusterContext.ToolVersions) {
			continue
		}

		desc := entExpandPlaceholders(iss.Description, src.ServerURL, sv.Flavor, serverVerStr)

		// Append reference tags.
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
			desc += " [" + strings.Join(refs, "; ") + "]"
		}

		// Always route to the security log — all enterprise security advisories
		// belong in SecurityStateMachine + LogSecurity regardless of the JSON
		// severity field (which may be WARNING for unresolved GitHub issues).
		findings = append(findings, Finding{
			ErrKey:       iss.ID,
			Severity:     SeveritySecurity,
			Description:  desc,
			Remediations: iss.Remediations,
		})
	}

	return EvaluateResult{Findings: findings}
}

// entExpandPlaceholders replaces {server_url}, {flavor}, {version} in s.
func entExpandPlaceholders(s, serverURL, flavor, version string) string {
	s = strings.ReplaceAll(s, "{server_url}", serverURL)
	s = strings.ReplaceAll(s, "{flavor}", flavor)
	s = strings.ReplaceAll(s, "{version}", version)
	return s
}

// entMatchIssue checks whether an advisory issue matches the current environment.
// For database flavors (MariaDB, MySQL, Percona) it compares against the server
// version. For tool flavors (repman, restic, proxysql, …) it looks up the
// version in toolVersions. Returns true if the issue should be emitted.
func entMatchIssue(flavor, affectedFrom, fixedIn string, sv StdioServerVersion, toolVersions map[string]string) bool {
	flavorLower := strings.ToLower(strings.TrimSpace(flavor))

	// Empty flavor = matches any server.
	if flavorLower == "" {
		return true
	}

	// Database flavors: compare against server version.
	dbFlavors := map[string]bool{"mariadb": true, "mysql": true, "percona": true, "postgresql": true}
	if dbFlavors[flavorLower] {
		if !strings.EqualFold(flavor, sv.Flavor) {
			return false
		}
		cur := [3]int{sv.Major, sv.Minor, sv.Release}
		return entVersionInRange(cur, affectedFrom, fixedIn)
	}

	// Tool flavors: look up version in ToolVersions map.
	// If the tool is not in the map (version unknown), skip the issue —
	// we cannot confirm the tool is affected without knowing its version.
	toolVer, ok := toolVersions[flavorLower]
	if !ok || toolVer == "" {
		return false
	}
	// Strip leading "v" prefix (e.g. "v3.1.23-448-gd8ad239df" → "3.1.23-…")
	toolVer = strings.TrimPrefix(toolVer, "v")
	cur := entParseVersion(toolVer)
	return entVersionInRange(cur, affectedFrom, fixedIn)
}

// entVersionInRange returns true if cur is within [affectedFrom, fixedIn).
func entVersionInRange(cur [3]int, affectedFrom, fixedIn string) bool {
	if affectedFrom != "" && entVersionLess(cur, entParseVersion(affectedFrom)) {
		return false
	}
	if fixedIn != "" && !entVersionLess(cur, entParseVersion(fixedIn)) {
		return false
	}
	return true
}

// entParseVersion converts "major.minor.release" to [3]int; missing parts → 0.
func entParseVersion(v string) [3]int {
	if v == "" {
		return [3]int{}
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		// Strip any pre-release suffix like "-beta".
		p = strings.TrimRight(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-+")
		fmt.Sscanf(p, "%d", &out[i])
	}
	return out
}

// entVersionLess returns true if a < b.
func entVersionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false // equal
}
