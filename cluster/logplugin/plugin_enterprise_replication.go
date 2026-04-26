// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_enterprise_replication is a built-in (in-process) plugin that surfaces
// known replication bugs and CVEs maintained by the Signal18 back office.
//
// It tracks MariaDB MDEV replication blockers (MDEV-20821, MDEV-28310,
// MDEV-19577) and NVD CVEs affecting the MySQL/MariaDB replication subsystem,
// with affected_from/fixed_in version ranges so findings auto-resolve on
// upgrade.
//
// File resolution:
//  1. {PluginDataDir}/enterprise-replication-issues.json  (back-office deployed)
//  2. Embedded default compiled into the binary via go:embed
//
// Error key ranges: RPL0001–RPL9999 (hand-curated MDEV entries),
//                   CVE-*-replication-* (NVD auto-imported)
package logplugin

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed plugins/plugin-enterprise-replication/enterprise-replication-issues.json
var enterpriseReplicationDefaultData []byte

func init() { Register(&EnterpriseReplicationPlugin{}) }

// EnterpriseReplicationPlugin implements LogPlugin.
type EnterpriseReplicationPlugin struct{}

func (p *EnterpriseReplicationPlugin) Name() string { return "enterprise-replication" }

type rplIssueFile struct {
	Issues []rplIssue `json:"issues"`
}

type rplIssue struct {
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

func (p *EnterpriseReplicationPlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() {
		return EvaluateResult{}
	}

	raw := enterpriseReplicationDefaultData
	if src.PluginDataDir != "" {
		dataFile := filepath.Join(src.PluginDataDir, "enterprise-replication-issues.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
		}
	}

	var data rplIssueFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return EvaluateResult{Findings: []Finding{{
			ErrKey:      "RPLWARN001",
			Severity:    SeverityWarning,
			Description: fmt.Sprintf("enterprise-replication plugin: cannot parse enterprise-replication-issues.json: %v", err),
		}}}
	}

	sv := src.ServerVersion
	serverVerStr := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	var findings []Finding

	plan := ConfigStr(src.Config, "cloud18-subscription-plan", "")
	if plan == "" || plan == "free" {
		findings = append(findings, Finding{
			ErrKey:   "RPLERR001",
			Severity: SeveritySecurity,
			Description: fmt.Sprintf(
				"Server %s: enterprise replication advisories are not refreshed on the free plan. "+
					"Replication bug coverage is frozen at the version shipped with this build. "+
					"Upgrade to a support or partner plan to receive daily advisory updates.",
				src.ServerURL),
		})
	}

	for _, iss := range data.Issues {
		if !entMatchIssue(iss.Flavor, iss.AffectedFrom, iss.FixedIn, sv, src.ClusterContext.ToolVersions) {
			continue
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

		// Always route to the security log — replication bugs are safety-critical
		// and belong in SecurityStateMachine + LogSecurity.
		findings = append(findings, Finding{
			ErrKey:       iss.ID,
			Severity:     SeveritySecurity,
			Description:  desc,
			Remediations: iss.Remediations,
		})
	}

	return EvaluateResult{Findings: findings}
}
