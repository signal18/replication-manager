// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	var tries atomic.Int32
	var mu sync.Mutex
	answer := `{"error":"invalid_grant","error_description":"Token is not active"}`
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tries.Add(1)
		mu.Lock()
		a := answer
		mu.Unlock()
		http.Error(w, a, http.StatusBadRequest)
	}))
	defer realm.Close()
	old := cloud18LoginRefusedWait
	cloud18LoginRefusedWait = time.Second
	defer func() { cloud18LoginRefusedWait = old }()
	conf := &Config{Cloud18IdentityURL: realm.URL + "/realms/cloud18"}

	for i := 0; i < 3; i++ {
		if _, err := conf.Cloud18LoginAccess("rt-dead-wait"); err == nil {
			t.Fatal("a refused refresh token gives no token")
		}
	}
	if n := tries.Load(); n != 1 {
		t.Fatalf("within the wait the realm is asked once, got %d", n)
	}
	time.Sleep(1200 * time.Millisecond)
	conf.Cloud18LoginAccess("rt-dead-wait")
	if n := tries.Load(); n != 2 {
		t.Fatalf("after the wait the realm is asked again, got %d", n)
	}

	mu.Lock()
	answer = `{"error":"invalid_request"}`
	mu.Unlock()
	conf.Cloud18LoginAccess("rt-other-400")
	conf.Cloud18LoginAccess("rt-other-400")
	if n := tries.Load(); n != 4 {
		t.Fatalf("a 400 that is not invalid_grant is not cached, got %d", n)
	}
}

// #2182 review 6: the realm's key ids. A failed fetch keeps the known keys and is not a refusal
// (ErrCloud18KeysUnavailable, retried soon); a new key id after a rotation is learnt once the
// refetch interval passed; within it an unknown key id costs no request.
func TestCloud18KeyKnown(t *testing.T) {
	var mu sync.Mutex
	kids, fail, fetches := []string{"k1"}, false, 0
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fetches++
		if fail {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		keys := []string{}
		for _, k := range kids {
			keys = append(keys, fmt.Sprintf(`{"kid":%q}`, k))
		}
		fmt.Fprintf(w, `{"keys":[%s]}`, strings.Join(keys, ","))
	}))
	defer realm.Close()
	oldRefetch, oldRetry := cloud18KeysRefetch, cloud18KeysRetry
	cloud18KeysRefetch, cloud18KeysRetry = 300*time.Millisecond, 100*time.Millisecond
	defer func() { cloud18KeysRefetch, cloud18KeysRetry = oldRefetch, oldRetry }()
	cloud18Keys.Lock()
	cloud18Keys.issuer, cloud18Keys.kids, cloud18Keys.failed, cloud18Keys.fetching = "", nil, false, false
	cloud18Keys.Unlock()
	conf := &Config{}
	issuer := realm.URL + "/realms/cloud18"
	set := func(k []string, f bool) { mu.Lock(); kids, fail = k, f; mu.Unlock() }
	count := func() int { mu.Lock(); defer mu.Unlock(); return fetches }

	if ok, err := conf.cloud18KeyKnown(issuer, "k1"); !ok || err != nil {
		t.Fatalf("k1 published: %v %v", ok, err)
	}
	if ok, err := conf.cloud18KeyKnown(issuer, "forged"); ok || err != nil || count() != 1 {
		t.Fatalf("an unknown key id within the interval: no request, a refusal (%v %v, %d fetches)", ok, err, count())
	}

	time.Sleep(350 * time.Millisecond)
	set([]string{"k1"}, true)
	if _, err := conf.cloud18KeyKnown(issuer, "k2"); !errors.Is(err, ErrCloud18KeysUnavailable) {
		t.Fatalf("a failed fetch is not a refusal: %v", err)
	}
	if ok, err := conf.cloud18KeyKnown(issuer, "k1"); !ok || err != nil {
		t.Fatalf("a failed fetch keeps the known keys: %v %v", ok, err)
	}

	time.Sleep(150 * time.Millisecond)
	set([]string{"k1", "k2"}, false)
	if ok, err := conf.cloud18KeyKnown(issuer, "k2"); !ok || err != nil {
		t.Fatalf("after the retry delay, the rotated key is learnt: %v %v", ok, err)
	}
}
