// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
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
