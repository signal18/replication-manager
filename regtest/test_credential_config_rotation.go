// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// TestCredentialConfigRotationScheduled is the real-cluster regtest of #2269 (T13), through
// repman's own API: with no store keeping versions (monitoring-secret-versioning off, no vault
// entry) the config is the credential store. The scheduled rotation, switched on with a cron of
// every minute, rotates the database credential, saved to the config before any server
// changes; then every server logs in with the new
// password, repman keeps the previous one in memory, a config reload and a start from the files
// on disk run on the new one, and the cluster config holds it encrypted. A credential set in a
// config file is refused here (no store keeps versions): the test fails on it. The schedule is
// switched off and its cron put back at the end. The rotation is real: the cluster ends on the
// new password.
func (regtest *RegTest) TestCredentialConfigRotationScheduled(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const key = "db-servers-credential"
	const name = "credential-config-rotation-scheduled"
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
	if cl.Conf.IsMonitoringSecretVersioningEnabled() || len(cl.Conf.SecretKvActive()) > 0 {
		return fail("a store keeps versions (monitoring-secret-versioning, backup-encryption or a vault entry): the config is not the store")
	}
	if err := cl.CredentialChain().CanSet(key); err != nil {
		return fail("%s takes no change here: %s", key, credstore.Reason(err))
	}
	token, err := APIToken(cl, config.GrantClusterSettings)
	if err != nil {
		return fail("no API user with the settings grant: %s", err)
	}
	api := func(path string) (int, string) {
		code, _, body, err := APIRequest(token, http.MethodPost, "/api/clusters/"+cl.Name+path)
		if err != nil {
			return 0, err.Error()
		}
		return code, strings.TrimSpace(string(body))
	}

	before := cl.Conf.GetDecryptedValue(key)
	if before == "" {
		return fail("repman holds no current value for %s", key)
	}

	// the schedule: every minute, switched on; put back as found at the end
	cron := cl.Conf.SchedulerRotatePasswordsCron
	wasOn := cl.Conf.SchedulerRotatePasswords
	defer func() {
		if cl.Conf.SchedulerRotatePasswords != wasOn {
			api("/settings/actions/switch/scheduler-rotate-passwords")
		}
		api("/settings/actions/set-cron/scheduler-rotate-passwords-cron/" + url.PathEscape(cron))
	}()
	if code, body := api("/settings/actions/set-cron/scheduler-rotate-passwords-cron/" + url.PathEscape("0 * * * * *")); code != http.StatusOK {
		return fail("setting the cron (%d): %s", code, body)
	}
	if !cl.Conf.SchedulerRotatePasswords {
		if code, body := api("/settings/actions/switch/scheduler-rotate-passwords"); code != http.StatusOK {
			return fail("switching the schedule on (%d): %s", code, body)
		}
	}
	deadline := time.Now().Add(150 * time.Second)
	for cl.Conf.GetDecryptedValue(key) == before {
		if time.Now().After(deadline) {
			return fail("no scheduled rotation within 150 s (scheduler running: %v)", cl.Conf.MonitorScheduler)
		}
		time.Sleep(2 * time.Second)
	}
	// one rotation is enough: the next minute would rotate again
	if code, body := api("/settings/actions/switch/scheduler-rotate-passwords"); code != http.StatusOK {
		return fail("switching the schedule off (%d): %s", code, body)
	}
	// the run saves the new values before it changes a database (#2269): it ends within two
	// config-sync gates, then the servers
	gates := 2 * 60 * time.Duration(cl.Conf.MonitoringTicker) * time.Second
	for deadline = time.Now().Add(gates + 60*time.Second); cl.IsInPasswordRotation(); time.Sleep(2 * time.Second) {
		if time.Now().After(deadline) {
			return fail("the rotation did not end within %s", gates+60*time.Second)
		}
	}
	pass := cl.Conf.GetDecryptedValue(key)
	if pass == before {
		return fail("rotation: the new credential was put back (no config save in time, see the security log)")
	}

	if why := serversOnCredential(cl, key, pass, 60*time.Second); why != "" {
		return fail("rotation: %s", why)
	}
	if cl.SecurityStateMachine.CurState.Search("WARN0274") {
		return fail("rotation: the skipped-rotation state is open after a successful run")
	}
	if old := cl.Conf.Secrets.Get(key).OldValue; old == "" || old == pass {
		return fail("rotation: the previous credential is not kept in memory")
	}
	logf("TEST", "scheduled rotation: every server logged in with the new credential, the previous one kept in memory -- OK")

	if code, body := api("/settings/actions/reload"); code != http.StatusOK {
		return fail("the config reload was refused (%d): %s", code, body)
	}
	time.Sleep(5 * time.Second)
	if why := serversOnCredential(cl, key, pass, 60*time.Second); why != "" {
		return fail("config reload: %s", why)
	}

	// the cluster config holds the new value, encrypted: written by the config-sync gate,
	// every 60 ticks; a cycle still busy skips one gate, so two periods
	cl.Save()
	deadline = time.Now().Add(2 * 60 * time.Duration(cl.Conf.MonitoringTicker) * time.Second)
	for {
		why := ""
		started, err := LoadClusterConfigFromDisk(cl.Name)
		if err != nil {
			return fail("the config a start builds from disk: %s", err)
		}
		if k, _ := started.LoadEncrytionKey(); k == nil {
			started.SecretKey = cl.Conf.SecretKey
		}
		started.MigrateSecretKvs()
		raw, _ := started.GetDynamicFlag(key)
		if s := fmt.Sprintf("%v", raw); !strings.Contains(s, "hash_") {
			why = "the cluster config does not hold the credential encrypted"
		} else if started.DecryptSecretValue(key, s) != pass {
			why = "the cluster config does not hold the new credential"
		} else if cl.ChainAtStart(&started).Refresh(); started.GetDecryptedValue(key) != pass {
			why = "a start from the files on disk runs on another credential than the rotated one"
		}
		if why == "" {
			break
		}
		if time.Now().After(deadline) {
			return fail("restart: %s", why)
		}
		time.Sleep(2 * time.Second)
	}
	logf("TEST", "restart: the cluster config holds the new credential encrypted, a start from the files on disk runs on it -- OK")
	return true
}
