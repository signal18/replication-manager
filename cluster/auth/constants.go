package clusterauth

import "sort"

const (
	ExternalActive  string = "active"
	ExternalPending string = "pending"
	ExternalQuote   string = "quote"
)

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

const (
	GrantDBStart                   string = "db-start"
	GrantDBStop                    string = "db-stop"
	GrantDBKill                    string = "db-kill"
	GrantDBOptimize                string = "db-optimize"
	GrantDBAnalyse                 string = "db-analyse"
	GrantDBReplication             string = "db-replication"
	GrantDBBackup                  string = "db-backup"
	GrantDBRestore                 string = "db-restore"
	GrantDBReadOnly                string = "db-readonly"
	GrantDBLogs                    string = "db-logs"
	GrantDBShowVariables           string = "db-show-variables"
	GrantDBShowStatus              string = "db-show-status"
	GrantDBShowSchema              string = "db-show-schema"
	GrantDBShowProcess             string = "db-show-process"
	GrantDBShowLogs                string = "db-show-logs"
	GrantDBCapture                 string = "db-capture"
	GrantDBMaintenance             string = "db-maintenance"
	GrantDBConfigCreate            string = "db-config-create"
	GrantDBConfigResource          string = "db-config-resource"
	GrantDBConfigFlag              string = "db-config-flag"
	GrantDBConfigGet               string = "db-config-get"
	GrantDBTerminal                string = "db-terminal"
	GrantClusterCreate             string = "cluster-create"
	GrantClusterDelete             string = "cluster-delete"
	GrantClusterCreateMonitor      string = "cluster-create-monitor"
	GrantClusterDropMonitor        string = "cluster-drop-monitor"
	GrantClusterFailover           string = "cluster-failover"
	GrantClusterSwitchover         string = "cluster-switchover"
	GrantClusterRolling            string = "cluster-rolling"
	GrantClusterSettings           string = "cluster-settings"
	GrantClusterGrant              string = "cluster-grant"
	GrantClusterChecksum           string = "cluster-checksum"
	GrantClusterSharding           string = "cluster-sharding"
	GrantClusterReplication        string = "cluster-replication"
	GrantClusterCertificatesRotate string = "cluster-certificates-rotate"
	GrantClusterCertificatesReload string = "cluster-certificates-reload"
	GrantClusterBench              string = "cluster-bench"
	GrantClusterProcess            string = "cluster-process" //Can ssh for jobs
	GrantClusterTest               string = "cluster-test"
	GrantClusterTraffic            string = "cluster-traffic"
	GrantClusterShowBackups        string = "cluster-show-backups"
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

	GrantProxyConfigCreate    string = "proxy-config-create"
	GrantProxyConfigGet       string = "proxy-config-get"
	GrantProxyConfigRessource string = "proxy-config-ressource"
	GrantProxyConfigFlag      string = "proxy-config-flag"
	GrantProxyStart           string = "proxy-start"
	GrantProxyStop            string = "proxy-stop"
	GrantProxyTerminal        string = "proxy-terminal"

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

	GrantAppStart    string = "app-start"
	GrantAppStop     string = "app-stop"
	GrantAppTerminal string = "app-terminal"

	GrantGlobalSettings string = "global-settings" // Can update global settings
	GrantGlobalGrant    string = "global-grant"    // Can grant global settings
	GrantGlobalTerminal string = "global-terminal" // Can use global terminal

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
)

var grantDB = []string{
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
	GrantDBConfigResource,
	GrantDBConfigFlag,
	GrantDBConfigGet,
	GrantDBShowVariables,
	GrantDBShowStatus,
	GrantDBShowSchema,
	GrantDBShowProcess,
	GrantDBShowLogs,
	GrantDBTerminal,
}

var grantCluster = []string{
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
	GrantClusterChecksum,
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
}

var grantProxy = []string{
	GrantProxyConfigCreate,
	GrantProxyConfigGet,
	GrantProxyConfigRessource,
	GrantProxyConfigFlag,
	GrantProxyStart,
	GrantProxyStop,
	GrantProxyTerminal,
}

var grantApp = []string{
	GrantAppStart,
	GrantAppStop,
	GrantAppTerminal,
}

var grantProvision = []string{
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

var grantGlobal = []string{
	GrantGlobalGrant,
	GrantGlobalSettings,
	GrantGlobalTerminal,
}

var grantSales = []string{
	GrantSalesValidate,
	GrantSalesRefuse,
	GrantSalesUnsubscribe,
}

var grantGrant = []string{
	GrantGrantShow,
	GrantGrantAdd,
	GrantGrantModify,
	GrantGrantDrop,
	GrantGrantGlobal,
}

var allGrants []string

var grantMap = make(map[string]struct{})

// AllRoles contains all possible roles in alphabetical order
var allRoles []string = []string{
	RoleDBOps,
	RoleExtDBOps,
	RoleExtSysOps,
	RolePending,
	RolePendingExtDBOps,
	RolePendingExtSysOps,
	RoleQuoteExtDBOps,
	RoleQuoteExtSysOps,
	RoleSponsor,
	RoleSysOps,
	RoleUnsubscribed,
	RoleUnsubscribedExtDBOps,
	RoleUnsubscribedExtSysOps,
	RoleVisitor,
}

var defaultACL map[string]AllowDiscardACL = map[string]AllowDiscardACL{
	RoleSysOps:    {"*", ""},
	RoleExtSysOps: {"*", "sales global extrole"},
	RoleDBOps:     {"*", "cluster prov sales global"},
	RoleSponsor:   {"db show proxy grant extrole sales-unsubscribe app", ""},
	RoleExtDBOps:  {"db show proxy grant", "extrole"},
}

// Init Function
func init() {
	// Preallocate `allGrants` slice for better performance
	allGrants = make([]string, 0, len(grantDB)+len(grantCluster)+len(grantProxy)+len(grantProvision)+len(grantGlobal)+len(grantSales)+len(grantGrant)+len(grantApp)+2)

	// Aggregate all grants
	for _, grantList := range [][]string{grantDB, grantCluster, grantProxy, grantProvision, grantGlobal, grantSales, grantGrant, grantApp} {
		allGrants = append(allGrants, grantList...)
	}

	// Add individual grants
	allGrants = append(allGrants, GrantShow, GrantExternalRole)

	// Sort all grants in alphabetical order
	sort.Strings(allGrants)

	// Fill grantMap for fast lookup
	for _, grant := range allGrants {
		grantMap[grant] = struct{}{}
	}
}
