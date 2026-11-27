// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The host steps of an external snapshot plugin (#2456): the embedded helper's devices, unmount
// and mount actions on a fake database host, around a fake external plugin that records what it
// got.

// fakeHostFindmnt reads FAKE_DIR/mounts ("target source fstype" lines): -T PATH the mount
// holding PATH (the longest target), -R TARGET the mounts at and under it, --source SRC its
// mount points; -o picks TARGET, SOURCE, FSTYPE.
const fakeHostFindmnt = `#!/bin/sh
D="$FAKE_DIR"; mode=""; cols="TARGET"; arg=""; prev=""
for a in "$@"; do
    case "$prev" in -o) cols="$a" ;; -T) mode=T; arg="$a" ;; -R) mode=R; arg="$a" ;; --source) mode=S; arg="$a" ;; esac
    prev="$a"
done
pick() { out=""; for c in $(echo "$cols" | tr ',' ' '); do
    case "$c" in TARGET) v="$1" ;; SOURCE) v="$2" ;; FSTYPE) v="$3" ;; esac
    out="$out${out:+ }$v"; done; echo "$out"; }
case "$mode" in
T) best=""; while read -r t s f; do
       case "$t" in /) m=1 ;; *) case "$arg" in "$t" | "$t"/*) m=1 ;; *) m=0 ;; esac ;; esac
       if [ "$m" = 1 ] && [ ${#t} -gt ${#best} ]; then best="$t"; bs="$s"; bf="$f"; fi
   done <"$D/mounts"
   [ -n "$best" ] && pick "$best" "$bs" "$bf" ;;
R) while read -r t s f; do case "$t" in "$arg" | "$arg"/*) pick "$t" "$s" "$f" ;; esac; done <"$D/mounts" ;;
S) while read -r t s f; do [ "$s" = "$arg" ] && pick "$t" "$s" "$f"; done <"$D/mounts" ;;
esac
exit 0
`

// fakeLsblk prints FAKE_DIR/lsblk/<basename of the device> ("WWN TYPE" lines), else nothing.
const fakeLsblk = `#!/bin/sh
last=""; for a in "$@"; do last="$a"; done
f="$FAKE_DIR/lsblk/$(basename "$last")"
[ -f "$f" ] && cat "$f"
exit 0
`

// fakeRecorder: a command that appends its name and arguments to FAKE_DIR/calls; it fails
// when FAKE_DIR/<name>-fails holds its last argument, and hangs 3 s when FAKE_DIR/<name>-hangs does.
const fakeRecorder = `#!/bin/sh
n=$(basename "$0")
echo "$n $*" >>"$FAKE_DIR/calls"
last=""; for a in "$@"; do last="$a"; done
[ -f "$FAKE_DIR/$n-hangs" ] && grep -qx "$last" "$FAKE_DIR/$n-hangs" && sleep 3
[ -f "$FAKE_DIR/$n-fails" ] && grep -qx "$last" "$FAKE_DIR/$n-fails" && { echo "$n: $last: failed" >&2; exit 1; }
exit 0
`

// fakeExternalPlugin records its environment and the time it ran (after which host call), and
// answers the contract.
const fakeExternalPlugin = `#!/bin/sh
env | sort >"$FAKE_DIR/plugin-env-$1"
echo "plugin $1" >>"$FAKE_DIR/calls"
printf 'CMD\tstorage-cli restore %s\n' "$REPMAN_SNAPSHOT_NAME"
printf 'TARGET\tlun-db1\n'
case "$1" in
list) printf 'SNAPSHOT\trepman-20261127T100000Z\t1795600000\t0\n' ;;
snapback)
    [ -f "$FAKE_DIR/plugin-refuses" ] && { echo "refused by the storage: nothing was changed" >&2; exit 1; }
    printf 'CHANGED\n'
    [ -f "$FAKE_DIR/plugin-fails-after" ] && { echo "restore failed half-way" >&2; exit 1; } ;;
esac
exit 0
`

type fakeHost struct {
	cl     *Cluster
	server *ServerMonitor
	vs     *contractSnapshotter
	dir    string
	data   string
}

func newFakeStorageHost(t *testing.T) *fakeHost {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	b := filepath.Join(dir, "bin")
	os.MkdirAll(b, 0o755)
	os.MkdirAll(filepath.Join(dir, "lsblk"), 0o755)
	os.WriteFile(filepath.Join(b, "findmnt"), []byte(fakeHostFindmnt), 0o755)
	os.WriteFile(filepath.Join(b, "lsblk"), []byte(fakeLsblk), 0o755)
	os.WriteFile(filepath.Join(b, "fuser"), []byte(fakeFuser), 0o755)
	for _, n := range []string{"umount", "mount", "blockdev"} {
		os.WriteFile(filepath.Join(b, n), []byte(fakeRecorder), 0o755)
	}
	plugin := filepath.Join(dir, "snapshotplugin-storage-host-test")
	os.WriteFile(plugin, []byte(fakeExternalPlugin), 0o755)

	data := filepath.Join(dir, "srv", "mysql")
	os.MkdirAll(filepath.Join(data, "binlog"), 0o755)
	// the datadir on a multipath LUN, its binlog directory on a second file system of another LUN
	os.WriteFile(filepath.Join(dir, "mounts"), []byte("/ /dev/sda1 ext4\n"+data+" /dev/mapper/mpatha xfs\n"+data+"/binlog /dev/mapper/mpathb xfs\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lsblk", "mpatha"), []byte("mpatha mpath\n0x600a098038303053743f4a6c6f4b4c4b disk\n0x600a098038303053743f4a6c6f4b4c4b disk\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lsblk", "mpathb"), []byte("mpathb mpath\n0x600a098038303053743f4a6c6f4b4c4c disk\n"), 0o644)

	if err := config.RegisterSnapshotPlugin(config.SnapshotPluginManifest{Name: "storage-host-test", Settings: config.PluginSettings{
		{Name: "url", Type: config.ConstArchiveSettingTypeString},
		{Name: "token", Type: config.ConstArchiveSettingTypeSecret},
	}}, plugin); err != nil {
		t.Fatal(err)
	}
	m, _ := config.SnapshotPlugin("storage-host-test")

	cl, server := newTestClusterServer(t)
	cl.Conf.BackupSnapshot = true
	cl.Conf.BackupSnapshotUnmount = true
	cl.Conf.BackupSnapshotPlugin = m.Name
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	if err := cl.Conf.PutSnapshotPluginSettings(m.Name, map[string]interface{}{"url": "https://storage.example", "token": "s3cr3t-TOKEN"}, nil); err != nil {
		t.Fatal(err)
	}
	server.SensitiveVariables = config.NewStringsMap()
	server.SensitiveVariables.Set("DATADIR", data)
	server.SensitiveVariables.Set("LOG_BIN_BASENAME", filepath.Join(data, "binlog", "mysql-bin"))
	vs := &contractSnapshotter{
		manifest: m,
		exec:     &programSnapshotExecutor{program: plugin, baseEnv: []string{"PATH=/usr/bin:/bin", "FAKE_DIR=" + dir}},
		timeout:  time.Minute,
		host:     &shSnapshotExecutor{bin: b, dir: dir},
	}
	return &fakeHost{cl: cl, server: server, vs: vs, dir: dir, data: data}
}

func (h *fakeHost) calls() []string {
	b, _ := os.ReadFile(filepath.Join(h.dir, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (h *fakeHost) pluginEnv(t *testing.T, action string) string {
	b, err := os.ReadFile(filepath.Join(h.dir, "plugin-env-"+action))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExternalPluginGetsSettingsAndDevices(t *testing.T) {
	h := newFakeStorageHost(t)
	run, err := h.vs.List(h.server)
	if err != nil || len(run.Snapshots) != 1 {
		t.Fatalf("list: %v %+v", err, run)
	}
	env := h.pluginEnv(t, "list")
	for _, want := range []string{
		"REPMAN_SNAPSHOT_SETTING_URL=https://storage.example",
		"REPMAN_SNAPSHOT_SECRET_TOKEN=s3cr3t-TOKEN",
		"REPMAN_SNAPSHOT_DEVICES=" + h.data + "\txfs\t/dev/mapper/mpatha\t0x600a098038303053743f4a6c6f4b4c4b",
		h.data + "/binlog\txfs\t/dev/mapper/mpathb\t0x600a098038303053743f4a6c6f4b4c4c",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the plugin's environment lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "REPMAN_SNAPSHOT_UNMOUNTED") {
		t.Fatal("a list is told the file systems are unmounted")
	}
	for _, c := range run.Commands {
		if strings.Contains(c, "s3cr3t") {
			t.Fatalf("the secret in a command: %s", c)
		}
	}
	// a path that can not be told: no devices at all, never a partial list
	os.Remove(filepath.Join(h.dir, "lsblk", "mpathb"))
	h.vs.List(h.server)
	if env := h.pluginEnv(t, "list"); strings.Contains(env, "mpatha") || !strings.Contains(env, "REPMAN_SNAPSHOT_DEVICES=") {
		t.Fatalf("a partial device list passed:\n%s", env)
	}
}

func TestExternalSnapbackUnmountsAroundTheRestore(t *testing.T) {
	h := newFakeStorageHost(t)
	run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
	if err != nil || !run.Changed {
		t.Fatalf("snapback: %v %+v", err, run)
	}
	calls := strings.Join(h.calls(), "\n")
	// deepest first, the plugin, then flushed and mounted back parents first
	order := []string{"umount " + h.data + "/binlog", "umount " + h.data + "\n", "plugin snapback", "blockdev --flushbufs /dev/mapper/mpatha", "mount " + h.data + "\n", "mount " + h.data + "/binlog"}
	pos := 0
	for _, o := range order {
		i := strings.Index(calls[pos:]+"\n", o)
		if i < 0 {
			t.Fatalf("%q not after position %d in:\n%s", o, pos, calls)
		}
		pos += i
	}
	if !strings.Contains(h.pluginEnv(t, "snapback"), "REPMAN_SNAPSHOT_UNMOUNTED=1") {
		t.Fatal("the plugin was not told the file systems are unmounted")
	}
	joined := strings.Join(run.Commands, "\n")
	if !strings.Contains(joined, "umount") || !strings.Contains(joined, "storage-cli restore") || !strings.Contains(joined, "mount ") {
		t.Fatalf("the commands of every step are not all listed (F11): %v", run.Commands)
	}
}

func TestExternalSnapbackRefusedBeforeAnyChange(t *testing.T) {
	t.Run("system mount point", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "mounts"), []byte("/ /dev/sda1 ext4\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || run.Changed || !strings.Contains(err.Error(), "system file system") {
			t.Fatalf("%v %+v", err, run)
		}
		if strings.Contains(strings.Join(h.calls(), "\n"), "umount") || strings.Contains(strings.Join(h.calls(), "\n"), "plugin") {
			t.Fatalf("calls: %v", h.calls())
		}
	})
	t.Run("a | in a mount point", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "mounts"), []byte("/ /dev/sda1 ext4\n"+h.data+" /dev/mapper/mpatha xfs\n"+h.data+"/binlog /dev/mapper/a|b xfs\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || run.Changed || !strings.Contains(err.Error(), "holds a '|'") {
			t.Fatalf("%v %+v", err, run)
		}
	})
	t.Run("busy", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "busy"), []byte(h.data+"\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || run.Changed || !strings.Contains(err.Error(), "still in use") {
			t.Fatalf("%v %+v", err, run)
		}
	})
	t.Run("umount fails: put back", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "umount-fails"), []byte(h.data+"\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || run.Changed || !strings.Contains(err.Error(), "nothing was changed") {
			t.Fatalf("%v %+v", err, run)
		}
		if !strings.Contains(strings.Join(h.calls(), "\n"), "mount "+h.data+"/binlog") {
			t.Fatalf("the binlog file system was not put back: %v", h.calls())
		}
	})
	t.Run("the plugin refuses: mounted back, unchanged", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "plugin-refuses"), nil, 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || run.Changed {
			t.Fatalf("%v %+v", err, run)
		}
		if !strings.Contains(strings.Join(h.calls(), "\n"), "mount "+h.data) {
			t.Fatalf("not mounted back: %v", h.calls())
		}
	})
}

func TestExternalSnapbackHalfway(t *testing.T) {
	t.Run("the restore fails after it changed the storage: left unmounted", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "plugin-fails-after"), nil, 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || !run.Changed {
			t.Fatalf("%v %+v", err, run)
		}
		calls := strings.Join(h.calls(), "\n")
		if strings.Contains(calls[strings.Index(calls, "plugin snapback"):], "mount ") {
			t.Fatalf("mounted a half-restored storage: %s", calls)
		}
	})
	t.Run("the unmount runs past the deadline: half-way, never started", func(t *testing.T) {
		h := newFakeStorageHost(t)
		h.vs.timeout = time.Second
		// the binlog file system is unmounted, then the datadir's umount hangs past the deadline
		os.WriteFile(filepath.Join(h.dir, "umount-hangs"), []byte(h.data+"\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || !run.Changed || !strings.Contains(err.Error(), "backup-snapshot-timeout") {
			t.Fatalf("%v %+v", err, run)
		}
		if strings.Contains(strings.Join(h.calls(), "\n"), "plugin snapback") {
			t.Fatal("the plugin ran after an unmount that did not end")
		}
	})
	t.Run("the mount fails after the restore", func(t *testing.T) {
		h := newFakeStorageHost(t)
		os.WriteFile(filepath.Join(h.dir, "mount-fails"), []byte(h.data+"\n"), 0o644)
		run, err := h.vs.Snapback(h.server, "repman-20261127T100000Z")
		if err == nil || !run.Changed || !strings.Contains(err.Error(), "could not be mounted again") {
			t.Fatalf("%v %+v", err, run)
		}
	})
}

func TestExternalSnapbackUnmountOff(t *testing.T) {
	h := newFakeStorageHost(t)
	h.cl.Conf.BackupSnapshotUnmount = false
	if _, err := h.vs.Snapback(h.server, "repman-20261127T100000Z"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(h.calls(), "\n"), "umount") {
		t.Fatal("unmounted with backup-snapshot-unmount off")
	}
	if strings.Contains(h.pluginEnv(t, "snapback"), "REPMAN_SNAPSHOT_UNMOUNTED") {
		t.Fatal("told unmounted with backup-snapshot-unmount off")
	}
}
