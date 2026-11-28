// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// What only the database host knows, for an external snapshot plugin (#2456,
// VOLUME_SNAPSHOTS.md "External plugins: settings, devices, unmount"). The plugin runs on the
// replication-manager host and drives a storage system; the embedded helper's host actions run
// on the database host, through the executor the zfs and lvm plugins use there:
//   - devices: what holds each server path (a disk's WWN, an NFS export), passed to the plugin
//     as REPMAN_SNAPSHOT_DEVICES so it checks that what it restores holds only the server's files;
//   - unmount, mount: around the plugin's snapback (the server already stopped), so the storage
//     is never restored under a mounted file system (REPMAN_SNAPSHOT_UNMOUNTED=1 tells it).

const (
	envSnapshotDevices   = "REPMAN_SNAPSHOT_DEVICES"
	envSnapshotUnmounted = "REPMAN_SNAPSHOT_UNMOUNTED"
	envSnapshotMounts    = "REPMAN_SNAPSHOT_MOUNTS"
)

// The host actions of the helper.
const (
	snapshotHostDevices = "devices"
	snapshotHostUnmount = "unmount"
	snapshotHostMount   = "mount"
)

// volumeSnapshotHostExecutor is the executor of the helper on the server's database host, nil
// when the orchestrator gives no access to it (the rules of volumeSnapshotRefusal).
func (cluster *Cluster) volumeSnapshotHostExecutor(server *ServerMonitor) snapshotExecutor {
	switch traits := cluster.Orchestrator().Traits(); {
	case traits.HostIsLocal:
		return &localSnapshotExecutor{}
	case traits.HostBySSH && cluster.Conf.OnPremiseSSH:
		return &sshSnapshotExecutor{server: server}
	}
	return nil
}

// unmountsHost: a snapback of this plugin unmounts the server's file systems first.
func (c *contractSnapshotter) unmountsHost(server *ServerMonitor) bool {
	return c.manifest.External && c.host != nil && server.ClusterGroup.Conf.BackupSnapshotUnmount
}

// hostRecord is one record of a host action: its kind and fields.
type hostRecord struct {
	kind   string
	fields []string
}

// hostRun runs a host action of the helper on the database host: its commands and CHANGED as
// for any run, and its DEVICE / UNRESOLVED / MOUNT records.
func (c *contractSnapshotter) hostRun(ctx context.Context, server *ServerMonitor, action string, extra []string) (VolumeSnapshotRun, []hostRecord, error) {
	cluster := server.ClusterGroup
	env := append(server.volumeSnapshotEnv(c.manifest, action, ""), "REPMAN_SNAPSHOT_BUSY_WAIT="+fmt.Sprint(volumeSnapshotBusyWait(c.timeout)))
	env = append(env, extra...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot host step %s of %s (plugin %s) %s", action, server.URL, c.manifest.Name, c.host.Where())
	out, err := c.host.Exec(ctx, action, env)
	run := parseVolumeSnapshotOutput(out.stdout)
	for _, cmd := range run.Commands {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot host step %s of %s ran: %s", action, server.URL, cmd)
	}
	var recs []hostRecord
	sc := bufio.NewScanner(strings.NewReader(out.stdout))
	for sc.Scan() && len(recs) < volumeSnapshotMaxLines {
		f := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
		switch f[0] {
		case "DEVICE", "UNRESOLVED", "MOUNT":
			recs = append(recs, hostRecord{kind: f[0], fields: f[1:]})
		}
	}
	if err != nil && (ctx.Err() != nil || out.truncated || run.incomplete) {
		// what the host step did is not known (a deadline: it may still run; an output cut
		// short: a CHANGED past the cut): it may have unmounted some file systems, so the
		// snapback is half-way and the server is never started on them
		run.Changed = true
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			limit := fmt.Sprintf("%s (backup-snapshot-timeout)", c.timeout)
			if action == snapshotHostMount {
				limit = fmt.Sprintf("%s (the mount step's own limit)", volumeSnapshotDownTimeout)
			}
			return run, recs, fmt.Errorf("stopped after %s", limit)
		}
		if msg := strings.TrimSpace(lastLine(strings.TrimSpace(out.stderr))); msg != "" {
			return run, recs, errors.New(msg)
		}
		return run, recs, err
	}
	if out.truncated || run.incomplete {
		run.Changed = true
		return run, recs, errors.New("the output of the host step was cut")
	}
	return run, recs, nil
}

// hostDevices is the REPMAN_SNAPSHOT_DEVICES value: one "path \t fstype \t source \t id" line
// per device; empty when a path can not be told (the plugin then can not check its target),
// or the host step fails.
func (c *contractSnapshotter) hostDevices(ctx context.Context, server *ServerMonitor) string {
	cluster := server.ClusterGroup
	_, recs, err := c.hostRun(ctx, server, snapshotHostDevices, nil)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Volume snapshot of %s: the devices of the server's files are not known: %s", server.URL, err)
		return ""
	}
	var lines []string
	for _, r := range recs {
		switch r.kind {
		case "UNRESOLVED":
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Volume snapshot of %s: the device holding %s is not known: the plugin gets no device", server.URL, strings.Join(r.fields, " "))
			return ""
		case "DEVICE":
			if len(r.fields) == 4 && r.fields[0] != "" && r.fields[3] != "" {
				lines = append(lines, strings.Join(r.fields, "\t"))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// snapbackUnmounted is the snapback of an external plugin with the server's file systems
// unmounted on the database host around it. Before the plugin's restore changes anything, a
// failure puts the file systems back and the snapback ends unchanged; after it, the file
// systems stay as they are and the snapback is half-way: the server is never started on them.
func (c *contractSnapshotter) snapbackUnmounted(ctx context.Context, server *ServerMonitor, name string) (VolumeSnapshotRun, error) {
	um, recs, err := c.hostRun(ctx, server, snapshotHostUnmount, nil)
	run := um
	if err != nil {
		// the helper put back what it unmounted, or said CHANGED when it could not
		return run, fmt.Errorf("unmount of the server's file systems: %w", err)
	}
	var mounts []string
	for _, r := range recs {
		if r.kind == "MOUNT" && len(r.fields) == 2 {
			mounts = append(mounts, r.fields[0]+"|"+r.fields[1])
		}
	}
	plug, perr := c.runCtx(ctx, server, config.ConstSnapshotActionSnapback, name, []string{envSnapshotUnmounted + "=1"})
	run.Target, run.Snapshots, run.Changed, run.incomplete = plug.Target, plug.Snapshots, plug.Changed, plug.incomplete
	run.Commands = append(run.Commands, plug.Commands...)
	run.Log = append(run.Log, plug.Log...)
	if perr != nil && run.Changed {
		return run, perr
	}
	// a context of its own for the mount: the plugin may have used the whole deadline, and an
	// unchanged volume left unmounted would leave the server stopped for nothing
	mctx, cancel := context.WithTimeout(context.Background(), volumeSnapshotDownTimeout)
	defer cancel()
	mnt, _, merr := c.hostRun(mctx, server, snapshotHostMount, []string{envSnapshotMounts + "=" + strings.Join(mounts, "\n")})
	run.Commands = append(run.Commands, mnt.Commands...)
	if merr != nil {
		// never start a server whose files are not mounted
		run.Changed = true
		if perr != nil {
			return run, fmt.Errorf("%v; the server's file systems could not be mounted again: %v", perr, merr)
		}
		return run, fmt.Errorf("restored, but the server's file systems could not be mounted again: %w", merr)
	}
	return run, perr
}
