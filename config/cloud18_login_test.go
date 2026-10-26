// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #2182 review (F4): the logins kept for their realm token are bounded: past the cap the least
// recently used goes, an ended login is dropped, a known login is the same entry.
func TestCloud18LoginTokensBounded(t *testing.T) {
	cloud18LoginTokens.Lock()
	cloud18LoginTokens.m = map[string]*cloud18LoginEntry{}
	cloud18LoginTokens.Unlock()
	now := time.Now()
	first := cloud18LoginEntryFor("rt-first", now)
	for i := 0; i < cloud18LoginTokensMax+10; i++ {
		cloud18LoginEntryFor(fmt.Sprintf("rt-%d", i), now.Add(time.Duration(i+1)*time.Millisecond))
	}
	cloud18LoginTokens.Lock()
	n := len(cloud18LoginTokens.m)
	cloud18LoginTokens.Unlock()
	if n != cloud18LoginTokensMax {
		t.Fatalf("cap %d, got %d entries", cloud18LoginTokensMax, n)
	}
	if again := cloud18LoginEntryFor("rt-first", now); again == first {
		t.Error("the least recently used login must have gone")
	}
	last := fmt.Sprintf("rt-%d", cloud18LoginTokensMax+9)
	if cloud18LoginEntryFor(last, now) != cloud18LoginEntryFor(last, now) {
		t.Error("a known login is the same entry")
	}
	ended := cloud18LoginEntryFor("rt-ended", now)
	ended.refreshEnd = now.Add(-time.Second)
	cloud18LoginEntryFor("rt-new", now)
	cloud18LoginTokens.Lock()
	defer cloud18LoginTokens.Unlock()
	for _, e := range cloud18LoginTokens.m {
		if e == ended {
			t.Error("an ended login is dropped")
		}
	}
}

// #2182 review 3: a refresh token the realm refused (invalid_grant) is not sent again before
// cloud18LoginRefusedWait, then it is (never blocked for good); another 400 is not cached.
func TestCloud18LoginRefusedWait(t *testing.T) {
	tries := 0
	answer := `{"error":"invalid_grant","error_description":"Token is not active"}`
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries++
		http.Error(w, answer, http.StatusBadRequest)
	}))
	defer realm.Close()
	old := cloud18LoginRefusedWait
	cloud18LoginRefusedWait = 200 * time.Millisecond
	defer func() { cloud18LoginRefusedWait = old }()
	conf := &Config{Cloud18IdentityURL: realm.URL + "/realms/cloud18"}

	for i := 0; i < 3; i++ {
		if _, err := conf.Cloud18LoginAccess("rt-dead-wait"); err == nil {
			t.Fatal("a refused refresh token gives no token")
		}
	}
	if tries != 1 {
		t.Fatalf("within the wait the realm is asked once, got %d", tries)
	}
	time.Sleep(250 * time.Millisecond)
	conf.Cloud18LoginAccess("rt-dead-wait")
	if tries != 2 {
		t.Fatalf("after the wait the realm is asked again, got %d", tries)
	}

	answer = `{"error":"invalid_request"}`
	conf.Cloud18LoginAccess("rt-other-400")
	conf.Cloud18LoginAccess("rt-other-400")
	if tries != 4 {
		t.Fatalf("a 400 that is not invalid_grant is not cached, got %d", tries)
	}
}
