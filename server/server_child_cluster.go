// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// Child cluster creation (#1988 staging, #1997 child): a cluster seeded from a parent
// and replicating from it, in one call. It lives on the parent's replication-manager
// (its seed reads the parent's backup locally), is provisioned on the parent's
// orchestrator, and is seeded with the parent's data only: it keeps its own system
// tables and release. A staging cluster detaches one replica for the dev team; a child
// cluster keeps every node replicating, to test a migration under the parent's writes.

const (
	childMaxDBCount = 5
	// childColumnstoreImage: a ColumnStore child's database image by default (#2012):
	// MariaDB with ColumnStore and CMAPI (docker/columnstore)
	childColumnstoreImage = "signal18/mariadb-columnstore:11.8"
	childMasterWait       = 5 * time.Minute
	childNodesWait        = 2 * time.Minute // the new cluster loads its orchestrator node list on its own tick
	stagingDBCount        = 3               // the built-in refresh detaches the first of 2 replicas
)

// childMode is what differs between a staging and a child cluster.
type childMode struct {
	kind          string // in states and logs: Staging, Child
	suffix        string // default name: <parent><suffix>
	ownSourceName string // replication-source-name of the child's internal channel (master to replicas)
	dbCount       int    // default number of databases
	staging       bool   // topology-staging, and the refresh detaches a replica
}

var (
	childModeStaging = childMode{kind: "Staging", suffix: "-staging", ownSourceName: "staging", dbCount: stagingDBCount, staging: true}
	childModeChild   = childMode{kind: "Child", suffix: "-child", ownSourceName: "child", dbCount: 2}
)

// ChildRequest is the body of POST /api/clusters/{parent}/actions/create-staging and
// create-child, defined once with the MCP tools' spec.
type ChildRequest = repmanmcp.ChildClusterSpec

// ChildPlan is what create-staging or create-child will build, returned before anything
// is created.
type ChildPlan struct {
	Kind              string   `json:"kind"`
	Parent            string   `json:"parent"`
	Name              string   `json:"name"`
	Orchestrator      string   `json:"orchestrator"`
	DBImage           string   `json:"dbImage"`
	DBCount           int      `json:"dbCount"`
	DBUPerNode        int      `json:"dbuPerNode"`
	Proxy             string   `json:"proxy"`              // "" = none
	Domain            string   `json:"domain"`             // GTID domain: the parent's + 1
	SourceName        string   `json:"sourceName"`         // channel from the parent's master: the parent's replication-source-name
	OwnSourceName     string   `json:"ownSourceName"`      // the new cluster's replication-source-name (its master to its replicas)
	BackupLogicalType string   `json:"backupLogicalType"`  // the parent's
	Topology          string   `json:"topology,omitempty"` // the child's topology-target, "" the default (#2012)
	Backup            string   `json:"backup"`
	Notes             []string `json:"notes,omitempty"`
}

// childProxyType is the parent's first provisionable proxy type, "" when it has none.
func childProxyType(parent *cluster.Cluster) string {
	for _, prx := range parent.GetProxies() {
		switch t := prx.GetType(); t {
		case config.ConstProxyHaproxy, config.ConstProxySqlproxy, config.ConstProxyMaxscale:
			return t
		}
	}
	return ""
}

// childProxyCredential is the parent's admin credential for a proxy type: the
// credential setting and its value (user:password, the secrets the rendered proxy
// config takes its password from), and the user and password as configured (the
// login replication-manager monitors the proxy with). "" for a type without one.
func childProxyCredential(parent *cluster.Cluster, proxyType string) (key, cred, user, confPass string) {
	switch proxyType {
	case config.ConstProxySqlproxy:
		return "proxysql-servers-credential", parent.Conf.ProxysqlUser + ":" + parent.Conf.Secrets["proxysql-password"].Value, parent.Conf.ProxysqlUser, parent.Conf.ProxysqlPassword
	case config.ConstProxyMaxscale:
		return "maxscale-servers-credential", parent.Conf.MxsUser + ":" + parent.Conf.Secrets["maxscale-pass"].Value, parent.Conf.MxsUser, parent.Conf.MxsPass
	}
	return "", "", "", ""
}

// PlanChildCluster checks a create-staging or create-child request against the parent
// and returns what would be created. The first check is the parent's logical backup:
// without it nothing can be seeded, so nothing is created.
func (repman *ReplicationManager) PlanChildCluster(parent *cluster.Cluster, req ChildRequest, mode childMode) (ChildPlan, error) {
	plan := ChildPlan{Kind: mode.kind, Parent: parent.Name}
	backup, _, err := parent.ParentLogicalBackup()
	if err != nil {
		return plan, err
	}
	plan.Backup = backup
	if parent.Conf.TopologyStaging {
		return plan, fmt.Errorf("%s is itself a staging cluster", parent.Name)
	}
	plan.Orchestrator = parent.GetOrchestrator()
	if plan.Orchestrator != config.ConstOrchestratorOpenSVC && plan.Orchestrator != config.ConstOrchestratorKubernetes {
		return plan, fmt.Errorf("%s runs on %s: a %s cluster is provisioned on new servers, only OpenSVC and Kubernetes clusters can", parent.Name, plan.Orchestrator, strings.ToLower(mode.kind))
	}
	name := req.Name
	if name == "" {
		name = parent.Name + mode.suffix
	}
	if plan.Name, err = plainClusterName(name); err != nil {
		return plan, err
	}
	if repman.getClusterByName(plan.Name) != nil {
		return plan, fmt.Errorf("cluster %s already exists", plan.Name)
	}
	plan.Topology = req.Topology
	if plan.Topology != "" {
		if mode.staging {
			return plan, fmt.Errorf("a staging cluster has the staging topology: no topology to choose")
		}
		if _, ok := config.GetTopologyType()[plan.Topology]; !ok {
			return plan, fmt.Errorf("unknown topology %q", plan.Topology)
		}
	}
	columnstore := plan.Topology == config.TopoColumnstore
	plan.DBCount = req.DBCount
	if plan.DBCount == 0 {
		// ColumnStore: equal servers, each a UM and a PM holding a dbroot (#2021)
		plan.DBCount = mode.dbCount
	}
	if plan.DBCount < 1 || plan.DBCount > childMaxDBCount {
		return plan, fmt.Errorf("dbCount must be between 1 and %d", childMaxDBCount)
	}
	if mode.staging && plan.DBCount != stagingDBCount {
		plan.Notes = append(plan.Notes, fmt.Sprintf("the built-in staging refresh needs %d databases (a master and 2 replicas): with %d, set topology-staging-refresh-script", stagingDBCount, plan.DBCount))
	}
	plan.DBImage = req.DBImage
	if plan.DBImage == "" {
		plan.DBImage = parent.Conf.ProvDbImg
		if columnstore {
			plan.DBImage = childColumnstoreImage
		}
	}
	plan.Notes = append(plan.Notes, "configurator tags: the parent's ("+parent.Conf.ProvTags+")")
	if columnstore {
		plan.Notes = append(plan.Notes, "each table of the parent's backup is converted to ColumnStore at the seed: keys removed, types widened or mapped (the log lists every change)")
		plan.Notes = append(plan.Notes, fmt.Sprintf("%d ColumnStore servers, each its own failover service holding a dbroot; the master is seeded, the others replicate its schema", plan.DBCount))
	}
	plan.DBUPerNode = parent.Conf.ProvDbDbu
	if columnstore && plan.DBUPerNode < cluster.ColumnstoreMinDBU {
		plan.DBUPerNode = cluster.ColumnstoreMinDBU
		plan.Notes = append(plan.Notes, fmt.Sprintf("%d DBU per server: a ColumnStore server runs a UM and a PM, 1 DBU each", plan.DBUPerNode))
	}
	plan.Proxy = childProxyType(parent)
	// Another GTID domain than the parent's: the child master replicates from the
	// parent and takes its own writes (staging doc, prov-db-domain). The parent's is
	// the one its configurator renders.
	parentDomain, err := strconv.ParseUint(parent.Configurator.GetConfigReplicationDomain(parent.Name), 10, 32)
	if err != nil {
		return plan, fmt.Errorf("parent %s GTID domain: %w", parent.Name, err)
	}
	plan.Domain = strconv.FormatUint(parentDomain+1, 10)
	// Two channels, two names (staging doc): the child master replicates from the parent's
	// master through the parent's replication-source-name, the same on both sides; the
	// child's own replicas through the child's, which must differ (the same name made the
	// child master a Relay of the parent's, the cluster masterless: dev3, 2026-10-12).
	plan.SourceName = parent.Conf.MasterConn
	plan.OwnSourceName = mode.ownSourceName
	if plan.OwnSourceName == plan.SourceName {
		return plan, fmt.Errorf("parent %s replicates through replication-source-name %q, the name the %s cluster's own channel would take: change the parent's", parent.Name, plan.SourceName, strings.ToLower(mode.kind))
	}
	plan.BackupLogicalType = parent.Conf.BackupLogicalType
	return plan, nil
}

// CreateChildCluster creates the planned cluster for a creator already authorised
// (clusterAddAuthorize), configures it and adds its servers, then provisions and seeds
// it in the background. Every step is recorded on the new cluster (CINF0014 while it
// runs, WARN0239 where it stopped); a cluster left half-built is kept, never dropped
// behind the creator's back.
func (repman *ReplicationManager) CreateChildCluster(parent *cluster.Cluster, plan ChildPlan, mode childMode, username string, sso bool, selfService bool, remoteAddr string) (*cluster.Cluster, error) {
	cl, err := repman.createCluster(plan.Name, cluster.ClusterForm{ClusterName: plan.Name, Orchestrator: plan.Orchestrator}, username, sso, selfService, remoteAddr)
	if err != nil {
		return nil, err
	}
	stepName := ""
	step := func(s string) {
		stepName = s
		cl.SetStagingCreation(cluster.StagingCreation{Kind: mode.kind, Parent: parent.Name, Step: s})
	}
	fail := func(err error) error {
		cl.SetStagingCreation(cluster.StagingCreation{Kind: mode.kind, Parent: parent.Name, Step: stepName, Error: err.Error()})
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s cluster %s from %s stopped at %s: %s", mode.kind, cl.Name, parent.Name, stepName, err)
		return err
	}

	step("configuration")
	if parent.Conf.ProvOrchestratorCluster != cl.Conf.ProvOrchestratorCluster {
		if err := cl.SetProvOrchestratorCluster(parent.Conf.ProvOrchestratorCluster); err != nil {
			return cl, fail(err)
		}
	}
	if plan.DBImage != "" && plan.DBImage != cl.Conf.ProvDbImg {
		if err := repman.SetClusterSetting(cl, "prov-db-image", plan.DBImage); err != nil {
			return cl, fail(err)
		}
	}
	if mode.staging {
		if err := repman.SetClusterSetting(cl, "topology-staging", "true"); err != nil {
			return cl, fail(err)
		}
	}
	// the parent's configurator tags (#2013): the child replays its data and writes, it
	// needs its server configuration (preprod: belair's noforcepk, its tables without a
	// primary key refused by the child's default innodb tag); a topology adds its own on
	// top (columnstore: mcs, below)
	cl.SetDBTagsConfig(parent.Configurator.GetDBTags())
	// before the bootstrap: it reads the target (#2012)
	if plan.Topology != "" {
		if err := repman.SetClusterSetting(cl, "topology-target", plan.Topology); err != nil {
			return cl, fail(err)
		}
	}
	if err := repman.SetClusterSetting(cl, "prov-db-domain", plan.Domain); err != nil {
		return cl, fail(err)
	}
	if err := repman.SetClusterSetting(cl, "replication-source-name", plan.OwnSourceName); err != nil {
		return cl, fail(err)
	}
	// the parent's backup tool: the logical backup started after the seed, and later
	// refreshes, use the parent's (dev3: the default mysqldump failed, the parent runs mydumper)
	if err := repman.SetClusterSetting(cl, "backup-logical-type", plan.BackupLogicalType); err != nil {
		return cl, fail(err)
	}
	// The parent's monitoring and replication accounts, same name same password (staging
	// doc): provisioned identical, nothing to restore from the parent's backup.
	for _, key := range []string{"db-servers-credential", "replication-credential"} {
		if err := repman.SetClusterSetting(cl, key, parent.Conf.Secrets[key].Value); err != nil {
			return cl, fail(err)
		}
	}
	if err := repman.SetClusterSetting(cl, "replication-multisource-head-clusters", parent.Name); err != nil {
		return cl, fail(err)
	}

	step("servers")
	for i := 1; i <= plan.DBCount; i++ {
		if err := cl.AddSeededServer(fmt.Sprintf("db%d:3306", i)); err != nil {
			return cl, fail(err)
		}
	}
	if plan.Proxy != "" {
		// The parent's proxy admin credential. The rendered ProxySQL config declares
		// admin and external with the password of the credential secrets; ProxySQL
		// accepts admin only locally, so replication-manager must log in with the
		// parent's user (external), not the default admin (dev3, ERR00051): the
		// secrets through the setting the GUI's Set Credentials uses, the login user
		// through the proxy add.
		key, cred, user, confPass := childProxyCredential(parent, plan.Proxy)
		if key != "" {
			if err := repman.SetClusterSetting(cl, key, cred); err != nil {
				return cl, fail(err)
			}
		}
		if err := cl.AddSeededProxy(plan.Proxy, plan.Proxy+"1", "3306", user, confPass); err != nil {
			return cl, fail(err)
		}
	}
	// after the servers: the plan ledger counts DBU per node x nodes
	if delta := plan.DBUPerNode - cl.Conf.ProvDbDbu; plan.DBUPerNode > 0 && delta != 0 {
		step("plan")
		if err := cl.ChangePlanUnits(cluster.PlanUnitDBU, delta); err != nil {
			return cl, fail(err)
		}
	}
	cl.Save()

	// the new cluster dropped meanwhile (or replaced by a reload): stop waiting for it
	gone := func() bool { return repman.getClusterByName(cl.Name) != cl }
	go func() {
		// a panic here must never take replication-manager down (#1998 review)
		defer func() {
			if r := recover(); r != nil {
				fail(fmt.Errorf("panic: %v", r))
			}
		}()
		step("orchestrator nodes")
		if err := cl.WaitOrchestratorNodes(childNodesWait, gone); err != nil {
			fail(err)
			return
		}
		step("provision")
		if err := cl.Bootstrap(); err != nil {
			fail(err)
			return
		}
		step("master")
		deadline := time.Now().Add(childMasterWait)
		for cl.GetMaster() == nil {
			if gone() {
				fail(errors.New("the cluster was dropped"))
				return
			}
			if time.Now().After(deadline) {
				fail(fmt.Errorf("no master after %s", childMasterWait))
				return
			}
			time.Sleep(time.Second)
		}
		step("seed from " + parent.Name)
		seed := cl.SeedChildFromParent
		if mode.staging {
			seed = cl.SeedFromParent
		}
		if err := seed(parent); err != nil {
			fail(err)
			return
		}
		cl.SetStagingCreation(cluster.StagingCreation{Kind: mode.kind, Parent: parent.Name, Step: "done", Done: true})
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "%s cluster %s created from %s", mode.kind, cl.Name, parent.Name)
	}()
	return cl, nil
}

// handlerMuxClusterCreateStaging creates a staging cluster of a cluster (#1988).
//
// @Summary Create a staging cluster of this cluster
// @Description Plans (confirm false) or creates (confirm true) a staging cluster of {clusterName}, named <clusterName>-staging unless a name is given: same orchestrator, database image unless dbImage is given, DBU per node and proxy type, dbCount databases (default 3), topology-staging on, this cluster as head cluster; provisioned, then seeded from this cluster's logical backup, data only (the staging cluster keeps its own accounts). Needs cluster-staging on this cluster and the right to create a cluster. The progress is the state CINF0014 on the new cluster, WARN0239 where it stopped.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Parent cluster name"
// @Param request body ChildRequest false "name, dbImage, dbCount, topology (child only), confirm"
// @Success 200 {object} ChildPlan "The plan (confirm false)"
// @Success 202 {object} ChildPlan "Creation started (confirm true)"
// @Failure 400 {string} string "Refused: the reason (no logical backup on the parent, orchestrator, name taken...)"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Failure 500 {string} string "Created and kept, then stopped: the cluster name, the step and the reason (WARN0239)"
// @Router /api/clusters/{clusterName}/actions/create-staging [post]
func (repman *ReplicationManager) handlerMuxClusterCreateStaging(w http.ResponseWriter, r *http.Request) {
	repman.createChildClusterRoute(w, r, childModeStaging)
}

// createChildClusterRoute serves create-staging and create-child: the parent's ACL
// (cluster-staging or cluster-replication, by route), the cluster-creation rights, the
// plan, then the creation on confirm.
func (repman *ReplicationManager) createChildClusterRoute(w http.ResponseWriter, r *http.Request, mode childMode) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	parent := repman.getClusterByName(mux.Vars(r)["clusterName"])
	if parent == nil {
		http.Error(w, "No cluster", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, parent); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	username, sso, selfService, status, reason := repman.clusterAddAuthorize(r)
	if status != 0 {
		http.Error(w, reason, status)
		return
	}
	var req ChildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	plan, err := repman.PlanChildCluster(parent, req, mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !req.Confirm {
		repman.jsonResponse(plan, w)
		return
	}
	if cl, err := repman.CreateChildCluster(parent, plan, mode, username, sso, selfService, r.RemoteAddr); err != nil {
		if cl == nil {
			// refused before anything was created
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// created, then stopped: never answered as a refusal, a retry would only meet
		// "already exists" (#1998 review)
		http.Error(w, fmt.Sprintf("cluster %s was created and kept, its creation stopped: %s (WARN0239 on it): fix the cause and reseed it from its parent, or drop it", cl.Name, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(plan)
}

// handlerMuxClusterCreateChild creates a child cluster of a cluster (#1997).
//
// @Summary Create a child cluster of this cluster
// @Description Plans (confirm false) or creates (confirm true) a child cluster of {clusterName}, named <clusterName>-child unless a name is given, to test a migration under this cluster's writes: same orchestrator, database image unless dbImage is given (a newer release to test), DBU per node, accounts and proxy type, dbCount databases (default 2), GTID domain this cluster's + 1, this cluster as head cluster; provisioned, then seeded from this cluster's logical backup, data only, its master replicating from this cluster's master through the parent's replication-source-name and its replicas following it through the child's own (child). Needs cluster-replication on this cluster and the right to create a cluster. The progress is the state CINF0014 on the new cluster, WARN0239 where it stopped.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Parent cluster name"
// @Param request body ChildRequest false "name, dbImage, dbCount, topology (child only), confirm"
// @Success 200 {object} ChildPlan "The plan (confirm false)"
// @Success 202 {object} ChildPlan "Creation started (confirm true)"
// @Failure 400 {string} string "Refused: the reason (no logical backup on the parent, orchestrator, name taken...)"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Failure 500 {string} string "Created and kept, then stopped: the cluster name, the step and the reason (WARN0239)"
// @Router /api/clusters/{clusterName}/actions/create-child [post]
func (repman *ReplicationManager) handlerMuxClusterCreateChild(w http.ResponseWriter, r *http.Request) {
	repman.createChildClusterRoute(w, r, childModeChild)
}

// CreateChildClusterAs is the MCP cluster-create-staging and cluster-create-child tools:
// the route (kind staging or child), called over the loopback with the principal's own
// credential, so its checks (the parent's ACL, the right to create a cluster, the
// self-service rules) apply exactly as over REST.
func (repman *ReplicationManager) CreateChildClusterAs(p *repmanmcp.Principal, parent string, kind string, spec repmanmcp.ChildClusterSpec) (int, string, error) {
	if p == nil || p.Bearer == "" {
		return 0, "", errors.New("no credential on this request to act as you on this instance")
	}
	if kind != "staging" && kind != "child" {
		return 0, "", fmt.Errorf("unknown kind %q", kind)
	}
	sess := &peerSession{callBase: repman.loopbackAPI(), token: p.Bearer}
	status, body, err := sess.call(http.MethodPost, "/api/clusters/"+url.PathEscape(parent)+"/actions/create-"+kind, spec)
	return status, strings.TrimSpace(string(body)), err
}
