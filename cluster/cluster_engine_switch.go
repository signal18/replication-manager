// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// Storage engine switch (#1999): ALTER TABLE ... ENGINE on the master, binlog on so the
// replicas follow, for one table or for every table of one engine. Tables of any other
// engine (Aria, MEMORY, MyISAM...) are never touched. One switch at a time per cluster,
// in the background; its progress is a state (CINF0015), its stop too (WARN0240).

// engineTags names the configurator tag that installs an engine, for the plan's advice.
var engineTags = map[string]string{
	"rocksdb": "rocksdb", "columnstore": "mcs", "s3": "s3", "connect": "connect",
	"archive": "archive", "blackhole": "blackhole", "mroonga": "mroonga", "oqgraph": "oqgraph",
}

// EngineSwitchTable is a table of a switch plan; Reason says why it is skipped.
type EngineSwitchTable struct {
	dbhelper.EngineTable
	Reason string `json:"reason,omitempty"`
}

// EngineSwitchPlan is what a switch will alter, returned before anything is altered.
type EngineSwitchPlan struct {
	Cluster    string              `json:"cluster"`
	Server     string              `json:"server"`
	From       string              `json:"engineFrom"`
	To         string              `json:"engineTo"`
	Schema     string              `json:"schema,omitempty"`
	Tables     []EngineSwitchTable `json:"tables"`
	Skipped    []EngineSwitchTable `json:"skipped,omitempty"`
	TotalBytes int64               `json:"totalBytes"`
	Notes      []string            `json:"notes,omitempty"`
}

// EngineSwitch is the progress of a running or stopped switch.
type EngineSwitch struct {
	From, To string
	Done     int
	Total    int
	Current  string
	Error    string
	Finished bool
}

// PlanEngineSwitch lists the tables a switch would alter on the master: the tables of
// engine from ("" for one table whatever its engine), optionally of one schema and one
// table. The target must be available on the master; a table the target cannot hold
// (a foreign key, only InnoDB enforces them) is skipped with its reason.
func (cluster *Cluster) PlanEngineSwitch(from, to, schema, table string) (EngineSwitchPlan, error) {
	plan := EngineSwitchPlan{Cluster: cluster.Name, From: from, To: to, Schema: schema}
	master := cluster.GetMaster()
	if master == nil || master.Conn == nil {
		return plan, errors.New("no master to alter the tables on")
	}
	plan.Server = master.URL
	if master.DBVersion != nil && master.DBVersion.IsPostgreSQL() {
		return plan, dbhelper.ErrNoStorageEngine
	}
	if to == "" || (from == "" && table == "") {
		return plan, errors.New("engine_to is required, and engine_from unless one table is named")
	}
	if strings.EqualFold(from, to) {
		return plan, fmt.Errorf("engine_from and engine_to are both %s", to)
	}
	support, _, err := dbhelper.GetEngineSupport(master.Conn, master.DBVersion, to)
	if err != nil {
		return plan, err
	}
	if support != "YES" && support != "DEFAULT" {
		hint := ""
		if tag, ok := engineTags[strings.ToLower(to)]; ok {
			hint = fmt.Sprintf(": add the configurator tag %s and restart the databases", tag)
		}
		if support == "" {
			support = "unknown"
		}
		return plan, fmt.Errorf("engine %s is %s on %s%s", to, support, master.URL, hint)
	}
	tables, _, err := dbhelper.GetTablesByEngine(master.Conn, master.DBVersion, from, schema, table)
	if err != nil {
		return plan, err
	}
	if table != "" && len(tables) == 0 {
		return plan, fmt.Errorf("no table %s.%s on %s", schema, table, master.URL)
	}
	for _, t := range tables {
		st := EngineSwitchTable{EngineTable: t}
		switch {
		case strings.EqualFold(t.Engine, to):
			st.Reason = "already " + to
		case t.ForeignKey && !strings.EqualFold(to, "InnoDB"):
			st.Reason = "a foreign key involves it: " + to + " does not enforce foreign keys"
		}
		if st.Reason != "" {
			plan.Skipped = append(plan.Skipped, st)
			continue
		}
		plan.Tables = append(plan.Tables, st)
		plan.TotalBytes += t.Bytes
	}
	if len(plan.Tables) > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("each ALTER copies its table and blocks its writes while it runs; one table may take up to db-servers-read-timeout (%d s)", cluster.Conf.ReadTimeout))
	}
	return plan, nil
}

// RunEngineSwitch alters the planned tables one after the other on a dedicated
// connection to the master, stopping at the first error. Refused while another switch
// runs on the cluster.
func (cluster *Cluster) RunEngineSwitch(plan EngineSwitchPlan) error {
	if len(plan.Tables) == 0 {
		return errors.New("nothing to alter")
	}
	if sw := cluster.engineSwitch.Load(); sw != nil && !sw.Finished {
		return fmt.Errorf("an engine switch %s to %s is running (%d/%d)", sw.From, sw.To, sw.Done, sw.Total)
	}
	master := cluster.GetMaster()
	if master == nil || master.URL != plan.Server {
		return fmt.Errorf("the master is no longer %s: plan again", plan.Server)
	}
	conn, err := master.GetNewDBConn()
	if err != nil {
		return err
	}
	from := plan.From
	if from == "" {
		from = plan.Tables[0].Engine
	}
	progress := EngineSwitch{From: from, To: plan.To, Total: len(plan.Tables)}
	cluster.engineSwitch.Store(&progress)
	go func() {
		defer conn.Close()
		// a panic here must never take replication-manager down
		defer func() {
			if r := recover(); r != nil {
				p := *cluster.engineSwitch.Load()
				p.Error, p.Finished = fmt.Sprintf("panic: %v", r), true
				cluster.engineSwitch.Store(&p)
			}
		}()
		for i, t := range plan.Tables {
			p := progress
			p.Done, p.Current = i, t.Schema+"."+t.Table
			cluster.engineSwitch.Store(&p)
			query, err := dbhelper.AlterTableEngine(conn, master.DBVersion, t.Schema, t.Table, plan.To)
			cluster.LogSQL(query, err, master.URL, "EngineSwitch", config.LvlErr, "Engine switch of %s to %s on %s: %s", p.Current, plan.To, master.URL, err)
			if err != nil {
				p.Error, p.Finished = err.Error(), true
				cluster.engineSwitch.Store(&p)
				return
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Engine switch: %s now %s (%d/%d)", p.Current, plan.To, i+1, len(plan.Tables))
		}
		done := progress
		done.Done, done.Finished = len(plan.Tables), true
		cluster.engineSwitch.Store(&done)
	}()
	return nil
}

// GetEngineSwitch returns the progress of the last switch, nil if none since the start.
func (cluster *Cluster) GetEngineSwitch() *EngineSwitch {
	return cluster.engineSwitch.Load()
}

// checkEngineSwitchStates: CINF0015 while a switch runs, WARN0240 once it stopped on an
// error (until the next switch); nothing once it finished.
func (cluster *Cluster) checkEngineSwitchStates() {
	sw := cluster.engineSwitch.Load()
	switch {
	case sw == nil:
	case sw.Error != "":
		cluster.SetState("WARN0240", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0240"], sw.From, sw.To, cluster.Name, sw.Current, sw.Error, sw.Done, sw.Total), ErrFrom: "SCHEMA"})
	case !sw.Finished:
		cluster.SetState("CINF0015", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0015"], sw.From, sw.To, cluster.Name, sw.Done, sw.Total, sw.Current), ErrFrom: "SCHEMA"})
	}
}
