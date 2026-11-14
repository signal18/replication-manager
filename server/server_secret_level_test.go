// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/config/manager"
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
	repman.ConfigManager = manager.NewConfigManager(config.NewLogrusWrapper(repman.Conf, repman.Logrus))
	globals := &cluster.ServerGlobals{ServerSecrets: repman.serverSecretLevel(), CheckServerSecrets: repman.reloadServerVersionsFromPeer, AdoptServerVersions: repman.adoptServerVersionsFromCluster}
	repman.CredentialChain().Refresh()
	for _, name := range []string{"c1", "c2"} {
		cl := &cluster.Cluster{Name: name, Conf: serverLevelConf(dir, list), ServerGlobals: globals,
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

// A global setting of a server secret: the server's chain takes it, every cluster reads it from
// the server and uses it, and the server's file is written at once. Through one cluster's
// settings it is refused.
func TestServerLevelFanOut(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first")
	if err := repman.setServerSetting("admin", "/api/clusters/settings/actions/set/mail-smtp-password", "mail-smtp-password", base64.StdEncoding.EncodeToString([]byte("smtp-1"))); err != nil {
		t.Fatal(err)
	}
	for _, cl := range repman.Clusters {
		if got := cl.Conf.GetDecryptedValue("mail-smtp-password"); got != "smtp-1" || cl.Conf.MailSMTPPassword != "smtp-1" {
			t.Fatalf("%s does not use the new value", cl.Name)
		}
		if cl.CredentialChain().Source("mail-smtp-password") != credstore.SourceServer {
			t.Fatalf("%s: source %q", cl.Name, cl.CredentialChain().Source("mail-smtp-password"))
		}
		if err := cl.SetCredential("mail-smtp-password", "smtp-2"); !errors.Is(err, credstore.ErrServerSecret) {
			t.Fatalf("%s: a cluster's setting changed a server secret: %v", cl.Name, err)
		}
	}
	if n := repman.serverVersionStore().Count("mail-smtp-password"); n != 1 {
		t.Fatalf("%d server versions", n)
	}
	if file, err := (credstore.LocalFile{DataDir: repman.Conf.WorkingDir}).Load(credstore.ServerScope()); err != nil || len(file["mail-smtp-password"]) != 1 {
		t.Fatalf("the server's file is not written at once: %v", err)
	}
}

// A server secret is changed by the instance's owners only: the server credentials grant on
// every cluster. Rotating another user of the instance's list with the rotation grant alone is
// refused; a user may always rotate its own password.
func TestServerLevelRotationNeedsServerCredentials(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,lim:second,viewer:third")
	for _, cl := range repman.Clusters {
		cl.Conf.APIUsersACLAllow = serverLevelACL + ",lim:cluster-rotate-passwords show,viewer:show"
		cl.LoadAPIUsers()
	}
	c1 := repman.Clusters["c1"]
	if _, err := repman.rotateAPIUserPassword(c1, "viewer", "lim"); !errors.Is(err, errRotateRefused) || !errors.Is(err, errNoServerCredentials) {
		t.Fatalf("rotated by a user without the server credentials grant: %v", err)
	}
	if _, err := repman.rotateAPIUserPassword(c1, "lim", "lim"); err != nil {
		t.Fatalf("a user cannot ban its own password: %v", err)
	}
	if _, err := repman.rotateAPIUserPassword(c1, "viewer", "admin"); err != nil {
		t.Fatalf("an owner cannot rotate: %v", err)
	}
	if err := repman.setServerSetting("lim", "/api/clusters/settings/actions/set/mail-smtp-password", "mail-smtp-password", base64.StdEncoding.EncodeToString([]byte("x"))); err == nil {
		t.Fatal("a server secret set in the global settings without the server credentials grant")
	}
}

// The ban is on disk when it is answered: a restart right after it, with the old list still in
// /etc, keeps the new password.
func TestServerLevelRotationSurvivesARestart(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil || res.PersistError != "" {
		t.Fatalf("%v %q", err, res.PersistError)
	}
	conf := serverLevelConf(repman.Conf.WorkingDir, "admin:first,ops:second")
	conf.ImmuableFlagMap[apiCredentialsKey] = "admin:first,ops:second"
	restarted := &ReplicationManager{Conf: conf, Logrus: log.New(), Status: ConstMonitorActif, Clusters: map[string]*cluster.Cluster{}}
	restarted.takeServerConfigFileCredentials()
	if got := restarted.Conf.DecryptedAPIUserList(); got != "admin:first,ops:"+res.Password {
		t.Fatal("after a restart the rotated password is not the current one")
	}
}

// The new password is in force but the server's file cannot be written: the rotation is kept
// (undoing it would give the compromised password back) and the answer says so.
func TestServerLevelRotationPersistFailureIsSaid(t *testing.T) {
	repman := newServerLevelTestManager(t, "admin:first,ops:second")
	if err := os.MkdirAll(filepath.Join(repman.Conf.WorkingDir, "secret_store.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.PersistError, "NOT written to disk") {
		t.Fatalf("persist error %q", res.PersistError)
	}
	if _, err := repman.Clusters["c2"].GetAPIUser("ops", "second"); err == nil {
		t.Fatal("the old password still logs in")
	}
}

// The upgrade: an older release kept a rotation of the instance's list in each cluster's file.
// The server takes it before it checks the config file, so the rotation stays in force over
// the value still in /etc, and the cluster's file is written without it.
func TestServerLevelAdoptsClusterVersionsAtUpgrade(t *testing.T) {
	dir := t.TempDir()
	conf := serverLevelConf(dir, "admin:first,ops:second")
	conf.ImmuableFlagMap[apiCredentialsKey] = "admin:first,ops:second"
	before := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	old := map[string][]credstore.FileVersion{
		apiCredentialsKey:       {{Version: 1, HashValue: conf.GetEncryptedString("admin:first,ops:Rotated-old"), RotatedAt: before}},
		"db-servers-credential": {{Version: 1, HashValue: conf.GetEncryptedString("root:db"), RotatedAt: before}},
	}
	local := credstore.LocalFile{DataDir: dir}
	for _, name := range []string{"c1", "c2"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := local.Write(credstore.ClusterScope(name), old); err != nil {
			t.Fatal(err)
		}
	}
	repman := &ReplicationManager{Conf: conf, Logrus: log.New(), Status: ConstMonitorActif, Clusters: map[string]*cluster.Cluster{}, ClusterList: []string{"c1", "c2"}}
	repman.takeServerConfigFileCredentials()
	if got := repman.Conf.DecryptedAPIUserList(); got != "admin:first,ops:Rotated-old" {
		t.Fatalf("after the upgrade the list is %q", got)
	}
	if n := repman.serverVersionStore().Count(apiCredentialsKey); n < 1 {
		t.Fatalf("%d server versions", n)
	}
	globals := &cluster.ServerGlobals{ServerSecrets: repman.serverSecretLevel(), AdoptServerVersions: repman.adoptServerVersionsFromCluster}
	cl := &cluster.Cluster{Name: "c1", Conf: serverLevelConf(dir, "admin:first,ops:second"), ServerGlobals: globals,
		APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType(), Roles: config.GetRoleType(), SecurityLogrus: log.New(), Logrus: log.New()}
	// as on an instance: the cluster's config holds every secret, the server's ones included
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{
		apiCredentialsKey:       {Value: cl.Conf.GetEncryptedString("admin:first,ops:second")},
		"db-servers-credential": {Value: cl.Conf.GetEncryptedString("root:db")},
	})
	cl.CredentialChain().Refresh()
	cl.LoadAPIUsers()
	cl.MarkSecretVersionStoreDirty()
	if _, err := cl.GetAPIUser("ops", "Rotated-old"); err != nil {
		t.Fatalf("the cluster does not take the rotated password: %v", err)
	}
	if err := cl.ReconcileSecretVersionStore(); err != nil {
		t.Fatal(err)
	}
	file, err := local.Load(credstore.ClusterScope("c1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file[apiCredentialsKey]; ok {
		t.Fatalf("the cluster's file still holds the server secret")
	}
	if _, ok := file["db-servers-credential"]; !ok {
		t.Fatalf("the cluster's own secret was lost")
	}
}

// A cluster's file that cannot be read at the upgrade may hold a rotation: the value in /etc is
// not taken over it, and the file is not overwritten.
func TestServerLevelTakeoverWithAnUnreadClusterFile(t *testing.T) {
	dir := t.TempDir()
	conf := serverLevelConf(dir, "admin:first,ops:second")
	conf.ImmuableFlagMap[apiCredentialsKey] = "admin:first,ops:second"
	path := (credstore.LocalFile{DataDir: dir}).Path(credstore.ClusterScope("c1"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{Conf: conf, Logrus: log.New(), Status: ConstMonitorActif, Clusters: map[string]*cluster.Cluster{}, ClusterList: []string{"c1"}}
	repman.takeServerConfigFileCredentials()
	if n := repman.serverVersionStore().Count(apiCredentialsKey); n != 0 {
		t.Fatalf("the value in /etc was recorded over an unread cluster file: %d versions", n)
	}
	globals := &cluster.ServerGlobals{ServerSecrets: repman.serverSecretLevel(), AdoptServerVersions: repman.adoptServerVersionsFromCluster}
	cl := &cluster.Cluster{Name: "c1", Conf: serverLevelConf(dir, "admin:first,ops:second"), ServerGlobals: globals,
		APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType(), Roles: config.GetRoleType(), SecurityLogrus: log.New(), Logrus: log.New()}
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: cl.Conf.GetEncryptedString("root:db")}})
	cl.CredentialChain().Refresh()
	cl.MarkSecretVersionStoreDirty()
	cl.ReconcileSecretVersionStore()
	if raw, _ := os.ReadFile(path); string(raw) != "{corrupt" {
		t.Fatal("an unread cluster file was overwritten")
	}
}

// The server cannot write its file: the cluster keeps the server secrets' versions in its own,
// a rotation is never in no file.
func TestServerLevelHandOverKeptWhenTheServerFileIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	conf := serverLevelConf(dir, "admin:first,ops:second")
	before := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	local := credstore.LocalFile{DataDir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "c1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := local.Write(credstore.ClusterScope("c1"), map[string][]credstore.FileVersion{
		apiCredentialsKey: {{Version: 1, HashValue: conf.GetEncryptedString("admin:first,ops:Rotated-old"), RotatedAt: before}},
	}); err != nil {
		t.Fatal(err)
	}
	// the server's file cannot be written
	if err := os.MkdirAll(filepath.Join(dir, "secret_store.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{Conf: conf, Logrus: log.New(), Status: ConstMonitorActif, Clusters: map[string]*cluster.Cluster{}}
	globals := &cluster.ServerGlobals{ServerSecrets: repman.serverSecretLevel(), AdoptServerVersions: repman.adoptServerVersionsFromCluster}
	cl := &cluster.Cluster{Name: "c1", Conf: serverLevelConf(dir, "admin:first,ops:second"), ServerGlobals: globals,
		APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType(), Roles: config.GetRoleType(), SecurityLogrus: log.New(), Logrus: log.New()}
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: cl.Conf.GetEncryptedString("root:db")}})
	cl.CredentialChain().Refresh()
	cl.MarkSecretVersionStoreDirty()
	cl.ReconcileSecretVersionStore()
	file, err := local.Load(credstore.ClusterScope("c1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(file[apiCredentialsKey]) != 1 {
		t.Fatal("the cluster dropped the server secret's versions the server could not write")
	}
}

// Rotations and a global setting of a server secret at once: each rotation's change of the list
// is kept, none overwrites another (serverSecrets.setMu). Run with -race.
func TestServerLevelConcurrentChangesKeepEveryRotation(t *testing.T) {
	users := []string{"u1", "u2", "u3", "u4", "u5", "u6"}
	list := "admin:first"
	for _, u := range users {
		list += "," + u + ":start"
	}
	repman := newServerLevelTestManager(t, list)
	var wg sync.WaitGroup
	got := make([]string, len(users))
	for i, u := range users {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], u, "admin")
			if err != nil {
				t.Errorf("%s: %v", u, err)
				return
			}
			got[i] = res.Password
		}(i, u)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := repman.setServerSetting("admin", "/api/clusters/settings/actions/set/mail-smtp-password", "mail-smtp-password", base64.StdEncoding.EncodeToString([]byte("smtp"))); err != nil {
			t.Errorf("smtp: %v", err)
		}
	}()
	wg.Wait()
	final := repman.Conf.DecryptedAPIUserList()
	for i, u := range users {
		if got[i] == "" || !strings.Contains(final, u+":"+got[i]) {
			t.Fatalf("the rotation of %s was lost", u)
		}
	}
	if n := repman.serverVersionStore().Count(apiCredentialsKey); n != len(users) {
		t.Fatalf("%d versions of the list, want %d", n, len(users))
	}
}
