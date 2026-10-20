// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

func TestBackupIDAndSnapshotID(t *testing.T) {
	if got := backupID("db1.belair.svc.cloud18:3306"); got != "db1.belair.svc.cloud18_3306" {
		t.Fatalf("backupID: %q", got)
	}
	if got := backupID(""); got != defaultBackup {
		t.Fatalf("empty host: %q", got)
	}
	if got := backupID("..x y"); got != "x_y" {
		t.Fatalf("leading dots and spaces: %q", got)
	}
	when := time.Date(2026, 10, 20, 9, 5, 7, 0, time.UTC)
	if got := snapshotID("host", "db1", when); got != "host/db1/2026-10-20T09:05:07Z" {
		t.Fatalf("snapshotID: %q", got)
	}
}

func TestNotesRoundTrip(t *testing.T) {
	line := encodeNotes("db1:3306", "/var/lib/backups/a b", []string{"cluster:belair", "logical,dump", "x=y"})
	host, dir, tags := decodeNotes(line)
	if host != "db1:3306" || dir != "/var/lib/backups/a b" || !reflect.DeepEqual(tags, []string{"cluster:belair", "logical,dump", "x=y"}) {
		t.Fatalf("round trip: %q %q %v from %q", host, dir, tags, line)
	}
	if h, d, tg := decodeNotes("a note written by an admin"); h != "" || d != "" || tg != nil {
		t.Fatal("a foreign comment gives nothing")
	}
}

func TestParseAndFindSnapshots(t *testing.T) {
	out := []byte(`[{"backup-type":"host","backup-id":"db1","backup-time":1760950000,"comment":"` +
		encodeNotes("db1", "/b/1", []string{"t1"}) + `","size":100,"files":[]},` +
		`{"backup-type":"host","backup-id":"db1","backup-time":1760960000,"size":50,"owner":"x"}]`)
	snaps, err := parseSnapshots(out)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("parse: %v %v", snaps, err)
	}
	if !snaps[0].Time.After(snaps[1].Time) || snaps[1].Host != "db1" || snaps[1].Paths[0] != "/b/1" || snaps[0].Size != 50 {
		t.Fatalf("newest first, notes decoded: %+v", snaps)
	}
	if _, err := findSnapshot(snaps, "host/db1/"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("an ambiguous prefix is refused: %v", err)
	}
	if s, err := findSnapshot(snaps, snaps[1].ID[:len(snaps[1].ID)-3]); err != nil || s.ID != snaps[1].ID {
		t.Fatalf("a unique prefix resolves: %v %v", s, err)
	}
}

func TestRestorePatterns(t *testing.T) {
	for _, tc := range []struct {
		include, pushed string
		want            []string
	}{
		{"", "/b/1", nil},
		{"/", "/b/1", nil},
		{"dump.sql.gz", "/b/1", []string{"dump.sql.gz", "dump.sql.gz/**"}},
		{"/b/1/sub/x", "/b/1", []string{"sub/x", "sub/x/**"}},
		{"/b/1", "/b/1", nil},
	} {
		if got := restorePatterns(tc.include, tc.pushed); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("restorePatterns(%q, %q) = %v, want %v", tc.include, tc.pushed, got, tc.want)
		}
	}
}

func TestPruneArgs(t *testing.T) {
	args, err := pruneArgs(&wire.Policy{KeepLast: 3, KeepDaily: 7, KeepMonthly: 2})
	if err != nil || strings.Join(args, " ") != "--keep-last 3 --keep-daily 7 --keep-monthly 2" {
		t.Fatalf("counts: %v %v", args, err)
	}
	if _, err := pruneArgs(&wire.Policy{KeepLast: 3, KeepWithinDaily: "7d"}); err == nil {
		t.Fatal("a keep-within window has no PBS equivalent: refused, never ignored")
	}
}

// fakeClient writes a proxmox-backup-client stand-in: it records each call's arguments and
// the key it reads on fd 3, and answers the JSON lists the plugin parses.
func fakeClient(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "proxmox-backup-client")
	body := `#!/bin/sh
echo "$@" >> "` + dir + `/calls"
echo "PBS_REPOSITORY=$PBS_REPOSITORY PBS_PASSWORD=$PBS_PASSWORD SECRET_LEAK=$REPMAN_ARCHIVEPLUGIN_SECRET_PASSWORD" >> "` + dir + `/env"
case "$*" in *"--keyfd 3"*) cat <&3 > "` + dir + `/key";; esac
case "$1 $2" in
  "snapshot list") echo '[{"backup-type":"host","backup-id":"db1_3306","backup-time":1760950000,"comment":"repman host=db1%3A3306 path=%2Fb%2F1 tags=t1","size":10}]';;
  "prune host/db1_3306") echo '[{"backup-type":"host","backup-id":"db1_3306","backup-time":1760950000,"keep":false}]';;
  "namespace list") echo '[{"ns":""},{"ns":"cl"}]';;
esac
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func testStore(client, ns string) wire.Store {
	return wire.Store{Plugin: pluginPBS, Name: "pbs1", Settings: map[string]string{
		setServer: "pbs.lan", setDatastore: "store1", setNamespace: ns, setAuthID: "repman@pbs!arch", setClientPath: client}}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// The operations run the client with the archive's namespace, the key on fd 3 (never in a
// file nor on the command line) and the password in PBS_PASSWORD, replication-manager's
// own secret variables kept out of the client's environment.
func TestOperationsWithFakeClient(t *testing.T) {
	dir := t.TempDir()
	client := fakeClient(t, dir)
	t.Setenv(wire.SecretEnv(setPassword), "s3cret")
	t.Setenv(wire.SecretEnv(setKey), `{"kdf":null,"data":"KEY"}`)
	backup := filepath.Join(dir, "backup")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := handle(wire.Request{Op: wire.OpPush, Store: testStore(client, "a/b"), Path: backup, Host: "db1:3306", Tags: []string{"t1"}})
	if err != nil || resp.Snapshot == nil || !strings.HasPrefix(resp.Snapshot.ID, "host/db1_3306/") {
		t.Fatalf("push: %+v %v", resp, err)
	}
	calls := readFile(t, filepath.Join(dir, "calls"))
	if !strings.Contains(calls, "backup backup.pxar:"+backup+" --backup-type host --backup-id db1_3306") ||
		!strings.Contains(calls, "--ns a/b") || !strings.Contains(calls, "--keyfd 3 --crypt-mode encrypt") ||
		!strings.Contains(calls, "snapshot notes update "+resp.Snapshot.ID+" repman host=db1%3A3306") {
		t.Fatalf("push calls:\n%s", calls)
	}
	if strings.Contains(calls, "KEY") || strings.Contains(calls, "s3cret") {
		t.Fatal("no secret on a command line")
	}
	if got := readFile(t, filepath.Join(dir, "key")); got != `{"kdf":null,"data":"KEY"}` {
		t.Fatalf("the key arrives on fd 3: %q", got)
	}
	env := readFile(t, filepath.Join(dir, "env"))
	if !strings.Contains(env, "PBS_REPOSITORY=repman@pbs!arch@pbs.lan:store1 PBS_PASSWORD=s3cret SECRET_LEAK=\n") {
		t.Fatalf("client environment: %q", env)
	}

	resp, err = handle(wire.Request{Op: wire.OpList, Store: testStore(client, "a/b")})
	if err != nil || len(resp.Snapshots) != 1 || resp.Snapshots[0].Host != "db1:3306" || resp.Snapshots[0].Tags[0] != "t1" {
		t.Fatalf("list: %+v %v", resp, err)
	}

	resp, err = handle(wire.Request{Op: wire.OpPurge, Store: testStore(client, "a/b"), Policy: &wire.Policy{KeepLast: 2}, DryRun: true})
	if err != nil || len(resp.Removed) != 1 {
		t.Fatalf("purge: %+v %v", resp, err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "calls")), "prune host/db1_3306 --output-format json --keep-last 2 --dry-run --ns a/b") {
		t.Fatalf("purge calls:\n%s", readFile(t, filepath.Join(dir, "calls")))
	}

	resp, err = handle(wire.Request{Op: wire.OpRestore, Store: testStore(client, "a/b"), Snapshot: "host/db1_3306/", Target: filepath.Join(dir, "out"), Include: "/b/1/dump.sql.gz"})
	if err != nil || resp.Snapshot == nil {
		t.Fatalf("restore: %+v %v", resp, err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "calls")), "restore host/db1_3306/2025-10-20T08:46:40Z backup.pxar "+filepath.Join(dir, "out")+" --allow-existing-dirs --pattern dump.sql.gz --pattern dump.sql.gz/** --ns a/b --keyfd 3") {
		t.Fatalf("restore calls:\n%s", readFile(t, filepath.Join(dir, "calls")))
	}

	resp, err = handle(wire.Request{Op: wire.OpSize, Store: testStore(client, "a/b")})
	if err != nil || resp.Size != 10 {
		t.Fatalf("size: %+v %v", resp, err)
	}
}

func TestProbeCreatesMissingNamespaceLevels(t *testing.T) {
	dir := t.TempDir()
	// the snapshot list fails until the namespace exists: a client that fails on "snapshot list --ns cl/new"
	script := filepath.Join(dir, "proxmox-backup-client")
	body := `#!/bin/sh
echo "$@" >> "` + dir + `/calls"
case "$*" in
  "snapshot list --output-format json --ns cl/new") [ -f "` + dir + `/created" ] || exit 1; echo '[]';;
  "namespace list"*) echo '[{"ns":""},{"ns":"cl"}]';;
  "namespace create cl/new") touch "` + dir + `/created";;
esac
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	resp, err := handle(wire.Request{Op: wire.OpProbe, Store: testStore(script, "cl/new"), Create: true})
	if err != nil || !resp.Created || resp.Location != "pbs://repman@pbs!arch@pbs.lan:store1/cl/new" {
		t.Fatalf("probe: %+v %v", resp, err)
	}
	calls := readFile(t, filepath.Join(dir, "calls"))
	if strings.Contains(calls, "namespace create cl\n") || !strings.Contains(calls, "namespace create cl/new") {
		t.Fatalf("only the missing level is created:\n%s", calls)
	}
}

func TestRemoveAndSyncRefusals(t *testing.T) {
	if _, err := handle(wire.Request{Op: wire.OpRemove, Store: testStore("/nonexistent", "")}); err == nil || !strings.Contains(err.Error(), "never removed") {
		t.Fatalf("remove without a namespace is refused: %v", err)
	}
	if _, err := handle(wire.Request{Op: wire.OpSync, Store: testStore("/nonexistent", "a")}); err == nil {
		t.Fatal("sync is refused")
	}
	if _, err := handle(wire.Request{Op: wire.OpList, Store: wire.Store{Plugin: "archive-plakar-local"}}); err == nil {
		t.Fatal("another plugin's store is refused")
	}
}
