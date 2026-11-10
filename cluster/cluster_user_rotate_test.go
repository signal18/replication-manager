// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"strings"
	"testing"
)

func rotateTestCluster(t *testing.T, list string) *Cluster {
	t.Helper()
	cl := secretKvTestCluster(t)
	cl.Conf.MonitoringSecretVersioning = true
	cl.Conf.APIUsers = list
	cl.Conf.DynamicFlagMap[apiCredentialsKey] = list
	cl.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show,ops:db show"
	cl.CredentialChain().Refresh()
	cl.LoadAPIUsers()
	return cl
}

func TestListWithPassword(t *testing.T) {
	got, ok := listWithPassword("admin:a,ops:b,dev:", "ops", "N3w")
	if !ok || got != "admin:a,ops:N3w,dev:" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := listWithPassword("admin:a", "ops", "x"); ok {
		t.Fatal("an absent user was found")
	}
	if !listHasUser(" admin:a, ops:b", "ops") || listHasUser("admin:a", "adm") {
		t.Fatal("listHasUser")
	}
}

func TestGeneratePasswordLength(t *testing.T) {
	cl := &Cluster{}
	p, err := cl.GeneratePasswordLength(24)
	if err != nil || len(p) != 24 || strings.ContainsAny(p, ",:") {
		t.Fatalf("%d %v", len(p), err)
	}
	if p8, _ := cl.GeneratePassword(); len(p8) != 8 {
		t.Fatalf("GeneratePassword changed length: %d", len(p8))
	}
}

// A rotated user is refused with its old password at once (its session carries it), the
// others keep theirs, and the change is a version in repman's own store.
func TestRotateInstanceUserPassword(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first,ops:second")
	if cl.APIUserScope("ops") != APIUserScopeInstance {
		t.Fatalf("scope %q", cl.APIUserScope("ops"))
	}
	if err := cl.CanRotateAPIUserPassword("ops"); err != nil {
		t.Fatal(err)
	}
	if err := cl.SetAPIUserPassword("ops", APIUserScopeInstance, "Rotated-1"); err != nil {
		t.Fatal(err)
	}
	if got := cl.APIUsers["ops"].Password; got != "Rotated-1" {
		t.Fatalf("ops password %q", got)
	}
	if got := cl.APIUsers["admin"].Password; got != "first" {
		t.Fatalf("admin password changed: %q", got)
	}
	if v, ok := cl.versionStore().Latest(apiCredentialsKey); !ok || !strings.Contains(v.Value, "ops:Rotated-1") {
		t.Fatalf("no version: %v %v", v, ok)
	}
	if _, err := cl.GetAPIUser("ops", "second"); err == nil {
		t.Fatal("the old password still logs in")
	}
}

func TestRotateRefusals(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first,system:k,sso:")
	cl.Conf.Cloud18GitUser = "admin"
	for user, want := range map[string]string{
		"system": "derived",
		"admin":  "Cloud18",
		"sso":    "no local password",
	} {
		if err := cl.CanRotateAPIUserPassword(user); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", user, err)
		}
	}
	if err := cl.CanRotateAPIUserPassword("nobody"); !errors.Is(err, ErrAPIUserUnknown) {
		t.Fatalf("unknown user: %v", err)
	}
}

// Set in a configuration file with no store keeping versions: the one refusal (rule 9).
func TestRotateRefusedWithoutVersionsOnAFileValue(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first,ops:second")
	cl.Conf.MonitoringSecretVersioning = false
	cl.Conf.ImmuableFlagMap[apiCredentialsKey] = "admin:first,ops:second"
	if err := cl.CanRotateAPIUserPassword("ops"); err == nil {
		t.Fatal("a file value with no versioned store was accepted")
	}
}

// At the upgrade, a history recorded before the list was versioned does not win over the file.
func TestAdoptConfigFileValueOverRecordedHistory(t *testing.T) {
	cl := rotateTestCluster(t, "admin:infile")
	vs := cl.versionStore()
	vs.Insert(apiCredentialsKey, cl.selfVersion("admin:recorded"))
	cl.Conf.ImmuableFlagMap[apiCredentialsKey] = "admin:infile"
	cl.takeConfigFileCredentials()
	cl.CredentialChain().Refresh()
	if got := cl.Conf.Secrets.Get(apiCredentialsKey).Value; got != "admin:infile" {
		t.Fatalf("current %q", got)
	}
	if vs.AdoptConfigFileValue(apiCredentialsKey, "admin:infile") {
		t.Fatal("adopted twice")
	}
}
