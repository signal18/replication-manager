// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_enterprise_workload is a built-in (in-process) plugin that surfaces
// CRITICAL and HIGH severity bugs that impact workload stability — server
// crashes, InnoDB deadlocks, optimizer regressions, memory leaks, and DoS
// vulnerabilities — that are not already covered by the enterprise-security
// or enterprise-replication plugins.
//
// File resolution:
//  1. {PluginDataDir}/enterprise-workload-issues.json  (back-office deployed)
//  2. Embedded default compiled into the binary via go:embed
//
// Error key ranges: WRK0001–WRK9999 (hand-curated MDEV entries),
//                   CVE-*-workload-* (NVD auto-imported)
package logplugin

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed plugins/plugin-enterprise-workload/enterprise-workload-issues.json
var enterpriseWorkloadDefaultData []byte

func init() { Register(&EnterpriseWorkloadPlugin{}) }

// EnterpriseWorkloadPlugin implements LogPlugin.
type EnterpriseWorkloadPlugin struct{}

func (p *EnterpriseWorkloadPlugin) Name() string { return "enterprise-workload" }

type wrkIssueFile struct {
	Issues []wrkIssue `json:"issues"`
}

type wrkIssue struct {
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

func (p *EnterpriseWorkloadPlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() {
		return EvaluateResult{}
	}

	raw := enterpriseWorkloadDefaultData
	if src.PluginDataDir != "" {
		dataFile := filepath.Join(src.PluginDataDir, "enterprise-workload-issues.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
		}
	}

	var data wrkIssueFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return EvaluateResult{Findings: []Finding{{
			ErrKey:      "WRKWARN001",
			Severity:    SeverityWarning,
			Description: fmt.Sprintf("enterprise-workload plugin: cannot parse enterprise-workload-issues.json: %v", err),
		}}}
	}

	sv := src.ServerVersion
	serverVerStr := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	var findings []Finding

	for _, iss := range data.Issues {
		if iss.Flavor != "" && !strings.EqualFold(iss.Flavor, "repman") {
			if !strings.EqualFold(iss.Flavor, sv.Flavor) {
				continue
			}
		}

		if !strings.EqualFold(iss.Flavor, "repman") {
			affected := entParseVersion(iss.AffectedFrom)
			fixed := entParseVersion(iss.FixedIn)
			cur := [3]int{sv.Major, sv.Minor, sv.Release}

			if iss.AffectedFrom != "" && entVersionLess(cur, affected) {
				continue
			}
			if iss.FixedIn != "" && !entVersionLess(cur, fixed) {
				continue
			}
		}

		desc := entExpandPlaceholders(iss.Description, src.ServerURL, sv.Flavor, serverVerStr)

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

		findings = append(findings, Finding{
			ErrKey:       iss.ID,
			Severity:     Severity(iss.Severity),
			Description:  desc,
			Remediations: iss.Remediations,
		})
	}

	return EvaluateResult{Findings: findings}
}
