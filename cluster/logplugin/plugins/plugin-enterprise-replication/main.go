// plugin-enterprise-replication surfaces replication-specific bugs and CVEs
// maintained by the Signal18 back office.  See the built-in static version in
// cluster/logplugin/plugin_enterprise_replication.go for full documentation.
//
// This standalone binary exists for testing and can be run with:
//   echo '{"server_url":"...","server_version":{...}}' | ./plugin-enterprise-replication
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

//go:embed enterprise-replication-issues.json
var defaultIssuesData []byte

type issueFile struct {
	Issues []issue `json:"issues"`
}

type issue struct {
	ID           string            `json:"id"`
	CVE          string            `json:"cve"`
	MariaDBJira  string            `json:"mariadb_jira"`
	GithubIssue  string            `json:"github_issue"`
	Severity     string            `json:"severity"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Flavor       string            `json:"flavor"`
	AffectedFrom string            `json:"affected_from"`
	FixedIn      string            `json:"fixed_in"`
	Remediations []wire.Remediation `json:"remediations,omitempty"`
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	raw := defaultIssuesData
	if req.PluginDataDir != "" {
		dataFile := filepath.Join(req.PluginDataDir, "enterprise-replication-issues.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
		}
	}

	var data issueFile
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Fprintf(os.Stderr, "bad enterprise-replication-issues.json: %v\n", err)
		os.Exit(1)
	}

	sv := req.ServerVersion
	serverVerStr := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	var findings []wire.Finding

	for _, iss := range data.Issues {
		if iss.Flavor != "" && !strings.EqualFold(iss.Flavor, "repman") {
			if !strings.EqualFold(iss.Flavor, sv.Flavor) {
				continue
			}
		}

		if !strings.EqualFold(iss.Flavor, "repman") {
			affected := parseVersion(iss.AffectedFrom)
			fixed := parseVersion(iss.FixedIn)
			cur := [3]int{sv.Major, sv.Minor, sv.Release}

			if iss.AffectedFrom != "" && versionLess(cur, affected) {
				continue
			}
			if iss.FixedIn != "" && !versionLess(cur, fixed) {
				continue
			}
		}

		desc := expandPlaceholders(iss.Description, req.ServerURL, sv.Flavor, serverVerStr)

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

		findings = append(findings, wire.Finding{
			ErrKey:       iss.ID,
			Severity:     iss.Severity,
			Description:  desc,
			Remediations: iss.Remediations,
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{Findings: findings})
}

func expandPlaceholders(s, serverURL, flavor, version string) string {
	s = strings.ReplaceAll(s, "{server_url}", serverURL)
	s = strings.ReplaceAll(s, "{flavor}", flavor)
	s = strings.ReplaceAll(s, "{version}", version)
	return s
}

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
		p = strings.TrimRight(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-+")
		fmt.Sscanf(p, "%d", &out[i])
	}
	return out
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
