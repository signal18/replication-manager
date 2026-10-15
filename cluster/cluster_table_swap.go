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
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// A table rebuilt as a copy then swapped in by one RENAME, while its writers wait, where
// ALTER TABLE cannot do it: the ColumnStore rebalance (#2008), ColumnStore refusing a
// same-engine ALTER. Shared so that another rebuild of a live table can use it.

// tableSwap describes one rebuild: the copy's and the original's names after the swap,
// how the copy is created and filled.
type tableSwap struct {
	module   string // log module of its SQL
	label    string // in its messages: "ColumnStore rebalance"
	copyName string // the copy being filled, schema.copyName
	oldName  string // the original once swapped
	create   func(ctx context.Context, conn *sqlx.Conn) (string, error)
	fill     func(ctx context.Context, conn *sqlx.Conn) (string, error)
	dropOld  bool // drop the original after the swap, else keep it as oldName
}

// swapTableCopy rebuilds schema.table. Session lock holds FLUSH TABLES ... WITH READ LOCK
// (writers wait, readers go on) while session copy creates and fills the copy, then queues
// the RENAME behind the lock; once the RENAME waits for its metadata lock, lock unlocks:
// the exclusive RENAME is granted before the queued writers, which then write into the
// new table. Before the RENAME, any failure drops the copy and leaves the table as it was.
func (cluster *Cluster) swapTableCopy(db *sqlx.DB, master *ServerMonitor, schema, table string, s tableSwap) error {
	ctx := context.Background()
	name := schema + "." + table
	logSQL := func(query string, err error) {
		cluster.LogSQL(query, err, master.URL, s.module, config.LvlErr, "%s of %s on %s: %s", s.label, name, master.URL, err)
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
		q, err := dbhelper.DropTable(ctx, db, master.DBVersion, schema, s.copyName)
		logSQL(q, err)
	}

	q, err := s.create(ctx, copyConn)
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
	q, err = s.fill(ctx, copyConn)
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
		q, err := dbhelper.SwapTables(ctx, copyConn, master.DBVersion, schema, table, s.copyName, s.oldName)
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
			break
		}
	}
	unlock()
	if err := <-swapped; err != nil {
		dropCopy()
		return fmt.Errorf("swap of %s: %w", name, err)
	}
	if !s.dropOld {
		return nil
	}
	q, err = dbhelper.DropTable(ctx, db, master.DBVersion, schema, s.oldName)
	logSQL(q, err)
	if err != nil {
		return fmt.Errorf("%s is rebuilt, but its old copy %s.%s is left: drop it (%w)", name, schema, s.oldName, err)
	}
	return nil
}
