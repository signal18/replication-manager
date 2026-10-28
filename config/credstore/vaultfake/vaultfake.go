// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package vaultfake is an in-memory Vault for the tests of the credential stores (#2227): the
// approle and jwt sign-ins, a KV v2 mount (data, patch, metadata), and a database engine's
// static roles. Never a real Vault, never real credentials.
package vaultfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Vault is the fake: its state is read and set by the tests.
type Vault struct {
	*httptest.Server
	Mount    string
	RoleID   string
	SecretID string
	JWTRole  string // the role a jwt sign-in must name ("" any)

	mu          sync.Mutex
	kv          map[string][]map[string]interface{} // path -> versions
	maxVersions map[string]int
	static      map[string]string // database engine: user -> password
	rotations   map[string]int
	Fail        bool          // every KV call answers 500
	Delay       time.Duration // every KV call waits first
	Logins      int
	Writes      int
}

// New starts a fake Vault with a KV v2 mount and an approle login.
func New(mount, roleID, secretID string) *Vault {
	v := &Vault{Mount: mount, RoleID: roleID, SecretID: secretID, kv: map[string][]map[string]interface{}{},
		maxVersions: map[string]int{}, static: map[string]string{}, rotations: map[string]int{}}
	v.Server = httptest.NewServer(http.HandlerFunc(v.handle))
	return v
}

// SetStatic sets a database engine static role's password.
func (v *Vault) SetStatic(user, password string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.static[user] = password
}

// Rotations is how many times a static role was rotated.
func (v *Vault) Rotations(user string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rotations[user]
}

// Put stores a KV v2 version at path, as another writer would.
func (v *Vault) Put(path string, data map[string]interface{}) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.kv[path] = append(v.kv[path], data)
}

// Get is the current data at path, nil when none.
func (v *Vault) Get(path string) map[string]interface{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	versions := v.kv[path]
	if len(versions) == 0 {
		return nil
	}
	return versions[len(versions)-1]
}

// Versions is how many versions path holds.
func (v *Vault) Versions(path string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.kv[path])
}

// MaxVersions is the max-versions set on path.
func (v *Vault) MaxVersions(path string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.maxVersions[path]
}

// Paths are the KV paths written.
func (v *Vault) Paths() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for p := range v.kv {
		out = append(out, p)
	}
	return out
}

func reply(w http.ResponseWriter, code int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}

func (v *Vault) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	p := strings.TrimPrefix(r.URL.Path, "/v1/")
	switch {
	case p == "auth/approle/login":
		if body["role_id"] != v.RoleID || body["secret_id"] != v.SecretID {
			reply(w, 400, map[string]interface{}{"errors": []string{"invalid role or secret id"}})
			return
		}
		v.mu.Lock()
		v.Logins++
		v.mu.Unlock()
		reply(w, 200, map[string]interface{}{"auth": map[string]interface{}{"client_token": "approle-token"}})
		return
	case p == "auth/jwt/login":
		if body["jwt"] == "" || v.JWTRole != "" && body["role"] != v.JWTRole {
			reply(w, 400, map[string]interface{}{"errors": []string{"invalid jwt login"}})
			return
		}
		v.mu.Lock()
		v.Logins++
		v.mu.Unlock()
		reply(w, 200, map[string]interface{}{"auth": map[string]interface{}{"client_token": "jwt-token"}})
		return
	case strings.HasPrefix(p, "database/rotate-role/"):
		user := strings.TrimPrefix(p, "database/rotate-role/")
		v.mu.Lock()
		v.rotations[user]++
		v.static[user] = fmt.Sprintf("rotated-%d", v.rotations[user])
		v.mu.Unlock()
		w.WriteHeader(204)
		return
	case strings.HasPrefix(p, "database/static-creds/"):
		user := strings.TrimPrefix(p, "database/static-creds/")
		v.mu.Lock()
		pass, ok := v.static[user]
		v.mu.Unlock()
		if !ok {
			reply(w, 404, map[string]interface{}{"errors": []string{}})
			return
		}
		reply(w, 200, map[string]interface{}{"data": map[string]interface{}{"username": user, "password": pass, "last_vault_rotation": time.Now().UTC().Format(time.RFC3339Nano)}})
		return
	}
	if v.Delay > 0 {
		time.Sleep(v.Delay)
	}
	if v.Fail {
		reply(w, 500, map[string]interface{}{"errors": []string{"vault is failing"}})
		return
	}
	if rest, ok := strings.CutPrefix(p, v.Mount+"/metadata/"); ok {
		if mv, ok := body["max_versions"].(float64); ok {
			v.mu.Lock()
			v.maxVersions[rest] = int(mv)
			v.mu.Unlock()
		}
		w.WriteHeader(204)
		return
	}
	rest, ok := strings.CutPrefix(p, v.Mount+"/data/")
	if !ok {
		reply(w, 404, map[string]interface{}{"errors": []string{}})
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	versions := v.kv[rest]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch r.Method {
	case http.MethodGet:
		if len(versions) == 0 {
			reply(w, 404, map[string]interface{}{"errors": []string{}})
			return
		}
		reply(w, 200, map[string]interface{}{"data": map[string]interface{}{"data": versions[len(versions)-1],
			"metadata": map[string]interface{}{"version": len(versions), "created_time": now}}})
	case http.MethodPatch:
		if len(versions) == 0 {
			reply(w, 404, map[string]interface{}{"errors": []string{}})
			return
		}
		data, _ := body["data"].(map[string]interface{})
		merged := map[string]interface{}{}
		for k, val := range versions[len(versions)-1] {
			merged[k] = val
		}
		for k, val := range data {
			merged[k] = val
		}
		v.kv[rest] = append(versions, merged)
		v.Writes++
		reply(w, 200, map[string]interface{}{"data": map[string]interface{}{"version": len(v.kv[rest]), "created_time": now}})
	default:
		data, _ := body["data"].(map[string]interface{})
		v.kv[rest] = append(versions, data)
		v.Writes++
		reply(w, 200, map[string]interface{}{"data": map[string]interface{}{"version": len(v.kv[rest]), "created_time": now}})
	}
}
