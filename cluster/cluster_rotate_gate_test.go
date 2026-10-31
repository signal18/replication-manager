// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// rotateGateCluster: a cluster where a rotation outside Vault is allowed, every case
// below breaks one condition
func rotateGateCluster() *Cluster {
	return &Cluster{
		Conf: &config.Config{
			SecretKey:                  []byte("0123456789abcdef"),
			ConfRewrite:                true,
			MonitoringSecretVersioning: true,
			ImmuableFlagMap:            map[string]interface{}{},
			Topology:                   config.TopoUnknown,
		},
		Servers: serverList{&ServerMonitor{State: stateMaster, Conn: &sqlx.DB{}}},
	}
}

// #2042, #2269: outside Vault, a rotation needs a durable place for the new passwords: a store
// keeping versions, or else the cluster config (saved, encrypted). A credential set in a config
// file (immutable) is refused when no store keeps versions, and the reason is given.
func TestCanRotatePasswords(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Cluster)
		want   string // "" = allowed, else a part of the refusal
	}{
		{"allowed", func(*Cluster) {}, ""},
		// #2269: no store keeps versions: the config is the store
		{"versioning off", func(c *Cluster) { c.Conf.MonitoringSecretVersioning = false }, ""},
		{"versioning on through backup encryption", func(c *Cluster) { c.Conf.MonitoringSecretVersioning = false; c.Conf.BackupEncryption = true }, ""},
		{"vault, versioning off", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.VaultServerAddr = "https://vault:8200"
		}, ""},
		// #2227: an active store keeps versions: the file with versioning on, or a vault entry
		{"versioning off, a vault entry enabled", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
		}, ""},
		{"versioning off, the vault entry off", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault}}
		}, ""},
		{"vault entry enabled, db credential in a file", func(c *Cluster) {
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
			c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x"
		}, ""},
		{"replication stopped, a database engine entry", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{"mode": config.VaultDbEngine}}}
		}, ""},
		{"no encryption key", func(c *Cluster) { c.Conf.SecretKey = nil }, "no encryption key"},
		{"config not saved", func(c *Cluster) { c.Conf.ConfRewrite = false }, "monitoring-save-config is off"},
		// #2227 rule 9: a credential set in a config file is a starting value, rotated as
		// versions; #2269: refused when no store keeps versions
		{"db credential in a file", func(c *Cluster) { c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x" }, ""},
		{"replication credential in a file", func(c *Cluster) { c.Conf.ImmuableFlagMap["replication-credential"] = "repl:x" }, ""},
		{"db credential in a file, no versioned store", func(c *Cluster) {
			c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x"
			c.Conf.MonitoringSecretVersioning = false
		}, "immutable"},
		{"a database down", func(c *Cluster) { c.Servers = append(c.Servers, &ServerMonitor{State: stateFailed}) }, "databases are down"},
		{"replication stopped on a replica", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}, Replications: []dbhelper.SlaveStatus{{
				SlaveIORunning:  sql.NullString{String: "Yes", Valid: true},
				SlaveSQLRunning: sql.NullString{String: "No", Valid: true},
			}}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
		}, "replication is not running"},
		{"replication stopped, vault database engine", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.VaultServerAddr = "https://vault:8200"
			c.Conf.VaultMode = VaultDbEngine
		}, ""}, // Vault rotates through its own engine, replication is not touched
		{"replication stopped, vault kv", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.VaultServerAddr = "https://vault:8200"
			c.Conf.VaultMode = VaultConfigStoreV2
		}, "replication is not running"}, // repman changes the replicas' passwords itself
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := rotateGateCluster()
			tc.change(cl)
			err := cl.CanRotatePasswords()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

// RotatePasswords itself refuses (MCP or any other caller), and changes nothing
func TestRotatePasswordsRefusedChangesNothing(t *testing.T) {
	cl := rotateGateCluster()
	cl.Conf.ImmuableFlagMap["db-servers-credential"] = "root:before"
	cl.Conf.MonitoringSecretVersioning = false
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:before"}})
	if err := cl.RotatePasswords(); err == nil {
		t.Fatal("rotation ran on a credential set in a config file")
	}
	if v := cl.Conf.Secrets.Get("db-servers-credential").Value; v != "root:before" {
		t.Fatalf("credential changed to %q", v)
	}
}

// #2269: a rotation waits for a config save, so a second one (the next cron, the button) is
// refused while one runs, and the running one is left alone
func TestRotatePasswordsRefusedWhileOneRuns(t *testing.T) {
	cl := rotateGateCluster()
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:before"}})
	cl.inPasswordRotation.Store(true)
	if err := cl.CanRotatePasswords(); !errors.Is(err, errPasswordRotationRunning) {
		t.Fatalf("CanRotatePasswords while one runs: %v", err)
	}
	if err := cl.RotatePasswords(); !errors.Is(err, errPasswordRotationRunning) {
		t.Fatalf("RotatePasswords while one runs: %v", err)
	}
	if !cl.inPasswordRotation.Load() {
		t.Fatal("a refused rotation must not release the running one")
	}
	if v := cl.Conf.Secrets.Get("db-servers-credential").Value; v != "root:before" {
		t.Fatalf("credential changed to %q", v)
	}
}

// #2269: a scheduled run the cluster refuses is skipped: WARN0274 in the security domain at each
// tick, until the schedule is switched off (or a scheduled rotation succeeds)
func TestScheduledPasswordRotationSkipped(t *testing.T) {
	cl := rotateGateCluster()
	cl.Status = ConstMonitorActif
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	cl.Conf.SchedulerRotatePasswords = true
	cl.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x"
	cl.Conf.MonitoringSecretVersioning = false

	cl.CheckScheduledPasswordRotation()
	if hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("a state before any scheduled run")
	}
	cl.GetRotatePasswordsFunction()()
	cl.CheckScheduledPasswordRotation()
	if !hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatalf("a skipped run must open WARN0274: %v", cl.SecurityStateMachine.GetStates())
	}
	if !strings.Contains(cl.scheduledRotationSkipped, "immutable") {
		t.Fatalf("reason %q", cl.scheduledRotationSkipped)
	}

	cl.SwitchSchedulerRotatePasswords() // off
	cl.SecurityStateMachine.ClearState()
	cl.CheckScheduledPasswordRotation()
	if hasState(cl.SecurityStateMachine, "WARN0274") || cl.scheduledRotationSkipped != "" {
		t.Fatal("switched off, the state must close")
	}

	// a standby runs nothing
	cl.Conf.SchedulerRotatePasswords = true
	cl.Status = ConstMonitorStandby
	cl.GetRotatePasswordsFunction()()
	if cl.scheduledRotationSkipped != "" {
		t.Fatal("a standby ran the scheduled rotation")
	}
}

// #2269: a cron that cannot be scheduled never stops the rotation silently: WARN0274 opens
// while the schedule is on, and closes once a valid cron is set
func TestScheduledPasswordRotationInvalidCron(t *testing.T) {
	cl := rotateGateCluster()
	cl.Status = ConstMonitorActif
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	cl.scheduler = cron.New()
	cl.Schedule = map[string]cron.Entry{}
	cl.Conf.SchedulerRotatePasswords = true
	cl.Conf.SchedulerRotatePasswordsCron = "0 0 3 * * 0"

	// the setter (API, GUI) refuses it and keeps the cron as it was
	if err := cl.SetSchedulerRotatePasswordsCron("not a cron"); err == nil {
		t.Fatal("an invalid cron was accepted")
	}
	if cl.Conf.SchedulerRotatePasswordsCron != "0 0 3 * * 0" {
		t.Fatalf("a refused cron must keep the former one, got %q", cl.Conf.SchedulerRotatePasswordsCron)
	}
	// a config file that holds one (read at start): not scheduled, the state says why
	cl.Conf.SchedulerRotatePasswordsCron = "not a cron"
	if err := cl.SetSchedulerRotatePasswords(); err == nil {
		t.Fatal("an invalid cron was scheduled")
	}
	cl.CheckScheduledPasswordRotation()
	if !hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("an invalid cron must open WARN0274")
	}
	if err := cl.SetSchedulerRotatePasswordsCron("0 0 3 * * 0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cl.Schedule["rotatepasswords"]; !ok {
		t.Fatal("the valid cron is not scheduled")
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckScheduledPasswordRotation()
	if hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("a valid cron must close the state")
	}
}
