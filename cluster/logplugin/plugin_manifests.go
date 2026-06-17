package logplugin

func pf(v float64) *float64 { return &v }

var errorlogManifest = PluginManifest{
	Description: "Error Log Scanner\n\nScans the MariaDB/MySQL error log for entries at or above min-log-level within a rolling window. Produces spike detection using Graphite-backed sigma analysis.",
	ConfigKeys: []ManifestConfigKey{
		{Key: "timeframe-hours", Label: "Timeframe (hours)", Type: "int", Default: "24", Min: pf(1), Max: pf(1440), Step: pf(1), Help: "Only error log entries within this rolling window are inspected."},
		{Key: "min-log-level", Label: "Min log level", Type: "enum", Default: "Warning", Options: []ManifestEnumOption{
			{Value: "System", Label: "System — startup/shutdown only"},
			{Value: "Note", Label: "Note — informational+"},
			{Value: "Warning", Label: "Warning — warnings + errors (default)"},
			{Value: "ERROR", Label: "ERROR — errors only"},
		}, Help: "Only error log entries at or above this severity are fed to the plugin."},
		{Key: "spike-sigma", Label: "Spike threshold (σ)", Type: "float", Default: "2", Min: pf(0.5), Max: pf(10), Step: pf(0.5), Help: "Standard deviations above the rolling mean before the metric is classified as a spike."},
	},
}

var sqlerrorlogManifest = PluginManifest{
	Description: "SQL Error Log Scanner\n\nScans the SQL error log for spike patterns within a rolling window using Graphite-backed sigma analysis.",
	ConfigKeys: []ManifestConfigKey{
		{Key: "timeframe-hours", Label: "Timeframe (hours)", Type: "int", Default: "24", Min: pf(1), Max: pf(1440), Step: pf(1), Help: "Only SQL error log entries within this rolling window are inspected."},
		{Key: "spike-sigma", Label: "Spike threshold (σ)", Type: "float", Default: "2", Min: pf(0.5), Max: pf(10), Step: pf(0.5), Help: "Standard deviations above the rolling mean before the metric is classified as a spike."},
	},
}

var slowlogManifest = PluginManifest{
	Description: "Slow Query Log Scanner\n\nScans the slow query log for spike patterns within a rolling window using Graphite-backed sigma analysis.",
	ConfigKeys: []ManifestConfigKey{
		{Key: "timeframe-hours", Label: "Timeframe (hours)", Type: "int", Default: "24", Min: pf(1), Max: pf(1440), Step: pf(1), Help: "Only slow log entries within this rolling window are inspected."},
		{Key: "spike-sigma", Label: "Spike threshold (σ)", Type: "float", Default: "2", Min: pf(0.5), Max: pf(10), Step: pf(0.5), Help: "Standard deviations above the rolling mean before the metric is classified as a spike."},
	},
}

var auditlogManifest = PluginManifest{
	Description: "Audit Log Drift Detector\n\nCompares recent audit log activity against a historical baseline to detect unusual operation patterns or volume shifts.",
	ConfigKeys: []ManifestConfigKey{
		{Key: "current-window-hours", Label: "Current window (hours)", Type: "int", Default: "1", Min: pf(1), Max: pf(1440), Step: pf(1), Help: "The recent audit log window used to compute the current activity baseline for drift detection."},
		{Key: "baseline-window-hours", Label: "Baseline window (hours)", Type: "int", Default: "24", Min: pf(1), Max: pf(1440), Step: pf(1), Help: "The historical audit log window used to establish the expected activity level for drift comparison."},
		{Key: "spike-sigma", Label: "Spike threshold (σ)", Type: "float", Default: "2", Min: pf(0.5), Max: pf(10), Step: pf(0.5), Help: "Standard deviations above the rolling mean before the metric is classified as a spike."},
	},
}

var enterpriseSecurityManifest = PluginManifest{
	Description: "Enterprise Security Advisories — ENT0001+\n\nSurfaces CVEs and security advisories from the Signal18 back-office advisory database. Covers all known MariaDB/MySQL security vulnerabilities with version-range matching — findings auto-resolve when the server is upgraded past the fix version.\n\nNo configurable parameters — the advisory JSON is managed by the back office.",
}

var enterpriseReplicationManifest = PluginManifest{
	Description: "Enterprise Replication Advisories — RPL0001+\n\nSurfaces known replication bugs and CVEs that affect the MySQL/MariaDB replication subsystem. Findings auto-resolve when the server is upgraded past the fix version.\n\nNo configurable parameters — the advisory JSON is managed by the back office.",
}

var enterpriseWorkloadManifest = PluginManifest{
	Description: "Enterprise Workload Advisories — WRK0001+\n\nSurfaces CRITICAL and HIGH severity bugs impacting workload stability — server crashes, InnoDB deadlocks, optimizer regressions, memory leaks, and DoS vulnerabilities. Findings auto-resolve on upgrade.\n\nNo configurable parameters — the advisory JSON is managed by the back office.",
}

var enterpriseComplianceManifest = PluginManifest{
	Description: "Enterprise Compliance — compliance scoring and audit trail analysis.\n\nNo configurable parameters.",
}

// Manifest methods for built-in plugins.

func (p *ErrorLogPlugin) Manifest() *PluginManifest            { return &errorlogManifest }
func (p *SqlErrorLogPlugin) Manifest() *PluginManifest         { return &sqlerrorlogManifest }
func (p *SlowLogPlugin) Manifest() *PluginManifest             { return &slowlogManifest }
func (p *AuditLogDriftPlugin) Manifest() *PluginManifest       { return &auditlogManifest }
func (p *EnterpriseSecurityPlugin) Manifest() *PluginManifest  { return &enterpriseSecurityManifest }
func (p *EnterpriseReplicationPlugin) Manifest() *PluginManifest { return &enterpriseReplicationManifest }
func (p *EnterpriseWorkloadPlugin) Manifest() *PluginManifest  { return &enterpriseWorkloadManifest }
func (p *EnterpriseCompliancePlugin) Manifest() *PluginManifest { return &enterpriseComplianceManifest }
