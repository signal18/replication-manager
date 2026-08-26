package cluster

import (
	"context"
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
