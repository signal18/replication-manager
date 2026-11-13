// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// etcTestManager is a server whose instance list is set in a config file "under /etc" (a temp
// folder outside the working directory), with the writer of a process that stays root.
func etcTestManager(t *testing.T, writerOn bool) (*ReplicationManager, string) {
	t.Helper()
	list := "admin:first,ops:second"
	repman := newServerLevelTestManager(t, list)
	etc := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(etc, []byte("[Default]\n  api-credentials = \""+list+"\" # users\n  monitoring-datadir = \"/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repman.ConfigPathList = []string{etc, filepath.Join(repman.Conf.WorkingDir, "default.toml")}
	repman.Conf.SetImmuableFlag("api-credentials", list)
	delete(repman.Conf.DynamicFlagMap, "api-credentials")
	repman.Conf.MonitoringEtcCredentialWriter = writerOn
	repman.InitAlertStateMachine()
	repman.startEtcWriter(false, 0, 0)
	return repman, etc
}

// A ban of a user of the instance's list set in the file under /etc writes the new list there,
// the other lines kept; the next start does not take it as an operator's edit.
func TestEtcCredentialWrittenOnRotation(t *testing.T) {
	repman, etc := etcTestManager(t, true)
	if files := repman.etcConfigFiles(); len(files) != 1 || files[0] != filepath.Clean(etc) {
		t.Fatalf("files %v: the working directory's files are never written", files)
	}
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if res.EtcFile != "written" {
		t.Fatalf("etcFile %q", res.EtcFile)
	}
	b, _ := os.ReadFile(etc)
	if !strings.Contains(string(b), `api-credentials = "admin:first,ops:`+res.Password+`" # users`) || !strings.Contains(string(b), `monitoring-datadir = "/x"`) {
		t.Fatalf("file:\n%s", b)
	}
	n := repman.serverVersionStore().Count("api-credentials")
	if repman.serverVersionStore().TakeConfigFileValue("api-credentials", "admin:first,ops:"+res.Password) || repman.serverVersionStore().Count("api-credentials") != n {
		t.Fatal("the written file is taken as an operator's edit at the next start")
	}
}

// Off, a ban says the file keeps the old password and GWARN025 stays until a write succeeds.
func TestEtcCredentialWriterOff(t *testing.T) {
	repman, etc := etcTestManager(t, false)
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.EtcFile, "NOT written") {
		t.Fatalf("etcFile %q", res.EtcFile)
	}
	b, _ := os.ReadFile(etc)
	if !strings.Contains(string(b), "ops:second") {
		t.Fatal("written while off")
	}
	repman.checkEtcCredentials()
	if !repman.StateMachine.CurState.Search("GWARN025@api-credentials") {
		t.Fatal("no GWARN025")
	}
	// only the server secrets' owners switch it
	c1, c2 := repman.Clusters["c1"], repman.Clusters["c2"]
	c1.Conf.APIUsersACLAllow = serverLevelACL + ",half:cluster-server-credentials global show"
	c1.LoadAPIUsers()
	c2.Conf.APIUsersACLAllow = serverLevelACL + ",half:global show"
	c2.LoadAPIUsers()
	if err := repman.switchServerSetting("half", "", "monitoring-etc-credential-writer", ""); err == nil {
		t.Fatal("switched without the server credentials grant on every cluster")
	}
}

// The key comes from a configuration file, but none the writer may write sets it as a one-line
// string in [default] (here a cluster section only): never a silent success.
func TestEtcCredentialNotFoundIsSaid(t *testing.T) {
	repman, etc := etcTestManager(t, true)
	if err := os.WriteFile(etc, []byte("[Default]\n  monitoring-datadir = \"/x\"\n[c1]\n  api-credentials = \"admin:first,ops:second\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := repman.rotateAPIUserPassword(repman.Clusters["c1"], "ops", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.EtcFile, "NOT written") || !strings.Contains(res.EtcFile, "not found") {
		t.Fatalf("etcFile %q", res.EtcFile)
	}
}
