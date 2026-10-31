// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// TestCredentialVersionsRotation is the real-cluster regtest of the versioned credential
// stores (#2227 rules 1, 1b, 2, T13), through repman's own API: on a MariaDB or MySQL cluster
// whose config holds its starting credential (pass1), running on pass1 or on an earlier
// run's rotation, a rotation (pass2), a config reload, a
// database tag added then dropped, and a start from the files on disk each leave every
// server monitored with no access denied and logged in with pass2, while the config still
// holds pass1 and the local versioned file holds both versions.
//
// It needs monitoring-secret-versioning on; a credential set in a config file is rotated as
// versions too (#2227 rule 9). The rotation is real: the cluster ends on pass2.
func (regtest *RegTest) TestCredentialVersionsRotation(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return regtest.credentialVersionsRotation(cl, false)
}

// TestCredentialVersionsRotationVault is TestCredentialVersionsRotation with a vault entry
// enabled ([[<cluster>.secret-kv]] on a Vault or OpenBao dev container the test environment
// starts): the same steps, and every enabled KV entry holds pass2 at
// <path-prefix>/<cluster>/databases/<server>/db-servers-credential.
func (regtest *RegTest) TestCredentialVersionsRotationVault(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return regtest.credentialVersionsRotation(cl, true)
}

func (regtest *RegTest) credentialVersionsRotation(cl *cluster.Cluster, withVault bool) bool {
	const key = "db-servers-credential"
	name := "credential-versions-rotation"
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
	if APIToken == nil || APIRequest == nil || LoadClusterConfigFromDisk == nil {
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
	if err := cl.CredentialChain().CanSet(key); err != nil {
		return fail("%s takes no change here: %s", key, credstore.Reason(err))
	}
	token, err := APIToken(cl, config.GrantClusterRotatePasswords, config.GrantClusterSettings, config.GrantDBConfigFlag)
	if err != nil {
		return fail("no API user with the rotation, settings and database tag grants: %s", err)
	}
	api := func(path string) (int, string) {
		code, _, body, err := APIRequest(token, http.MethodPost, "/api/clusters/"+cl.Name+path)
		if err != nil {
			return 0, err.Error()
		}
		return code, strings.TrimSpace(string(body))
	}

	// the starting value the config holds: the dynamic config, else a config file
	startingRaw, ok := cl.Conf.GetDynamicFlag(key)
	if !ok {
		startingRaw, _ = cl.Conf.GetImmuableFlag(key)
	}
	pass1 := cl.Conf.DecryptSecretValue(key, fmt.Sprintf("%v", startingRaw))
	if pass1 == "" {
		return fail("the config holds no starting value for %s", key)
	}
	// the credential the cluster runs on: the starting value, or the last rotation's when the
	// test runs again on the same cluster
	current := cl.Conf.GetDecryptedValue(key)
	if current == "" {
		return fail("repman holds no current value for %s", key)
	}

	// check runs after each step: every server answers pass2, the config keeps pass1, the file
	// holds both
	check := func(step, pass2 string) bool {
		deadline := time.Now().Add(60 * time.Second)
		for {
			why := ""
			if v := cl.Conf.GetDecryptedValue(key); v != pass2 {
				why = "repman's current credential is not the rotated one"
			}
			for _, srv := range cl.Servers {
				if why != "" {
					break
				}
				if srv.IsDown() {
					why = srv.URL + " is down"
					break
				}
				if cl.StateMachine.IsInState("ERR00004@" + srv.URL) {
					why = srv.URL + " access denied"
					break
				}
				conn, err := srv.GetNewDBConn()
				if err != nil {
					why = fmt.Sprintf("%s: no login with the rotated credential: %s", srv.URL, err)
					break
				}
				conn.Close()
			}
			if why == "" {
				break
			}
			if time.Now().After(deadline) {
				return fail("%s: %s", step, why)
			}
			time.Sleep(2 * time.Second)
		}
		raw, ok := cl.Conf.GetDynamicFlag(key)
		if !ok {
			raw, _ = cl.Conf.GetImmuableFlag(key)
		}
		if cl.Conf.DecryptSecretValue(key, fmt.Sprintf("%v", raw)) != pass1 {
			return fail("%s: the config does not hold the starting value any more", step)
		}
		cl.Save()
		// the file is written by the config-sync gate, every 60 ticks; a cycle still busy
		// skips one gate, so two periods
		deadline = time.Now().Add(2 * 60 * time.Duration(cl.Conf.MonitoringTicker) * time.Second)
		for {
			versions, err := cl.SecretVersionsInFile(key)
			if err == nil && len(versions) >= 2 && versions[0] == pass1 && versions[len(versions)-1] == pass2 {
				break
			}
			if time.Now().After(deadline) {
				hint := ""
				if cl.Conf.MonitoringSecretVersioningAutoPrune && cl.Conf.MonitoringSecretVersioningKeepLast > 0 {
					hint = fmt.Sprintf("; monitoring-secret-versioning-auto-prune keeps the last %d versions only, the starting one may be pruned", cl.Conf.MonitoringSecretVersioningKeepLast)
				}
				return fail("%s: the local file does not hold the starting and the rotated versions (%d versions, %v)%s", step, len(versions), err, hint)
			}
			time.Sleep(2 * time.Second)
		}
		for _, e := range vaults {
			for _, srv := range cl.Servers {
				deadline = time.Now().Add(90 * time.Second)
				for {
					v, found, err := cl.SecretKvRead(e, cluster.SecretServerName(srv), key)
					if err == nil && found && v == pass2 {
						break
					}
					if time.Now().After(deadline) {
						return fail("%s: vault entry %s does not hold the rotated credential under databases/%s (%v)", step, e.Name, cluster.SecretServerName(srv), err)
					}
					time.Sleep(3 * time.Second)
				}
			}
		}
		logf("TEST", "%s: every server logged in with the rotated credential, the config keeps the starting value, the file holds both -- OK", step)
		return true
	}

	// the starting value is a version of the file before the rotation
	cl.MarkSecretVersionStoreDirty()

	// rotate through the API
	if code, body := api("/actions/rotate-passwords"); code != http.StatusOK {
		return fail("the rotation was refused (%d): %s", code, body)
	}
	deadline := time.Now().Add(120 * time.Second)
	for cl.Conf.GetDecryptedValue(key) == current {
		if time.Now().After(deadline) {
			return fail("the rotation did not change %s", key)
		}
		time.Sleep(2 * time.Second)
	}
	pass2 := cl.Conf.GetDecryptedValue(key)
	if !check("rotation", pass2) {
		return false
	}

	// reload the config through the API
	if code, body := api("/settings/actions/reload"); code != http.StatusOK {
		return fail("the config reload was refused (%d): %s", code, body)
	}
	time.Sleep(5 * time.Second)
	if !check("config reload", pass2) {
		return false
	}

	// a database tag added then dropped: each rereads every credential
	tag := ""
	for _, t := range []string{"pfs", "localinfile", "logaudit"} {
		if !cl.Configurator.HaveDBTag(t) {
			tag = t
			break
		}
	}
	if tag == "" {
		return fail("no free database tag to add and drop")
	}
	if code, body := api("/settings/actions/add-db-tag/" + tag); code != http.StatusOK {
		return fail("adding the tag %s (%d): %s", tag, code, body)
	}
	if !check("tag "+tag+" added", pass2) {
		return false
	}
	if code, body := api("/settings/actions/drop-db-tag/" + tag); code != http.StatusOK {
		return fail("dropping the tag %s (%d): %s", tag, code, body)
	}
	if !check("tag "+tag+" dropped", pass2) {
		return false
	}

	// a start: the config the server builds from the files on disk, and the chain a start
	// builds over it with the local file
	started, err := LoadClusterConfigFromDisk(cl.Name)
	if err != nil {
		return fail("the config a start builds from disk: %s", err)
	}
	if k, _ := started.LoadEncrytionKey(); k == nil {
		started.SecretKey = cl.Conf.SecretKey
	}
	started.MigrateSecretKvs()
	cl.ChainAtStart(&started).Refresh()
	if v := started.GetDecryptedValue(key); v != pass2 {
		return fail("restart: a start from the files on disk runs on another credential than the rotated one")
	}
	raw, ok := started.GetDynamicFlag(key)
	if !ok {
		raw, _ = started.GetImmuableFlag(key)
	}
	if started.DecryptSecretValue(key, fmt.Sprintf("%v", raw)) != pass1 {
		return fail("restart: the config on disk does not hold the starting value")
	}
	logf("TEST", "restart: a start from the files on disk runs on the rotated credential, its config holds the starting value -- OK")
	return true
}
