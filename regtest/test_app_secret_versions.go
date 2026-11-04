// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// TestAppSecretVersions is the real-cluster regtest of the app secret versions (#2306, #2227
// rules 2, 6 and 10, T13), through repman's own API: a monitor-only app added to the cluster gets a secret
// variable, a per-agent value of it and a new value, then the variable and the app are
// dropped. After each save the local versioned file holds one more version per changed secret
// under apps/<app>/<key>, encrypted whole; an unchanged value adds none; a dropped variable or
// app keeps its versions. It needs monitoring-secret-versioning on.
func (regtest *RegTest) TestAppSecretVersions(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return regtest.appSecretVersions(cl, false)
}

// TestAppSecretVersionsVault is TestAppSecretVersions with a vault entry enabled
// ([[<cluster>.secret-kv]] on a Vault or OpenBao dev container the test environment starts):
// the same steps, and every enabled KV entry holds each value at
// <path-prefix>/<cluster>/apps/<app>/<key>.
func (regtest *RegTest) TestAppSecretVersionsVault(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return regtest.appSecretVersions(cl, true)
}

const (
	rtAppSecretHost  = "rt-app-secret-versions"
	rtAppSecretPort  = "8080"
	rtAppSecretVar   = "RT_SECRET"
	rtAppSecretAgent = "rt-agent"
)

func (regtest *RegTest) appSecretVersions(cl *cluster.Cluster, withVault bool) bool {
	name := "app-secret-versions"
	if withVault {
		name += "-vault"
	}
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST "+name+": "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}
	if APIToken == nil || APIRequest == nil || APIRequestBody == nil {
		return fail("the server hooks are not wired")
	}
	if !cl.Conf.IsMonitoringSecretVersioningEnabled() {
		return fail("monitoring-secret-versioning is off: the local file keeps no version")
	}
	var vaults []config.SecretKvConfig
	for _, e := range cl.Conf.SecretKvActive() {
		if e.Settings[config.ConstSecretKvVaultMode] != config.VaultDbEngine {
			vaults = append(vaults, e)
		}
	}
	if withVault && len(vaults) == 0 {
		return fail("no vault entry (secret-kv) enabled in KV mode")
	}
	token, err := APIToken(cl, config.GrantClusterCreateMonitor, config.GrantAppDeployment)
	if err != nil {
		return fail("no API user with the app grants: %s", err)
	}
	api := func(method, path string, body interface{}) (int, string) {
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
	findApp := func() *cluster.App {
		for _, app := range cl.GetAppsCopy() {
			if app != nil && app.AppConfig != nil && app.AppConfig.AppHost == rtAppSecretHost {
				return app
			}
		}
		return nil
	}
	removeApp := func() error {
		if app := findApp(); app != nil {
			return cl.RemoveAppMonitor(app.GetHost(), app.GetPort())
		}
		return nil
	}
	// an app left by an earlier run that stopped half-way
	if err := removeApp(); err != nil {
		return fail("removing the app of an earlier run: %s", err)
	}

	storePath := cluster.SecretVersionStorePath(cl.Conf.WorkingDir, cl.Name)
	// latest is the newest version of an app secret in the local file: its number (0 when the
	// key has none yet) and its value decrypted; whole is false when the stored value is not
	// one ciphertext token
	latest := func(key string) (int, string, bool) {
		entries, err := cluster.ResolveSecretVersionStoreEntries(storePath, []string{"apps/" + rtAppSecretHost + "/" + key}, cluster.SecretVersionLatest, nil)
		if err != nil || len(entries) == 0 {
			return 0, "", true
		}
		stored := entries[0].HashValue
		whole := strings.HasPrefix(stored, "hash_") && !strings.ContainsAny(stored, ":,")
		return entries[0].Version, cl.Conf.GetDecryptedPassword(key, stored), whole
	}
	saved := func(step string) bool {
		if err := cl.WaitConfigSaved(); err != nil {
			return fail("%s: no save completed: %s", step, err)
		}
		return true
	}
	// expect checks the local file holds version n of key with value; with a vault, every
	// enabled KV entry holds the value (written by the store queue after the save)
	expect := func(step, key string, n int, value string) bool {
		v, got, whole := latest(key)
		if v != n || got != value {
			return fail("%s: the local file holds version %d of %s with another value, want version %d", step, v, key, n)
		}
		if !whole {
			return fail("%s: version %d of %s is not stored as one ciphertext", step, v, key)
		}
		for _, e := range vaults {
			deadline := time.Now().Add(60 * time.Second)
			for {
				held, found, err := cl.SecretKvReadObject(e, credstore.KindApps, rtAppSecretHost, key)
				if err == nil && found && held == value {
					break
				}
				if time.Now().After(deadline) {
					return fail("%s: vault entry %s does not hold the value of %s under apps/%s (found %v, err %v)", step, e.Name, key, rtAppSecretHost, found, err)
				}
				time.Sleep(2 * time.Second)
			}
		}
		logf(config.LvlInfo, "%s: %s at version %d in the local file%s", step, key, n, map[bool]string{true: " and in every vault entry", false: ""}[len(vaults) > 0])
		return true
	}

	run := time.Now().UnixNano()
	v1, v2, a1 := fmt.Sprintf("rt-%d-v1", run), fmt.Sprintf("rt-%d-v2", run), fmt.Sprintf("rt-%d-a1", run)
	agentKey := rtAppSecretVar + "@" + rtAppSecretAgent
	// versions persist across runs (rule 10): count from what the file holds now
	base, _, _ := latest(rtAppSecretVar)
	agentBase, _, _ := latest(agentKey)

	// a monitor-only app: nothing provisioned, no plan reservation, so the test runs on any
	// cluster; its deployment variables are an app's secrets like any other
	if code, body := api(http.MethodPost, "/actions/addserver/"+rtAppSecretHost+"/"+rtAppSecretPort+"/app", map[string]interface{}{"monitorOnly": true}); code != http.StatusOK {
		return fail("adding the app: %d %s", code, body)
	}
	app := findApp()
	if app == nil {
		return fail("the app added through the API is not in the cluster")
	}
	appRemoved := false
	defer func() {
		if !appRemoved {
			if err := removeApp(); err != nil {
				logf(config.LvlWarn, "teardown: removing the app: %s", err)
			}
		}
	}()
	appPath := "/apps/" + app.GetId() + "/deployment/variables"
	varIndex := func() int {
		for i, v := range app.AppConfig.Deployment.Variables {
			if v.Name == rtAppSecretVar {
				return i
			}
		}
		return -1
	}

	// 1. a secret variable added through the API: version base+1
	if code, body := api(http.MethodPost, appPath+"/add", []config.VariableMapping{{Name: rtAppSecretVar, Type: config.VariableTypeSecret, Value: v1}}); code != http.StatusOK {
		return fail("adding the secret variable: %d %s", code, body)
	}
	if !saved("add") || !expect("add", rtAppSecretVar, base+1, v1) {
		return false
	}
	i := varIndex()
	if i < 0 {
		return fail("the variable added through the API is not in the app")
	}
	// 2. a per-agent value: its own key, one version
	if code, body := api(http.MethodPost, fmt.Sprintf("%s/index/%d/conditional/modify", appPath, i), map[string]interface{}{"value": config.AVSlice{{Agent: rtAppSecretAgent, Value: a1}}}); code != http.StatusOK {
		return fail("setting the per-agent value: %d %s", code, body)
	}
	if !saved("per-agent value") || !expect("per-agent value", agentKey, agentBase+1, a1) {
		return false
	}
	// 3. a new value: version base+2
	if code, body := api(http.MethodPost, fmt.Sprintf("%s/index/%d/value/modify", appPath, i), map[string]string{"value": v2}); code != http.StatusOK {
		return fail("modifying the value: %d %s", code, body)
	}
	if !saved("modify") || !expect("modify", rtAppSecretVar, base+2, v2) {
		return false
	}
	// 4. a save with nothing changed: no new version
	if !saved("unchanged") || !expect("unchanged", rtAppSecretVar, base+2, v2) {
		return false
	}
	// 5. the variable dropped: its versions stay (rule 10)
	if code, body := api(http.MethodPost, fmt.Sprintf("%s/index/%d/drop", appPath, i), nil); code != http.StatusOK {
		return fail("dropping the variable: %d %s", code, body)
	}
	if !saved("variable dropped") || !expect("variable dropped", rtAppSecretVar, base+2, v2) {
		return false
	}
	// 6. the app removed: its versions stay (rule 10)
	if err := removeApp(); err != nil {
		return fail("removing the app: %s", err)
	}
	appRemoved = true
	if !saved("app removed") || !expect("app removed", rtAppSecretVar, base+2, v2) || !expect("app removed", agentKey, agentBase+1, a1) {
		return false
	}
	logf(config.LvlInfo, "PASS")
	return true
}
