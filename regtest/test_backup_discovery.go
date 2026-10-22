// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// TestBackupDiscovery checks the discovery of backups placed in a backup folder by hand
// (#2051) and their registration on demand (#2073) on real servers. For a single mysqldump
// file and a splitdump folder, and a mydumper folder when mydumper is installed:
//
//   - D1: a backup of the primary is copied in its backup folder under another name, as an
//     operator would, its times set back past the settle window of the discovery;
//   - D2: the discovery lists the copy as a candidate, a backup, and registers nothing;
//   - D3: Register reads it and registers it: discovered, completed, Full, with the position
//     and the GTID set the original backup recorded and its fingerprint;
//   - D4: the replica is reseeded from the copy on request (ReseedFromBackupID), checked as
//     TestLogicalReseed checks a reseed (rows and checksum, replication threads, GTID mode,
//     transactions after it). Skipped for mydumper without myloader;
//   - D5 (mysqldump): the copy is edited by hand (a marker schema added): WARN0249 is raised,
//     the reseed is refused; Register of the change clears it and the reseed loads the edited
//     dump (the marker is on the replica, removed there afterwards, unlogged);
//   - D6 (mysqldump): the copy is truncated: WARN0249, Verify on demand fails, Register of the
//     change is refused;
//   - D7: Unregister removes the registration only: the file stays, the state clears, the
//     copy is a candidate again; the test removes its copy;
//   - D8 (splitdump): a copy whose metadata holds no position is registered with its note,
//     its reseed refused: the replica would start at the primary's current position;
//   - D9: an accounts file (GRANT lines only) is listed as not a backup and Register refuses it;
//   - D10: the record a 3.1.44 discovery made of the scheduled mysqldump.sql.gz is removed,
//     the file kept;
//   - D11: the scheduled mysqldump.sql.gz replaced by hand is locked (WARN0249, reseed
//     refused) until registered, then the automatic logical reseed uses it.
//
// It reseeds the cluster's first replica and needs backup-logical-type mysqldump. Run it by
// name: /api/clusters/<cluster>/tests/actions/run/testBackupDiscovery
func (regtest *RegTest) TestBackupDiscovery(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	defer saveConf(cl)()
	logicalType := cl.Conf.BackupLogicalType
	defer func() { cl.Conf.BackupLogicalType = logicalType }()
	// its reseeds test the registration, not the proof a registered backup's reseed requires
	// by default (#2108, testBackupRegisterArchive)
	proven := cl.Conf.BackupVerificationRegisteredProven
	cl.Conf.BackupVerificationRegisteredProven = false
	defer func() { cl.Conf.BackupVerificationRegisteredProven = proven }()
	cl.Conf.SchedulerJobsExecRemote = ""
	cl.Conf.ParseJobsExecOverrides()
	gtid := e.r.HasGTIDReplication()

	e.checkNotABackup()
	for _, kind := range []struct {
		name, tool, copyName string
		split                bool
	}{
		{"mysqldump", config.ConstBackupLogicalTypeMysqldump, "discovery-copy.sql.gz", false},
		{"splitdump", config.ConstBackupLogicalTypeMysqldump, "discovery-copy-split", true},
		{"mydumper", config.ConstBackupLogicalTypeMydumper, "mydumper-discovery-copy", false},
	} {
		if kind.tool == config.ConstBackupLogicalTypeMydumper && !toolInstalled(cl.GetMyDumperPath()) {
			e.logf("TEST", "%s skipped: mydumper is not installed", kind.name)
			continue
		}
		cl.Conf.BackupLogicalType = kind.tool
		cl.Conf.BackupMysqldumpSplitDump = kind.split
		e.seed()
		if err := e.m.JobBackupLogical(context.Background()); err != nil {
			e.check("D1 "+kind.name+" backup", false, err.Error())
			continue
		}
		orig := e.m.LastBackupMeta.Logical
		if orig == nil || !orig.Completed {
			e.check("D1 "+kind.name+" backup", false, fmt.Sprintf("metadata %+v", orig))
			continue
		}
		copyPath := filepath.Join(e.m.GetMyBackupDirectory(), kind.copyName)
		if err := copyBackupArtifact(orig.Dest, copyPath, time.Now().Add(-2*time.Hour)); err != nil {
			e.check("D1 "+kind.name+" copy", false, err.Error())
			continue
		}
		e.check("D1 "+kind.name+" copy", true, "")

		c := e.discoverCandidate(copyPath)
		if !e.check("D2 "+kind.name+" listed, not registered", c != nil && c.Registerable() && e.registered(copyPath) == nil, fmt.Sprintf("candidate %+v, registered %v", c, e.registered(copyPath) != nil)) {
			os.RemoveAll(copyPath)
			continue
		}
		meta, err := e.register(copyPath)
		switch {
		case err != nil:
			e.check("D3 "+kind.name+" registered", false, err.Error())
		case !meta.Discovered || !meta.Completed || meta.SplitDump != kind.split || meta.BackupTool != kind.tool || meta.BackupStrategy != backupmgr.BackupStrategyFull || meta.Registration == nil:
			e.check("D3 "+kind.name+" registered", false, fmt.Sprintf("metadata %+v", meta))
		// the GTID set compared without its spaces: the backup records it with ", " between
		// its sources, a dump writes them on separate lines
		case (meta.BinLogFileName != orig.BinLogFileName || meta.BinLogFilePos != orig.BinLogFilePos || strings.Join(strings.Fields(meta.BinLogGtid), "") != strings.Join(strings.Fields(orig.BinLogGtid), "")):
			e.check("D3 "+kind.name+" registered", false, fmt.Sprintf("position %s:%d %q, the backup recorded %s:%d %q", meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid, orig.BinLogFileName, orig.BinLogFilePos, orig.BinLogGtid))
		default:
			e.check("D3 "+kind.name+" registered", true, "")
		}
		if meta == nil {
			os.RemoveAll(copyPath)
			continue
		}

		if kind.tool == config.ConstBackupLogicalTypeMydumper && !toolInstalled(cl.GetMyLoaderPath()) {
			e.logf("TEST", "D4 %s reseed skipped: myloader is not installed", kind.name)
		} else {
			id := meta.Id
			e.reseedWithAndCheck("D4 "+kind.name+" reseed from the registered copy", gtid, kind.tool, func() error {
				return cl.ReseedFromBackupID(e.r, id)
			})
		}

		if !kind.split && kind.tool == config.ConstBackupLogicalTypeMysqldump {
			e.checkEditedCopy(meta.Id, copyPath, gtid)
			e.checkTruncatedCopy(meta.Id, copyPath)
		}
		if kind.split {
			e.checkNoPositionCopy(orig.Dest)
		}

		err = cl.UnregisterBackup(meta.Id)
		_, statErr := os.Stat(copyPath)
		e.check("D7 "+kind.name+" unregister", err == nil && statErr == nil && cl.BackupMetaMap.Get(meta.Id) == nil && e.waitChange(meta.Id, false), fmt.Sprintf("err %v, file %v, registered %v", err, statErr, cl.BackupMetaMap.Get(meta.Id) != nil))
		e.waitDiscoveryIdle()
		c = e.discoverCandidate(copyPath)
		e.check("D7 "+kind.name+" a candidate again", c != nil, "not listed")
		os.RemoveAll(copyPath)
	}
	e.discoverCandidate("") // the copies removed: the candidates listed no longer name them
	e.checkScheduledRecord()
	e.checkReplacedScheduledBackup(gtid)
	return e.result()
}

// checkReplacedScheduledBackup replaces the scheduled mysqldump.sql.gz by hand, under its
// name: the backup is locked (WARN0249, a reseed refused) until the replacement is
// registered, then the automatic logical reseed uses it (D11).
func (e *edgeRun) checkReplacedScheduledBackup(gtid bool) {
	const name = "D11 scheduled backup replaced by hand"
	meta := e.m.LastBackupMeta.Logical
	if meta == nil || filepath.Base(meta.Dest) != "mysqldump.sql.gz" || meta.Registration == nil {
		e.check(name, false, fmt.Sprintf("no scheduled mysqldump.sql.gz with its fingerprint: %+v", meta))
		return
	}
	id := meta.Id
	if err := editDump(meta.Dest, "-- replaced by hand\n"); err != nil {
		e.check(name, false, err.Error())
		return
	}
	// placed by hand a while ago: past the settle window (the backup was just taken)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(meta.Dest, old, old)
	if !e.check(name+": locked, WARN0249", e.waitChange(id, true), "no change recorded") {
		return
	}
	err := e.cl.ReseedFromBackupID(e.r, id)
	e.check(name+": reseed refused", errors.Is(err, clusterpkg.ErrBackupChangedSinceRegistered), fmt.Sprint(err))
	if err := e.cl.RegisterBackupChange(id, time.Time{}); err != nil {
		e.check(name+": registered", false, err.Error())
		return
	}
	time.Sleep(time.Second)
	e.waitDiscoveryIdle()
	now := e.m.LastBackupMeta.Logical
	if !e.check(name+": registered as the scheduled backup", now != nil && now.Id != id && !now.Discovered && now.Registration != nil && e.waitChange(id, false), fmt.Sprintf("%+v", now)) {
		return
	}
	e.seed()
	e.reseedAndCheck(name+": automatic reseed from it", gtid)
}

// checkScheduledRecord writes the record a 3.1.44 discovery made of the scheduled backup's own
// file (mysqldump.sql.gz): the next discovery removes the record, its metadata file, and
// leaves the scheduled backup file as it is (D10).
func (e *edgeRun) checkScheduledRecord() {
	const name = "D10 record of a scheduled backup file"
	e.cl.Conf.BackupLogicalType = config.ConstBackupLogicalTypeMysqldump
	e.cl.Conf.BackupMysqldumpSplitDump = false
	if err := e.m.JobBackupLogical(context.Background()); err != nil {
		e.check(name, false, err.Error())
		return
	}
	dest := filepath.Join(e.m.GetMyBackupDirectory(), "mysqldump.sql.gz")
	id := time.Now().Add(-48 * time.Hour).Unix()
	data, _ := json.Marshal(&backupmgr.BackupMetadata{Id: id, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: config.ConstBackupLogicalTypeMysqldump, Source: e.m.URL, Dest: dest, Completed: true, Discovered: true})
	metaPath := filepath.Join(e.m.GetMyBackupDirectory(), fmt.Sprintf("mysqldump.%d.meta.json", id))
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer os.Remove(metaPath)
	e.discoverCandidate("")
	_, metaErr := os.Stat(metaPath)
	_, fileErr := os.Stat(dest)
	e.check(name+" removed, the file kept", e.cl.BackupMetaMap.Get(id) == nil && os.IsNotExist(metaErr) && fileErr == nil, fmt.Sprintf("registered %v, meta file %v, backup file %v", e.cl.BackupMetaMap.Get(id) != nil, metaErr, fileErr))
}

// discoveryMarker is the schema D5 adds to the edited dump: on the replica only, after the
// reseed from it.
const discoveryMarker = "discoverytest"

// checkEditedCopy edits the registered mysqldump copy as an operator would (a marker schema
// added before its footer): the change raises WARN0249 and refuses the reseed until it is
// registered; then the reseed loads the edited dump (D5).
func (e *edgeRun) checkEditedCopy(id int64, path string, gtid bool) {
	const name = "D5 edited copy"
	if err := editDump(path, "CREATE DATABASE IF NOT EXISTS `"+discoveryMarker+"`;\nCREATE TABLE `"+discoveryMarker+"`.`edit` (`id` INT PRIMARY KEY);\nINSERT INTO `"+discoveryMarker+"`.`edit` VALUES (2073);\n"); err != nil {
		e.check(name, false, err.Error())
		return
	}
	if !e.check(name+": WARN0249 raised for it", e.waitChange(id, true) && e.waitServerState("WARN0249", true), "not raised") {
		return
	}
	err := e.cl.ReseedFromBackupID(e.r, id)
	e.check(name+": reseed refused", errors.Is(err, clusterpkg.ErrBackupChangedSinceRegistered), fmt.Sprint(err))
	if err := e.cl.RegisterBackupChange(id, time.Time{}); err != nil {
		e.check(name+": change registered", false, err.Error())
		return
	}
	if meta, err := e.waitRegistered(path); !e.check(name+": change registered", err == nil && meta != nil && meta.Id == id && e.waitChange(id, false), fmt.Sprintf("%v %+v", err, meta)) {
		return
	}
	// the test table recreated: D4 already added the rows the reseed check adds after the backup
	e.seed()
	e.reseedWithAndCheck(name+": reseed from the registered change", gtid, config.ConstBackupLogicalTypeMysqldump, func() error {
		return e.cl.ReseedFromBackupID(e.r, id)
	})
	var marker int
	err = e.r.Conn.QueryRowx("SELECT id FROM `" + discoveryMarker + "`.`edit`").Scan(&marker)
	e.check(name+": the reseed loaded the edit", err == nil && marker == 2073, fmt.Sprintf("%d %v", marker, err))
	e.dropReplicaMarker()
}

// dropReplicaMarker removes D5's marker schema from the replica, unlogged: the primary never
// had it.
func (e *edgeRun) dropReplicaMarker() {
	ctx := context.Background()
	conn, err := e.r.Conn.Conn(ctx)
	if err != nil {
		e.logf(config.LvlWarn, "marker schema %s left on the replica: %s", discoveryMarker, err)
		return
	}
	defer conn.Close()
	for _, q := range []string{"SET SESSION sql_log_bin=0", "DROP DATABASE IF EXISTS `" + discoveryMarker + "`"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			e.logf(config.LvlWarn, "marker schema %s left on the replica: %s: %s", discoveryMarker, q, err)
			return
		}
	}
}

// checkTruncatedCopy cuts the registered copy's last byte: WARN0249 is raised, a verification
// on demand fails, and Register of the change is refused, the state staying (D6).
func (e *edgeRun) checkTruncatedCopy(id int64, path string) {
	const name = "D6 truncated copy"
	info, err := os.Stat(path)
	if err == nil {
		err = cutLastByte(path)
	}
	if err != nil {
		e.check(name, false, err.Error())
		return
	}
	// its time put back: an old copy cut, not one still being written
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if !e.check(name+": WARN0249 raised for it", e.waitChange(id, true) && e.waitServerState("WARN0249", true), "not raised") {
		return
	}
	if err := e.cl.VerifyBackupByID(id, config.ConstBackupVerificationLevelIntegrity); err != nil {
		e.check(name+": verify on demand", false, err.Error())
	} else {
		v := e.waitVerification(e.cl.BackupMetaMap.Get(id))
		e.check(name+": verify on demand fails", v != nil && v.Status == backupmgr.BackupVerificationFailed, fmt.Sprintf("%+v", v))
	}
	if err := e.cl.RegisterBackupChange(id, time.Time{}); err != nil {
		e.check(name+": change refused", false, "not started: "+err.Error())
		return
	}
	_, err = e.waitRegistered(path)
	e.check(name+": change refused", err != nil && e.waitChange(id, true), fmt.Sprint(err))
}

// checkNoPositionCopy copies the splitdump at src with a metadata file without a position
// (D8): registered completed with its note, its reseed refused, then unregistered.
func (e *edgeRun) checkNoPositionCopy(src string) {
	const name = "D8 splitdump without a position"
	copyPath := filepath.Join(e.m.GetMyBackupDirectory(), "discovery-copy-nopos")
	old := time.Now().Add(-2 * time.Hour)
	if err := copyBackupArtifact(src, copyPath, old); err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer os.RemoveAll(copyPath)
	metaFile := filepath.Join(copyPath, "metadata")
	if err := os.WriteFile(metaFile, []byte("[source]\nFile = \nPosition = 0\nExecuted_Gtid_Set = \n"), 0644); err != nil {
		e.check(name, false, err.Error())
		return
	}
	os.Chtimes(metaFile, old, old)
	os.Chtimes(copyPath, old, old)
	meta, err := e.register(copyPath)
	if err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer e.cl.UnregisterBackup(meta.Id)
	e.check(name+" noted", meta.Completed && strings.Contains(meta.DiscoveryNote, "no replication position"), fmt.Sprintf("completed %v, note %q", meta.Completed, meta.DiscoveryNote))
	err = e.cl.ReseedFromBackupID(e.r, meta.Id)
	e.check(name+" reseed refused", err != nil && strings.Contains(err.Error(), "no replication position"), fmt.Sprintf("%v", err))
}

// checkNotABackup places an accounts file (GRANT lines only) in the primary's backup folder:
// listed as not a backup, Register refused (D9).
func (e *edgeRun) checkNotABackup() {
	const name = "D9 accounts file"
	path := filepath.Join(e.m.GetMyBackupDirectory(), "discovery-grants.sql")
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte("GRANT SELECT ON *.* TO `discovery`@`%`;\n"), 0644); err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer os.Remove(path)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(path, old, old)
	c := e.discoverCandidate(path)
	e.check(name+" listed not a backup", c != nil && c.Status == clusterpkg.BackupCandidateNotBackup && strings.Contains(c.Reason, "accounts only"), fmt.Sprintf("%+v", c))
	err := e.cl.RegisterBackup(e.m.URL, path, time.Time{})
	e.check(name+" register refused", errors.Is(err, clusterpkg.ErrBackupNotABackup), fmt.Sprint(err))
}

// discoverCandidate runs a discovery of the primary's folder and returns the candidate at path.
func (e *edgeRun) discoverCandidate(path string) *clusterpkg.BackupCandidate {
	// a discovery already running (the one an Unregister starts) is waited for, then run again
	var cands []clusterpkg.BackupCandidate
	var err error
	for i := 0; i < 60; i++ {
		e.waitDiscoveryIdle()
		if cands, err = e.m.DiscoverBackups(); !errors.Is(err, clusterpkg.ErrBackupDiscoveryRunning) {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		e.logf(config.LvlWarn, "discovery: %s", err)
		return nil
	}
	for i := range cands {
		if cands[i].Path == path {
			return &cands[i]
		}
	}
	return nil
}

// registered returns the registered backup whose artifact is path.
func (e *edgeRun) registered(path string) *backupmgr.BackupMetadata {
	var found *backupmgr.BackupMetadata
	e.cl.BackupMetaMap.Callback(func(_ int64, m *backupmgr.BackupMetadata) bool {
		if m.Dest == path {
			found = m
			return false
		}
		return true
	})
	return found
}

// register asks for the registration of the candidate at path and waits for its outcome.
func (e *edgeRun) register(path string) (*backupmgr.BackupMetadata, error) {
	e.waitDiscoveryIdle()
	if err := e.cl.RegisterBackup(e.m.URL, path, time.Time{}); err != nil {
		return nil, err
	}
	return e.waitRegistered(path)
}

// waitRegistered waits for the registration of path to end: the backup, or why it was refused.
func (e *edgeRun) waitRegistered(path string) (*backupmgr.BackupMetadata, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		view := e.cl.GetBackupDiscovery()
		running, refused := false, ""
		for _, s := range view.Servers {
			for _, c := range s.Candidates {
				if c.Path == path {
					running, refused = c.Registering, c.RegisterError
				}
			}
		}
		for _, c := range view.Changes {
			if c.Dest == path && c.RegisterError != "" {
				refused = c.RegisterError
			}
		}
		if running {
			continue
		}
		e.waitDiscoveryIdle()
		if refused != "" {
			return nil, errors.New(refused)
		}
		if m := e.registered(path); m != nil && m.Registration != nil && artifactRegisteredNow(m) {
			return m, nil
		}
	}
	return nil, fmt.Errorf("registration of %s did not end", path)
}

// artifactRegisteredNow tells whether the registration of m is the file as it is now.
func artifactRegisteredNow(m *backupmgr.BackupMetadata) bool {
	info, err := os.Stat(m.Dest)
	return err == nil && (info.IsDir() || info.Size() == m.Registration.Artifact.Size)
}

// waitDiscoveryIdle waits for a discovery or registration of the primary's folder to end.
func (e *edgeRun) waitDiscoveryIdle() {
	for i := 0; i < 600; i++ {
		if !e.cl.IsBackupDiscoveryRunning(e.m.URL) {
			return
		}
		time.Sleep(time.Second)
	}
}

// waitChange waits up to three minutes (the change check runs every 30 heartbeats) for a
// change of backup id to be recorded (or cleared). Another registered backup of the folder
// may be changed meanwhile (a scheduled backup rewriting a file a release registered as
// discovered): the server-level WARN0249 is not this backup's alone.
func (e *edgeRun) waitChange(id int64, changed bool) bool {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		found := false
		for _, c := range e.cl.GetBackupDiscovery().Changes {
			found = found || c.ID == id
		}
		if found == changed {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// waitServerState waits up to three minutes (the change check runs every 30 heartbeats) for
// a state of the primary to be raised (or cleared).
func (e *edgeRun) waitServerState(key string, raised bool) bool {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if e.cl.IsInErrorState(key, e.m.URL) == raised {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// editDump inserts lines before the footer of the gzipped dump at path, keeping its times.
func editDump(path, lines string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return err
	}
	data, err := io.ReadAll(zr)
	f.Close()
	if err != nil {
		return err
	}
	text := string(data)
	i := strings.LastIndex(text, "-- Dump completed")
	if i < 0 {
		return errors.New("no footer")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(text[:i] + lines + text[i:]))
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return err
	}
	return os.Chtimes(path, info.ModTime(), info.ModTime())
}

// toolInstalled tells whether path names an executable file.
func toolInstalled(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

// copyBackupArtifact copies a backup file or folder to dst, as an operator would, and sets
// its times to mtime.
func copyBackupArtifact(src, dst string, mtime time.Time) error {
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chtimes(target, mtime, mtime)
	})
	if err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}
