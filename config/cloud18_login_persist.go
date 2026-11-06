// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/misc"
	log "github.com/sirupsen/logrus"
)

// The live Cloud18 logins kept across a restart (#2341): the login cache (cloud18LoginTokens) is
// written to cloud18LoginsFileName in the data directory, its refresh tokens encrypted with the
// instance's key (F9), and read back at start. A session the restart left unverifiable comes back
// only through the realm (the server's resume): this file only lets it find the newest refresh token
// of its login, which a realm rotating them needs.
//
// - Session data, not config: outside the files pushed to the config repo (it changes at every
//   refresh, T17), written by this cache only (T25).
// - Bounded (F4): the cache's own bound and pruning, ended logins never written or read back.
// - Writes batched (cloud18LoginsSaveDelay) and durable (misc.WriteFileDurable).
// - Off with cloud18-login-resume = false: nothing written, the file removed, nothing read back.

const (
	cloud18LoginsFileName = "cloud18-logins.json"
	cloud18LoginsTemp     = ".cloud18-logins.json-*.tmp"
)

// cloud18LoginsSaveDelay batches the writes of the login cache: one write for the changes of a
// few seconds (a variable: tests lower it).
var cloud18LoginsSaveDelay = 5 * time.Second

// cloud18LoginsStore is where the cache is kept: set by LoadCloud18Logins, empty (nothing kept)
// until then and when cloud18-login-resume is off.
// cloud18LoginsWrite serializes the writes (the batch, a rotation, a flush): one writer at a time,
// each with a fresh snapshot.
var cloud18LoginsWrite sync.Mutex

var cloud18LoginsStore = struct {
	sync.Mutex
	conf    *Config
	path    string
	pending bool
}{}

// cloud18LoginRecord is one login in the file: the cache key (the hash of the login's first
// refresh token, never the token itself), the newest refresh token, encrypted, and its end.
type cloud18LoginRecord struct {
	Key        string    `json:"key"`
	Refresh    string    `json:"refresh"`
	RefreshEnd time.Time `json:"refresh_end"`
	Seen       time.Time `json:"seen"`
	SessionEnd time.Time `json:"session_end,omitempty"`
}

type cloud18LoginsFile struct {
	Version int                  `json:"version"`
	Logins  []cloud18LoginRecord `json:"logins"`
}

func (conf *Config) cloud18LoginsPath() string {
	return filepath.Join(conf.WorkingDir, cloud18LoginsFileName)
}

// LoadCloud18Logins reads the kept logins back into the cache at start and keeps the cache in the
// file from then on. With cloud18-login-resume off it removes the file instead: no session data
// left behind on disk. It returns how many logins came back.
func (conf *Config) LoadCloud18Logins() (int, error) {
	path := conf.cloud18LoginsPath()
	if !conf.Cloud18LoginResume {
		cloud18LoginsStore.Lock()
		cloud18LoginsStore.conf, cloud18LoginsStore.path = nil, ""
		cloud18LoginsStore.Unlock()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		return 0, nil
	}
	cloud18LoginsStore.Lock()
	cloud18LoginsStore.conf, cloud18LoginsStore.path = conf, path
	cloud18LoginsStore.Unlock()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f cloud18LoginsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	now := time.Now()
	// the most recently used first: past the bound, the oldest stay out
	sort.Slice(f.Logins, func(i, j int) bool { return f.Logins[i].Seen.After(f.Logins[j].Seen) })
	n := 0
	cloud18LoginTokens.Lock()
	for _, l := range f.Logins {
		if n >= cloud18LoginTokensMax {
			break
		}
		if l.Key == "" || (!l.RefreshEnd.IsZero() && !now.Before(l.RefreshEnd)) {
			continue // ended
		}
		if _, ok := cloud18LoginTokens.m[l.Key]; ok {
			continue // already in use in this process
		}
		refresh, err := conf.DecryptCloud18LoginClaim(l.Refresh)
		if err != nil {
			continue // another key: this login cannot come back
		}
		cloud18LoginTokens.m[l.Key] = &cloud18LoginEntry{refresh: refresh, refreshEnd: l.RefreshEnd, seen: l.Seen, sessionEnd: l.SessionEnd}
		n++
	}
	cloud18LoginTokens.Unlock()
	return n, nil
}

// cloud18LoginsChanged says the cache changed: it is written within cloud18LoginsSaveDelay, once
// for all the changes until then. Nothing when the logins are not kept.
func cloud18LoginsChanged() {
	cloud18LoginsStore.Lock()
	defer cloud18LoginsStore.Unlock()
	if cloud18LoginsStore.path == "" || cloud18LoginsStore.pending {
		return
	}
	cloud18LoginsStore.pending = true
	time.AfterFunc(cloud18LoginsSaveDelay, saveCloud18Logins)
}

// cloud18LoginRotated says the realm rotated a login's refresh token: the old one is revoked, the
// new one is the only way back for that session, so it is written now, not after the batch delay
// (a hard stop runs no shutdown flush). Called once the login's lock is released; in its own
// goroutine, so the request that refreshed does not wait on the disk.
func cloud18LoginRotated() {
	cloud18LoginsStore.Lock()
	kept := cloud18LoginsStore.path != ""
	cloud18LoginsStore.Unlock()
	if kept {
		go saveCloud18Logins()
	}
}

// saveCloud18Logins writes the cache. A login being refreshed (its lock held across the call to
// the realm) is written as it was, or at the next write: the writer never waits on the realm.
func saveCloud18Logins() {
	cloud18LoginsWrite.Lock()
	defer cloud18LoginsWrite.Unlock()
	cloud18LoginsStore.Lock()
	conf, path := cloud18LoginsStore.conf, cloud18LoginsStore.path
	cloud18LoginsStore.pending = false
	cloud18LoginsStore.Unlock()
	if path == "" {
		return
	}
	key := conf.cloud18EncryptionKey()
	if key == nil {
		return // never in clear: no key, nothing kept
	}
	// the file as written last time: a login being refreshed (its lock busy) keeps its last record.
	// One read per write, bounded by the cache's own bound (cloud18LoginTokensMax records).
	previous := map[string]cloud18LoginRecord{}
	if data, err := os.ReadFile(path); err == nil {
		var f cloud18LoginsFile
		if json.Unmarshal(data, &f) == nil {
			for _, l := range f.Logins {
				previous[l.Key] = l
			}
		}
	}

	now := time.Now()
	type snap struct {
		key        string
		e          *cloud18LoginEntry
		sessionEnd time.Time
	}
	cloud18LoginTokens.Lock()
	entries := make([]snap, 0, len(cloud18LoginTokens.m))
	for k, e := range cloud18LoginTokens.m {
		entries = append(entries, snap{k, e, e.sessionEnd})
	}
	cloud18LoginTokens.Unlock()

	f := cloud18LoginsFile{Version: 1, Logins: make([]cloud18LoginRecord, 0, len(entries))}
	busy := false
	for _, s := range entries {
		if !s.e.mu.TryLock() {
			busy = true
			if l, ok := previous[s.key]; ok {
				f.Logins = append(f.Logins, l)
			}
			continue
		}
		refresh, end, seen, refused := s.e.refresh, s.e.refreshEnd, s.e.seen, s.e.refusedErr != nil && now.Before(s.e.refused)
		s.e.mu.Unlock()
		if refresh == "" || refused || (!end.IsZero() && !now.Before(end)) {
			continue // ended or refused by the realm: not kept
		}
		p := crypto.Password{Key: key, PlainText: refresh}
		p.Encrypt()
		if p.CipherText == "" {
			continue
		}
		f.Logins = append(f.Logins, cloud18LoginRecord{Key: s.key, Refresh: "hash_" + p.CipherText, RefreshEnd: end, Seen: seen, SessionEnd: s.sessionEnd})
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err == nil {
		err = misc.WriteFileDurable(path, data, 0o600, cloud18LoginsTemp)
	}
	if err != nil && conf.IsEligibleForPrinting(ConstLogModGeneral, LvlWarn) {
		log.Warnf("Cloud18 logins not kept for the next start: %s", err)
	}
	if busy {
		cloud18LoginsChanged() // the login being refreshed is written at the next pass
	}
}

// DecryptCloud18LoginClaim gives the refresh token of a session's Cloud18 login: only a claim this
// instance encrypted (hash_, its key) decrypts, a plain token or another instance's is refused.
//
// The cipher (crypto.Password, AES-CFB) is not authenticated: a wrong key gives bytes, not an
// error. A refresh token is printable ASCII, so anything else is refused here and never sent to
// the realm (which would refuse it anyway: the realm is what makes a login valid).
func (conf *Config) DecryptCloud18LoginClaim(claim string) (string, error) {
	refresh, err := conf.cloud18Decrypt(claim)
	if err != nil {
		return "", err
	}
	if refresh == "" {
		return "", errors.New("empty Cloud18 login")
	}
	for i := 0; i < len(refresh); i++ {
		if refresh[i] < 0x21 || refresh[i] > 0x7e {
			return "", errors.New("not a Cloud18 login of this instance (another key)")
		}
	}
	return refresh, nil
}

// RecordCloud18SessionEnd records that this instance issued a session of the login (its first
// refresh token) ending at end (#2341). A resume after a restart takes its end from here, never from
// the old session (read without its signature, its end could be forged).
func RecordCloud18SessionEnd(refresh string, end time.Time) {
	if refresh == "" {
		return
	}
	e := cloud18LoginEntryFor(refresh, time.Now())
	cloud18LoginTokens.Lock()
	if end.After(e.sessionEnd) {
		e.sessionEnd = end
	}
	cloud18LoginTokens.Unlock()
	cloud18LoginsChanged()
}

// Cloud18SessionEnd is the latest end of a session this instance issued for the login, false when
// it issued none (or no longer knows: logged out, evicted, another instance). It never creates an
// entry and never asks the realm: a login no session of this instance carried is refused for free.
func Cloud18SessionEnd(refresh string) (time.Time, bool) {
	cloud18LoginTokens.Lock()
	defer cloud18LoginTokens.Unlock()
	e, ok := cloud18LoginTokens.m[cloud18LoginKey(refresh)]
	if !ok || e.sessionEnd.IsZero() {
		return time.Time{}, false
	}
	return e.sessionEnd, true
}

// FlushCloud18Logins writes the cache now, without waiting for the batch (tests, shutdown).
func FlushCloud18Logins() {
	saveCloud18Logins()
}

// StopKeepingCloud18Logins stops writing the cache (tests, shutdown).
func StopKeepingCloud18Logins() {
	cloud18LoginsStore.Lock()
	cloud18LoginsStore.conf, cloud18LoginsStore.path, cloud18LoginsStore.pending = nil, "", false
	cloud18LoginsStore.Unlock()
}
