package cluster

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"k8s.io/client-go/kubernetes/fake"
)

func newTestCluster(name string) *Cluster {
	return &Cluster{Name: name, Conf: &config.Config{}}
}

// Embeds a nil DatabaseProxy so only GetName/GetPort need overriding.
type fakeProxy struct {
	DatabaseProxy
	name string
	port string
}

func (f *fakeProxy) GetName() string { return f.name }
func (f *fakeProxy) GetPort() string { return f.port }

// --- K8SGetNodes / node discovery safety ---

func TestK8SAgentFromNode_NoAddresses(t *testing.T) {
	n := apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: apiv1.NodeStatus{
			Addresses: nil, // node reporting no addresses must not panic
			Capacity: apiv1.ResourceList{
				apiv1.ResourceCPU:    resource.MustParse("2"),
				apiv1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocatable: apiv1.ResourceList{
				apiv1.ResourceMemory: resource.MustParse("3Gi"),
			},
		},
	}

	agent, address, hostnameLabel := k8sAgentFromNode(n)
	if address != "" {
		t.Fatalf("expected empty address for node with no reported addresses, got %q", address)
	}
	if agent.HostName != "node-1" {
		t.Fatalf("expected HostName to be preserved as the Kubernetes node name, got %q", agent.HostName)
	}
	if agent.CpuCores != 2 {
		t.Fatalf("expected 2 CPU cores, got %d", agent.CpuCores)
	}
	if hostnameLabel != "" {
		t.Fatalf("expected empty hostname label when the node has no labels, got %q", hostnameLabel)
	}
}

func TestK8SAgentFromNode_WithAddress(t *testing.T) {
	n := apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-2",
			Labels: map[string]string{"kubernetes.io/hostname": "node-2-label"},
		},
		Status: apiv1.NodeStatus{
			Addresses: []apiv1.NodeAddress{{Type: apiv1.NodeInternalIP, Address: "10.0.0.5"}},
		},
	}
	_, address, hostnameLabel := k8sAgentFromNode(n)
	if address != "10.0.0.5" {
		t.Fatalf("expected address 10.0.0.5, got %q", address)
	}
	if hostnameLabel != "node-2-label" {
		t.Fatalf("expected hostname label node-2-label, got %q", hostnameLabel)
	}
}

func TestK8SNodesFromClient_ListError(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	cluster := newTestCluster("k8stest")
	agents, err := cluster.k8sNodesFromClient(client)
	if err == nil {
		t.Fatal("expected error to propagate from a failed node List(), got nil")
	}
	if agents != nil {
		t.Fatalf("expected nil agents on list error, got %v", agents)
	}
}

func TestK8SNodesFromClient_Success(t *testing.T) {
	client := fake.NewSimpleClientset(&apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
	})
	cluster := newTestCluster("k8stest")
	agents, err := cluster.k8sNodesFromClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(agents) != 1 || agents[0].HostName != "node-a" {
		t.Fatalf("expected one agent named node-a, got %v", agents)
	}
}

// --- Node hostname-label resolution for NodeSelector ---
//
// k8sHostnameLabel reads a cache populated by k8sNodesFromClient's nodes/list
// call, not a per-node nodes/get — a least-privilege RBAC setup that grants
// nodes/list but not nodes/get still resolves the label correctly.

func TestK8SHostnameLabel_LabelDiffersFromNodeName(t *testing.T) {
	client := fake.NewSimpleClientset(&apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-a.internal",
			Labels: map[string]string{"kubernetes.io/hostname": "node-a"},
		},
	})
	cluster := newTestCluster("k8stest")
	if _, err := cluster.k8sNodesFromClient(client); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	got := cluster.k8sHostnameLabel("node-a.internal")
	if got != "node-a" {
		t.Fatalf("expected the actual hostname label value %q, got %q", "node-a", got)
	}
}

func TestK8SHostnameLabel_MissingLabelFallsBackToNodeName(t *testing.T) {
	client := fake.NewSimpleClientset(&apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
	})
	cluster := newTestCluster("k8stest")
	if _, err := cluster.k8sNodesFromClient(client); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	got := cluster.k8sHostnameLabel("node-a")
	if got != "node-a" {
		t.Fatalf("expected fallback to node name %q, got %q", "node-a", got)
	}
}

func TestK8SHostnameLabel_UncachedNodeFallsBackToNodeName(t *testing.T) {
	cluster := newTestCluster("k8stest")
	got := cluster.k8sHostnameLabel("node-a")
	if got != "node-a" {
		t.Fatalf("expected fallback to node name %q when nothing has been cached, got %q", "node-a", got)
	}
}

// --- Proxy naming uniqueness ---

func TestK8SProxyDeploymentName_UniquePerProxy(t *testing.T) {
	n1 := k8sProxyDeploymentName("mycluster", "proxysql1")
	n2 := k8sProxyDeploymentName("mycluster", "proxysql2")
	if n1 == n2 {
		t.Fatalf("expected distinct deployment names for distinct proxies, got %q for both", n1)
	}
}

// --- Proxy provisioning ---

func TestK8SProvisionProxy_InvalidPort(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "not-a-port"}

	err := cluster.k8sProvisionProxyServiceWithClient(client, prx)
	if err == nil {
		t.Fatal("expected an explicit error for an invalid proxy port, not a silent port-0 deployment")
	}
}

func TestK8SProvisionProxy_AlreadyExistsIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6033"}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("first provision: unexpected error: %s", err)
	}
	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("second provision (AlreadyExists) should be idempotent, got error: %s", err)
	}
}

func TestK8SProvisionProxy_TwoProxiesDoNotCollide(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prxA := &fakeProxy{name: "proxysql1", port: "6033"}
	prxB := &fakeProxy{name: "proxysql2", port: "6034"}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prxA); err != nil {
		t.Fatalf("provision proxysql1: unexpected error: %s", err)
	}
	if err := cluster.k8sProvisionProxyServiceWithClient(client, prxB); err != nil {
		t.Fatalf("provision proxysql2: unexpected error (name/selector collision?): %s", err)
	}

	list, err := client.AppsV1().Deployments("k8stest").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error listing deployments: %s", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 distinct proxy deployments, got %d", len(list.Items))
	}
}

func TestK8SUnprovisionProxy_DeletesDeployment(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6033"}
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: k8sProxyDeploymentName("k8stest", "proxysql1"), Namespace: "k8stest"},
	})

	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	_, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), k8sProxyDeploymentName("k8stest", "proxysql1"), metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected deployment to be deleted")
	}
}

func TestK8SUnprovisionProxy_NotFoundIsIdempotent(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "does-not-exist", port: "6033"}
	client := fake.NewSimpleClientset()

	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("expected repeated/already-gone unprovision to succeed idempotently, got error: %s", err)
	}
}

// --- Database Deployment builder ---
//
// k8sDatabaseDeployment is a pure builder (no API calls, no ServerMonitor
// methods invoked), so the placement fix — NodeSelector, not NodeName,
// which is what actually made WaitForFirstConsumer PVC binding work — can
// be asserted directly without a fake clientset or a live cluster.

func TestK8SDatabaseDeployment_UsesNodeSelectorNotNodeName(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbImg = "mariadb:10.11"
	s := &ServerMonitor{Name: "db1", Port: "3306", Pass: "secret"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	if dep.Spec.Template.Spec.NodeName != "" {
		t.Fatalf("expected NodeName to be unset (placement must go through NodeSelector, not NodeName, or WaitForFirstConsumer PVC binding breaks again), got %q", dep.Spec.Template.Spec.NodeName)
	}
	got := dep.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]
	if got != "node-a" {
		t.Fatalf("expected NodeSelector kubernetes.io/hostname=node-a, got %q", got)
	}
}

func TestK8SDatabaseDeployment_NodeSelectorTracksHostnameLabelArgument(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a-label")

	got := dep.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]
	if got != "node-a-label" {
		t.Fatalf("expected NodeSelector to use the passed-in hostname label, got %q", got)
	}
}

// --- Namespace ensure ---

func TestK8SEnsureNamespace_AlreadyExistsDoesNotPanic(t *testing.T) {
	client := fake.NewSimpleClientset(&apiv1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "k8stest"},
	})
	cluster := newTestCluster("k8stest")
	cluster.k8sEnsureNamespace(client, "k8stest")
}

func TestK8SEnsureNamespace_ForbiddenDoesNotBlockProvisioning(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "namespaces", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(apiv1.Resource("namespaces"), "k8stest", context.DeadlineExceeded)
	})

	cluster := newTestCluster("k8stest")
	cluster.k8sEnsureNamespace(client, "k8stest")
}

// --- Legacy proxy deployment: left alone, only warned about ---

func TestK8SProvisionProxy_DoesNotTouchLegacyDeployment(t *testing.T) {
	legacyName := k8sLegacyProxyDeploymentName("k8stest")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: legacyName, Namespace: "k8stest"},
	})
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6033"}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), legacyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected the legacy deployment to be left alone by provisioning a different, unrelated proxy: %s", err)
	}
}

func TestK8SUnprovisionProxy_DoesNotTouchLegacyDeployment(t *testing.T) {
	legacyName := k8sLegacyProxyDeploymentName("k8stest")
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: k8sProxyDeploymentName("k8stest", "proxysql1"), Namespace: "k8stest"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: legacyName, Namespace: "k8stest"}},
	)
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6033"}

	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), legacyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected unprovisioning proxysql1 to leave a cluster-wide legacy deployment alone (it may belong to a different, not-yet-migrated proxy): %s", err)
	}
}

func TestK8SWarnIfLegacyProxyDeployment_AbsentIsSilentNoOp(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)
}

// --- Database start: verify state instead of blindly reporting success ---

func TestK8SStartDatabase_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment to start does not exist, not a silent success")
	}
}

func TestK8SStartDatabase_ScaledToZeroIsError(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	})
	cluster := newTestCluster("k8stest")
	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment is scaled to zero, not a silent success")
	}
}

func TestK8SStartDatabase_GenuineGetErrorPropagates(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(appsv1.Resource("deployments"), "db1", context.DeadlineExceeded)
	})
	cluster := newTestCluster("k8stest")
	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected a genuine (non-NotFound) Get error to propagate")
	}
}

func TestK8SStartDatabase_RunningDeploymentSucceeds(t *testing.T) {
	one := int32(1)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	})
	cluster := newTestCluster("k8stest")
	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

// --- Database unprovisioning ---

func TestK8SUnprovisionDatabase_DeletesDeploymentAndService(t *testing.T) {
	cluster := newTestCluster("k8stest")
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"}},
		&apiv1.Service{ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"}},
	)

	if err := cluster.k8sUnprovisionDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestK8SUnprovisionDatabase_NotFoundIsIdempotent(t *testing.T) {
	cluster := newTestCluster("k8stest")
	client := fake.NewSimpleClientset()

	if err := cluster.k8sUnprovisionDatabaseServiceWithClient(client, "does-not-exist"); err != nil {
		t.Fatalf("expected repeated/already-gone unprovision to succeed idempotently, got error: %s", err)
	}
}

func TestK8SUnprovisionDatabase_GenuineDeleteFailurePropagates(t *testing.T) {
	cluster := newTestCluster("k8stest")
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"}},
	)
	client.PrependReactor("delete", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	err := cluster.k8sUnprovisionDatabaseServiceWithClient(client, "db1")
	if err == nil {
		t.Fatal("expected the genuine deployment-delete failure to be reported, not swallowed")
	}
}

// --- Container port ---
//
// No HostPort: reachability comes from the headless-service DNS record.

func TestK8SDatabaseDeployment_NoHostPortBinding(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	ports := dep.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 1 {
		t.Fatalf("expected exactly one container port, got %d", len(ports))
	}
	if ports[0].HostPort != 0 {
		t.Fatalf("expected no HostPort binding (DNS is the only reachability path now), got %d", ports[0].HostPort)
	}
	if ports[0].ContainerPort != 3306 {
		t.Fatalf("expected ContainerPort to track the port argument, got %d", ports[0].ContainerPort)
	}
}

// --- Config-bootstrap Basic Auth (init container) ---
//
// Credentials are sent as a base64 Authorization header for the "admin"
// user -- the same fixed-account convention every other bootstrap
// credential injection in this codebase uses (GetExecEnv, OpenSVC's
// secrets injection, onpremise env exports), not "whichever api-credentials
// entry is configured first" (which may lack the grant /config requires).
// Only sent when api-credentials-secure-config actually requires it.

func TestK8SDatabaseDeployment_ConfigFetchUsesBasicAuthHeader(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if strings.Contains(cmd, "admin:s3cr3t") || strings.Contains(cmd, "admin:s3cr3t@") {
		t.Fatalf("expected credentials to never appear in cleartext in the init container command, got: %s", cmd)
	}
	wantHeader := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("admin:s3cr3t"))
	if !strings.Contains(cmd, wantHeader) {
		t.Fatalf("expected the base64-encoded Basic Auth header %q in the init container command, got: %s", wantHeader, cmd)
	}
}

// A credential with shell metacharacters must not change what the shell
// actually executes.
func TestK8SDatabaseDeployment_ConfigFetchAuthSafeWithShellMetacharacters(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: `p"$(rm -rf /)"\`}}
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	for _, meta := range []string{"$(", "`", "\"$", ";rm"} {
		if strings.Contains(cmd, meta) {
			t.Fatalf("expected no raw shell metacharacter sequence %q from the credential to reach the command, got: %s", meta, cmd)
		}
	}
}

// No admin user configured falls back to the documented default password
// "repman" -- matching api-credentials' own CLI default ("admin:repman"),
// never silently skipping auth when secure-config requires it.
func TestK8SDatabaseDeployment_ConfigFetchFallsBackToDefaultAdminPassword(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantHeader := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("admin:repman"))
	if !strings.Contains(cmd, wantHeader) {
		t.Fatalf("expected the default admin:repman header %q, got: %s", wantHeader, cmd)
	}
}

// A non-admin api-credentials entry, even a differently-privileged one
// configured alongside admin, must never end up in the bootstrap header --
// only the admin user's own password is ever used.
func TestK8SDatabaseDeployment_ConfigFetchIgnoresNonAdminUsers(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{
		"admin": {User: "admin", Password: "admin-pass"},
		"dba":   {User: "dba", Password: "dba-pass"},
	}
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantHeader := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("admin:admin-pass"))
	if !strings.Contains(cmd, wantHeader) {
		t.Fatalf("expected the admin user's own header %q, got: %s", wantHeader, cmd)
	}
	if strings.Contains(cmd, "dba-pass") {
		t.Fatalf("expected the dba user's credential to never be used, got: %s", cmd)
	}
}

// api-credentials-secure-config off means the endpoint doesn't enforce auth
// at all -- embedding a real admin credential in every Deployment spec
// regardless would be needless exposure, so no header should be sent.
func TestK8SDatabaseDeployment_ConfigFetchNoAuthHeaderWhenSecureConfigOff(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = false
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if strings.Contains(cmd, "Authorization") || strings.Contains(cmd, "s3cr3t") {
		t.Fatalf("expected no Authorization header when api-credentials-secure-config is off, got: %s", cmd)
	}
}

// GetServerFromURL (cluster_get.go), which handlerMuxServersPortConfig uses
// to resolve the init container's request, matches only server.Host -- the
// domain-qualified name when prov-net-cni is on -- never the bare s.Name.
// The request path must match what GetServerFromURL actually looks for, or
// every config fetch 500s ("No server") on a freshly (re)provisioned pod
// (confirmed live: this exact mismatch broke clusterin's bootstrap).
func TestK8SDatabaseDeployment_ConfigFetchURLMatchesServerHost(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "cluster.local"
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "clustera-0", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantPath := "/servers/clustera-0.db.clustera.svc.cluster.local/3306/config"
	if !strings.Contains(cmd, wantPath) {
		t.Fatalf("expected the request path to use the domain-qualified name %q (matching server.Host), got: %s", wantPath, cmd)
	}
}

func TestK8SDatabaseDeployment_ConfigFetchURLStaysBareWhenProvNetCNIDisabled(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvNetCNI = false
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.HttpPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantPath := "/servers/db1/3306/config"
	if !strings.Contains(cmd, wantPath) {
		t.Fatalf("expected the bare short name in the request path when prov-net-cni is off, got: %s", cmd)
	}
}

// --- Headless Service DNS (per-pod names that follow pod recreation) ---

func TestK8SDatabaseDeployment_SubdomainMatchesHeadlessServiceName(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvNetCNI = true
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	got := dep.Spec.Template.Spec.Subdomain
	want := k8sHeadlessServiceName
	if got != want {
		t.Fatalf("expected Pod Subdomain to match the headless service name %q, got %q", want, got)
	}
}

// prov-net-cni gates the whole mechanism; disabled must stay byte-identical
// to before.
func TestK8SDatabaseDeployment_NoSubdomainWhenProvNetCNIDisabled(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvNetCNI = false
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	if got := dep.Spec.Template.Spec.Subdomain; got != "" {
		t.Fatalf("expected no Subdomain when prov-net-cni is disabled, got %q", got)
	}
}

// The Service selector and pod labels are defined in two places -- assert
// they agree, or the Service silently gets zero endpoints.
func TestK8SDatabaseDeployment_PodLabelsSatisfyHeadlessServiceSelector(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvNetCNI = true
	cluster.k8sEnsureHeadlessService(client, 3306)
	svc, err := client.CoreV1().Services("k8stest").Get(context.TODO(), k8sHeadlessServiceName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	s := &ServerMonitor{Name: "db1", Port: "3306"}
	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	podLabels := dep.Spec.Template.ObjectMeta.Labels

	for k, want := range svc.Spec.Selector {
		if got := podLabels[k]; got != want {
			t.Fatalf("headless Service selects %s=%q, but DB pod is labeled %s=%q -- Service would get zero endpoints", k, want, k, got)
		}
	}
}

func TestK8SEnsureHeadlessService_CreatesClusterIPNone(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	cluster.k8sEnsureHeadlessService(client, 3306)

	svc, err := client.CoreV1().Services("k8stest").Get(context.TODO(), k8sHeadlessServiceName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected the headless service to have been created, got error: %s", err)
	}
	if svc.Spec.ClusterIP != apiv1.ClusterIPNone {
		t.Fatalf("expected ClusterIP %q (headless), got %q", apiv1.ClusterIPNone, svc.Spec.ClusterIP)
	}
}

func TestK8SEnsureHeadlessService_AlreadyExistsDoesNotPanic(t *testing.T) {
	cluster := newTestCluster("k8stest")
	client := fake.NewSimpleClientset(&apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: k8sHeadlessServiceName, Namespace: "k8stest"},
	})

	cluster.k8sEnsureHeadlessService(client, 3306)
}

func TestK8SEnsureHeadlessService_SelectsEveryDBPodNotJustOneServer(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	cluster.k8sEnsureHeadlessService(client, 3306)

	svc, err := client.CoreV1().Services("k8stest").Get(context.TODO(), k8sHeadlessServiceName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, ok := svc.Spec.Selector["tag"]; ok {
		t.Fatal("expected the headless service to select on \"app\" alone, not a per-server \"tag\" -- a per-server tag would mean only one server's pod ever gets a published DNS record")
	}
	if svc.Spec.Selector["app"] != "repication-manager" {
		t.Fatalf("expected selector app=repication-manager, got %q", svc.Spec.Selector["app"])
	}
}

// --- Cluster DNS domain (prov-orchestrator-cluster) ---

func TestK8SClusterDomain_UsesConfiguredValue(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvOrchestratorCluster = "k8s.internal"

	if got := k8sClusterDomain(cluster); got != "k8s.internal" {
		t.Fatalf("expected the configured prov-orchestrator-cluster value, got %q", got)
	}
}

func TestK8SClusterDomain_FallsBackWhenUnset(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvOrchestratorCluster = ""

	if got := k8sClusterDomain(cluster); got != "cluster.local" {
		t.Fatalf("expected the Kubernetes default cluster.local when unset, got %q", got)
	}
}

// "local" is prov-orchestrator-cluster's own CLI default (server/server.go)
// -- an OpenSVC-oriented value, never a real Kubernetes --cluster-domain --
// so a cluster that never touched this flag must still get "cluster.local",
// not a malformed ".svc.local" CoreDNS can't resolve.
func TestK8SClusterDomain_FallsBackWhenLiteralCLIDefault(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvOrchestratorCluster = "local"

	if got := k8sClusterDomain(cluster); got != "cluster.local" {
		t.Fatalf("expected the CLI default \"local\" to fall back to cluster.local, got %q", got)
	}
}

// --- GetDomain (cluster_get.go): Kubernetes routes through the headless
// Service, OpenSVC keeps its original plain-namespace shape ---

func TestGetDomain_KubernetesRoutesThroughHeadlessService(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "cluster.local"

	got := cluster.GetDomain()
	want := ".db.clustera.svc.cluster.local"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestGetDomain_KubernetesTracksNonDefaultClusterDomain(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "k8s.internal"

	got := cluster.GetDomain()
	want := ".db.clustera.svc.k8s.internal"
	if got != want {
		t.Fatalf("expected the domain to track the configured cluster domain, got %q (want %q)", got, want)
	}
}

func TestGetDomain_OpenSVCUnaffectedByKubernetesChange(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cluster.Conf.ProvOrchestratorCluster = "signal18.id"

	got := cluster.GetDomain()
	want := ".clustera.svc.signal18.id"
	if got != want {
		t.Fatalf("expected OpenSVC's plain-namespace shape to be untouched, got %q (want %q)", got, want)
	}
}

// prov-net-cni set in [DEFAULT] must not leak a domain for a cluster whose
// orchestrator isn't OpenSVC.
func TestGetDomain_OnPremiseIgnoresProvNetCNI(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	cluster.Conf.ProvOrchestratorCluster = "signal18.id"

	if got := cluster.GetDomain(); got != "" {
		t.Fatalf("expected prov-net-cni to have no effect on a non-OpenSVC orchestrator, got %q", got)
	}
}

func TestGetDomainHeadCluster_KubernetesRoutesThroughParentsHeadlessService(t *testing.T) {
	cluster := newTestCluster("clustera-child")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "cluster.local"
	cluster.Conf.ClusterHead = "clustera"

	got := cluster.GetDomainHeadCluster()
	want := ".db.clustera.svc.cluster.local"
	if got != want {
		t.Fatalf("expected the parent's own headless service name, got %q (want %q)", got, want)
	}
}
