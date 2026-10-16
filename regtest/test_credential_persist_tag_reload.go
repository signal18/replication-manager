// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// LoadClusterConfigFromDisk is wired by the server package (server/regtest_api.go): the
// config a start builds for a cluster from the files on disk, its config maps included,
// leaving the running server as it was.
var LoadClusterConfigFromDisk func(clusterName string) (config.Config, error)

// TestCredentialPersistTagReload is the real-cluster regtest of the credential chain (#2075
// step 1, #2017), through repman's own API as the GUI calls it: a credential set at runtime
// must survive a database tag added then dropped (each rereads the credentials), be saved
// encrypted to the datadir <cluster>.toml by the save callback, and be the value a start
// builds from the files on disk (the server's own loader, not a parse of the file). A
// credential pinned in a config file must be refused and left unchanged.
//
// It uses backup-verification-credential: repman uses it only to reach the verification
// server, so the marker value never touches a monitored server. The former value is put back
// through the API, pass or fail. In static mode (monitoring-save-config off) the change
// lives in memory only: the tag change is checked, the save and the start are not.
func (regtest *RegTest) TestCredentialPersistTagReload(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	const key = "backup-verification-credential"
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST credential-persist-tag-reload: "+format, args...)
	}
	if APIToken == nil || APIRequest == nil || LoadClusterConfigFromDisk == nil {
		logf(config.LvlErr, "FAIL the server hooks are not wired")
		return false
	}
	token, err := APIToken(cl, config.GrantClusterSettings, config.GrantDBConfigFlag)
	if err != nil {
		logf(config.LvlErr, "FAIL no API user with the settings and database tag grants: %s", err)
		return false
	}
	// api calls a cluster route, the setting's value base64 as the GUI sends it
	api := func(path string) (int, string) {
		code, _, body, err := APIRequest(token, http.MethodPost, "/api/clusters/"+cl.Name+path)
		if err != nil {
			return 0, err.Error()
		}
		return code, strings.TrimSpace(string(body))
	}
	set := func(k, v string) (int, string) {
		return api("/settings/actions/set/" + k + "/" + base64.StdEncoding.EncodeToString([]byte(v)))
	}

	chain := cl.CredentialChain()
	if err := chain.CanSet(key); err != nil {
		logf(config.LvlErr, "FAIL %s takes no change here, nothing to test: %s", key, credstore.Reason(err))
		return false
	}
	original := cl.Conf.GetDecryptedValue(key)
	defer func() {
		if code, body := set(key, original); code != http.StatusOK {
			logf(config.LvlErr, "the former %s is not put back (%d): %s", key, code, body)
			return
		}
		cl.ConfigManager.SaveConfig(cl, true)
	}()

	// the user in clear, the password the part a save must encrypt
	password := fmt.Sprintf("marker%d", time.Now().UnixNano())
	marker := "regtest:" + password
	if code, body := set(key, marker); code != http.StatusOK {
		logf(config.LvlErr, "FAIL the API refused the change (%d): %s", code, body)
		return false
	}
	if cl.Conf.GetDecryptedValue(key) != marker {
		logf(config.LvlErr, "FAIL the API accepted the change and %s is not the value set", key)
		return false
	}
	logf("TEST", "%s set through the API, source %s -- OK", key, chain.Source(key))

	// a tag change rereads every credential (SetClusterCredentialsFromConfig): take a tag the
	// cluster does not have, add it then drop it, the tags end as they began
	tag := ""
	for _, t := range []string{"pfs", "localinfile", "logaudit"} {
		if !cl.Configurator.HaveDBTag(t) {
			tag = t
			break
		}
	}
	if tag == "" {
		logf(config.LvlErr, "FAIL no free database tag to add and drop")
		return false
	}
	if code, body := api("/settings/actions/add-db-tag/" + tag); code != http.StatusOK {
		logf(config.LvlErr, "FAIL adding the tag %s (%d): %s", tag, code, body)
		return false
	}
	added := cl.Conf.GetDecryptedValue(key) == marker
	if code, body := api("/settings/actions/drop-db-tag/" + tag); code != http.StatusOK {
		logf(config.LvlErr, "FAIL dropping the tag %s (%d): %s", tag, code, body)
		return false
	}
	if !added {
		logf(config.LvlErr, "FAIL %s reverted when the tag %s was added (#2017)", key, tag)
		return false
	}
	if cl.Conf.GetDecryptedValue(key) != marker {
		logf(config.LvlErr, "FAIL %s reverted when the tag %s was dropped (#2017)", key, tag)
		return false
	}
	logf("TEST", "%s kept across the tag %s added and dropped -- OK", key, tag)

	// a credential pinned in a config file is refused, its value unchanged: the value sent
	// is the current one, so even a wrongly accepted change changes nothing
	for _, pinned := range []string{"db-servers-credential", "replication-credential"} {
		if _, ok := cl.Conf.ImmuableFlagMap[pinned]; !ok {
			continue
		}
		before := cl.Conf.GetDecryptedValue(pinned)
		code, body := set(pinned, before)
		if code == http.StatusOK || !strings.Contains(body, credstore.ErrChangeRefused.Error()) {
			logf(config.LvlErr, "FAIL %s is pinned in a config file and the API did not refuse it (%d)", pinned, code)
			return false
		}
		if cl.Conf.GetDecryptedValue(pinned) != before {
			logf(config.LvlErr, "FAIL the refused change of %s changed its value", pinned)
			return false
		}
		logf("TEST", "%s is pinned: the API refused the change, value unchanged -- OK", pinned)
	}

	if !chain.Persists(key) {
		logf("TEST", "monitoring-save-config is off: the change lives in memory (%s), the save and the start are not checked", chain.Source(key))
		return true
	}

	// saved by the save callback, the only writer of the config files (T25)
	cl.ConfigManager.SaveConfig(cl, true)
	savedFile := filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml")
	tree, err := toml.LoadFile(savedFile)
	if err != nil {
		logf(config.LvlErr, "FAIL cannot read %s: %s", savedFile, err)
		return false
	}
	saved, _ := tree.Get("saved-" + cl.Name).(*toml.Tree)
	raw := ""
	if saved != nil {
		raw, _ = saved.Get(key).(string)
	}
	if !strings.HasPrefix(raw, "regtest:") || strings.Contains(raw, password) {
		logf(config.LvlErr, "FAIL %s is not saved encrypted in [saved-%s] of %s", key, cl.Name, savedFile)
		return false
	}
	logf("TEST", "%s saved encrypted to %s -- OK", key, savedFile)

	// a start: the server builds the cluster's config from the files, loads the cluster's
	// encryption key (else the server's), and the chain reads the credentials from its maps
	started, err := LoadClusterConfigFromDisk(cl.Name)
	if err != nil {
		logf(config.LvlErr, "FAIL the config a start builds from disk: %s", err)
		return false
	}
	if k, _ := started.LoadEncrytionKey(); k == nil {
		started.SecretKey = cl.Conf.SecretKey
	}
	credstore.NewConfigChain(&started, nil).Refresh()
	if started.GetDecryptedValue(key) != marker {
		logf(config.LvlErr, "FAIL %s in the config a start builds from disk is not the value set at runtime", key)
		return false
	}
	logf("TEST", "%s is the value a start builds from the files on disk -- OK", key)
	return true
}
