// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestVolumeSnapshotExternalPlugin is the real-cluster regtest of the external snapshot plugins
// (#2456, T13), through repman's own API, with a fake plugin standing for a storage system (no
// real storage needed):
//   - its settings are set through the API with the global-settings grant (refused with
//     cluster-settings only), the secret never returned, and saved encrypted in the cluster's
//     config file;
//   - the plugin is chosen, then a snapshot of a replica is taken, listed, rolled back to (the
//     server stopped, the plugin's snapback, started, left in maintenance) and deleted;
//   - the plugin got its settings and the secret decrypted in its environment at each run.
//
// backup-snapshot-unmount is switched off for the run: the fake plugin restores nothing, and
// the regtest host's file systems are not the servers' own (the unmount and mount steps are
// covered by cluster/srv_volume_snapshot_host_test.go against a fake host).
func (regtest *RegTest) TestVolumeSnapshotExternalPlugin(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST volume-snapshot-external-plugin: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}
	if APIToken == nil || APITokenOf == nil || APIRequestBody == nil {
		return fail("the server hooks are not wired")
	}
	const plugin = "rt-fake-storage"
	const secret = "rt-S3CR3T-value"
	var replica *cluster.ServerMonitor
	for _, s := range cl.GetSlaves() {
		if s != nil && !s.IsMaintenance && s.IsReadOnly() {
			replica = s
			break
		}
	}
	if replica == nil {
		return fail("no read-only replica out of maintenance")
	}

	// the fake plugin: answers the contract, keeps its snapshots in a file, records its
	// environment per action
	dir := filepath.Join(cl.Conf.WorkingDir, "regtest-snapshotplugin")
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail("plugin dir: %s", err)
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, config.ConstSnapshotPluginBinaryPrefix+plugin)
	script := `#!/bin/sh
D="` + dir + `"
env | sort >"$D/env-$1"
touch "$D/snaps"
printf 'TARGET\tfake-volume\n'
case "$1" in
take) printf 'CMD\tfake-storage snapshot %s\n' "$REPMAN_SNAPSHOT_NAME"; printf '%s\t%s\n' "$REPMAN_SNAPSHOT_NAME" "$(date +%s)" >>"$D/snaps"
      printf 'SNAPSHOT\t%s\t%s\t0\n' "$REPMAN_SNAPSHOT_NAME" "$(date +%s)" ;;
list) while IFS="$(printf '\t')" read -r n c; do [ -n "$n" ] && printf 'SNAPSHOT\t%s\t%s\t0\n' "$n" "$c"; done <"$D/snaps" ;;
delete) printf 'CMD\tfake-storage delete %s\n' "$REPMAN_SNAPSHOT_NAME"; grep -v "^$REPMAN_SNAPSHOT_NAME	" "$D/snaps" >"$D/snaps.new"; mv "$D/snaps.new" "$D/snaps" ;;
snapback) grep -q "^$REPMAN_SNAPSHOT_NAME	" "$D/snaps" || { echo "no snapshot $REPMAN_SNAPSHOT_NAME" >&2; exit 1; }
      printf 'CMD\tfake-storage restore %s\n' "$REPMAN_SNAPSHOT_NAME"; printf 'CHANGED\n' ;;
*) echo "unknown action $1" >&2; exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		return fail("plugin: %s", err)
	}
	untested := false
	if err := config.RegisterSnapshotPlugin(config.SnapshotPluginManifest{
		Name: plugin, Description: "regtest fake storage", Tested: &untested,
		Settings: config.PluginSettings{{Name: "url", Type: config.ConstArchiveSettingTypeString}, {Name: "token", Type: config.ConstArchiveSettingTypeSecret}},
	}, bin); err != nil {
		return fail("register: %s", err)
	}

	// put the cluster back as it was, whatever happens
	prevOn, prevPlugin, prevUnmount := cl.Conf.BackupSnapshot, cl.Conf.BackupSnapshotPlugin, cl.Conf.BackupSnapshotUnmount
	defer func() {
		cl.Conf.BackupSnapshot, cl.Conf.BackupSnapshotPlugin, cl.Conf.BackupSnapshotUnmount = prevOn, prevPlugin, prevUnmount
		cl.Conf.PutSnapshotPluginSettings(plugin, nil, []string{"url", "token"})
		if replica.IsMaintenance {
			replica.DelMaintenance()
		}
		cl.Save()
	}()

	admin, err := APIToken(cl, config.GrantGlobalSettings, config.GrantClusterSettings, config.GrantClusterShowBackups, config.GrantDBBackup, config.GrantDBRestore, config.GrantDBMaintenance)
	if err != nil {
		return fail("no API user with the grants: %s", err)
	}
	api := func(token, method, path string, body interface{}) (int, string) {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		code, _, resp, err := APIRequestBody(token, method, "/api/clusters/"+cl.Name+path, payload)
		if err != nil {
			return 0, err.Error()
		}
		return code, strings.TrimSpace(string(resp))
	}

	// a cluster owner: cluster-settings without global-settings, a throwaway user (the first
	// user holding cluster-settings is often the admin, who holds global-settings too)
	const ownerName = "rt-snapshot-owner"
	ownerPass := fmt.Sprintf("rt-%x", time.Now().UnixNano())
	defer api(admin, "POST", "/users/drop", map[string]string{"username": ownerName})
	if code, body := api(admin, "POST", "/users/add", map[string]string{"username": ownerName, "password": ownerPass, "grants": config.GrantClusterSettings, "roles": "visitor"}); code != http.StatusOK {
		return fail("adding %s: %d %s", ownerName, code, body)
	}
	if u, ok := cl.GetACLUser(ownerName); !ok || !u.Grants[config.GrantClusterSettings] || u.Grants[config.GrantGlobalSettings] {
		return fail("%s does not hold cluster-settings without global-settings", ownerName)
	}
	owner, err := APITokenOf(cl, ownerName)
	if err != nil {
		return fail("no token for %s: %s", ownerName, err)
	}

	// 1. the settings: global-settings only, the secret never returned
	set := map[string]interface{}{"settings": map[string]interface{}{"url": "https://fake-storage.example", "token": secret}}
	if code, body := api(owner, "POST", "/volume-snapshot-plugin-actions/set/"+plugin, set); code != http.StatusForbidden {
		return fail("settings set with cluster-settings only: %d %s", code, body)
	}
	if code, body := api(admin, "POST", "/volume-snapshot-plugin-actions/set/"+plugin, set); code != http.StatusOK || strings.Contains(body, secret) || strings.Contains(body, "hash_") {
		return fail("settings set: %d %s", code, body)
	}
	code, body := api(admin, "GET", "/volume-snapshot-plugins", nil)
	if code != http.StatusOK || strings.Contains(body, secret) || strings.Contains(body, "hash_") || !strings.Contains(body, `"secretsSet":["token"]`) || !strings.Contains(body, `"tested":false`) {
		return fail("plugin list: %d %s", code, body)
	}

	// 2. chosen, snapshots on, unmount off for the fake (see above)
	for _, s := range []struct{ path string }{
		{"/settings/actions/set/backup-snapshot-plugin/" + plugin},
		{"/settings/actions/switch/backup-snapshot/on"},
		{"/settings/actions/switch/backup-snapshot-unmount/off"},
	} {
		if code, body := api(admin, "POST", s.path, nil); code != http.StatusOK {
			return fail("%s: %d %s", s.path, code, body)
		}
	}
	if !cl.Conf.BackupSnapshot || cl.Conf.BackupSnapshotPlugin != plugin || cl.Conf.BackupSnapshotUnmount {
		return fail("settings not applied: on %v plugin %s unmount %v", cl.Conf.BackupSnapshot, cl.Conf.BackupSnapshotPlugin, cl.Conf.BackupSnapshotUnmount)
	}

	// 3. saved encrypted in the cluster's config file
	if err := cl.WaitConfigSaved(); err != nil {
		return fail("no save: %s", err)
	}
	saved, err := os.ReadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		return fail("the saved config: %s", err)
	}
	if strings.Contains(string(saved), secret) || !strings.Contains(string(saved), "snapshot-plugin]]") || !strings.Contains(string(saved), "token = \"hash_") {
		return fail("the saved config does not hold the entry with its secret encrypted")
	}

	// wait for the server's last action to end
	waitOp := func(action string) (*cluster.VolumeSnapshotOp, bool) {
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			if op := cl.VolumeSnapshotOp(replica); op != nil && op.Action == action && op.Status != cluster.VolumeSnapshotRunning {
				return op, op.Status == cluster.VolumeSnapshotDone
			}
			time.Sleep(time.Second)
		}
		return nil, false
	}
	envOf := func(action string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "env-"+action))
		return string(b)
	}
	srv := "/servers/" + replica.Id

	// 4. take, with the settings and the secret decrypted in the plugin's environment
	code, body = api(admin, "POST", srv+"/actions/volume-snapshot-take", nil)
	if code != http.StatusAccepted {
		return fail("take: %d %s", code, body)
	}
	var taken map[string]string
	json.Unmarshal([]byte(body), &taken)
	name := taken["snapshot"]
	if op, ok := waitOp(config.ConstSnapshotActionTake); !ok {
		return fail("take did not end well: %+v", op)
	}
	env := envOf("take")
	if !strings.Contains(env, "REPMAN_SNAPSHOT_SETTING_URL=https://fake-storage.example") || !strings.Contains(env, "REPMAN_SNAPSHOT_SECRET_TOKEN="+secret) {
		return fail("the plugin did not get its settings and secret")
	}

	// 5. list
	code, body = api(admin, "GET", srv+"/volume-snapshots", nil)
	if code != http.StatusOK || !strings.Contains(body, name) || strings.Contains(body, secret) {
		return fail("list: %d %s", code, body)
	}

	// 6. snapback: stopped, rolled back by the plugin, started, left in maintenance
	code, body = api(admin, "POST", srv+"/actions/volume-snapback/"+name, nil)
	if code != http.StatusAccepted {
		return fail("snapback: %d %s", code, body)
	}
	if op, ok := waitOp(config.ConstSnapshotActionSnapback); !ok {
		return fail("snapback did not end well: %+v", op)
	}
	if !replica.IsMaintenance {
		return fail("the server is not left in maintenance after the snapback")
	}
	if strings.Contains(envOf("snapback"), "REPMAN_SNAPSHOT_UNMOUNTED") {
		return fail("told unmounted with backup-snapshot-unmount off")
	}
	if err := replica.WaitDatabaseStart(); err != nil {
		return fail("the server did not start after the snapback: %s", err)
	}
	replica.DelMaintenance()

	// 7. delete
	code, body = api(admin, "POST", srv+"/actions/volume-snapshot-delete/"+name, nil)
	if code != http.StatusAccepted && code != http.StatusOK {
		return fail("delete: %d %s", code, body)
	}
	if op, ok := waitOp(config.ConstSnapshotActionDelete); !ok {
		return fail("delete did not end well: %+v", op)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "snaps")); strings.Contains(string(b), name) {
		return fail("snapshot %s still listed after its delete", name)
	}
	logf(config.LvlInfo, "PASS settings %s, take/list/snapback/delete of %s on %s", fmt.Sprint("set, masked, encrypted"), name, replica.URL)
	return true
}
