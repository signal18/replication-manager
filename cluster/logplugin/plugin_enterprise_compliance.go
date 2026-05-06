// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// plugin_enterprise_compliance is a built-in plugin that reports when new
// compliance modulesets are available from the back office via git pull.
//
// It does NOT auto-reload the configurator. Instead it raises a finding
// (ENTCOMP001) that tells the user to accept the update via the API.
// The cluster's state machine also gets WARN0168 set so the update appears
// in the cluster warnings. The user must explicitly accept (which calls
// Configurator.AcceptComplianceUpdate) to activate the new compliance.
//
// On free plan instances where no compliance files are pushed, it emits
// ENTCOMPERR001 warning that the compliance is frozen at build version.
package logplugin

import (
	"fmt"
)

func init() { Register(&EnterpriseCompliancePlugin{}) }

// EnterpriseCompliancePlugin implements LogPlugin.
type EnterpriseCompliancePlugin struct{}

func (p *EnterpriseCompliancePlugin) Name() string { return "enterprise-compliance" }

func (p *EnterpriseCompliancePlugin) Evaluate(src LogSource) EvaluateResult {
	if !src.IsEnabled() || src.PluginDataDir == "" {
		return EvaluateResult{}
	}

	var findings []Finding

	// The actual check is done by the cluster monitoring loop which calls
	// Configurator.CheckComplianceUpdate(). Here we just report the pending
	// state if any.

	// Free plan warning — no compliance files pushed
	plan := ConfigStr(src.Config, "cloud18-subscription-plan", "")
	if plan == "" || plan == "free" {
		findings = append(findings, Finding{
			ErrKey:   "ENTCOMPERR001",
			Severity: SeveritySecurity,
			Description: fmt.Sprintf(
				"Server %s: compliance modulesets are not refreshed on the free plan. "+
					"The configurator uses the version shipped with this build. "+
					"Upgrade to a support or partner plan to receive compliance updates.",
				src.ServerURL),
		})
	}

	return EvaluateResult{Findings: findings}
}
