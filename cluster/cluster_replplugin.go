// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// Replication plugins (#2015): a long-running process per enabled TOML of the cluster's
// replication-plugins directory, supervised here on the active replication-manager only (a
// standby running it too would apply every change twice). Its binary is replplugin-<plugin>,
// signed like the log plugins; its passwords go in its environment, never in its TOML. Each
// status line it prints is kept and raised every tick: CINF0017 while it runs, WARN0242 while
// it is stopped, with the reason and the next restart. A crash, a hang or a fatal error stays
// in its process: it is restarted with a backoff.

const (
	replPluginDirName   = "replication-plugins"
	replPluginBackoffMn = 5 * time.Second
	replPluginBackoffMx = 5 * time.Minute
	replPluginStopGrace = 30 * time.Second
	replPluginStderrMax = 4096
)

type replPluginRunner struct {
	path    string // its TOML
	modTime time.Time
	cfg     wire.Config
	cancel  context.CancelFunc
	done    chan struct{}

	status  atomic.Pointer[wire.Status]
	stopped atomic.Pointer[replPluginStop] // nil while the process runs
}

type replPluginStop struct {
	Reason  string
	Restart time.Time
}

// replPluginDir is <WorkingDir>/<cluster>/replication-plugins.
func (cluster *Cluster) replPluginDir() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, replPluginDirName)
}

// readReplPluginConfigs reads the cluster's plugin TOMLs, by path; a file that does not parse
// is returned with its error, so it is said, never started.
func (cluster *Cluster) readReplPluginConfigs() (map[string]wire.Config, map[string]time.Time, map[string]error) {
	cfgs, mods, errs := map[string]wire.Config{}, map[string]time.Time{}, map[string]error{}
	entries, err := os.ReadDir(cluster.replPluginDir())
	if err != nil {
		return cfgs, mods, errs
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		path := filepath.Join(cluster.replPluginDir(), e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		var c wire.Config
		if _, err := toml.DecodeFile(path, &c); err != nil {
			errs[path] = err
			continue
		}
		cfgs[path], mods[path] = c, info.ModTime()
	}
	return cfgs, mods, errs
}

// CheckReplicationPlugins reconciles the running plugins with the TOMLs, every tick: a
// runner per enabled TOML on the active replication-manager, restarted when its TOML
// changes; none on a standby or on a stopping cluster. Then the states.
func (cluster *Cluster) CheckReplicationPlugins() {
	cfgs, mods, errs := cluster.readReplPluginConfigs()
	active := cluster.IsActive() && !cluster.exit.Load()
	cluster.replPluginsMu.Lock()
	if cluster.replPlugins == nil {
		cluster.replPlugins = map[string]*replPluginRunner{}
	}
	for path, r := range cluster.replPlugins {
		c, ok := cfgs[path]
		if !active || !ok || !c.Enabled || !mods[path].Equal(r.modTime) {
			cluster.stopReplPluginLocked(path, r)
		}
	}
	if active {
		for path, c := range cfgs {
			if _, running := cluster.replPlugins[path]; running || !c.Enabled {
				continue
			}
			cluster.startReplPluginLocked(path, c, mods[path])
		}
	}
	runners := make([]*replPluginRunner, 0, len(cluster.replPlugins))
	for _, r := range cluster.replPlugins {
		runners = append(runners, r)
	}
	cluster.replPluginsMu.Unlock()
	cluster.checkReplPluginStates(runners, errs)
}

func (cluster *Cluster) startReplPluginLocked(path string, c wire.Config, mod time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &replPluginRunner{path: path, modTime: mod, cfg: c, cancel: cancel, done: make(chan struct{})}
	cluster.replPlugins[path] = r
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s: starting (%s)", c.Plugin, cluster.Name, path)
	go cluster.superviseReplPlugin(ctx, r)
}

func (cluster *Cluster) stopReplPluginLocked(path string, r *replPluginRunner) {
	r.cancel()
	delete(cluster.replPlugins, path)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s: stopping (%s)", r.cfg.Plugin, cluster.Name, path)
}

// StopReplicationPlugins stops every plugin of the cluster and waits for them (a SIGTERM,
// then the grace period): at the cluster's stop.
func (cluster *Cluster) StopReplicationPlugins() {
	cluster.replPluginsMu.Lock()
	var waits []chan struct{}
	for path, r := range cluster.replPlugins {
		waits = append(waits, r.done)
		cluster.stopReplPluginLocked(path, r)
	}
	cluster.replPluginsMu.Unlock()
	for _, w := range waits {
		<-w
	}
}

// superviseReplPlugin runs a plugin until its context ends, restarting it with a backoff
// (back to the minimum after a run of 10 minutes).
func (cluster *Cluster) superviseReplPlugin(ctx context.Context, r *replPluginRunner) {
	defer close(r.done)
	defer func() {
		if p := recover(); p != nil {
			r.stopped.Store(&replPluginStop{Reason: fmt.Sprintf("panic in its supervisor: %v", p)})
		}
	}()
	backoff := replPluginBackoffMn
	for ctx.Err() == nil {
		started := time.Now()
		err := cluster.execReplPlugin(ctx, r)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > 10*time.Minute {
			backoff = replPluginBackoffMn
		}
		reason := "exited"
		if err != nil {
			reason = err.Error()
		}
		r.stopped.Store(&replPluginStop{Reason: reason, Restart: time.Now().Add(backoff)})
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Replication plugin %s of %s stopped: %s; restart in %s", r.cfg.Plugin, cluster.Name, reason, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > replPluginBackoffMx {
			backoff = replPluginBackoffMx
		}
	}
}

// replPluginBinary finds replplugin-<plugin>: the delivered plugins (<WorkingDir>/plugins),
// else the ones built next to this binary (a source build: build/binaries/.. -> build/plugins).
func (cluster *Cluster) replPluginBinary(plugin string) (string, error) {
	name := wire.BinaryPrefix + plugin
	if strings.ContainsAny(plugin, `/\`) || plugin == "" {
		return "", fmt.Errorf("invalid plugin name %q", plugin)
	}
	dirs := []string{filepath.Join(cluster.Conf.WorkingDir, logplugin.PluginDirName)}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "..", "plugins"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("binary %s not found in %s", name, strings.Join(dirs, ", "))
}

// execReplPlugin runs the plugin once, until it exits or the context ends.
func (cluster *Cluster) execReplPlugin(ctx context.Context, r *replPluginRunner) error {
	bin, err := cluster.replPluginBinary(r.cfg.Plugin)
	if err != nil {
		return err
	}
	// the same check as the log plugins: when the key is there, a valid signature
	if key := cluster.Conf.PluginSigningPublicKey; key != "" {
		if _, serr := os.Stat(key); serr == nil {
			if err := logplugin.VerifyPluginSignature(bin, filepath.Join(cluster.Conf.ShareDir, "plugins"), key); err != nil {
				return fmt.Errorf("signature: %w", err)
			}
		}
	}
	cmd := exec.CommandContext(ctx, bin, "-config", r.path)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = replPluginStopGrace
	// a minimal environment: the passwords and the wire version, nothing of this process's
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		wire.EnvWireVersion + "=" + strconv.Itoa(wire.WireVersion),
		wire.EnvSourcePassword + "=" + cluster.GetDbPass(),
		wire.EnvTargetPassword + "=" + cluster.GetDbPass(),
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr capBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	r.stopped.Store(nil)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var st wire.Status
		if json.Unmarshal(sc.Bytes(), &st) == nil {
			r.status.Store(&st)
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if st := r.status.Load(); st != nil && st.State == wire.StateError && st.Error != "" {
		return fmt.Errorf("%s", st.Error)
	}
	if werr != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return fmt.Errorf("%v: %s", werr, lastLine(s))
		}
		return werr
	}
	return nil
}

// checkReplPluginStates raises the states of the plugins.
func (cluster *Cluster) checkReplPluginStates(runners []*replPluginRunner, errs map[string]error) {
	sort.Slice(runners, func(i, j int) bool { return runners[i].path < runners[j].path })
	for _, r := range runners {
		name := r.cfg.Plugin
		if stop := r.stopped.Load(); stop != nil {
			in := time.Until(stop.Restart).Round(time.Second)
			if in < 0 {
				in = 0
			}
			cluster.SetState("WARN0242", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0242"], name, cluster.Name, stop.Reason, in), ErrFrom: "REPL"})
			continue
		}
		st := r.status.Load()
		if st == nil {
			continue
		}
		detail := ""
		if st.Description != "" {
			detail = "; " + st.Description
		}
		cluster.SetState("CINF0017", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0017"], name, cluster.Name, st.State, st.GTID, st.LagSeconds, st.Inserts, st.Updates, st.Deletes, st.Skipped, detail), ErrFrom: "REPL"})
	}
	for path, err := range errs {
		cluster.SetState("WARN0242", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0242"], filepath.Base(path), cluster.Name, "its TOML does not parse: "+err.Error(), time.Duration(0)), ErrFrom: "REPL"})
	}
}

// capBuffer keeps the last replPluginStderrMax bytes written to it.
type capBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *capBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - replPluginStderrMax; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *capBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func lastLine(s string) string {
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
