// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// #2043 review: the monitor writes RotatePasswordsRefusal and Credentials each tick while
// API goroutines serialize the cluster. Run with -race.
func TestPublishedStoreWhileMarshal(t *testing.T) {
	var view struct {
		Refusal     published[string]           `json:"rotatePasswordsRefusal"`
		Credentials published[[]CredentialInfo] `json:"credentials"`
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the monitor
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			view.Refusal.Store(fmt.Sprintf("refused %d", i))
			view.Credentials.Store(make([]CredentialInfo, i%5))
		}
	}()
	go func() { // the API
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			if _, err := json.Marshal(&view); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

// the JSON is the value itself, as before the fields were published (the GUI reads
// rotatePasswordsRefusal as a string and credentials as an array)
func TestPublishedJSON(t *testing.T) {
	var view struct {
		Refusal     published[string]           `json:"rotatePasswordsRefusal"`
		Credentials published[[]CredentialInfo] `json:"credentials"`
	}
	out, _ := json.Marshal(&view)
	if string(out) != `{"rotatePasswordsRefusal":"","credentials":null}` {
		t.Fatalf("before a store: %s", out)
	}
	view.Refusal.Store("versioning off")
	view.Credentials.Store([]CredentialInfo{{Key: "db-servers-credential", User: "root", Source: "dynamic-config"}})
	out, _ = json.Marshal(&view)
	want := `{"rotatePasswordsRefusal":"versioning off","credentials":[{"key":"db-servers-credential","user":"root","source":"dynamic-config","refusal":"","initial":"","versions":0,"lastChange":"0001-01-01T00:00:00Z"}]}`
	if string(out) != want {
		t.Fatalf("got %s\nwant %s", out, want)
	}
}

// the API masks a server through a JSON round trip (topology/master, topology/slaves)
// and the CLI decodes the same JSON: a published field must decode what it encodes
func TestPublishedServerRoundTrip(t *testing.T) {
	srv := &ServerMonitor{}
	srv.GraphiteHost.Store("db1.example.test")
	data, err := json.Marshal(srv)
	if err != nil {
		t.Fatal(err)
	}
	var decoded *ServerMonitor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.GraphiteHost.Load(); got != "db1.example.test" {
		t.Fatalf("graphiteHost %q", got)
	}

	var view struct {
		Credentials published[[]CredentialInfo] `json:"credentials"`
	}
	if err := json.Unmarshal([]byte(`{"credentials":[{"key":"db-servers-credential","user":"root"}]}`), &view); err != nil {
		t.Fatal(err)
	}
	if c := view.Credentials.Load(); len(c) != 1 || c[0].User != "root" {
		t.Fatalf("credentials %+v", c)
	}

	// null stores the zero value, as a fresh published field reads before any Store
	if err := json.Unmarshal([]byte(`{"graphiteHost":null}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.GraphiteHost.Load(); got != "" {
		t.Fatalf("graphiteHost after null %q", got)
	}
}
