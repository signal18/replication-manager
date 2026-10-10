package cluster

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes/fake"
)

func TestK8SDatabaseManifests_FetchesLiveDeploymentServicePVCPods(t *testing.T) {
	cluster := newTestCluster("clustera")
	s := &ServerMonitor{Name: "clustera-0", Port: "3306"}

	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "clustera-0", Namespace: "clustera"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(1),
			},
		},
		&apiv1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "clustera-0", Namespace: "clustera"},
		},
		&apiv1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "clustera-clustera-0-claim", Namespace: "clustera"},
		},
		&apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clustera-0-abc123",
				Namespace: "clustera",
				Labels:    map[string]string{"app": "repication-manager", "tag": "clustera-0"},
			},
			Status: apiv1.PodStatus{Phase: apiv1.PodRunning},
		},
	)

	m := cluster.k8sDatabaseManifestsFromClient(client, s)

	if !strings.Contains(m.Deployment, "kind: Deployment") {
		t.Fatalf("expected Deployment YAML with kind set, got: %s", m.Deployment)
	}
	if !strings.Contains(m.Service, "kind: Service") {
		t.Fatalf("expected Service YAML with kind set, got: %s", m.Service)
	}
	if !strings.Contains(m.PVC, "kind: PersistentVolumeClaim") {
		t.Fatalf("expected PVC YAML with kind set, got: %s", m.PVC)
	}
	if !strings.Contains(m.Pods, "clustera-0-abc123") || !strings.Contains(m.Pods, "Running") {
		t.Fatalf("expected Pods YAML to include the live pod's name and phase, got: %s", m.Pods)
	}
}

// A server with no Deployment yet (e.g. provisioning failed before that
// step, or it was never provisioned) must surface *why* each section is
// empty, not silently omit it or 500 the whole response -- the GUI always
// gets a 200 with this struct (see K8SGetDatabaseManifests).
func TestK8SDatabaseManifests_MissingResourcesReportNotFoundNotPanic(t *testing.T) {
	cluster := newTestCluster("clustera")
	s := &ServerMonitor{Name: "clustera-9", Port: "3306"}
	client := fake.NewSimpleClientset()

	m := cluster.k8sDatabaseManifestsFromClient(client, s)

	for name, got := range map[string]string{"Deployment": m.Deployment, "Service": m.Service, "PVC": m.PVC} {
		if !strings.Contains(got, "not found") {
			t.Fatalf("expected %s section to report a not-found error, got: %s", name, got)
		}
	}
	// An empty pod list is not an error -- List() succeeds with zero items.
	if strings.Contains(m.Pods, "not found") {
		t.Fatalf("expected an empty pod list, not a not-found error, got: %s", m.Pods)
	}
}

// The pod selector must match exactly what k8sDatabaseDeployment actually
// labels pods with (app=repication-manager, tag=<server name>) -- a pod
// belonging to a different server in the same namespace must never leak
// into this server's manifest view.
func TestK8SDatabaseManifests_PodSelectorScopedToServer(t *testing.T) {
	cluster := newTestCluster("clustera")
	s := &ServerMonitor{Name: "clustera-0", Port: "3306"}
	client := fake.NewSimpleClientset(
		&apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clustera-0-pod",
				Namespace: "clustera",
				Labels:    map[string]string{"app": "repication-manager", "tag": "clustera-0"},
			},
		},
		&apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clustera-1-pod",
				Namespace: "clustera",
				Labels:    map[string]string{"app": "repication-manager", "tag": "clustera-1"},
			},
		},
	)

	m := cluster.k8sDatabaseManifestsFromClient(client, s)

	if !strings.Contains(m.Pods, "clustera-0-pod") {
		t.Fatalf("expected clustera-0's own pod in the result, got: %s", m.Pods)
	}
	if strings.Contains(m.Pods, "clustera-1-pod") {
		t.Fatalf("expected clustera-1's pod to be excluded, got: %s", m.Pods)
	}
}

// K8SConnectAPI failure (unreachable kubeconfig, etc.) must still return a
// 200-shaped struct with the error visible in every section, matching what
// the API handler always does -- not a nil struct or a panic.
func TestK8SGetDatabaseManifests_ConnectFailureReportsErrorInEverySection(t *testing.T) {
	cluster := newTestCluster("clustera")
	cluster.Conf.KubeConfig = "/nonexistent/kubeconfig-path-for-test"
	s := &ServerMonitor{Name: "clustera-0", Port: "3306"}

	m := cluster.K8SGetDatabaseManifests(s)

	for name, got := range map[string]string{"Deployment": m.Deployment, "Service": m.Service, "PVC": m.PVC, "Pods": m.Pods} {
		if !strings.HasPrefix(got, "# ") {
			t.Fatalf("expected %s section to render the connect error as a YAML comment, got: %s", name, got)
		}
	}
}
