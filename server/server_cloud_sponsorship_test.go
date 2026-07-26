package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/sirupsen/logrus"
)

// newSponsorshipTestCluster builds a minimal but safe-to-exercise Cluster:
//   - ConfigManager is real (its background git-push goroutine is idle and
//     stopped via t.Cleanup) so UpdateUser/DropUser's reloadACL=true path
//     doesn't dereference a nil ConfigManager.
//   - Roles/Grants are populated the same way InitFromConf does, since
//     SetUserRoles/SetUserGrants iterate cluster.Roles/cluster.Grants (not
//     the per-user maps) to decide which flags to set.
func newSponsorshipTestCluster(t *testing.T) *cluster.Cluster {
	t.Helper()

	logger := config.NewLogrusWrapper(&config.Config{}, logrus.New())
	cm := manager.NewConfigManager(logger)
	t.Cleanup(cm.Stop)

	cl := &cluster.Cluster{
		Name:          "test",
		WorkingDir:    t.TempDir(),
		Conf:          &config.Config{Secrets: map[string]config.Secret{}},
		ConfigManager: cm,
		APIUsers:      map[string]cluster.APIUser{},
		Roles:         config.GetRoleType(),
		Grants:        config.GetGrantType(),
	}
	return cl
}

// addUserWithRole registers username with the given role through the same
// credentials+ACL-string path both AcceptSubscription and UpdateUser(...,
// reloadACL=true) read and rewrite. UpdateUser/DropUser call
// cl.LoadAPIUsers() whenever reloadACL is true, which rebuilds cl.APIUsers
// wholesale from cl.Conf.Secrets/ACL strings — so a user injected directly
// into cl.APIUsers without this backing would be silently dropped by that
// reload.
func addUserWithRole(t *testing.T, cl *cluster.Cluster, username, role string) {
	t.Helper()
	cl.Conf.Secrets["api-credentials"] = config.Secret{Value: username + ":password"}
	cl.Conf.APIUsersACLAllowExternal = username + "::" + cl.Name + ":" + role
	if err := cl.LoadAPIUsers(); err != nil {
		t.Fatalf("LoadAPIUsers: %v", err)
	}
	if !cl.APIUsers[username].Roles[role] {
		t.Fatalf("test setup failed: %s does not have the %q role", username, role)
	}
}

func addPendingUser(t *testing.T, cl *cluster.Cluster, username string) {
	t.Helper()
	addUserWithRole(t, cl, username, config.RolePending)
}

func addSponsorUser(t *testing.T, cl *cluster.Cluster, username string) {
	t.Helper()
	addUserWithRole(t, cl, username, config.RoleSponsor)
}

func TestAcceptSubscription_PersistsBeforeRoleMutation(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	addPendingUser(t, cl, "alice")

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.AcceptSubscription(userform, cl, "bob"); err != nil {
		t.Fatalf("AcceptSubscription: %v", err)
	}

	if got := cl.GetSponsorshipState().Status; got != cluster.SponsorshipStatusActive {
		t.Errorf("SponsorshipState.Status = %q, want %q", got, cluster.SponsorshipStatusActive)
	}
	if !cl.APIUsers["alice"].Roles[config.RoleSponsor] {
		t.Error("expected alice to have the sponsor role after AcceptSubscription")
	}
}

func TestAcceptSubscription_WriteFailureShortCircuitsRoleMutation(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	addPendingUser(t, cl, "alice")

	// Make WorkingDir unwritable by pointing it at a path whose parent is a
	// regular file, so the sponsorship-state.json MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cl.WorkingDir = filepath.Join(blocker, "sub")

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.AcceptSubscription(userform, cl, "bob"); err == nil {
		t.Fatal("expected AcceptSubscription to fail when sponsorship state write fails")
	}

	if got := cl.APIUsers["alice"].Roles[config.RoleSponsor]; got {
		t.Error("role mutation ran despite the authoritative write failing")
	}
	if got := cl.APIUsers["alice"].Roles[config.RolePending]; !got {
		t.Error("pending role should be unchanged after a failed accept")
	}
}

func TestCancelSubscription_PersistsRejectedStatus(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	addPendingUser(t, cl, "alice")

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.CancelSubscription(userform, cl, "bob"); err != nil {
		t.Fatalf("CancelSubscription: %v", err)
	}

	if got := cl.GetSponsorshipState().Status; got != cluster.SponsorshipStatusRejected {
		t.Errorf("SponsorshipState.Status = %q, want %q", got, cluster.SponsorshipStatusRejected)
	}
}

// TestCancelSubscription_PreconditionGatesBeforeStateWrite ensures an already
// active sponsor (or any non-pending user) cannot be pushed into
// SponsorshipStatusRejected by calling CancelSubscription/refuse-subscription
// out of order — reject only applies to a still-pending request.
func TestCancelSubscription_PreconditionGatesBeforeStateWrite(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	addSponsorUser(t, cl, "alice")

	before := cl.GetSponsorshipState()

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.CancelSubscription(userform, cl, "bob"); err == nil {
		t.Fatal("expected CancelSubscription to fail for a user without the pending role")
	}

	if after := cl.GetSponsorshipState(); after.Status != before.Status {
		t.Errorf("sponsorship state was mutated despite the precondition failing: before=%q after=%q", before.Status, after.Status)
	}
	if got := cl.APIUsers["alice"].Roles[config.RoleSponsor]; !got {
		t.Error("sponsor role mutation ran despite the precondition failing")
	}
}

func TestEndSubscription_PersistsEndedStatus(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	addSponsorUser(t, cl, "alice")

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.EndSubscription(userform, cl, "bob"); err != nil {
		t.Fatalf("EndSubscription: %v", err)
	}

	if got := cl.GetSponsorshipState().Status; got != cluster.SponsorshipStatusEnded {
		t.Errorf("SponsorshipState.Status = %q, want %q", got, cluster.SponsorshipStatusEnded)
	}
	if !cl.APIUsers["alice"].Roles[config.RoleUnsubscribed] {
		t.Error("expected alice to have the unsubscribed role after EndSubscription")
	}
}

func TestEndSubscription_PreconditionStillGatesBeforeStateWrite(t *testing.T) {
	repman := &ReplicationManager{}
	cl := newSponsorshipTestCluster(t)
	// alice exists but has no sponsor role.
	cl.APIUsers["alice"] = cluster.APIUser{User: "alice", Roles: map[string]bool{}, Grants: map[string]bool{}}

	before := cl.GetSponsorshipState()

	userform := cluster.UserForm{Username: "alice"}
	if err := repman.EndSubscription(userform, cl, "bob"); err == nil {
		t.Fatal("expected EndSubscription to fail for a user without the sponsor role")
	}

	if after := cl.GetSponsorshipState(); after.Status != before.Status {
		t.Errorf("sponsorship state was mutated despite the precondition failing: before=%q after=%q", before.Status, after.Status)
	}
}

// TestClusterUpdateUser_ReturnsErrorForMissingUser and
// TestClusterDropUser_ReturnsErrorForMissingUser document the one real
// failure mode of cluster.UpdateUser/DropUser (the core side effects
// AcceptSubscription/CancelSubscription/EndSubscription now explicitly
// check): a nonexistent user. This is the contract
// TestSponsorshipHandlers_CoreSideEffectErrorsAreNotDiscarded relies on being
// meaningful.
func TestClusterUpdateUser_ReturnsErrorForMissingUser(t *testing.T) {
	cl := newSponsorshipTestCluster(t)
	if err := cl.UpdateUser(cluster.UserForm{Username: "ghost"}, "admin", false); err == nil {
		t.Fatal("expected UpdateUser to return an error for a user that does not exist")
	}
}

func TestClusterDropUser_ReturnsErrorForMissingUser(t *testing.T) {
	cl := newSponsorshipTestCluster(t)
	if err := cl.DropUser(cluster.UserForm{Username: "ghost"}, false); err == nil {
		t.Fatal("expected DropUser to return an error for a user that does not exist")
	}
}

// TestSponsorshipHandlers_CoreSideEffectErrorsAreNotDiscarded is a regression
// guard for the non-breaking failure model: core side effects (main subject
// user role/ACL mutation, API user reload after ACL rewrite) must be
// explicitly handled and surfaced, never called as bare unchecked
// statements. It looks for the specific unchecked call shape (the call as
// the last token on its line) rather than depending on exact indentation.
func TestSponsorshipHandlers_CoreSideEffectErrorsAreNotDiscarded(t *testing.T) {
	data, err := os.ReadFile("server_cloud.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	src := string(data)

	bareCallEndings := []string{
		"cl.LoadAPIUsers()\n",
		"cl.DropUser(userform, true)\n",
		"cl.UpdateUser(userform, \"admin\", true)\n",
	}
	for _, ending := range bareCallEndings {
		if strings.Contains(src, ending) {
			t.Errorf("server_cloud.go appears to call %q without checking its error", strings.TrimSuffix(ending, "\n"))
		}
	}
}

// TestSponsorshipHandlers_AncillarySyncErrorsAreNotDiscarded is a regression
// guard for AcceptSubscription's external sysops/dbops sync: these are
// ancillary (not the main subject user), so a failure must be logged as
// degraded reconciliation rather than failing the sponsorship acceptance —
// but it still must not be silently discarded as a bare unchecked call.
func TestSponsorshipHandlers_AncillarySyncErrorsAreNotDiscarded(t *testing.T) {
	data, err := os.ReadFile("server_cloud.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	src := string(data)

	bareCallEndings := []string{
		"cl.AddUser(esys, cl.Conf.Cloud18GitUser, false)\n",
		"cl.UpdateUser(esys, cl.Conf.Cloud18GitUser, false)\n",
		"cl.AddUser(edbops, cl.Conf.Cloud18GitUser, false)\n",
		"cl.UpdateUser(edbops, cl.Conf.Cloud18GitUser, false)\n",
	}
	for _, ending := range bareCallEndings {
		if strings.Contains(src, ending) {
			t.Errorf("server_cloud.go appears to call %q without checking its error", strings.TrimSuffix(ending, "\n"))
		}
	}
}

// TestSubscribeHandler_PostCommitUserSyncErrorsAreNotDiscarded is a
// regression guard for handlerMuxClusterSubscribe: the post-commit
// UpdateUser/AddUser calls after SetSponsorshipRequested must not be bare
// unchecked statements — a failure there is degraded reconciliation (logged,
// handler flow preserved), not silence.
func TestSubscribeHandler_PostCommitUserSyncErrorsAreNotDiscarded(t *testing.T) {
	data, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	src := string(data)

	bareCallEndings := []string{
		"mycluster.UpdateUser(userform, repman.Conf.Cloud18GitUser, true)\n",
		"mycluster.AddUser(userform, repman.Conf.Cloud18GitUser, true)\n",
	}
	for _, ending := range bareCallEndings {
		if strings.Contains(src, ending) {
			t.Errorf("api.go appears to call %q without checking its error", strings.TrimSuffix(ending, "\n"))
		}
	}
}

// TestSponsorshipStateFile_ImportsNoNetworkClient is a regression guard for
// "no outbound CRM runtime calls": cluster_sponsorship_state.go must not
// import net/http or any transport package, since Phase 1 is local-only.
func TestSponsorshipStateFile_ImportsNoNetworkClient(t *testing.T) {
	data, err := os.ReadFile("../cluster/cluster_sponsorship_state.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	src := string(data)
	disallowed := []string{`"net/http"`, `"net/rpc"`, `"net"`}
	for _, imp := range disallowed {
		if strings.Contains(src, imp) {
			t.Errorf("cluster_sponsorship_state.go imports %s, violating the no-outbound-CRM-calls rule", imp)
		}
	}
}
