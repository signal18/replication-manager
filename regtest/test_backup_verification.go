// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// verificationWait bounds a backup verification: the restore level loads the whole backup.
const verificationWait = 30 * time.Minute

// TestBackupVerification runs backup verification (#2023) on mysqldump backups of the
// primary, each verified on demand and waited for:
//
//   - V1: a local backup, integrity level: passed;
//   - V2: remote, then local splitdump backups, integrity level: passed;
//   - V3: the restore level on backup-verification-host, for a single-file and a splitdump
//     backup: passed, the test table's rows compared (skipped, and logged, without a
//     backup-verification-host);
//   - V4: backup-verification on: a backup is verified right after it completes;
//   - V5: the last backup cut short (its last byte removed): the integrity level fails and
//     ERR00117 is raised; a new backup clears it;
//   - V6: the event scheduler off, a backup leaves the test event out: its test restore
//     passes, warns that the source has an event the backup lacks and raises WARN0248; a
//     new backup with the scheduler on clears it (skipped without a
//     backup-verification-host);
//   - V7: the replica as backup-verification-host, listed in db-servers-ignored-hosts: the
//     test restore reseeds it from the backup, it replicates again from the backup's position
//     (a write on the primary reaches it, its data matches) and leaves maintenance; with its
//     event scheduler on, the events of the backup are disabled on it (DISABLE ON SLAVE /
//     ON REPLICA) and its scheduler is back on.
//
// The test schema holds a view, a procedure, a function, a trigger and an event besides
// its table: V3 compares them, with the event scheduler on so the dump has the event.
//
// V5 damages the last backup: for a dedicated test cluster only. It needs dbjobs next to
// each database (the remote backup of V2). Run it by name.
func (regtest *RegTest) TestBackupVerification(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	e.name = "backup verification"
	defer saveConf(cl)()
	cl.Conf.BackupVerification = false
	cl.Conf.BackupMysqldumpSplitDump = false
	e.seed()
	e.seedObjects()
	if scheduler := e.m.HasEventScheduler(); !scheduler {
		e.setEventScheduler(true)
		defer e.setEventScheduler(false)
	}

	verify := func(name, level string) *backupmgr.BackupVerification {
		meta := e.m.LastBackupMeta.Logical
		if meta == nil {
			e.check(name, false, "no backup")
			return nil
		}
		if err := cl.VerifyBackupByID(meta.Id, level); err != nil {
			e.check(name, false, err.Error())
			return nil
		}
		v := e.waitVerification(meta)
		if v == nil {
			e.check(name, false, "verification did not end")
		}
		return v
	}
	passed := func(name, level string) *backupmgr.BackupVerification {
		v := verify(name, level)
		if v != nil {
			e.check(name, v.Status == backupmgr.BackupVerificationPassed && v.Level == level, fmt.Sprintf("%s: %s", v.Status, v.Error))
			if len(v.Notes) > 0 {
				e.logf("TEST", "%s: %s", name, strings.Join(v.Notes, "; "))
			}
		}
		return v
	}

	// V1
	if _, ok := e.backup("V1 local backup", false); ok {
		passed("V1 integrity", config.ConstBackupVerificationLevelIntegrity)
	}

	// V2
	if _, ok := e.backup("V2 remote backup", true); ok {
		passed("V2 remote integrity", config.ConstBackupVerificationLevelIntegrity)
	}
	cl.Conf.BackupMysqldumpSplitDump = true
	if _, ok := e.backup("V2 local splitdump backup", false); ok {
		passed("V2 splitdump integrity", config.ConstBackupVerificationLevelIntegrity)
	}

	// V3
	if cl.Conf.BackupVerificationHost == "" {
		e.logf("TEST", "V3 restore level skipped: backup-verification-host is not set")
	} else {
		for _, splitDump := range []bool{false, true} {
			cl.Conf.BackupMysqldumpSplitDump = splitDump
			name := fmt.Sprintf("V3 restore level (splitdump %v)", splitDump)
			if _, ok := e.backup(name+" backup", false); ok {
				if v := passed(name, config.ConstBackupVerificationLevelRestore); v != nil && v.Status == backupmgr.BackupVerificationPassed {
					notes := strings.Join(v.Notes, "; ")
					e.check(name+", rows compared", strings.Contains(notes, "row(s)"), notes)
					others := otherObjectsCompared.FindStringSubmatch(notes)
					n := 0
					if others != nil {
						n, _ = strconv.Atoi(others[1])
					}
					e.check(name+", view, routines, trigger and event compared", n >= 5, notes)
					e.check(name+", nothing of the source missing", len(v.Warnings) == 0, strings.Join(v.Warnings, "; "))
				}
			}
		}
	}
	cl.Conf.BackupMysqldumpSplitDump = false

	// V4
	cl.Conf.BackupVerification = true
	cl.Conf.BackupVerificationLevel = config.ConstBackupVerificationLevelIntegrity
	if _, ok := e.backup("V4 backup with backup-verification on", false); ok {
		v := e.m.GetBackupVerification(e.m.LastBackupMeta.Logical)
		e.check("V4 verified after the backup", v != nil && v.Status == backupmgr.BackupVerificationPassed, fmt.Sprintf("%+v", v))
	}
	cl.Conf.BackupVerification = false

	// V5
	if meta, ok := e.backup("V5 backup", false); ok {
		if err := cutLastByte(meta.Dest); err != nil {
			e.check("V5 cut the backup", false, err.Error())
		} else if v := verify("V5 integrity of a cut backup", config.ConstBackupVerificationLevelIntegrity); v != nil {
			e.check("V5 integrity of a cut backup", v.Status == backupmgr.BackupVerificationFailed, fmt.Sprintf("%s: %s", v.Status, v.Error))
			e.check("V5 ERR00117 raised", e.waitState("ERR00117", true), "not raised")
			if _, ok := e.backup("V5 new backup", false); ok {
				e.check("V5 ERR00117 cleared by a new backup", e.waitState("ERR00117", false), "still raised")
			}
		}
	}

	// V6
	if cl.Conf.BackupVerificationHost == "" {
		e.logf("TEST", "V6 source comparison skipped: backup-verification-host is not set")
	} else {
		e.setEventScheduler(false)
		if _, ok := e.backup("V6 backup with the event scheduler off", false); ok {
			if v := verify("V6 test restore of a backup without its event", config.ConstBackupVerificationLevelRestore); v != nil {
				warnings := strings.Join(v.Warnings, "; ")
				e.check("V6 test restore of a backup without its event", v.Status == backupmgr.BackupVerificationPassed, fmt.Sprintf("%s: %s", v.Status, v.Error))
				e.check("V6 warns that the source has an event the backup lacks", strings.Contains(warnings, "event(s), the backup none"), warnings)
				e.check("V6 WARN0248 raised", e.waitState("WARN0248", true), "not raised")
			}
		}
		e.setEventScheduler(true)
		if _, ok := e.backup("V6 new backup with the event scheduler on", false); ok {
			e.check("V6 WARN0248 cleared by a new backup", e.waitState("WARN0248", false), "still raised")
		}
	}

	// V7
	savedHost, savedIgnored := cl.Conf.BackupVerificationHost, cl.GetIgnoredHostList()
	wantGTID := e.r.HasMySQLGTID() || (e.r.IsMariaDB() && e.r.HasGTIDReplication())
	cl.SetIgnoreSrv(e.r.URL)
	cl.Conf.BackupVerificationHost = e.r.URL
	e.logf("TEST", "V7 backup-verification-host set to the replica %s, listed in db-servers-ignored-hosts (was %q, %q)", e.r.URL, savedHost, savedIgnored)
	replicaScheduler := e.r.HasEventScheduler()
	if _, err := e.r.SetEventScheduler(true); err != nil {
		e.check("V7 event scheduler on, on the replica", false, err.Error())
	}
	if _, ok := e.backup("V7 backup for a test restore on the replica", false); ok {
		name := "V7 test restore on the ignored replica " + e.r.URL
		if v := passed(name, config.ConstBackupVerificationLevelRestore); v != nil && v.Status == backupmgr.BackupVerificationPassed {
			notes := strings.Join(v.Notes, "; ")
			e.check(name+", reseeded from the backup", strings.Contains(notes, "reseeded from the backup"), notes)
		}
		e.check("V7 replica out of maintenance, reseed flag released", !e.r.IsMaintenance && e.r.IsReseeding == "", fmt.Sprintf("maintenance %v, reseeding %q", e.r.IsMaintenance, e.r.IsReseeding))
		if _, err := e.m.Conn.Exec("INSERT INTO " + edgeTable + " VALUES (2000001, 'after the test restore')"); err != nil {
			e.check("V7 write on the primary", false, err.Error())
		}
		e.checkReplica("V7 replica replicating from the backup's position, caught up", wantGTID)
		events, _, err := dbhelper.GetEventStatus(e.r.Conn, e.r.DBVersion)
		enabled := []string{}
		for _, ev := range events {
			if ev.Db == "reseedtest" && ev.Status == 1 {
				enabled = append(enabled, ev.Name)
			}
		}
		e.check("V7 events of the backup disabled on the replica", err == nil && len(events) > 0 && len(enabled) == 0, fmt.Sprintf("enabled %v, err %v", enabled, err))
		state, _, err := dbhelper.GetEventScheduler(e.r.Conn, e.r.DBVersion)
		e.check("V7 event scheduler of the replica back on", err == nil && state == "ON", fmt.Sprintf("%s %v", state, err))
	}
	if _, err := e.r.SetEventScheduler(replicaScheduler); err != nil {
		e.check("V7 event scheduler of the replica restored", false, err.Error())
	}
	cl.Conf.BackupVerificationHost = savedHost
	cl.SetIgnoreSrv(savedIgnored)
	e.logf("TEST", "V7 backup-verification-host back to %q, db-servers-ignored-hosts to %q", savedHost, savedIgnored)
	return e.result()
}

// otherObjectsCompared reads the number of views, routines, triggers and events a test
// restore compared from its notes.
var otherObjectsCompared = regexp.MustCompile(`(\d+) other object\(s\) compared`)

// seedObjects adds a view, a procedure, a function, a trigger and two events to the test
// schema (one disabled, one enabled whose first run is a day away: neither runs).
func (e *edgeRun) seedObjects() {
	for _, q := range []string{
		"CREATE OR REPLACE VIEW reseedtest.edge_view AS SELECT id FROM " + edgeTable,
		"DROP PROCEDURE IF EXISTS reseedtest.edge_proc",
		"CREATE PROCEDURE reseedtest.edge_proc() SELECT COUNT(*) FROM " + edgeTable,
		"DROP FUNCTION IF EXISTS reseedtest.edge_func",
		"CREATE FUNCTION reseedtest.edge_func() RETURNS INT DETERMINISTIC NO SQL RETURN 1",
		"CREATE TRIGGER reseedtest.edge_trg BEFORE INSERT ON " + edgeTable + " FOR EACH ROW SET NEW.v = NEW.v",
		"DROP EVENT IF EXISTS reseedtest.edge_event",
		"CREATE EVENT reseedtest.edge_event ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1",
		// enabled, its first run a day away: V7 checks it is disabled on the replica
		"DROP EVENT IF EXISTS reseedtest.edge_event_on",
		"CREATE EVENT reseedtest.edge_event_on ON SCHEDULE EVERY 1 DAY STARTS CURRENT_TIMESTAMP + INTERVAL 1 DAY ENABLE DO SELECT 1",
	} {
		if _, err := e.m.Conn.Exec(q); err != nil {
			e.check("seed objects", false, fmt.Sprintf("%s: %s", q, err))
			return
		}
	}
}

// setEventScheduler turns the primary's event scheduler on or off and waits up to a minute
// for the monitor to see it: the dump reads it to keep or leave out the events.
func (e *edgeRun) setEventScheduler(on bool) {
	if _, err := e.m.SetEventScheduler(on); err != nil {
		e.check(fmt.Sprintf("event scheduler %v", on), false, err.Error())
		return
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if e.m.HasEventScheduler() == on {
			return
		}
		time.Sleep(2 * time.Second)
	}
	e.check(fmt.Sprintf("event scheduler %v seen by the monitor", on), false, "not seen")
}

// waitVerification waits for the verification of meta to end, and returns its result.
func (e *edgeRun) waitVerification(meta *backupmgr.BackupMetadata) *backupmgr.BackupVerification {
	deadline := time.Now().Add(verificationWait)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if v := e.m.GetBackupVerification(meta); v != nil && v.Status != backupmgr.BackupVerificationRunning {
			return v
		}
	}
	return nil
}

// waitState waits up to a minute for a state of the primary to be raised (or cleared).
func (e *edgeRun) waitState(key string, raised bool) bool {
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if e.cl.IsInErrorState(key, e.m.URL) == raised {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// cutLastByte removes the last byte of a backup file.
func cutLastByte(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Truncate(path, info.Size()-1)
}
