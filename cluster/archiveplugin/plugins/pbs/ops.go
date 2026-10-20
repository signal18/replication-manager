// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// A backup is one pxar archive of the pushed folder, in a host-type snapshot of the group
// host/<backup-id>; the backup id is the request's host. PBS has no tags: replication-
// manager's tags, host and path ride on the first line of the snapshot's notes, the one
// "snapshot list" returns as its comment.
const (
	archiveName   = "backup.pxar"
	backupType    = "host"
	notesPrefix   = "repman"
	defaultBackup = "repman"
)

// handle runs one operation.
func handle(req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if !isPBS(req.Store.Plugin) {
		return resp, fmt.Errorf("unknown archive plugin %q (this binary serves %s)", req.Store.Plugin, pluginPBS)
	}
	c, err := newClient(req.Store)
	if err != nil {
		return resp, err
	}
	switch req.Op {
	case wire.OpProbe:
		return opProbe(c, req)
	case wire.OpPush:
		return opPush(c, req)
	case wire.OpList:
		snaps, err := c.snapshots()
		resp.Snapshots = snaps
		return resp, err
	case wire.OpRestore:
		return opRestore(c, req)
	case wire.OpForget:
		return opForget(c, req)
	case wire.OpPurge:
		return opPurge(c, req)
	case wire.OpSize:
		snaps, err := c.snapshots()
		for _, s := range snaps {
			resp.Size += s.Size
		}
		return resp, err
	case wire.OpRemove:
		return opRemove(c)
	case wire.OpSync:
		return resp, errors.New("sync is not supported by archive-pbs: copy between datastores with a sync job of the PBS")
	}
	return resp, fmt.Errorf("unknown op %q", req.Op)
}

// location is where the archive is, without secret.
func (c *client) location() string {
	if c.namespace == "" {
		return "pbs://" + c.repo
	}
	return "pbs://" + c.repo + "/" + c.namespace
}

// opProbe opens the archive: the snapshot list of its namespace answers when the server,
// the credentials and the namespace are right. With create, a missing namespace is created
// level by level (the datastore itself is the PBS admin's).
func opProbe(c *client, req wire.Request) (wire.Response, error) {
	resp := wire.Response{Location: c.location()}
	if _, err := c.snapshots(); err == nil {
		return resp, nil
	} else if !req.Create || c.namespace == "" {
		return resp, err
	}
	existing, err := c.namespaces()
	if err != nil {
		return resp, err
	}
	parts := strings.Split(c.namespace, "/")
	for i := range parts {
		ns := strings.Join(parts[:i+1], "/")
		if existing[ns] {
			continue
		}
		if _, err := c.run(false, "namespace", "create", ns); err != nil {
			return resp, fmt.Errorf("create namespace %s: %w", ns, err)
		}
		resp.Created = true
	}
	if _, err := c.snapshots(); err != nil {
		return resp, err
	}
	return resp, nil
}

// namespaces lists the namespaces of the datastore.
func (c *client) namespaces() (map[string]bool, error) {
	out, err := c.run(false, "namespace", "list", "--max-depth", "7", "--output-format", "json")
	if err != nil {
		return nil, err
	}
	var items []struct {
		NS string `json:"ns"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("namespace list: %w", err)
	}
	ns := map[string]bool{}
	for _, it := range items {
		ns[strings.Trim(it.NS, "/")] = true
	}
	return ns, nil
}

// backupIDRe is what PBS accepts as a backup id.
var backupIDRe = regexp.MustCompile(`[^A-Za-z0-9_.\-]`)

// backupID is the backup id of a host: its characters PBS refuses as "_", "repman" when none.
func backupID(host string) string {
	id := backupIDRe.ReplaceAllString(strings.TrimSpace(host), "_")
	id = strings.TrimLeft(id, ".-")
	if id == "" {
		return defaultBackup
	}
	return id
}

// snapshotID is the PBS name of a snapshot: type/id/time (UTC, seconds).
func snapshotID(typ, id string, t time.Time) string {
	return typ + "/" + id + "/" + t.UTC().Format("2006-01-02T15:04:05Z")
}

// encodeNotes is the notes line carrying the snapshot's host, path and tags.
func encodeNotes(host, dir string, tags []string) string {
	q := url.QueryEscape
	enc := make([]string, 0, len(tags))
	for _, t := range tags {
		enc = append(enc, q(t))
	}
	return fmt.Sprintf("%s host=%s path=%s tags=%s", notesPrefix, q(host), q(dir), strings.Join(enc, ","))
}

// decodeNotes reads a notes line encodeNotes wrote; another comment gives nothing.
func decodeNotes(line string) (host, dir string, tags []string) {
	fields := strings.Fields(strings.SplitN(line, "\n", 2)[0])
	if len(fields) == 0 || fields[0] != notesPrefix {
		return "", "", nil
	}
	for _, f := range fields[1:] {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "host":
			host, _ = url.QueryUnescape(v)
		case "path":
			dir, _ = url.QueryUnescape(v)
		case "tags":
			for _, t := range strings.Split(v, ",") {
				if t == "" {
					continue
				}
				if u, err := url.QueryUnescape(t); err == nil {
					tags = append(tags, u)
				}
			}
		}
	}
	return host, dir, tags
}

// pbsSnapshot is one item of "snapshot list --output-format json" (the datastore API's
// snapshot list item). The client's documentation does not list these fields: verified on
// the lab PBS, unknown ones are ignored.
type pbsSnapshot struct {
	Type    string `json:"backup-type"`
	ID      string `json:"backup-id"`
	Time    int64  `json:"backup-time"`
	Comment string `json:"comment"`
	Size    int64  `json:"size"`
}

// parseSnapshots turns the client's snapshot list into wire snapshots, newest first.
func parseSnapshots(out []byte) ([]wire.Snapshot, error) {
	var items []pbsSnapshot
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("snapshot list: %w", err)
	}
	snaps := make([]wire.Snapshot, 0, len(items))
	for _, it := range items {
		t := time.Unix(it.Time, 0).UTC()
		s := wire.Snapshot{ID: snapshotID(it.Type, it.ID, t), Time: t, Size: it.Size}
		host, dir, tags := decodeNotes(it.Comment)
		s.Host, s.Tags = host, tags
		if dir != "" {
			s.Paths = []string{dir}
		}
		snaps = append(snaps, s)
	}
	sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
	return snaps, nil
}

// snapshots lists the archive's snapshots, newest first.
func (c *client) snapshots() ([]wire.Snapshot, error) {
	out, err := c.run(false, append([]string{"snapshot", "list", "--output-format", "json"}, c.nsArgs()...)...)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(out)
}

// findSnapshot resolves a snapshot by its full name or a unique prefix of it.
func findSnapshot(snaps []wire.Snapshot, ref string) (wire.Snapshot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return wire.Snapshot{}, errors.New("no snapshot given")
	}
	var found []wire.Snapshot
	for _, s := range snaps {
		if s.ID == ref {
			return s, nil
		}
		if strings.HasPrefix(s.ID, ref) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return wire.Snapshot{}, fmt.Errorf("snapshot %s not in the archive", ref)
	case 1:
		return found[0], nil
	}
	return wire.Snapshot{}, fmt.Errorf("snapshot %s is ambiguous: %d snapshots start with it", ref, len(found))
}

// opPush archives the folder Path as a new snapshot of the host's group, then writes its
// notes; a notes failure is reported with the snapshot, which exists without its tags.
func opPush(c *client, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		return resp, fmt.Errorf("push: path %q is not an absolute folder", req.Path)
	}
	dir := filepath.Clean(req.Path)
	if fi, err := os.Stat(dir); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	} else if !fi.IsDir() {
		return resp, fmt.Errorf("push: %s is not a folder", dir)
	}
	id := backupID(req.Host)
	when := time.Now().UTC().Truncate(time.Second)
	args := []string{"backup", archiveName + ":" + dir, "--backup-type", backupType, "--backup-id", id,
		"--backup-time", strconv.FormatInt(when.Unix(), 10)}
	if c.change != "" {
		args = append(args, "--change-detection-mode", c.change)
	}
	if _, err := c.run(true, append(args, c.nsArgs()...)...); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	snap := &wire.Snapshot{ID: snapshotID(backupType, id, when), Time: when, Host: req.Host,
		Tags: append([]string(nil), req.Tags...), Paths: []string{dir}}
	resp.Snapshot = snap
	notes := encodeNotes(req.Host, dir, req.Tags)
	if _, err := c.run(false, append([]string{"snapshot", "notes", "update", snap.ID, notes}, c.nsArgs()...)...); err != nil {
		return resp, fmt.Errorf("push: snapshot %s written, its tags not: %w", snap.ID, err)
	}
	return resp, nil
}

// restorePatterns turns Include into the client's --pattern: a path relative to the pushed
// folder (absolute paths under it are made relative), and its content for a folder.
func restorePatterns(include, pushed string) []string {
	inc := strings.TrimSpace(include)
	if inc == "" || inc == "." || inc == "/" {
		return nil
	}
	if strings.HasPrefix(inc, "/") {
		root := strings.TrimSuffix(path.Clean(pushed), "/")
		if pushed != "" && (inc == root || strings.HasPrefix(inc, root+"/")) {
			inc = strings.TrimPrefix(inc, root)
		}
		inc = strings.TrimPrefix(inc, "/")
	}
	inc = path.Clean(inc)
	if inc == "." {
		return nil
	}
	return []string{inc, inc + "/**"}
}

// opRestore restores Snapshot into Target: the pushed folder's content under Target, or
// only its path Include, kept at its place under Target.
func opRestore(c *client, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if req.Target == "" || !filepath.IsAbs(req.Target) {
		return resp, fmt.Errorf("restore: target %q is not an absolute folder", req.Target)
	}
	snaps, err := c.snapshots()
	if err != nil {
		return resp, err
	}
	snap, err := findSnapshot(snaps, req.Snapshot)
	if err != nil {
		return resp, err
	}
	pushed := ""
	if len(snap.Paths) > 0 {
		pushed = snap.Paths[0]
	}
	if err := os.MkdirAll(req.Target, 0o750); err != nil {
		return resp, fmt.Errorf("restore: %w", err)
	}
	args := []string{"restore", snap.ID, archiveName, req.Target, "--allow-existing-dirs"}
	for _, p := range restorePatterns(req.Include, pushed) {
		args = append(args, "--pattern", p)
	}
	if _, err := c.run(true, append(args, c.nsArgs()...)...); err != nil {
		return resp, fmt.Errorf("restore %s: %w", snap.ID, err)
	}
	resp.Snapshot = &snap
	return resp, nil
}

// opForget deletes one snapshot.
func opForget(c *client, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	snaps, err := c.snapshots()
	if err != nil {
		return resp, err
	}
	snap, err := findSnapshot(snaps, req.Snapshot)
	if err != nil {
		return resp, err
	}
	if _, err := c.run(false, append([]string{"snapshot", "forget", snap.ID}, c.nsArgs()...)...); err != nil {
		return resp, fmt.Errorf("forget %s: %w", snap.ID, err)
	}
	resp.Removed = []string{snap.ID}
	return resp, nil
}

// pruneArgs is the cluster's policy as PBS prune options. PBS prunes by counts only: a
// keep-within window has no PBS equivalent and is refused, never ignored (keeping less
// than the policy says would delete backups the cluster wants kept).
func pruneArgs(p *wire.Policy) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	for name, w := range map[string]string{
		"keep-within": p.KeepWithin, "keep-within-hourly": p.KeepWithinHourly, "keep-within-daily": p.KeepWithinDaily,
		"keep-within-weekly": p.KeepWithinWeekly, "keep-within-monthly": p.KeepWithinMonthly, "keep-within-yearly": p.KeepWithinYearly,
	} {
		if strings.TrimSpace(w) != "" {
			return nil, fmt.Errorf("purge: %s has no Proxmox Backup Server equivalent (PBS prunes by counts: last, hourly, daily, weekly, monthly, yearly)", name)
		}
	}
	var args []string
	for _, kv := range []struct {
		flag string
		n    int
	}{{"--keep-last", p.KeepLast}, {"--keep-hourly", p.KeepHourly}, {"--keep-daily", p.KeepDaily},
		{"--keep-weekly", p.KeepWeekly}, {"--keep-monthly", p.KeepMonthly}, {"--keep-yearly", p.KeepYearly}} {
		if kv.n > 0 {
			args = append(args, kv.flag, strconv.Itoa(kv.n))
		}
	}
	return args, nil
}

// pruneItem is one item of "prune --output-format json": a snapshot and whether it stays.
type pruneItem struct {
	Type string `json:"backup-type"`
	ID   string `json:"backup-id"`
	Time int64  `json:"backup-time"`
	Keep bool   `json:"keep"`
}

// opPurge applies the cluster's policy with PBS prune, group by group (one group per
// backed-up host); DryRun only lists. The space comes back with the datastore's garbage
// collection, a job of the PBS.
func opPurge(c *client, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	keep, err := pruneArgs(req.Policy)
	if err != nil {
		return resp, err
	}
	if len(keep) == 0 {
		return resp, nil // no count in the policy: nothing to remove
	}
	snaps, err := c.snapshots()
	if err != nil {
		return resp, err
	}
	groups := map[string]bool{}
	for _, s := range snaps {
		if i := strings.LastIndex(s.ID, "/"); i > 0 {
			groups[s.ID[:i]] = true
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		args := append([]string{"prune", g, "--output-format", "json"}, keep...)
		if req.DryRun {
			args = append(args, "--dry-run")
		}
		out, err := c.run(false, append(args, c.nsArgs()...)...)
		if err != nil {
			return resp, fmt.Errorf("purge %s: %w", g, err)
		}
		var items []pruneItem
		if err := json.Unmarshal(out, &items); err != nil {
			return resp, fmt.Errorf("purge %s: %w", g, err)
		}
		for _, it := range items {
			if !it.Keep {
				resp.Removed = append(resp.Removed, snapshotID(it.Type, it.ID, time.Unix(it.Time, 0)))
			}
		}
	}
	return resp, nil
}

// opRemove deletes the archive: its namespace and every group in it. Refused without a
// namespace: the datastore's root is never deleted by a cluster's archive.
func opRemove(c *client) (wire.Response, error) {
	resp := wire.Response{Location: c.location()}
	if c.namespace == "" {
		return resp, errors.New("remove: the archive has no namespace, the datastore's root is never removed")
	}
	if _, err := c.run(false, "namespace", "delete", c.namespace, "--delete-groups"); err != nil {
		return resp, fmt.Errorf("remove %s: %w", c.location(), err)
	}
	return resp, nil
}
