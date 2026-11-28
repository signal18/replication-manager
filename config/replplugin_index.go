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
	// Mode: "index" (or empty), the query SQL; "table", the tables of Tables copied as they are
	// into SlaveSchema (SlaveTable renames a single table)
	Mode   string   `mapstructure:"mode" toml:"mode" json:"mode,omitempty"`
	Tables []string `mapstructure:"tables" toml:"tables" json:"tables,omitempty"`
	// Init: "select" (or empty), a SELECT on a consistent snapshot of the source, range by
	// range; "backup" (table mode), the parent's latest split dump
	Init      string `mapstructure:"init" toml:"init" json:"init,omitempty"`
	RangeStep int    `mapstructure:"range-step" toml:"range-step" json:"rangeStep,omitempty"`
	// the destination's partitioning, when it needs one: a DATE, DATETIME or TIMESTAMP column of
	// the result, day, week or month, the partitions kept ahead (0: 3)
	PartitionColumn string `mapstructure:"partition-column" toml:"partition-column" json:"partitionColumn,omitempty"`
	PartitionPeriod string `mapstructure:"partition-period" toml:"partition-period" json:"partitionPeriod,omitempty"`
	PartitionsAhead int    `mapstructure:"partitions-ahead" toml:"partitions-ahead" json:"partitionsAhead,omitempty"`
	// TargetEngine: the destination table's engine, as the destination plugin takes them
	// (InnoDB for repl-mariadb-mariadb-cdc); empty, the plugin's default
	TargetEngine string `mapstructure:"target-engine" toml:"target-engine" json:"targetEngine,omitempty"`
}

// TableMode: the index copies tables as they are.
func (idx ReplicationPluginIndex) TableMode() bool { return strings.EqualFold(idx.Mode, "table") }

// replPluginIndexMu guards the index list: the save (config-sync goroutine) and the API setters.
var replPluginIndexMu sync.Mutex

// ValidateReplicationPluginIndex checks an index before it is stored.
func ValidateReplicationPluginIndex(idx ReplicationPluginIndex) error {
	if err := validateEntryName("index", idx.Name); err != nil {
		return err
	}
	switch strings.ToLower(idx.Mode) {
	case "", "index":
		if strings.TrimSpace(idx.SQL) == "" {
			return fmt.Errorf("index %s: the denormalizing query is empty", idx.Name)
		}
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(idx.SQL)), "SELECT") {
			return fmt.Errorf("index %s: the denormalizing query is a SELECT", idx.Name)
		}
		if idx.SlaveTable == "" {
			return fmt.Errorf("index %s: slave-table is required", idx.Name)
		}
		if !strings.EqualFold(idx.Init, "") && !strings.EqualFold(idx.Init, "select") {
			return fmt.Errorf("index %s: an index of a query inits by select", idx.Name)
		}
	case "table":
		if strings.TrimSpace(idx.SQL) != "" {
			return fmt.Errorf("index %s: the table mode copies tables, it takes no query", idx.Name)
		}
		if len(idx.Tables) == 0 {
			return fmt.Errorf("index %s: the table mode needs its tables (schema.table)", idx.Name)
		}
		for _, t := range idx.Tables {
			s, n, ok := strings.Cut(strings.TrimSpace(t), ".")
			if !ok || s == "" || n == "" || strings.ContainsAny(t, "`/*") || strings.Contains(n, ".") {
				return fmt.Errorf("index %s: table %q: schema.table", idx.Name, t)
			}
			if strings.EqualFold(s, idx.SlaveSchema) && (idx.SlaveTable == "" || strings.EqualFold(n, idx.SlaveTable)) {
				return fmt.Errorf("index %s: %s would be copied onto itself: another slave-schema", idx.Name, t)
			}
		}
		if idx.SlaveTable != "" && len(idx.Tables) > 1 {
			return fmt.Errorf("index %s: slave-table renames one table, %d are listed", idx.Name, len(idx.Tables))
		}
		switch strings.ToLower(idx.Init) {
		case "", "select", "backup":
		default:
			return fmt.Errorf("index %s: init %q: select or backup", idx.Name, idx.Init)
		}
	default:
		return fmt.Errorf("index %s: mode %q: index or table", idx.Name, idx.Mode)
	}
	for k, v := range map[string]string{"slave-schema": idx.SlaveSchema, "slave-table": idx.SlaveTable} {
		if (v == "" && k == "slave-schema") || len(v) > 64 || strings.ContainsAny(v, "`./") {
			return fmt.Errorf("index %s: %s %q: a MariaDB name, without ` . or /", idx.Name, k, v)
		}
	}
	switch strings.ToLower(idx.PartitionPeriod) {
	case "", "day", "week", "month":
	default:
		return fmt.Errorf("index %s: partition-period %q: day, week or month", idx.Name, idx.PartitionPeriod)
	}
	if (idx.PartitionColumn == "") != (idx.PartitionPeriod == "") {
		return fmt.Errorf("index %s: partition-column and partition-period go together", idx.Name)
	}
	if idx.PartitionColumn != "" && (len(idx.PartitionColumn) > 64 || strings.ContainsAny(idx.PartitionColumn, "`./")) {
		return fmt.Errorf("index %s: partition-column %q: a column name", idx.Name, idx.PartitionColumn)
	}
	if idx.RangeStep < 0 || idx.PartitionsAhead < 0 || idx.PartitionsAhead > 366 {
		return fmt.Errorf("index %s: range-step is 0 or more, partitions-ahead from 0 to 366", idx.Name)
	}
	if strings.ContainsAny(idx.TargetEngine, "`./ ;") || len(idx.TargetEngine) > 64 {
		return fmt.Errorf("index %s: target-engine %q: an engine name", idx.Name, idx.TargetEngine)
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
		if !e.TableMode() && !idx.TableMode() && strings.EqualFold(e.SlaveSchema, idx.SlaveSchema) && strings.EqualFold(e.SlaveTable, idx.SlaveTable) {
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
		part := map[string]interface{}{"name": e.Name, "slave-schema": e.SlaveSchema}
		for k, v := range map[string]string{"sql": e.SQL, "slave-table": e.SlaveTable, "mode": e.Mode, "init": e.Init, "partition-column": e.PartitionColumn, "partition-period": e.PartitionPeriod, "target-engine": e.TargetEngine} {
			if v != "" {
				part[k] = v
			}
		}
		for k, v := range map[string]int{"max-keys-per-batch": e.MaxKeysPerBatch, "range-step": e.RangeStep, "partitions-ahead": e.PartitionsAhead} {
			if v > 0 {
				part[k] = int64(v)
			}
		}
		if len(e.Tables) > 0 {
			part["tables"] = append([]string(nil), e.Tables...)
		}
		if err := writeEntryToml(&b, section, "replication-plugin-index", part); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}
