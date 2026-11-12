// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/utils/state"
)

// The server level's credential stores (CREDENTIAL_STORES.md "Two levels"): the vault entries
// of [[default.server-secret-kv]] get every version of every server secret, under
// <path-prefix>/system/repman/<instance name>/<key>. Set by the instance's sysops and extsysops
// only, never copied to a cluster, so no cluster's sponsor or member reaches them. The same
// queue and worker as a cluster's (credstore.StoreRunner).

// serverSecretKv is the server level's queue, its worker's flags and what the states come from.
type serverSecretKv struct {
	once    sync.Once
	queue   *credstore.StoreQueue
	running atomic.Bool
	passDue atomic.Bool

	mu          sync.Mutex
	behind      map[string]string    // entry -> why it is behind (never a value)
	lastWritten map[string]time.Time // entry -> its last successful write
}

// errServerKvNotNamed: the instance's name is not complete, its folder in a shared store is
// unknown, nothing is written.
const errServerKvNotNamed = "the instance's name is not complete (company, hostname, location): nothing is written"

func (repman *ReplicationManager) serverKvQueue() *credstore.StoreQueue {
	k := &repman.serverSecretKv
	k.once.Do(func() {
		k.queue = credstore.NewStoreQueue(func() int { return repman.Conf.SecretKvQueueLimit })
		k.behind = map[string]string{}
		k.lastWritten = map[string]time.Time{}
	})
	return k.queue
}

// serverKvRunner is the server level's store worker.
func (repman *ReplicationManager) serverKvRunner() *credstore.StoreRunner {
	k := &repman.serverSecretKv
	return &credstore.StoreRunner{
		Queue: repman.serverKvQueue(),
		Writer: func(name string) credstore.StoreWriter {
			if e, ok := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, name); ok {
				return credstore.NewVaultKV(credstore.VaultSettingsFromEntry(repman.Conf, e), nil)
			}
			return nil
		},
		Timeout: func() credstore.Timeout {
			return credstore.Timeout(time.Duration(repman.Conf.SecretKvWriteTimeout) * time.Second)
		},
		Retry:    func() time.Duration { return time.Duration(repman.Conf.SecretKvRetryInterval) * time.Second },
		Stopped:  repman.exit.Load,
		FullPass: repman.serverKvFullPass,
		Behind:   repman.setServerKvBehind,
		Written: func(name string) {
			k.mu.Lock()
			k.lastWritten[name] = time.Now()
			k.mu.Unlock()
		},
		Running: &k.running,
		Due:     &k.passDue,
	}
}

// setServerKvBehind records why entry is behind, "" when it is not (never a value, F10).
func (repman *ReplicationManager) setServerKvBehind(entry, why string) {
	repman.serverKvQueue()
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	if why == "" {
		delete(k.behind, entry)
		return
	}
	if i := strings.IndexByte(why, '\n'); i >= 0 {
		why = why[:i]
	}
	k.behind[entry] = why
}

// serverKvItems are the items writing ver of key to every active server entry ("" every one,
// else entry only). None, each entry behind, while the instance is not named.
func (repman *ReplicationManager) serverKvItems(entry, key string, ver credstore.Version, compare bool) []credstore.StoreItem {
	active := repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer)
	if len(active) == 0 || ver.Value == "" {
		return nil
	}
	name, named := repman.Conf.InstanceStoreName()
	var items []credstore.StoreItem
	for _, e := range active {
		if entry != "" && e.Name != entry {
			continue
		}
		if !named {
			repman.setServerKvBehind(e.Name, errServerKvNotNamed)
			continue
		}
		items = append(items, credstore.StoreItem{Entry: e.Name, Path: repman.Conf.ServerSecretKvFolder(e, name), Key: key, Version: ver, Compare: compare})
	}
	return items
}

// queueServerVersion hands the current version of server secret key to every server entry.
// Only the active monitor writes: a standby takes the server's file from the config pull.
func (repman *ReplicationManager) queueServerVersion(key string) {
	if repman.Status != ConstMonitorActif || !isServerSecretKey(key) {
		return
	}
	items := repman.serverKvItems("", key, repman.serverSecretVersion(key), false)
	if len(items) == 0 {
		return
	}
	repman.serverKvQueue().Push(items...)
	repman.serverKvRunner().Start()
}

// serverSecretVersion is this instance's current version of server secret key: the version
// store's, else the chain's value now (versioning off: the config is the store).
func (repman *ReplicationManager) serverSecretVersion(key string) credstore.Version {
	if v, ok := repman.serverVersionStore().Latest(key); ok {
		return v
	}
	return credstore.Version{Value: repman.Conf.GetDecryptedValue(key), At: time.Now().UTC(), By: repman.Conf.ArbitrationSasUniqueId}
}

// onServerCredentialChange is the server chain's hook, outside its lock: each server secret
// changed goes to every server entry.
func (repman *ReplicationManager) onServerCredentialChange(changes []credstore.Change) {
	for _, ch := range changes {
		repman.queueServerVersion(ch.Key)
	}
}

// serverKvFullPass writes the current version of every server secret to every active server
// entry, where it differs (compare): at start, for an entry turned on, after a failure, after
// the versions an older release kept were taken over.
func (repman *ReplicationManager) serverKvFullPass() {
	if repman.Status != ConstMonitorActif {
		return
	}
	keys := make([]string, 0, len(serverSecretKeys))
	for key := range serverSecretKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items []credstore.StoreItem
	for _, key := range keys {
		items = append(items, repman.serverKvItems("", key, repman.serverSecretVersion(key), true)...)
	}
	if len(items) > 0 {
		repman.serverKvQueue().Push(items...)
	}
}

// startServerKvPass asks a full pass of the server entries (start, an entry added or turned
// on, versions taken over) and starts the worker. Never blocks.
func (repman *ReplicationManager) startServerKvPass() {
	if repman.Status != ConstMonitorActif || len(repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer)) == 0 {
		return
	}
	repman.serverKvQueue()
	repman.serverSecretKv.passDue.Store(true)
	repman.serverKvRunner().Start()
}

// ServerKvBehind is why each server entry is behind, for the states of every level.
func (repman *ReplicationManager) ServerKvBehind() map[string]string {
	repman.serverKvQueue()
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	active := map[string]bool{}
	for _, e := range repman.Conf.SecretKvActiveAt(config.SecretKvLevelServer) {
		active[e.Name] = true
	}
	out := map[string]string{}
	for name, why := range k.behind {
		if active[name] {
			out[name] = why
		} else {
			// turned off or removed: its versions stopped, it is not behind
			delete(k.behind, name)
		}
	}
	return out
}

// checkServerSecretKvStores runs at each server tick: a store behind is a state of the server
// level (GWARN024); every cluster raises its own (WARN0320) from ServerKvBehind.
func (repman *ReplicationManager) checkServerSecretKvStores() {
	if repman.Status != ConstMonitorActif {
		return
	}
	if q := repman.serverKvQueue(); q.Len() > 0 || repman.serverSecretKv.passDue.Load() {
		repman.serverKvRunner().Start()
	}
	for name, why := range repman.ServerKvBehind() {
		repman.SetState("GWARN024@"+name, state.State{ErrType: "WARNING", ErrKey: "GWARN024", ErrFrom: "REPMAN",
			ErrDesc: fmt.Sprintf(config.GlobalError["GWARN024"], name, why)})
	}
}

// ServerSecretKvState is a server entry's state for the API and the GUI: never a value.
type ServerSecretKvState struct {
	LastWrite time.Time `json:"lastWrite,omitempty"`
	Behind    string    `json:"behind,omitempty"`
	// Path is the entry's folder for this instance, empty while the instance is not named
	Path string `json:"path,omitempty"`
}

// ServerSecretKvListResult is the server entries for the API: never a secret.
type ServerSecretKvListResult struct {
	Entries []config.SecretKvConfig         `json:"entries"`
	States  map[string]ServerSecretKvState  `json:"states"`
	Plugins []config.SecretKvPluginManifest `json:"plugins"`
	// Pinned: a config file sets the list, the API does not change it
	Pinned bool `json:"pinned"`
	// InstanceName is the folder's name, empty while not complete
	InstanceName string   `json:"instanceName"`
	Keys         []string `json:"keys"`
}

// serverSecretKvList is the server entries and their states.
func (repman *ReplicationManager) serverSecretKvList() ServerSecretKvListResult {
	repman.serverKvQueue()
	name, named := repman.Conf.InstanceStoreName()
	res := ServerSecretKvListResult{
		Entries: repman.Conf.SecretKvListAt(config.SecretKvLevelServer),
		States:  map[string]ServerSecretKvState{},
		Plugins: config.SecretKvPlugins(),
		Pinned:  repman.serverSecretKvsPinned(),
	}
	if named {
		res.InstanceName = name
	}
	for key := range serverSecretKeys {
		res.Keys = append(res.Keys, key)
	}
	sort.Strings(res.Keys)
	behind := repman.ServerKvBehind()
	k := &repman.serverSecretKv
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, e := range res.Entries {
		st := ServerSecretKvState{LastWrite: k.lastWritten[e.Name], Behind: behind[e.Name]}
		if named {
			st.Path = repman.Conf.ServerSecretKvFolder(e, name)
		}
		res.States[e.Name] = st
	}
	return res
}

// serverSecretKvsPinned: a config file sets the server entries, the API does not change them.
func (repman *ReplicationManager) serverSecretKvsPinned() bool {
	_, pinned := repman.Conf.GetImmuableFlag("server-secret-kv")
	return pinned
}
