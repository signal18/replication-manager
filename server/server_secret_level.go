// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// The server level of the credential stores (CREDENTIAL_STORES.md "Two levels"): every secret
// whose setting is tagged scope:"server" is the instance's. It has one version store and one
// local file for the instance, <datadir>/secret_store.json, written by the server's save
// callback (T25) and pushed with default.toml; every cluster reads it from the server's chain
// (credstore.ServerLevel), never keeps its own copy.

// serverSecretKeys are the secrets tagged scope:"server".
var serverSecretKeys = func() map[string]bool {
	out := map[string]bool{}
	server := config.GetParamsByScope("server")
	for _, key := range config.SecretKeys {
		if server[key] {
			out[key] = true
		}
	}
	return out
}()

// isServerSecretKey: key is a server secret.
func isServerSecretKey(key string) bool { return serverSecretKeys[key] }

// errNoServerVersionedStore: no store keeps the server secrets' versions (versioning off and
// no server entry): the server config is their store (#2269).
var errNoServerVersionedStore = errors.New("no store keeps the server secrets' versions (monitoring-secret-versioning off)")

// serverSecrets is the server level's version store and its file.
type serverSecrets struct {
	mu       sync.Mutex
	versions *credstore.VersionStore
	file     *credstore.FileBinding
}

func (repman *ReplicationManager) serverVersionsKeepError() error {
	if repman.Conf != nil && repman.Conf.IsMonitoringSecretVersioningEnabled() {
		return nil
	}
	return errNoServerVersionedStore
}

// serverVersionStore is the server secrets' version store, its file loaded at the first use
// when versioning is on.
func (repman *ReplicationManager) serverVersionStore() *credstore.VersionStore {
	s := &repman.serverSecrets
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions == nil {
		s.versions = credstore.NewVersionStore(func() *config.Config { return repman.Conf }, isServerSecretKey, repman.serverVersionsKeepError, repman.Save)
		s.file = &credstore.FileBinding{DataDir: func() string { return repman.Conf.WorkingDir }, Scope: credstore.ServerScope()}
		if repman.Conf != nil && repman.Conf.IsMonitoringSecretVersioningEnabled() {
			if err := s.file.LoadInto(s.versions); err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Server credential versions: %s not read: %s", s.file.Path(), err)
			}
		}
	}
	return s.versions
}

// serverSecretLevel is the store a cluster's chain reads the server secrets from.
func (repman *ReplicationManager) serverSecretLevel() *credstore.ServerLevel {
	return &credstore.ServerLevel{Chain: repman.CredentialChain, Held: isServerSecretKey}
}

// writeServerSecretFile is the save callback's part for the server's local file: written when
// a version changed, encrypted only (F9). Nothing when versioning is off.
func (repman *ReplicationManager) writeServerSecretFile() error {
	if !repman.Conf.IsMonitoringSecretVersioningEnabled() {
		return nil
	}
	vs := repman.serverVersionStore()
	if err := repman.serverSecrets.file.Save(vs, credstore.LooksEncrypted, nil); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Failed to persist the server secret versions %s: %v", repman.serverSecrets.file.Path(), err)
		return err
	}
	return nil
}

// reloadServerVersionsFromPeer: on a standby, the server's file the config pull brought is
// merged, and every cluster reads the newest server secrets (rule 11).
func (repman *ReplicationManager) reloadServerVersionsFromPeer() {
	if repman.Status == ConstMonitorActif || !repman.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := repman.serverVersionStore()
	merged, err := repman.serverSecrets.file.MergeIfChanged(vs)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Server credential versions: %s not read: %s", repman.serverSecrets.file.Path(), err)
		return
	}
	if merged {
		repman.applyServerSecrets()
	}
}

// takeServerConfigFileCredentials is the start's check of the server secrets set in a config
// file (immutable, rule 9): an edit of the file is the current version.
func (repman *ReplicationManager) takeServerConfigFileCredentials() {
	if !repman.Conf.IsMonitoringSecretVersioningEnabled() {
		return
	}
	vs := repman.serverVersionStore()
	for key := range serverSecretKeys {
		raw, pinned := repman.Conf.GetImmuableFlag(key)
		if !pinned || raw == nil {
			continue
		}
		v := fmt.Sprintf("%v", raw)
		if v == "" || repman.Conf.IsPath(v) {
			continue
		}
		if vs.TakeConfigFileValue(key, repman.Conf.DecryptSecretValue(key, v)) {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModVault, config.LvlInfo, "Server credentials: %s was edited in a config file, its value is the current version", key)
		}
	}
	repman.CredentialChain().Refresh()
}

// SetServerSecret writes a server secret through the server's chain (a new version where a
// store keeps versions) and gives it to every cluster: the one write point of a server
// secret.
func (repman *ReplicationManager) SetServerSecret(key, value string) error {
	if !isServerSecretKey(key) {
		return fmt.Errorf("%s is not a server secret", key)
	}
	if err := repman.CredentialChain().Set(key, value); err != nil {
		return err
	}
	repman.applyServerSecrets()
	return nil
}

// applyServerSecrets has every cluster read the server secrets again (its chain asks the
// server's), and reload what depends on them: the API users.
func (repman *ReplicationManager) applyServerSecrets() {
	for _, cl := range repman.clustersSnapshot() {
		cl.CredentialChain().Refresh()
		cl.LoadAPIUsers()
	}
}
