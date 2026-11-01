// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// #2278: an empty or malformed option name must be rejected, because "loose_" + "" =
// "loose_=<value>" is a fatal ambiguous option that stops mariadbd from starting.
func TestIsValidConfigVarName(t *testing.T) {
	cases := map[string]bool{
		"":                        false,
		"loose_":                  false,
		"loose-":                  false,
		"=true":                   false,
		";set @s=1":               false,
		"loose_;set global x=1":   false,
		"a b":                     false, // space
		"x\ty":                    false, // tab
		"a\nb":                    false, // newline
		"loose_loose_":            true,  // strips one prefix -> "loose_", non-empty valid chars
		"optimizer_switch":        true,
		"binlog_format":           true,
		"innodb_buffer_pool_size": true,
		"loose_binlog_format":     true,
		"loose-gtid-mode":         true,
		"gtid-strict-mode":        true,
		"wsrep.sync.wait":         true, // dotted (plugin-qualified)
	}
	for name, want := range cases {
		if got := IsValidConfigVarName(name); got != want {
			t.Errorf("IsValidConfigVarName(%q) = %v, want %v", name, got, want)
		}
	}
}

// Root-cause: a "loose_=true" line read from a config must not create an empty-name
// entry in the variables map (which would be written back as the fatal bare loose_=).
func TestLoadFromConfigFileDropsEmptyName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "01_preserved.cnf")
	content := "[mysqld]\n" +
		"loose_=true\n" + // the poisoned line
		"binlog_format=ROW\n" +
		"optimizer_switch=index_merge=on\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewVariablesMap()
	if err := m.LoadFromConfigFile(path, "preserved"); err != nil {
		t.Fatalf("load: %v", err)
	}
	all := m.ToNewMap()
	if _, bad := all[""]; bad {
		t.Fatal("empty-name variable was stored (the #2278 poison)")
	}
	if _, ok := all["binlog_format"]; !ok {
		t.Fatal("legitimate binlog_format was dropped")
	}
}
