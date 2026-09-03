package cluster

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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

// Embeds a nil DatabaseProxy so only the methods actually exercised below
// need overriding. proxyType defaults to "" (zero value), which
// k8sProxyImage/k8sProxyContainerPorts/k8sProxyServicePorts treat as an
// unsupported type -- tests exercising the ProxySQL path must set it
// explicitly to config.ConstProxySqlproxy.
type fakeProxy struct {
	DatabaseProxy
	name          string
	host          string
	port          string
	proxyType     string
	writePort     int
	readPort      int
	readWritePort int
	cluster       *Cluster
}

func (f *fakeProxy) GetName() string { return f.name }
func (f *fakeProxy) GetPort() string { return f.port }
func (f *fakeProxy) GetType() string { return f.proxyType }
func (f *fakeProxy) GetHost() string {
	if f.host != "" {
		return f.host
	}
	return f.name
}
func (f *fakeProxy) GetWritePort() int     { return f.writePort }
func (f *fakeProxy) GetReadPort() int      { return f.readPort }
func (f *fakeProxy) GetReadWritePort() int { return f.readWritePort }

// GetCluster falls back to a bare Cluster instead of nil, which would panic
// on the HAProxy stat-port lookup in k8sProxyContainerPorts.
func (f *fakeProxy) GetCluster() *Cluster {
	if f.cluster != nil {
		return f.cluster
	}
	return &Cluster{Conf: &config.Config{}}
}

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
	prx := &fakeProxy{name: "proxysql1", port: "not-a-port", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	err := cluster.k8sProvisionProxyServiceWithClient(client, prx)
	if err == nil {
		t.Fatal("expected an explicit error for an invalid proxy port, not a silent port-0 deployment")
	}
}

func TestK8SProvisionProxy_AlreadyExistsIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

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
	prxA := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}
	prxB := &fakeProxy{name: "proxysql2", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

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

	svcList, err := client.CoreV1().Services("k8stest").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error listing services: %s", err)
	}
	if len(svcList.Items) != 2 {
		t.Fatalf("expected 2 distinct proxy services, got %d", len(svcList.Items))
	}
}

// --- Proxy provisioning: Service exposure and type-awareness (Phase 3) ---

func TestK8SProvisionProxy_CreatesServiceWithAdminAndSQLPorts(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	svc, err := client.CoreV1().Services("k8stest").Get(context.TODO(), k8sProxyServiceName(prx), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected a Service named %q to exist: %s", k8sProxyServiceName(prx), err)
	}
	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("expected 2 service ports (admin, sql), got %d: %v", len(svc.Spec.Ports), svc.Spec.Ports)
	}
	var gotAdmin, gotSQL bool
	for _, p := range svc.Spec.Ports {
		switch p.Name {
		case "admin":
			gotAdmin = p.Port == 6032
		case "sql":
			gotSQL = p.Port == 6033
		}
	}
	if !gotAdmin {
		t.Fatalf("expected an admin port 6032, got %v", svc.Spec.Ports)
	}
	if !gotSQL {
		t.Fatalf("expected a sql port 6033, got %v", svc.Spec.Ports)
	}
}

func TestK8SProvisionProxy_ServiceAlreadyExistsIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("first provision: unexpected error: %s", err)
	}
	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("second provision (Service AlreadyExists) should be idempotent, got error: %s", err)
	}
}

func TestK8SProxyDeployment_UsesProvProxProxysqlImg(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvProxProxysqlImg = "signal18/proxysql:1.4"
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "signal18/proxysql:1.4" {
		t.Fatalf("expected image %q, got %q", "signal18/proxysql:1.4", got)
	}
}

func TestK8SProxyDeployment_ExposesAdminAndSQLContainerPorts(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	ports := dep.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 2 {
		t.Fatalf("expected 2 container ports (admin, sql), got %d: %v", len(ports), ports)
	}
	var gotAdmin, gotSQL bool
	for _, p := range ports {
		switch p.Name {
		case "admin":
			gotAdmin = p.ContainerPort == 6032
		case "sql":
			gotSQL = p.ContainerPort == 6033
		}
	}
	if !gotAdmin {
		t.Fatalf("expected an admin container port 6032, got %v", ports)
	}
	if !gotSQL {
		t.Fatalf("expected a sql container port 6033, got %v", ports)
	}
}

// Only GetPort() (the admin port) previously fed Kubernetes proxy
// provisioning -- if a caller reverted to that, the SQL port applications
// actually connect to would silently disappear from both the Deployment and
// the Service.
func TestK8SProxyDeployment_SQLPortIsNotJustGetPort(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	for _, p := range dep.Spec.Template.Spec.Containers[0].Ports {
		if p.Name == "sql" && p.ContainerPort == int32(6032) {
			t.Fatal("sql container port must not equal the admin port (GetPort()) -- GetWritePort() was not consulted")
		}
	}
}

func TestK8SProxyImage_UnsupportedTypeReturnsExplicitError(t *testing.T) {
	cluster := newTestCluster("k8stest")
	for _, typ := range []string{config.ConstProxySpider, config.ConstProxySphinx, ""} {
		prx := &fakeProxy{name: "proxy1", port: "6032", proxyType: typ}
		if _, err := cluster.k8sProxyImage(prx); err == nil {
			t.Fatalf("expected an explicit unsupported-type error for proxy type %q, got nil", typ)
		}
	}
}

func TestK8SProvisionProxy_UnsupportedTypeCreatesNothing(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "spider1", port: "6032", proxyType: config.ConstProxySpider}

	err := cluster.k8sProvisionProxyServiceWithClient(client, prx)
	if err == nil {
		t.Fatal("expected an explicit error for an unsupported proxy type, not a silent ProxySQL deployment")
	}

	if _, getErr := client.AppsV1().Deployments("k8stest").Get(context.TODO(), k8sProxyDeploymentName("k8stest", "spider1"), metav1.GetOptions{}); getErr == nil {
		t.Fatal("expected no Deployment to be created for an unsupported proxy type")
	}
	if _, getErr := client.CoreV1().Services("k8stest").Get(context.TODO(), "spider1", metav1.GetOptions{}); getErr == nil {
		t.Fatal("expected no Service to be created for an unsupported proxy type")
	}
	if _, getErr := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), k8sProxyPVCName("k8stest", "spider1"), metav1.GetOptions{}); getErr == nil {
		t.Fatal("expected no PVC to be created for an unsupported proxy type")
	}
}

// --- Proxy PVC (Phase 4: ProxySQL persistent storage) ---

func TestK8SProxyPVC_UsesProvProxyDiskSize(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvProxDisk = "10G"
	prx := &fakeProxy{name: "proxysql1"}

	pvc := cluster.k8sProxyPVC(prx)

	got := pvc.Spec.Resources.Requests[apiv1.ResourceStorage]
	want := resource.MustParse("10G")
	if got.Cmp(want) != 0 {
		t.Fatalf("expected storage request %s, got %s", want.String(), got.String())
	}
}

func TestK8SProxyPVC_FallsBackToDefaultSizeWhenUnparseable(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvProxDisk = "not-a-size"
	prx := &fakeProxy{name: "proxysql1"}

	pvc := cluster.k8sProxyPVC(prx)

	got := pvc.Spec.Resources.Requests[apiv1.ResourceStorage]
	want := resource.MustParse("20G")
	if got.Cmp(want) != 0 {
		t.Fatalf("expected the fallback to match prov-proxy-disk-size's own default (20G), got %s", got.String())
	}
}

func TestK8SProxyPVC_NoStorageClassNameWhenUnset(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1"}

	pvc := cluster.k8sProxyPVC(prx)

	if pvc.Spec.StorageClassName != nil {
		t.Fatalf("expected a nil StorageClassName (use cluster default), got %q", *pvc.Spec.StorageClassName)
	}
}

func TestK8SProxyPVC_UsesConfiguredStorageClass(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeProxyStorageClass = "fast-ssd"
	prx := &fakeProxy{name: "proxysql1"}

	pvc := cluster.k8sProxyPVC(prx)

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Fatalf("expected StorageClassName \"fast-ssd\", got %v", pvc.Spec.StorageClassName)
	}
}

func TestK8SProxyPVC_NamePerProxy(t *testing.T) {
	n1 := k8sProxyPVCName("mycluster", "proxysql1")
	n2 := k8sProxyPVCName("mycluster", "proxysql2")
	if n1 == n2 {
		t.Fatalf("expected distinct PVC names for distinct proxies, got %q for both", n1)
	}
}

// --- ProxySQL Deployment: PVC-backed persistent storage and bootstrap
// init container (Phase 4) ---

func TestK8SProxyDeployment_UsesPVCBackedVolume(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected exactly one volume, got %d: %v", len(dep.Spec.Template.Spec.Volumes), dep.Spec.Template.Spec.Volumes)
	}
	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName != k8sProxyPVCName("k8stest", "proxysql1") {
		t.Fatalf("expected the volume to reference PVC %q, got %v", k8sProxyPVCName("k8stest", "proxysql1"), vol)
	}
}

func TestK8SProxyDeployment_MainContainerMountsDataAndConfigDirs(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	var hasData, hasConf bool
	for _, m := range mounts {
		if m.MountPath == "/var/lib/proxysql" && m.SubPath == "" {
			hasData = true
		}
		if m.MountPath == "/etc/proxysql" && m.SubPath == k8sProxyConfPersistSubPath {
			hasConf = true
		}
	}
	if !hasData {
		t.Fatalf("expected a full-volume mount at /var/lib/proxysql, got %v", mounts)
	}
	if !hasConf {
		t.Fatalf("expected a subPath mount at /etc/proxysql (subPath %q), got %v", k8sProxyConfPersistSubPath, mounts)
	}
}

// The generated proxysql.cnf's ssl_p2s_cert/ssl_p2s_key/ssl_p2s_ca
// directives are built from CONFDIR ("/etc" for Kubernetes,
// GetConfigConfigdir, prx_get.go) + "/ssl/*.pem" -- i.e. /etc/ssl/*.pem --
// not /etc/proxysql/ssl/*.pem, which is where GenerateProxyConfig
// (cluster/configurator/configurator.go) actually stages those certs in
// the fetched tarball. Both the main and init containers must mount
// /etc/ssl, on its own subPath so it doesn't also relocate proxysql.cnf.
func TestK8SProxyDeployment_MountsSSLCertPathConfigExpects(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	for _, c := range []apiv1.Container{dep.Spec.Template.Spec.Containers[0], dep.Spec.Template.Spec.InitContainers[0]} {
		var hasSSL bool
		for _, m := range c.VolumeMounts {
			if m.MountPath == "/etc/ssl" {
				hasSSL = true
				if m.SubPath != k8sProxySSLPersistSubPath {
					t.Fatalf("container %q: expected /etc/ssl subPath %q, got %q", c.Name, k8sProxySSLPersistSubPath, m.SubPath)
				}
				if m.SubPath == k8sProxyConfPersistSubPath {
					t.Fatalf("container %q: /etc/ssl must not share proxysql.cnf's own subPath (would relocate proxysql.cnf out of /etc/proxysql)", c.Name)
				}
			}
		}
		if !hasSSL {
			t.Fatalf("container %q: expected a mount at /etc/ssl, got %v", c.Name, c.VolumeMounts)
		}
	}
}

// Regression test for a review finding: the init container fetched the
// tarball and copied proxysql.cnf/data/*.pem, but never copied the p2s SSL
// certs from the tarball's etc/proxysql/ssl/ staging path to /etc/ssl,
// where the generated config actually looks for them -- so an SSL-enabled
// ProxySQL would come up with ssl_p2s_cert/key/ca pointing at files that
// were never placed there.
func TestK8SProxyDeployment_InitContainerCopiesSSLCertsToConfigExpectedPath(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", host: "proxysql1.k8stest.svc.cluster.local", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	cmdStr := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")
	if !strings.Contains(cmdStr, "cp /tmp/cfg/etc/proxysql/ssl/") {
		t.Fatalf("expected the init container to copy the tarball's staged SSL certs (etc/proxysql/ssl/), got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "/etc/ssl/") {
		t.Fatalf("expected the init container to copy SSL certs to /etc/ssl (where ssl_p2s_cert/key/ca in the generated config point), got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "mkdir -p") || !strings.Contains(cmdStr, "/etc/ssl") {
		t.Fatalf("expected /etc/ssl to be created before use, got %q", cmdStr)
	}
}

func TestK8SProxyDeployment_MainContainerCommandPointsToPersistedConfig(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	cmd := dep.Spec.Template.Spec.Containers[0].Command
	found := false
	for _, c := range cmd {
		if strings.Contains(c, "/etc/proxysql/proxysql.cnf") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the container command to reference /etc/proxysql/proxysql.cnf, got %v", cmd)
	}
}

func TestK8SProxyDeployment_ProxySQLCommandCapsFileDescriptorLimit(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	cmd := dep.Spec.Template.Spec.Containers[0].Command
	if len(cmd) < 3 {
		t.Fatalf("expected a sh -c command, got %+v", cmd)
	}
	script := cmd[2]
	// Matches plain "docker run"'s default (soft 1024 / hard 1048576), which
	// is what OpenSVC's ProxySQL container gets implicitly -- some container
	// runtimes (observed on kind/containerd) instead hand pods an inflated
	// RLIMIT_NOFILE by default, so this keeps behavior identical regardless
	// of orchestrator.
	for _, want := range []string{"ulimit -Sn 1024", "ulimit -Hn 1048576", "exec proxysql"} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected proxysql command to contain %q, got: %s", want, script)
		}
	}
}

func TestK8SProxyDeployment_HasBootstrapInitContainer(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", host: "proxysql1.k8stest.svc.cluster.local", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(dep.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected exactly one init container, got %d", len(dep.Spec.Template.Spec.InitContainers))
	}
	init := dep.Spec.Template.Spec.InitContainers[0]
	cmdStr := strings.Join(init.Command, " ")
	if !strings.Contains(cmdStr, "-T 8") {
		t.Fatalf("expected a bounded (-T 8) wget fetch in the init container command, got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, prx.GetHost()+"/"+prx.GetPort()+"/config") {
		t.Fatalf("expected the init container to fetch from the proxy's own host/port, got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "need-config-fetch") {
		t.Fatalf("expected the init container to consult need-config-fetch before fetching, got %q", cmdStr)
	}
}

func TestK8SProxyDeployment_InitContainerSecureConfigUsesSecretKeyRef(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	init := dep.Spec.Template.Spec.InitContainers[0]
	var found *apiv1.EnvVar
	for i := range init.Env {
		if init.Env[i].Name == k8sSecretKeyAPIAuthHeader {
			found = &init.Env[i]
		}
	}
	if found == nil {
		t.Fatal("expected a REPMAN_AUTH_HEADER env var when api-credentials-secure-config is enabled")
	}
	if found.Value != "" {
		t.Fatalf("expected no raw Value (auth header must come from the Secret), got %q", found.Value)
	}
	if found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil {
		t.Fatal("expected REPMAN_AUTH_HEADER to be sourced from a SecretKeyRef")
	}
	if found.ValueFrom.SecretKeyRef.Name != k8sClusterSecretName("k8stest") {
		t.Fatalf("expected SecretKeyRef to name %q, got %q", k8sClusterSecretName("k8stest"), found.ValueFrom.SecretKeyRef.Name)
	}
}

func TestK8SProxyDeployment_NoInitContainerOrVolumesForUnsupportedType(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "spider1", port: "6032", proxyType: config.ConstProxySpider}

	if _, err := cluster.k8sProxyDeployment(prx); err == nil {
		t.Fatal("expected an explicit error for an unsupported proxy type")
	}
}

// --- Proxy provisioning: PVC lifecycle (Phase 4) ---

func TestK8SProvisionProxy_CreatesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if _, err := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), k8sProxyPVCName("k8stest", "proxysql1"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected a PVC named %q to exist: %s", k8sProxyPVCName("k8stest", "proxysql1"), err)
	}
}

func TestK8SProvisionProxy_PVCAlreadyExistsIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("first provision: unexpected error: %s", err)
	}
	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("second provision (PVC AlreadyExists) should be idempotent, got error: %s", err)
	}
}

// Unprovisioning must never destroy the persisted ProxySQL config/data --
// same retention semantics as the database PVC (prov_k8s_db.go).
func TestK8SUnprovisionProxy_RetainsPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("provision: unexpected error: %s", err)
	}
	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unprovision: unexpected error: %s", err)
	}

	if _, err := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), k8sProxyPVCName("k8stest", "proxysql1"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected the PVC to be retained after unprovision, got error: %s", err)
	}
}

// --- Proxy fetch-config parity (prov-proxy-start-fetch-config) ---
//
// Mirrors TestSrvCheckNeedConfigFetch-equivalent DB behavior
// (srv_chk.go's ServerMonitor.CheckNeedConfigFetch): the cookie state
// tracks the live config setting, both directions.

func newTestProxyForFetchConfig(cluster *Cluster) *Proxy {
	// Host and Port must be set: SetDataDir is a no-op ("" Datadir, so
	// createCookie writes to filesystem root and silently fails) unless
	// proxy.Host is non-empty.
	prx := &Proxy{Name: "proxysql1", Host: "proxysql1", Port: "6032", ClusterGroup: cluster}
	prx.SetDataDir()
	return prx
}

func TestProxyCheckNeedConfigFetch_EnabledDropsNoFetchCookie(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.WorkingDir = t.TempDir()
	cluster.Conf.ProvProxyStartFetchConfig = true
	prx := newTestProxyForFetchConfig(cluster)
	prx.SetNoConfigFetchCookie()

	prx.CheckNeedConfigFetch()

	if prx.HasNoConfigFetchCookie() {
		t.Fatal("expected the no-fetch cookie to be removed once prov-proxy-start-fetch-config is enabled")
	}
}

func TestProxyCheckNeedConfigFetch_DisabledSetsNoFetchCookie(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.WorkingDir = t.TempDir()
	cluster.Conf.ProvProxyStartFetchConfig = false
	prx := newTestProxyForFetchConfig(cluster)

	prx.CheckNeedConfigFetch()

	if !prx.HasNoConfigFetchCookie() {
		t.Fatal("expected the no-fetch cookie to be set once prov-proxy-start-fetch-config is disabled")
	}
}

func TestClusterCheckNeedConfigFetch_CoversProxiesToo(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.WorkingDir = t.TempDir()
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.ProvProxyStartFetchConfig = false
	prx := newTestProxyForFetchConfig(cluster)
	cluster.Proxies = []DatabaseProxy{prx}

	cluster.CheckNeedConfigFetch()

	if !prx.HasNoConfigFetchCookie() {
		t.Fatal("expected cluster.CheckNeedConfigFetch to also sync the proxy's no-fetch cookie, not just servers'")
	}
}

func TestK8SUnprovisionProxy_DeletesService(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: k8sProxyDeploymentName("k8stest", "proxysql1"), Namespace: "k8stest"}},
		&apiv1.Service{ObjectMeta: metav1.ObjectMeta{Name: "proxysql1", Namespace: "k8stest"}},
	)

	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, err := client.CoreV1().Services("k8stest").Get(context.TODO(), "proxysql1", metav1.GetOptions{}); err == nil {
		t.Fatal("expected service to be deleted")
	}
}

func TestK8SUnprovisionProxy_ServiceNotFoundIsIdempotent(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}
	// Deployment exists, Service does not -- e.g. an object provisioned by an
	// older repman build before Service creation existed.
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: k8sProxyDeploymentName("k8stest", "proxysql1"), Namespace: "k8stest"}},
	)

	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("expected a missing Service to be treated as idempotent, got error: %s", err)
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

// --- Database Secret (MYSQL_ROOT_PASSWORD, not a raw Env value) ---
//
// One Secret shared by the whole cluster (k8sClusterSecretName), matching
// OpenSVC's own single cluster-wide secret store: every server shares the
// same root credential, so a per-server Secret would only ever hold
// duplicate copies of the same value.

func TestK8SEnsureDatabaseSecret_CreatesSecretWithPassword(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sEnsureDatabaseSecret(client, "s3cr3t"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	secret, err := client.CoreV1().Secrets("k8stest").Get(context.TODO(), k8sClusterSecretName("k8stest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected the secret to have been created, got error: %s", err)
	}
	if secret.StringData[k8sSecretKeyRootPassword] != "s3cr3t" {
		t.Fatalf("expected MYSQL_ROOT_PASSWORD %q, got %q", "s3cr3t", secret.StringData[k8sSecretKeyRootPassword])
	}
}

// Password rotation must take effect on the next pod restart, not silently
// keep whatever was there before.
func TestK8SEnsureDatabaseSecret_UpdatesExistingSecret(t *testing.T) {
	client := fake.NewSimpleClientset(&apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: k8sClusterSecretName("k8stest"), Namespace: "k8stest"},
		StringData: map[string]string{k8sSecretKeyRootPassword: "old-pass"},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sEnsureDatabaseSecret(client, "new-pass"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	secret, err := client.CoreV1().Secrets("k8stest").Get(context.TODO(), k8sClusterSecretName("k8stest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if secret.StringData[k8sSecretKeyRootPassword] != "new-pass" {
		t.Fatalf("expected the rotated password %q, got %q", "new-pass", secret.StringData[k8sSecretKeyRootPassword])
	}
}

// k8sRotatePasswordsWithClient is ProvisionRotatePasswords' Kubernetes
// branch (prov.go): a single patch to the cluster's shared Secret covers
// every server's Deployment, since they all reference the same Secret.
func TestK8SRotatePasswordsWithClient_UpdatesClusterSecret(t *testing.T) {
	client := fake.NewSimpleClientset(
		&apiv1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: k8sClusterSecretName("k8stest"), Namespace: "k8stest"},
			StringData: map[string]string{k8sSecretKeyRootPassword: "old-pass"},
		},
	)
	cluster := newTestCluster("k8stest")
	cluster.Servers = serverList{&ServerMonitor{Name: "db1"}, &ServerMonitor{Name: "db2"}}

	cluster.k8sRotatePasswordsWithClient(client, "rotated-pass")

	secret, err := client.CoreV1().Secrets("k8stest").Get(context.TODO(), k8sClusterSecretName("k8stest"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if secret.StringData[k8sSecretKeyRootPassword] != "rotated-pass" {
		t.Fatalf("expected the shared secret to be updated to the rotated password, got %q", secret.StringData[k8sSecretKeyRootPassword])
	}
}

// The Deployment must reference the Secret via SecretKeyRef, never embed
// the password as a raw Env value -- that would put it in cleartext in the
// Deployment spec (kubectl get deploy -o yaml, RBAC permitting).
func TestK8SDatabaseDeployment_RootPasswordUsesSecretKeyRefNotRawValue(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306", Pass: "s3cr3t"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	env := dep.Spec.Template.Spec.Containers[0].Env

	var found *apiv1.EnvVar
	for i := range env {
		if env[i].Name == "MYSQL_ROOT_PASSWORD" {
			found = &env[i]
		}
	}
	if found == nil {
		t.Fatal("expected a MYSQL_ROOT_PASSWORD env var")
	}
	if found.Value != "" {
		t.Fatalf("expected no raw Value (password must come from the Secret), got %q", found.Value)
	}
	if found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil {
		t.Fatal("expected MYSQL_ROOT_PASSWORD to be sourced from a SecretKeyRef")
	}
	if found.ValueFrom.SecretKeyRef.Name != k8sClusterSecretName("k8stest") {
		t.Fatalf("expected SecretKeyRef to name %q, got %q", k8sClusterSecretName("k8stest"), found.ValueFrom.SecretKeyRef.Name)
	}
	if found.ValueFrom.SecretKeyRef.Key != k8sSecretKeyRootPassword {
		t.Fatalf("expected SecretKeyRef key %q, got %q", k8sSecretKeyRootPassword, found.ValueFrom.SecretKeyRef.Key)
	}
}

// --- Database PVC (size, StorageClass) ---

func TestK8SDatabasePVC_UsesProvDiskSize(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDisk = "20G"
	s := &ServerMonitor{Name: "db1"}

	pvc := cluster.k8sDatabasePVC(s)

	got := pvc.Spec.Resources.Requests[apiv1.ResourceStorage]
	want := resource.MustParse("20G")
	if got.Cmp(want) != 0 {
		t.Fatalf("expected storage request %s, got %s", want.String(), got.String())
	}
}

func TestK8SDatabasePVC_FallsBackToDefaultSizeWhenUnparseable(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDisk = "not-a-size"
	s := &ServerMonitor{Name: "db1"}

	pvc := cluster.k8sDatabasePVC(s)

	got := pvc.Spec.Resources.Requests[apiv1.ResourceStorage]
	want := resource.MustParse("20G")
	if got.Cmp(want) != 0 {
		t.Fatalf("expected the fallback to match prov-db-disk-size's own default (20G), got %s", got.String())
	}
}

// StorageClassName is a *string in the K8s API specifically to distinguish
// "use the cluster's default StorageClass" (nil) from "use no StorageClass"
// (a pointer to "") -- prov-kube-storage-class unset must stay nil, not a
// pointer to an empty string, or PVC binding would behave differently than
// leaving the field alone.
func TestK8SDatabasePVC_NoStorageClassNameWhenUnset(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1"}

	pvc := cluster.k8sDatabasePVC(s)

	if pvc.Spec.StorageClassName != nil {
		t.Fatalf("expected a nil StorageClassName (use cluster default), got %q", *pvc.Spec.StorageClassName)
	}
}

func TestK8SDatabasePVC_UsesConfiguredStorageClass(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeStorageClass = "fast-ssd"
	s := &ServerMonitor{Name: "db1"}

	pvc := cluster.k8sDatabasePVC(s)

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Fatalf("expected StorageClassName \"fast-ssd\", got %v", pvc.Spec.StorageClassName)
	}
}

// --- StorageClass listing (provisioning GUI dropdown) ---

func TestK8SStorageClassesFromClient_ListsNames(t *testing.T) {
	client := fake.NewSimpleClientset(
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard"}},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast-ssd"}},
	)
	cluster := newTestCluster("k8stest")

	names, err := cluster.k8sStorageClassesFromClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 storage classes, got %d: %v", len(names), names)
	}
}

func TestK8SStorageClassesFromClient_EmptyClusterReturnsEmptyNotNilError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	names, err := cluster.k8sStorageClassesFromClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected no storage classes, got %v", names)
	}
}

// --- Legacy proxy deployment: left alone, only warned about ---

func TestK8SProvisionProxy_DoesNotTouchLegacyDeployment(t *testing.T) {
	legacyName := k8sLegacyProxyDeploymentName("k8stest")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: legacyName, Namespace: "k8stest"},
	})
	cluster := newTestCluster("k8stest")
	prx := &fakeProxy{name: "proxysql1", port: "6032", proxyType: config.ConstProxySqlproxy, writePort: 6033}

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

// --- Database stop/start: a real scale-to-0/scale-to-1 cycle ---
//
// Stop then Start ends up with the same freshly-configured pod as
// K8SRestartDatabaseService, just via a genuine downtime window instead of
// a rolling replacement -- see k8sStopDatabaseServiceWithClient and
// k8sStartDatabaseServiceWithClient (prov_k8s_db.go).

func TestK8SStopDatabase_ScalesReplicasToZero(t *testing.T) {
	one := int32(1)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("expected replicas to be patched to 0, got %v", dep.Spec.Replicas)
	}
}

func TestK8SStopDatabase_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment to stop does not exist")
	}
}

func TestK8SStartDatabase_ScalesReplicasToOne(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("expected replicas to be patched back to 1, got %v", dep.Spec.Replicas)
	}
}

// Patching replicas to 1 when already at 1 must be a harmless no-op, not
// an error -- Start is called unconditionally by K8SRestartDatabaseService's
// callers regardless of whether the server was actually stopped first.
func TestK8SStartDatabase_AlreadyRunningIsNoOp(t *testing.T) {
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

func TestK8SStartDatabase_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStartDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment to start does not exist")
	}
}

// --- Proxy stop/start: same scale-to-0/scale-to-1 pattern as the database
// lifecycle above (k8sStopProxyServiceWithClient/
// k8sStartProxyServiceWithClient, prov_k8s_prx.go), keyed by the per-proxy
// Deployment name (k8sProxyDeploymentName), never the legacy shared
// <cluster>-deployment.

func TestK8SStopProxy_ScalesReplicasToZero(t *testing.T) {
	one := int32(1)
	name := k8sProxyDeploymentName("k8stest", "proxysql1")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("expected replicas to be patched to 0, got %v", dep.Spec.Replicas)
	}
}

func TestK8SStopProxy_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopProxyServiceWithClient(client, k8sProxyDeploymentName("k8stest", "proxysql1")); err == nil {
		t.Fatal("expected an error when the proxy deployment to stop does not exist")
	}
}

// Patching replicas to 0 when already at 0 must be a harmless no-op.
func TestK8SStopProxy_AlreadyStoppedIsNoOp(t *testing.T) {
	zero := int32(0)
	name := k8sProxyDeploymentName("k8stest", "proxysql1")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestK8SStartProxy_ScalesReplicasToOne(t *testing.T) {
	zero := int32(0)
	name := k8sProxyDeploymentName("k8stest", "proxysql1")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStartProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("expected replicas to be patched back to 1, got %v", dep.Spec.Replicas)
	}
}

func TestK8SStartProxy_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStartProxyServiceWithClient(client, k8sProxyDeploymentName("k8stest", "proxysql1")); err == nil {
		t.Fatal("expected an error when the proxy deployment to start does not exist")
	}
}

// Patching replicas to 1 when already at 1 must be a harmless no-op -- Start
// is called unconditionally regardless of whether Stop actually ran.
func TestK8SStartProxy_AlreadyRunningIsNoOp(t *testing.T) {
	one := int32(1)
	name := k8sProxyDeploymentName("k8stest", "proxysql1")
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStartProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

// Stop/start on a proxy must never touch the legacy shared
// <cluster>-deployment, even though it also carries the shared "app"
// selector -- a single proxy's lifecycle call has no way to prove the
// legacy Deployment belongs to it rather than a different, not-yet-migrated
// proxy in the same cluster.
func TestK8SStopStartProxy_DoesNotTouchLegacyDeployment(t *testing.T) {
	legacyName := k8sLegacyProxyDeploymentName("k8stest")
	one := int32(1)
	name := k8sProxyDeploymentName("k8stest", "proxysql1")
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "k8stest"}, Spec: appsv1.DeploymentSpec{Replicas: &one}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: legacyName, Namespace: "k8stest"}, Spec: appsv1.DeploymentSpec{Replicas: &one}},
	)
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sStopProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := cluster.k8sStartProxyServiceWithClient(client, name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	legacy, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), legacyName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if legacy.Spec.Replicas == nil || *legacy.Spec.Replicas != 1 {
		t.Fatalf("expected the legacy deployment's replicas to be left untouched at 1, got %v", legacy.Spec.Replicas)
	}
}

// --- Image pull policy (prov-kube-image-force-pull) ---

func TestK8SImagePullPolicy_AlwaysWhenForcePullSet(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = true

	if got := k8sImagePullPolicy(cluster); got != apiv1.PullAlways {
		t.Fatalf("expected PullAlways, got %q", got)
	}
}

func TestK8SImagePullPolicy_ExplicitIfNotPresentByDefault(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = false

	if got := k8sImagePullPolicy(cluster); got != apiv1.PullIfNotPresent {
		t.Fatalf("expected an explicit PullIfNotPresent, got %q", got)
	}
}

func TestK8SDatabaseDeployment_ContainerUsesConfiguredPullPolicy(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = true
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullAlways {
		t.Fatalf("expected the database container to use PullAlways, got %q", got)
	}
}

// --- Plain restart (rolling restart via annotation patch only) ---
//
// Used by RollingRestart (cluster/cluster_roll.go), a scheduled/bulk
// operation -- unlike k8sForceRepullDatabaseServiceWithClient below, this
// must never touch ImagePullPolicy: a restart is not an upgrade, and
// silently re-asserting the force-pull setting on every scheduled restart
// would be a surprising side effect.

func TestK8SRestartDatabaseService_PatchesRestartedAtAnnotation(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sRestartDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, ok := dep.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"]; !ok {
		t.Fatal("expected the pod template to be annotated with kubectl.kubernetes.io/restartedAt")
	}
}

func TestK8SRestartDatabaseService_NeverTouchesImagePullPolicy(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", ImagePullPolicy: apiv1.PullIfNotPresent},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = true

	if err := cluster.k8sRestartDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullIfNotPresent {
		t.Fatalf("expected ImagePullPolicy to be left untouched at PullIfNotPresent despite prov-kube-image-force-pull=true, got %q", got)
	}
}

func TestK8SRestartDatabaseService_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sRestartDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment doesn't exist, got nil")
	}
}

// --- Rollout completion confirmation ---
//
// WaitRejoin's own completion signal only fires when repman's monitoring
// loop observes a PrevState==stateFailed transition, which a clean rollout
// with nothing for replication to actively rejoin may never trigger.
// k8sWaitRolloutCompleteWithClient gives a positive, Kubernetes-native
// confirmation the pod was genuinely replaced -- the same condition
// `kubectl rollout status` checks -- so a stalled rollout is
// distinguishable from a fast, successful one instead of both just leaving
// WaitRejoin to time out with no error either way.

func TestK8SWaitRolloutComplete_SuccessWhenAlreadyRolledOut(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			Replicas:           1,
		},
	}
	client := fake.NewSimpleClientset(dep)
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sWaitRolloutCompleteWithClient(client, "db1", time.Second, time.Millisecond); err != nil {
		t.Fatalf("expected a rolled-out Deployment to report success immediately, got: %s", err)
	}
}

func TestK8SWaitRolloutComplete_TimesOutWhenRolloutStalled(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		Status: appsv1.DeploymentStatus{
			// ObservedGeneration stuck at the pre-restart generation --
			// simulates the controller never picking up the patch, or a new
			// pod stuck Pending/ImagePullBackOff.
			ObservedGeneration: 1,
			UpdatedReplicas:    0,
			ReadyReplicas:      1,
			Replicas:           1,
		},
	}
	client := fake.NewSimpleClientset(dep)
	cluster := newTestCluster("k8stest")

	start := time.Now()
	err := cluster.k8sWaitRolloutCompleteWithClient(client, "db1", 30*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected a stalled rollout to return a timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("expected the call to actually wait out the timeout before failing, returned after only %s", elapsed)
	}
}

func TestK8SWaitRolloutComplete_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	start := time.Now()
	err := cluster.k8sWaitRolloutCompleteWithClient(client, "db1", time.Second, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when the deployment doesn't exist, got nil")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected a missing deployment to fail immediately, not wait out the timeout, took %s", elapsed)
	}
}

// A transient Get error (API server hiccup, rate limiting) must be
// retried like "not yet rolled out", not treated as fatal the way a
// genuine NotFound is.
func TestK8SWaitRolloutComplete_RetriesOnTransientGetError(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			Replicas:           1,
		},
	}
	client := fake.NewSimpleClientset(dep)
	failuresLeft := 2
	client.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if failuresLeft > 0 {
			failuresLeft--
			return true, nil, apierrors.NewServiceUnavailable("transient")
		}
		return false, nil, nil
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sWaitRolloutCompleteWithClient(client, "db1", time.Second, 5*time.Millisecond); err != nil {
		t.Fatalf("expected transient errors to be retried until success, got: %s", err)
	}
	if failuresLeft != 0 {
		t.Fatalf("expected all injected transient errors to have been consumed, %d remaining", failuresLeft)
	}
}

// --- Force image re-pull action (rolling restart via annotation patch) ---

func TestK8SForceRepullDatabaseService_PatchesRestartedAtAnnotation(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sForceRepullDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, ok := dep.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"]; !ok {
		t.Fatal("expected the pod template to be annotated with kubectl.kubernetes.io/restartedAt")
	}
}

// The Deployment object only ever gets ImagePullPolicy at creation time
// (k8sDatabaseDeployment) -- toggling prov-kube-image-force-pull afterward
// must still reach an already-provisioned server's Deployment through this
// action, or the setting would silently do nothing until a full
// reprovision.
func TestK8SForceRepullDatabaseService_PatchesImagePullPolicyToCurrentSetting(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", ImagePullPolicy: apiv1.PullIfNotPresent},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = true

	if err := cluster.k8sForceRepullDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullAlways {
		t.Fatalf("expected ImagePullPolicy to be patched to PullAlways, got %q", got)
	}
}

func TestK8SForceRepullDatabaseService_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sForceRepullDatabaseServiceWithClient(client, "db1"); err == nil {
		t.Fatal("expected an error when the deployment doesn't exist, got nil")
	}
}

// --- Rolling upgrade: database image + pull policy update ---
//
// K8SUpdateDatabaseServiceConfig / k8sUpdateDatabaseServiceConfigWithClient
// is what makes RollingUpgrade (cluster/cluster_roll.go) actually change the
// running database image on Kubernetes, instead of only restarting pods on
// the existing spec like K8SForceRepullDatabaseService above.

func TestK8SUpdateDatabaseServiceConfig_PatchesMainContainerImage(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbImg = "mariadb:10.11"

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "mariadb:10.11" {
		t.Fatalf("expected main container image to be patched to mariadb:10.11, got %q", got)
	}
}

func TestK8SUpdateDatabaseServiceConfig_PatchesDbjobsSidecarImageWhenPresent(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:10.6"},
						{Name: "db1-dbjobs", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbImg = "mariadb:10.11"

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(dep.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("expected exactly 2 containers (no bogus container created), got %d", len(dep.Spec.Template.Spec.Containers))
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Image != "mariadb:10.11" {
			t.Fatalf("expected container %s image to be patched to mariadb:10.11, got %q", c.Name, c.Image)
		}
	}
}

// A missing sidecar (an older Deployment provisioned before it existed) must
// not produce a bogus, incomplete container entry -- strategic merge patches
// treat "containers" as merge-by-name, so patching a name absent from the
// live Deployment would otherwise create a container missing its command,
// volume mounts, and env.
func TestK8SUpdateDatabaseServiceConfig_MissingSidecarDoesNotCreateBogusContainer(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbImg = "mariadb:10.11"

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected exactly 1 container, got %d: %v", len(dep.Spec.Template.Spec.Containers), dep.Spec.Template.Spec.Containers)
	}
}

func TestK8SUpdateDatabaseServiceConfig_ForcePullTruePatchesPullAlwaysEvenWhenConfigDisabled(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", ImagePullPolicy: apiv1.PullIfNotPresent},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = false

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", true); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullAlways {
		t.Fatalf("expected forcePull=true to patch PullAlways regardless of prov-kube-image-force-pull, got %q", got)
	}
}

func TestK8SUpdateDatabaseServiceConfig_ForcePullFalseRestoresSteadyStatePolicy(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", ImagePullPolicy: apiv1.PullAlways},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvKubeImageForcePull = false

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullIfNotPresent {
		t.Fatalf("expected forcePull=false to restore the configured steady-state policy PullIfNotPresent, got %q", got)
	}
}

func TestK8SUpdateDatabaseServiceConfig_MissingDeploymentIsError(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err == nil {
		t.Fatal("expected an error when the deployment does not exist, got nil")
	}
}

func TestK8SUpdateDatabaseServiceConfig_MissingMainContainerIsError(t *testing.T) {
	zero := int32(0)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "some-other-container", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err == nil {
		t.Fatal("expected an error when the main database container is missing, got nil")
	}
}

// Patching while pods may still be live would race the Deployment
// controller's own rollout against RollingUpgrade's explicit stop/start
// (see the ordering comment on rollingUpgradeStopUpdateStart,
// cluster/cluster_roll.go) -- refused unconditionally, not just documented.
func TestK8SUpdateDatabaseServiceConfig_NotScaledToZeroIsError(t *testing.T) {
	one := int32(1)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err == nil {
		t.Fatal("expected an error when the deployment is not scaled to 0 replicas, got nil")
	}
}

// A nil Replicas is apps/v1's own "default to 1" case -- must be treated the
// same as an explicit non-zero value, not as "unset, assume safe".
func TestK8SUpdateDatabaseServiceConfig_NilReplicasIsError(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:10.6"},
					},
				},
			},
		},
	})
	cluster := newTestCluster("k8stest")

	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err == nil {
		t.Fatal("expected an error when Replicas is nil (apps/v1 defaults to 1), got nil")
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
// Credentials are sent as a base64 Authorization header for the fixed
// "admin" user, only when api-credentials-secure-config requires it. The
// value itself lives in a Secret (k8sSecretKeyAPIAuthHeader), referenced by
// the init container via an env var -- never baked as literal text into the
// command, which is plain-text visible via `kubectl get deploy -o yaml`.

// k8sAPIAuthHeaderValue is the pure function that resolves the admin
// password (falling back to "repman") and base64-encodes it; the command
// text itself only ever contains a "$REPMAN_AUTH_HEADER" reference.
func TestK8SAPIAuthHeaderValue_UsesConfiguredAdminPassword(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}

	want := base64.StdEncoding.EncodeToString([]byte("admin:s3cr3t"))
	if got := k8sAPIAuthHeaderValue(cluster); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// No admin user configured falls back to the documented default password
// "repman" -- matching api-credentials' own CLI default ("admin:repman"),
// never silently skipping auth when secure-config requires it.
func TestK8SAPIAuthHeaderValue_FallsBackToDefaultAdminPassword(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true

	want := base64.StdEncoding.EncodeToString([]byte("admin:repman"))
	if got := k8sAPIAuthHeaderValue(cluster); got != want {
		t.Fatalf("expected the default admin:repman value %q, got %q", want, got)
	}
}

// A non-admin api-credentials entry, even a differently-privileged one
// configured alongside admin, must never end up in the bootstrap header --
// only the admin user's own password is ever used.
func TestK8SAPIAuthHeaderValue_IgnoresNonAdminUsers(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{
		"admin": {User: "admin", Password: "admin-pass"},
		"dba":   {User: "dba", Password: "dba-pass"},
	}

	want := base64.StdEncoding.EncodeToString([]byte("admin:admin-pass"))
	if got := k8sAPIAuthHeaderValue(cluster); got != want {
		t.Fatalf("expected the admin user's own value %q, got %q", want, got)
	}
}

// api-credentials-secure-config off means the endpoint doesn't enforce auth
// at all -- embedding a real admin credential regardless would be needless
// exposure, so no header, no Secret key, and no env var should be sent.
func TestK8SAPIAuthHeaderValue_EmptyWhenSecureConfigOff(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = false
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}

	if got := k8sAPIAuthHeaderValue(cluster); got != "" {
		t.Fatalf("expected no auth header value when api-credentials-secure-config is off, got %q", got)
	}
}

func TestK8SDatabaseDeployment_ConfigFetchReferencesAuthHeaderEnvVarNotLiteral(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if strings.Contains(cmd, "admin:s3cr3t") || strings.Contains(cmd, "s3cr3t") {
		t.Fatalf("expected the raw password to never appear in the init container command, got: %s", cmd)
	}
	literalHeader := base64.StdEncoding.EncodeToString([]byte("admin:s3cr3t"))
	if strings.Contains(cmd, literalHeader) {
		t.Fatalf("expected the base64 auth value to never be baked literally into the command (it's plain-text visible via `kubectl get deploy -o yaml`), got: %s", cmd)
	}
	if !strings.Contains(cmd, "Authorization: Basic $"+k8sSecretKeyAPIAuthHeader) {
		t.Fatalf("expected the command to reference the auth header via $%s, got: %s", k8sSecretKeyAPIAuthHeader, cmd)
	}
}

// The init container's env var must actually resolve from this server's own
// Secret, at the same key the value gets patched into
// (K8SProvisionDatabaseService) -- a wiring mismatch here would silently
// send an empty/wrong Authorization header at runtime despite the command
// text looking correct.
func TestK8SDatabaseDeployment_AuthHeaderEnvVarSourcedFromClusterSecret(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = true
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	var found *apiv1.EnvVar
	for i, e := range dep.Spec.Template.Spec.InitContainers[0].Env {
		if e.Name == k8sSecretKeyAPIAuthHeader {
			found = &dep.Spec.Template.Spec.InitContainers[0].Env[i]
		}
	}
	if found == nil {
		t.Fatalf("expected an env var named %s on the init container", k8sSecretKeyAPIAuthHeader)
	}
	if found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected %s to be sourced from a SecretKeyRef, got a raw value", k8sSecretKeyAPIAuthHeader)
	}
	if got := found.ValueFrom.SecretKeyRef.LocalObjectReference.Name; got != k8sClusterSecretName("k8stest") {
		t.Fatalf("expected the env var to reference the cluster's shared secret %q, got %q", k8sClusterSecretName("k8stest"), got)
	}
	if got := found.ValueFrom.SecretKeyRef.Key; got != k8sSecretKeyAPIAuthHeader {
		t.Fatalf("expected the secret key %q, got %q", k8sSecretKeyAPIAuthHeader, got)
	}
}

// api-credentials-secure-config off must produce byte-identical init
// containers to before this credential moved into a Secret -- no env var at
// all, not just an empty one.
func TestK8SDatabaseDeployment_NoAuthHeaderEnvVarWhenSecureConfigOff(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.APISecureConfig = false
	cluster.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "s3cr3t"}}
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if strings.Contains(cmd, "Authorization") || strings.Contains(cmd, "s3cr3t") {
		t.Fatalf("expected no Authorization header when api-credentials-secure-config is off, got: %s", cmd)
	}
	if len(dep.Spec.Template.Spec.InitContainers[0].Env) != 0 {
		t.Fatalf("expected no env vars on the init container when api-credentials-secure-config is off, got: %v", dep.Spec.Template.Spec.InitContainers[0].Env)
	}
}

// GetServerFromURL (cluster_get.go) matches only server.Host -- the
// domain-qualified name when prov-net-cni is on -- never the bare s.Name.
// A mismatch here 500s ("No server") on every config fetch.
func TestK8SDatabaseDeployment_ConfigFetchURLMatchesServerHost(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "cluster.local"
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
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
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.ProvNetCNI = false
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantPath := "/servers/db1/3306/config"
	if !strings.Contains(cmd, wantPath) {
		t.Fatalf("expected the bare short name in the request path when prov-net-cni is off, got: %s", cmd)
	}
}

// All three fetches (need-config-fetch, config, replication-manager-cli)
// must use HTTPS on api-port (10005), self-signed cert hence
// --no-check-certificate. See ...FallsBackToHTTPWhenAPIServerDisabled for
// the api-server=false case.
func TestK8SDatabaseDeployment_ConfigFetchUsesHTTPSOnAPIPort(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	cluster.Conf.HttpPort = "10001"
	cluster.Conf.ApiServ = true
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if !strings.Contains(cmd, "https://127.0.0.1:10005/api/clusters/") {
		t.Fatalf("expected the config fetch to use https:// on api-port 10005, got: %s", cmd)
	}
	if !strings.Contains(cmd, "https://127.0.0.1:10005/static/configurator/bin/replication-manager-cli") {
		t.Fatalf("expected the replication-manager-cli fetch to use https:// on api-port 10005, got: %s", cmd)
	}
	if strings.Contains(cmd, ":10001") {
		t.Fatalf("expected http-port (10001) to never appear in the init container command, got: %s", cmd)
	}
	if strings.Count(cmd, "--no-check-certificate") != 3 {
		t.Fatalf("expected all three wget calls (need-config-fetch, config, replication-manager-cli) to pass --no-check-certificate for the self-signed cert, got: %s", cmd)
	}
}

// api-server=false means nothing listens on api-port -- must fall back to
// http-port instead of hanging the init container's wget with no error.
func TestK8SDatabaseDeployment_ConfigFetchFallsBackToHTTPWhenAPIServerDisabled(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	cluster.Conf.HttpPort = "10001"
	cluster.Conf.ApiServ = false
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if !strings.Contains(cmd, "http://127.0.0.1:10001/api/clusters/") {
		t.Fatalf("expected the config fetch to fall back to http:// on http-port 10001, got: %s", cmd)
	}
	if !strings.Contains(cmd, "http://127.0.0.1:10001/static/configurator/bin/replication-manager-cli") {
		t.Fatalf("expected the replication-manager-cli fetch to fall back to http:// on http-port 10001, got: %s", cmd)
	}
	if strings.Contains(cmd, ":10005") {
		t.Fatalf("expected api-port (10005) to never appear in the init container command when api-server is disabled, got: %s", cmd)
	}
	if strings.Contains(cmd, "--no-check-certificate") {
		t.Fatalf("expected no --no-check-certificate flag for the plain-HTTP fallback, got: %s", cmd)
	}
}

// --- dbjobs sidecar (backups/optimize/config-refresh) ---

// k8sDatabaseDeployment must stay callable with a bare *ServerMonitor (no
// ClusterGroup): calling s.GetJobDatadir() instead of hardcoding the path
// would dereference s.ClusterGroup.Configurator with no nil check and
// panic here.
func TestK8SDatabaseDeployment_PureBuilderDoesNotPanicOnBareServerMonitor(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	_ = cluster.k8sDatabaseDeployment(s, 3306, "node-a")
}

func TestK8SDatabaseDeployment_InitContainerCreatesJobsDatadir(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if !strings.Contains(cmd, "/var/lib/mysql/.system/jobs") {
		t.Fatalf("expected the init container to pre-create .system/jobs (JOBS_DATADIR), got: %s", cmd)
	}
}

func TestK8SDatabaseDeployment_InitContainerPopulatesDbjobsVolume(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	for _, want := range []string{
		"cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/",
		"/static/configurator/bin/replication-manager-cli",
		"chmod +x /docker-entrypoint-initdb.d/replication-manager-cli",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected the init container command to contain %q, got: %s", want, cmd)
		}
	}
}

// /docker-entrypoint-initdb.d is a subPath mount of the persistent-storage
// PVC now (k8sInitPersistSubPath), not a separate emptyDir -- matching
// OpenSVC's own {name}/init living under the same service volume as its
// data dir, so a failed config fetch has something to leave alone instead
// of an empty directory.
func TestK8SDatabaseDeployment_InitContainerMountsDbjobsVolume(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	found := false
	for _, vm := range dep.Spec.Template.Spec.InitContainers[0].VolumeMounts {
		if vm.Name == "db1-data" && vm.MountPath == "/docker-entrypoint-initdb.d" && vm.SubPath == k8sInitPersistSubPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the init container to mount db1-data at /docker-entrypoint-initdb.d via subPath %q", k8sInitPersistSubPath)
	}
}

// /etc/mysql/conf.d is likewise a subPath mount of the same PVC now, in
// every container that touches it.
func TestK8SDatabaseDeployment_ConfMountsAreSubPathsOfPersistentStorage(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	checkConf := func(t *testing.T, mounts []apiv1.VolumeMount, where string) {
		t.Helper()
		for _, vm := range mounts {
			if vm.MountPath == "/etc/mysql/conf.d" {
				if vm.Name != "db1-data" || vm.SubPath != k8sConfPersistSubPath {
					t.Fatalf("%s: expected /etc/mysql/conf.d mounted from db1-data via subPath %q, got name=%q subPath=%q", where, k8sConfPersistSubPath, vm.Name, vm.SubPath)
				}
				return
			}
		}
		t.Fatalf("%s: expected a mount at /etc/mysql/conf.d", where)
	}
	checkConf(t, dep.Spec.Template.Spec.InitContainers[0].VolumeMounts, "init container")
	checkConf(t, dep.Spec.Template.Spec.Containers[0].VolumeMounts, "db container")
}

// No separate -conf/-init emptyDirs and no ConfigMap volume should exist
// anymore -- persistent-storage is the only volume, mounted at different
// subPaths.
func TestK8SDatabaseDeployment_OnlyPersistentStorageVolumeExists(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected exactly one Volume (persistent-storage), got %d: %v", len(dep.Spec.Template.Spec.Volumes), dep.Spec.Template.Spec.Volumes)
	}
	v := dep.Spec.Template.Spec.Volumes[0]
	if v.Name != "db1-data" || v.VolumeSource.PersistentVolumeClaim == nil {
		t.Fatalf("expected the sole volume to be the PVC-backed db1-data, got: %+v", v)
	}
	if v.VolumeSource.ConfigMap != nil {
		t.Fatal("expected no ConfigMap volume source at all")
	}
}

func TestK8SDatabaseDeployment_DbjobsSidecarPresent(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbImg = "mariadb:10.11"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("expected the DB container plus one dbjobs sidecar, got %d containers", len(containers))
	}
	sidecar := containers[1]
	if sidecar.Name != "db1-dbjobs" {
		t.Fatalf("expected the sidecar name %q, got %q", "db1-dbjobs", sidecar.Name)
	}
	if sidecar.Image != "mariadb:10.11" {
		t.Fatalf("expected the sidecar to share the DB image %q, got %q", "mariadb:10.11", sidecar.Image)
	}
	// Guarded, not a bare exec: see TestK8SDatabaseDeployment_DbjobsSidecarIdlesInsteadOfCrashingWhenLauncherMissing.
	if len(sidecar.Command) != 3 || sidecar.Command[0] != "/bin/sh" || sidecar.Command[1] != "-c" {
		t.Fatalf("expected a guarded [\"/bin/sh\", \"-c\", ...] command, got %v", sidecar.Command)
	}
	if !strings.Contains(sidecar.Command[2], "/bin/bash /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm") {
		t.Fatalf("expected the guarded command to still exec the launcher when present, got %v", sidecar.Command)
	}
}

// dbjobs_new.sh reads $MYSQL_ROOT_PASSWORD (a real runtime env var, not a
// %%ENV:...%% template placeholder) -- must come from the same Secret as
// the DB container, never a raw Value.
func TestK8SDatabaseDeployment_DbjobsSidecarUsesSecretForRootPassword(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306", Pass: "s3cr3t"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	sidecar := dep.Spec.Template.Spec.Containers[1]

	var found *apiv1.EnvVar
	for i := range sidecar.Env {
		if sidecar.Env[i].Name == "MYSQL_ROOT_PASSWORD" {
			found = &sidecar.Env[i]
		}
	}
	if found == nil {
		t.Fatal("expected a MYSQL_ROOT_PASSWORD env var on the dbjobs sidecar")
	}
	if found.Value != "" {
		t.Fatalf("expected no raw Value, got %q", found.Value)
	}
	if found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil || found.ValueFrom.SecretKeyRef.Name != k8sClusterSecretName("k8stest") {
		t.Fatal("expected MYSQL_ROOT_PASSWORD sourced from the same shared cluster Secret as the DB container")
	}
}

// dbjobs_new.sh does raw filesystem operations directly against $DATADIR
// (e.g. moving restored .ibd files into place), so the sidecar needs the
// same persistent-storage volume the DB container writes to -- ReadWriteOnce
// restricts a PVC to one node, not one container, and every container here
// is in the same pod (so the same node) by construction.
func TestK8SDatabaseDeployment_DbjobsSidecarMountsDataAndInitVolumes(t *testing.T) {
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	sidecar := dep.Spec.Template.Spec.Containers[1]

	// Both mounts come from the same "db1-data" Volume now
	// (init via subPath), so a plain name-keyed map can't distinguish them
	// -- match on (MountPath, SubPath) pairs instead.
	type wantMount struct {
		mountPath string
		subPath   string
	}
	want := []wantMount{
		{"/var/lib/mysql", ""},
		{"/docker-entrypoint-initdb.d", k8sInitPersistSubPath},
	}
	if len(sidecar.VolumeMounts) != len(want) {
		t.Fatalf("expected %d volume mounts, got %d: %v", len(want), len(sidecar.VolumeMounts), sidecar.VolumeMounts)
	}
	for _, vm := range sidecar.VolumeMounts {
		if vm.Name != "db1-data" {
			t.Fatalf("expected every sidecar mount to come from db1-data, got %q", vm.Name)
		}
		found := false
		for _, w := range want {
			if vm.MountPath == w.mountPath && vm.SubPath == w.subPath {
				found = true
			}
		}
		if !found {
			t.Fatalf("unexpected mount at %s (subPath %q)", vm.MountPath, vm.SubPath)
		}
	}
}

// Real-shell check (with "sleep infinity" swapped for a bounded sleep so
// the test terminates) that the guard idles instead of erroring when the
// launcher script doesn't exist yet.
func TestK8SDatabaseDeployment_DbjobsSidecarIdlesInsteadOfCrashingWhenLauncherMissing(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cluster := newTestCluster("k8stest")
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	sidecarCmd := dep.Spec.Template.Spec.Containers[1].Command[2]

	tmpDir := t.TempDir() // stays empty: the launcher is never created
	sidecarCmd = strings.ReplaceAll(sidecarCmd, "/docker-entrypoint-initdb.d", tmpDir)
	sidecarCmd = strings.ReplaceAll(sidecarCmd, "sleep infinity", "sleep 2")

	start := time.Now()
	cmd := exec.Command("sh", "-c", sidecarCmd)
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected the guard to idle (exit 0 after the sleep), not error out, got error %s, output: %s", err, out)
	}
	if !strings.Contains(string(out), "dbjobs_launcher_with_sigterm not found") {
		t.Fatalf("expected a clear message explaining why it's idling, got: %s", out)
	}
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("expected the guard to have actually slept (~2s), not exited immediately -- took %s, output: %s", elapsed, out)
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

// --- NewProxySQLProxy host suffix (prx_proxysql.go): must share
// k8sClusterDomain's "local" -> "cluster.local" fallback on Kubernetes,
// like GetDomain() already does for the DB side, or the proxy's own host
// ends in ".svc.local" -- one ".svc." segment short of the real Service DNS
// name -- and CoreDNS never resolves it. Found via live kind testing: the
// Kubernetes Service itself resolved fine under its real name, but the
// proxy's own computed Host did not.

func TestNewProxySQLProxy_KubernetesFallsBackToClusterLocalWhenUnset(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "" // never configured, same as the CLI default "local"

	prx := NewProxySQLProxy(0, cluster, "proxysql1")
	want := "proxysql1.clustera.svc.cluster.local"
	if prx.Host != want {
		t.Fatalf("expected %q, got %q", want, prx.Host)
	}
}

func TestNewProxySQLProxy_KubernetesFallsBackWhenLiteralCLIDefault(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "local"

	prx := NewProxySQLProxy(0, cluster, "proxysql1")
	want := "proxysql1.clustera.svc.cluster.local"
	if prx.Host != want {
		t.Fatalf("expected the CLI default \"local\" to fall back to cluster.local, got %q (want %q)", prx.Host, want)
	}
}

func TestNewProxySQLProxy_KubernetesTracksConfiguredClusterDomain(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "k8s.internal"

	prx := NewProxySQLProxy(0, cluster, "proxysql1")
	want := "proxysql1.clustera.svc.k8s.internal"
	if prx.Host != want {
		t.Fatalf("expected the host to track the configured cluster domain, got %q (want %q)", prx.Host, want)
	}
}

func TestNewProxySQLProxy_KubernetesHeadCluster(t *testing.T) {
	cluster := newTestCluster("clustera-child")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "cluster.local"
	cluster.Conf.ClusterHead = "clustera"

	prx := NewProxySQLProxy(0, cluster, "proxysql1")
	want := "proxysql1.clustera.svc.cluster.local"
	if prx.Host != want {
		t.Fatalf("expected the parent cluster's own name, got %q (want %q)", prx.Host, want)
	}
}

// OpenSVC must keep its original, unfallback-ed shape exactly -- this fix
// is Kubernetes-only, since OpenSVC doesn't share Kubernetes' CoreDNS
// "cluster.local" default and never had this fallback.
func TestNewProxySQLProxy_OpenSVCUnaffectedByKubernetesFallback(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cluster.Conf.ProvOrchestratorCluster = "local"

	prx := NewProxySQLProxy(0, cluster, "proxysql1")
	want := "proxysql1.clustera.svc.local"
	if prx.Host != want {
		t.Fatalf("expected OpenSVC's raw, unfallback-ed suffix to be untouched, got %q (want %q)", prx.Host, want)
	}
}

// --- Config bootstrap resilience: bounded remote fetch + persisted PVC
// fallback ---
//
// An unreachable repman must never hang the init container forever, and an
// existing DB must be able to come back up during a repman outage from
// whatever config.tar.gz repman last successfully applied to its own PVC
// (k8sConfPersistSubPath/k8sInitPersistSubPath) -- mirroring OpenSVC's own
// bootstrap script, not a separate cached object.

// -T, not the GNU "--timeout"/"--tries" long forms: undocumented in
// busybox's own `wget --help`, unlike "-T SEC".
func TestK8SDatabaseDeployment_RemoteFetchIsBoundedWithTimeout(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	wantFetch := "wget -T 8 -qO /tmp/config.tar.gz"
	if !strings.Contains(cmd, wantFetch) {
		t.Fatalf("expected a bounded, non-streaming config fetch using the documented -T flag %q, got: %s", wantFetch, cmd)
	}
	if strings.Contains(cmd, "--timeout") || strings.Contains(cmd, "--tries") {
		t.Fatalf("expected no undocumented long-option flags (--timeout/--tries), got: %s", cmd)
	}
}

// Mirrors OpenSVC's own bootstrap script: the clear-and-replace of the
// persisted config/init directories only happens inside a successful
// fetch+extract, and the outer "if wget" gates the whole thing -- a failed
// fetch must never touch what's already persisted.
func TestK8SDatabaseDeployment_ConfigApplyGatedOnSuccessfulFetchAndExtract(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if !strings.Contains(cmd, "if wget") {
		t.Fatalf("expected the remote fetch to gate the whole apply step (an `if wget ...`), got: %s", cmd)
	}
	if !strings.Contains(cmd, "if tar xzf /tmp/config.tar.gz -C /tmp/cfg") {
		t.Fatalf("expected the extract to be its own nested gate (a failed/corrupt tarball must not clear persisted config either), got: %s", cmd)
	}
	for _, want := range []string{
		"rm -f /etc/mysql/conf.d/*.cnf",
		// Excludes replication-manager-cli, not a bare "rm -rf .../*":
		// that file isn't part of the config archive and shouldn't be
		// destroyed by a config refresh regardless of whether that same
		// boot's own (separate, best-effort) CLI re-fetch succeeds -- see
		// TestK8SDatabaseDeployment_SuccessfulConfigApplyPreservesCLIWhenItsOwnFetchFails.
		"find /docker-entrypoint-initdb.d -mindepth 1 ! -name replication-manager-cli -delete",
		"cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/",
		"cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected %q inside the gated apply step, got: %s", want, cmd)
		}
	}
	// The clears must come after both gates open, not before -- i.e. nested
	// inside them, not run unconditionally ahead of the fetch attempt.
	fetchIdx := strings.Index(cmd, "if wget")
	rmIdx := strings.Index(cmd, "rm -f /etc/mysql/conf.d/*.cnf")
	if fetchIdx < 0 || rmIdx < 0 || rmIdx < fetchIdx {
		t.Fatalf("expected the persisted-config clear to appear after the fetch gate opens, got: %s", cmd)
	}
}

// prov-db-start-fetch-config is not read here at all -- unlike an earlier
// version of this mechanism, the decision is made live, server-side, on
// every bootstrap attempt (need-config-fetch, mirroring OpenSVC's own
// bootstrap gate: see CheckNeedConfigFetch in cluster/srv_chk.go and
// handlerMuxServerNeedConfigFetch in server/api_database.go), not baked
// into the Deployment spec at build time. So toggling the flag takes
// effect on the pod's very next restart, with no reprovision required --
// and the generated command must be identical either way.
func TestK8SDatabaseDeployment_ConfigFetchGatedByLiveNeedConfigFetchCheck(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	cluster.Conf.ProvDbStartFetchConfig = true
	depTrue := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmdTrue := strings.Join(depTrue.Spec.Template.Spec.InitContainers[0].Command, " ")

	cluster.Conf.ProvDbStartFetchConfig = false
	depFalse := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmdFalse := strings.Join(depFalse.Spec.Template.Spec.InitContainers[0].Command, " ")

	if cmdTrue != cmdFalse {
		t.Fatalf("expected prov-db-start-fetch-config to have no effect on the generated command (the decision is live, server-side), got:\ntrue:  %s\nfalse: %s", cmdTrue, cmdFalse)
	}

	if !strings.Contains(cmdTrue, "/need-config-fetch") {
		t.Fatalf("expected a live need-config-fetch check in the init container command, got: %s", cmdTrue)
	}
	needIdx := strings.Index(cmdTrue, "/need-config-fetch")
	configIdx := strings.Index(cmdTrue, "-qO /tmp/config.tar.gz")
	if needIdx < 0 || configIdx < 0 || configIdx < needIdx {
		t.Fatalf("expected the need-config-fetch check to gate (appear before) the config fetch, got: %s", cmdTrue)
	}
}

// replication-manager-cli is only consumed by the dbjobs sidecar, never by
// mariadbd itself -- its fetch must be joined with ";", not "&&", so a
// failure (or unreachable repman) degrades the sidecar without blocking the
// chmod step (and so the database container's own startup) that follows it.
func TestK8SDatabaseDeployment_CLIFetchIsBestEffortNotBlocking(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	cmd := strings.Join(dep.Spec.Template.Spec.InitContainers[0].Command, " ")

	if !strings.Contains(cmd, "; wget") {
		t.Fatalf("expected the replication-manager-cli fetch joined with \";\", not \"&&\", so it can't block chmod, got: %s", cmd)
	}
	if !strings.Contains(cmd, "; chmod +x") {
		t.Fatalf("expected chmod joined with \";\" so it always runs regardless of the CLI fetch's outcome, got: %s", cmd)
	}
	if strings.Contains(cmd, "replication-manager-cli && chmod") {
		t.Fatalf("expected the CLI fetch to never gate chmod via \"&&\", got: %s", cmd)
	}
	// chmod on multiple targets still chmods every target that exists but
	// exits non-zero overall if even one is missing (confirmed:
	// `chmod +x present-file missing-file` -> exit 1). Simply joining with
	// ";" isn't enough on its own: "&&"/";" sit at the same precedence in
	// POSIX shell, so a flat list's own exit status is always whatever its
	// last command returns. The script instead captures mkdir's own exit
	// status into MKDIR_STATUS *before* the fully best-effort
	// fetch/apply/CLI/chmod tail runs, and re-asserts it with an explicit
	// "exit" at the very end -- see
	// TestK8SDatabaseDeployment_CLIFetchFailureDoesNotFailInitContainer
	// (this tail can't fail the container), exercised through a real
	// shell, not just this source-text check.
	if !strings.Contains(cmd, "; MKDIR_STATUS=$? ; ") {
		t.Fatalf("expected mkdir's own exit status captured into MKDIR_STATUS before the best-effort tail runs, got: %s", cmd)
	}
	if !strings.HasSuffix(strings.TrimSpace(cmd), `exit "$MKDIR_STATUS"`) {
		t.Fatalf("expected the script to end by re-asserting MKDIR_STATUS as its own exit code (not whatever chmod/wget/tar happened to return), got: %s", cmd)
	}
}

// TestK8SDatabaseDeployment_CLIFetchIsBestEffortNotBlocking checks the
// command's shape; this exercises the actual exit-code behavior of the
// generated script's CLI-fetch-and-chmod tail through a real shell, proving
// a missing replication-manager-cli genuinely can't fail the init
// container -- not just that the source text looks right.
func TestK8SDatabaseDeployment_CLIFetchFailureDoesNotFailInitContainer(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	script := dep.Spec.Template.Spec.InitContainers[0].Command[2]

	// Isolate the MKDIR_STATUS-capture-and-reassert tail (everything from
	// "MKDIR_STATUS=$? ; " onward, i.e. the apply-config/CLI-fetch/chmod/exit
	// portion) -- the earlier mkdir step needs real filesystem paths this
	// test doesn't have, and isn't what's under test here. Prepending
	// "MKDIR_STATUS=0 ; " simulates mkdir having already succeeded, which is
	// what makes "exit \"$MKDIR_STATUS\"" at the end valid on its own and is
	// exactly the scenario under test: given mkdir succeeded, does a failing
	// CLI fetch still exit 0?
	marker := "MKDIR_STATUS=$? ; "
	idx := strings.Index(script, marker)
	if idx < 0 {
		t.Fatalf("expected to find the MKDIR_STATUS-capture marker in the generated script: %s", script)
	}
	tail := "MKDIR_STATUS=0 ; " + script[idx+len(marker):]

	// The real script writes into the hardcoded absolute path
	// /docker-entrypoint-initdb.d, which this test can't write to --
	// relocate to a temp dir instead. /tmp/cfg and /etc/mysql/conf.d are
	// left as their real hardcoded paths deliberately: with the stubbed
	// wget below always failing, the "if wget ...; then ...; fi" gate
	// around every reference to them in applyConfig never opens, so they're
	// never actually touched -- confirmed by this test passing without
	// permission errors. The operators (";") and flags are otherwise
	// exercised exactly as production generates them.
	tmpDir := t.TempDir()
	tail = strings.ReplaceAll(tail, "/docker-entrypoint-initdb.d", tmpDir)

	// Reproduce exactly the situation under test: dbjobs_new and
	// dbjobs_launcher_with_sigterm already populated (normally by the
	// earlier "cp -r .../init/." step), but replication-manager-cli is not
	// -- because the fetch below is stubbed to always fail, simulating an
	// unreachable repman.
	for _, f := range []string{"dbjobs_new", "dbjobs_launcher_with_sigterm"} {
		if err := os.WriteFile(tmpDir+"/"+f, nil, 0644); err != nil {
			t.Fatalf("setup: %s", err)
		}
	}
	binDir := t.TempDir()
	if err := os.WriteFile(binDir+"/wget", []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", tail)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container's tail to exit 0 even when the CLI fetch fails (a missing CLI must never fail the whole init container), got error %s, output: %s\nscript: %s", err, out, tail)
	}

	for _, f := range []string{"dbjobs_new", "dbjobs_launcher_with_sigterm"} {
		info, err := os.Stat(tmpDir + "/" + f)
		if err != nil {
			t.Fatalf("expected %s to still exist: %s", f, err)
		}
		if info.Mode()&0111 == 0 {
			t.Fatalf("expected %s to still be chmod +x'd despite the CLI fetch failing, mode: %s", f, info.Mode())
		}
	}
	if _, err := os.Stat(tmpDir + "/replication-manager-cli"); err == nil {
		t.Fatal("expected replication-manager-cli to not exist, since the stubbed wget always fails")
	}
}

// buildRelocatedInitScript returns the init container's generated script
// with every hardcoded absolute path it touches (image-provided
// directories, the PVC-backed datadir and its subPath-mounted conf.d/init)
// relocated under tmpDir, none of which a test can or should touch
// directly -- /var/lib/mysql included, since a machine where the test user
// happens to have write access there could otherwise touch a real,
// unrelated MariaDB install. conf.d and init-out are pre-created (matching
// their being subPath mount points, already existing, in production) so a
// test can seed "persisted from a previous boot" content before running.
func buildRelocatedInitScript(t *testing.T, cluster *Cluster, s *ServerMonitor, tmpDir string) string {
	t.Helper()
	dep := cluster.k8sDatabaseDeployment(s, 3306, "node-a")
	script := dep.Spec.Template.Spec.InitContainers[0].Command[2]
	for _, r := range [][2]string{
		{"/tmp/config.tar.gz", tmpDir + "/config.tar.gz"},
		{"/tmp/cfg", tmpDir + "/cfg"},
		// A leading space, not the bare path: "/tmp/cfg/etc/mysql/conf.d"
		// (the archive's own source path, already relocated above)
		// contains "/etc/mysql/conf.d" as a substring with no space before
		// it, so a bare ReplaceAll would corrupt that path too. Every real
		// destination occurrence is its own argument, preceded by a space.
		{" /etc/mysql/conf.d", " " + tmpDir + "/conf.d"},
		{"/docker-entrypoint-initdb.d", tmpDir + "/init-out"},
		{"/var/lib/mysql", tmpDir + "/var-lib-mysql"},
	} {
		script = strings.ReplaceAll(script, r[0], r[1])
	}
	for _, dir := range []string{"conf.d", "init-out"} {
		if err := os.MkdirAll(tmpDir+"/"+dir, 0755); err != nil {
			t.Fatalf("setup: %s", err)
		}
	}
	return script
}

// wgetStubFindingDashQO returns a POSIX sh script for a stub "wget" binary
// that locates the path following "-qO" in its own arguments (matching
// exactly how the generated command invokes it: "wget ... -qO <path> <url>")
// and, if action isn't empty, runs it with that path as $1 before exiting 0;
// with action empty, it just exits 1 (simulating an unreachable repman).
func wgetStubFindingDashQO(action string) string {
	if action == "" {
		return "#!/bin/sh\nexit 1\n"
	}
	return "#!/bin/sh\nprev=\"\"\nout=\"\"\nfor a in \"$@\"; do\n  if [ \"$prev\" = \"-qO\" ]; then out=\"$a\"; fi\n  prev=\"$a\"\ndone\n" + action + "\nexit 0\n"
}

// wgetStubDual returns a stub "wget" that runs a different action
// depending on which destination file it's asked to write to (found via
// "-qO", same as wgetStubFindingDashQO) -- config.tar.gz vs
// replication-manager-cli.new -- so a single test run can make the config
// fetch and the CLI fetch succeed/fail independently. Each action string
// must end with its own "exit" (0 or 1); this stub doesn't add one.
func wgetStubDual(configAction, cliAction string) string {
	return "#!/bin/sh\nprev=\"\"\nout=\"\"\nfor a in \"$@\"; do\n  if [ \"$prev\" = \"-qO\" ]; then out=\"$a\"; fi\n  prev=\"$a\"\ndone\n" +
		"case \"$out\" in\n" +
		"  */config.tar.gz) " + configAction + " ;;\n" +
		"  */replication-manager-cli.new) " + cliAction + " ;;\n" +
		"esac\n"
}

// Mirrors OpenSVC's own bootstrap script: a failed fetch must leave
// whatever's already persisted in conf.d/init exactly as it was, and the
// init container must still succeed -- OpenSVC's own "optional=true"
// resource means the same thing (a failed/timed-out bootstrap container
// never blocks the service). Only mkdir's own exit status can fail the
// container now, not a fetch/extract/apply failure.
func TestK8SDatabaseDeployment_FetchFailureLeavesPersistedConfigUntouchedAndStillSucceeds(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	tmpDir := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, s, tmpDir)
	if err := os.WriteFile(tmpDir+"/conf.d/old-good.cnf", []byte("old-good-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}

	binDir := t.TempDir()
	if err := os.WriteFile(binDir+"/wget", []byte(wgetStubFindingDashQO("")), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container to still succeed when repman is unreachable, got error %s, output: %s\nscript: %s", err, out, script)
	}
	got, err := os.ReadFile(tmpDir + "/conf.d/old-good.cnf")
	if err != nil || string(got) != "old-good-content\n" {
		t.Fatalf("expected the persisted config to be left untouched, got content %q, err %v", got, err)
	}
}

// A successful wget with a corrupt/truncated body must be treated the same
// as a failed one: the inner "if tar ...; then" gate means a failed extract
// never reaches the clear-and-replace step either, leaving persisted
// config untouched -- same as OpenSVC's own script, which doesn't check
// its own "tar xzvf" exit code any more strictly than this.
func TestK8SDatabaseDeployment_CorruptTarballLeavesPersistedConfigUntouchedAndStillSucceeds(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	tmpDir := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, s, tmpDir)
	if err := os.WriteFile(tmpDir+"/conf.d/old-good.cnf", []byte("old-good-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}

	binDir := t.TempDir()
	if err := os.WriteFile(binDir+"/wget", []byte(wgetStubFindingDashQO(`echo "not a real tarball" > "$out"`)), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container to still succeed despite a corrupt download, got error %s, output: %s\nscript: %s", err, out, script)
	}
	got, err := os.ReadFile(tmpDir + "/conf.d/old-good.cnf")
	if err != nil || string(got) != "old-good-content\n" {
		t.Fatalf("expected the persisted config to be left untouched by a corrupt tarball, got content %q, err %v", got, err)
	}
}

// The other side of the same guarantee: a genuinely successful fetch must
// actually clear stale persisted files, not just overlay new ones on top --
// matching OpenSVC's own script's "rm -f .../conf.d/*; rm -rf .../init/*"
// ahead of its own extract. A variable removed server-side must actually
// disappear here, not linger forever once persisted.
func TestK8SDatabaseDeployment_SuccessfulFetchClearsAndReplacesPersistedConfig(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	tmpDir := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, s, tmpDir)
	if err := os.WriteFile(tmpDir+"/conf.d/old-good.cnf", []byte("old-good-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}

	// A real, valid archive shaped like the ones configurator.TarGz builds:
	// etc/mysql/conf.d/*.cnf and init/* at its root.
	fixtureRoot := t.TempDir()
	if err := os.MkdirAll(fixtureRoot+"/etc/mysql/conf.d", 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.MkdirAll(fixtureRoot+"/init", 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.WriteFile(fixtureRoot+"/etc/mysql/conf.d/new.cnf", []byte("new-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.WriteFile(fixtureRoot+"/init/dbjobs_new", []byte("new-dbjobs-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}
	fixtureTarball := t.TempDir() + "/fixture.tar.gz"
	tarCmd := exec.Command("tar", "czf", fixtureTarball, "-C", fixtureRoot, "etc", "init")
	if out, err := tarCmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: tar: %s: %s", err, out)
	}

	binDir := t.TempDir()
	if err := os.WriteFile(binDir+"/wget", []byte(wgetStubFindingDashQO(`cp `+fixtureTarball+` "$out"`)), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container to succeed on a genuinely successful fetch, got error %s, output: %s\nscript: %s", err, out, script)
	}

	if _, statErr := os.Stat(tmpDir + "/conf.d/old-good.cnf"); statErr == nil {
		t.Fatal("expected the stale old-good.cnf to have been cleared by a successful fetch")
	}
	got, err := os.ReadFile(tmpDir + "/conf.d/new.cnf")
	if err != nil || string(got) != "new-content\n" {
		t.Fatalf("expected the freshly fetched new.cnf to be applied, got content %q, err %v", got, err)
	}
	got, err = os.ReadFile(tmpDir + "/init-out/dbjobs_new")
	if err != nil || string(got) != "new-dbjobs-content\n" {
		t.Fatalf("expected the freshly fetched init/dbjobs_new to be applied, got content %q, err %v", got, err)
	}
}

// The wipe on a successful config apply must never destroy a previously
// cached replication-manager-cli, even when that same boot's own
// (unrelated, best-effort) CLI re-fetch fails -- it isn't part of the
// config archive and shouldn't be held hostage to a config refresh
// succeeding.
func TestK8SDatabaseDeployment_SuccessfulConfigApplyPreservesCLIWhenItsOwnFetchFails(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	tmpDir := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, s, tmpDir)

	// A previously-cached CLI, from an earlier successful fetch.
	if err := os.WriteFile(tmpDir+"/init-out/replication-manager-cli", []byte("old-good-cli-binary\n"), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	fixtureRoot := t.TempDir()
	if err := os.MkdirAll(fixtureRoot+"/etc/mysql/conf.d", 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.MkdirAll(fixtureRoot+"/init", 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.WriteFile(fixtureRoot+"/etc/mysql/conf.d/new.cnf", []byte("new-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}
	if err := os.WriteFile(fixtureRoot+"/init/dbjobs_new", []byte("new-dbjobs-content\n"), 0644); err != nil {
		t.Fatalf("setup: %s", err)
	}
	fixtureTarball := t.TempDir() + "/fixture.tar.gz"
	tarCmd := exec.Command("tar", "czf", fixtureTarball, "-C", fixtureRoot, "etc", "init")
	if out, err := tarCmd.CombinedOutput(); err != nil {
		t.Fatalf("setup: tar: %s: %s", err, out)
	}

	binDir := t.TempDir()
	stub := wgetStubDual(`cp `+fixtureTarball+` "$out"; exit 0`, `exit 1`)
	if err := os.WriteFile(binDir+"/wget", []byte(stub), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container to still succeed, got error %s, output: %s\nscript: %s", err, out, script)
	}

	got, err := os.ReadFile(tmpDir + "/init-out/replication-manager-cli")
	if err != nil || string(got) != "old-good-cli-binary\n" {
		t.Fatalf("expected the previously-cached CLI to survive a successful config apply despite its own re-fetch failing, got content %q, err %v", got, err)
	}
	if _, statErr := os.Stat(tmpDir + "/conf.d/new.cnf"); statErr != nil {
		t.Fatalf("expected the config apply itself to have succeeded regardless: %s", statErr)
	}
}

// busybox wget's "-qO" has no atomic rename: a download truncated
// mid-transfer leaves the partial garbage at that path despite the nonzero
// exit. Fetching to a temp file first and only `cp`-ing on success is what
// prevents that from corrupting a previously-good cached CLI.
func TestK8SDatabaseDeployment_TruncatedCLIDownloadDoesNotCorruptCachedCLI(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cluster := newTestCluster("k8stest")
	cluster.Conf.MonitorAddress = "127.0.0.1"
	cluster.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306"}

	tmpDir := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, s, tmpDir)
	if err := os.WriteFile(tmpDir+"/init-out/replication-manager-cli", []byte("old-good-cli-binary\n"), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	binDir := t.TempDir()
	// Simulates a connection that drops mid-transfer: writes garbage to its
	// own "-qO" destination (the temp file, not the final one, if the fix
	// holds) before exiting nonzero -- exactly what a real truncated
	// download does.
	stub := wgetStubFindingDashQO(`echo "TRUNCATED_GARBAGE" > "$out"; exit 1`)
	if err := os.WriteFile(binDir+"/wget", []byte(stub), 0755); err != nil {
		t.Fatalf("setup: %s", err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected the init container to still succeed, got error %s, output: %s\nscript: %s", err, out, script)
	}

	got, err := os.ReadFile(tmpDir + "/init-out/replication-manager-cli")
	if err != nil || string(got) != "old-good-cli-binary\n" {
		t.Fatalf("expected the cached CLI to survive a truncated download untouched, got content %q, err %v", got, err)
	}
}

// --- HAProxy K8s provisioning ---

func newTestHaproxyProxy(cluster *Cluster) *fakeProxy {
	return &fakeProxy{
		name:      "haproxy1",
		host:      "haproxy1",
		port:      "1999",
		proxyType: config.ConstProxyHaproxy,
		writePort: 3306,
		readPort:  3307,
		cluster:   cluster,
	}
}

func TestK8SProxyImage_HaproxyUsesProvProxHaproxyImg(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvProxHaproxyImg = "haproxytech/haproxy-alpine:2.4"
	prx := newTestHaproxyProxy(cluster)

	image, err := cluster.k8sProxyImage(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if image != "haproxytech/haproxy-alpine:2.4" {
		t.Fatalf("expected the configured HAProxy image, got %q", image)
	}
}

func TestK8SProxyContainerPorts_HaproxyExposesAdminWriteReadStat(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.HaproxyStatPort = 1988
	prx := newTestHaproxyProxy(cluster)

	ports, err := k8sProxyContainerPorts(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	want := map[string]int32{"admin": 1999, "write": 3306, "read": 3307, "stat": 1988}
	if len(ports) != len(want) {
		t.Fatalf("expected %d ports, got %d: %+v", len(want), len(ports), ports)
	}
	for _, p := range ports {
		wp, ok := want[p.Name]
		if !ok {
			t.Fatalf("unexpected port name %q in %+v", p.Name, ports)
		}
		if p.ContainerPort != wp {
			t.Fatalf("port %q: expected %d, got %d", p.Name, wp, p.ContainerPort)
		}
	}
}

func TestK8SProxyServicePorts_HaproxyMatchesContainerPorts(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.HaproxyStatPort = 1988
	prx := newTestHaproxyProxy(cluster)

	ports, err := k8sProxyServicePorts(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	want := map[string]int32{"admin": 1999, "write": 3306, "read": 3307, "stat": 1988}
	if len(ports) != len(want) {
		t.Fatalf("expected %d ports, got %d: %+v", len(want), len(ports), ports)
	}
	for _, p := range ports {
		wp, ok := want[p.Name]
		if !ok {
			t.Fatalf("unexpected port name %q in %+v", p.Name, ports)
		}
		if p.Port != wp {
			t.Fatalf("port %q: expected %d, got %d", p.Name, wp, p.Port)
		}
	}
}

func TestK8SProxyDeployment_HaproxyMountsConfDirSubPath(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	found := false
	for _, m := range container.VolumeMounts {
		if m.MountPath == "/usr/local/etc/haproxy" {
			found = true
			if m.SubPath != k8sHaproxyConfPersistSubPath {
				t.Fatalf("expected subPath %q, got %q", k8sHaproxyConfPersistSubPath, m.SubPath)
			}
		}
	}
	if !found {
		t.Fatalf("expected a /usr/local/etc/haproxy mount, got %+v", container.VolumeMounts)
	}
}

func TestK8SProxyDeployment_HaproxyNoCommandOverride(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.Command != nil {
		t.Fatalf("expected no Command override for HAProxy (rely on the image's own entrypoint), got %+v", container.Command)
	}
}

func TestK8SProxyDeployment_HaproxyExternalCheckModePlacesCheckScriptsAndUsesCheckConfig(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.HaproxyMode = "externalcheck"
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.Command) < 3 {
		t.Fatalf("expected a sh -c command for haproxy-mode=externalcheck, got %+v", container.Command)
	}
	script := container.Command[2]
	for _, want := range []string{
		"cp /usr/local/etc/haproxy/checkmaster /usr/bin/checkmaster",
		"cp /usr/local/etc/haproxy/checkslave /usr/bin/checkslave",
		"chmod +x /usr/bin/checkmaster /usr/bin/checkslave",
		"ulimit -Sn 1024",
		"ulimit -Hn 1048576",
		"exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected externalcheck-mode command to contain %q, got: %s", want, script)
		}
	}

	// The copy into /usr/bin only succeeds if the container runs as root:
	// haproxytech/haproxy-alpine defaults to root, but the official Debian
	// haproxy:<tag> image drops to uid 99 via its own USER directive, which
	// would otherwise leave /usr/bin unwritable and the copy a silent no-op.
	if container.SecurityContext == nil || container.SecurityContext.RunAsUser == nil {
		t.Fatalf("expected externalcheck-mode container to force RunAsUser=0, got SecurityContext=%+v", container.SecurityContext)
	}
	if got := *container.SecurityContext.RunAsUser; got != 0 {
		t.Fatalf("expected externalcheck-mode container to force RunAsUser=0, got %d", got)
	}
}

func TestK8SProxyDeployment_HaproxyRuntimeAPIModeDoesNotForceRoot(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.HaproxyMode = "runtimeapi"
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.SecurityContext != nil {
		t.Fatalf("expected no SecurityContext override outside externalcheck mode, got %+v", container.SecurityContext)
	}
}

func TestK8SProxyDeployment_HaproxyStandbyModeNoCommandOverride(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.HaproxyMode = "standby"
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if container.Command != nil {
		t.Fatalf("expected no Command override for haproxy-mode=standby (rely on the image's own entrypoint against haproxy.cfg), got %+v", container.Command)
	}
	if container.SecurityContext != nil {
		t.Fatalf("expected no SecurityContext override for haproxy-mode=standby, got %+v", container.SecurityContext)
	}
}

func TestK8SProxyDeployment_HaproxyInitContainerAppliesConfigAndScripts(t *testing.T) {
	cluster := newTestCluster("k8stest")
	prx := newTestHaproxyProxy(cluster)

	dep, err := cluster.k8sProxyDeployment(prx)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(dep.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected exactly one init container, got %d", len(dep.Spec.Template.Spec.InitContainers))
	}
	initContainer := dep.Spec.Template.Spec.InitContainers[0]
	if len(initContainer.Command) < 3 {
		t.Fatalf("expected a sh -c command, got %+v", initContainer.Command)
	}
	script := initContainer.Command[2]
	for _, want := range []string{
		"mkdir -p",
		"/usr/local/etc/haproxy",
		"cp -r /tmp/cfg/etc/haproxy/. /usr/local/etc/haproxy/",
		"/tmp/cfg/init/checkmaster",
		"/tmp/cfg/init/checkslave",
		"chmod +x",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected init command to contain %q, got: %s", want, script)
		}
	}

	for _, m := range initContainer.VolumeMounts {
		if m.MountPath == "/usr/local/etc/haproxy" && m.SubPath != k8sHaproxyConfPersistSubPath {
			t.Fatalf("expected init container's /usr/local/etc/haproxy mount to share the main container's subPath, got %q", m.SubPath)
		}
	}
}

func TestK8SProvisionProxy_HaproxyCreatesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := newTestHaproxyProxy(cluster)

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if _, err := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), k8sProxyPVCName("k8stest", "haproxy1"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected a PVC to be created for HAProxy: %s", err)
	}
}

func TestK8SUnprovisionProxy_HaproxyRetainsPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	cluster := newTestCluster("k8stest")
	prx := newTestHaproxyProxy(cluster)

	if err := cluster.k8sProvisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error provisioning: %s", err)
	}
	if err := cluster.k8sUnprovisionProxyServiceWithClient(client, prx); err != nil {
		t.Fatalf("unexpected error unprovisioning: %s", err)
	}

	if _, err := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), k8sProxyPVCName("k8stest", "haproxy1"), metav1.GetOptions{}); err != nil {
		t.Fatalf("expected the PVC to survive unprovision: %s", err)
	}
}

func TestNewHaproxyProxy_KubernetesFallsBackToClusterLocalWhenUnset(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "" // never configured, same as the CLI default "local"

	prx := NewHaproxyProxy(0, cluster, "haproxy1")
	want := "haproxy1.clustera.svc.cluster.local"
	if prx.Host != want {
		t.Fatalf("expected %q, got %q", want, prx.Host)
	}
}

func TestNewHaproxyProxy_KubernetesFallsBackWhenLiteralCLIDefault(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "local"

	prx := NewHaproxyProxy(0, cluster, "haproxy1")
	want := "haproxy1.clustera.svc.cluster.local"
	if prx.Host != want {
		t.Fatalf("expected the CLI default \"local\" to fall back to cluster.local, got %q (want %q)", prx.Host, want)
	}
}

func TestNewHaproxyProxy_KubernetesTracksConfiguredClusterDomain(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cluster.Conf.ProvOrchestratorCluster = "k8s.internal"

	prx := NewHaproxyProxy(0, cluster, "haproxy1")
	want := "haproxy1.clustera.svc.k8s.internal"
	if prx.Host != want {
		t.Fatalf("expected the host to track the configured cluster domain, got %q (want %q)", prx.Host, want)
	}
}

func TestNewHaproxyProxy_OpenSVCUnaffectedByKubernetesFallback(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.ProvNetCNI = true
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cluster.Conf.ProvOrchestratorCluster = "local"

	prx := NewHaproxyProxy(0, cluster, "haproxy1")
	want := "haproxy1.clustera.svc.local"
	if prx.Host != want {
		t.Fatalf("expected OpenSVC's raw, unfallback-ed suffix to be untouched, got %q (want %q)", prx.Host, want)
	}
}
