// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package wire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// a request and a response cross the wire whole; another wire version is refused; a plugin's
// error comes back as an error; a secret's environment name follows its setting name
func TestWire(t *testing.T) {
	var buf bytes.Buffer
	in := Request{WireVersion: WireVersion, Op: OpPush, Store: Store{Plugin: "archive-plakar-s3", Name: "a1", Settings: map[string]string{"bucket": "b"}}, Path: "/backups/c1", Tags: []string{"line:scheduled"}}
	if err := writeJSON(&buf, in); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&buf)
	if err != nil || got.Op != OpPush || got.Store.Settings["bucket"] != "b" || got.Path != "/backups/c1" {
		t.Fatalf("request %+v %v", got, err)
	}
	if _, err := ReadRequest(strings.NewReader(`{"wireVersion":99,"op":"push"}`)); err == nil {
		t.Fatal("another wire version must be refused")
	}

	buf.Reset()
	WriteResponse(&buf, Response{}, errors.New("no such bucket"))
	if _, err := ReadResponse(&buf); err == nil || err.Error() != "no such bucket" {
		t.Fatalf("the plugin's error: %v", err)
	}
	buf.Reset()
	WriteResponse(&buf, Response{Snapshot: &Snapshot{ID: "abc"}}, nil)
	if r, err := ReadResponse(&buf); err != nil || r.Snapshot.ID != "abc" {
		t.Fatalf("response %+v %v", r, err)
	}

	if e := SecretEnv("aws-access-secret"); e != "REPMAN_ARCHIVEPLUGIN_SECRET_AWS_ACCESS_SECRET" {
		t.Fatalf("env %s", e)
	}
}

func writeJSON(buf *bytes.Buffer, req Request) error {
	return WriteRequest(buf, req)
}
