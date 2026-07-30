// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/signal18/replication-manager/config"
)

const validRestoreSelectorsFilename = "valid_restore_selectors.json"

// ValidatedSelectorSource is coarse, applicability-oriented context about the
// backup a validated selector resolved to when it was tested. It is NOT a
// claim of backup-level restorability/proof — that remains
// RestoreSelector.Proven, a separate, not-yet-built verify mechanism. This is
// only used to help judge whether a saved selector still looks applicable
// when it's reused later.
type ValidatedSelectorSource struct {
	Repo       string `json:"repo,omitempty"` // local | remote
	Tool       string `json:"tool,omitempty"`
	Kind       string `json:"kind,omitempty"` // logical | physical
	PathLayout string `json:"pathLayout,omitempty"`
}

// ValidatedSelectorEntry records one RestoreSelector that was manually
// exercised and actually succeeded on this deployment.
type ValidatedSelectorEntry struct {
	Method   string                  `json:"method"` // logical | physical
	Selector RestoreSelector         `json:"selector"`
	TestedAt time.Time               `json:"testedAt"`
	Result   string                  `json:"result"` // always "success" -- only true success is ever recorded
	Notes    string                  `json:"notes,omitempty"`
	Source   ValidatedSelectorSource `json:"source,omitempty"`
}

// ValidatedSelectorStore is the on-disk shape of valid_restore_selectors.json
// -- a top-level object (not a bare array) so the schema can grow later
// without a breaking format change, matching this repo's existing JSON style.
type ValidatedSelectorStore struct {
	Entries []ValidatedSelectorEntry `json:"entries"`
}

// ValidRestoreSelectorsPath returns the per-cluster path for
// valid_restore_selectors.json under workingDir (cluster.WorkingDir, which
// already includes the cluster name).
func ValidRestoreSelectorsPath(workingDir string) string {
	return filepath.Join(workingDir, validRestoreSelectorsFilename)
}

// loadValidatedSelectors reads valid_restore_selectors.json, mirroring
// loadSecretVersionStore (secret_version_store.go): a missing or empty file
// returns an initialized empty store, no error — this feature must degrade
// safely rather than block a restore on a missing/corrupt file.
func loadValidatedSelectors(path string) (*ValidatedSelectorStore, error) {
	store := &ValidatedSelectorStore{}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, store); err != nil {
		return nil, err
	}
	return store, nil
}

// writeValidatedSelectorsAtomic writes the store through the shared
// temp-file+rename helper (writeFileAtomic, secret_version_store.go) so
// readers never observe a partially written file.
func writeValidatedSelectorsAtomic(path string, store *ValidatedSelectorStore) error {
	if store == nil {
		store = &ValidatedSelectorStore{}
	}
	payload, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, payload, "valid_restore_selectors-*.tmp")
}

// selectorsEqualForDedup reports whether two selectors should be treated as
// the same validated method — compared via their JSON form so future field
// additions stay meaningful without a hand-maintained field-by-field diff.
func selectorsEqualForDedup(a, b RestoreSelector) bool {
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(aj) == string(bj)
}

// recordValidatedSelectorOnSuccess records sel as a validated selector for
// method, deduplicating on (method, selector): a matching existing entry is
// updated in place (fresh TestedAt/Source) rather than appended as a
// duplicate. Callers must only invoke this for a MANUAL restore that has
// just genuinely succeeded — see call sites for the success-gating logic;
// this function does not itself judge success.
func (cluster *Cluster) recordValidatedSelectorOnSuccess(method string, sel RestoreSelector, pick *BackupCatalogEntry) {
	if cluster == nil || cluster.Conf == nil {
		return
	}

	cluster.validatedSelectorsMu.Lock()
	defer cluster.validatedSelectorsMu.Unlock()

	path := ValidRestoreSelectorsPath(cluster.WorkingDir)
	store, err := loadValidatedSelectors(path)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cannot record validated selector, unreadable %s: %v", path, err)
		return
	}

	entry := ValidatedSelectorEntry{
		Method:   method,
		Selector: sel,
		TestedAt: time.Now(),
		Result:   "success",
	}
	if pick != nil {
		repo := "local"
		if !pick.isLocal() {
			repo = "remote"
		}
		entry.Source = ValidatedSelectorSource{
			Repo:       repo,
			Tool:       pick.Tool,
			Kind:       pick.Kind,
			PathLayout: "file",
		}
	}

	updated := false
	for i := range store.Entries {
		if store.Entries[i].Method == method && selectorsEqualForDedup(store.Entries[i].Selector, sel) {
			store.Entries[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		store.Entries = append(store.Entries, entry)
	}

	if err := writeValidatedSelectorsAtomic(path, store); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to persist validated selector to %s: %v", path, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Recorded validated %s restore selector for %s", method, cluster.Name)
}

// newestValidatedSelector returns the most recently tested validated selector
// for method, or nil if none exists or the store can't be read — callers
// must fall back to their own default in that case (the store degrading
// safely to empty means this always returns nil until a first manual success
// is recorded, so behavior is unchanged for any deployment without one).
func (cluster *Cluster) newestValidatedSelector(method string) *RestoreSelector {
	if cluster == nil || cluster.Conf == nil {
		return nil
	}

	cluster.validatedSelectorsMu.RLock()
	defer cluster.validatedSelectorsMu.RUnlock()

	path := ValidRestoreSelectorsPath(cluster.WorkingDir)
	store, err := loadValidatedSelectors(path)
	if err != nil || store == nil || len(store.Entries) == 0 {
		return nil
	}

	var newest *ValidatedSelectorEntry
	for i := range store.Entries {
		e := &store.Entries[i]
		if e.Method != method {
			continue
		}
		if newest == nil || e.TestedAt.After(newest.TestedAt) {
			newest = e
		}
	}
	if newest == nil {
		return nil
	}
	sel := newest.Selector
	return &sel
}

// ListValidatedSelectors returns a copy of every recorded entry — used by the
// read-only list API endpoint.
func (cluster *Cluster) ListValidatedSelectors() ([]ValidatedSelectorEntry, error) {
	cluster.validatedSelectorsMu.RLock()
	defer cluster.validatedSelectorsMu.RUnlock()

	store, err := loadValidatedSelectors(ValidRestoreSelectorsPath(cluster.WorkingDir))
	if err != nil {
		return nil, err
	}
	return store.Entries, nil
}

// AddValidatedSelector appends an operator-curated entry directly (not via a
// restore success) — used by the add API endpoint. TestedAt/Result are
// stamped here so the caller only supplies Method/Selector/Notes/Source.
func (cluster *Cluster) AddValidatedSelector(entry ValidatedSelectorEntry) error {
	cluster.validatedSelectorsMu.Lock()
	defer cluster.validatedSelectorsMu.Unlock()

	path := ValidRestoreSelectorsPath(cluster.WorkingDir)
	store, err := loadValidatedSelectors(path)
	if err != nil {
		return err
	}
	if entry.TestedAt.IsZero() {
		entry.TestedAt = time.Now()
	}
	if entry.Result == "" {
		entry.Result = "success"
	}
	store.Entries = append(store.Entries, entry)
	return writeValidatedSelectorsAtomic(path, store)
}

// RemoveValidatedSelector deletes the entry at index (as returned by
// ListValidatedSelectors) — used by the remove API endpoint.
func (cluster *Cluster) RemoveValidatedSelector(index int) error {
	cluster.validatedSelectorsMu.Lock()
	defer cluster.validatedSelectorsMu.Unlock()

	path := ValidRestoreSelectorsPath(cluster.WorkingDir)
	store, err := loadValidatedSelectors(path)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(store.Entries) {
		return os.ErrNotExist
	}
	store.Entries = append(store.Entries[:index], store.Entries[index+1:]...)
	return writeValidatedSelectorsAtomic(path, store)
}
