// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

func rotateTestCluster(t *testing.T, list string) *Cluster {
	t.Helper()
	cl := secretKvTestCluster(t)
	cl.Grants = config.GetGrantType()
	cl.Roles = config.GetRoleType()
	cl.Conf.MonitoringSecretVersioning = true
	cl.Conf.APIUsers = list
	cl.Conf.DynamicFlagMap[apiCredentialsKey] = list
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show,ops:db show"
	cl.CredentialChain().Refresh()
	cl.LoadAPIUsers()
	return cl
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

// The caller gets the new password: it may rotate its own, or that of a user holding no grant
// it lacks, never a user with more rights.
func TestCanRotateFor(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first,ops:second,dev:third")
	cl.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show,ops:cluster db show,dev:db show"
	cl.LoadAPIUsers()
	if err := cl.CanRotateFor("ops", "admin"); err == nil {
		t.Fatal("ops may rotate admin, who holds more grants")
	}
	if err := cl.CanRotateFor("ops", "dev"); err != nil {
		t.Fatalf("ops on dev: %v", err)
	}
	if err := cl.CanRotateFor("dev", "dev"); err != nil {
		t.Fatalf("own password: %v", err)
	}
	if err := cl.CanRotateFor("admin", "ops"); err != nil {
		t.Fatalf("admin on ops: %v", err)
	}
}

func TestSetAPIUserPasswordRefusesASeparator(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first")
	cl.Conf.DynamicFlagMap["api-credentials-external"] = "ops:second"
	cl.CredentialChain().Refresh()
	for _, p := range []string{"", "a,b", "a:b", "a b"} {
		if err := cl.SetAPIUserPassword("ops", APIUserScopeCluster, p); err == nil {
			t.Fatalf("%q accepted", p)
		}
	}
	if err := cl.SetAPIUserPassword("admin", APIUserScopeInstance, "Valid-1"); !errors.Is(err, credstore.ErrServerSecret) {
		t.Fatalf("the instance's list changed by a cluster: %v", err)
	}
}

// A cluster's own list: one version in the cluster's chain, the other users kept.
func TestRotateClusterUserPassword(t *testing.T) {
	cl := rotateTestCluster(t, "admin:first")
	cl.Conf.DynamicFlagMap["api-credentials-external"] = "ops:second,dev:third"
	cl.CredentialChain().Refresh()
	cl.LoadAPIUsers()
	if cl.APIUserScope("ops") != APIUserScopeCluster {
		t.Fatalf("scope %q", cl.APIUserScope("ops"))
	}
	if err := cl.SetAPIUserPassword("ops", APIUserScopeCluster, "Rotated-1"); err != nil {
		t.Fatal(err)
	}
	if got := cl.Conf.GetDecryptedValue("api-credentials-external"); got != "ops:Rotated-1,dev:third" {
		t.Fatalf("list %q", got)
	}
}

// A login check reads the user list while a reload swaps it in (each password rotation reloads
// every cluster's list). Run with -race: before GetAPIUsers took the read lock, the reader raced
// the swap.
func TestAPIUsersReadDuringReload(t *testing.T) {
	cl := rotateTestCluster(t, "admin:pw-admin,ops:pw-ops")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			cl.LoadAPIUsers()
		}
	}()
	for {
		select {
		case <-done:
			if _, ok := cl.GetACLUser("ops"); !ok {
				t.Fatal("ops is gone after the reloads")
			}
			return
		default:
			if _, ok := cl.GetACLUser("admin"); !ok {
				t.Fatal("admin missing during a reload")
			}
		}
	}
}
