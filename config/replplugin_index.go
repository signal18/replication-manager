// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"strings"
	"sync"
)

// ReplicationPluginIndex is one index of an analytic child's denormalize mode (the CDC core,
// doc/implementation/cluster/CDC_CORE.md "Denormalize"): a denormalizing query, run on the
// source, and the denormalized table it fills on the child. Nothing per source table: the
// plugin derives the query's tables and their primary keys from the query itself.
type ReplicationPluginIndex struct {
	Name        string `mapstructure:"name" toml:"name" json:"name"`
	SQL         string `mapstructure:"sql" toml:"sql" json:"sql"`
	SlaveSchema string `mapstructure:"slave-schema" toml:"slave-schema" json:"slaveSchema"`
	SlaveTable  string `mapstructure:"slave-table" toml:"slave-table" json:"slaveTable"`
	// MaxKeysPerBatch: the packed keys one unpack restricts the query to; 0: the plugin's default
	MaxKeysPerBatch int `mapstructure:"max-keys-per-batch" toml:"max-keys-per-batch" json:"maxKeysPerBatch"`
}

// replPluginIndexMu guards the index list: the save (config-sync goroutine) and the API setters.
var replPluginIndexMu sync.Mutex

// ValidateReplicationPluginIndex checks an index before it is stored.
func ValidateReplicationPluginIndex(idx ReplicationPluginIndex) error {
	if err := validateEntryName("index", idx.Name); err != nil {
		return err
	}
	if strings.TrimSpace(idx.SQL) == "" {
		return fmt.Errorf("index %s: the denormalizing query is empty", idx.Name)
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(idx.SQL)), "SELECT") {
		return fmt.Errorf("index %s: the denormalizing query is a SELECT", idx.Name)
	}
	for k, v := range map[string]string{"slave-schema": idx.SlaveSchema, "slave-table": idx.SlaveTable} {
		if v == "" || len(v) > 64 || strings.ContainsAny(v, "`./") {
			return fmt.Errorf("index %s: %s %q: a MariaDB name, without ` . or /", idx.Name, k, v)
		}
	}
	if idx.MaxKeysPerBatch < 0 {
		return fmt.Errorf("index %s: max-keys-per-batch is 0 (the default) or more", idx.Name)
	}
	return nil
}

// ReplicationPluginIndexList is a copy of the index list.
func (conf *Config) ReplicationPluginIndexList() []ReplicationPluginIndex {
	replPluginIndexMu.Lock()
	defer replPluginIndexMu.Unlock()
	return append([]ReplicationPluginIndex(nil), conf.ReplicationPluginIndexes...)
}

// PutReplicationPluginIndex adds idx, or replaces the index of its name; refused when another
// index fills the same denormalized table.
func (conf *Config) PutReplicationPluginIndex(idx ReplicationPluginIndex) error {
	idx.SQL = strings.TrimRight(strings.TrimSpace(idx.SQL), ";")
	if err := ValidateReplicationPluginIndex(idx); err != nil {
		return err
	}
	replPluginIndexMu.Lock()
	defer replPluginIndexMu.Unlock()
	next := make([]ReplicationPluginIndex, 0, len(conf.ReplicationPluginIndexes)+1)
	found := false
	for _, e := range conf.ReplicationPluginIndexes {
		if e.Name == idx.Name {
			next, found = append(next, idx), true
			continue
		}
		if strings.EqualFold(e.SlaveSchema, idx.SlaveSchema) && strings.EqualFold(e.SlaveTable, idx.SlaveTable) {
			return fmt.Errorf("index %s already fills %s.%s", e.Name, e.SlaveSchema, e.SlaveTable)
		}
		next = append(next, e)
	}
	if !found {
		next = append(next, idx)
	}
	conf.ReplicationPluginIndexes = next
	return nil
}

// DeleteReplicationPluginIndex removes the index name; false when there is none. Its
// denormalized table stays on the child (F1: the child's data is dropped by hand only).
func (conf *Config) DeleteReplicationPluginIndex(name string) bool {
	replPluginIndexMu.Lock()
	defer replPluginIndexMu.Unlock()
	next := make([]ReplicationPluginIndex, 0, len(conf.ReplicationPluginIndexes))
	for _, e := range conf.ReplicationPluginIndexes {
		if e.Name != name {
			next = append(next, e)
		}
	}
	found := len(next) != len(conf.ReplicationPluginIndexes)
	conf.ReplicationPluginIndexes = next
	return found
}

// ReplicationPluginIndexesToml renders the list as [[<section>.replication-plugin-index]]
// tables for the saved config.
func (conf *Config) ReplicationPluginIndexesToml(section string) (string, error) {
	replPluginIndexMu.Lock()
	defer replPluginIndexMu.Unlock()
	var b strings.Builder
	for _, e := range conf.ReplicationPluginIndexes {
		part := map[string]interface{}{"name": e.Name, "sql": e.SQL, "slave-schema": e.SlaveSchema, "slave-table": e.SlaveTable}
		if e.MaxKeysPerBatch > 0 {
			part["max-keys-per-batch"] = int64(e.MaxKeysPerBatch)
		}
		if err := writeEntryToml(&b, section, "replication-plugin-index", part); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}
