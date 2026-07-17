// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// schemaAdvisorPluginNames are the external plugins under test. Built via
// `make plugins` into build/plugins/<name>, signed into share/plugins/<name>.sig.
var schemaAdvisorPluginNames = []string{"plugin-schema-row-size", "plugin-schema-lob-compression"}

// TestSchemaPlugin creates the schema conditions the schema-advisory plugins
// are designed to flag:
//   - an InnoDB table with enough short (< 256 byte), always-inline VARCHAR
//     columns to exceed the InnoDB row-size budget (SCH0001);
//   - on MariaDB, an uncompressed BLOB/TEXT column with a large observed
//     average size (SCH0002).
//
// It then deploys the plugin binaries, synchronously refreshes the schema
// snapshot and runs the plugin dispatch (no need to wait on the background
// monitoring ticker), and asserts the expected findings land in
// cluster.SchemaStates.
func (regtest *RegTest) TestSchemaPlugin(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	master := cl.GetMaster()
	if master == nil || master.Conn == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: no master connection")
		return false
	}

	// Feeds the plugins depend on.
	cl.Conf.LogPlugin = true
	cl.Conf.MonitorSchemaChange = true
	cl.Conf.MonitorSchemaColumns = true

	if !deploySchemaAdvisorPlugins(cl) {
		return false
	}

	const testSchema = "test_schema_plugin"
	const wideTable = "wide_row"
	const lobTable = "lob_uncompressed"

	if _, err := master.Conn.Exec("CREATE DATABASE IF NOT EXISTS " + testSchema); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: create database: %s", err)
		return false
	}
	defer master.Conn.Exec("DROP DATABASE IF EXISTS " + testSchema)

	// SCH0001 fixture: 30 VARCHAR(63) utf8mb4 columns. 63*4=252 declared bytes
	// (under the 256-byte inline threshold) + 1-byte length prefix = 253 each.
	// 30*253 = 7590 bytes.
	//
	// This must land in the narrow gap between the plugin's ROW_FORMAT=COMPRESSED
	// budget (page_size/2 - 66, discounted 10% for the unknown KEY_BLOCK_SIZE:
	// ~7313 bytes for a 16K page) and MariaDB's own hard DDL-time rejection
	// (page_size/2 - 66 = 8126 bytes exactly — "Row size too large (> 8126)").
	// A wider fixture (e.g. 40 columns / 10120 bytes) exceeds MariaDB's own hard
	// limit and CREATE TABLE is rejected outright before the plugin ever runs —
	// the plugin's uncompressed budget was deliberately built to match MariaDB's
	// real enforcement, so it can never observe a table wider than that via a
	// fresh CREATE TABLE. ROW_FORMAT=COMPRESSED is the one place the plugin's
	// estimate is intentionally more conservative than MariaDB's own check
	// (KEY_BLOCK_SIZE isn't in the schema snapshot), which is what creates a
	// legitimate window: MariaDB accepts the table, the plugin still flags it.
	cols := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		cols = append(cols, fmt.Sprintf("c%d VARCHAR(63)", i))
	}
	createWide := fmt.Sprintf(
		"CREATE TABLE %s.%s (id INT PRIMARY KEY, %s) ENGINE=InnoDB ROW_FORMAT=COMPRESSED DEFAULT CHARSET=utf8mb4",
		testSchema, wideTable, strings.Join(cols, ", "),
	)
	if _, err := master.Conn.Exec(createWide); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: create wide-row table: %s", err)
		return false
	}

	isMariaDB := master.DBVersion != nil && master.DBVersion.IsMariaDB()
	if isMariaDB {
		// SCH0002 fixture: an uncompressed TEXT column populated with rows
		// large enough that the bounded sample (LIMIT 1024 non-NULL rows)
		// observes an average past the 8192-byte default threshold.
		createLob := fmt.Sprintf(
			"CREATE TABLE %s.%s (id INT PRIMARY KEY, payload TEXT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
			testSchema, lobTable,
		)
		if _, err := master.Conn.Exec(createLob); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: create lob table: %s", err)
			return false
		}

		payload := strings.Repeat("x", 9000)
		insertLob := fmt.Sprintf("INSERT INTO %s.%s (id, payload) VALUES (?, ?)", testSchema, lobTable)
		for i := 0; i < 5; i++ {
			if _, err := master.Conn.Exec(insertLob, i, payload); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: insert lob rows: %s", err)
				return false
			}
		}
	}

	// Synchronous refresh: schema scan (populates DictTables + per-column LOB
	// enrichment), wire snapshot rebuild, then the plugin dispatch itself.
	// Bypasses the async SetWaitMonitorSchema()/MonitorSchema() cooldown gate
	// so the test is deterministic regardless of prior test execution order.
	if err := cl.MonitorMasterTableSchema(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: schema scan: %s", err)
		return false
	}
	cl.RefreshSchemaWireTables()
	cl.CheckLogPlugins()

	foundSCH0001 := false
	foundSCH0002 := false
	for _, s := range cl.SchemaStates {
		switch s.ErrKey {
		case "SCH0001":
			foundSCH0001 = true
		case "SCH0002":
			foundSCH0002 = true
		}
	}

	if !foundSCH0001 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"TEST : SchemaPlugin: expected SCH0001 finding not found in SchemaStates: %v", cl.SchemaStates)
		return false
	}
	if isMariaDB && !foundSCH0002 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"TEST : SchemaPlugin: expected SCH0002 finding not found in SchemaStates (MariaDB): %v", cl.SchemaStates)
		return false
	}

	return true
}

// deploySchemaAdvisorPlugins copies the schema advisor plugin binaries (built
// via `make plugins` into build/plugins/) into the cluster's plugin
// directory and reloads the registry. Signature verification is satisfied by
// the .sig files `make plugins` already wrote to share/plugins/ against the
// same binaries.
func deploySchemaAdvisorPlugins(cl *cluster.Cluster) bool {
	repoRoot := filepath.Dir(cl.GetShareDir()) // share/ and build/ are siblings at repo root
	buildDir := filepath.Join(repoRoot, "build", "plugins")

	pluginDir := filepath.Join(cl.WorkingDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: create plugin dir %s: %s", pluginDir, err)
		return false
	}

	for _, name := range schemaAdvisorPluginNames {
		src := filepath.Join(buildDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"TEST : SchemaPlugin: plugin binary %s not found — run `make plugins` before this test: %s", src, err)
			return false
		}
		if err := os.WriteFile(filepath.Join(pluginDir, name), data, 0755); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : SchemaPlugin: copy %s: %s", name, err)
			return false
		}

		// Manifest sidecar is best-effort: it only affects prerequisite
		// declaration/UI metadata, not plugin execution, and this test
		// enables monitoring-schema-columns directly regardless.
		manifestSrc := filepath.Join(repoRoot, "cluster", "logplugin", "plugins", name, name+".manifest.json")
		if manifestData, err := os.ReadFile(manifestSrc); err == nil {
			os.WriteFile(filepath.Join(pluginDir, name+".manifest.json"), manifestData, 0644)
		}
	}

	cl.ReloadLogPlugins()
	return true
}
