// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
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
// (#2051) on real servers. For a single mysqldump file and a splitdump folder, and a
// mydumper folder when mydumper is installed:
//
//   - D1: a backup of the primary is copied in its backup folder under another name, as an
//     operator would, its times set back past the settle window of the discovery;
//   - D2: the discovery registers the copy: discovered, completed, with the position and the
//     GTID set the original backup recorded;
//   - D3: the replica is reseeded from the copy on request (ReseedFromBackupID), checked as
//     TestLogicalReseed checks a reseed (rows and checksum, replication threads, GTID mode,
//     transactions after it). Skipped for mydumper without myloader;
//   - D4: the copy is deleted through the backup deletion: artifact and metadata gone;
//   - D5 (splitdump): a copy whose metadata holds no position is listed completed, noted, and
//     its reseed refused: the replica would start at the primary's current position.
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
	cl.Conf.SchedulerJobsExecRemote = ""
	cl.Conf.ParseJobsExecOverrides()
	gtid := e.r.HasGTIDReplication()

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

		found, err := e.m.DiscoverBackups()
		var meta *backupmgr.BackupMetadata
		for _, m := range found {
			if m.Dest == copyPath {
				meta = m
			}
		}
		switch {
		case err != nil:
			e.check("D2 "+kind.name+" discovered", false, err.Error())
		case meta == nil:
			e.check("D2 "+kind.name+" discovered", false, fmt.Sprintf("%s not registered (%d registered)", copyPath, len(found)))
		case !meta.Discovered || !meta.Completed || meta.SplitDump != kind.split || meta.BackupTool != kind.tool:
			e.check("D2 "+kind.name+" discovered", false, fmt.Sprintf("metadata %+v", meta))
		// the GTID set compared without its spaces: the backup records it with ", " between
		// its sources, a dump writes them on separate lines
		case (meta.BinLogFileName != orig.BinLogFileName || meta.BinLogFilePos != orig.BinLogFilePos || strings.Join(strings.Fields(meta.BinLogGtid), "") != strings.Join(strings.Fields(orig.BinLogGtid), "")):
			e.check("D2 "+kind.name+" discovered", false, fmt.Sprintf("position %s:%d %q, the backup recorded %s:%d %q", meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid, orig.BinLogFileName, orig.BinLogFilePos, orig.BinLogGtid))
		default:
			e.check("D2 "+kind.name+" discovered", true, "")
		}
		if meta == nil {
			os.RemoveAll(copyPath)
			continue
		}

		if kind.tool == config.ConstBackupLogicalTypeMydumper && !toolInstalled(cl.GetMyLoaderPath()) {
			e.logf("TEST", "D3 %s reseed skipped: myloader is not installed", kind.name)
		} else {
			id := meta.Id
			e.reseedWithAndCheck("D3 "+kind.name+" reseed from the discovered copy", gtid, kind.tool, func() error {
				return cl.ReseedFromBackupID(e.r, id)
			})
		}

		if kind.split {
			e.checkNoPositionCopy(orig.Dest)
		}

		if _, err := cl.DeleteBackupByID(meta.Id); err != nil {
			e.check("D4 "+kind.name+" delete", false, err.Error())
			os.RemoveAll(copyPath)
			continue
		}
		_, statErr := os.Stat(copyPath)
		e.check("D4 "+kind.name+" delete", os.IsNotExist(statErr) && cl.BackupMetaMap.Get(meta.Id) == nil, fmt.Sprintf("artifact stat %v, registered %v", statErr, cl.BackupMetaMap.Get(meta.Id) != nil))
	}
	return e.result()
}

// checkNoPositionCopy copies the splitdump at src with a metadata file without a position
// (D5): discovered completed with its note, its reseed refused, then deleted.
func (e *edgeRun) checkNoPositionCopy(src string) {
	const name = "D5 splitdump without a position"
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
	found, err := e.m.DiscoverBackups()
	if err != nil {
		e.check(name, false, err.Error())
		return
	}
	var meta *backupmgr.BackupMetadata
	for _, m := range found {
		if m.Dest == copyPath {
			meta = m
		}
	}
	if meta == nil {
		e.check(name, false, fmt.Sprintf("%s not registered", copyPath))
		return
	}
	defer e.cl.DeleteBackupByID(meta.Id)
	e.check(name+" noted", meta.Completed && strings.Contains(meta.DiscoveryNote, "no replication position"), fmt.Sprintf("completed %v, note %q", meta.Completed, meta.DiscoveryNote))
	err = e.cl.ReseedFromBackupID(e.r, meta.Id)
	e.check(name+" reseed refused", err != nil && strings.Contains(err.Error(), "no replication position"), fmt.Sprintf("%v", err))
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
