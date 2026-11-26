// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"golang.org/x/crypto/ssh"
)

// Volume snapshots (#2443, doc/implementation/cluster/VOLUME_SNAPSHOTS.md): a snapshot of the
// volume that holds a database server's files, taken, listed, deleted, and put back
// (snapback). One interface (VolumeSnapshotter), the plugins of config/snapshot.go behind it.
// Every plugin speaks the same contract (share/scripts/volume_snapshot.sh): only where its
// program runs differs (snapshotExecutor).
//
// A snapback is destructive: it is only an operator's action (API, GUI), never started by
// replication-manager, refused on the master; the server goes into maintenance, is stopped
// through its orchestrator, its volume rolled back, started, and left in maintenance with a
// state (WARN0331) until the operator takes it out. The replication links of the server are
// not touched (T27): the volume carries the positions of its own data.

// VolumeSnapshot is one snapshot of a server's volume.
type VolumeSnapshot struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`    // bytes: the space it holds (ZFS), its copy-on-write space (LVM)
	Managed bool      `json:"managed"` // taken by replication-manager: pruned by backup-snapshot-keep, deletable
}

// VolumeSnapshotRun is what a plugin run reported.
type VolumeSnapshotRun struct {
	Target    string           `json:"target"`
	Snapshots []VolumeSnapshot `json:"snapshots"`
	Commands  []string         `json:"commands"` // every command it ran, replayable by hand (F11)
	Changed   bool             `json:"changed"`  // a snapback changed the volume
	Log       []string         `json:"log"`
	// incomplete: the output could not be read to its end (a line past the scanner's limit)
	incomplete bool
}

// VolumeSnapshotter is the interface of a snapshot plugin (T7): one implementation per way of
// running the contract; the storage backends (zfs, lvm, a client script, an external plugin)
// are its manifests.
type VolumeSnapshotter interface {
	Manifest() config.SnapshotPluginManifest
	Take(server *ServerMonitor, name string) (VolumeSnapshotRun, error)
	List(server *ServerMonitor) (VolumeSnapshotRun, error)
	Delete(server *ServerMonitor, name string) (VolumeSnapshotRun, error)
	Snapback(server *ServerMonitor, name string) (VolumeSnapshotRun, error)
}

// snapshotOutput is what a run printed: the head of its standard output (the records, cut at
// volumeSnapshotOutputMax, truncated then), the tail of its standard error (the error).
type snapshotOutput struct {
	stdout    string
	truncated bool
	stderr    string
}

// snapshotExecutor runs the contract's program for one action and returns its output.
type snapshotExecutor interface {
	Exec(ctx context.Context, action string, env []string) (snapshotOutput, error)
	Where() string // for the log: "on db1 over SSH", "with /usr/local/bin/snap.sh"
}

// contractSnapshotter implements VolumeSnapshotter over a snapshotExecutor.
type contractSnapshotter struct {
	manifest config.SnapshotPluginManifest
	exec     snapshotExecutor
	timeout  time.Duration
}

func (c *contractSnapshotter) Manifest() config.SnapshotPluginManifest { return c.manifest }

func (c *contractSnapshotter) Take(server *ServerMonitor, name string) (VolumeSnapshotRun, error) {
	return c.run(server, config.ConstSnapshotActionTake, name)
}

func (c *contractSnapshotter) List(server *ServerMonitor) (VolumeSnapshotRun, error) {
	return c.run(server, config.ConstSnapshotActionList, "")
}

func (c *contractSnapshotter) Delete(server *ServerMonitor, name string) (VolumeSnapshotRun, error) {
	return c.run(server, config.ConstSnapshotActionDelete, name)
}

func (c *contractSnapshotter) Snapback(server *ServerMonitor, name string) (VolumeSnapshotRun, error) {
	return c.run(server, config.ConstSnapshotActionSnapback, name)
}

func (c *contractSnapshotter) run(server *ServerMonitor, action, name string) (VolumeSnapshotRun, error) {
	if !c.manifest.Can(action) {
		return VolumeSnapshotRun{}, fmt.Errorf("snapshot plugin %s does not implement %s", c.manifest.Name, action)
	}
	if name != "" {
		if err := config.ValidSnapshotName(name); err != nil {
			return VolumeSnapshotRun{}, err
		}
	}
	cluster := server.ClusterGroup
	env := server.volumeSnapshotEnv(c.manifest, action, name)
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot %s of %s (plugin %s) %s", action, server.URL, c.manifest.Name, c.exec.Where())
	out, err := c.exec.Exec(ctx, action, env)
	run := parseVolumeSnapshotOutput(out.stdout)
	for _, cmd := range run.Commands {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot %s of %s ran: %s", action, server.URL, cmd)
	}
	if action == config.ConstSnapshotActionSnapback && err != nil && (ctx.Err() != nil || out.truncated || run.incomplete) {
		// what ran on the volume is not known (a deadline: the remote program may still run;
		// an output cut short: a CHANGED past the cut): half-way, the server is never started
		run.Changed = true
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return run, fmt.Errorf("stopped after %s (backup-snapshot-timeout)", c.timeout)
		}
		if msg := strings.TrimSpace(lastLine(strings.TrimSpace(out.stderr))); msg != "" {
			return run, errors.New(msg)
		}
		return run, err
	}
	return run, nil
}

// volumeSnapshotMaxLines bounds what a run's output keeps (F4): a plugin printing without end
// does not grow the memory of replication-manager.
const volumeSnapshotMaxLines = 512

// parseVolumeSnapshotOutput reads the contract's records; other lines are log text.
func parseVolumeSnapshotOutput(out string) VolumeSnapshotRun {
	run := VolumeSnapshotRun{Snapshots: []VolumeSnapshot{}, Commands: []string{}, Log: []string{}}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		f := strings.Split(line, "\t")
		switch f[0] {
		case "CMD":
			if len(f) > 1 && len(run.Commands) < volumeSnapshotMaxLines {
				run.Commands = append(run.Commands, strings.Join(f[1:], " "))
			}
		case "TARGET":
			if len(f) > 1 {
				run.Target = f[1]
			}
		case "CHANGED":
			run.Changed = true
		case "SNAPSHOT":
			if len(f) < 2 || f[1] == "" || len(run.Snapshots) >= volumeSnapshotMaxLines {
				continue
			}
			s := VolumeSnapshot{Name: f[1], Managed: strings.HasPrefix(f[1], config.ConstSnapshotPrefix)}
			if len(f) > 2 {
				if t, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64); err == nil && t > 0 {
					s.Created = time.Unix(t, 0).UTC()
				}
			}
			if len(f) > 3 {
				s.Size, _ = strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64)
			}
			run.Snapshots = append(run.Snapshots, s)
		default:
			if strings.TrimSpace(line) != "" && len(run.Log) < volumeSnapshotMaxLines {
				run.Log = append(run.Log, line)
			}
		}
	}
	if sc.Err() != nil {
		run.incomplete = true
	}
	sort.SliceStable(run.Snapshots, func(i, j int) bool { return run.Snapshots[i].Created.Before(run.Snapshots[j].Created) })
	return run
}

// volumeSnapshotPaths are the directories of the server's own files, from its variables: the
// datadir, the redo, undo and InnoDB data directories, the binary logs. A relative one is
// under the datadir. The helper snapshots the volume holding all of them.
func (server *ServerMonitor) volumeSnapshotPaths() []string {
	if server.Variables == nil {
		return nil
	}
	datadir := ""
	if v, ok := server.Variables.CheckAndGet("DATADIR"); ok {
		datadir = filepath.Clean(strings.TrimSpace(v))
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			if datadir == "" {
				return
			}
			p = filepath.Join(datadir, p)
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(datadir)
	for _, v := range []string{"INNODB_LOG_GROUP_HOME_DIR", "INNODB_UNDO_DIRECTORY", "INNODB_DATA_HOME_DIR"} {
		if val, ok := server.Variables.CheckAndGet(v); ok {
			add(val)
		}
	}
	if val, ok := server.Variables.CheckAndGet("LOG_BIN_BASENAME"); ok && strings.TrimSpace(val) != "" {
		add(filepath.Dir(strings.TrimSpace(val)))
	}
	return out
}

// volumeSnapshotEnv is the context of a run: REPMAN_SNAPSHOT_* (the contract), and who the
// server is. No secret: the helper runs as the SSH user, a client script gets the API
// credentials of every replication-manager script (GetExecEnv) from its executor.
func (server *ServerMonitor) volumeSnapshotEnv(m config.SnapshotPluginManifest, action, name string) []string {
	cluster := server.ClusterGroup
	typ := m.Name
	if m.Name == config.ConstSnapshotPluginScript || m.External {
		// a script or a plugin is told which built-in backend the cluster names, if any
		typ = strings.TrimSpace(cluster.Conf.BackupSnapshotPlugin)
	}
	return []string{
		"REPMAN_SNAPSHOT_ACTION=" + action,
		"REPMAN_SNAPSHOT_TYPE=" + typ,
		"REPMAN_SNAPSHOT_TARGET=" + strings.TrimSpace(cluster.Conf.BackupSnapshotTarget),
		"REPMAN_SNAPSHOT_PATHS=" + strings.Join(server.volumeSnapshotPaths(), ":"),
		"REPMAN_SNAPSHOT_NAME=" + name,
		"REPMAN_SNAPSHOT_PREFIX=" + config.ConstSnapshotPrefix,
		"REPMAN_SNAPSHOT_LVM_SIZE=" + strings.TrimSpace(cluster.Conf.BackupSnapshotLVMSize),
		"REPMAN_CLUSTER=" + cluster.Name,
		"REPMAN_SERVER_HOST=" + misc.Unbracket(server.Host),
		"REPMAN_SERVER_PORT=" + server.Port,
	}
}

// shellQuote quotes s as one word of a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// helperScript is the helper fed to sh on the database host: the context exported first.
func helperScript(env []string) (io.Reader, error) {
	body, err := share.EmbededDbModuleFS.ReadFile("scripts/volume_snapshot.sh")
	if err != nil {
		return nil, fmt.Errorf("volume snapshot helper not embedded: %w", err)
	}
	var head bytes.Buffer
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&head, "%s=%s; export %s\n", k, shellQuote(v), k)
	}
	return io.MultiReader(&head, bytes.NewReader(body)), nil
}

// sshSnapshotExecutor runs the helper on the database host over the on-premise SSH
// connection (onpremise-ssh, onpremise-ssh-credential): sh -s, the script on its input.
type sshSnapshotExecutor struct{ server *ServerMonitor }

func (e *sshSnapshotExecutor) Where() string {
	return "on " + e.server.Host + " over SSH"
}

func (e *sshSnapshotExecutor) Exec(ctx context.Context, action string, env []string) (snapshotOutput, error) {
	client, err := e.server.ClusterGroup.OnPremiseConnect(e.server)
	if err != nil {
		return snapshotOutput{}, err
	}
	defer client.Close()
	script, err := helperScript(env)
	if err != nil {
		return snapshotOutput{}, err
	}
	session, err := client.UnderlyingClient().NewSession()
	if err != nil {
		return snapshotOutput{}, err
	}
	defer session.Close()
	var stdout limitedBuffer
	var stderr capBuffer
	session.Stdin, session.Stdout, session.Stderr = script, &stdout, &stderr
	done := make(chan error, 1)
	go func() { done <- session.Run("sh -s -- " + shellQuote(action)) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// the deadline kills the remote run where the server honours the signal, then closes
		// the connection: the run never holds its caller past it (F2). A remote program that
		// outlives it is possible: a snapback stopped this way is half-way (run).
		session.Signal(ssh.SIGKILL)
		client.Close()
		err = ctx.Err()
	}
	return snapshotOutput{stdout: stdout.String(), truncated: stdout.Truncated(), stderr: stderr.String()}, err
}

// localSnapshotExecutor runs the helper on this host: the localhost orchestrator, whose
// database servers run here.
type localSnapshotExecutor struct{}

func (e *localSnapshotExecutor) Where() string { return "on the replication-manager host" }

func (e *localSnapshotExecutor) Exec(ctx context.Context, action string, env []string) (snapshotOutput, error) {
	script, err := helperScript(env)
	if err != nil {
		return snapshotOutput{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-s", "--", action)
	cmd.Env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, env...)
	return runSnapshotCmd(cmd, script)
}

// programSnapshotExecutor runs a program on this host with the action as its argument: the
// client's backup-snapshot-script (with the environment of every replication-manager script),
// or an external plugin (with nothing of this process).
type programSnapshotExecutor struct {
	program string
	baseEnv []string
}

func (e *programSnapshotExecutor) Where() string { return "with " + e.program }

func (e *programSnapshotExecutor) Exec(ctx context.Context, action string, env []string) (snapshotOutput, error) {
	cmd := exec.CommandContext(ctx, e.program, action)
	cmd.Env = append(append([]string{}, e.baseEnv...), env...)
	return runSnapshotCmd(cmd, nil)
}

func runSnapshotCmd(cmd *exec.Cmd, stdin io.Reader) (snapshotOutput, error) {
	var stdout limitedBuffer
	var stderr capBuffer // its tail: the error is the last line
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second // a child keeping the pipes open does not hold the run
	err := cmd.Run()
	return snapshotOutput{stdout: stdout.String(), truncated: stdout.Truncated(), stderr: stderr.String()}, err
}

// limitedBuffer keeps the first volumeSnapshotOutputMax bytes written to it (F4): the records,
// printed first. capBuffer (the tail) is the one for standard error.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

// Truncated tells whether bytes were dropped.
func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

const volumeSnapshotOutputMax = 1 << 20

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := volumeSnapshotOutputMax - b.buf.Len()
	switch {
	case room <= 0:
		b.truncated = b.truncated || len(p) > 0
	case len(p) > room:
		b.buf.Write(p[:room])
		b.truncated = true
	default:
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// VolumeSnapshotter is the cluster's snapshot plugin for server, or why there is none:
// backup-snapshot off, a plugin refused (unknown, enterprise without the plan), or no way to
// reach the database host for a plugin that runs there.
func (cluster *Cluster) VolumeSnapshotter(server *ServerMonitor) (VolumeSnapshotter, error) {
	m, err := cluster.volumeSnapshotRefusal()
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cluster.Conf.BackupSnapshotTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	var ex snapshotExecutor
	switch {
	case m.Name == config.ConstSnapshotPluginScript:
		ex = &programSnapshotExecutor{program: strings.TrimSpace(cluster.Conf.BackupSnapshotScript), baseEnv: cluster.GetExecEnv()}
	case m.External:
		// nothing of this process: PATH, HOME and the wire version only (archivePluginBaseEnv)
		ex = &programSnapshotExecutor{program: m.Binary, baseEnv: archivePluginBaseEnv()}
	case cluster.GetOrchestrator() == config.ConstOrchestratorLocalhost:
		ex = &localSnapshotExecutor{}
	default:
		ex = &sshSnapshotExecutor{server: server}
	}
	return &contractSnapshotter{manifest: m, exec: ex, timeout: timeout}, nil
}

// volumeSnapshotRefusal is the cluster's snapshot plugin, or why it can not run: backup-snapshot
// off, a plugin refused (unknown, enterprise without the plan), or no way to reach the database
// host for a plugin that runs there. No server needed: the state check reads it every tick.
func (cluster *Cluster) volumeSnapshotRefusal() (config.SnapshotPluginManifest, error) {
	if !cluster.Conf.BackupSnapshot {
		return config.SnapshotPluginManifest{}, errors.New("volume snapshots are off (backup-snapshot)")
	}
	m, err := cluster.Conf.SnapshotPluginAllowed()
	if err != nil {
		return m, err
	}
	if m.Run != config.ConstSnapshotRunDatabaseHost || m.External || m.Name == config.ConstSnapshotPluginScript {
		return m, nil
	}
	switch orch := cluster.GetOrchestrator(); orch {
	case config.ConstOrchestratorOnPremise:
		if !cluster.Conf.OnPremiseSSH {
			return m, fmt.Errorf("snapshot plugin %s runs on the database host over SSH: onpremise-ssh is off", m.Name)
		}
		return m, nil
	case config.ConstOrchestratorLocalhost:
		return m, nil
	default:
		return m, fmt.Errorf("snapshot plugin %s runs on the database host, which orchestrator %s gives no access to: use the on-premise or localhost orchestrator, or a backup-snapshot-script", m.Name, orch)
	}
}

// Volume snapshot action statuses.
const (
	VolumeSnapshotRunning = "running"
	VolumeSnapshotDone    = "done"
	VolumeSnapshotFailed  = "failed"
	VolumeSnapshotHalfway = "halfway" // a snapback that changed the volume, then failed
)

// VolumeSnapshotOp is the last volume snapshot action of a server.
type VolumeSnapshotOp struct {
	Server   string    `json:"server"`
	Action   string    `json:"action"`
	Snapshot string    `json:"snapshot"`
	Plugin   string    `json:"plugin"`
	Status   string    `json:"status"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Error    string    `json:"error"`
	Commands []string  `json:"commands"`
	// a snapback done: the server stays in maintenance until an operator takes it out (WARN0331)
	SnapbackDone bool `json:"snapbackDone"`
	// the server's previous last action, put back when this one is released before it ran
	prev *VolumeSnapshotOp
}

// volumeSnapshots: the last action of each server, one entry per server (bounded by the
// servers, F4), in memory: a state of this replication-manager, rebuilt by the next action.
type volumeSnapshots struct {
	mu  sync.Mutex
	ops map[string]*VolumeSnapshotOp
}

var errVolumeSnapshotBusy = errors.New("a volume snapshot action is already running on this server")

// beginVolumeSnapshotOp claims the server for one action: one at a time per server.
func (cluster *Cluster) beginVolumeSnapshotOp(server *ServerMonitor, action, snapshot, plugin string) (*VolumeSnapshotOp, error) {
	cluster.vsnap.mu.Lock()
	defer cluster.vsnap.mu.Unlock()
	if cluster.vsnap.ops == nil {
		cluster.vsnap.ops = map[string]*VolumeSnapshotOp{}
	}
	old := cluster.vsnap.ops[server.URL]
	if old != nil && old.Status == VolumeSnapshotRunning {
		return nil, errVolumeSnapshotBusy
	}
	op := &VolumeSnapshotOp{Server: server.URL, Action: action, Snapshot: snapshot, Plugin: plugin, Status: VolumeSnapshotRunning, Start: time.Now(), Commands: []string{}, prev: old}
	cluster.vsnap.ops[server.URL] = op
	return op, nil
}

// releaseVolumeSnapshotOp gives the server back without an outcome: an action refused by its
// checks after it claimed the server, before it ran anything. The previous action is back.
func (cluster *Cluster) releaseVolumeSnapshotOp(op *VolumeSnapshotOp) {
	cluster.vsnap.mu.Lock()
	defer cluster.vsnap.mu.Unlock()
	if cluster.vsnap.ops[op.Server] != op {
		return
	}
	if op.prev != nil {
		cluster.vsnap.ops[op.Server] = op.prev
	} else {
		delete(cluster.vsnap.ops, op.Server)
	}
}

// goVolumeSnapshotOp runs an action in its own goroutine and ends its op whatever happens: a
// panic is recovered (F2: never the monitor's death, never a server claimed for good) and ends
// it failed, or half-way for a snapback, whose volume is then in an unknown state.
func (cluster *Cluster) goVolumeSnapshotOp(server *ServerMonitor, op *VolumeSnapshotOp, fn func() (VolumeSnapshotRun, error)) {
	go func() {
		var run VolumeSnapshotRun
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("volume snapshot %s stopped by a panic: %v", op.Action, r)
				if op.Action == config.ConstSnapshotActionSnapback {
					run.Changed = true
				}
			}
			cluster.logVolumeSnapshotEnd(server, op, err)
			cluster.endVolumeSnapshotOp(op, run, err)
		}()
		run, err = fn()
	}()
}

func (cluster *Cluster) endVolumeSnapshotOp(op *VolumeSnapshotOp, run VolumeSnapshotRun, err error) {
	cluster.vsnap.mu.Lock()
	defer cluster.vsnap.mu.Unlock()
	op.End = time.Now()
	// set with the status: a tick never sees a snapback done without its WARN0331
	op.SnapbackDone = err == nil && op.Action == config.ConstSnapshotActionSnapback
	op.Commands = append(op.Commands, run.Commands...)
	if len(op.Commands) > volumeSnapshotMaxLines {
		op.Commands = append([]string{}, op.Commands[len(op.Commands)-volumeSnapshotMaxLines:]...)
	}
	switch {
	case err == nil:
		op.Status, op.Error = VolumeSnapshotDone, ""
	case op.Action == config.ConstSnapshotActionSnapback && run.Changed:
		op.Status, op.Error = VolumeSnapshotHalfway, err.Error()
	default:
		op.Status, op.Error = VolumeSnapshotFailed, err.Error()
	}
}

// VolumeSnapshotOps are the last actions of the servers, copies.
func (cluster *Cluster) VolumeSnapshotOps() []VolumeSnapshotOp {
	cluster.vsnap.mu.Lock()
	defer cluster.vsnap.mu.Unlock()
	out := make([]VolumeSnapshotOp, 0, len(cluster.vsnap.ops))
	for _, op := range cluster.vsnap.ops {
		c := *op
		c.Commands = append([]string{}, op.Commands...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// VolumeSnapshotOp is the last action on server, nil for none.
func (cluster *Cluster) VolumeSnapshotOp(server *ServerMonitor) *VolumeSnapshotOp {
	for _, op := range cluster.VolumeSnapshotOps() {
		if op.Server == server.URL {
			return &op
		}
	}
	return nil
}

// VolumeSnapshotList is a server's snapshots, with what the GUI needs around them.
type VolumeSnapshotList struct {
	Enabled   bool                            `json:"enabled"`
	Plugin    string                          `json:"plugin"`
	Plugins   []config.SnapshotPluginManifest `json:"plugins"`
	Error     string                          `json:"error,omitempty"`
	Keep      int                             `json:"keep"`
	IsMaster  bool                            `json:"isMaster"`
	Target    string                          `json:"target"`
	Snapshots []VolumeSnapshot                `json:"snapshots"`
	Commands  []string                        `json:"commands"`
	LastOp    *VolumeSnapshotOp               `json:"lastOp"`
}

// volumeSnapshotListTimeout bounds a list asked from the API, which waits for it.
const volumeSnapshotListTimeout = 2 * time.Minute

// ListVolumeSnapshots lists the server's snapshots now. An error to run the plugin is in the
// result, not an error: the GUI shows it next to the settings.
func (cluster *Cluster) ListVolumeSnapshots(server *ServerMonitor) VolumeSnapshotList {
	out := VolumeSnapshotList{
		Enabled: cluster.Conf.BackupSnapshot, Plugin: cluster.Conf.SnapshotPluginName(), Plugins: config.SnapshotPlugins(),
		Keep: cluster.Conf.SnapshotKeep(), Snapshots: []VolumeSnapshot{}, Commands: []string{}, LastOp: cluster.VolumeSnapshotOp(server),
	}
	if m := cluster.GetMaster(); m != nil && m.URL == server.URL {
		out.IsMaster = true
	}
	vs, err := cluster.VolumeSnapshotter(server)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if c, ok := vs.(*contractSnapshotter); ok && c.timeout > volumeSnapshotListTimeout {
		vs = &contractSnapshotter{manifest: c.manifest, exec: c.exec, timeout: volumeSnapshotListTimeout}
	}
	run, err := vs.List(server)
	out.Target, out.Snapshots, out.Commands = run.Target, run.Snapshots, run.Commands
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

// volumeSnapshotName is the name of a snapshot replication-manager takes now.
func volumeSnapshotName(now time.Time) string {
	return config.ConstSnapshotPrefix + now.UTC().Format("20060102T150405Z")
}

// TakeVolumeSnapshot starts a snapshot of the server's volume, then prunes the ones it took
// beyond backup-snapshot-keep. It returns once the action is claimed; the run goes on in the
// background, its outcome in the server's last action and its states.
func (cluster *Cluster) TakeVolumeSnapshot(server *ServerMonitor) (string, error) {
	vs, err := cluster.VolumeSnapshotter(server)
	if err != nil {
		return "", err
	}
	name := volumeSnapshotName(time.Now())
	op, err := cluster.beginVolumeSnapshotOp(server, config.ConstSnapshotActionTake, name, vs.Manifest().Name)
	if err != nil {
		return "", err
	}
	cluster.goVolumeSnapshotOp(server, op, func() (VolumeSnapshotRun, error) {
		run, err := vs.Take(server, name)
		if err == nil {
			var pruned VolumeSnapshotRun
			pruned, err = cluster.pruneVolumeSnapshots(server, vs)
			run.Commands = append(run.Commands, pruned.Commands...)
		}
		return run, err
	})
	return name, nil
}

// volumeSnapshotsToPrune are the managed snapshots beyond the keep newest; never another one.
func volumeSnapshotsToPrune(snaps []VolumeSnapshot, keep int) []string {
	managed := make([]VolumeSnapshot, 0, len(snaps))
	for _, s := range snaps {
		if s.Managed {
			managed = append(managed, s)
		}
	}
	// newest first by name: repman-<UTC time> sorts as its time, and a creation time a plugin
	// could not give (zero) never makes the snapshot just taken the oldest one
	sort.SliceStable(managed, func(i, j int) bool { return managed[i].Name > managed[j].Name })
	var out []string
	for i := keep; i < len(managed); i++ {
		out = append(out, managed[i].Name)
	}
	return out
}

func (cluster *Cluster) pruneVolumeSnapshots(server *ServerMonitor, vs VolumeSnapshotter) (VolumeSnapshotRun, error) {
	list, err := vs.List(server)
	if err != nil {
		return list, fmt.Errorf("snapshot taken, its list for the retention failed: %w", err)
	}
	all := list
	for _, name := range volumeSnapshotsToPrune(list.Snapshots, cluster.Conf.SnapshotKeep()) {
		run, err := vs.Delete(server, name)
		all.Commands = append(all.Commands, run.Commands...)
		if err != nil {
			return all, fmt.Errorf("snapshot taken, the retention could not delete %s: %w", name, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot %s of %s pruned (backup-snapshot-keep %d)", name, server.URL, cluster.Conf.SnapshotKeep())
	}
	return all, nil
}

// DeleteVolumeSnapshot deletes a snapshot replication-manager took; another one is refused
// (F1: replication-manager removes only what it created).
func (cluster *Cluster) DeleteVolumeSnapshot(server *ServerMonitor, name string) error {
	if err := config.ValidSnapshotName(name); err != nil {
		return err
	}
	if !strings.HasPrefix(name, config.ConstSnapshotPrefix) {
		return fmt.Errorf("snapshot %s was not taken by replication-manager (%s*): it is never deleted by it", name, config.ConstSnapshotPrefix)
	}
	vs, err := cluster.VolumeSnapshotter(server)
	if err != nil {
		return err
	}
	op, err := cluster.beginVolumeSnapshotOp(server, config.ConstSnapshotActionDelete, name, vs.Manifest().Name)
	if err != nil {
		return err
	}
	cluster.goVolumeSnapshotOp(server, op, func() (VolumeSnapshotRun, error) {
		return vs.Delete(server, name)
	})
	return nil
}

// snapbackRefusal is why a snapback of server can not start, "" when it can.
func (cluster *Cluster) snapbackRefusal(server *ServerMonitor) string {
	switch {
	case cluster.IsInFailover():
		return "a failover or switchover is running"
	case server.IsMaster():
		return "the server is the master: switch over first, a snapback of the master loses the writes of the whole cluster"
	}
	if m := cluster.GetMaster(); m != nil && m.URL == server.URL {
		return "the server is the master: switch over first, a snapback of the master loses the writes of the whole cluster"
	}
	return ""
}

// SnapbackVolume puts the server's volume back to the snapshot: an operator's action only.
// Refused on the master and during a failover. The server goes into maintenance, is stopped
// through its orchestrator, rolled back, started, and stays in maintenance (WARN0331). When the
// rollback fails before it changed the volume, the server is started again as it was; after
// it changed it, the server is left stopped and in maintenance (ERR00123).
func (cluster *Cluster) SnapbackVolume(server *ServerMonitor, name string) error {
	if err := config.ValidSnapshotName(name); err != nil {
		return err
	}
	if why := cluster.snapbackRefusal(server); why != "" {
		return errors.New(why)
	}
	vs, err := cluster.VolumeSnapshotter(server)
	if err != nil {
		return err
	}
	// the server is claimed before its checks: two snapbacks never pass them together
	op, err := cluster.beginVolumeSnapshotOp(server, config.ConstSnapshotActionSnapback, name, vs.Manifest().Name)
	if err != nil {
		return err
	}
	if err := cluster.snapbackPreflight(server, vs, name); err != nil {
		cluster.releaseVolumeSnapshotOp(op)
		return err
	}
	cluster.goVolumeSnapshotOp(server, op, func() (VolumeSnapshotRun, error) {
		return cluster.runVolumeSnapback(server, vs, name)
	})
	return nil
}

// snapbackPreflight: the snapshot exists (a list bounded as the API's, the request waits for
// it), and the refusals still hold once the server is claimed.
func (cluster *Cluster) snapbackPreflight(server *ServerMonitor, vs VolumeSnapshotter, name string) error {
	if c, ok := vs.(*contractSnapshotter); ok && c.timeout > volumeSnapshotListTimeout {
		vs = &contractSnapshotter{manifest: c.manifest, exec: c.exec, timeout: volumeSnapshotListTimeout}
	}
	list, err := vs.List(server)
	if err != nil {
		return fmt.Errorf("can not list the snapshots: %w", err)
	}
	found := false
	for _, s := range list.Snapshots {
		found = found || s.Name == name
	}
	if !found {
		return fmt.Errorf("snapshot %s not found on %s", name, list.Target)
	}
	if why := cluster.snapbackRefusal(server); why != "" {
		return errors.New(why)
	}
	return nil
}

// volumeSnapshotDownTimeout bounds the waits of a snapback for the server to stop or start.
const volumeSnapshotDownTimeout = 5 * time.Minute

func (cluster *Cluster) runVolumeSnapback(server *ServerMonitor, vs VolumeSnapshotter, name string) (run VolumeSnapshotRun, err error) {
	setMaintenance := !server.IsMaintenance
	defer func() {
		// nothing changed and the server answers: out of the maintenance this run set. Safe even
		// when the stop failed and the port never closed: the volume was not touched, the
		// server is the one it was before the snapback
		if err != nil && !run.Changed && setMaintenance && waitServerPort(server, true, time.Second) == nil {
			server.DelMaintenance()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to %s did not change the volume: server out of maintenance again", server.URL, name)
		}
	}()
	if setMaintenance {
		server.SetMaintenance()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to %s: server set in maintenance", server.URL, name)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to %s: stopping the database", server.URL, name)
	if err := cluster.StopDatabaseService(server); err != nil {
		return VolumeSnapshotRun{}, cluster.snapbackUndoStop(server, fmt.Errorf("stop failed: %w", err))
	}
	// the port closed: the SQL SHUTDOWN or the orchestrator stop is done (a server reachable on
	// a socket only has no port to watch: it is not monitored by replication-manager either)
	if err := waitServerPort(server, false, volumeSnapshotDownTimeout); err != nil {
		return VolumeSnapshotRun{}, cluster.snapbackUndoStop(server, fmt.Errorf("the database did not stop: %w", err))
	}
	// checked again once stopped: a switchover or a failover may have started meanwhile, or made
	// this server the master (in maintenance it is no candidate, the check is the guard)
	if why := cluster.snapbackRefusal(server); why != "" {
		return VolumeSnapshotRun{}, cluster.snapbackUndoStop(server, fmt.Errorf("refused once the server was stopped: %s", why))
	}
	run, err = vs.Snapback(server, name)
	if err != nil && run.Changed {
		// half-way: never start a server on a volume in an unknown state
		return run, err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to %s: starting the database", server.URL, name)
	if serr := cluster.StartDatabaseService(server); serr != nil {
		if err != nil {
			return run, fmt.Errorf("%v; the start after it failed too: %v", err, serr)
		}
		return run, fmt.Errorf("volume rolled back, the start failed: %w", serr)
	}
	// started: answered on SQL through the monitor's own path (Ping, Refresh), not a port
	if werr := server.WaitDatabaseStart(); werr != nil && err == nil {
		return run, fmt.Errorf("volume rolled back, the database did not start: %w", werr)
	}
	return run, err
}

// volumeSnapshotUndoWait is how long a snapback that changed nothing waits for the server to
// answer again before it starts it.
const volumeSnapshotUndoWait = 30 * time.Second

// snapbackUndoStop: the snapback ends before any change to the volume, after its stop (failed or
// done). The server is started again when it does not answer; the error says where it is left.
func (cluster *Cluster) snapbackUndoStop(server *ServerMonitor, cause error) error {
	if waitServerPort(server, true, volumeSnapshotUndoWait) == nil {
		return fmt.Errorf("%w; nothing was changed, the server answers", cause)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s ended before any change: starting the database again", server.URL)
	if err := cluster.StartDatabaseService(server); err == nil && server.WaitDatabaseStart() == nil {
		return fmt.Errorf("%w; nothing was changed, the server was started again", cause)
	}
	return fmt.Errorf("%w; nothing was changed on the volume, but the server is stopped and in maintenance: start it", cause)
}

// waitServerPort waits until the server's port answers (up) or refuses (down). TCP only, no
// cancellation, bounded by its timeout: it watches the stop of a server replication-manager
// monitors on that port, the start is confirmed on SQL (WaitDatabaseStart).
func waitServerPort(server *ServerMonitor, up bool, timeout time.Duration) error {
	addr := net.JoinHostPort(misc.Unbracket(server.Host), server.Port)
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if conn != nil {
			conn.Close()
		}
		if (err == nil) == up {
			return nil
		}
		if time.Now().After(deadline) {
			if up {
				return fmt.Errorf("%s does not answer after %s", addr, timeout)
			}
			return fmt.Errorf("%s still answers after %s", addr, timeout)
		}
		time.Sleep(time.Second)
	}
}

func (cluster *Cluster) logVolumeSnapshotEnd(server *ServerMonitor, op *VolumeSnapshotOp, err error) {
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Volume snapshot %s %s of %s failed: %s", op.Action, op.Snapshot, server.URL, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot %s %s of %s done", op.Action, op.Snapshot, server.URL)
}

// checkVolumeSnapshotStates, every tick, from memory (F7): CINF0024 while an action runs,
// WARN0330 its failure until the next action of the server ends well, ERR00123 a snapback
// stopped half-way, WARN0331 a server a snapback left in maintenance until it leaves it,
// WARN0332 snapshots switched on with a plugin that can not run.
func (cluster *Cluster) checkVolumeSnapshotStates() {
	if cluster.Conf.BackupSnapshot {
		if _, err := cluster.volumeSnapshotRefusal(); err != nil {
			cluster.SetState("WARN0332", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0332"], err), ErrFrom: "BACKUP"})
		}
	}
	for _, op := range cluster.VolumeSnapshotOps() {
		server := cluster.GetServerFromURL(op.Server)
		if server == nil {
			continue
		}
		switch op.Status {
		case VolumeSnapshotRunning:
			cluster.SetState("CINF0024", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0024"], op.Action, op.Server, op.Plugin, op.Start.Format(time.RFC3339)), ErrFrom: "BACKUP", ServerUrl: op.Server})
		case VolumeSnapshotFailed:
			cluster.SetState("WARN0330", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0330"], op.Action, op.Server, op.Plugin, op.Error), ErrFrom: "BACKUP", ServerUrl: op.Server})
		case VolumeSnapshotHalfway:
			cluster.SetState("ERR00123", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00123"], op.Server, op.Snapshot, op.Error), ErrFrom: "BACKUP", ServerUrl: op.Server})
		case VolumeSnapshotDone:
			if op.SnapbackDone {
				if server.IsMaintenance {
					cluster.SetState("WARN0331", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0331"], op.Server, op.Snapshot, op.End.Format(time.RFC3339)), ErrFrom: "BACKUP", ServerUrl: op.Server})
				} else {
					// the operator took it out of maintenance: the snapback is over
					cluster.vsnap.mu.Lock()
					if cur := cluster.vsnap.ops[op.Server]; cur != nil && cur.Start.Equal(op.Start) {
						cur.SnapbackDone = false
					}
					cluster.vsnap.mu.Unlock()
				}
			}
		}
	}
}
