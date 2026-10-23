// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2077: the save that migrates a cluster replaces its legacy archive keys with the list.
// The diff emits one event per archive setting, and none for the legacy keys leaving the
// file: a peer replaying their unset would reset the values the list now carries. The
// connection to archive-local-1 is a plain key.
func TestArchiveMigrationSaveEvents(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)

	oldData := []byte("[saved-c1]\ntitle = \"c1\" \nbackup-archive-mode = \"restic-local\"\nbackup-restic = true\nbackup-restic-local-repository = \"/data/restic\"\nfoo = \"a\"\n")
	newData := []byte("[saved-c1]\ntitle = \"c1\" \nfoo = \"a\"\nbackup-archive-name = \"archive-local-1\"\n\n[[saved-c1.archive]]\nname = \"archive-local-1\"\nplugin = \"archive-restic-local\"\nlocal-repository = \"/data/restic\"\n")

	cl.emitConfigChangeEvents(oldData, newData, "saved-c1")

	got := map[string]ConfigChangeEvent{}
	for _, ev := range readEventLog(t, cl) {
		got[ev.Key] = ev
	}
	want := map[string]string{
		"backup-archive-name":                      "archive-local-1",
		"archive.archive-local-1.name":             "archive-local-1",
		"archive.archive-local-1.plugin":           "archive-restic-local",
		"archive.archive-local-1.local-repository": "/data/restic",
	}
	if len(got) != len(want) {
		t.Fatalf("events %+v, want %v", got, want)
	}
	for k, v := range want {
		if ev, ok := got[k]; !ok || ev.Action != "set" || ev.Value != v {
			t.Errorf("event %s: %+v, want set %q", k, got[k], v)
		}
	}
}

// #2077: one setting changed in one archive is one event, keyed by the archive's name
func TestArchiveSettingChangeIsOneEvent(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	base := "[saved-c1]\ntitle = \"c1\" \n\n[[saved-c1.archive]]\nname = \"archive-local-1\"\nplugin = \"archive-restic-local\"\ntags = \"%s\"\n\n[[saved-c1.archive]]\nname = \"archive-s3-1\"\nplugin = \"archive-restic-s3\"\naws-bucket = \"bk\"\n"
	oldData := []byte(fmt.Sprintf(base, "a"))
	newData := []byte(fmt.Sprintf(base, "b"))
	cl.emitConfigChangeEvents(oldData, newData, "saved-c1")
	events := readEventLog(t, cl)
	if len(events) != 1 || events[0].Key != "archive.archive-local-1.tags" || events[0].Value != "b" {
		t.Fatalf("events %+v, want one set archive.archive-local-1.tags=b", events)
	}
}

// #2091: a pushed inject.toml in TOML gives its flat keys as before, a value holding = whole
func TestInjectConfigFlatKeys(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.applyInjectConfig([]byte("monitoring-ticker = 3\nprov-db-tags = \"a,b\"\nalert-slack-url = \"https://x/y?a=b\"\n"))
	for k, want := range map[string]string{"monitoring-ticker": "3", "prov-db-tags": "a,b", "alert-slack-url": "https://x/y?a=b"} {
		if v, _ := cl.Conf.GetDynamicFlag(k); fmt.Sprintf("%v", v) != want {
			t.Errorf("%s = %v, want %s", k, v, want)
		}
	}
	// not TOML (an unquoted value): read line by line as before
	cl.applyInjectConfig([]byte("prov-db-tags = c,d\n"))
	if v, _ := cl.Conf.GetDynamicFlag("prov-db-tags"); v != "c,d" {
		t.Errorf("legacy line: %v", v)
	}
}

// #2091: an [[archive]] block of inject.toml is read as an archive spec
func TestInjectArchiveSpec(t *testing.T) {
	spec := injectArchiveSpec(map[string]interface{}{"name": "archive-bso-s3-uk1", "plugin": "archive-restic-s3", "aws-bucket": "uk1", "timeout": int64(60)})
	if spec.Name != "archive-bso-s3-uk1" || spec.Plugin != "archive-restic-s3" || spec.Settings["aws-bucket"] != "uk1" || spec.Settings["timeout"] != int64(60) || len(spec.Settings) != 2 {
		t.Fatalf("spec %+v", spec)
	}
}

// #2091: an [[s3-provider]] block of inject.toml adds the storage, an unchanged block is a
// no-op, a changed one replaces it, an invalid one is refused
func TestInjectS3Provider(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.WorkingDir = t.TempDir()
	block := map[string]interface{}{"name": "cloud18-ovh-fr-2", "endpoint": "https://minio.example", "region": "eu-west-1", "cloud18-sso": true, "role-arn": "arn:minio:iam:eu-west-1::role/x"}
	p := config.S3ProviderFromSettings(block)
	if p.ProviderSource != "custom" || !p.Cloud18SSO || p.RoleArn != "arn:minio:iam:eu-west-1::role/x" {
		t.Fatalf("provider %+v", p)
	}
	if changed, err := cl.putInjectedS3Provider(p); !changed || err != nil {
		t.Fatalf("add: %v %v", changed, err)
	}
	if changed, err := cl.putInjectedS3Provider(p); changed || err != nil {
		t.Fatalf("an unchanged block must be a no-op: %v %v", changed, err)
	}
	block["region"] = "eu-west-3"
	if changed, err := cl.putInjectedS3Provider(config.S3ProviderFromSettings(block)); !changed || err != nil {
		t.Fatalf("update: %v %v", changed, err)
	}
	if ps := cl.GetS3ProvidersSnapshot(); len(ps) != 1 || ps[0].Region != "eu-west-3" {
		t.Fatalf("providers %+v", ps)
	}
	block["access-key"] = "id"
	if _, err := cl.putInjectedS3Provider(config.S3ProviderFromSettings(block)); err == nil {
		t.Fatal("an SSO provider with keys must be refused")
	}
}
