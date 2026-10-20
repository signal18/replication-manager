// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
	"github.com/signal18/replication-manager/config"
)

// a fake archive plugin program: its manifests on --manifest; for an operation, it answers
// with its op and whether the archive's secret reached it in its environment, never on stdin
const fakeArchivePlugin = `#!/bin/sh
if [ "$1" = "--manifest" ]; then
  echo '[{"wireVersion":1,"name":"archive-fake-local","description":"Local folder","tier":"","settings":[{"name":"location","type":"string"},{"name":"password","type":"secret"}],"capabilities":["probe","size","remove"]},{"wireVersion":1,"name":"archive-fake-s3","description":"S3 bucket","settings":[{"name":"bucket","type":"string"}],"capabilities":["probe","s3"]}]'
  exit 0
fi
req=$(cat)
case "$req" in *pw-secret*) echo '{"wireVersion":1,"error":"the secret came on stdin"}'; exit 0;; esac
case "$req" in
  *'"op":"probe"'*) [ "$REPMAN_ARCHIVEPLUGIN_SECRET_PASSWORD" = "pw-secret" ] && echo '{"wireVersion":1,"created":true}' || echo '{"wireVersion":1,"error":"no secret in the environment"}';;
  *'"op":"size"'*) echo '{"wireVersion":1,"size":4096,"files":3}';;
  *) echo '{"wireVersion":1,"error":"unknown op"}';;
esac
`

func TestArchivePluginExternal(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	dir := filepath.Join(cl.Conf.WorkingDir, "plugins")
	os.MkdirAll(dir, 0o755)
	bin := filepath.Join(dir, wire.BinaryPrefix+"fake")
	if err := os.WriteFile(bin, []byte(fakeArchivePlugin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cl.loadArchivePlugin(bin); err != nil {
		t.Fatalf("load: %v", err)
	}
	m, ok := config.ArchivePlugin("archive-fake-local")
	if !ok || !m.External || m.Binary != bin {
		t.Fatalf("manifest %+v", m)
	}
	if s3, _ := config.ArchivePlugin("archive-fake-s3"); !s3.HasSetting(config.ConstArchiveSettingS3Provider) {
		t.Fatalf("an s3 plugin gets the s3-provider setting: %+v", s3.Settings)
	}

	a := config.ArchiveConfig{Name: "f1", Plugin: "archive-fake-local", Settings: map[string]interface{}{"location": "/data/f1", "password": "pw-secret"}}
	if got, err := cl.ProbeArchive(a); err != nil || got != ArchiveProbeInitialised {
		t.Fatalf("probe: %q %v", got, err)
	}
	resp, err := cl.runArchivePlugin(a, wire.Request{Op: wire.OpSize})
	if err != nil || resp.Size != 4096 || resp.Files != 3 {
		t.Fatalf("size: %+v %v", resp, err)
	}
	if _, err := cl.runArchivePlugin(a, wire.Request{Op: wire.OpForget}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("the plugin's error must come back: %v", err)
	}
	if loc := cl.ArchiveLocation(a); loc != "archive-fake-local:/data/f1" {
		t.Fatalf("location %s", loc)
	}

	// attached, a backup's snapshot is recorded in its location: no restic manager runs then
	// (the push panicked on the nil manager)
	cl.ResticManager = nil
	cl.Conf.Archives, cl.Conf.BackupArchiveName = []config.ArchiveConfig{a}, a.Name
	if got := cl.attachedArchiveRepoPath(); got != "archive-fake-local:/data/f1" {
		t.Fatalf("attached plugin archive repository %q", got)
	}
	cl.Conf.BackupArchiveName = ""
	if got := cl.attachedArchiveRepoPath(); got != "" {
		t.Fatalf("no archive, no restic manager: %q", got)
	}
}
