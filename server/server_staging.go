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
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// Staging creation (#1988): a staging cluster of a parent in one call. The staging
// cluster lives on the parent's replication-manager (its seed reads the parent's
// backup locally), is provisioned on the parent's orchestrator, and is seeded with the
// parent's data only: it keeps its own accounts and system tables.

const (
	stagingDefaultDBCount = 3 // the built-in refresh detaches the first of 2 replicas
	stagingMaxDBCount     = 5
	stagingMasterWait     = 5 * time.Minute
	stagingNodesWait      = 2 * time.Minute // the new cluster loads its orchestrator node list on its own tick
)

// StagingRequest is the body of POST /api/clusters/{parent}/actions/create-staging,
// defined once with the MCP tool's spec.
type StagingRequest = repmanmcp.StagingSpec

// StagingPlan is what create-staging will build, returned before anything is created.
type StagingPlan struct {
	Parent       string   `json:"parent"`
	Name         string   `json:"name"`
	Orchestrator string   `json:"orchestrator"`
	DBImage      string   `json:"dbImage"`
	DBCount      int      `json:"dbCount"`
	DBUPerNode   int      `json:"dbuPerNode"`
	Proxy        string   `json:"proxy"` // "" = none
	Backup       string   `json:"backup"`
	Notes        []string `json:"notes,omitempty"`
}

// stagingProxyType is the parent's first provisionable proxy type, "" when it has none.
func stagingProxyType(parent *cluster.Cluster) string {
	for _, prx := range parent.GetProxies() {
		switch t := prx.GetType(); t {
		case config.ConstProxyHaproxy, config.ConstProxySqlproxy, config.ConstProxyMaxscale:
			return t
		}
	}
	return ""
}

// stagingProxyCredential is the parent's admin credential for a proxy type, as the
// credential setting takes it (user:password), "" for a type without one.
func stagingProxyCredential(parent *cluster.Cluster, proxyType string) (string, string) {
	switch proxyType {
	case config.ConstProxySqlproxy:
		return "proxysql-servers-credential", parent.Conf.ProxysqlUser + ":" + parent.Conf.Secrets["proxysql-password"].Value
	case config.ConstProxyMaxscale:
		return "maxscale-servers-credential", parent.Conf.MxsUser + ":" + parent.Conf.Secrets["maxscale-pass"].Value
	}
	return "", ""
}

// PlanStagingCluster checks a create-staging request against the parent and returns
// what would be created. The first check is the parent's logical backup: without it
// nothing can be seeded, so nothing is created.
func (repman *ReplicationManager) PlanStagingCluster(parent *cluster.Cluster, req StagingRequest) (StagingPlan, error) {
	plan := StagingPlan{Parent: parent.Name}
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
		return plan, fmt.Errorf("%s runs on %s: create-staging provisions new servers, only OpenSVC and Kubernetes clusters can", parent.Name, plan.Orchestrator)
	}
	name := req.Name
	if name == "" {
		name = parent.Name + "-staging"
	}
	if plan.Name, err = plainClusterName(name); err != nil {
		return plan, err
	}
	if repman.getClusterByName(plan.Name) != nil {
		return plan, fmt.Errorf("cluster %s already exists", plan.Name)
	}
	plan.DBCount = req.DBCount
	if plan.DBCount == 0 {
		plan.DBCount = stagingDefaultDBCount
	}
	if plan.DBCount < 1 || plan.DBCount > stagingMaxDBCount {
		return plan, fmt.Errorf("dbCount must be between 1 and %d", stagingMaxDBCount)
	}
	if plan.DBCount != stagingDefaultDBCount {
		plan.Notes = append(plan.Notes, fmt.Sprintf("the built-in staging refresh needs %d databases (a master and 2 replicas): with %d, set topology-staging-refresh-script", stagingDefaultDBCount, plan.DBCount))
	}
	plan.DBImage = req.DBImage
	if plan.DBImage == "" {
		plan.DBImage = parent.Conf.ProvDbImg
	}
	plan.DBUPerNode = parent.Conf.ProvDbDbu
	plan.Proxy = stagingProxyType(parent)
	return plan, nil
}

// CreateStagingCluster creates the planned cluster for a creator already authorised
// (clusterAddAuthorize), configures it and adds its servers, then provisions and seeds
// it in the background. Every step is recorded on the new cluster (CINF0014 while it
// runs, WARN0238 where it stopped); a cluster left half-built is kept, never dropped
// behind the creator's back.
func (repman *ReplicationManager) CreateStagingCluster(parent *cluster.Cluster, plan StagingPlan, username string, sso bool, selfService bool, remoteAddr string) (*cluster.Cluster, error) {
	cl, err := repman.createCluster(plan.Name, cluster.ClusterForm{ClusterName: plan.Name, Orchestrator: plan.Orchestrator}, username, sso, selfService, remoteAddr)
	if err != nil {
		return nil, err
	}
	stepName := ""
	step := func(s string) {
		stepName = s
		cl.SetStagingCreation(cluster.StagingCreation{Parent: parent.Name, Step: s})
	}
	fail := func(err error) error {
		cl.SetStagingCreation(cluster.StagingCreation{Parent: parent.Name, Step: stepName, Error: err.Error()})
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Staging cluster %s from %s stopped at %s: %s", cl.Name, parent.Name, stepName, err)
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
	if err := repman.SetClusterSetting(cl, "topology-staging", "true"); err != nil {
		return cl, fail(err)
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
		// The parent's proxy admin credential, through the setting the GUI's Set
		// Credentials uses: the default ProxySQL user admin only connects locally, so a
		// staging proxy rendered with it could never be monitored (dev3, ERR00051).
		if key, cred := stagingProxyCredential(parent, plan.Proxy); key != "" {
			if err := repman.SetClusterSetting(cl, key, cred); err != nil {
				return cl, fail(err)
			}
		}
		if err := cl.AddSeededProxy(plan.Proxy, plan.Proxy+"1", "3306", "", ""); err != nil {
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

	go func() {
		step("orchestrator nodes")
		if err := cl.WaitOrchestratorNodes(stagingNodesWait); err != nil {
			fail(err)
			return
		}
		step("provision")
		if err := cl.Bootstrap(); err != nil {
			fail(err)
			return
		}
		step("master")
		deadline := time.Now().Add(stagingMasterWait)
		for cl.GetMaster() == nil {
			if time.Now().After(deadline) {
				fail(fmt.Errorf("no master after %s", stagingMasterWait))
				return
			}
			time.Sleep(time.Second)
		}
		step("seed from " + parent.Name)
		if err := cl.SeedFromParent(parent); err != nil {
			fail(err)
			return
		}
		cl.SetStagingCreation(cluster.StagingCreation{Parent: parent.Name, Step: "done", Done: true})
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Staging cluster %s created from %s", cl.Name, parent.Name)
	}()
	return cl, nil
}

// handlerMuxClusterCreateStaging creates a staging cluster of a cluster (#1988).
//
// @Summary Create a staging cluster of this cluster
// @Description Plans (confirm false) or creates (confirm true) a staging cluster of {clusterName}, named <clusterName>-staging unless a name is given: same orchestrator, database image unless dbImage is given, DBU per node and proxy type, dbCount databases (default 3), topology-staging on, this cluster as head cluster; provisioned, then seeded from this cluster's logical backup, data only (the staging cluster keeps its own accounts). Needs cluster-staging on this cluster and the right to create a cluster. The progress is the state CINF0014 on the new cluster, WARN0238 where it stopped.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Parent cluster name"
// @Param request body StagingRequest false "name, dbImage, dbCount, confirm"
// @Success 200 {object} StagingPlan "The plan (confirm false)"
// @Success 202 {object} StagingPlan "Creation started (confirm true)"
// @Failure 400 {string} string "Refused: the reason (no logical backup on the parent, orchestrator, name taken...)"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/create-staging [post]
func (repman *ReplicationManager) handlerMuxClusterCreateStaging(w http.ResponseWriter, r *http.Request) {
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
	var req StagingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	plan, err := repman.PlanStagingCluster(parent, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !req.Confirm {
		repman.jsonResponse(plan, w)
		return
	}
	if _, err := repman.CreateStagingCluster(parent, plan, username, sso, selfService, r.RemoteAddr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(plan)
}

// CreateStaging is the MCP cluster-create-staging tool: the route, called over the
// loopback with the principal's own credential, so its checks (the parent's ACL, the
// right to create a cluster, the self-service rules) apply exactly as over REST.
func (repman *ReplicationManager) CreateStaging(p *repmanmcp.Principal, parent string, spec repmanmcp.StagingSpec) (int, string, error) {
	if p == nil || p.Bearer == "" {
		return 0, "", errors.New("no credential on this request to act as you on this instance")
	}
	sess := &peerSession{callBase: repman.loopbackAPI(), token: p.Bearer}
	status, body, err := sess.call(http.MethodPost, "/api/clusters/"+url.PathEscape(parent)+"/actions/create-staging", spec)
	return status, strings.TrimSpace(string(body)), err
}
