package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ssoRequest builds a request carrying a login JWT with GitLab profile claims,
// i.e. what a peer logged in with its Cloud18 credentials presents.
func ssoRequest(t *testing.T, repman *ReplicationManager, email string) *http.Request {
	t.Helper()
	info := struct {
		Name     string
		Role     string
		Password string
		Email    string `json:"email"`
		Profile  string `json:"profile"`
	}{email, "Member", "enc", email, "https://gitlab.signal18.io"}
	tok, err := repman.issueJWT(info, "sso-token")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/add/blab", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.RemoteAddr = "10.0.0.9:1234"
	return r
}

func localRequest(t *testing.T, repman *ReplicationManager, user string) *http.Request {
	t.Helper()
	info := struct {
		Name     string
		Role     string
		Password string
	}{user, "Member", "enc"}
	tok, err := repman.issueJWT(info, "")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/add/blab", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

func TestClusterAddAuthorize(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	repman.Conf.TokenTimeout = 1
	repman.Conf.OAuthProvider = "https://gitlab.signal18.io"
	repman.initKeys()

	// Local account without cluster-create: refused, no self-service for locals.
	_, sso, self, status, reason := repman.clusterAddAuthorize(localRequest(t, repman, "bob"))
	if status != http.StatusForbidden || sso || self {
		t.Errorf("bob (db-show) must be refused: status=%d sso=%v self=%v %s", status, sso, self, reason)
	}
	// Local account with cluster-create: allowed, ordinary path.
	dave := cl.APIUsers["bob"]
	dave.User = "dave"
	cl.SetUserGrants(&dave, config.GrantClusterCreate)
	cl.APIUsers["dave"] = dave
	id, sso, self, status, _ := repman.clusterAddAuthorize(localRequest(t, repman, "dave"))
	if status != 0 || self || sso || id != "dave" {
		t.Errorf("dave (cluster-create) must pass the ordinary path: id=%s status=%d self=%v", id, status, self)
	}
	// SSO identity unknown here, self-service off: refused with the reason.
	_, sso, self, status, reason = repman.clusterAddAuthorize(ssoRequest(t, repman, "carol@example.com"))
	if status != http.StatusForbidden || !sso || self || !contains(reason, "disabled") {
		t.Errorf("carol with self-service off must be refused: status=%d sso=%v self=%v %s", status, sso, self, reason)
	}
	// Self-service on: carol goes through it.
	repman.Conf.Cloud18 = true
	repman.Conf.Cloud18SelfServiceClusters = true
	repman.Conf.Cloud18SelfServiceMaxClustersPerUser = 1
	repman.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	id, sso, self, status, _ = repman.clusterAddAuthorize(ssoRequest(t, repman, "carol@example.com"))
	if status != 0 || !sso || !self || id != "carol@example.com" {
		t.Errorf("carol must be allowed through self-service: id=%s status=%d sso=%v self=%v", id, status, sso, self)
	}
	// She becomes the sponsor of the cluster, then hits the limit.
	if err := repman.attachSelfServiceSponsor(cl, "carol@example.com"); err != nil {
		t.Fatal(err)
	}
	u := cl.APIUsers["carol@example.com"]
	if !u.Roles[config.RoleSponsor] || !u.Grants[config.GrantClusterSettings] || !u.Grants[config.GrantClusterDelete] || u.Grants[config.GrantClusterCreate] {
		t.Errorf("sponsor account wrong: roles=%v", u.Roles)
	}
	_, _, _, status, reason = repman.clusterAddAuthorize(ssoRequest(t, repman, "carol@example.com"))
	if status != http.StatusForbidden || !contains(reason, "limit is 1") {
		t.Errorf("second cluster must hit the limit: status=%d %s", status, reason)
	}
	// The sponsor acts on its cluster through SSO only: the oidc check passes on
	// a granted URL, is refused on one outside its grants, and a password login
	// never authenticates it.
	if !cl.IsValidACL("carol@example.com", "", "/api/clusters/c1/settings/actions/set/prov-db-docker-img/mariadb:lts", "oidc") {
		t.Error("sponsor must pass the oidc ACL on cluster settings")
	}
	if !cl.IsValidACL("carol@example.com", "", "/api/clusters/c1/services/actions/provision", "oidc") {
		t.Error("sponsor must be able to provision its cluster")
	}
	if cl.IsValidACL("carol@example.com", "", "/api/clusters/c1/actions/switchover", "oidc") {
		t.Error("sponsor must not get grants outside the self-service set")
	}
	if cl.IsValidACL("carol@example.com", "", "/api/clusters/c1/settings/actions/set/prov-db-docker-img/mariadb:lts", "password") {
		t.Error("a passwordless sponsor must never authenticate with a password")
	}
	// Re-attaching an identity that already manages the cluster is a no-op.
	before := cl.Conf.APIUsersACLAllowExternal
	if err := repman.attachSelfServiceSponsor(cl, "carol@example.com"); err != nil || cl.Conf.APIUsersACLAllowExternal != before {
		t.Errorf("re-attach must not change the ACL: err=%v", err)
	}
	// An identity already present as a default visitor (what a new cluster gives
	// an SSO creator today) is promoted to sponsor, a local account is not.
	cl.APIUsers["frank@example.com"] = cluster.APIUser{User: "frank@example.com", Grants: map[string]bool{}, Roles: map[string]bool{config.RoleVisitor: true}}
	if err := repman.attachSelfServiceSponsor(cl, "frank@example.com"); err != nil {
		t.Fatal(err)
	}
	if f := cl.APIUsers["frank@example.com"]; !f.Roles[config.RoleSponsor] || !f.Grants[config.GrantClusterSettings] || f.Password != "" {
		t.Errorf("visitor must become a passwordless sponsor: %+v", f)
	}
	// (fixture users set directly in the map are gone after LoadAPIUsers, so use a
	// persisted local account with a password here)
	if err := cl.AddUser(cluster.UserForm{Username: "local1", Grants: "db-show"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	cl.LoadAPIUsers()
	if cl.APIUsers["local1"].Password == "" {
		t.Fatal("local1 must have a generated password")
	}
	if err := repman.attachSelfServiceSponsor(cl, "local1"); err == nil {
		t.Error("a local account must not be taken over by an SSO identity")
	}
	// Another SSO identity is not affected by carol's usage.
	_, _, self, status, _ = repman.clusterAddAuthorize(ssoRequest(t, repman, "erin@example.com"))
	if status != 0 || !self {
		t.Errorf("erin must still be allowed: status=%d self=%v", status, self)
	}
}

// The ResourceManager pool gates self-service: a new cluster needs 2 × prov-db-dbu
// and prov-service-plan-apu free in the infrastructure pool (capacity × quota − Σ plans).
func TestSelfServicePoolGate(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	repman.Conf.Cloud18 = true
	repman.Conf.Cloud18SelfServiceClusters = true
	repman.Conf.Cloud18SelfServiceMaxClustersPerUser = 3
	repman.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	repman.Conf.ProvDbDbu = 1
	repman.Conf.ProvServicePlanApu = 1
	// No capacity known: the pool cannot gate.
	if err := repman.selfServiceCheck("u@x.io"); err != nil {
		t.Fatalf("unknown pool must not gate: %v", err)
	}
	st := repman.selfServiceStatusFor("u@x.io")
	if st.Pool.Known || !st.PoolOK || st.PoolNote == "" {
		t.Errorf("status must say the pool is unknown: %+v", st)
	}
	// 3 cores / 12 GB declared: 3 DBU (1c/4GB) and 3 APU (1c/1GB, memory not binding).
	repman.resourceManager = cluster.NewResourceManager()
	repman.Conf.ResourceManagerInfraCpuCores = 3
	repman.Conf.ResourceManagerInfraMemoryMB = 12288
	pool := repman.infraUnitPool()
	if !pool.Known || pool.UsableDbu != 3 || pool.UsableApu != 3 {
		t.Fatalf("pool must be 3 DBU / 3 APU: %+v", pool)
	}
	if err := repman.selfServiceCheck("u@x.io"); err != nil {
		t.Errorf("2 DBU needed of 3 free must pass: %v", err)
	}
	// A cluster already planned at 2 DBU leaves 1 free: refused (an unprovisioned
	// cluster reserves nothing, so the fixture must be provisioned to count).
	cl.IsProvision = true
	cl.Conf.ProvServicePlanDbu = 2
	if err := repman.selfServiceCheck("u@x.io"); err == nil || !contains(err.Error(), "no free DBU") {
		t.Errorf("1 DBU free must refuse a 2 DBU cluster, got %v", err)
	}
	st = repman.selfServiceStatusFor("u@x.io")
	if st.Enabled || st.PoolOK || st.Pool.FreeDbu != 1 || st.NeededDBU != 2 {
		t.Errorf("status must expose the refusal and the pool: %+v", st)
	}
	// Quota halves the usable pool.
	cl.Conf.ProvServicePlanDbu = 0
	repman.resourceManager.SetQuotaPct(50)
	if err := repman.selfServiceCheck("u@x.io"); err == nil || !contains(err.Error(), "no free DBU") {
		t.Errorf("1.5 usable DBU must refuse a 2 DBU cluster, got %v", err)
	}
	// APU gate: plenty of DBU, no APU left.
	repman.resourceManager.SetQuotaPct(0)
	repman.Conf.ProvServicePlanApu = 4
	if err := repman.selfServiceCheck("u@x.io"); err == nil || !contains(err.Error(), "no free APU") {
		t.Errorf("4 APU needed of 3 must refuse, got %v", err)
	}
}

// cloud18-self-service-clusters-can-borrow: a pool that cannot guarantee the default units
// asks the over-commit pot; the creation goes through on borrowed capacity, said in the note.
func TestSelfServiceCanBorrow(t *testing.T) {
	repman, _ := newTokenTestManager(t)
	repman.Conf.Cloud18 = true
	repman.Conf.Cloud18SelfServiceClusters = true
	repman.Conf.Cloud18SelfServiceMaxClustersPerUser = 3
	repman.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	repman.Conf.ProvDbDbu = 1
	repman.Conf.ProvServicePlanApu = 1
	repman.resourceManager = cluster.NewResourceManager()
	repman.Conf.ResourceManagerInfraCpuCores = 3
	repman.Conf.ResourceManagerInfraMemoryMB = 12288
	repman.resourceManager.SetInfraCapacity(&cluster.AgentCapacity{Cores: 3, MemMB: 12288, DiskGB: 1000, Iops: 10000})
	// Quota 50 %: 1.5 DBU sellable, 2 needed -> refused without the flag.
	repman.resourceManager.SetQuotaPct(50)
	if err := repman.selfServiceCheck("u@x.io"); err == nil || !contains(err.Error(), "no free DBU") {
		t.Fatalf("1.5 usable DBU must refuse a 2 DBU cluster, got %v", err)
	}
	// With the flag the over-commit pot (3 cores, nothing borrowed) lends the 2 DBU + 1 APU.
	repman.Conf.Cloud18SelfServiceClustersCanBorrow = true
	if err := repman.selfServiceCheck("u@x.io"); err != nil {
		t.Fatalf("the borrow pot must admit the cluster: %v", err)
	}
	st := repman.selfServiceStatusFor("u@x.io")
	if !st.Enabled || !st.PoolOK || !st.Borrowed || !contains(st.PoolNote, "borrowed capacity") {
		t.Fatalf("status must say the cluster is created on borrowed capacity: %+v", st)
	}
	// A pot that cannot lend either still refuses: 4 DBU needed of 3 cores.
	repman.Conf.ProvDbDbu = 2
	if err := repman.selfServiceCheck("u@x.io"); err == nil || !contains(err.Error(), "cannot borrow either") {
		t.Fatalf("4 DBU over 3 cores must refuse even with the flag, got %v", err)
	}
}

// cloud18-self-service-clusters-enabled-script: a non-zero exit vetoes with the first output
// line as reason, the pool figures ride env, an empty script allows.
func TestSelfServiceEnabledScript(t *testing.T) {
	repman, _ := newTokenTestManager(t)
	repman.Conf.Cloud18 = true
	repman.Conf.Cloud18SelfServiceClusters = true
	repman.Conf.Cloud18SelfServiceMaxClustersPerUser = 3
	repman.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	repman.Conf.ProvDbDbu = 1
	repman.Conf.ProvServicePlanApu = 1
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	veto := filepath.Join(dir, "veto.sh")
	if err := os.WriteFile(veto, []byte("#!/bin/sh\necho \"no new cluster for $1 on $2\"\necho more\nenv | grep ^REPMAN_ > "+envFile+"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repman.Conf.Cloud18SelfServiceClustersEnabledScript = veto
	err := repman.selfServiceCheck("u@x.io")
	if err == nil || !contains(err.Error(), "no new cluster for u@x.io on opensvc") || contains(err.Error(), "more") {
		t.Fatalf("the veto must carry the first output line only, got %v", err)
	}
	env, _ := os.ReadFile(envFile)
	for _, want := range []string{"REPMAN_IDENTITY=u@x.io", "REPMAN_ORCHESTRATOR=opensvc", "REPMAN_SPONSORED_CLUSTERS=0", "REPMAN_NEEDED_DBU=2.00", "REPMAN_NEEDED_APU=1.00", "REPMAN_FREE_DBU=", "REPMAN_BORROW_APU="} {
		if !contains(string(env), want) {
			t.Errorf("script env must carry %s, got:\n%s", want, env)
		}
	}
	st := repman.selfServiceStatusFor("u@x.io")
	if st.Enabled || !contains(st.Reason, "enabled-script refused") {
		t.Fatalf("status must expose the veto: %+v", st)
	}
	allow := filepath.Join(dir, "allow.sh")
	if err := os.WriteFile(allow, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repman.Conf.Cloud18SelfServiceClustersEnabledScript = allow
	if err := repman.selfServiceCheck("u@x.io"); err != nil {
		t.Fatalf("an allowing script must pass: %v", err)
	}
	repman.Conf.Cloud18SelfServiceClustersEnabledScript = ""
	if err := repman.selfServiceCheck("u@x.io"); err != nil {
		t.Fatalf("no script must pass: %v", err)
	}
}

// A self-service cluster is born dynamic on OpenSVC (PG slice: run-args cap off) and on
// Kubernetes (in-place Pod resize: requests/limits pair on); other orchestrators untouched.
func TestSelfServiceBornDynamic(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cl.Conf.ProvDBDockerRunArgsLimit = true
	repman.selfServiceBornDynamic(cl)
	if !cl.Conf.ProvDBApplyDynamicConfig || !cl.Conf.ProvDBDynamicResource || cl.Conf.ProvDBDockerRunArgsLimit {
		t.Fatalf("OpenSVC: dynamic on, run-args cap off, got apply=%t resource=%t runargs=%t", cl.Conf.ProvDBApplyDynamicConfig, cl.Conf.ProvDBDynamicResource, cl.Conf.ProvDBDockerRunArgsLimit)
	}
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cl.Conf.ProvDBApplyDynamicConfig, cl.Conf.ProvDBDynamicResource, cl.Conf.ProvDBDockerRunArgsLimit = false, false, false
	repman.selfServiceBornDynamic(cl)
	if !cl.Conf.ProvDBApplyDynamicConfig || !cl.Conf.ProvDBDynamicResource || !cl.Conf.ProvDBDockerRunArgsLimit {
		t.Fatalf("Kubernetes: dynamic on, requests/limits pair on")
	}
	cl.Conf.ProvOrchestrator = "local"
	cl.Conf.ProvDBApplyDynamicConfig, cl.Conf.ProvDBDynamicResource = false, false
	repman.selfServiceBornDynamic(cl)
	if cl.Conf.ProvDBApplyDynamicConfig || cl.Conf.ProvDBDynamicResource {
		t.Fatalf("another orchestrator must be left as the defaults say")
	}
}

// cloud18-self-service-cache-seconds: within the TTL the status is served from the snapshot
// (a plan change is not seen until it expires), with 0 every call recomputes; the enabled
// script runs once per identity per TTL, not per request.
func TestSelfServiceStatusCache(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	repman.Conf.Cloud18 = true
	repman.Conf.Cloud18SelfServiceClusters = true
	repman.Conf.Cloud18SelfServiceMaxClustersPerUser = 3
	repman.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	repman.Conf.ProvDbDbu = 1
	repman.Conf.ProvServicePlanApu = 1
	repman.resourceManager = cluster.NewResourceManager()
	repman.Conf.ResourceManagerInfraCpuCores = 3
	repman.Conf.ResourceManagerInfraMemoryMB = 12288

	repman.Conf.Cloud18SelfServiceCacheSeconds = 0
	st := repman.selfServiceStatusFor("u@x.io")
	if !st.Pool.Known || st.Pool.FreeDbu != 3 {
		t.Fatalf("fresh pool must be 3 DBU free: %+v", st.Pool)
	}
	cl.IsProvision = true
	cl.Conf.ProvServicePlanDbu = 2
	if st = repman.selfServiceStatusFor("u@x.io"); st.Pool.FreeDbu != 1 {
		t.Fatalf("TTL 0 recomputes at every call, want 1 free, got %v", st.Pool.FreeDbu)
	}

	repman.Conf.Cloud18SelfServiceCacheSeconds = 300
	repman.selfServiceSnap = nil
	st = repman.selfServiceStatusFor("u@x.io")
	if st.Pool.FreeDbu != 1 {
		t.Fatalf("first call under TTL computes, want 1 free, got %v", st.Pool.FreeDbu)
	}
	cl.Conf.ProvServicePlanDbu = 0
	if st = repman.selfServiceStatusFor("u@x.io"); st.Pool.FreeDbu != 1 {
		t.Fatalf("within the TTL the snapshot is served, want 1 free still, got %v", st.Pool.FreeDbu)
	}
	repman.selfServiceSnap.at = time.Now().Add(-10 * time.Minute)
	if st = repman.selfServiceStatusFor("u@x.io"); st.Pool.FreeDbu != 3 {
		t.Fatalf("an expired snapshot is recomputed, want 3 free, got %v", st.Pool.FreeDbu)
	}

	// the enabled script runs once per identity within the TTL
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "enabled.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho x >> "+counter+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repman.Conf.Cloud18SelfServiceClustersEnabledScript = script
	repman.selfServiceSnap = nil
	for i := 0; i < 5; i++ {
		if st = repman.selfServiceStatusFor("u@x.io"); !st.Enabled {
			t.Fatalf("script allows: %+v", st)
		}
	}
	repman.selfServiceStatusFor("v@x.io")
	b, _ := os.ReadFile(counter)
	if n := strings.Count(string(b), "x"); n != 2 {
		t.Fatalf("script must run once per identity within the TTL (2 identities), ran %d times", n)
	}
}
