// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// ACLRule represents an access control rule for a URL pattern
type ACLRule struct {
	URLPattern     string   // The URL pattern to match (uses strings.Contains)
	RequiredGrants []string // All grants in this list are required (AND logic)
	AllowedGrants  []string // Any grant in this list is sufficient (OR logic)
}

// databaseACLRules defines ACL rules for database endpoints
var databaseACLRules = []ACLRule{
	// Actions requiring single grants
	{"/actions/run-jobs", nil, []string{config.GrantClusterProcess}},
	{"/actions/provision", nil, []string{config.GrantProvDBProvision}},
	{"/service-opensvc", nil, []string{config.GrantProvDBProvision}},
	{"/actions/unprovision", nil, []string{config.GrantProvDBUnprovision}},
	{"/actions/start", nil, []string{config.GrantDBStart}},
	{"/actions/stop", nil, []string{config.GrantDBStop}},
	{"/actions/switchover", nil, []string{config.GrantClusterSwitchover}},
	{"/actions/set-prefered", nil, []string{config.GrantClusterFailover}},
	{"/actions/set-unrated", nil, []string{config.GrantClusterFailover}},
	{"/actions/set-ignored", nil, []string{config.GrantClusterFailover}},
	{"/actions/kill", nil, []string{config.GrantDBKill}},

	{"/actions/restart", []string{config.GrantDBStart, config.GrantDBStop}, nil},

	// Actions with multiple allowed grants (OR logic)
	{"/actions/analyze-pfs", nil, []string{config.GrantDBOptimize, config.GrantDBAnalyse}},

	// Analysis actions
	{"/actions/analyze-slowlog", nil, []string{config.GrantDBAnalyse}},
	{"/actions/reset-pfs-queries", nil, []string{config.GrantDBAnalyse}},

	// Replication actions
	{"/all-slaves-status", nil, []string{config.GrantDBReplication}},
	{"/master-status", nil, []string{config.GrantDBReplication}},
	{"actions/start-slave", nil, []string{config.GrantDBReplication}},
	{"actions/stop-slave", nil, []string{config.GrantDBReplication}},
	{"actions/skip-replication-event", nil, []string{config.GrantDBReplication}},
	{"actions/reset-master", nil, []string{config.GrantDBReplication}},
	{"actions/reset-slave-all", nil, []string{config.GrantDBReplication}},

	// Backup actions
	{"/actions/backup-logical", nil, []string{config.GrantDBBackup}},
	{"/actions/backup-error-log", nil, []string{config.GrantDBBackup}},
	{"/actions/backup-physical", nil, []string{config.GrantDBBackup}},
	{"/actions/backup-slowquery-log", nil, []string{config.GrantDBBackup}},
	{"/actions/flush-logs", nil, []string{config.GrantDBBackup}},

	// Restore actions
	{"/actions/reseed/", nil, []string{config.GrantDBRestore}},
	{"/actions/reseed-restic", nil, []string{config.GrantDBRestore}},
	{"/actions/pitr", nil, []string{config.GrantDBRestore}},
	{"/actions/reseed-cancel", nil, []string{config.GrantDBRestore, config.GrantClusterProcess}},

	// Process actions
	{"/actions/job-cancel/", nil, []string{config.GrantClusterProcess}},

	// Read-only toggle
	{"actions/toggle-read-only", nil, []string{config.GrantDBReadOnly}},

	// Config actions
	{"/config", nil, []string{config.GrantDBConfigFlag}},
	{"/config-gen", nil, []string{config.GrantDBConfigFlag}},

	// Capture actions
	{"/actions/toggle-slow-query-capture", nil, []string{config.GrantDBCapture, config.GrantDBLogs}},

	// Maintenance actions
	{"/actions/optimize", nil, []string{config.GrantDBMaintenance}},
	{"/actions/maintenance", nil, []string{config.GrantDBMaintenance}},
	{"/actions/set-maintenance", nil, []string{config.GrantDBMaintenance}},
	{"/actions/del-maintenance", nil, []string{config.GrantDBMaintenance}},
	{"/actions/wait-innodb-purge", nil, []string{config.GrantDBMaintenance}},
	{"/actions/jobs-upgrade", nil, []string{config.GrantDBMaintenance}},

	// Show actions
	{"/variables", nil, []string{config.GrantDBShowVariables}},
	{"/tables", nil, []string{config.GrantDBShowSchema}},
	{"/vtables", nil, []string{config.GrantDBShowSchema}},
	{"/schemas", nil, []string{config.GrantDBShowSchema}},
	{"/status", nil, []string{config.GrantDBShowStatus}},
	{"/status-delta", nil, []string{config.GrantDBShowStatus}},
}

// proxyACLRules defines ACL rules for proxy endpoints
var proxyACLRules = []ACLRule{
	{"/actions/provision", nil, []string{config.GrantProvProxyProvision}},
	{"/actions/unprovision", nil, []string{config.GrantProvProxyUnprovision}},
	{"/actions/start", nil, []string{config.GrantProxyStart}},
	{"/actions/stop", nil, []string{config.GrantProxyStop}},
	{"/actions/staging", nil, []string{config.GrantClusterStaging}},
}

// appACLRules defines ACL rules for application endpoints
var appACLRules = []ACLRule{
	{"/actions/provision", nil, []string{config.GrantProvAppProvision}},
	{"/service-opensvc", nil, []string{config.GrantProvAppProvision}},
	{"/actions/unprovision", nil, []string{config.GrantProvAppUnprovision}},
	{"/actions/drop", nil, []string{config.GrantProvAppUnprovision}},
	{"/deployment/", nil, []string{config.GrantAppDeployment}},
	{"/storages/", nil, []string{config.GrantAppDeployment}},
	{"/substitution", nil, []string{config.GrantAppDeployment}},
	{"/resolve-template", nil, []string{config.GrantAppDeployment}},
	{"/settings/actions/reset-from-template", nil, []string{config.GrantAppDeployment}},
	{"/settings/actions/save-to-template", nil, []string{config.GrantAppDeployment}},
	{"/actions/start", nil, []string{config.GrantAppStart}},
	{"/actions/stop", nil, []string{config.GrantAppStop}},
	{"/actions/restart", []string{config.GrantAppStop, config.GrantAppStart}, nil},
	{"/settings/actions/", nil, []string{config.GrantAppConfig}},
	{"/git/", nil, []string{config.GrantAppGit}},
}

// clusterACLRules defines ACL rules for general cluster-level endpoints
var clusterACLRules = []ACLRule{
	// Sharding
	{"/actions/monitor-schemas", nil, []string{config.GrantClusterSharding}},
	{"/schema", nil, []string{config.GrantClusterSharding}},
	{"/shardclusters", nil, []string{config.GrantClusterSharding}},

	// Process and Jobs
	{"/jobs", nil, []string{config.GrantClusterProcess}},
	{"/top", nil, []string{config.GrantClusterProcess}},
	{"/actions/restic-mount-toggle", nil, []string{config.GrantClusterSettings}},
	{"/restic", nil, []string{config.GrantClusterProcess}},

	// Backups
	{"/backups", nil, []string{config.GrantClusterShowBackups}},
	{"/restic/snapshots", nil, []string{config.GrantClusterShowBackups}},
	{"/restic/stats", nil, []string{config.GrantClusterShowBackups}},

	// Routes and Certificates
	{"/queryrules", nil, []string{config.GrantClusterShowRoutes}},
	{"/certificates", nil, []string{config.GrantClusterShowCertificates}},
	{"/actions/certificates-reload", nil, []string{config.GrantClusterCertificatesReload}},
	{"/actions/certificates-rotate", nil, []string{config.GrantClusterCertificatesRotate}},

	// SLA and Monitoring
	{"/actions/reset-sla", nil, []string{config.GrantClusterResetSLA}},
	{"/actions/addserver", nil, []string{config.GrantClusterCreateMonitor, config.GrantAppDeployment}},
	{"/actions/dropserver", nil, []string{config.GrantClusterDropMonitor}},

	// Cluster Actions
	{"/actions/switchover", nil, []string{config.GrantClusterSwitchover}},
	{"/actions/failover", nil, []string{config.GrantClusterFailover}},
	{"/actions/send-email", nil, []string{config.GrantClusterAlert}},
	{"/actions/send-alert", nil, []string{config.GrantClusterAlert}},
	{"/actions/stop-traffic", nil, []string{config.GrantClusterTraffic}},
	{"/actions/start-traffic", nil, []string{config.GrantClusterTraffic}},

	// Backup (master)
	{"/actions/master-logical-backup", nil, []string{config.GrantDBBackup}},
	{"/actions/master-physical-backup", nil, []string{config.GrantDBBackup}},

	// Testing and Benchmarking
	{"/actions/sysbench", nil, []string{config.GrantClusterBench, config.GrantClusterTest}},
	{"/tests/", nil, []string{config.GrantClusterTest}},

	// Replication
	{"/actions/replication/bootstrap", nil, []string{config.GrantClusterReplication}},
	{"/actions/replication/cleanup", nil, []string{config.GrantClusterReplication}},

	// Rolling Operations
	{"/actions/optimize", nil, []string{config.GrantClusterRolling}},
	{"/actions/rolling", nil, []string{config.GrantClusterRolling}},
	{"/actions/cancel-rolling-restart", nil, []string{config.GrantClusterRolling}},
	{"/actions/cancel-rolling-reprov", nil, []string{config.GrantClusterRolling}},
	{"/actions/jobs-upgrade", nil, []string{config.GrantClusterRolling}},

	// Security
	{"/actions/rotate-passwords", nil, []string{config.GrantClusterRotatePasswords}},

	// Configuration
	{"/settings/actions/drop-db-tag", nil, []string{config.GrantDBConfigFlag}},
	{"/settings/actions/add-db-tag", nil, []string{config.GrantDBConfigFlag}},
	{"/settings/actions/apply-dynamic-config", nil, []string{config.GrantDBConfigFlag}},
	{"/settings/actions/drop-proxy-tag", nil, []string{config.GrantProxyConfigFlag}},
	{"/settings/actions/add-proxy-tag", nil, []string{config.GrantProxyConfigFlag}},
	{"/settings/actions/generate-configs", nil, []string{config.GrantProxyConfigFlag}},
	{"/settings/actions/preserve-variable", nil, []string{config.GrantProxyConfigFlag}},

	// Cluster Settings
	{"/settings/actions/reload", nil, []string{config.GrantClusterSettings}},
	{"/settings/actions/reload-plan-info", nil, []string{config.GrantClusterSettings}},
	{"/settings/actions/switch", nil, []string{config.GrantClusterSettings, config.GrantGlobalSettings}},
	{"/settings/actions/set", nil, []string{config.GrantClusterSettings, config.GrantGlobalSettings}},
	{"/settings/actions/clear", nil, []string{config.GrantClusterSettings, config.GrantGlobalSettings}},
	{"/settings/actions/discover", nil, []string{config.GrantClusterSettings}},
	{"/actions/reset-failover-control", nil, []string{config.GrantClusterSettings}},

	// Config Management - MySQL Defaults and Preserved Variables (from origin/develop)
	{"/settings/mysql-defaults-cnf", nil, []string{config.GrantClusterSettings}},
	{"/settings/actions/save-mysql-defaults-cnf", nil, []string{config.GrantClusterSettings}},
	{"/settings/preserved-variables-cnf", nil, []string{config.GrantClusterSettings}},
	{"/settings/actions/save-preserved-variables-cnf", nil, []string{config.GrantClusterSettings}},

	// Maintenance
	{"/actions/checksum-all-tables", nil, []string{config.GrantClusterChecksum}},
	{"/actions/checksum-repair-all-tables", nil, []string{config.GrantClusterChecksumRepair}},
	{"/actions/analyze-all-tables", nil, []string{config.GrantClusterAnalyze}},

	// Provisioning
	{"/services/actions/provision", nil, []string{config.GrantProvCluster}},
	{"/services/actions/unprovision", nil, []string{config.GrantProvClusterUnprovision}},
	{"/api/clusters/actions/add", nil, []string{config.GrantProvCluster, config.GrantClusterCreate}},
	{"/opensvc-gateway", nil, []string{config.GrantProvCluster}},

	// Staging
	{"/actions/staging-refresh", nil, []string{config.GrantClusterStaging}},
	{"/actions/staging-reload-script", nil, []string{config.GrantClusterStaging}},
	{"/actions/staging-reseed-from-parent", nil, []string{config.GrantClusterStaging}},

	// Cluster Management
	{"/api/clusters/actions/delete", nil, []string{config.GrantClusterDelete}},
	{"/api/clusters/actions/rename", nil, []string{config.GrantClusterDelete}},

	// Graphite/Graphs
	{"/settings/actions/set-graphite-filterlist", nil, []string{config.GrantClusterConfigGraphs}},
	{"/settings/actions/reload-graphite-filterlist", nil, []string{config.GrantClusterConfigGraphs}},
	{"/settings/actions/reset-graphite-filterlist", nil, []string{config.GrantClusterConfigGraphs}},

	// User Management
	{"/users/send-credentials", nil, []string{config.GrantGrantShow}},
	{"/api/monitor/actions/adduser/", nil, []string{config.GrantGrantAdd}},
	{"/users/add", nil, []string{config.GrantGrantAdd}},
	{"/users/update", nil, []string{config.GrantGrantModify}},
	{"/users/drop", nil, []string{config.GrantGrantDrop}},

	// External Roles
	{"/ext-role/subscribe", nil, []string{config.GrantExternalRole}},
	{"/ext-role/accept", nil, []string{config.GrantExternalRole}},
	{"/ext-role/quote", nil, []string{config.GrantSalesValidate}},
	{"/ext-role/refuse", nil, []string{config.GrantSalesRefuse}},
	{"/ext-role/end", nil, []string{config.GrantSalesUnsubscribe}},

	// Sales
	{"/sales/accept-subscription", nil, []string{config.GrantSalesValidate}},
	{"/sales/refuse-subscription", nil, []string{config.GrantSalesRefuse}},
	{"/sales/end-subscription", nil, []string{config.GrantSalesUnsubscribe}},
	{"/unsubscribe", nil, []string{config.GrantSalesRefuse}},

	// Docker
	{"/docker", nil, []string{config.GrantClusterDocker, config.GrantAppDocker}},
}

// globalSettingsACLRules defines ACL rules for global (cross-cluster) settings
// These are checked BEFORE cluster-level rules to prevent /settings/actions/switch
// from granting access to /api/clusters/settings/actions/switch
var globalSettingsACLRules = []ACLRule{
	{"/api/clusters/settings/actions/switch", nil, []string{config.GrantGlobalSettings}},
	{"/api/clusters/settings/actions/set", nil, []string{config.GrantGlobalSettings}},
	{"/api/clusters/settings/actions/clear", nil, []string{config.GrantGlobalSettings}},
	{"/api/clusters/settings/actions/reload-clusters-plans", nil, []string{config.GrantGlobalSettings}},
	{"/api/clusters/settings/actions/reload-clusters-plan-info", nil, []string{config.GrantGlobalSettings}},
}

// terminalACLRules defines ACL rules for terminal access
var terminalACLRules = []ACLRule{
	{"/api/terminal/connect", nil, []string{config.GrantTerminalGlobal}},
	{"/api/terminal/list", nil, []string{config.GrantTerminalGlobal}},
	{"/servers", nil, []string{config.GrantTerminalDatabase}},
	{"/proxies", nil, []string{config.GrantTerminalProxy}},
}

// checkACLRule checks if a user has the required grants for a specific rule
// Returns true if access granted, false otherwise
// Logs detailed information about missing grants when access is denied
func (cluster *Cluster) checkACLRule(strUser string, rule ACLRule, URL string) (bool, string) {
	user, ok := cluster.APIUsers[strUser]
	if !ok {
		return false, "user not found"
	}

	// Check required grants (AND logic) - all must be present
	if len(rule.RequiredGrants) > 0 {
		var missingGrants []string
		var hasGrants []string

		for _, grant := range rule.RequiredGrants {
			if !user.Grants[grant] {
				missingGrants = append(missingGrants, grant)
			} else {
				hasGrants = append(hasGrants, grant)
			}
		}

		if len(missingGrants) > 0 {
			reason := fmt.Sprintf("missing required grant(s): %v (has: %v, needs all: %v)",
				missingGrants, hasGrants, rule.RequiredGrants)
			return false, reason
		}
		return true, ""
	}

	// Check allowed grants (OR logic) - any one is sufficient
	if len(rule.AllowedGrants) > 0 {
		for _, grant := range rule.AllowedGrants {
			if user.Grants[grant] {
				return true, ""
			}
		}
		reason := fmt.Sprintf("missing any required grant from: %v", rule.AllowedGrants)
		return false, reason
	}

	return false, "no grants defined for rule"
}

// matchACLRules checks if a URL matches any of the provided ACL rules
// Logs detailed information about permission denials
// Matches are checked in order of specificity (longer patterns first) to ensure
// more specific rules take precedence, but all matching patterns are tried (hierarchical fallback)
func (cluster *Cluster) matchACLRules(strUser string, URL string, rules []ACLRule) bool {
	// First pass: collect all matching rules with their pattern lengths
	type matchedRule struct {
		rule          ACLRule
		patternLength int
	}
	var matches []matchedRule

	for _, rule := range rules {
		if strings.Contains(URL, rule.URLPattern) {
			matches = append(matches, matchedRule{
				rule:          rule,
				patternLength: len(rule.URLPattern),
			})
		}
	}

	// If no matches, deny access
	if len(matches) == 0 {
		return false
	}

	// Sort by pattern length (longer/more specific first)
	// This ensures /api/clusters/settings/actions/switch is checked before /settings/actions/switch
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].patternLength > matches[i].patternLength {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	// Check rules in order of specificity (hierarchical fallback)
	// If a more specific pattern denies access, try less specific patterns
	var matchedRules []string
	var deniedReasons []string

	for _, match := range matches {
		matchedRules = append(matchedRules, match.rule.URLPattern)

		granted, reason := cluster.checkACLRule(strUser, match.rule, URL)
		if granted {
			return true
		}

		if reason != "" {
			deniedReasons = append(deniedReasons, fmt.Sprintf("[%s: %s]", match.rule.URLPattern, reason))
		}
	}

	// Log detailed information about why access was denied
	if len(matchedRules) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"ACL denied for user %s on URL %s - matched patterns but insufficient grants: %v",
			strUser, URL, deniedReasons)
	}

	return false
}

// dbLogPaths defines log-related paths for database endpoints
var dbLogPaths = []string{
	"/processlist",
	"/status-innodb",
	"/auditlog",
	"/errorlog",
	"/sqlerrorlog",
	"/slow-queries",
	"/query-response-time",
	"/meta-data-locks",
	"/digest-statements-pfs",
	"/digest-statements-slow",
	"/actions/toggle-sql-error-log",
	"/actions/toggle-server-audit-log",
	"/actions/toggle-query-response-time",
	"/actions/toggle-meta-data-locks",
	"/actions/toggle-slow-query-table",
	"/actions/toggle-slow-query-capture",
	"/actions/toggle-slow-query",
	"/actions/set-long-query-time",
	"/actions/toggle-pfs-slow-query",
	"/actions/toggle-innodb-monitor",
	"/actions/explain-pfs",
	"/actions/explain-slowlog",
}

// checkDBLogAccess checks if user has access to database log paths
func (cluster *Cluster) checkDBLogAccess(strUser string, URL string) bool {
	if !cluster.APIUsers[strUser].Grants[config.GrantDBLogs] {
		return false
	}

	for _, path := range dbLogPaths {
		if strings.Contains(URL, path) {
			return true
		}
	}
	return false
}
