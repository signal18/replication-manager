// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// ColumnStore table rebalance (#2008): a PM added to a ColumnStore cluster brings an empty
// dbroot, and the existing tables stay on the old ones. A rebalance rebuilds a table so
// that its extents spread over every dbroot: a copy into schema.table__rebalance while a
// table read lock makes the writers wait, then an atomic rename. One table at a time, one
// rebalance at a time per cluster, in the background; its progress is a state (CINF0016),
// its stop too (WARN0241).

const (
	columnstoreRebalanceSuffix = "__rebalance" // the copy being filled
	columnstoreOldSuffix       = "__old"       // the original once swapped, dropped at the end
	// columnstoreQueuedWait: how long the swap may take to queue behind the read lock;
	// the RENAME of a table under a read lock queues at once, unless it failed
	columnstoreQueuedWait = time.Minute
)

// ColumnstoreRebalanceTable is a table of a rebalance plan; Reason says why it is skipped.
type ColumnstoreRebalanceTable struct {
	dbhelper.ColumnstoreTable
	Reason string `json:"reason,omitempty"`
}

// ColumnstoreRebalancePlan is what a rebalance will rebuild, returned before anything is touched.
type ColumnstoreRebalancePlan struct {
	Cluster    string                      `json:"cluster"`
	Server     string                      `json:"server"`
	Schema     string                      `json:"schema,omitempty"`
	DBRoots    []int                       `json:"dbroots"`
	Tables     []ColumnstoreRebalanceTable `json:"tables"`
	Skipped    []ColumnstoreRebalanceTable `json:"skipped,omitempty"`
	TotalBytes int64                       `json:"totalBytes"`
	Notes      []string                    `json:"notes,omitempty"`
}

// ColumnstoreRebalance is the progress of a running or stopped rebalance.
type ColumnstoreRebalance struct {
	Done     int
	Total    int
	Current  string
	Error    string
	Finished bool
}

// PlanColumnstoreRebalance lists the ColumnStore tables (optionally of one schema, one
// table) whose extents are not on every dbroot. The dbroots are those of the UM's
// Columnstore.xml, else the ones holding extents. A table with a leftover __rebalance or
// __old copy, an autoincrement column, or a name that leaves no room for the suffix is
// skipped with its reason.
func (cluster *Cluster) PlanColumnstoreRebalance(schema, table string) (ColumnstoreRebalancePlan, error) {
	plan := ColumnstoreRebalancePlan{Cluster: cluster.Name, Schema: schema}
	if !cluster.HasConfigTopoColumnstore() {
		return plan, fmt.Errorf("a rebalance spreads ColumnStore tables over the PMs: cluster %s is not of topology %s", cluster.Name, config.TopoColumnstore)
	}
	master := cluster.GetMaster()
	if master == nil || master.Conn == nil {
		return plan, errors.New("no UM to rebalance the tables on")
	}
	plan.Server = master.URL
	tables, _, err := dbhelper.GetColumnstoreTableExtents(master.Conn, master.DBVersion, schema, table)
	if err != nil {
		return plan, err
	}
	if table != "" && len(tables) == 0 {
		return plan, fmt.Errorf("no ColumnStore table %s.%s on %s", schema, table, master.URL)
	}
	plan.DBRoots = columnstoreDBRoots(master.Conn, master, tables)
	autoinc, _, err := dbhelper.GetColumnstoreAutoincrementTables(master.Conn, master.DBVersion, schema, table)
	if err != nil {
		return plan, err
	}
	existing := make(map[string]bool, len(tables))
	if table != "" {
		// the copies of one table are not in a one-table listing
		all, _, err := dbhelper.GetColumnstoreTableExtents(master.Conn, master.DBVersion, schema, "")
		if err != nil {
			return plan, err
		}
		for _, t := range all {
			existing[t.Schema+"."+t.Table] = true
		}
	} else {
		for _, t := range tables {
			existing[t.Schema+"."+t.Table] = true
		}
	}
	for _, t := range tables {
		rt := ColumnstoreRebalanceTable{ColumnstoreTable: t}
		missing, extents := 0, 0
		for _, d := range plan.DBRoots {
			if t.Extents[d] == 0 {
				missing++
			}
		}
		for _, n := range t.Extents {
			extents += n
		}
		switch {
		case isColumnstoreRebalanceCopy(t.Table):
			rt.Reason = "a copy left by a rebalance: drop it, or rename it back if the original is gone"
		case existing[t.Schema+"."+t.Table+columnstoreRebalanceSuffix] || existing[t.Schema+"."+t.Table+columnstoreOldSuffix]:
			rt.Reason = "a copy of a previous rebalance is still there (" + t.Table + columnstoreRebalanceSuffix + " or " + t.Table + columnstoreOldSuffix + "): drop it first"
		case autoinc[t.Schema+"."+t.Table]:
			rt.Reason = "an autoincrement column: ColumnStore keeps its next value in its catalog, a copy would restart it and the next insert would reuse an id"
		case len(t.Table)+len(columnstoreRebalanceSuffix) > 64:
			rt.Reason = "the name leaves no room for the copy's suffix " + columnstoreRebalanceSuffix + " (64 characters)"
		case missing == 0:
			rt.Reason = "already on every dbroot"
		case t.Columns > 0 && extents <= t.Columns:
			// one extent per column: a rebuild writes it to one dbroot again (dev3: 3 rows)
			rt.Reason = "too small to spread: one extent per column, a rebuild would put it on one dbroot again"
		}
		if rt.Reason != "" {
			plan.Skipped = append(plan.Skipped, rt)
			continue
		}
		plan.Tables = append(plan.Tables, rt)
		plan.TotalBytes += t.Bytes
	}
	if len(plan.Tables) > 0 {
		plan.Notes = append(plan.Notes,
			"each table is copied whole: the PMs need that much free space while it runs",
			"writes to a table wait while it is copied (a table read lock), reads go on",
		)
	}
	return plan, nil
}

// columnstoreDBRoots: the dbroots of the cluster, from Columnstore.xml (DBRootCount), else
// the ones that hold an extent.
func columnstoreDBRoots(db *sqlx.DB, master *ServerMonitor, tables []dbhelper.ColumnstoreTable) []int {
	var roots []int
	if n, _, err := dbhelper.GetColumnstoreDBRootCount(db, master.DBVersion); err == nil && n > 0 {
		for d := 1; d <= n; d++ {
			roots = append(roots, d)
		}
		return roots
	}
	seen := map[int]bool{}
	for _, t := range tables {
		for d := range t.Extents {
			if !seen[d] {
				seen[d] = true
				roots = append(roots, d)
			}
		}
	}
	sort.Ints(roots)
	return roots
}

func isColumnstoreRebalanceCopy(name string) bool {
	for _, s := range []string{columnstoreRebalanceSuffix, columnstoreOldSuffix} {
		if len(name) > len(s) && name[len(name)-len(s):] == s {
			return true
		}
	}
	return false
}

// RunColumnstoreRebalance rebuilds the planned tables one after the other, stopping at the
// first error. Refused while another rebalance runs on the cluster.
func (cluster *Cluster) RunColumnstoreRebalance(plan ColumnstoreRebalancePlan) error {
	if len(plan.Tables) == 0 {
		return errors.New("nothing to rebalance")
	}
	if rb := cluster.columnstoreRebalance.Load(); rb != nil && !rb.Finished {
		return fmt.Errorf("a ColumnStore rebalance is running (%d/%d, %s)", rb.Done, rb.Total, rb.Current)
	}
	master := cluster.GetMaster()
	if master == nil || master.URL != plan.Server {
		return fmt.Errorf("the UM is no longer %s: plan again", plan.Server)
	}
	db, err := master.GetNewDBConn()
	if err != nil {
		return err
	}
	progress := ColumnstoreRebalance{Total: len(plan.Tables)}
	cluster.columnstoreRebalance.Store(&progress)
	go func() {
		defer db.Close()
		// a panic here must never take replication-manager down
		defer func() {
			if r := recover(); r != nil {
				p := *cluster.columnstoreRebalance.Load()
				p.Error, p.Finished = fmt.Sprintf("panic: %v", r), true
				cluster.columnstoreRebalance.Store(&p)
			}
		}()
		for i, t := range plan.Tables {
			p := progress
			p.Done, p.Current = i, t.Schema+"."+t.Table
			cluster.columnstoreRebalance.Store(&p)
			if err := cluster.rebalanceColumnstoreTable(db, master, t.Schema, t.Table); err != nil {
				p.Error, p.Finished = err.Error(), true
				cluster.columnstoreRebalance.Store(&p)
				return
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ColumnStore rebalance: %s now on every dbroot (%d/%d)", p.Current, i+1, len(plan.Tables))
		}
		done := progress
		done.Done, done.Finished = len(plan.Tables), true
		cluster.columnstoreRebalance.Store(&done)
	}()
	return nil
}

// rebalanceColumnstoreTable rebuilds one table. Session lock holds FLUSH TABLES ... WITH
// READ LOCK (writers wait, readers go on) while session copy fills the copy, then queues
// the RENAME behind the lock; once the RENAME waits for its metadata lock, lock unlocks:
// the exclusive RENAME is granted before the queued writers, which then write into the
// new table. Before the RENAME, any failure drops the copy and leaves the table as it was.
func (cluster *Cluster) rebalanceColumnstoreTable(db *sqlx.DB, master *ServerMonitor, schema, table string) error {
	ctx := context.Background()
	copyName, oldName := table+columnstoreRebalanceSuffix, table+columnstoreOldSuffix
	name := schema + "." + table
	logSQL := func(query string, err error) {
		cluster.LogSQL(query, err, master.URL, "ColumnstoreRebalance", config.LvlErr, "ColumnStore rebalance of %s on %s: %s", name, master.URL, err)
	}
	lockConn, err := db.Connx(ctx)
	if err != nil {
		return err
	}
	defer lockConn.Close()
	copyConn, err := db.Connx(ctx)
	if err != nil {
		return err
	}
	defer copyConn.Close()
	dropCopy := func() {
		q, err := dbhelper.DropTable(ctx, db, master.DBVersion, schema, copyName)
		logSQL(q, err)
	}

	q, err := dbhelper.CreateTableLike(ctx, copyConn, master.DBVersion, schema, table, copyName)
	logSQL(q, err)
	if err != nil {
		return err
	}
	q, err = dbhelper.LockTableForRead(ctx, lockConn, master.DBVersion, schema, table, cluster.Conf.ReadTimeout)
	logSQL(q, err)
	if err != nil {
		dropCopy()
		return fmt.Errorf("read lock on %s: %w", name, err)
	}
	locked := true
	unlock := func() {
		if locked {
			q, err := dbhelper.UnlockTablesConn(ctx, lockConn)
			logSQL(q, err)
			locked = false
		}
	}
	defer unlock()
	q, err = dbhelper.CopyTableRows(ctx, copyConn, master.DBVersion, schema, table, copyName)
	logSQL(q, err)
	if err != nil {
		unlock()
		dropCopy()
		return fmt.Errorf("copy of %s: %w", name, err)
	}
	copyID, q, err := dbhelper.GetConnectionID(ctx, copyConn)
	logSQL(q, err)
	if err != nil {
		unlock()
		dropCopy()
		return err
	}
	swapped := make(chan error, 1)
	go func() {
		q, err := dbhelper.SwapTables(ctx, copyConn, master.DBVersion, schema, table, copyName, oldName)
		logSQL(q, err)
		swapped <- err
	}()
	// unlock only once the RENAME waits for the lock: unlocked earlier, a writer could
	// write into the old table between the unlock and the RENAME
	deadline := time.Now().Add(columnstoreQueuedWait)
	for queued := false; !queued; {
		select {
		case err := <-swapped:
			// the RENAME ended without waiting: it failed (it cannot pass the read lock)
			unlock()
			if err == nil {
				err = errors.New("the swap ran past the read lock")
			}
			dropCopy()
			return fmt.Errorf("swap of %s: %w", name, err)
		case <-time.After(100 * time.Millisecond):
		}
		waiting, q, err := dbhelper.IsWaitingForMetadataLock(db, master.DBVersion, copyID)
		if err != nil {
			logSQL(q, err)
		}
		queued = waiting
		if !queued && time.Now().After(deadline) {
			// still not queued: release the writers, the RENAME then runs or fails on its own
			unlock()
			if err := <-swapped; err != nil {
				dropCopy()
				return fmt.Errorf("swap of %s: %w", name, err)
			}
			return cluster.dropColumnstoreOld(ctx, db, master, schema, oldName, name)
		}
	}
	unlock()
	if err := <-swapped; err != nil {
		dropCopy()
		return fmt.Errorf("swap of %s: %w", name, err)
	}
	return cluster.dropColumnstoreOld(ctx, db, master, schema, oldName, name)
}

// dropColumnstoreOld drops the original once the copy took its name.
func (cluster *Cluster) dropColumnstoreOld(ctx context.Context, db *sqlx.DB, master *ServerMonitor, schema, oldName, name string) error {
	q, err := dbhelper.DropTable(ctx, db, master.DBVersion, schema, oldName)
	cluster.LogSQL(q, err, master.URL, "ColumnstoreRebalance", config.LvlErr, "ColumnStore rebalance of %s on %s: %s", name, master.URL, err)
	if err != nil {
		return fmt.Errorf("%s is rebalanced, but its old copy %s.%s is left: drop it (%w)", name, schema, oldName, err)
	}
	return nil
}

// GetColumnstoreRebalance returns the progress of the last rebalance, nil if none since the start.
func (cluster *Cluster) GetColumnstoreRebalance() *ColumnstoreRebalance {
	return cluster.columnstoreRebalance.Load()
}

// checkColumnstoreRebalanceStates: CINF0016 while a rebalance runs, WARN0241 once it
// stopped on an error (until the next rebalance); nothing once it finished.
func (cluster *Cluster) checkColumnstoreRebalanceStates() {
	rb := cluster.columnstoreRebalance.Load()
	switch {
	case rb == nil:
	case rb.Error != "":
		cluster.SetState("WARN0241", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0241"], cluster.Name, rb.Current, rb.Error, rb.Done, rb.Total), ErrFrom: "SCHEMA"})
	case !rb.Finished:
		cluster.SetState("CINF0016", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0016"], cluster.Name, rb.Done, rb.Total, rb.Current), ErrFrom: "SCHEMA"})
	}
}
