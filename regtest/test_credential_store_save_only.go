// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// TestCredentialStoreSaveOnly is the real-cluster regtest of #2041 (T25): secret_store.json
// is written by the config save only, never by the API setter or by a backup.
//
//  1. A tracked secret set through the settings API: right after the call, while no save has
//     completed since, the file does not hold the new version; the next save writes it.
//  2. A logical backup with backup-encryption on records the password version it used in its
//     metadata; the file holds that version once the next save has run.
//
// The versioned file on (monitoring-secret-versioning), encryption key set.
func (regtest *RegTest) TestCredentialStoreSaveOnly(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const key = "backup-verification-credential"
	const dbKey = "db-servers-credential"
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST credential-store-save-only: "+format, args...)
	}
	fail := func(format string, args ...interface{}) bool {
		logf(config.LvlErr, "FAIL "+format, args...)
		return false
	}
	if APIToken == nil || APIRequest == nil {
		return fail("the server hooks are not wired")
	}
	if !cl.Conf.IsMonitoringSecretVersioningEnabled() {
		return fail("monitoring-secret-versioning is off: the local file keeps no version")
	}
	if cl.Conf.SecretKey == nil {
		return fail("no encryption key: the file keeps no encrypted version")
	}
	token, err := APIToken(cl, config.GrantClusterSettings, config.GrantDBBackup)
	if err != nil {
		return fail("no API user with the settings and backup grants: %s", err)
	}
	api := func(method, path string) (int, string) {
		code, _, body, err := APIRequest(token, method, "/api/clusters/"+cl.Name+path)
		if err != nil {
			return 0, err.Error()
		}
		return code, strings.TrimSpace(string(body))
	}
	b64 := func(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }
	// the file is written by the config-sync gate; a cycle still busy skips one gate, so two
	// periods
	gate := 2 * 60 * time.Duration(cl.Conf.MonitoringTicker) * time.Second
	fileHolds := func(k, value string) bool {
		versions, err := cl.SecretVersionsInFile(k)
		return err == nil && slices.Contains(versions, value)
	}
	waitFile := func(k, value string) bool {
		for deadline := time.Now().Add(gate); ; time.Sleep(2 * time.Second) {
			if fileHolds(k, value) {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
		}
	}

	// 1. a tracked secret through the settings API
	if err := cl.CredentialChain().CanSet(key); err != nil {
		return fail("%s takes no change here, nothing to test: %s", key, credstore.Reason(err))
	}
	original := cl.Conf.GetDecryptedValue(key)
	defer func() {
		if code, body := api(http.MethodPost, "/settings/actions/set/"+key+"/"+b64(original)); code != http.StatusOK {
			logf(config.LvlErr, "the former %s is not put back (%d): %s", key, code, body)
			return
		}
		cl.ConfigManager.SaveConfig(cl, true)
	}()
	marker := fmt.Sprintf("regtest:marker%d", time.Now().UnixNano())
	savedBefore := cl.ConfigSavedAsOf()
	if code, body := api(http.MethodPost, "/settings/actions/set/"+key+"/"+b64(marker)); code != http.StatusOK {
		return fail("the API refused the change (%d): %s", code, body)
	}
	if fileHolds(key, marker) {
		// a save may have run between the call and the read: then the file is the save's
		time.Sleep(3 * time.Second)
		if !cl.ConfigSavedAsOf().After(savedBefore) {
			return fail("the file holds the new %s right after the setter, no save completed since: written outside the save callback", key)
		}
		logf("TEST", "a save ran right after the setter: the file is the save's, not checked")
	} else {
		logf("TEST", "right after the setter, no save yet: the file does not hold the new %s -- OK", key)
	}
	if !waitFile(key, marker) {
		return fail("the file does not hold the new %s after the next save", key)
	}
	logf("TEST", "the next save wrote the new %s to the file -- OK", key)

	// 2. a logical backup with backup-encryption on
	if !cl.Conf.BackupEncryption {
		if code, body := api(http.MethodPost, "/settings/actions/switch/backup-encryption"); code != http.StatusOK {
			return fail("backup-encryption was not switched on (%d): %s", code, body)
		}
		defer func() {
			if code, body := api(http.MethodPost, "/settings/actions/switch/backup-encryption"); code != http.StatusOK {
				logf(config.LvlErr, "backup-encryption is not switched back off (%d): %s", code, body)
			}
		}()
	}
	master := cl.GetMaster()
	if master == nil {
		return fail("no master to back up")
	}
	known := map[int64]bool{}
	for id := range cl.BackupMetaMap.ToNewMap() {
		known[id] = true
	}
	if code, body := api(http.MethodGet, "/servers/"+master.Id+"/actions/backup-logical"); code != http.StatusOK {
		return fail("the logical backup was refused (%d): %s", code, body)
	}
	version := 0
	for deadline := time.Now().Add(10 * time.Minute); version == 0; time.Sleep(5 * time.Second) {
		for id, m := range cl.BackupMetaMap.ToNewMap() {
			if !known[id] && m.Encrypted && m.EncryptionKeyVersion > 0 {
				version = m.EncryptionKeyVersion
			}
		}
		if time.Now().After(deadline) {
			return fail("no new encrypted backup recorded a password version in 10 minutes")
		}
	}
	password := cl.Conf.GetDecryptedValue(dbKey)
	if !waitFile(dbKey, password) {
		return fail("the file does not hold the password the backup used (version %d) after the next save", version)
	}
	if versions, _ := cl.SecretVersionsInFile(dbKey); len(versions) < version {
		return fail("the backup recorded version %d, the file holds %d versions", version, len(versions))
	}
	logf("TEST", "the backup recorded password version %d, the file holds it after the next save -- OK", version)
	return true
}
