// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
)

// A line of any length never stops the reading: cut at max, the next lines read (#2246).
func TestReadLinesBounded(t *testing.T) {
	long := strings.Repeat("x", 10000)
	var got []string
	readLines(strings.NewReader("a\n"+long+"\nb\nlast"), 100, func(l string) { got = append(got, l) })
	if len(got) != 4 || got[0] != "a" || len(got[1]) != 100 || got[2] != "b" || got[3] != "last" {
		t.Fatalf("lines: %d %q", len(got), got)
	}
}

// The server's credentials are the last ones its parent sent, taken up at once.
func TestRelayedCredentials(t *testing.T) {
	r := &relayedCredentials{}
	creds := credentials.NewCredentials(r)
	if _, err := creds.Get(); err == nil {
		t.Fatal("credentials before any was sent")
	}
	r.set(&credentialsMessage{AccessKeyID: "a", SecretAccessKey: "s1"})
	if v, err := creds.Get(); err != nil || v.SecretAccessKey != "s1" {
		t.Fatalf("first: %v %v", v.SecretAccessKey, err)
	}
	if r.IsExpired() {
		t.Fatal("expired with nothing new and no end")
	}
	r.set(&credentialsMessage{AccessKeyID: "a", SecretAccessKey: "s2", SessionToken: "t", Expires: time.Now().Add(time.Hour)})
	if v, err := creds.Get(); err != nil || v.SecretAccessKey != "s2" || v.SessionToken != "t" {
		t.Fatalf("renewed: %v %v", v.SecretAccessKey, err)
	}
	r.set(&credentialsMessage{AccessKeyID: "a", SecretAccessKey: "s3", Expires: time.Now().Add(-time.Second)})
	creds.Get()
	if !r.IsExpired() {
		t.Fatal("past their end: expired")
	}
	if v, _ := creds.Get(); v.SecretAccessKey != "s3" {
		t.Fatal("past their end, the last ones are kept")
	}
}

// The parent's messages decode as the server reads them: the mount, then the credentials.
func TestRequestRoundTrip(t *testing.T) {
	var b strings.Builder
	for _, r := range []request{
		{Mount: &mountRequest{MountPoint: "/m", Bucket: "b:p", OpTimeout: 2 * time.Minute}},
		{Credentials: &credentialsMessage{AccessKeyID: "a", SecretAccessKey: "s"}},
	} {
		j, _ := json.Marshal(r)
		b.Write(append(j, '\n'))
	}
	dec := json.NewDecoder(strings.NewReader(b.String()))
	var m, c request
	if dec.Decode(&m) != nil || dec.Decode(&c) != nil {
		t.Fatal("decode")
	}
	if m.Mount == nil || m.Mount.Bucket != "b:p" || m.Mount.OpTimeout != 2*time.Minute || m.Credentials != nil {
		t.Fatalf("mount: %+v", m)
	}
	if c.Credentials == nil || c.Credentials.SecretAccessKey != "s" || c.Mount != nil {
		t.Fatalf("credentials: %+v", c)
	}
}
