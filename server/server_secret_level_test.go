// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

const serverLevelACL = "admin:cluster db proxy prov global grant show,ops:cluster db show"

func serverLevelConf(dir, list string) *config.Config {
	return &config.Config{
		WorkingDir:                 dir,
		SecretKey:                  []byte("0123456789abcdef"),
		ConfRewrite:                true,
		MonitoringSecretVersioning: true,
		APIUsers:                   list,
		APIUsersACLAllow:           serverLevelACL,
		ImmuableFlagMap:            map[string]interface{}{},
		DynamicFlagMap:             map[string]interface{}{"api-credentials": list},
		DefaultFlagMap:             map[string]interface{}{},
		Secrets:                    config.NewSecrets(map[string]config.Secret{}),
	}
}

// newServerLevelTestManager is a server with two clusters whose chains read the server
// secrets from the server's (CREDENTIAL_STORES.md "Two levels").
func newServerLevelTestManager(t *testing.T, list string) *ReplicationManager {
	t.Helper()
	dir := t.TempDir()
	repman := &ReplicationManager{Conf: serverLevelConf(dir, list), Logrus: log.New(), Status: ConstMonitorActif, Clusters: map[string]*cluster.Cluster{}}
	globals := &cluster.ServerGlobals{ServerSecrets: repman.serverSecretLevel(), CheckServerSecrets: repman.reloadServerVersionsFromPeer}
	repman.CredentialChain().Refresh()
	for _, name := range []string{"c1", "c2"} {
		cl := &cluster.Cluster{Name: name, Conf: serverLevelConf(filepath.Join(dir, name), list), ServerGlobals: globals,
			APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType(), Roles: config.GetRoleType(), SecurityLogrus: log.New(), Logrus: log.New()}
		cl.CredentialChain().Refresh()
		cl.LoadAPIUsers()
		repman.Clusters[name] = cl
		repman.ClusterList = append(repman.ClusterList, name)
	}
	return repman
}

// A rotation of a user of the instance's list is one version at the server level; every
// cluster takes it, the other users keep their password, no cluster keeps a copy.
func TestServerLevelRotation(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	c1 := repman.Clusters["c1"]
	res, err := repman.rotateAPIUserPassword(c1, "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Password) != rotatedPasswordLength || strings.Join(res.Clusters, ",") != "c1,c2" || res.Scope != cluster.APIUserScopeInstance {
		t.Fatalf("result %+v", res)
	}
	for _, cl := range repman.Clusters {
		if got := cl.APIUsers["ops"].Password; got != res.Password {
			t.Fatalf("%s: ops not rotated", cl.Name)
		}
		if got := cl.APIUsers["admin"].Password; got != "first" {
			t.Fatalf("%s: admin changed", cl.Name)
		}
		if _, err := cl.GetAPIUser("ops", "second"); err == nil {
			t.Fatalf("%s: the old password still logs in", cl.Name)
		}
		if err := cl.CredentialChain().Set(apiCredentialsKey, "admin:x"); !errors.Is(err, credstore.ErrServerSecret) {
			t.Fatalf("%s: a cluster changed a server secret: %v", cl.Name, err)
		}
	}
	if n := repman.serverVersionStore().Count(apiCredentialsKey); n != 1 {
		t.Fatalf("%d server versions, want 1", n)
	}
}

// An encrypted list: the other users keep their password, nothing is decrypted twice.
func TestServerLevelRotationEncryptedList(t *testing.T) {
	repman := newServerLevelTestManager(t, "")
	enc := "admin:" + repman.Conf.GetEncryptedString("first") + ",ops:" + repman.Conf.GetEncryptedString("second")
	repman.Conf.DynamicFlagMap[apiCredentialsKey] = enc
	repman.CredentialChain().Refresh()
	repman.applyServerSecrets()
	if err := repman.setInstanceUserPassword("ops", "Rotated-2"); err != nil {
		t.Fatal(err)
	}
	if got := repman.Conf.DecryptedAPIUserList(); got != "admin:first,ops:Rotated-2" {
		t.Fatalf("list %q", got)
	}
	if got := repman.Clusters["c2"].APIUsers["admin"].Password; got != "first" {
		t.Fatalf("admin %q", got)
	}
}

// Set in a configuration file with no store keeping versions: the one refusal (rule 9).
func TestServerLevelRotationRefusedWithoutVersionsOnAFileValue(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	repman.Conf.MonitoringSecretVersioning = false
	repman.Conf.ImmuableFlagMap[apiCredentialsKey] = "admin:first,ops:second"
	if _, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin"); !errors.Is(err, errRotateRefused) {
		t.Fatalf("a file value with no versioned store: %v", err)
	}
}

// The server's file is written by its save and taken by a standby sharing the data directory.
func TestServerLevelFileAndStandby(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	if err := repman.setInstanceUserPassword("ops", "Rotated-3"); err != nil {
		t.Fatal(err)
	}
	if err := repman.writeServerSecretFile(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(repman.Conf.WorkingDir, "secret_store.json"))
	if err != nil || strings.Contains(string(raw), "Rotated-3") || !strings.Contains(string(raw), "hash_") {
		t.Fatalf("file: %v, encrypted %v", err, !strings.Contains(string(raw), "Rotated-3"))
	}
	standby := &ReplicationManager{Conf: serverLevelConf(repman.Conf.WorkingDir, "admin:first,ops:second"), Logrus: log.New(), Status: ConstMonitorStandby}
	standby.serverVersionStore() // loads the file at the first use
	standby.CredentialChain().Refresh()
	if got := standby.Conf.DecryptedAPIUserList(); got != "admin:first,ops:Rotated-3" {
		t.Fatalf("standby list %q", got)
	}
}

// A global setting fans out to every cluster: a server secret is read from the server, never
// written by a cluster.
func TestServerLevelFanOut(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first")
	if err := repman.CredentialChain().Set("mail-smtp-password", "smtp-1"); err != nil {
		t.Fatal(err)
	}
	c1 := repman.Clusters["c1"]
	if err := c1.SetCredential("mail-smtp-password", "smtp-1"); err != nil {
		t.Fatal(err)
	}
	if got := c1.Conf.GetDecryptedValue("mail-smtp-password"); got != "smtp-1" {
		t.Fatalf("cluster reads %q", got)
	}
	if c1.CredentialChain().Source("mail-smtp-password") != credstore.SourceServer {
		t.Fatalf("source %q", c1.CredentialChain().Source("mail-smtp-password"))
	}
}
