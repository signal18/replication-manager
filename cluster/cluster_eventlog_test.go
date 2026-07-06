// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func newEventLogTestCluster(t *testing.T, author int) *Cluster {
	t.Helper()
	return &Cluster{
		Name: "c1",
		Conf: &config.Config{
			WorkingDir:             t.TempDir(),
			ArbitrationSasUniqueId: author,
		},
	}
}

func readEventLog(t *testing.T, cl *Cluster) []ConfigChangeEvent {
	t.Helper()
	f, err := os.Open(EventLogPath(cl.Conf.WorkingDir, cl.Conf.ArbitrationSasUniqueId))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("open event log: %s", err)
	}
	defer f.Close()
	var out []ConfigChangeEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var ev ConfigChangeEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("bad event line %q: %s", sc.Text(), err)
		}
		out = append(out, ev)
	}
	return out
}

func TestEmitConfigChangeEventsDiffAndEcho(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)

	oldData := []byte("[saved-c1]\ntitle = \"c1\" \nbar = 1\nfoo = \"a\"\ngone = \"x\"\n")
	newData := []byte("[saved-c1]\ntitle = \"c1\" \nbar = 1\nbaz = \"peer\"\nfoo = \"b\"\n")

	// baz was just replayed from peer 2: it must be subtracted, not re-emitted.
	cl.RecordEventProvenance("baz", "peer", time.Now(), 2)

	cl.emitConfigChangeEvents(oldData, newData, "saved-c1")

	events := readEventLog(t, cl)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (foo set, gone unset), got %d: %+v", len(events), events)
	}
	byKey := map[string]ConfigChangeEvent{}
	for _, ev := range events {
		byKey[ev.Key] = ev
		if ev.Author != 1 {
			t.Errorf("event %s: author = %d, want 1", ev.Key, ev.Author)
		}
		if ev.Cluster != "c1" {
			t.Errorf("event %s: cluster = %s, want c1", ev.Key, ev.Cluster)
		}
	}
	if ev, ok := byKey["foo"]; !ok || ev.Action != "set" || ev.Value != "b" {
		t.Errorf("foo event wrong: %+v", byKey["foo"])
	}
	if ev, ok := byKey["gone"]; !ok || ev.Action != "unset" || ev.Value != "" {
		t.Errorf("gone event wrong: %+v", byKey["gone"])
	}
	if _, ok := byKey["baz"]; ok {
		t.Errorf("baz replay echo was re-emitted: ping-pong")
	}
	if _, ok := byKey["bar"]; ok {
		t.Errorf("unchanged key bar produced an event")
	}
	// unchanged title must never appear
	if _, ok := byKey["title"]; ok {
		t.Errorf("title key produced an event")
	}

	// Locally-born changes must be recorded as own provenance.
	if p, ok := cl.GetEventProvenance("foo"); !ok || p.Author != 1 || p.Value != "b" {
		t.Errorf("foo provenance wrong: %+v ok=%v", p, ok)
	}
}

func TestSaveConfigArtifactCrashSafe(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	dir := filepath.Join(cl.Conf.WorkingDir, "c1")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "c1.toml")

	// First save: baseline — file created, changed=true, NO events.
	v1 := []byte("[saved-c1]\ntitle = \"c1\" \nfoo = \"a\"\n")
	changed, err := cl.saveConfigArtifact(path, v1, "saved-c1", true)
	if err != nil || !changed {
		t.Fatalf("first save: changed=%v err=%v", changed, err)
	}
	if events := readEventLog(t, cl); len(events) != 0 {
		t.Fatalf("baseline save emitted %d events, want 0", len(events))
	}

	// Identical content: no rewrite, changed=false.
	before, _ := os.Stat(path)
	changed, err = cl.saveConfigArtifact(path, v1, "saved-c1", true)
	if err != nil || changed {
		t.Fatalf("identical save: changed=%v err=%v", changed, err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("identical content rewrote the file")
	}

	// Changed content: events emitted, file replaced, no .new leftover.
	v2 := []byte("[saved-c1]\ntitle = \"c1\" \nfoo = \"b\"\n")
	changed, err = cl.saveConfigArtifact(path, v2, "saved-c1", true)
	if err != nil || !changed {
		t.Fatalf("second save: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(v2) {
		t.Errorf("file content not replaced")
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf(".new temp file left behind")
	}
	events := readEventLog(t, cl)
	if len(events) != 1 || events[0].Key != "foo" || events[0].Value != "b" {
		t.Fatalf("expected single foo=b event, got %+v", events)
	}
}

func TestEventProvenanceImportDoesNotOverwrite(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	now := time.Now()
	cl.RecordEventProvenance("k", "mem", now, 1)
	cl.ImportEventProvenance(map[string]EventProvenanceEntry{
		"k":     {Value: "disk", Ts: now.Add(-time.Hour), Author: 2},
		"fresh": {Value: "disk", Ts: now.Add(-time.Hour), Author: 2},
	})
	if p, _ := cl.GetEventProvenance("k"); p.Value != "mem" {
		t.Errorf("import overwrote in-memory provenance: %+v", p)
	}
	if p, ok := cl.GetEventProvenance("fresh"); !ok || p.Value != "disk" {
		t.Errorf("import did not seed missing key: %+v ok=%v", p, ok)
	}
}
