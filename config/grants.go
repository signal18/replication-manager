// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"slices"
	"strings"
)

// Grant and Role types

type Grant struct {
	Grant  string `json:"grant"`
	Enable bool   `json:"enable"`
}

type Role struct {
	Role   string `json:"role"`
	Enable bool   `json:"enable"`
}

// Role constants

const (
	RoleSysOps                string = "sysops"
	RoleDBOps                 string = "dbops"
	RoleExtSysOps             string = "extsysops"
	RoleExtDBOps              string = "extdbops"
	RoleSponsor               string = "sponsor"
	RoleUnsubscribed          string = "unsubscribed"
	RoleUnsubscribedExtDBOps  string = "unsubscribed-extdbops"
	RoleUnsubscribedExtSysOps string = "unsubscribed-extsysops"
	RolePending               string = "pending"
	RolePendingExtDBOps       string = "pending-extdbops"
	RolePendingExtSysOps      string = "pending-extsysops"
	RoleQuoteExtDBOps         string = "quote-extdbops"
	RoleQuoteExtSysOps        string = "quote-extsysops"
	RoleVisitor               string = "visitor"
)

// Grant constants

const (
	GrantDBStart           string = "db-start"
	GrantDBStop            string = "db-stop"
	GrantDBKill            string = "db-kill"
	GrantDBOptimize        string = "db-optimize"
	GrantDBAnalyse         string = "db-analyse"
	GrantDBReplication     string = "db-replication"
	GrantDBBackup          string = "db-backup"
	GrantDBRestore         string = "db-restore"
	GrantDBReadOnly        string = "db-readonly"
	GrantDBLogs            string = "db-logs"
	GrantDBShowVariables   string = "db-show-variables"
	GrantDBShowStatus      string = "db-show-status"
	GrantDBShowSchema      string = "db-show-schema"
	GrantDBShowProcess     string = "db-show-process"
	GrantDBShowLogs        string = "db-show-logs"
	GrantDBCapture         string = "db-capture"
	GrantDBMaintenance     string = "db-maintenance"
	GrantDBConfigCreate    string = "db-config-create"
	GrantDBConfigRessource string = "db-config-ressource"
	GrantDBConfigFlag      string = "db-config-flag"
	GrantDBConfigGet       string = "db-config-get"

	GrantClusterCreate             string = "cluster-create"
	GrantClusterDelete             string = "cluster-delete"
	GrantClusterCreateMonitor      string = "cluster-create-monitor"
	GrantClusterDropMonitor        string = "cluster-drop-monitor"
	GrantClusterFailover           string = "cluster-failover"
	GrantClusterSwitchover         string = "cluster-switchover"
	GrantClusterRolling            string = "cluster-rolling"
	GrantClusterSettings           string = "cluster-settings"
	GrantClusterGrant              string = "cluster-grant"
	GrantClusterAnalyze            string = "cluster-analyze"
	GrantClusterChecksum           string = "cluster-checksum"
	GrantClusterChecksumRepair     string = "cluster-checksum-repair"
	GrantClusterSharding           string = "cluster-sharding"
	GrantClusterReplication        string = "cluster-replication"
	GrantClusterCertificatesRotate string = "cluster-certificates-rotate"
	GrantClusterCertificatesReload string = "cluster-certificates-reload"
	GrantClusterBench              string = "cluster-bench"
	GrantClusterProcess            string = "cluster-process" // Can ssh for jobs
	GrantClusterTest               string = "cluster-test"
	GrantClusterTraffic            string = "cluster-traffic"
	GrantClusterShowBackups        string = "cluster-show-backups"
	GrantClusterShowJobs           string = "cluster-show-jobs"
	GrantClusterShowRoutes         string = "cluster-show-routes"
	GrantClusterShowGraphs         string = "cluster-show-graphs"
	GrantClusterConfigGraphs       string = "cluster-config-graphs"
	GrantClusterShowAgents         string = "cluster-show-agents"
	GrantClusterShowCertificates   string = "cluster-show-certificates"
	GrantClusterRotatePasswords    string = "cluster-rotate-passwords"
	GrantClusterResetSLA           string = "cluster-reset-sla"
	GrantClusterDebug              string = "cluster-debug"
	GrantClusterStaging            string = "cluster-staging"
	GrantClusterAlert              string = "cluster-alert"
	GrantClusterDocker             string = "cluster-docker"

	GrantProxyConfigCreate    string = "proxy-config-create"
	GrantProxyConfigGet       string = "proxy-config-get"
	GrantProxyConfigRessource string = "proxy-config-ressource"
	GrantProxyConfigFlag      string = "proxy-config-flag"
	GrantProxyStart           string = "proxy-start"
	GrantProxyStop            string = "proxy-stop"

	GrantAppConfig     string = "app-config"
	GrantAppDocker     string = "app-docker"
	GrantAppDeployment string = "app-deployment"
	GrantAppStart      string = "app-start"
	GrantAppStop       string = "app-stop"
	GrantAppGit        string = "app-git"

	GrantProvClusterProvision   string = "prov-cluster-provision"
	GrantProvClusterUnprovision string = "prov-cluster-unprovision"
	GrantProvProxyProvision     string = "prov-proxy-provision"
	GrantProvProxyUnprovision   string = "prov-proxy-unprovision"
	GrantProvDBProvision        string = "prov-db-provision"
	GrantProvDBUnprovision      string = "prov-db-unprovision"
	GrantProvAppProvision       string = "prov-app-provision"
	GrantProvAppUnprovision     string = "prov-app-unprovision"
	GrantProvSettings           string = "prov-settings"
	GrantProvCluster            string = "prov-cluster"

	GrantGlobalSettings  string = "global-settings"     // Can update global settings
	GrantGlobalGrant     string = "global-grant"        // Can grant global settings
	GrantGlobalAdminShow string = "global-admin-show"   // Can view global dashboard (logs, metrics, alerts)
	GrantGlobalAdminConf string = "global-admin-config" // Can modify global-level monitoring configuration

	GrantGrantShow   string = "grant-show"   // Can show users settings
	GrantGrantAdd    string = "grant-add"    // Can add new user
	GrantGrantDrop   string = "grant-drop"   // Can drop user ACL
	GrantGrantModify string = "grant-modify" // Can modify user ACL
	GrantGrantGlobal string = "grant-global" // Can grant global acl

	GrantShow         string = "show"    // Can show basic view
	GrantExternalRole string = "extrole" // Can manage external ops

	GrantSalesValidate    string = "sales-validate"    // Can validate sales
	GrantSalesRefuse      string = "sales-refuse"      // Can refuse sales
	GrantSalesUnsubscribe string = "sales-unsubscribe" // Can unsubscribe sales

	GrantTerminalDatabase string = "terminal-db"
	GrantTerminalProxy    string = "terminal-proxy"
	GrantTerminalApp      string = "terminal-app"
	GrantTerminalGlobal   string = "terminal-global" // Can use global terminal
)

// GetGrantType returns the full map of all known grant identifiers.
func GetGrantType() map[string]string {
	return map[string]string{
		GrantDBStart:                   GrantDBStart,
		GrantDBStop:                    GrantDBStop,
		GrantDBKill:                    GrantDBKill,
		GrantDBOptimize:                GrantDBOptimize,
		GrantDBAnalyse:                 GrantDBAnalyse,
		GrantDBReplication:             GrantDBReplication,
		GrantDBBackup:                  GrantDBBackup,
		GrantDBRestore:                 GrantDBRestore,
		GrantDBReadOnly:                GrantDBReadOnly,
		GrantDBLogs:                    GrantDBLogs,
		GrantDBCapture:                 GrantDBCapture,
		GrantDBMaintenance:             GrantDBMaintenance,
		GrantDBConfigCreate:            GrantDBConfigCreate,
		GrantDBConfigRessource:         GrantDBConfigRessource,
		GrantDBConfigFlag:              GrantDBConfigFlag,
		GrantDBConfigGet:               GrantDBConfigGet,
		GrantDBShowVariables:           GrantDBShowVariables,
		GrantDBShowStatus:              GrantDBShowStatus,
		GrantDBShowSchema:              GrantDBShowSchema,
		GrantDBShowProcess:             GrantDBShowProcess,
		GrantDBShowLogs:                GrantDBShowLogs,
		GrantClusterCreate:             GrantClusterCreate,
		GrantClusterDelete:             GrantClusterDelete,
		GrantClusterCreateMonitor:      GrantClusterCreateMonitor,
		GrantClusterDropMonitor:        GrantClusterDropMonitor,
		GrantClusterFailover:           GrantClusterFailover,
		GrantClusterSwitchover:         GrantClusterSwitchover,
		GrantClusterRolling:            GrantClusterRolling,
		GrantClusterSettings:           GrantClusterSettings,
		GrantClusterGrant:              GrantClusterGrant,
		GrantClusterReplication:        GrantClusterReplication,
		GrantClusterAnalyze:            GrantClusterAnalyze,
		GrantClusterChecksum:           GrantClusterChecksum,
		GrantClusterChecksumRepair:     GrantClusterChecksumRepair,
		GrantClusterSharding:           GrantClusterSharding,
		GrantClusterCertificatesRotate: GrantClusterCertificatesRotate,
		GrantClusterCertificatesReload: GrantClusterCertificatesReload,
		GrantClusterBench:              GrantClusterBench,
		GrantClusterTest:               GrantClusterTest,
		GrantClusterTraffic:            GrantClusterTraffic,
		GrantClusterProcess:            GrantClusterProcess,
		GrantClusterDebug:              GrantClusterDebug,
		GrantClusterShowBackups:        GrantClusterShowBackups,
		GrantClusterShowJobs:           GrantClusterShowJobs,
		GrantClusterShowAgents:         GrantClusterShowAgents,
		GrantClusterShowGraphs:         GrantClusterShowGraphs,
		GrantClusterConfigGraphs:       GrantClusterConfigGraphs,
		GrantClusterShowRoutes:         GrantClusterShowRoutes,
		GrantClusterShowCertificates:   GrantClusterShowCertificates,
		GrantClusterResetSLA:           GrantClusterResetSLA,
		GrantClusterRotatePasswords:    GrantClusterRotatePasswords,
		GrantClusterStaging:            GrantClusterStaging,
		GrantClusterAlert:              GrantClusterAlert,
		GrantClusterDocker:             GrantClusterDocker,
		GrantProxyConfigCreate:         GrantProxyConfigCreate,
		GrantProxyConfigGet:            GrantProxyConfigGet,
		GrantProxyConfigRessource:      GrantProxyConfigRessource,
		GrantProxyConfigFlag:           GrantProxyConfigFlag,
		GrantProxyStart:                GrantProxyStart,
		GrantProxyStop:                 GrantProxyStop,
		GrantProvSettings:              GrantProvSettings,
		GrantProvCluster:               GrantProvCluster,
		GrantProvClusterProvision:      GrantProvClusterProvision,
		GrantProvClusterUnprovision:    GrantProvClusterUnprovision,
		GrantProvDBUnprovision:         GrantProvDBUnprovision,
		GrantProvDBProvision:           GrantProvDBProvision,
		GrantProvProxyProvision:        GrantProvProxyProvision,
		GrantProvProxyUnprovision:      GrantProvProxyUnprovision,
		GrantProvAppProvision:          GrantProvAppProvision,
		GrantProvAppUnprovision:        GrantProvAppUnprovision,
		GrantAppConfig:                 GrantAppConfig,
		GrantAppDocker:                 GrantAppDocker,
		GrantAppDeployment:             GrantAppDeployment,
		GrantAppStart:                  GrantAppStart,
		GrantAppStop:                   GrantAppStop,
		GrantAppGit:                    GrantAppGit,
		GrantGlobalGrant:               GrantGlobalGrant,
		GrantGlobalSettings:            GrantGlobalSettings,
		GrantGlobalAdminShow:           GrantGlobalAdminShow,
		GrantGlobalAdminConf:           GrantGlobalAdminConf,
		GrantSalesValidate:             GrantSalesValidate,
		GrantSalesRefuse:               GrantSalesRefuse,
		GrantSalesUnsubscribe:          GrantSalesUnsubscribe,
		GrantExternalRole:              GrantExternalRole,
		GrantGrantShow:                 GrantGrantShow,
		GrantGrantAdd:                  GrantGrantAdd,
		GrantGrantModify:               GrantGrantModify,
		GrantGrantDrop:                 GrantGrantDrop,
		GrantGrantGlobal:               GrantGrantGlobal,
		GrantShow:                      GrantShow,
		GrantTerminalDatabase:          GrantTerminalDatabase,
		GrantTerminalProxy:             GrantTerminalProxy,
		GrantTerminalApp:               GrantTerminalApp,
		GrantTerminalGlobal:            GrantTerminalGlobal,
	}
}

func GetGrantDB() []string {
	return []string{
		GrantDBStart,
		GrantDBStop,
		GrantDBKill,
		GrantDBOptimize,
		GrantDBAnalyse,
		GrantDBReplication,
		GrantDBBackup,
		GrantDBRestore,
		GrantDBReadOnly,
		GrantDBLogs,
		GrantDBCapture,
		GrantDBMaintenance,
		GrantDBConfigCreate,
		GrantDBConfigRessource,
		GrantDBConfigFlag,
		GrantDBConfigGet,
		GrantDBShowVariables,
		GrantDBShowStatus,
		GrantDBShowSchema,
		GrantDBShowProcess,
		GrantDBShowLogs,
	}
}

func HasAllDBGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantDB() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantCluster() []string {
	return []string{
		GrantClusterCreate,
		GrantClusterDelete,
		GrantClusterCreateMonitor,
		GrantClusterDropMonitor,
		GrantClusterFailover,
		GrantClusterSwitchover,
		GrantClusterRolling,
		GrantClusterSettings,
		GrantClusterGrant,
		GrantClusterReplication,
		GrantClusterAnalyze,
		GrantClusterChecksum,
		GrantClusterChecksumRepair,
		GrantClusterSharding,
		GrantClusterCertificatesRotate,
		GrantClusterCertificatesReload,
		GrantClusterBench,
		GrantClusterTest,
		GrantClusterTraffic,
		GrantClusterProcess,
		GrantClusterDebug,
		GrantClusterShowBackups,
		GrantClusterShowAgents,
		GrantClusterShowGraphs,
		GrantClusterConfigGraphs,
		GrantClusterShowRoutes,
		GrantClusterShowCertificates,
		GrantClusterResetSLA,
		GrantClusterRotatePasswords,
		GrantClusterStaging,
		GrantClusterAlert,
		GrantClusterDocker,
	}
}

func HasAllClusterGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantCluster() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantProxy() []string {
	return []string{
		GrantProxyConfigCreate,
		GrantProxyConfigGet,
		GrantProxyConfigRessource,
		GrantProxyConfigFlag,
		GrantProxyStart,
		GrantProxyStop,
	}
}

func HasAllProxyGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantProxy() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantProvision() []string {
	return []string{
		GrantProvSettings,
		GrantProvCluster,
		GrantProvClusterProvision,
		GrantProvClusterUnprovision,
		GrantProvDBUnprovision,
		GrantProvDBProvision,
		GrantProvProxyProvision,
		GrantProvProxyUnprovision,
		GrantProvAppProvision,
		GrantProvAppUnprovision,
	}
}

func HasAllProvisionGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantProvision() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantApp() []string {
	return []string{
		GrantAppStart,
		GrantAppStop,
		GrantAppConfig,
		GrantAppDocker,
		GrantAppDeployment,
		GrantAppGit,
	}
}

func HasAllAppGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantApp() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantGlobal() []string {
	return []string{
		GrantGlobalGrant,
		GrantGlobalSettings,
		GrantGlobalAdminShow,
		GrantGlobalAdminConf,
	}
}

func HasAllGlobalGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantGlobal() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantSales() []string {
	return []string{
		GrantSalesValidate,
		GrantSalesRefuse,
		GrantSalesUnsubscribe,
	}
}

func HasAllSalesGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantSales() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantGrant() []string {
	return []string{
		GrantGrantShow,
		GrantGrantAdd,
		GrantGrantModify,
		GrantGrantDrop,
		GrantGrantGlobal,
	}
}

func HasAllGrantGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantGrant() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetGrantTerminal() []string {
	return []string{
		GrantTerminalDatabase,
		GrantTerminalProxy,
		GrantTerminalApp,
		GrantTerminalGlobal,
	}
}

func HasAllTerminalGrants(grants map[string]bool) bool {
	for _, grant := range GetGrantTerminal() {
		if !grants[grant] {
			return false
		}
	}
	return true
}

func GetCompactGrants(grants map[string]bool) ([]string, []string) {
	var compactGrants []string
	var compactDiscardGrants []string
	var counter int

	// DB
	tmp := make([]string, 0)
	if HasAllDBGrants(grants) {
		compactGrants = append(compactGrants, "db")
	} else {
		for _, grant := range GetGrantDB() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "db")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Cluster
	tmp = make([]string, 0)
	counter = 0
	if HasAllClusterGrants(grants) {
		compactGrants = append(compactGrants, "cluster")
	} else {
		for _, grant := range GetGrantCluster() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "cluster")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Proxy
	tmp = make([]string, 0)
	counter = 0
	if HasAllProxyGrants(grants) {
		compactGrants = append(compactGrants, "proxy")
	} else {
		for _, grant := range GetGrantProxy() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "proxy")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Provision
	tmp = make([]string, 0)
	counter = 0
	if HasAllProvisionGrants(grants) {
		compactGrants = append(compactGrants, "prov")
	} else {
		for _, grant := range GetGrantProvision() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "prov")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// App
	tmp = make([]string, 0)
	counter = 0
	if HasAllAppGrants(grants) {
		compactGrants = append(compactGrants, "app")
	} else {
		for _, grant := range GetGrantApp() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "app")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Global
	tmp = make([]string, 0)
	counter = 0
	if HasAllGlobalGrants(grants) {
		compactGrants = append(compactGrants, "global")
	} else {
		for _, grant := range GetGrantGlobal() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "global")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Sales
	tmp = make([]string, 0)
	counter = 0
	if HasAllSalesGrants(grants) {
		compactGrants = append(compactGrants, "sales")
	} else {
		for _, grant := range GetGrantSales() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "sales")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Grant
	tmp = make([]string, 0)
	counter = 0
	if HasAllGrantGrants(grants) {
		compactGrants = append(compactGrants, "grant")
	} else {
		for _, grant := range GetGrantGrant() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "grant")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	// Terminal
	tmp = make([]string, 0)
	counter = 0
	if HasAllTerminalGrants(grants) {
		compactGrants = append(compactGrants, "terminal")
	} else {
		for _, grant := range GetGrantTerminal() {
			if grants[grant] {
				compactGrants = append(compactGrants, grant)
				counter++
			} else {
				tmp = append(tmp, grant)
			}
		}
		if counter == 0 {
			compactDiscardGrants = append(compactDiscardGrants, "terminal")
		} else {
			compactDiscardGrants = append(compactDiscardGrants, tmp...)
		}
	}

	if grants["show"] {
		compactGrants = append(compactGrants, "show")
	} else {
		compactDiscardGrants = append(compactDiscardGrants, "show")
	}

	if grants["extrole"] {
		compactGrants = append(compactGrants, "extrole")
	} else {
		compactDiscardGrants = append(compactDiscardGrants, "extrole")
	}

	return compactGrants, compactDiscardGrants
}

func GetRoleType() map[string]string {
	return map[string]string{
		RoleSysOps:                RoleSysOps,
		RoleDBOps:                 RoleDBOps,
		RoleExtSysOps:             RoleExtSysOps,
		RoleExtDBOps:              RoleExtDBOps,
		RoleSponsor:               RoleSponsor,
		RolePending:               RolePending,
		RolePendingExtDBOps:       RolePendingExtDBOps,
		RolePendingExtSysOps:      RolePendingExtSysOps,
		RoleQuoteExtDBOps:         RoleQuoteExtDBOps,
		RoleQuoteExtSysOps:        RoleQuoteExtSysOps,
		RoleUnsubscribed:          RoleUnsubscribed,
		RoleUnsubscribedExtDBOps:  RoleUnsubscribedExtDBOps,
		RoleUnsubscribedExtSysOps: RoleUnsubscribedExtSysOps,
		RoleVisitor:               RoleVisitor,
	}
}

func GetCompactRoles(roles map[string]bool) []string {
	rolelist := make([]string, 0)
	for role, v := range roles {
		if v {
			rolelist = append(rolelist, role)
		}
	}
	return rolelist
}

func GetDefaultAllowDiscardACL(role string) (string, string) {
	switch role {
	case RoleSysOps:
		return "*", ""
	case RoleExtSysOps:
		return "*", "sales global extrole"
	case RoleDBOps:
		return "*", "cluster prov sales global"
	case RoleSponsor:
		return "db show proxy grant extrole sales-unsubscribe app", ""
	case RoleExtDBOps:
		return "db show proxy grant", "extrole"
	default:
		return "show", ""
	}
}

func GetDefaultGrants(role string) string {
	grants := make(map[string]bool)
	gtypes := GetGrantType()
	allow, discard := GetDefaultAllowDiscardACL(role)

	for _, grant := range gtypes {
		found := false

		if allow == "*" {
			found = true
		} else {
			for _, acl := range strings.Split(allow, " ") {
				if strings.HasPrefix(grant, acl) && acl != "" {
					found = true
					break
				}
			}
		}

		if discard != "" {
			for _, acl := range strings.Split(discard, " ") {
				if strings.HasPrefix(grant, acl) && acl != "" {
					found = false
					break
				}
			}
		}

		grants[grant] = found
	}

	allowlist, _ := GetCompactGrants(grants)
	return strings.Join(allowlist, " ")
}

func GetRoleDefaultGrant(roles string) string {
	list := strings.Split(roles, " ")
	if slices.Contains(list, RoleSysOps) {
		return GetDefaultGrants(RoleSysOps)
	} else if slices.Contains(list, RoleExtSysOps) {
		return GetDefaultGrants(RoleExtSysOps)
	} else if slices.Contains(list, RoleSponsor) {
		return GetDefaultGrants(RoleSponsor)
	} else if slices.Contains(list, RoleDBOps) {
		return GetDefaultGrants(RoleDBOps)
	} else if slices.Contains(list, RoleExtDBOps) {
		return GetDefaultGrants(RoleExtDBOps)
	}
	return ""
}
