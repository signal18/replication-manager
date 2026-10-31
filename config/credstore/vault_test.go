// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
)

func kvSettings(v *vaultfake.Vault) VaultSettings {
	return VaultSettings{Address: v.URL, Mount: "kv", Mode: config.VaultConfigStoreV2, Auth: config.ConstVaultAuthAppRole, RoleID: "role", SecretID: "sid", MaxVersions: 5}
}

// #2227: approle signs in with the role and secret id; jwt with the identity token, the role
// being the realm's client id when no role id is set
func TestVaultSignIn(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	if _, err := NewVaultKV(kvSettings(v), nil).Connect(context.Background()); err != nil {
		t.Fatalf("approle: %s", err)
	}
	bad := kvSettings(v)
	bad.SecretID = "wrong"
	if _, err := NewVaultKV(bad, nil).Connect(context.Background()); err == nil {
		t.Fatal("approle with a wrong secret id accepted")
	}
	v.JWTRole = config.ConstCloud18IdentityClientID
	jwt := VaultSettings{Address: v.URL, Mount: "kv", Mode: config.VaultConfigStoreV2, Auth: config.ConstVaultAuthJWT, JWT: func() (string, error) { return "identity-token", nil }}
	client, err := NewVaultKV(jwt, nil).Connect(context.Background())
	if err != nil || client.Token() != "jwt-token" {
		t.Fatalf("jwt: %v", err)
	}
	jwt.JWT = func() (string, error) { return "", errors.New("no identity") }
	if _, err := NewVaultKV(jwt, nil).Connect(context.Background()); err == nil {
		t.Fatal("jwt without an identity accepted")
	}
}

// #2227: a version is a whole secret at <path>/<key> with written-at and written-by, max-versions
// set at its first write; a compare item is written only when the store holds another value;
// the version read back carries its time and writer
func TestVaultWriteVersions(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	kv := NewVaultKV(kvSettings(v), nil)
	at := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	it := StoreItem{Entry: "e", Path: "pair-x/c1/databases/db1", Key: "db-servers-credential", Version: Version{Value: "u:p1", At: at, By: 7}}
	if failed, err := kv.WriteAll(context.Background(), []StoreItem{it}, Timeout(time.Second)); err != nil || len(failed) != 0 {
		t.Fatalf("write: %v %v", failed, err)
	}
	path := "pair-x/c1/databases/db1/db-servers-credential"
	d := v.Get(path)
	if d["db-servers-credential"] != "u:p1" || d["written-by"] != "7" || v.MaxVersions(path) != 5 {
		t.Fatalf("stored %v max %d", d, v.MaxVersions(path))
	}
	it.Compare = true
	kv.WriteAll(context.Background(), []StoreItem{it}, Timeout(time.Second))
	if v.Versions(path) != 1 {
		t.Fatalf("same value written again: %d versions", v.Versions(path))
	}
	it.Version.Value = "u:p2"
	kv.WriteAll(context.Background(), []StoreItem{it}, Timeout(time.Second))
	if v.Versions(path) != 2 {
		t.Fatalf("a different value not written: %d versions", v.Versions(path))
	}
	client, _ := kv.Connect(context.Background())
	ver, found, err := kv.ReadVersion(context.Background(), client, path, "db-servers-credential")
	if err != nil || !found || ver.Value != "u:p2" || !ver.At.Equal(at) || ver.By != 7 {
		t.Fatalf("read %+v %v %v", ver, found, err)
	}
	if _, found, err := kv.ReadVersion(context.Background(), client, "nothing/here", "k"); found || err != nil {
		t.Fatalf("absent: %v %v", found, err)
	}
}

// #2227: the newest version wins, the instance id breaks a tie
func TestVersionNewer(t *testing.T) {
	t0 := time.Now()
	if !(Version{At: t0.Add(time.Second), By: 1}).Newer(Version{At: t0, By: 9}) {
		t.Fatal("a later version must win")
	}
	if !(Version{At: t0, By: 2}).Newer(Version{At: t0, By: 1}) || (Version{At: t0, By: 1}).Newer(Version{At: t0, By: 2}) {
		t.Fatal("the instance id breaks a tie")
	}
}

// #2227: a failing or slow store returns its items, within the timeout
func TestVaultWriteFailsWithinTimeout(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	v.Delay = 2 * time.Second
	kv := NewVaultKV(kvSettings(v), nil)
	items := []StoreItem{{Entry: "e", Path: "s/c1/databases/db1", Key: "k", Version: Version{Value: "x"}}}
	start := time.Now()
	failed, err := kv.WriteAll(context.Background(), items, Timeout(200*time.Millisecond))
	if err == nil || len(failed) != 1 || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("failed %v err %v after %s", failed, err, time.Since(start))
	}
}

// #2227: the database engine makes the passwords: rotate-role, static creds as user:password,
// no KV write; a seed path is read as user:password
func TestVaultDatabaseEngine(t *testing.T) {
	v := vaultfake.New("kv", "role", "sid")
	defer v.Close()
	v.SetStatic("repman", "first")
	s := kvSettings(v)
	s.Mode = config.VaultDbEngine
	kv := NewVaultKV(s, nil)
	if !kv.Generates() || kv.Versioned() {
		t.Fatal("engine capabilities")
	}
	client, err := kv.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if value, _, err := kv.StaticCreds(context.Background(), client, "repman"); err != nil || value != "repman:first" {
		t.Fatalf("static %q %v", value, err)
	}
	if err := kv.RotateRole(context.Background(), client, "repman"); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := kv.StaticCreds(context.Background(), client, "repman"); value != "repman:rotated-1" {
		t.Fatalf("after rotation %q", value)
	}
	if value, err := kv.Read(context.Background(), client, "database/static-creds/repman", "db-servers-credential"); err != nil || value != "repman:rotated-1" {
		t.Fatalf("seed read %q %v", value, err)
	}
	if failed, err := kv.WriteAll(context.Background(), []StoreItem{{Entry: "e", Path: "p", Key: "k", Version: Version{Value: "x"}}}, Timeout(time.Second)); err != nil || failed != nil || len(v.Paths()) != 0 {
		t.Fatalf("engine took a write: %v %v %v", failed, err, v.Paths())
	}
}

// #2227: the queue keeps the latest version of each secret, at most its limit, dropping the
// oldest and counting the drops; a requeued failure never replaces a newer version
func TestStoreQueueBoundAndDrop(t *testing.T) {
	limit := 3
	q := NewStoreQueue(func() int { return limit })
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		q.Push(StoreItem{Entry: "e", Path: "p", Key: fmt.Sprintf("k%d", i), Version: Version{Value: "v", At: t0}})
	}
	if q.Len() != 3 || q.Dropped() != 2 {
		t.Fatalf("len %d dropped %d", q.Len(), q.Dropped())
	}
	q.Push(StoreItem{Entry: "e", Path: "p", Key: "k4", Version: Version{Value: "newer", At: t0.Add(time.Second)}})
	q.Requeue([]StoreItem{{Entry: "e", Path: "p", Key: "k4", Version: Version{Value: "older", At: t0}}})
	items := q.Take()
	if len(items) != 3 || items[0].Key != "k2" || items[2].Key != "k4" || items[2].Version.Value != "newer" {
		t.Fatalf("items %+v", items)
	}
	if q.Len() != 0 {
		t.Fatal("take left items")
	}
	limit = 0 // unset: the default bound, never unbounded
	for i := 0; i < DefaultStoreQueueLimit+10; i++ {
		q.Push(StoreItem{Entry: "e", Path: "p", Key: fmt.Sprintf("x%d", i)})
	}
	if q.Len() != DefaultStoreQueueLimit || q.Dropped() != 12 {
		t.Fatalf("default bound: len %d dropped %d", q.Len(), q.Dropped())
	}
}

// #2227: the chain tells its hook the changes of a successful SetAll, after its lock, and a
// hook that only queues never delays it
func TestChainOnSetHook(t *testing.T) {
	conf := testConf()
	chain := NewConfigChain(conf, nil)
	var got []Change
	chain.OnSet(func(changes []Change) {
		// the chain's lock is released: reading it here must not deadlock
		chain.Source("db-servers-credential")
		got = changes
	})
	if err := chain.Set("db-servers-credential", "u:new"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != "u:new" {
		t.Fatalf("hook got %+v", got)
	}
}
