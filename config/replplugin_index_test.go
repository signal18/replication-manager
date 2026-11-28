// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml"
)

// The index list: put, replace by name, one denormalized table per index, rendered for the
// saved config and read back.
func TestReplicationPluginIndexes(t *testing.T) {
	conf := &Config{}
	ol := ReplicationPluginIndex{Name: "order-lines", SQL: "SELECT ol.ol_o_id FROM tpcc.order_line ol;", SlaveSchema: "dn", SlaveTable: "ol"}
	if err := conf.PutReplicationPluginIndex(ol); err != nil {
		t.Fatal(err)
	}
	ol.MaxKeysPerBatch = 500
	if err := conf.PutReplicationPluginIndex(ol); err != nil || len(conf.ReplicationPluginIndexes) != 1 || conf.ReplicationPluginIndexes[0].MaxKeysPerBatch != 500 {
		t.Fatalf("replace: %v %+v", err, conf.ReplicationPluginIndexes)
	}
	if err := conf.PutReplicationPluginIndex(ReplicationPluginIndex{Name: "other", SQL: "SELECT 1", SlaveSchema: "dn", SlaveTable: "OL"}); err == nil {
		t.Fatal("two indexes cannot fill one table")
	}
	for _, bad := range []ReplicationPluginIndex{
		{Name: "Bad Name", SQL: "SELECT 1", SlaveSchema: "a", SlaveTable: "b"},
		{Name: "x", SQL: "DELETE FROM t", SlaveSchema: "a", SlaveTable: "b"},
		{Name: "x", SQL: "SELECT 1", SlaveSchema: "a.b", SlaveTable: "b"},
	} {
		if err := conf.PutReplicationPluginIndex(bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
	out, err := conf.ReplicationPluginIndexesToml("saved-c1")
	if err != nil || !strings.Contains(out, "[[saved-c1.replication-plugin-index]]") || strings.Contains(out, ";") {
		t.Fatalf("%v\n%s", err, out)
	}
	tree, err := toml.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		C struct {
			Indexes []ReplicationPluginIndex `toml:"replication-plugin-index"`
		} `toml:"saved-c1"`
	}
	if err := tree.Unmarshal(&back); err != nil || len(back.C.Indexes) != 1 || back.C.Indexes[0].SlaveTable != "ol" || back.C.Indexes[0].MaxKeysPerBatch != 500 {
		t.Fatalf("%v %+v", err, back)
	}
	if !conf.DeleteReplicationPluginIndex("order-lines") || conf.DeleteReplicationPluginIndex("order-lines") {
		t.Fatal("delete")
	}
}

// The table mode: tables copied into another schema, partitioned, from a SELECT or a backup;
// rendered for the saved config and read back whole.
func TestReplicationPluginIndexTableMode(t *testing.T) {
	conf := &Config{}
	logs := ReplicationPluginIndex{Name: "logs", Mode: "table", Tables: []string{"logs.access", "logs.audit"}, SlaveSchema: "logs_p", Init: "backup",
		PartitionColumn: "created_at", PartitionPeriod: "day", PartitionsAhead: 5, RangeStep: 20000, TargetEngine: "InnoDB"}
	if err := conf.PutReplicationPluginIndex(logs); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ReplicationPluginIndex{
		{Name: "x", Mode: "table", SlaveSchema: "p"},
		{Name: "x", Mode: "table", Tables: []string{"logs.access"}, SlaveSchema: "logs"},
		{Name: "x", Mode: "table", Tables: []string{"access"}, SlaveSchema: "p"},
		{Name: "x", Mode: "table", Tables: []string{"a.t"}, SlaveSchema: "p", SQL: "SELECT 1"},
		{Name: "x", Mode: "table", Tables: []string{"a.t"}, SlaveSchema: "p", Init: "dump"},
		{Name: "x", Mode: "table", Tables: []string{"a.t"}, SlaveSchema: "p", PartitionColumn: "ts"},
		{Name: "x", Mode: "table", Tables: []string{"a.t"}, SlaveSchema: "p", PartitionColumn: "ts", PartitionPeriod: "decade"},
		{Name: "x", SQL: "SELECT 1", SlaveSchema: "p", SlaveTable: "t", Init: "backup"},
		{Name: "x", Mode: "copy", Tables: []string{"a.t"}, SlaveSchema: "p"},
	} {
		if err := conf.PutReplicationPluginIndex(bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
	out, err := conf.ReplicationPluginIndexesToml("saved-c1")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := toml.Load(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var back struct {
		C struct {
			Indexes []ReplicationPluginIndex `toml:"replication-plugin-index"`
		} `toml:"saved-c1"`
	}
	if err := tree.Unmarshal(&back); err != nil || len(back.C.Indexes) != 1 {
		t.Fatalf("%v %+v", err, back)
	}
	b := back.C.Indexes[0]
	if !b.TableMode() || strings.Join(b.Tables, ",") != "logs.access,logs.audit" || b.Init != "backup" || b.PartitionColumn != "created_at" || b.PartitionPeriod != "day" || b.PartitionsAhead != 5 || b.RangeStep != 20000 || b.TargetEngine != "InnoDB" {
		t.Fatalf("%+v\n%s", b, out)
	}
}

// The partition periods: a time period, or one of six id ranges; the purge on a time period only.
func TestReplicationPluginIndexPartitionPeriods(t *testing.T) {
	base := ReplicationPluginIndex{Name: "x", Mode: "table", Tables: []string{"a.t"}, SlaveSchema: "p", PartitionColumn: "c"}
	for _, ok := range []string{"day", "week", "month", "year", "1M", "2M", "5m", "10M", "20M", "50M"} {
		i := base
		i.PartitionPeriod = ok
		if err := ValidateReplicationPluginIndex(i); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"decade", "3M", "100M", "0M", "M", "1G"} {
		i := base
		i.PartitionPeriod = bad
		if err := ValidateReplicationPluginIndex(i); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
	i := base
	i.PartitionPeriod, i.PartitionPurgeAfterDays = "month", 400
	if err := ValidateReplicationPluginIndex(i); err != nil {
		t.Fatal(err)
	}
	i.PartitionPeriod = "10M"
	if err := ValidateReplicationPluginIndex(i); err == nil {
		t.Fatal("a purge on an id range must be refused")
	}
	i.PartitionPeriod, i.PartitionPurgeAfterDays = "day", -1
	if err := ValidateReplicationPluginIndex(i); err == nil {
		t.Fatal("a negative purge must be refused")
	}
}
