// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func TestParseVolumeSnapshotOutput(t *testing.T) {
	out := "CMD\tzfs list -H -o name tank/db\nTARGET\ttank/db\nsome log line\n" +
		"SNAPSHOT\tdaily-2\t1700000200\t8192\nSNAPSHOT\trepman-20261126T100000Z\t1700000100\t4096\nSNAPSHOT\trepman-x\t1700000300\t1\nCHANGED\n"
	run := parseVolumeSnapshotOutput(out)
	if run.Target != "tank/db" || !run.Changed {
		t.Fatalf("target/changed: %+v", run)
	}
	if len(run.Commands) != 1 || run.Commands[0] != "zfs list -H -o name tank/db" {
		t.Fatalf("commands: %v", run.Commands)
	}
	if len(run.Snapshots) != 3 || run.Snapshots[0].Name != "repman-20261126T100000Z" || !run.Snapshots[0].Managed || run.Snapshots[1].Managed || run.Snapshots[2].Managed {
		t.Fatalf("snapshots sorted by creation, managed by prefix: %+v", run.Snapshots)
	}
	if run.Snapshots[0].Size != 4096 || run.Snapshots[0].Created.Unix() != 1700000100 {
		t.Fatalf("snapshot fields: %+v", run.Snapshots[0])
	}
	if len(run.Log) != 1 || run.Log[0] != "some log line" {
		t.Fatalf("log: %v", run.Log)
	}
}

func TestParseVolumeSnapshotOutputBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < volumeSnapshotMaxLines*3; i++ {
		b.WriteString("SNAPSHOT\trepman-x\t1\t1\nCMD\tzfs\n")
	}
	run := parseVolumeSnapshotOutput(b.String())
	if len(run.Snapshots) > volumeSnapshotMaxLines || len(run.Commands) > volumeSnapshotMaxLines {
		t.Fatalf("not bounded: %d snapshots, %d commands", len(run.Snapshots), len(run.Commands))
	}
}

func TestVolumeSnapshotsToPrune(t *testing.T) {
	at := func(s int64) time.Time { return time.Unix(s, 0) }
	snaps := []VolumeSnapshot{
		{Name: "repman-20261101T000000Z", Created: at(1), Managed: true},
		{Name: "daily-1", Created: at(2)},
		{Name: "repman-20261102T000000Z", Created: at(3), Managed: true},
		{Name: "repman-20261103T000000Z", Created: at(4), Managed: true},
		{Name: "repman-20261104T000000Z", Created: at(5), Managed: true},
		// a manual snapshot with the prefix is not replication-manager's: never pruned
		{Name: "repman-before-upgrade", Created: at(6), Managed: true},
	}
	got := volumeSnapshotsToPrune(snaps, 2)
	if strings.Join(got, ",") != "repman-20261102T000000Z,repman-20261101T000000Z" {
		t.Fatalf("prune = %v, want the managed ones beyond the 2 newest", got)
	}
	// a creation time the plugin could not give: the newest by its name is kept
	undated := []VolumeSnapshot{
		{Name: "repman-20261126T100000Z", Created: at(100), Managed: true},
		{Name: "repman-20261126T110000Z", Managed: true},
	}
	if got := volumeSnapshotsToPrune(undated, 1); strings.Join(got, ",") != "repman-20261126T100000Z" {
		t.Fatalf("an undated snapshot just taken was pruned: %v", got)
	}
	if got := volumeSnapshotsToPrune(snaps, 10); len(got) != 0 {
		t.Fatalf("nothing beyond keep: %v", got)
	}
}

func TestVolumeSnapshotPaths(t *testing.T) {
	_, server := newTestClusterServer(t)
	// the monitor keeps Variables uppercased (GetVariables): the paths come from the
	// case-preserving SensitiveVariables, never from them
	server.Variables = config.NewStringsMap()
	server.Variables.Set("DATADIR", "/SRV/DB/DATA/")
	server.SensitiveVariables = config.NewStringsMap()
	server.SensitiveVariables.Set("DATADIR", "/srv/db/data/")
	server.SensitiveVariables.Set("INNODB_LOG_GROUP_HOME_DIR", "/srv/db/redo")
	server.SensitiveVariables.Set("INNODB_UNDO_DIRECTORY", "./")
	server.SensitiveVariables.Set("LOG_BIN_BASENAME", "/srv/db/binlog/mysql-bin")
	got := strings.Join(server.volumeSnapshotPaths(), ":")
	if got != "/srv/db/data:/srv/db/redo:/srv/db/binlog" {
		t.Fatalf("paths = %s", got)
	}
}

func TestVolumeSnapshotterRefusals(t *testing.T) {
	cl, server := newTestClusterServer(t)
	if _, err := cl.VolumeSnapshotter(server); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("off: %v", err)
	}
	cl.Conf.BackupSnapshot = true
	cl.Conf.BackupSnapshotPlugin = "nosuch"
	if _, err := cl.VolumeSnapshotter(server); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown plugin: %v", err)
	}
	cl.Conf.BackupSnapshotPlugin = config.ConstSnapshotPluginZFS
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	if _, err := cl.VolumeSnapshotter(server); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("zfs on an orchestrator without host access: %v", err)
	}
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	if _, err := cl.VolumeSnapshotter(server); err == nil || !strings.Contains(err.Error(), "onpremise-ssh") {
		t.Fatalf("on-premise without SSH: %v", err)
	}
	cl.Conf.OnPremiseSSH = true
	vs, err := cl.VolumeSnapshotter(server)
	if err != nil {
		t.Fatalf("on-premise SSH: %v", err)
	}
	if _, ok := vs.(*contractSnapshotter).exec.(*sshSnapshotExecutor); !ok || vs.(*contractSnapshotter).exec.(*sshSnapshotExecutor).server != server {
		t.Fatalf("on-premise runs over SSH: %T", vs.(*contractSnapshotter).exec)
	}
	// the client's script wins over every plugin, on any orchestrator (F6)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cl.Conf.BackupSnapshotScript = "/usr/local/bin/my-snapshot"
	vs, err = cl.VolumeSnapshotter(server)
	if err != nil || vs.Manifest().Name != config.ConstSnapshotPluginScript {
		t.Fatalf("script override: %v %v", vs, err)
	}
	if p := vs.(*contractSnapshotter).exec.(*programSnapshotExecutor).program; p != "/usr/local/bin/my-snapshot" {
		t.Fatalf("script program: %s", p)
	}
}

func TestVolumeSnapshotterEnterpriseTier(t *testing.T) {
	cl, server := newTestClusterServer(t)
	if err := config.RegisterSnapshotPlugin(config.SnapshotPluginManifest{Name: "test-array", Tier: config.ConstArchiveTierEnterprise}, "/bin/true"); err != nil {
		t.Fatal(err)
	}
	cl.Conf.BackupSnapshot = true
	cl.Conf.BackupSnapshotPlugin = "test-array"
	if _, err := cl.VolumeSnapshotter(server); err == nil || !strings.Contains(err.Error(), "enterprise") {
		t.Fatalf("enterprise plugin without the plan: %v", err)
	}
	cl.Conf.Cloud18GitUser = "someone"
	cl.Conf.Cloud18SubscriptionPlan = "support"
	vs, err := cl.VolumeSnapshotter(server)
	if err != nil {
		t.Fatalf("enterprise plugin with the plan: %v", err)
	}
	if p := vs.(*contractSnapshotter).exec.(*programSnapshotExecutor).program; p != "/bin/true" {
		t.Fatalf("external program: %s", p)
	}
}

func TestSnapbackRefusedOnMaster(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.Conf.BackupSnapshot = true
	cl.master = server
	if err := cl.SnapbackVolume(server, "repman-1"); err == nil || !strings.Contains(err.Error(), "master") {
		t.Fatalf("snapback of the master: %v", err)
	}
	if err := cl.SnapbackVolume(server, "bad name;rm"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestSnapbackRefusedOnWriter(t *testing.T) {
	cl, server := newTestClusterServer(t)
	server.HaveReadOnly = false
	if why := cl.snapbackRefusal(server); !strings.Contains(why, "accepts writes") {
		t.Fatalf("a writable server: %q", why)
	}
	server.HaveReadOnly = true
	if why := cl.snapbackRefusal(server); why != "" {
		t.Fatalf("a read-only replica: %q", why)
	}
}

func TestDeleteVolumeSnapshotOnlyManaged(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.Conf.BackupSnapshot = true
	for _, name := range []string{"daily-1", "repman-before-upgrade"} {
		if err := cl.DeleteVolumeSnapshot(server, name); err == nil || !strings.Contains(err.Error(), "never deleted") {
			t.Fatalf("delete of a snapshot of another origin %s: %v", name, err)
		}
	}
}

func TestVolumeSnapshotOpOneAtATime(t *testing.T) {
	cl, server := newTestClusterServer(t)
	op, err := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionTake, "repman-1", "zfs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionDelete, "repman-1", "zfs"); err != errVolumeSnapshotBusy {
		t.Fatalf("second action while one runs: %v", err)
	}
	cl.endVolumeSnapshotOp(op, VolumeSnapshotRun{Commands: []string{"zfs snapshot -r tank/db@repman-1"}}, nil)
	got := cl.VolumeSnapshotOp(server)
	if got == nil || got.Status != VolumeSnapshotDone || len(got.Commands) != 1 {
		t.Fatalf("op = %+v", got)
	}
	op, _ = cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionSnapback, "repman-1", "zfs")
	cl.endVolumeSnapshotOp(op, VolumeSnapshotRun{Changed: true}, os.ErrPermission)
	if got := cl.VolumeSnapshotOp(server); got.Status != VolumeSnapshotHalfway {
		t.Fatalf("snapback failing after a change is half-way: %+v", got)
	}
}

func TestCheckVolumeSnapshotStates(t *testing.T) {
	cl, server := newTestClusterServer(t)
	cl.Conf.BackupSnapshot = true
	cl.Conf.BackupSnapshotPlugin = "nosuch"
	op, _ := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionTake, "repman-1", "zfs")
	cl.checkVolumeSnapshotStates()
	if !cl.StateMachine.IsInState("WARN0332") || !inServerState(cl, "CINF0024", server) {
		t.Fatalf("states: %v", cl.StateMachine.GetStates())
	}
	cl.endVolumeSnapshotOp(op, VolumeSnapshotRun{}, os.ErrNotExist)
	cl.checkVolumeSnapshotStates()
	if !inServerState(cl, "WARN0330", server) {
		t.Fatalf("failed action state: %v", cl.StateMachine.GetStates())
	}
	op, _ = cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionSnapback, "repman-1", "zfs")
	cl.endVolumeSnapshotOp(op, VolumeSnapshotRun{}, nil)
	if !cl.VolumeSnapshotOp(server).SnapbackDone {
		t.Fatal("a snapback done is marked with its status")
	}
	server.IsMaintenance = true
	cl.checkVolumeSnapshotStates()
	if !inServerState(cl, "WARN0331", server) {
		t.Fatalf("snapback in maintenance state: %v", cl.StateMachine.GetStates())
	}
	server.IsMaintenance = false
	cl.checkVolumeSnapshotStates()
	if cl.VolumeSnapshotOp(server).SnapbackDone {
		t.Fatal("out of maintenance: the snapback is over")
	}
}

// fakeZFS is a zfs and a findmnt of a test: the datasets and snapshots in files of dir.
const fakeZFS = `#!/bin/sh
D="$FAKE_DIR"
echo "zfs $*" >>"$D/calls"
last=""
for a in "$@"; do last="$a"; done
case "$1" in
snapshot)
    base="${last%@*}"; n="${last#*@}"
    grep -E "^$base(/|$)" "$D/datasets" | while read -r ds; do printf '%s@%s\t1700000300\t4096\n' "$ds" "$n" >>"$D/snaps"; done ;;
destroy)
    base="${last%@*}"; n="${last#*@}"
    grep -v -E "^$base(/[^@]*)?@$n	" "$D/snaps" >"$D/snaps.new"; mv "$D/snaps.new" "$D/snaps" ;;
rollback)
    [ -f "$D/rollback-fails" ] && grep -qx "${last%@*}" "$D/rollback-fails" && exit 1
    echo "$last" >>"$D/rolled" ;;
list)
    case "$*" in *"-t snapshot"*) [ -f "$D/list-fails" ] && { echo "sudo: a password is required" >&2; exit 1; } ;; esac
    case "$*" in
    *"name,clones"*) grep "^$last@" "$D/snaps" | while IFS="	" read -r n c u; do
        cl=$(awk -v n="$n" '$1 == n { print $2 }' "$D/clones" 2>/dev/null); printf '%s\t%s\n' "$n" "${cl:--}"; done ;;
    *"-t snapshot -d 1"*) grep "^$last@" "$D/snaps" || true ;;
    *"-t snapshot"*) grep "^$last	" "$D/snaps" ;;
    *"-r -t filesystem,volume"*) grep -E "^$last(/|$)" "$D/datasets" ;;
    *)
        case "$last" in
        *@*) grep -q "^$last	" "$D/snaps" || exit 1; echo "$last" ;;
        *) grep -qx "$last" "$D/datasets" || exit 1; echo "$last" ;;
        esac ;;
    esac ;;
esac
`

// fakeFindmnt: -T PATH prints the source and type of the mounts file line of PATH; --source DS
// prints the mount point of a "DS PATH zfs" line (a dataset mounted inside a server path).
const fakeFindmnt = `#!/bin/sh
source=""
prev=""
for a in "$@"; do
    [ "$prev" = "--source" ] && source="$a"
    prev="$a"; last="$a"
done
if [ -n "$source" ]; then
    awk -v s="$source" '$1 == s { print $2 }' "$FAKE_DIR/mounts"
else
    awk -v p="$last" '$1 == p { print $2 " " $3 }' "$FAKE_DIR/mounts"
fi
`

// fakeFuser: -V answers unless FAKE_DIR/fuser-broken exists; -m PATH is in use when PATH is a
// line of FAKE_DIR/busy.
// FAKE_DIR/busy-once is busy at the first -m, free after.
const fakeFuser = `#!/bin/sh
[ "$1" = "-V" ] && { [ -f "$FAKE_DIR/fuser-broken" ] && exit 1; exit 0; }
[ -f "$FAKE_DIR/busy-once" ] && { rm -f "$FAKE_DIR/busy-once"; exit 0; }
[ -f "$FAKE_DIR/busy" ] && grep -qx "$2" "$FAKE_DIR/busy" && exit 0
exit 1
`

// shSnapshotExecutor runs the embedded helper with sh and the fake tools of a test.
type shSnapshotExecutor struct{ bin, dir, busyWait string }

func (e *shSnapshotExecutor) Where() string { return "in a test" }

func (e *shSnapshotExecutor) Exec(ctx context.Context, action string, env []string) (snapshotOutput, error) {
	script, err := helperScript(env)
	if err != nil {
		return snapshotOutput{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-s", "--", action)
	busyWait := e.busyWait
	if busyWait == "" {
		busyWait = "0"
	}
	// the test's values last: they win over the contract's
	cmd.Env = append(append([]string{"PATH=" + e.bin + ":/usr/bin:/bin"}, env...), "REPMAN_SNAPSHOT_SUDO=", "REPMAN_SNAPSHOT_BUSY_WAIT="+busyWait, "FAKE_DIR="+e.dir)
	return runSnapshotCmd(cmd, script)
}

func newFakeZFSHost(t *testing.T) (*Cluster, *ServerMonitor, *contractSnapshotter, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "zfs"), []byte(fakeZFS), 0o755)
	os.WriteFile(filepath.Join(bin, "findmnt"), []byte(fakeFindmnt), 0o755)
	os.WriteFile(filepath.Join(bin, "fuser"), []byte(fakeFuser), 0o755)
	data, redo := filepath.Join(dir, "srv", "data"), filepath.Join(dir, "srv", "redo")
	os.MkdirAll(data, 0o755)
	os.MkdirAll(redo, 0o755)
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank/db\ntank/db/data\ntank/db/redo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "snaps"), []byte("tank/db@daily-1\t1700000100\t512\ntank/db/data@daily-1\t1700000100\t512\ntank/db/redo@daily-1\t1700000100\t512\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" tank/db/data zfs\n"+redo+" tank/db/redo zfs\n"), 0o644)

	cl, server := newTestClusterServer(t)
	cl.Conf.BackupSnapshot = true
	cl.Conf.BackupSnapshotPlugin = config.ConstSnapshotPluginZFS
	server.SensitiveVariables = config.NewStringsMap()
	server.SensitiveVariables.Set("DATADIR", data)
	server.SensitiveVariables.Set("INNODB_LOG_GROUP_HOME_DIR", redo)
	m, _ := config.SnapshotPlugin(config.ConstSnapshotPluginZFS)
	vs := &contractSnapshotter{manifest: m, exec: &shSnapshotExecutor{bin: bin, dir: dir}, timeout: time.Minute}
	return cl, server, vs, dir
}

func TestVolumeSnapshotHelperZFS(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)

	// the common dataset of the datadir and the redo directory, recursive
	run, err := vs.Take(server, "repman-20261126T100000Z")
	if err != nil {
		t.Fatalf("take: %v (%+v)", err, run)
	}
	if run.Target != "tank/db" {
		t.Fatalf("target = %q, want the parent of data and redo", run.Target)
	}
	if !containsString(run.Commands, "zfs snapshot -r tank/db@repman-20261126T100000Z") {
		t.Fatalf("take commands: %v", run.Commands)
	}
	list, err := vs.List(server)
	if err != nil || len(list.Snapshots) != 2 || list.Snapshots[1].Name != "repman-20261126T100000Z" || !list.Snapshots[1].Managed {
		t.Fatalf("list: %v %+v", err, list.Snapshots)
	}

	// a child without the snapshot: nothing changed
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank/db\ntank/db/data\ntank/db/redo\ntank/db/new\n"), 0o644)
	run, err = vs.Snapback(server, "daily-1")
	if err == nil || run.Changed || !strings.Contains(err.Error(), "tank/db/new") {
		t.Fatalf("snapback with a child created after: %v changed=%v", err, run.Changed)
	}
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank/db\ntank/db/data\ntank/db/redo\n"), 0o644)

	run, err = vs.Snapback(server, "daily-1")
	if err != nil || !run.Changed {
		t.Fatalf("snapback: %v %+v", err, run)
	}
	rolled, _ := os.ReadFile(filepath.Join(dir, "rolled"))
	if string(rolled) != "tank/db@daily-1\ntank/db/data@daily-1\ntank/db/redo@daily-1\n" {
		t.Fatalf("rolled back = %q", rolled)
	}

	// a clone of a snapshot taken after it: refused, nothing changed
	os.WriteFile(filepath.Join(dir, "clones"), []byte("tank/db/data@repman-20261126T100000Z tank/clone1\n"), 0o644)
	if run, err := vs.Snapback(server, "daily-1"); err == nil || run.Changed || !strings.Contains(err.Error(), "clones") {
		t.Fatalf("snapback over a clone: %v changed=%v", err, run.Changed)
	}
	os.Remove(filepath.Join(dir, "clones"))

	// a dataset still in use (the database flushing): refused, nothing changed
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(filepath.Join(dir, "srv", "data")+" tank/db/data zfs\n"+filepath.Join(dir, "srv", "redo")+" tank/db/redo zfs\ntank/db/data "+filepath.Join(dir, "srv", "data")+" zfs\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "busy"), []byte(filepath.Join(dir, "srv", "data")+"\n"), 0o644)
	if run, err := vs.Snapback(server, "daily-1"); err == nil || run.Changed || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("snapback of a busy volume: %v changed=%v", err, run.Changed)
	}
	os.Remove(filepath.Join(dir, "busy"))
	// fuser that does not run (sudo refusing it): refused, nothing changed
	os.WriteFile(filepath.Join(dir, "fuser-broken"), nil, 0o644)
	if run, err := vs.Snapback(server, "daily-1"); err == nil || run.Changed || !strings.Contains(err.Error(), "fuser") {
		t.Fatalf("snapback without a usable fuser: %v changed=%v", err, run.Changed)
	}
	os.Remove(filepath.Join(dir, "fuser-broken"))

	// a rollback failing on a child after the first: half-way
	os.WriteFile(filepath.Join(dir, "rollback-fails"), []byte("tank/db/redo\n"), 0o644)
	run, err = vs.Snapback(server, "daily-1")
	if err == nil || !run.Changed {
		t.Fatalf("half-way snapback: %v changed=%v", err, run.Changed)
	}

	if _, err := vs.Delete(server, "repman-20261126T100000Z"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := vs.List(server); len(list.Snapshots) != 1 {
		t.Fatalf("after delete: %+v", list.Snapshots)
	}
	if _, err := vs.Delete(server, "repman-nosuch"); err == nil {
		t.Fatal("delete of a missing snapshot must fail")
	}
}

// fakeLVS: vg0/data, a thick volume with one snapshot repman-20261126T100000Z
const fakeLVS = `#!/bin/sh
case "$*" in
*"vg_name,lv_name"*)
    for x in "$@"; do last="$x"; done
    case "$last" in vg0/data | /dev/vg0/data | /dev/mapper/vg0-data) echo "  vg0 data" ;; vg0/other | /dev/vg0/other) echo "  vg0 other" ;; esac ;;
*"lv_path"*) echo "  /dev/vg0/data" ;;
*"segtype"*) echo "  linear" ;;
*"lv_name,origin,lv_time,lv_size"*)
    [ -f "$FAKE_DIR/lvm-empty" ] && { echo "  data||2026-11-01 10:00:00 +0000|10737418240"; exit 0; }
    echo "  data||2026-11-01 10:00:00 +0000|10737418240"; echo "  repman-20261126T100000Z|data|2026-11-26 10:00:00 +0000|1073741824"; echo "  zz-manual|data|2026-11-26 11:00:00 +0000|1073741824" ;;
esac
`

func TestVolumeSnapshotHelperLVMNotMountedRefused(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	os.WriteFile(filepath.Join(dir, "bin", "lvs"), []byte(fakeLVS), 0o755)
	m, _ := config.SnapshotPlugin(config.ConstSnapshotPluginLVM)
	vs.manifest = m
	server.ClusterGroup.Conf.BackupSnapshotPlugin = config.ConstSnapshotPluginLVM
	data, redo := filepath.Join(dir, "srv", "data"), filepath.Join(dir, "srv", "redo")
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" /dev/mapper/vg0-data ext4\n"+redo+" /dev/mapper/vg0-data ext4\n"), 0o644)
	// a named volume that is not the one of the server's files: refused
	server.ClusterGroup.Conf.BackupSnapshotTarget = "vg0/other"
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "not the logical volume") {
		t.Fatalf("named LV not holding the server's files: %v", err)
	}
	server.ClusterGroup.Conf.BackupSnapshotTarget = "vg0/data"
	os.WriteFile(filepath.Join(bin(dir), "lvcreate"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	list, err := vs.List(server)
	if err != nil || list.Target != "vg0/data" || len(list.Snapshots) != 2 {
		t.Fatalf("lvm list: %v %+v", err, list)
	}
	// the snapshot taken is not the last row (a manual one sorts after it): still a success
	run, err := vs.Take(server, "repman-20261126T100000Z")
	if err != nil || len(run.Snapshots) != 1 {
		t.Fatalf("lvm take, its row not the last: %v %+v", err, run)
	}
	// no snapshot yet: an empty list, not an error
	os.WriteFile(filepath.Join(dir, "lvm-empty"), nil, 0o644)
	if list, err := vs.List(server); err != nil || len(list.Snapshots) != 0 {
		t.Fatalf("empty lvm list: %v %+v", err, list)
	}
	os.Remove(filepath.Join(dir, "lvm-empty"))
	// findmnt does not tie a mount to /dev/vg0/data (no --source line): refused before any change
	run, err = vs.Snapback(server, "repman-20261126T100000Z")
	if err == nil || run.Changed || !strings.Contains(err.Error(), "no mount point") {
		t.Fatalf("lvm snapback without a mount point: %v changed=%v", err, run.Changed)
	}
}

func TestVolumeSnapshotHelperWaitsForTheVolume(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	vs.exec.(*shSnapshotExecutor).busyWait = "10"
	os.WriteFile(filepath.Join(dir, "busy-once"), nil, 0o644)
	start := time.Now()
	run, err := vs.Snapback(server, "daily-1")
	if err != nil || !run.Changed {
		t.Fatalf("snapback after the volume got free: %v %+v", err, run)
	}
	if time.Since(start) < 2*time.Second {
		t.Fatal("the busy volume was not waited for")
	}
}

func TestVolumeSnapshotBusyWaitWithinTheDeadline(t *testing.T) {
	for timeout, want := range map[time.Duration]int{30 * time.Second: 10, 30 * time.Minute: 120, 0: 0} {
		if got := volumeSnapshotBusyWait(timeout); got != want {
			t.Errorf("busy wait of %s = %d, want %d", timeout, got, want)
		}
	}
}

func TestVolumeSnapshotHelperEmptyZFSList(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	os.WriteFile(filepath.Join(dir, "snaps"), nil, 0o644)
	if list, err := vs.List(server); err != nil || len(list.Snapshots) != 0 {
		t.Fatalf("no snapshot yet: an empty list, not an error: %v %+v", err, list)
	}
}

func TestVolumeSnapshotHelperListFailureIsAnError(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	os.WriteFile(filepath.Join(dir, "list-fails"), nil, 0o644)
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "zfs list") {
		t.Fatalf("a failing zfs list must be an error, not an empty list: %v", err)
	}
}

func TestVolumeSnapshotHelperNotZFS(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	data := filepath.Join(dir, "srv", "data")
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" /dev/sda1 ext4\n"), 0o644)
	server.SensitiveVariables.Set("INNODB_LOG_GROUP_HOME_DIR", data)
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "not on ZFS") {
		t.Fatalf("datadir on ext4: %v", err)
	}
}

func TestPruneVolumeSnapshots(t *testing.T) {
	cl, server, vs, dir := newFakeZFSHost(t)
	cl.Conf.BackupSnapshotKeep = 1
	for _, n := range []string{"repman-20261126T100000Z", "repman-20261126T110000Z"} {
		if _, err := vs.Take(server, n); err != nil {
			t.Fatal(err)
		}
	}
	// same creation time in the fake: the name orders them, repman-2 is the newest
	run, err := cl.pruneVolumeSnapshots(server, vs)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !containsString(run.Commands, "zfs destroy -r tank/db@repman-20261126T100000Z") {
		t.Fatalf("prune commands: %v", run.Commands)
	}
	snaps, _ := os.ReadFile(filepath.Join(dir, "snaps"))
	if strings.Contains(string(snaps), "@repman-20261126T100000Z\t") || !strings.Contains(string(snaps), "@daily-1\t") || !strings.Contains(string(snaps), "@repman-20261126T110000Z\t") {
		t.Fatalf("after prune: %s", snaps)
	}
}

func inServerState(cl *Cluster, key string, server *ServerMonitor) bool {
	return cl.StateMachine.OldState.Search(state.BuildStateKey(key, server.URL))
}

func bin(dir string) string { return filepath.Join(dir, "bin") }

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// stubExecutor answers a run with a fixed output, or blocks until the deadline.
type stubExecutor struct {
	out   snapshotOutput
	err   error
	block bool
}

func (e *stubExecutor) Where() string { return "stub" }

func (e *stubExecutor) Exec(ctx context.Context, action string, env []string) (snapshotOutput, error) {
	if e.block {
		<-ctx.Done()
		return e.out, ctx.Err()
	}
	return e.out, e.err
}

func TestSnapbackTimeoutIsHalfway(t *testing.T) {
	_, server := newTestClusterServer(t)
	m, _ := config.SnapshotPlugin(config.ConstSnapshotPluginZFS)
	vs := &contractSnapshotter{manifest: m, exec: &stubExecutor{block: true}, timeout: 50 * time.Millisecond}
	run, err := vs.Snapback(server, "repman-1")
	if err == nil || !run.Changed {
		t.Fatalf("a snapback stopped by its deadline is half-way: %v changed=%v", err, run.Changed)
	}
	// a take stopped by its deadline is a plain failure
	if run, err := vs.Take(server, "repman-2"); err == nil || run.Changed {
		t.Fatalf("take at its deadline: %v changed=%v", err, run.Changed)
	}
	// an output cut short: CHANGED may be past the cut
	vs.exec = &stubExecutor{out: snapshotOutput{stdout: "CMD\tzfs\n", truncated: true}, err: os.ErrInvalid}
	if run, _ := vs.Snapback(server, "repman-1"); !run.Changed {
		t.Fatal("a failed snapback with a truncated output is half-way")
	}
	// a plain refusal before any change: not half-way, the error is the last line of stderr
	vs.exec = &stubExecutor{out: snapshotOutput{stderr: "noise\nsnapshot not found\n"}, err: os.ErrInvalid}
	run, err = vs.Snapback(server, "repman-1")
	if run.Changed || err == nil || err.Error() != "snapshot not found" {
		t.Fatalf("refusal: %v changed=%v", err, run.Changed)
	}
}

func TestParseVolumeSnapshotOutputChangedAfterManyLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < volumeSnapshotMaxLines*5; i++ {
		b.WriteString("log line\n")
	}
	b.WriteString("CHANGED\n")
	if !parseVolumeSnapshotOutput(b.String()).Changed {
		t.Fatal("CHANGED after many log lines is read")
	}
}

func TestParseVolumeSnapshotOutputIncomplete(t *testing.T) {
	if run := parseVolumeSnapshotOutput(strings.Repeat("x", 2<<20)); !run.incomplete {
		t.Fatal("a line past the scanner's limit makes the output incomplete")
	}
}

func TestLimitedBufferTruncated(t *testing.T) {
	var b limitedBuffer
	b.Write(make([]byte, volumeSnapshotOutputMax-1))
	if b.Truncated() {
		t.Fatal("not yet")
	}
	b.Write([]byte("ab"))
	if !b.Truncated() || len(b.String()) != volumeSnapshotOutputMax {
		t.Fatalf("truncated=%v len=%d", b.Truncated(), len(b.String()))
	}
}

func TestVolumeSnapshotOpPanicEndsTheOp(t *testing.T) {
	cl, server := newTestClusterServer(t)
	op, _ := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionSnapback, "repman-1", "zfs")
	done := make(chan struct{})
	cl.goVolumeSnapshotOp(server, op, func() (VolumeSnapshotRun, error) {
		defer close(done)
		var m map[string]int
		m["x"] = 1 // a panic
		return VolumeSnapshotRun{}, nil
	})
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := cl.VolumeSnapshotOp(server); got != nil && got.Status != VolumeSnapshotRunning {
			if got.Status != VolumeSnapshotHalfway || !strings.Contains(got.Error, "panic") {
				t.Fatalf("a snapback ending in a panic is half-way: %+v", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the op stayed running after a panic")
}

func TestReleaseVolumeSnapshotOp(t *testing.T) {
	cl, server := newTestClusterServer(t)
	first, _ := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionTake, "repman-1", "zfs")
	cl.endVolumeSnapshotOp(first, VolumeSnapshotRun{}, nil)
	op, _ := cl.beginVolumeSnapshotOp(server, config.ConstSnapshotActionSnapback, "repman-1", "zfs")
	cl.releaseVolumeSnapshotOp(op)
	if got := cl.VolumeSnapshotOp(server); got == nil || got.Action != config.ConstSnapshotActionTake || got.Status != VolumeSnapshotDone {
		t.Fatalf("released: the previous action is back: %+v", got)
	}
}

func TestVolumeSnapshotHelperZFSPoolRootRefused(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	data, redo := filepath.Join(dir, "srv", "data"), filepath.Join(dir, "srv", "redo")
	// the datadir on tank/mysql/data, the redo log on tank/binlog: the common parent is the pool
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank\ntank/mysql\ntank/mysql/data\ntank/binlog\ntank/backups\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" tank/mysql/data zfs\n"+redo+" tank/binlog zfs\n"), 0o644)
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "dataset tank (found) is the root of a pool") {
		t.Fatalf("pool root found on a guess: %v", err)
	}
	// a sibling dataset of something else under the common parent
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank/db\ntank/db/data\ntank/db/redo\ntank/db/other\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" tank/db/data zfs\n"+redo+" tank/db/redo zfs\n"), 0o644)
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "tank/db/other") {
		t.Fatalf("a tree with a dataset of something else: %v", err)
	}
	// mounted inside the datadir, it belongs to the server
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" tank/db/data zfs\n"+redo+" tank/db/redo zfs\ntank/db/other "+data+"/.system/tmp zfs\n"), 0o644)
	if run, err := vs.List(server); err != nil || run.Target != "tank/db" {
		t.Fatalf("a dataset mounted in the datadir: %v %+v", err, run)
	}
}

// a named target goes through the checks of a found one (the bug of the first real-host run:
// backup-snapshot-target "data", a pool root, snapshotted the whole pool)
func TestVolumeSnapshotHelperZFSNamedTargetChecked(t *testing.T) {
	_, server, vs, dir := newFakeZFSHost(t)
	data, redo := filepath.Join(dir, "srv", "data"), filepath.Join(dir, "srv", "redo")
	cl := server.ClusterGroup
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" data/db/data zfs\n"+redo+" data/db/redo zfs\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("data\ndata/db\ndata/db/data\ndata/db/redo\ndata/gitlab\n"), 0o644)
	for target, want := range map[string]string{
		"data":         "root of a pool", // the pool root
		"data/gitlab":  "does not hold",  // a dataset not holding the server's files
		"data/db/data": "does not hold",  // holds the datadir, not the redo log
		"data/nosuch":  "not found",      // missing
	} {
		cl.Conf.BackupSnapshotTarget = target
		if _, err := vs.Take(server, "repman-20261126T100000Z"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("named target %s: %v, want %q", target, err, want)
		}
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Contains(string(calls), "zfs snapshot") {
		t.Fatalf("a refused target was snapshotted: %s", calls)
	}
	// the datadir and the redo log on siblings under the pool root: only the root holds both,
	// named or not it is refused
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" tank/mysql/data zfs\n"+redo+" tank/binlog zfs\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("tank\ntank/mysql\ntank/mysql/data\ntank/binlog\n"), 0o644)
	cl.Conf.BackupSnapshotTarget = "tank"
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "dataset tank (named) is the root of a pool") {
		t.Fatalf("named pool root holding every path: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "mounts"), []byte(data+" data/db/data zfs\n"+redo+" data/db/redo zfs\n"), 0o644)
	// a foreign dataset under the named one
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("data\ndata/db\ndata/db/data\ndata/db/redo\ndata/db/other\n"), 0o644)
	cl.Conf.BackupSnapshotTarget = "data/db"
	if _, err := vs.List(server); err == nil || !strings.Contains(err.Error(), "data/db/other") {
		t.Fatalf("named target with a foreign dataset: %v", err)
	}
	// the dataset holding every path and only them: accepted
	os.WriteFile(filepath.Join(dir, "datasets"), []byte("data\ndata/db\ndata/db/data\ndata/db/redo\n"), 0o644)
	if run, err := vs.List(server); err != nil || run.Target != "data/db" {
		t.Fatalf("named target holding the server's files: %v %+v", err, run)
	}
}

// the SSH executor's quoting keeps a value with line breaks (the mount pairs) and quotes whole
func TestShellQuoteKeepsLineBreaks(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	v := "/srv/mysql|/dev/mapper/mpatha\n/srv/mysql/binlog|/dev/mapper/mpathb\nit's"
	out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(v)).Output()
	if err != nil || string(out) != v {
		t.Fatalf("%q -> %q %v", v, out, err)
	}
}
