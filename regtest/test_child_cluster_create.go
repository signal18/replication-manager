// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

const (
	stagingRegtestCreate  = 20 * time.Minute
	stagingRegtestBackup  = 10 * time.Minute
	stagingRegtestDeleted = 5 * time.Minute
)

// TestCreateStaging (#1988) creates a staging cluster of this cluster through the real
// route and checks what the staging doc asks: on the staging cluster one Master, one
// Slave and one StandAlone (the detached staging server); the staging master
// replicating from this cluster's master through the channel "staging", IO and SQL
// running; the GTID domain this cluster's + 1; the proxy monitored. The staging cluster
// is deleted at the end, whatever the outcome.
func (regtest *RegTest) TestCreateStaging(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return testCreateChildCluster(cl, "staging", config.GrantClusterStaging, map[string]int{"Master": 1, "Slave": 1, "StandAlone": 1})
}

// TestCreateChild (#1997): the same with a child cluster, which detaches nothing: one
// Master and one Slave, the master replicating from this cluster's master through the
// channel "child".
func (regtest *RegTest) TestCreateChild(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return testCreateChildCluster(cl, "child", config.GrantClusterReplication, map[string]int{"Master": 1, "Slave": 1})
}

func testCreateChildCluster(cl *cluster.Cluster, kind string, grant string, wantStates map[string]int) bool {
	orch := cl.GetOrchestrator()
	if orch != config.ConstOrchestratorOpenSVC && orch != config.ConstOrchestratorKubernetes || cl.Conf.TopologyStaging || cl.GetMaster() == nil {
		tlsLogf(cl, "Skipping Create "+kind+": needs a provisioned (OpenSVC or Kubernetes) cluster with a master, not itself staging")
		return true
	}
	if APIToken == nil || APIRequest == nil || APIRequestBody == nil {
		tlsLogf(cl, "FAIL Create "+kind+": API hooks not wired")
		return false
	}
	tok, err := APIToken(cl, grant, config.GrantClusterCreate)
	if err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": %s", err)
		return false
	}
	name := cl.Name + "-" + kind + "-regtest"
	parentMaster := cl.GetMaster().Host

	// The parent's logical backup is the seed: take one when there is none.
	if _, _, err := cl.ParentLogicalBackup(); err != nil {
		tlsLogf(cl, "Create %s: %s, taking a logical backup", kind, err)
		go cl.GetMaster().JobBackupLogical(context.Background())
		if err := tlsWait(stagingRegtestBackup, func() error { _, _, err := cl.ParentLogicalBackup(); return err }); err != nil {
			tlsLogf(cl, "FAIL Create "+kind+": no logical backup on %s: %s", cl.Name, err)
			return false
		}
	}

	// The plan: domain and channel name.
	var plan struct {
		Name, Domain, SourceName string
	}
	if err := stagingPost(tok, cl.Name, kind, map[string]any{"name": name}, http.StatusOK, &plan); err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": plan: %s", err)
		return false
	}
	parentDomain, _ := strconv.Atoi(cl.Configurator.GetConfigReplicationDomain(cl.Name))
	if plan.Domain != strconv.Itoa(parentDomain+1) || plan.SourceName != kind {
		tlsLogf(cl, "FAIL Create "+kind+": plan domain %q (parent %d) source %q", plan.Domain, parentDomain, plan.SourceName)
		return false
	}

	if err := stagingPost(tok, cl.Name, kind, map[string]any{"name": name, "confirm": true}, http.StatusAccepted, nil); err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": create: %s", err)
		return false
	}
	defer stagingDelete(cl, tok, name)

	base := "/api/clusters/" + name
	// The expected topology; a stop (WARN0238) ends the wait with its reason.
	if err := stagingWait(stagingRegtestCreate, func() error {
		if desc := stagingAlert(tok, base, "WARN0238"); desc != "" {
			return stagingStop{desc}
		}
		var servers []map[string]any
		if err := stagingGet(tok, base+"/topology/servers", &servers); err != nil {
			return err
		}
		count := map[string]int{}
		for _, s := range servers {
			count[stagingString(s["state"])]++
		}
		total := 0
		for st, n := range wantStates {
			total += n
			if count[st] != n {
				return fmt.Errorf("servers %v, want %v", count, wantStates)
			}
		}
		if len(servers) != total {
			return fmt.Errorf("servers %v, want %v", count, wantStates)
		}
		return nil
	}); err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": topology of %s: %s", name, err)
		return false
	}

	// The new master replicates from this cluster's master through the channel named kind.
	if err := tlsWait(2*time.Minute, func() error {
		var master struct {
			Replications []map[string]any `json:"replications"`
		}
		if err := stagingGet(tok, base+"/topology/master", &master); err != nil {
			return err
		}
		for _, r := range master.Replications {
			if stagingString(r["connectionName"]) == kind && stagingString(r["masterHost"]) == parentMaster &&
				stagingString(r["slaveIoRunning"]) == "Yes" && stagingString(r["slaveSqlRunning"]) == "Yes" {
				return nil
			}
		}
		return fmt.Errorf("no running channel %s from %s: %v", kind, parentMaster, master.Replications)
	}); err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": %s", err)
		return false
	}

	// The proxy is monitored (the staging proxy logs in with the parent's user).
	if err := tlsWait(3*time.Minute, func() error {
		var proxies []map[string]any
		if err := stagingGet(tok, base+"/topology/proxies", &proxies); err != nil {
			return err
		}
		for _, p := range proxies {
			if st := stagingString(p["state"]); st != "ProxyRunning" {
				return fmt.Errorf("proxy %s is %s", stagingString(p["host"]), st)
			}
		}
		return nil
	}); err != nil {
		tlsLogf(cl, "FAIL Create "+kind+": %s", err)
		return false
	}
	tlsLogf(cl, "PASS Create %s: %s from %s: %v, channel %s from %s running, domain %s, proxy running", kind, name, cl.Name, wantStates, kind, parentMaster, plan.Domain)
	return true
}

type stagingStop struct{ desc string }

// stagingWait is tlsWait ending at once on a reported stop (stagingStop), returned.
func stagingWait(timeout time.Duration, cond func() error) error {
	var stop error
	err := tlsWait(timeout, func() error {
		err := cond()
		var s stagingStop
		if errors.As(err, &s) {
			stop = err
			return nil
		}
		return err
	})
	if stop != nil {
		return stop
	}
	return err
}

func (s stagingStop) Error() string { return "creation stopped: " + s.desc }

// stagingString reads a JSON value that is a string or a sql.NullString ({String, Valid}).
func stagingString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		s, _ := x["String"].(string)
		return s
	}
	return ""
}

func stagingPost(tok, parent, kind string, body map[string]any, want int, out any) error {
	b, _ := json.Marshal(body)
	code, _, resp, err := APIRequestBody(tok, http.MethodPost, "/api/clusters/"+parent+"/actions/create-"+kind, b)
	if err != nil {
		return err
	}
	if code != want {
		return fmt.Errorf("HTTP %d: %s", code, resp)
	}
	if out != nil {
		return json.Unmarshal(resp, out)
	}
	return nil
}

func stagingGet(tok, path string, out any) error {
	code, _, resp, err := APIRequest(tok, http.MethodGet, path)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", path, code)
	}
	return json.Unmarshal(resp, out)
}

// stagingAlert returns the description of an open state of the cluster, "" when none.
func stagingAlert(tok, base, key string) string {
	var alerts map[string][]map[string]any
	if stagingGet(tok, base+"/topology/alerts", &alerts) != nil {
		return ""
	}
	for _, list := range alerts {
		for _, a := range list {
			if stagingString(a["number"]) == key {
				return stagingString(a["desc"])
			}
		}
	}
	return ""
}

// stagingDelete drops the staging cluster (unprovision included) and waits until it is
// gone, logging how long it took (#1994: seconds, not the 4 minutes of a stuck wait).
func stagingDelete(cl *cluster.Cluster, tok, name string) {
	start := time.Now()
	if _, _, _, err := APIRequest(tok, http.MethodPost, "/api/clusters/actions/delete/"+name); err != nil {
		tlsLogf(cl, "Create cleanup: delete %s: %s (waiting for it anyway)", name, err)
	}
	err := tlsWait(stagingRegtestDeleted, func() error {
		var clusters []map[string]any
		if err := stagingGet(tok, "/api/clusters", &clusters); err != nil {
			return err
		}
		for _, c := range clusters {
			if stagingString(c["name"]) == name {
				return fmt.Errorf("%s still listed", name)
			}
		}
		return nil
	})
	tlsLogf(cl, "Create cleanup: %s deleted in %s (%v)", name, time.Since(start).Round(time.Second), err)
}
