// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

const engineRegtestSchema = "regtest_engine"

// TestAlterEngine (#1999), on a child cluster of this cluster (#1997): a test schema with
// two InnoDB tables linked by a foreign key, one InnoDB table alone and one MEMORY table.
// The switch InnoDB to Aria on that schema must plan the lone table only, skip the two
// with the foreign key with their reason, leave the MEMORY table out; run, the lone table
// is Aria on the child's master and replica, the others keep their engine, and the
// child's channel from this cluster keeps running. The child is deleted at the end.
func (regtest *RegTest) TestAlterEngine(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	run, skipped, ok := createCheckedChild(cl, "child", config.GrantClusterReplication, map[string]int{"Master": 1, "Slave": 1})
	if run.created {
		defer stagingDelete(cl, run.tok, run.name)
	}
	if skipped {
		return true
	}
	if !ok {
		tlsLogf(cl, "FAIL AlterEngine: the child cluster could not be created")
		return false
	}
	var child *cluster.Cluster
	if err := tlsWait(time.Minute, func() (err error) { child, err = cl.GetClusterFromName(run.name); return err }); err != nil || child.GetMaster() == nil || len(child.GetSlaves()) == 0 {
		tlsLogf(cl, "FAIL AlterEngine: child %s not reachable: %v", run.name, err)
		return false
	}
	master, replica := child.GetMaster(), child.GetSlaves()[0]
	if err := tlsExecOneConn(master,
		"CREATE DATABASE IF NOT EXISTS "+engineRegtestSchema,
		"CREATE TABLE "+engineRegtestSchema+".parent (id INT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE "+engineRegtestSchema+".child (id INT PRIMARY KEY, pid INT, FOREIGN KEY (pid) REFERENCES "+engineRegtestSchema+".parent(id)) ENGINE=InnoDB",
		"CREATE TABLE "+engineRegtestSchema+".lone (id INT PRIMARY KEY, v VARCHAR(16)) ENGINE=InnoDB",
		"INSERT INTO "+engineRegtestSchema+".lone VALUES (1,'a'),(2,'b')",
		"CREATE TABLE "+engineRegtestSchema+".mem (id INT PRIMARY KEY) ENGINE=MEMORY"); err != nil {
		tlsLogf(cl, "FAIL AlterEngine: test schema: %s", err)
		return false
	}

	tok, err := APIToken(child, config.GrantDBOptimize)
	if err != nil {
		tlsLogf(cl, "FAIL AlterEngine: %s", err)
		return false
	}
	path := "/api/clusters/" + run.name + "/actions/alter-engine"
	body, _ := json.Marshal(map[string]any{"engineFrom": "InnoDB", "engineTo": "Aria", "schema": engineRegtestSchema})
	code, _, resp, err := APIRequestBody(tok, http.MethodPost, path, body)
	var plan cluster.EngineSwitchPlan
	if err != nil || code != http.StatusOK || json.Unmarshal(resp, &plan) != nil {
		tlsLogf(cl, "FAIL AlterEngine: plan HTTP %d %v %s", code, err, resp)
		return false
	}
	skippedFK := 0
	for _, t := range plan.Skipped {
		if t.ForeignKey && strings.Contains(t.Reason, "foreign key") {
			skippedFK++
		}
	}
	if len(plan.Tables) != 1 || plan.Tables[0].Table != "lone" || skippedFK != 2 {
		tlsLogf(cl, "FAIL AlterEngine: plan tables %v skipped %v, want lone only and parent, child skipped for their foreign key", plan.Tables, plan.Skipped)
		return false
	}

	body, _ = json.Marshal(map[string]any{"engineFrom": "InnoDB", "engineTo": "Aria", "schema": engineRegtestSchema, "confirm": true})
	if code, _, resp, err = APIRequestBody(tok, http.MethodPost, path, body); err != nil || code != http.StatusAccepted {
		tlsLogf(cl, "FAIL AlterEngine: run HTTP %d %v %s", code, err, resp)
		return false
	}
	// lone becomes Aria on the master and, through the binlog, on the replica; the
	// others keep their engine.
	want := map[string]string{"parent": "InnoDB", "child": "InnoDB", "lone": "Aria", "mem": "MEMORY"}
	for _, srv := range []*cluster.ServerMonitor{master, replica} {
		if err := tlsWait(3*time.Minute, func() error {
			if sw := child.GetEngineSwitch(); sw != nil && sw.Error != "" {
				return stagingStop{sw.Error}
			}
			tables, _, err := dbhelper.GetTablesByEngine(srv.Conn, srv.DBVersion, "", engineRegtestSchema, "")
			if err != nil {
				return err
			}
			got := map[string]string{}
			for _, t := range tables {
				got[t.Table] = t.Engine
			}
			for tbl, eng := range want {
				if !strings.EqualFold(got[tbl], eng) {
					return fmt.Errorf("%s: engines %v, want %v", srv.URL, got, want)
				}
			}
			return nil
		}); err != nil {
			tlsLogf(cl, "FAIL AlterEngine: %s", err)
			return false
		}
	}
	// The child master's channel from this cluster still runs.
	running := false
	for _, r := range master.Replications {
		if r.ConnectionName.String == cl.Conf.MasterConn && r.SlaveIORunning.String == "Yes" && r.SlaveSQLRunning.String == "Yes" {
			running = true
		}
	}
	if !running {
		tlsLogf(cl, "FAIL AlterEngine: the channel %q of %s from %s is not running", cl.Conf.MasterConn, master.URL, cl.Name)
		return false
	}
	tlsLogf(cl, "PASS AlterEngine: on %s InnoDB to Aria switched lone only (master and replica), parent and child skipped for their foreign key, mem left MEMORY, channel %q from %s running", run.name, cl.Conf.MasterConn, cl.Name)
	return true
}
