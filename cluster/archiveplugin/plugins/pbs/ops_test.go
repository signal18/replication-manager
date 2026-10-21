// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/signal18/replication-manager/cluster/archiveplugin/plugins/pbs/internal/pbscommon"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
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
	when := time.Date(2026, 10, 20, 9, 5, 7, 0, time.UTC)
	name := snapshotID("host", "db1", when)
	if name != "host/db1/2026-10-20T09:05:07Z" {
		t.Fatalf("snapshotID: %q", name)
	}
	typ, id, back, err := parseSnapshotID(name)
	if err != nil || typ != "host" || id != "db1" || !back.Equal(when) {
		t.Fatalf("parseSnapshotID: %s %s %s %v", typ, id, back, err)
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

func TestRestoreIncludes(t *testing.T) {
	for _, tc := range []struct {
		include, pushed string
		want            []string
	}{
		{"", "/b/1", nil},
		{"/", "/b/1", nil},
		{"dump.sql.gz", "/b/1", []string{"dump.sql.gz"}},
		{"/b/1/sub/x", "/b/1", []string{"sub/x"}},
		{"/b/1", "/b/1", nil},
	} {
		if got := restoreIncludes(tc.include, tc.pushed); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("restoreIncludes(%q, %q) = %v, want %v", tc.include, tc.pushed, got, tc.want)
		}
	}
}

func TestPruneValues(t *testing.T) {
	q, err := pruneValues(&wire.Policy{KeepLast: 3, KeepDaily: 7})
	if err != nil || q.Get("keep-last") != "3" || q.Get("keep-daily") != "7" || len(q) != 2 {
		t.Fatalf("counts: %v %v", q, err)
	}
	if _, err := pruneValues(&wire.Policy{KeepLast: 3, KeepWithinDaily: "7d"}); err == nil {
		t.Fatal("a keep-within window has no PBS equivalent: refused, never ignored")
	}
}

func TestNewArchiveValidation(t *testing.T) {
	t.Setenv(wire.SecretEnv(setSecret), "s")
	base := map[string]string{setBaseURL: "https://pbs:8007", setDatastore: "ds", setAuthID: "u@pbs!t"}
	if _, err := newArchive(wire.Store{Plugin: pluginPBS, Settings: base}); err != nil {
		t.Fatalf("a complete store: %v", err)
	}
	for name, mutate := range map[string]func(m map[string]string){
		"http":       func(m map[string]string) { m[setBaseURL] = "http://pbs:8007" },
		"not token":  func(m map[string]string) { m[setAuthID] = "u@pbs" },
		"no store":   func(m map[string]string) { delete(m, setDatastore) },
		"cache zero": func(m map[string]string) { m[setChunkCache] = "0" },
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		mutate(m)
		if _, err := newArchive(wire.Store{Plugin: pluginPBS, Settings: m}); err == nil {
			t.Errorf("%s: refused", name)
		}
	}
	t.Setenv(wire.SecretEnv(setKey), "not a key")
	if _, err := newArchive(wire.Store{Plugin: pluginPBS, Settings: base}); err == nil {
		t.Fatal("an unreadable encryption key is refused")
	}
}

// fakePBS is the datastore API of a PBS: snapshots, notes, forget, prune and namespaces of
// the datastore "ds", answering as the PBS does ({"data": ...}), with the calls recorded.
type fakePBS struct {
	mu         sync.Mutex
	calls      []string
	namespaces map[string]bool
	snapshots  []pbsSnapshot
}

func (f *fakePBS) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "PBSAPIToken=repman@pbs!arch:s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api2/json/admin/datastore/ds")
		f.calls = append(f.calls, route+" "+r.Form.Encode())
		answer := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": v}) }
		switch route {
		case "GET /snapshots":
			if ns := r.Form.Get("ns"); ns != "" && !f.namespaces[ns] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, "namespace not found")
				return
			}
			answer(f.snapshots)
		case "PUT /notes", "DELETE /snapshots":
			answer(nil)
		case "POST /prune":
			id := r.Form.Get("backup-id")
			answer([]pruneItem{{Type: "host", ID: id, Time: 1760950000, Keep: false}, {Type: "host", ID: id, Time: 1760960000, Keep: true}})
		case "GET /namespace":
			var items []map[string]string
			for n := range f.namespaces {
				items = append(items, map[string]string{"ns": n})
			}
			answer(items)
		case "POST /namespace":
			name := r.Form.Get("name")
			if p := r.Form.Get("parent"); p != "" {
				name = p + "/" + name
			}
			f.namespaces[name] = true
			answer(nil)
		case "DELETE /namespace":
			delete(f.namespaces, r.Form.Get("ns"))
			answer(nil)
		default:
			t.Errorf("unexpected call %s", route)
			w.WriteHeader(http.StatusNotImplemented)
		}
	})
}

// newFake starts the fake PBS over TLS and returns the store pinned on its certificate.
func newFake(t *testing.T, ns string) (*fakePBS, wire.Store) {
	t.Helper()
	f := &fakePBS{namespaces: map[string]bool{"": true, "cl": true}, snapshots: []pbsSnapshot{
		{Type: "host", ID: "db1_3306", Time: 1760950000, Size: 10, Comment: encodeNotes("db1:3306", "/b/1", []string{"t1"})},
		{Type: "host", ID: "db1_3306", Time: 1760960000, Size: 20},
	}}
	srv := httptest.NewTLSServer(f.handler(t))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	t.Setenv(wire.SecretEnv(setSecret), "s3cret")
	return f, wire.Store{Plugin: pluginPBS, Name: "pbs1", Settings: map[string]string{
		setBaseURL: srv.URL, setDatastore: "ds", setNamespace: ns, setAuthID: "repman@pbs!arch",
		setFingerprint: strings.ToUpper(hex.EncodeToString(sum[:]))}}
}

func (f *fakePBS) called(t *testing.T, want string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, want) {
			return
		}
	}
	t.Fatalf("no call %q in:\n%s", want, strings.Join(f.calls, "\n"))
}

func TestAdministrationOperations(t *testing.T) {
	f, st := newFake(t, "cl")

	resp, err := handle(wire.Request{Op: wire.OpList, Store: st})
	if err != nil || len(resp.Snapshots) != 2 || resp.Snapshots[0].Time.Unix() != 1760960000 ||
		resp.Snapshots[1].Host != "db1:3306" || resp.Snapshots[1].Tags[0] != "t1" || resp.Snapshots[1].Paths[0] != "/b/1" {
		t.Fatalf("list, newest first, notes decoded: %+v %v", resp, err)
	}
	f.called(t, "GET /snapshots ns=cl")

	resp, err = handle(wire.Request{Op: wire.OpSize, Store: st})
	if err != nil || resp.Size != 30 {
		t.Fatalf("size: %+v %v", resp, err)
	}

	resp, err = handle(wire.Request{Op: wire.OpForget, Store: st, Snapshot: "host/db1_3306/2025-10-20T08:46"})
	if err != nil || len(resp.Removed) != 1 {
		t.Fatalf("forget by unique prefix: %+v %v", resp, err)
	}
	f.called(t, "DELETE /snapshots backup-id=db1_3306&backup-time=1760950000&backup-type=host&ns=cl")
	if _, err := handle(wire.Request{Op: wire.OpForget, Store: st, Snapshot: "host/db1_3306/"}); err == nil {
		t.Fatal("an ambiguous prefix is refused")
	}

	resp, err = handle(wire.Request{Op: wire.OpPurge, Store: st, Policy: &wire.Policy{KeepLast: 1}, DryRun: true})
	if err != nil || len(resp.Removed) != 1 || resp.Removed[0] != "host/db1_3306/2025-10-20T08:46:40Z" {
		t.Fatalf("purge: %+v %v", resp, err)
	}
	f.called(t, "POST /prune backup-id=db1_3306&backup-type=host&dry-run=true&keep-last=1&ns=cl")

	if resp, err := handle(wire.Request{Op: wire.OpPurge, Store: st, Policy: &wire.Policy{}}); err != nil || len(resp.Removed) != 0 {
		t.Fatalf("a policy without counts removes nothing: %+v %v", resp, err)
	}
}

func TestProbeCreatesMissingNamespaceLevels(t *testing.T) {
	f, st := newFake(t, "cl/new/deep")
	resp, err := handle(wire.Request{Op: wire.OpProbe, Store: st, Create: true})
	if err != nil || !resp.Created || !strings.HasSuffix(resp.Location, "/ds/cl/new/deep") {
		t.Fatalf("probe: %+v %v", resp, err)
	}
	f.called(t, "POST /namespace name=new&parent=cl")
	f.called(t, "POST /namespace name=deep&parent=cl%2Fnew")
	for _, c := range f.calls {
		if c == "POST /namespace name=cl" {
			t.Fatal("an existing level is not created again")
		}
	}
	_, other := newFake(t, "other")
	if _, err := handle(wire.Request{Op: wire.OpProbe, Store: other}); err == nil {
		t.Fatal("a missing namespace without create fails the probe")
	}
}

func TestRemoveAndRefusals(t *testing.T) {
	f, st := newFake(t, "cl")
	if _, err := handle(wire.Request{Op: wire.OpRemove, Store: st}); err != nil || f.namespaces["cl"] {
		t.Fatalf("remove deletes the namespace and its groups: %v", err)
	}
	f.called(t, "DELETE /namespace delete-groups=true&ns=cl")

	_, root := newFake(t, "")
	if _, err := handle(wire.Request{Op: wire.OpRemove, Store: root}); err == nil || !strings.Contains(err.Error(), "never removed") {
		t.Fatalf("remove without a namespace is refused: %v", err)
	}
	if _, err := handle(wire.Request{Op: wire.OpSync, Store: st}); err == nil {
		t.Fatal("sync is refused")
	}
	if _, err := handle(wire.Request{Op: wire.OpList, Store: wire.Store{Plugin: "archive-plakar-local"}}); err == nil {
		t.Fatal("another plugin's store is refused")
	}
}

// A certificate that is not the pinned one is refused before any request.
func TestFingerprintPinning(t *testing.T) {
	_, st := newFake(t, "cl")
	st.Settings[setFingerprint] = strings.Repeat("AB", 32)
	if _, err := handle(wire.Request{Op: wire.OpList, Store: st}); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("a wrong fingerprint is refused: %v", err)
	}
}

// A no-purge, no-modify archive (a backup-only token) never sends what the PBS would
// refuse: purge is a no-op (the PBS's prune job keeps the retention), forget, namespace
// creation and remove are refused with the reason, and no such call reaches the PBS.
func TestNoPurgeNoModify(t *testing.T) {
	f, st := newFake(t, "cl/missing")
	st.Settings[setNoPurge] = "true"
	st.Settings[setNoModify] = "true"

	if resp, err := handle(wire.Request{Op: wire.OpPurge, Store: st, Policy: &wire.Policy{KeepLast: 1}}); err != nil || len(resp.Removed) != 0 {
		t.Fatalf("no-purge: purge is a no-op, not an error: %+v %v", resp, err)
	}
	if _, err := handle(wire.Request{Op: wire.OpForget, Store: st, Snapshot: "host/db1_3306/2025-10-20T08:46:40Z"}); err == nil || !strings.Contains(err.Error(), "no-purge") {
		t.Fatalf("no-purge: forget refused with the reason: %v", err)
	}
	if _, err := handle(wire.Request{Op: wire.OpProbe, Store: st, Create: true}); err == nil || !strings.Contains(err.Error(), "no-modify") {
		t.Fatalf("no-modify: a missing namespace is not created: %v", err)
	}
	if _, err := handle(wire.Request{Op: wire.OpRemove, Store: st}); err == nil || !strings.Contains(err.Error(), "no-modify") {
		t.Fatalf("no-modify: remove refused: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Fatalf("only reads reach the PBS, got %q", c)
		}
	}
	if _, err := newArchive(wire.Store{Plugin: pluginPBS, Settings: map[string]string{setBaseURL: "https://p", setDatastore: "d", setAuthID: "u@pbs!t", setNoPurge: "maybe"}}); err == nil {
		t.Fatal("a no-purge that is not a bool is refused")
	}
}

// #2091: contents answers the entries directly in the folder asked, from the catalog
func TestFolderEntries(t *testing.T) {
	entries := []pbscommon.CatalogEntry{
		{Path: "mysqldump.meta.json", Size: 10},
		{Path: "mydumper", IsDir: true},
		{Path: "mydumper/metadata", Size: 5},
		{Path: "/binlog.000001", Size: 7},
	}
	got := folderEntries(entries, "")
	if len(got) != 3 || got[0].Name != "mysqldump.meta.json" || got[0].Size != 10 || !got[1].Dir || got[2].Name != "binlog.000001" {
		t.Fatalf("root %+v", got)
	}
	if got := folderEntries(entries, "/mydumper/"); len(got) != 1 || got[0].Name != "metadata" {
		t.Fatalf("mydumper %+v", got)
	}
}
