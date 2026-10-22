// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphadose/haxmap"
	"github.com/signal18/replication-manager/cluster/archiveplugin/plugins/pbs/internal/pbscommon"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// A backup is one pxar archive of the pushed folder (and its catalog), in a host-type
// snapshot of the group host/<backup-id>; the backup id is the request's host. PBS has no
// tags: replication-manager's tags, host and path ride on the first line of the snapshot's
// notes, the one the snapshot list returns as its comment.
const (
	archiveName   = "backup.pxar.didx"
	catalogName   = "catalog.pcat1.didx"
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
	if req.Op == wire.OpSync {
		return resp, errors.New("sync is not supported by archive-pbs: copy between datastores with a sync job of the PBS")
	}
	a, err := newArchive(req.Store)
	if err != nil {
		return resp, err
	}
	switch req.Op {
	case wire.OpProbe:
		return opProbe(a, req)
	case wire.OpPush:
		return opPush(a, req)
	case wire.OpList:
		snaps, err := a.snapshots()
		resp.Snapshots = snaps
		return resp, err
	case wire.OpRestore:
		return opRestore(a, req)
	case wire.OpContents:
		return opContents(a, req)
	case wire.OpForget:
		return opForget(a, req)
	case wire.OpPurge:
		return opPurge(a, req)
	case wire.OpSize:
		snaps, err := a.snapshots()
		for _, s := range snaps {
			resp.Size += s.Size
		}
		return resp, err
	case wire.OpRemove:
		return opRemove(a)
	}
	return resp, fmt.Errorf("unknown op %q", req.Op)
}

// opProbe opens the archive: the snapshot list of its namespace answers when the server,
// the token and the namespace are right. With create, a missing namespace is created level
// by level (the datastore itself is the PBS admin's).
func opProbe(a *archive, req wire.Request) (wire.Response, error) {
	resp, err := probeNamespace(a, req)
	if err != nil || !generatesKey(a, req) {
		return resp, err
	}
	// a new archive is encrypted by default: its client key generated here, stored by
	// replication-manager as the archive's encryption-key secret (no-encrypt: none)
	key, err := newKeyConfig(time.Now())
	if err != nil {
		return resp, err
	}
	resp.Secrets = map[string]string{setKey: key}
	return resp, nil
}

// generatesKey: the probe of a new archive with no key of its own and no-encrypt off.
func generatesKey(a *archive, req wire.Request) bool {
	return req.New && a.crypt == nil && !a.noEncrypt
}

// newKeyConfig is a new client encryption key as a proxmox-backup-client key file (JSON, no
// passphrase): proxmox-backup-client reads it, and so does a paper key printed from it.
func newKeyConfig(now time.Time) (string, error) {
	raw, err := pbscommon.GenerateEncryptionKey()
	if err != nil {
		return "", err
	}
	crypt, err := pbscommon.NewCryptConfig(raw)
	if err != nil {
		return "", err
	}
	ts := now.UTC().Format(time.RFC3339)
	b, err := json.Marshal(pbscommon.KeyConfig{Created: ts, Modified: ts, Data: base64.StdEncoding.EncodeToString(raw), Fingerprint: crypt.Fingerprint()})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// probeNamespace opens the archive's namespace, creating it when asked (see opProbe).
func probeNamespace(a *archive, req wire.Request) (wire.Response, error) {
	resp := wire.Response{Location: a.location()}
	_, err := a.snapshots()
	if err == nil {
		return resp, nil
	}
	if !req.Create || a.namespace == "" {
		return resp, err
	}
	if a.noModify {
		return resp, fmt.Errorf("%w (no-modify: the namespace %s is created on the PBS by its admin, scripts/pbs-cluster-token.sh)", err, a.namespace)
	}
	existing, lerr := a.namespaces()
	if lerr != nil {
		return resp, fmt.Errorf("%w (namespaces: %s)", err, lerr)
	}
	parts := strings.Split(a.namespace, "/")
	for i := range parts {
		ns := strings.Join(parts[:i+1], "/")
		if existing[ns] {
			continue
		}
		q := url.Values{"name": {parts[i]}}
		if i > 0 {
			q.Set("parent", strings.Join(parts[:i], "/"))
		}
		if err := a.api(http.MethodPost, "/namespace", q, nil); err != nil {
			return resp, fmt.Errorf("create namespace %s: %w", ns, err)
		}
		resp.Created = true
	}
	if _, err := a.snapshots(); err != nil {
		return resp, err
	}
	return resp, nil
}

// namespaces lists the namespaces of the datastore.
func (a *archive) namespaces() (map[string]bool, error) {
	var items []struct {
		NS string `json:"ns"`
	}
	if err := a.api(http.MethodGet, "/namespace", url.Values{"max-depth": {"7"}}, &items); err != nil {
		return nil, err
	}
	ns := map[string]bool{}
	for _, it := range items {
		ns[strings.Trim(it.NS, "/")] = true
	}
	return ns, nil
}

// backupIDRe is what PBS refuses in a backup id.
var backupIDRe = regexp.MustCompile(`[^A-Za-z0-9_.\-]`)

// backupID is the backup id of a host: the characters PBS refuses as "_", "repman" when none.
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

// parseSnapshotID splits a snapshot name into its type, id and time.
func parseSnapshotID(name string) (typ, id string, t time.Time, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return "", "", t, fmt.Errorf("snapshot %q is not type/id/time", name)
	}
	t, err = time.Parse("2006-01-02T15:04:05Z", parts[2])
	return parts[0], parts[1], t, err
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

// pbsSnapshot is one item of the datastore's snapshot list; unknown fields are ignored.
type pbsSnapshot struct {
	Type    string `json:"backup-type"`
	ID      string `json:"backup-id"`
	Time    int64  `json:"backup-time"`
	Comment string `json:"comment"`
	Size    int64  `json:"size"`
}

// toWire turns the datastore's snapshot list into wire snapshots, newest first.
func toWire(items []pbsSnapshot) []wire.Snapshot {
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
	return snaps
}

// snapshots lists the archive's snapshots, newest first.
func (a *archive) snapshots() ([]wire.Snapshot, error) {
	var items []pbsSnapshot
	if err := a.api(http.MethodGet, "/snapshots", a.nsValues(nil), &items); err != nil {
		return nil, err
	}
	return toWire(items), nil
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

// snapshotValues is the snapshot's backup-type, backup-id, backup-time and namespace.
func (a *archive) snapshotValues(name string) (url.Values, error) {
	typ, id, t, err := parseSnapshotID(name)
	if err != nil {
		return nil, err
	}
	return a.nsValues(url.Values{"backup-type": {typ}, "backup-id": {id}, "backup-time": {strconv.FormatInt(t.Unix(), 10)}}), nil
}

// opPush archives the folder Path as a new snapshot of the host's group (the pxar archive
// and its catalog, chunks the previous snapshot holds not uploaded again), then writes its
// notes; a notes failure is reported with the snapshot, which exists without its tags.
func opPush(a *archive, req wire.Request) (resp wire.Response, err error) {
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		return resp, fmt.Errorf("push: path %q is not an absolute folder", req.Path)
	}
	dir := filepath.Clean(req.Path)
	if fi, serr := os.Stat(dir); serr != nil {
		return resp, fmt.Errorf("push: %w", serr)
	} else if !fi.IsDir() {
		return resp, fmt.Errorf("push: %s is not a folder", dir)
	}
	id := backupID(req.Host)
	client := a.session(id, 0)
	client.Connect(false, backupType) // sets the snapshot's backup time
	defer client.Close()
	when := time.Unix(client.Manifest.BackupTime, 0).UTC()

	known := haxmap.New[string, bool]()
	prev, err := client.DownloadPreviousToBytes(archiveName)
	if err != nil {
		return resp, fmt.Errorf("push: open the backup session: %w", err)
	}
	for _, d := range pbscommon.ParsePreviousDIDXChunkDigests(prev) {
		known.Set(d, true)
	}

	pxar := &pbscommon.PXARArchive{ArchiveName: archiveName}
	wrPxar, err := client.CreateDynamicIndex(archiveName)
	if err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	wrCat, err := client.CreateDynamicIndex(catalogName)
	if err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	pxarStream := newChunkStream(client, wrPxar, known)
	catStream := newChunkStream(client, wrCat, known)
	pxar.WriteCB = pxarStream.write
	pxar.CatalogWriteCB = catStream.write
	if _, err := pxar.WriteDir(dir, "", true); err != nil {
		return resp, fmt.Errorf("push: archive %s: %w", dir, err)
	}
	if err := pxarStream.close(); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	if err := catStream.close(); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	if err := client.UploadManifest(); err != nil {
		return resp, fmt.Errorf("push: manifest: %w", err)
	}
	if err := client.Finish(); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	}
	logf("pushed %s: %d chunks uploaded, %d reused", dir, pxarStream.added+catStream.added, pxarStream.reused+catStream.reused)

	snap := &wire.Snapshot{ID: snapshotID(backupType, id, when), Time: when, Host: req.Host,
		Tags: append([]string(nil), req.Tags...), Paths: []string{dir}}
	resp.Snapshot = snap
	if len(pxar.ReadErrors) > 0 {
		return resp, fmt.Errorf("push: snapshot %s written without %d file(s) that could not be read: %s",
			snap.ID, len(pxar.ReadErrors), strings.Join(pxar.ReadErrors[:min(3, len(pxar.ReadErrors))], "; "))
	}
	q, _ := a.snapshotValues(snap.ID)
	q.Set("notes", encodeNotes(req.Host, dir, req.Tags))
	if err := a.api(http.MethodPut, "/notes", q, nil); err != nil {
		return resp, fmt.Errorf("push: snapshot %s written, its tags not: %w", snap.ID, err)
	}
	return resp, nil
}

// restoreIncludes turns Include into the archive path to extract: relative to the pushed
// folder (absolute paths under it are made relative); none for everything.
func restoreIncludes(include, pushed string) []string {
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
	return []string{inc}
}

// opRestore restores Snapshot into Target: the pushed folder's content under Target, or
// only its path Include, kept at its place. The archive is read through the PBS reader
// session chunk by chunk (a bounded cache, never the whole archive in memory) and the files
// are streamed to disk.
func opRestore(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if req.Target == "" || !filepath.IsAbs(req.Target) {
		return resp, fmt.Errorf("restore: target %q is not an absolute folder", req.Target)
	}
	snaps, err := a.snapshots()
	if err != nil {
		return resp, err
	}
	snap, err := findSnapshot(snaps, req.Snapshot)
	if err != nil {
		return resp, err
	}
	_, id, when, err := parseSnapshotID(snap.ID)
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
	client := a.session(id, when.Unix())
	client.Connect(true, backupType)
	defer client.Close()
	ra, size, err := client.NewDIDXReaderAt(archiveName, a.chunkCache, nil)
	if err != nil {
		return resp, fmt.Errorf("restore %s: %w", snap.ID, err)
	}
	files, err := pbscommon.NewPXARReaderAt(ra, size).ExtractFiltered(req.Target, restoreIncludes(req.Include, pushed), true)
	if err != nil {
		return resp, fmt.Errorf("restore %s: %w", snap.ID, err)
	}
	resp.Files = int64(len(files))
	resp.Snapshot = &snap
	return resp, nil
}

// catalogMaxBytes bounds the catalog read for contents: a catalog lists names, sizes and times
// only, a few dozen bytes an entry.
const catalogMaxBytes = 256 << 20

// opContents lists the entries of the folder Include (relative to the pushed folder, "" the
// pushed folder itself) of Snapshot, from the snapshot's catalog: the archive is not read.
func opContents(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	snaps, err := a.snapshots()
	if err != nil {
		return resp, err
	}
	snap, err := findSnapshot(snaps, req.Snapshot)
	if err != nil {
		return resp, err
	}
	_, id, when, err := parseSnapshotID(snap.ID)
	if err != nil {
		return resp, err
	}
	pushed := ""
	if len(snap.Paths) > 0 {
		pushed = snap.Paths[0]
	}
	client := a.session(id, when.Unix())
	client.Connect(true, backupType)
	defer client.Close()
	ra, size, err := client.NewDIDXReaderAt(catalogName, a.chunkCache, nil)
	if err != nil {
		return resp, fmt.Errorf("contents %s: %w", snap.ID, err)
	}
	if size > catalogMaxBytes {
		return resp, fmt.Errorf("contents %s: catalog of %d bytes, over %d", snap.ID, size, catalogMaxBytes)
	}
	data := make([]byte, size)
	if _, err := ra.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
		return resp, fmt.Errorf("contents %s: %w", snap.ID, err)
	}
	entries, err := pbscommon.ParseCatalog(data)
	if err != nil {
		return resp, fmt.Errorf("contents %s: %w", snap.ID, err)
	}
	folder := ""
	if inc := restoreIncludes(req.Include, pushed); len(inc) > 0 {
		folder = inc[0]
	}
	resp.Entries = folderEntries(entries, folder)
	resp.Snapshot = &snap
	return resp, nil
}

// folderEntries are the catalog entries directly in folder ("" the root).
func folderEntries(entries []pbscommon.CatalogEntry, folder string) []wire.Entry {
	folder = strings.Trim(folder, "/")
	out := make([]wire.Entry, 0)
	for _, e := range entries {
		p := strings.Trim(e.Path, "/")
		dir, name := path.Split(p)
		if strings.TrimSuffix(dir, "/") != folder || name == "" {
			continue
		}
		out = append(out, wire.Entry{Name: name, Dir: e.IsDir, Size: int64(e.Size)})
	}
	return out
}

// opForget deletes one snapshot.
func opForget(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if a.noPurge {
		return resp, errors.New("forget: the archive is no-purge, its token may not delete snapshots: the PBS prunes them (scripts/pbs-cluster-allow-purge.sh to allow it)")
	}
	snaps, err := a.snapshots()
	if err != nil {
		return resp, err
	}
	snap, err := findSnapshot(snaps, req.Snapshot)
	if err != nil {
		return resp, err
	}
	q, err := a.snapshotValues(snap.ID)
	if err != nil {
		return resp, err
	}
	if err := a.api(http.MethodDelete, "/snapshots", q, nil); err != nil {
		return resp, fmt.Errorf("forget %s: %w", snap.ID, err)
	}
	resp.Removed = []string{snap.ID}
	return resp, nil
}

// pruneValues is the cluster's policy as PBS prune parameters. PBS prunes by counts only:
// a keep-within window has no PBS equivalent and is refused, never ignored (keeping less
// than the policy says would delete backups the cluster wants kept).
func pruneValues(p *wire.Policy) (url.Values, error) {
	q := url.Values{}
	if p == nil {
		return q, nil
	}
	for name, w := range map[string]string{
		"keep-within": p.KeepWithin, "keep-within-hourly": p.KeepWithinHourly, "keep-within-daily": p.KeepWithinDaily,
		"keep-within-weekly": p.KeepWithinWeekly, "keep-within-monthly": p.KeepWithinMonthly, "keep-within-yearly": p.KeepWithinYearly,
	} {
		if strings.TrimSpace(w) != "" {
			return nil, fmt.Errorf("purge: %s has no Proxmox Backup Server equivalent (PBS prunes by counts: last, hourly, daily, weekly, monthly, yearly)", name)
		}
	}
	for _, kv := range []struct {
		name string
		n    int
	}{{"keep-last", p.KeepLast}, {"keep-hourly", p.KeepHourly}, {"keep-daily", p.KeepDaily},
		{"keep-weekly", p.KeepWeekly}, {"keep-monthly", p.KeepMonthly}, {"keep-yearly", p.KeepYearly}} {
		if kv.n > 0 {
			q.Set(kv.name, strconv.Itoa(kv.n))
		}
	}
	return q, nil
}

// pruneItem is one item of a prune answer: a snapshot and whether it stays.
type pruneItem struct {
	Type string `json:"backup-type"`
	ID   string `json:"backup-id"`
	Time int64  `json:"backup-time"`
	Keep bool   `json:"keep"`
}

// opPurge applies the cluster's policy with PBS prune, group by group (one group per
// backed-up host); DryRun only lists. The space comes back with the datastore's garbage
// collection, a job of the PBS.
func opPurge(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if a.noPurge {
		// not an error: repman purges after every backup, the retention is the PBS's prune job
		logf("purge skipped: the archive is no-purge, the PBS's prune job keeps the retention")
		return resp, nil
	}
	keep, err := pruneValues(req.Policy)
	if err != nil {
		return resp, err
	}
	if len(keep) == 0 {
		return resp, nil // no count in the policy: nothing to remove
	}
	snaps, err := a.snapshots()
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
		typ, id, _ := strings.Cut(g, "/")
		q := a.nsValues(url.Values{"backup-type": {typ}, "backup-id": {id}})
		for k, v := range keep {
			q[k] = v
		}
		if req.DryRun {
			q.Set("dry-run", "true")
		}
		var items []pruneItem
		if err := a.api(http.MethodPost, "/prune", q, &items); err != nil {
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
func opRemove(a *archive) (wire.Response, error) {
	resp := wire.Response{Location: a.location()}
	if a.namespace == "" {
		return resp, errors.New("remove: the archive has no namespace, the datastore's root is never removed")
	}
	if a.noModify {
		return resp, errors.New("remove: the archive is no-modify, its namespace is removed on the PBS by its admin")
	}
	q := url.Values{"ns": {a.namespace}, "delete-groups": {"true"}}
	if err := a.api(http.MethodDelete, "/namespace", q, nil); err != nil {
		return resp, fmt.Errorf("remove %s: %w", a.location(), err)
	}
	return resp, nil
}
