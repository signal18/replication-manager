package cluster

// Single-writer safety of the Kubernetes database lifecycle (issue #2389).
//
// These tests use the fake clientset: they prove repman's own logic (what it patches, what it waits
// for, what it refuses), NOT the ordering of a real Deployment controller or kubelet. The real
// sequencing (old Pod terminated before the new one is created, foreground deletion completing, the
// strategy migration accepted by an API server) is covered by the runtime tests of the issue.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	swNS    = "k8stest"
	swClaim = "k8stest-db1-claim"
	// tiny waits so the timeout cases run in milliseconds
	swTimeout = 300 * time.Millisecond
	swPoll    = 5 * time.Millisecond
)

// swDeployment is a database Deployment as an older repman created it: RollingUpdate (inherited),
// the data PVC in its template, a main and a dbjobs container, labels, a node pin and an annotation,
// i.e. fields the migration must leave alone.
func swDeployment(replicas int32, strategy appsv1.DeploymentStrategy) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: swNS, Labels: map[string]string{"keep": "me"}, Annotations: map[string]string{"owner": "repman"}},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: strategy,
			Template: apiv1.PodTemplateSpec{
				Spec: apiv1.PodSpec{
					NodeSelector: map[string]string{"kubernetes.io/hostname": "node-a"},
					Containers: []apiv1.Container{
						{Name: "db1", Image: "mariadb:11.8.9", Env: []apiv1.EnvVar{{Name: "KEEP", Value: "1"}}},
						{Name: "db1-dbjobs", Image: "mariadb:11.8.9"},
					},
					Volumes: []apiv1.Volume{{Name: "db1-data", VolumeSource: apiv1.VolumeSource{
						PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: swClaim}}}},
				},
			},
		},
	}
}

func swRollingUpdate() appsv1.DeploymentStrategy {
	maxSurge, maxUnavailable := intstr.FromString("25%"), intstr.FromString("25%")
	return appsv1.DeploymentStrategy{
		Type:          appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &maxSurge, MaxUnavailable: &maxUnavailable},
	}
}

func swRecreate() appsv1.DeploymentStrategy {
	return appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
}

// swPod is a Pod of the namespace mounting claim (empty: no PVC); terminating sets a deletion timestamp.
func swPod(name, claim string, terminating bool) *apiv1.Pod {
	pod := &apiv1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: swNS}, Status: apiv1.PodStatus{Phase: apiv1.PodRunning}}
	if claim != "" {
		pod.Spec.Volumes = []apiv1.Volume{{Name: "data", VolumeSource: apiv1.VolumeSource{
			PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}}
	}
	if terminating {
		now := metav1.Now()
		pod.DeletionTimestamp = &now
	}
	return pod
}

func swReplicas(t *testing.T, client *fake.Clientset) int32 {
	t.Helper()
	dep, err := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %s", err)
	}
	return *dep.Spec.Replicas
}

// swCountActions counts the recorded API actions of a verb on a resource.
func swCountActions(client *fake.Clientset, verb, resource string) int {
	n := 0
	for _, a := range client.Actions() {
		if a.Matches(verb, resource) {
			n++
		}
	}
	return n
}

// --- 1. strategy: new Deployments and the migration of legacy ones ---

func TestK8SDatabaseDeployment_UsesRecreateStrategy(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("expected the Recreate strategy, got %q", dep.Spec.Strategy.Type)
	}
	if dep.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("expected no rollingUpdate block with Recreate, got %+v", dep.Spec.Strategy.RollingUpdate)
	}
}

func TestK8SRecreateStrategyPatch(t *testing.T) {
	leftover := swRecreate()
	leftover.RollingUpdate = &appsv1.RollingUpdateDeployment{}
	tests := []struct {
		name      string
		dep       *appsv1.Deployment
		wantPatch bool
	}{
		{"legacy RollingUpdate migrates", swDeployment(1, swRollingUpdate()), true},
		{"strategy never set (empty type) migrates", swDeployment(1, appsv1.DeploymentStrategy{}), true},
		{"Recreate with a stale rollingUpdate block is cleaned", swDeployment(1, leftover), true},
		{"already Recreate sends nothing", swDeployment(1, swRecreate()), false},
		{"unknown deployment (nil) sends the idempotent patch", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := k8sRecreateStrategyPatch(tt.dep)
			if (got != nil) != tt.wantPatch {
				t.Fatalf("patch = %v, want a patch: %v", got, tt.wantPatch)
			}
			if got != nil {
				raw, _ := json.Marshal(got)
				if string(raw) != `{"rollingUpdate":null,"type":"Recreate"}` {
					t.Fatalf("patch body %s must set Recreate and null out rollingUpdate", raw)
				}
			}
		})
	}
}

// swMigrationOps are the three existing lifecycle operations that carry the strategy migration.
var swMigrationOps = []struct {
	name   string
	replic int32
	run    func(cluster *Cluster, client *fake.Clientset) error
}{
	{"restart", 1, func(c *Cluster, cl *fake.Clientset) error { return c.k8sRestartDatabaseServiceWithClient(cl, "db1") }},
	{"force re-pull", 1, func(c *Cluster, cl *fake.Clientset) error {
		return c.k8sForceRepullDatabaseServiceWithClient(cl, "db1")
	}},
	{"update config (image change)", 0, func(c *Cluster, cl *fake.Clientset) error {
		return c.k8sUpdateDatabaseServiceConfigWithClient(cl, "db1", true)
	}},
}

func TestK8SLifecyclePatches_MigrateLegacyDeploymentToRecreate(t *testing.T) {
	for _, op := range swMigrationOps {
		t.Run(op.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(swDeployment(op.replic, swRollingUpdate()))
			if err := op.run(newTestCluster(swNS), client); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			dep, _ := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
			if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Fatalf("strategy %q, want Recreate", dep.Spec.Strategy.Type)
			}
			if dep.Spec.Strategy.RollingUpdate != nil {
				t.Fatalf("the old rollingUpdate block must be cleared, got %+v", dep.Spec.Strategy.RollingUpdate)
			}
		})
	}
}

// the migration changes the strategy and nothing else the operation does not already change
func TestK8SLifecyclePatches_MigrationPreservesUnrelatedFields(t *testing.T) {
	for _, op := range swMigrationOps {
		t.Run(op.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(swDeployment(op.replic, swRollingUpdate()))
			if err := op.run(newTestCluster(swNS), client); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			dep, _ := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
			if *dep.Spec.Replicas != op.replic {
				t.Errorf("replicas %d, want %d", *dep.Spec.Replicas, op.replic)
			}
			if dep.Labels["keep"] != "me" || dep.Annotations["owner"] != "repman" {
				t.Errorf("deployment labels or annotations lost: %v %v", dep.Labels, dep.Annotations)
			}
			pod := dep.Spec.Template.Spec
			if pod.NodeSelector["kubernetes.io/hostname"] != "node-a" {
				t.Errorf("node pin lost: %v", pod.NodeSelector)
			}
			if len(pod.Containers) != 2 || pod.Containers[0].Name != "db1" || pod.Containers[1].Name != "db1-dbjobs" {
				t.Fatalf("containers changed: %+v", pod.Containers)
			}
			if len(pod.Containers[0].Env) != 1 || pod.Containers[0].Env[0].Name != "KEEP" {
				t.Errorf("container env lost: %+v", pod.Containers[0].Env)
			}
			if len(pod.Volumes) != 1 || pod.Volumes[0].PersistentVolumeClaim == nil || pod.Volumes[0].PersistentVolumeClaim.ClaimName != swClaim {
				t.Errorf("PVC volume lost: %+v", pod.Volumes)
			}
		})
	}
}

// an up-to-date Deployment gets no strategy in the patch; a legacy one gets it in the SAME patch the
// operation already sends (no extra request, so no extra Pod replacement of its own)
func TestK8SLifecyclePatches_SendStrategyOnlyWhenNeeded(t *testing.T) {
	for _, op := range swMigrationOps {
		for _, tc := range []struct {
			name     string
			strategy appsv1.DeploymentStrategy
			want     bool
		}{{"legacy", swRollingUpdate(), true}, {"already Recreate", swRecreate(), false}} {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				client := fake.NewSimpleClientset(swDeployment(op.replic, tc.strategy))
				var bodies []string
				client.PrependReactor("patch", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
					bodies = append(bodies, string(a.(k8stesting.PatchAction).GetPatch()))
					return false, nil, nil
				})
				if err := op.run(newTestCluster(swNS), client); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if len(bodies) != 1 {
					t.Fatalf("expected exactly one Deployment patch, got %d: %v", len(bodies), bodies)
				}
				if got := strings.Contains(bodies[0], `"strategy"`); got != tc.want {
					t.Fatalf("patch %s: carries strategy = %v, want %v", bodies[0], got, tc.want)
				}
				if tc.want && !strings.Contains(bodies[0], `"rollingUpdate":null`) {
					t.Fatalf("patch %s must null out rollingUpdate", bodies[0])
				}
			})
		}
	}
}

// --- 2. the scale-up guard ---

func TestK8SStart_WaitsWhileARunningPodUsesThePVC(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, false))
	cluster := newTestCluster(swNS)

	released := make(chan time.Time, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		released <- time.Now()
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{})
	}()
	err := cluster.k8sStartDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll)
	done := time.Now()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if r := <-released; done.Before(r) {
		t.Fatalf("Start returned before the old Pod was released")
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1 once the Pod is gone", got)
	}
}

func TestK8SStart_WaitsWhileATerminatingPodUsesThePVC(t *testing.T) {
	// a Pod of an old ReplicaSet, Terminating, with a Running phase: it still runs its containers
	old := swPod("db1-oldrs-x", swClaim, true)
	old.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "db1-oldrs"}}
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), old)

	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "db1-oldrs-x") || !strings.Contains(err.Error(), "terminating") {
		t.Fatalf("expected an error naming the terminating Pod, got %v", err)
	}
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("must not scale up with a terminating consumer, replicas %d", got)
	}
}

func TestK8SStart_ReleasedOnceTheTerminatingPodIsGone(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-oldrs-x", swClaim, true))
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-oldrs-x", metav1.DeleteOptions{})
	}()
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

func TestK8SStart_IgnoresPodsOfOtherPVCsAndOtherNamespaces(t *testing.T) {
	other := swPod("other-ns", swClaim, false)
	other.Namespace = "elsewhere"
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()),
		swPod("db2-pod", "k8stest-db2-claim", false), // another database's PVC
		swPod("proxy-pod", "", false),                // no PVC at all
		other,                                        // same claim name, another namespace
	)
	start := time.Now()
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 5*time.Second, time.Second); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("unrelated Pods must not delay Start, it waited %s", time.Since(start))
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

// the claim is the one the Deployment really mounts, not a name rebuilt from a convention
func TestK8SStart_UsesTheDeploymentsActualClaim(t *testing.T) {
	dep := swDeployment(0, swRecreate())
	dep.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName = "custom-claim"
	client := fake.NewSimpleClientset(dep,
		swPod("by-convention", swClaim, false), // the conventional name is NOT this database's claim
		swPod("holder", "custom-claim", false),
	)
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "holder") || strings.Contains(err.Error(), "by-convention") {
		t.Fatalf("expected only the Pod holding custom-claim to block, got %v", err)
	}
}

func TestK8SStart_TimeoutDoesNotScaleUpNorDelete(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, false))

	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "not scaling up") || !strings.Contains(err.Error(), "db1-old") {
		t.Fatalf("expected an explicit timeout error naming the Pod, got %v", err)
	}
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("replicas %d after a timeout, want 0", got)
	}
	if n := swCountActions(client, "delete", "pods") + swCountActions(client, "delete", "deployments") + swCountActions(client, "patch", "deployments"); n != 0 {
		t.Fatalf("a timed-out Start must not delete or patch anything, %d such actions", n)
	}
	if _, getErr := client.CoreV1().Pods(swNS).Get(context.TODO(), "db1-old", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the old Pod must still exist: %v", getErr)
	}
}

func TestK8SStart_PodListErrorNeverScalesUp(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()))
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("api down")
	})
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "cannot confirm") || !strings.Contains(err.Error(), "api down") {
		t.Fatalf("expected an explicit API error, got %v", err)
	}
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("replicas %d after an API error, want 0", got)
	}
}

func TestK8SStart_ATransientListErrorIsRetriedWithinTheDeadline(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()))
	var mu sync.Mutex
	failures := 2
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		if failures > 0 {
			failures--
			return true, nil, apierrors.NewTimeoutError("hiccup", 1)
		}
		return false, nil, nil
	})
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("a transient list error must be retried, got %s", err)
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

func TestK8SStart_DeploymentReadErrorNeverScalesUp(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()))
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("api down")
	})
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll); err == nil {
		t.Fatal("expected an error when the Deployment cannot be read")
	}
	if n := swCountActions(client, "patch", "deployments"); n != 0 {
		t.Fatalf("nothing may be patched, %d patches", n)
	}
}

// a Deployment that already runs keeps the old idempotent Start: patched to 1, no Pod listing, no wait
func TestK8SStart_AlreadyRunningDoesNotWaitOrList(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()), swPod("db1-self", swClaim, false))
	start := time.Now()
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 5*time.Second, time.Second); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("an already running Deployment must not wait, took %s", time.Since(start))
	}
	if n := swCountActions(client, "list", "pods"); n != 0 {
		t.Fatalf("an already running Deployment must not list Pods, %d lists", n)
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

// --- ordering of the stop / update / start cycle of a rolling upgrade ---

func TestK8SUpgradeCycle_StartWaitsForTheOldPodAfterTheImageUpdate(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRollingUpdate()), swPod("db1-old", swClaim, true))
	cluster := newTestCluster(swNS)
	cluster.Conf.ProvDbImg = "mariadb:11.8.10"

	if err := cluster.k8sStopDatabaseServiceWithClient(client, "db1"); err != nil {
		t.Fatalf("stop: %s", err)
	}
	// the image is patched only once the Deployment is at 0 (existing guard), and migrates the strategy
	if err := cluster.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", true); err != nil {
		t.Fatalf("update config: %s", err)
	}
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{})
	}()
	if err := cluster.k8sStartDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("start: %s", err)
	}

	// order of the recorded actions: patch replicas 0, patch image, delete pod, patch replicas 1
	podDeleted, scaledUp := -1, -1
	for i, a := range client.Actions() {
		if a.Matches("delete", "pods") {
			podDeleted = i
		}
		if pa, ok := a.(k8stesting.PatchAction); ok && a.Matches("patch", "deployments") && strings.Contains(string(pa.GetPatch()), `"replicas":1`) {
			scaledUp = i
		}
	}
	if podDeleted < 0 || scaledUp < 0 || scaledUp < podDeleted {
		t.Fatalf("the scale-up (action %d) must follow the old Pod's removal (action %d)", scaledUp, podDeleted)
	}
	dep, _ := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{})
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || dep.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("the upgrade must have migrated the strategy, got %+v", dep.Spec.Strategy)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != "mariadb:11.8.10" {
		t.Fatalf("image %q, want the upgrade image", dep.Spec.Template.Spec.Containers[0].Image)
	}
	if *dep.Spec.Replicas != 1 {
		t.Fatalf("replicas %d, want 1", *dep.Spec.Replicas)
	}
}

// --- 3. unprovision waits for the Deployment to be gone; provisioning refuses a deleting one ---

// swTerminatingDeployment keeps a Deployment visible, with a deletion timestamp, for the first
// `stickyGets` reads after a delete, then reports NotFound: a foreground delete waiting for its Pods.
func swTerminatingDeployment(client *fake.Clientset, stickyGets int) (deleteOptions func() *metav1.DeleteOptions, gets func() int) {
	var mu sync.Mutex
	var deleted bool
	var opts *metav1.DeleteOptions
	n := 0
	client.PrependReactor("delete", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		deleted = true
		o := a.(k8stesting.DeleteActionImpl).DeleteOptions
		opts = &o
		return true, nil, nil // accepted, the object stays: its Pods are still stopping
	})
	client.PrependReactor("get", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		if !deleted {
			return false, nil, nil
		}
		n++
		if n > stickyGets {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "db1")
		}
		dep := swDeployment(0, swRecreate())
		now := metav1.Now()
		dep.DeletionTimestamp = &now
		return true, dep, nil
	})
	return func() *metav1.DeleteOptions { mu.Lock(); defer mu.Unlock(); return opts }, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

func TestK8SUnprovision_WaitsUntilTheDeploymentIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()))
	deleteOptions, gets := swTerminatingDeployment(client, 3)

	if err := newTestCluster(swNS).k8sUnprovisionDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gets() < 4 {
		t.Fatalf("returned after %d reads: it must keep reading until NotFound (3 terminating reads, then NotFound)", gets())
	}
	opts := deleteOptions()
	if opts == nil || opts.PropagationPolicy == nil || *opts.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("the Deployment must still be deleted with foreground propagation, got %+v", opts)
	}
	if opts.GracePeriodSeconds != nil {
		t.Fatalf("nothing may be force-deleted, grace period %v", *opts.GracePeriodSeconds)
	}
}

func TestK8SUnprovision_TimeoutIsAnActionableErrorAndForceDeletesNothing(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()), &apiv1.Service{ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: swNS}},
		&apiv1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: swClaim, Namespace: swNS}})
	swTerminatingDeployment(client, 1<<30) // never completes

	err := newTestCluster(swNS).k8sUnprovisionDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "still exists") || !strings.Contains(err.Error(), "nothing was force-deleted") {
		t.Fatalf("expected an actionable timeout error, got %v", err)
	}
	if n := swCountActions(client, "delete", "deployments"); n != 1 {
		t.Fatalf("the Deployment is deleted once and never again, %d delete actions", n)
	}
	if swCountActions(client, "delete", "pods") != 0 || swCountActions(client, "delete", "persistentvolumeclaims") != 0 {
		t.Fatal("no Pod and no PVC may be deleted")
	}
	// the Service delete is not held up by the wait, and the PVC is retained
	if _, getErr := client.CoreV1().Services(swNS).Get(context.TODO(), "db1", metav1.GetOptions{}); !apierrors.IsNotFound(getErr) {
		t.Fatalf("the Service is deleted as before, got %v", getErr)
	}
	if _, getErr := client.CoreV1().PersistentVolumeClaims(swNS).Get(context.TODO(), swClaim, metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the PVC must be retained: %v", getErr)
	}
}

func TestK8SUnprovision_MissingDeploymentIsStillIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	if err := newTestCluster(swNS).k8sUnprovisionDatabaseServiceWithWait(client, "db1", swTimeout, swPoll); err != nil {
		t.Fatalf("an absent Deployment and Service stay a success, got %v", err)
	}
}

func TestK8SUnprovision_DeleteFailureReturnsOneErrorWithoutWaiting(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()))
	client.PrependReactor("delete", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "db1", nil)
	})
	start := time.Now()
	err := newTestCluster(swNS).k8sUnprovisionDatabaseServiceWithWait(client, "db1", 5*time.Second, time.Second)
	if err == nil {
		t.Fatal("expected the delete error")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a failed delete must not be followed by a wait, took %s", time.Since(start))
	}
}

func TestK8SExistingDeploymentUsable(t *testing.T) {
	running := swDeployment(1, swRecreate())
	deleting := swDeployment(1, swRecreate())
	now := metav1.Now()
	deleting.DeletionTimestamp = &now

	if err := newTestCluster(swNS).k8sExistingDeploymentUsable(fake.NewSimpleClientset(running), "db1"); err != nil {
		t.Fatalf("an existing, non-deleting Deployment keeps the idempotent AlreadyExists: %s", err)
	}
	err := newTestCluster(swNS).k8sExistingDeploymentUsable(fake.NewSimpleClientset(deleting), "db1")
	if err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatalf("a Deployment being deleted must not count as provisioned, got %v", err)
	}
	if err := newTestCluster(swNS).k8sExistingDeploymentUsable(fake.NewSimpleClientset(), "db1"); err == nil {
		t.Fatal("an unreadable existing Deployment cannot be accepted as provisioned")
	}
}

// reprovision order: provisioning the same name after an unprovision that completed finds no Deployment,
// and after one that did not complete is refused (the foreground-delete race of the T13 run)
func TestK8SReprovisionOrdering(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()))
	swTerminatingDeployment(client, 2)
	cluster := newTestCluster(swNS)

	// the old, pre-fix sequence: Delete returned, provision straight away -> the object is still there
	if _, err := client.AppsV1().Deployments(swNS).Create(context.TODO(), swDeployment(1, swRecreate()), metav1.CreateOptions{}); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("setup: expected AlreadyExists, got %v", err)
	}
	_ = client.AppsV1().Deployments(swNS).Delete(context.TODO(), "db1", metav1.DeleteOptions{})
	if err := cluster.k8sExistingDeploymentUsable(client, "db1"); err == nil {
		t.Fatal("while the old Deployment is still being deleted, provisioning must be refused")
	}
	// the fixed sequence: unprovision waits for NotFound first
	if err := cluster.k8sWaitDeploymentGone(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if _, err := client.AppsV1().Deployments(swNS).Get(context.TODO(), "db1", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after the wait, got %v", err)
	}
}

// --- provisioning: a Deployment about to be created starts at one replica, so the guard applies ---

func TestK8SWaitBeforeCreatingDeployment_FirstProvisioningDoesNotWait(t *testing.T) {
	client := fake.NewSimpleClientset() // no Deployment, no Pod
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

	start := time.Now()
	if err := cluster.k8sWaitBeforeCreatingDeployment(client, dep, 5*time.Second, time.Second); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a first provisioning must not wait, took %s", time.Since(start))
	}
}

// the claim the guard looks at is the one the real builder mounts, and only PVC volumes count
func TestK8SDeploymentPVCClaims_RealBuilderMountsOneDataClaim(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

	claims := k8sDeploymentPVCClaims(dep, "fallback-not-used")
	if len(claims) != 1 || claims[0] != k8sDatabasePVCName(swNS, "db1") {
		t.Fatalf("expected exactly the data claim %q, got %v", k8sDatabasePVCName(swNS, "db1"), claims)
	}
	// a template without any PVC volume falls back to the conventional name
	empty := &appsv1.Deployment{}
	if got := k8sDeploymentPVCClaims(empty, "conventional"); len(got) != 1 || got[0] != "conventional" {
		t.Fatalf("expected the fallback claim, got %v", got)
	}
}

func TestK8SWaitBeforeCreatingDeployment_OrphanedPodHoldingTheRetainedPVCBlocks(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")
	// the Deployment is gone (NotFound) but a Pod of an earlier generation still mounts the retained claim
	client := fake.NewSimpleClientset(swPod("db1-orphan", k8sDatabasePVCName(swNS, "db1"), false))

	err := cluster.k8sWaitBeforeCreatingDeployment(client, dep, swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "db1-orphan") || !strings.Contains(err.Error(), "not scaling up") {
		t.Fatalf("expected the orphaned Pod to block the creation, got %v", err)
	}
	if n := swCountActions(client, "delete", "pods") + swCountActions(client, "create", "deployments"); n != 0 {
		t.Fatalf("nothing may be deleted or created, %d such actions", n)
	}
}

func TestK8SWaitBeforeCreatingDeployment_ReleasedWhenTheTerminatingPodIsGone(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")
	client := fake.NewSimpleClientset(swPod("db1-old", k8sDatabasePVCName(swNS, "db1"), true))
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{})
	}()
	if err := cluster.k8sWaitBeforeCreatingDeployment(client, dep, 2*time.Second, swPoll); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestK8SWaitBeforeCreatingDeployment_UnrelatedPodsDoNotBlock(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")
	client := fake.NewSimpleClientset(swPod("db2-pod", k8sDatabasePVCName(swNS, "db2"), false), swPod("proxy", "", false))
	if err := cluster.k8sWaitBeforeCreatingDeployment(client, dep, swTimeout, swPoll); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

// an existing Deployment keeps the idempotent provisioning: its own Pod holds the claim and must not block
func TestK8SWaitBeforeCreatingDeployment_ExistingDeploymentIsNotChecked(t *testing.T) {
	for name, mutate := range map[string]func(*appsv1.Deployment){
		"running":  func(*appsv1.Deployment) {},
		"stopped":  func(d *appsv1.Deployment) { z := int32(0); d.Spec.Replicas = &z },
		"deleting": func(d *appsv1.Deployment) { n := metav1.Now(); d.DeletionTimestamp = &n },
	} {
		t.Run(name, func(t *testing.T) {
			existing := swDeployment(1, swRecreate())
			mutate(existing)
			client := fake.NewSimpleClientset(existing, swPod("db1-self", swClaim, false))
			cluster := newTestCluster(swNS)
			dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

			if err := cluster.k8sWaitBeforeCreatingDeployment(client, dep, 5*time.Second, time.Second); err != nil {
				t.Fatalf("an existing Deployment must not be waited on: %s", err)
			}
			if n := swCountActions(client, "list", "pods"); n != 0 {
				t.Fatalf("an existing Deployment must not list Pods, %d lists", n)
			}
		})
	}
}

func TestK8SWaitBeforeCreatingDeployment_ReadErrorsAreNotAGoAhead(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

	getFails := fake.NewSimpleClientset()
	getFails.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("api down")
	})
	if err := cluster.k8sWaitBeforeCreatingDeployment(getFails, dep, swTimeout, swPoll); err == nil || !strings.Contains(err.Error(), "cannot read Deployment") {
		t.Fatalf("an unreadable Deployment must be an error, got %v", err)
	}

	listFails := fake.NewSimpleClientset()
	listFails.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
	})
	if err := cluster.k8sWaitBeforeCreatingDeployment(listFails, dep, swTimeout, swPoll); err == nil || !strings.Contains(err.Error(), "`list` on `pods`") {
		t.Fatalf("a Pod list that is refused (missing RBAC) must fail closed, got %v", err)
	}
}

// --- the strategy migration patch, structurally ---

// the real patch bytes of the three operations are exactly the operation's own patch plus the strategy
func TestK8SLifecyclePatches_StrategyPatchStructure(t *testing.T) {
	for _, op := range swMigrationOps {
		t.Run(op.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(swDeployment(op.replic, swRollingUpdate()))
			var body []byte
			client.PrependReactor("patch", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
				body = a.(k8stesting.PatchAction).GetPatch()
				return false, nil, nil
			})
			if err := op.run(newTestCluster(swNS), client); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			var patch map[string]map[string]interface{}
			if err := json.Unmarshal(body, &patch); err != nil {
				t.Fatalf("patch %s is not the expected JSON object: %s", body, err)
			}
			if len(patch) != 1 || patch["spec"] == nil {
				t.Fatalf("the patch must only touch spec, got %s", body)
			}
			for k := range patch["spec"] {
				if k != "template" && k != "strategy" {
					t.Fatalf("unexpected spec key %q in %s", k, body)
				}
			}
			want := map[string]interface{}{"type": "Recreate", "rollingUpdate": nil}
			if got, ok := patch["spec"]["strategy"].(map[string]interface{}); !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("strategy = %v, want %v (type Recreate, rollingUpdate explicitly null)", patch["spec"]["strategy"], want)
			}
		})
	}
}

// applied with the strategic-merge library the API server uses (a fake client does not prove admission):
// the strategy becomes Recreate with no rollingUpdate, and the rest of the object is the original
func TestK8SLifecyclePatches_StrategicMergeResult(t *testing.T) {
	legacy := swDeployment(1, swRollingUpdate())
	origJSON, _ := json.Marshal(legacy)
	patch, _ := json.Marshal(map[string]interface{}{"spec": map[string]interface{}{"strategy": k8sRecreateStrategyPatch(legacy)}})

	patchedJSON, err := strategicpatch.StrategicMergePatch(origJSON, patch, appsv1.Deployment{})
	if err != nil {
		t.Fatalf("strategic merge: %s", err)
	}
	var patched appsv1.Deployment
	if err := json.Unmarshal(patchedJSON, &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || patched.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("strategy after merge: %+v", patched.Spec.Strategy)
	}
	// nothing but the strategy changed, the Pod template (hence its hash, hence the ReplicaSet) included:
	// a migration alone starts no rollout
	if !reflect.DeepEqual(patched.Spec.Template, legacy.Spec.Template) {
		t.Fatalf("a strategy-only patch changed the Pod template")
	}
	if *patched.Spec.Replicas != *legacy.Spec.Replicas || !reflect.DeepEqual(patched.Spec.Selector, legacy.Spec.Selector) || !reflect.DeepEqual(patched.ObjectMeta, legacy.ObjectMeta) {
		t.Fatalf("a strategy-only patch changed more than the strategy")
	}
}

// --- permission denied (missing RBAC): fail closed, fast, and nothing is scaled, created or deleted ---

func swForbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", nil)
}

func swNoMutation(t *testing.T, client *fake.Clientset) {
	t.Helper()
	for _, verb := range []string{"delete", "create", "update"} {
		for _, res := range []string{"pods", "deployments", "persistentvolumeclaims"} {
			if n := swCountActions(client, verb, res); n != 0 {
				t.Fatalf("%d unexpected %s %s action(s): a refused read must not change anything", n, verb, res)
			}
		}
	}
	if n := swCountActions(client, "patch", "deployments"); n != 0 {
		t.Fatalf("%d unexpected Deployment patch(es): nothing may be scaled", n)
	}
}

func TestK8SStart_PodListForbiddenFailsAtOnceWithAnActionableError(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, false))
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, swForbidden("pods")
	})
	start := time.Now()
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 30*time.Second, 5*time.Second)
	if time.Since(start) > time.Second {
		t.Fatalf("a permanent permission error must not wait for the deadline, took %s", time.Since(start))
	}
	if err == nil || !strings.Contains(err.Error(), "`list` on `pods`") || !apierrors.IsForbidden(errors.Unwrap(err)) {
		t.Fatalf("expected an actionable RBAC error wrapping the API error, got %v", err)
	}
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("replicas %d, want 0", got)
	}
	swNoMutation(t, client)
}

func TestK8SStart_DeploymentGetForbiddenFailsClosed(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()))
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, swForbidden("deployments")
	})
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll); err == nil || !apierrors.IsForbidden(err) {
		t.Fatalf("expected the Forbidden error, got %v", err)
	}
	swNoMutation(t, client)
}

func TestK8SWaitBeforeCreatingDeployment_PermissionDeniedFailsClosed(t *testing.T) {
	cluster := newTestCluster(swNS)
	dep := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1"}, 3306, "node-a")

	podsForbidden := fake.NewSimpleClientset()
	podsForbidden.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, swForbidden("pods") })
	start := time.Now()
	if err := cluster.k8sWaitBeforeCreatingDeployment(podsForbidden, dep, 30*time.Second, 5*time.Second); err == nil || !strings.Contains(err.Error(), "`list` on `pods`") {
		t.Fatalf("expected an actionable RBAC error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("must fail at once, took %s", time.Since(start))
	}
	swNoMutation(t, podsForbidden)

	deploymentsForbidden := fake.NewSimpleClientset()
	deploymentsForbidden.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, swForbidden("deployments") })
	if err := cluster.k8sWaitBeforeCreatingDeployment(deploymentsForbidden, dep, swTimeout, swPoll); err == nil || !strings.Contains(err.Error(), "cannot read Deployment") {
		t.Fatalf("an unreadable Deployment must be an error, got %v", err)
	}
	swNoMutation(t, deploymentsForbidden)
}

func TestK8SUnprovision_DeploymentGetForbiddenIsAnErrorWithoutForceDelete(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(1, swRecreate()))
	// the delete is accepted, the follow-up read is refused: gone cannot be confirmed
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, swForbidden("deployments") })
	start := time.Now()
	err := newTestCluster(swNS).k8sUnprovisionDatabaseServiceWithWait(client, "db1", 30*time.Second, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "`get` on `deployments`") {
		t.Fatalf("expected an actionable RBAC error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("must fail at once, took %s", time.Since(start))
	}
	if swCountActions(client, "delete", "pods") != 0 || swCountActions(client, "delete", "deployments") != 1 {
		t.Fatal("exactly one Deployment delete (the requested one), no Pod delete, nothing forced")
	}
}
