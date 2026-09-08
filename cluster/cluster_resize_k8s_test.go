package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// k8sResizeTestServer returns a ServerMonitor wired with a sqlmock-backed
// connection (no expectations set -- ExecScriptSQL logs and continues past an
// unmatched/erroring call, it never panics on one) so resizeMemorySQL's SET
// GLOBAL statements can actually run through ExecScriptSQL without a nil *sqlx.DB
// panic.
func k8sResizeTestServer(t *testing.T, clusterName, serverName string) (*Cluster, *ServerMonitor, func()) {
	t.Helper()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	cluster := newTestCluster(clusterName)
	cluster.Conf.ProvOrchestrator = "kube"
	// resizeMemorySQL (via applyConfirmedMemoryGrow) reaches into the
	// Configurator's %-memory model and DBVersion-gated branches -- both need
	// real values here since this bare cluster skips the AddFlags() defaults
	// and the Configurator.Init() wiring a production Cluster always has
	// (Configurator.ClusterConfig is its OWN config.Config copy, not
	// cluster.Conf, so it must be seeded separately).
	cluster.Conf.ProvMemThreadedPct = "tmp:70,join:20,sort:10"
	cluster.Configurator.ClusterConfig = *cluster.Conf
	s := &ServerMonitor{
		Name:         serverName,
		URL:          serverName + ":3306",
		ClusterGroup: cluster,
		Conn:         sqlx.NewDb(db, "sqlmock"),
		DBVersion:    &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11},
		// SetRestartCookie/HasRestartCookie are real file-backed cookies
		// (server.Datadir + "/@cookie_restart") -- a real temp dir so the
		// fallback path in cluster_resize_k8s.go is actually observable.
		Datadir: t.TempDir(),
	}
	cluster.Servers = []*ServerMonitor{s}
	return cluster, s, func() { db.Close() }
}

// k8sTestController builds a Deployment + its CURRENT ReplicaSet (matching
// revision annotation, matching selector labels, and a real owner reference)
// -- the fixture shape k8sFindDatabasePod (controller-aware Pod lookup,
// prov_k8s_db.go) requires since it no longer trusts label+phase alone. Every
// Pod fixture below must set OwnerReferences to rs.UID to be found.
func k8sTestController(namespace, name string) (*appsv1.Deployment, *appsv1.ReplicaSet) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: ktypes.UID(name + "-dep-uid")},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "repication-manager", "tag": name}},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name + "-rs-current",
			Namespace:       namespace,
			UID:             ktypes.UID(name + "-rs-current-uid"),
			Labels:          map[string]string{"app": "repication-manager", "tag": name},
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{{UID: dep.UID}},
		},
	}
	return dep, rs
}

func k8sResizeTestPod(namespace, podName, containerName string, memMi string, resizeStatus apiv1.PodResizeStatus, rsUID ktypes.UID) *apiv1.Pod {
	q := resource.MustParse(memMi)
	return &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            podName,
			Namespace:       namespace,
			Labels:          map[string]string{"app": "repication-manager", "tag": containerName},
			OwnerReferences: []metav1.OwnerReference{{UID: rsUID}},
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				{
					Name: containerName,
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{apiv1.ResourceMemory: q},
						Limits:   apiv1.ResourceList{apiv1.ResourceMemory: q},
					},
				},
			},
		},
		Status: apiv1.PodStatus{
			Phase:             apiv1.PodRunning,
			Resize:            resizeStatus,
			ContainerStatuses: []apiv1.ContainerStatus{{Name: containerName, Ready: true}},
		},
	}
}

// --- k8sFindDatabasePod: deterministic, controller-aware active-Pod selection ---

func TestK8sFindDatabasePod_SinglePodReturned(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got == nil || got.Name != "db1-abc" {
		t.Fatalf("expected to find db1-abc, got %#v", got)
	}
}

func TestK8sFindDatabasePod_AmbiguousMultipleRunningPodsCannotConfirm(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rs := k8sTestController("k8stest", "db1")
	old := k8sResizeTestPod("k8stest", "db1-old", "db1", "128Mi", "", rs.UID)
	fresh := k8sResizeTestPod("k8stest", "db1-new", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, old, fresh)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil (cannot confirm a single target) when two Pods match, got %#v", got)
	}
}

func TestK8sFindDatabasePod_TerminatingPodIgnored(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rs := k8sTestController("k8stest", "db1")
	now := metav1.Now()
	terminating := k8sResizeTestPod("k8stest", "db1-old", "db1", "128Mi", "", rs.UID)
	terminating.DeletionTimestamp = &now
	fresh := k8sResizeTestPod("k8stest", "db1-new", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, terminating, fresh)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got == nil || got.Name != "db1-new" {
		t.Fatalf("expected the terminating Pod to be excluded, leaving db1-new as the single candidate, got %#v", got)
	}
}

func TestK8sFindDatabasePod_NonRunningPhaseIgnored(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rs := k8sTestController("k8stest", "db1")
	pending := &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db1-pending", Namespace: "k8stest", Labels: map[string]string{"app": "repication-manager", "tag": "db1"}, OwnerReferences: []metav1.OwnerReference{{UID: rs.UID}}},
		Status:     apiv1.PodStatus{Phase: apiv1.PodPending},
	}
	client := fake.NewSimpleClientset(dep, rs, pending)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil while the only Pod is not yet Running, got %#v", got)
	}
}

func TestK8sFindDatabasePod_NoDeploymentYetCannotConfirm(t *testing.T) {
	cluster := newTestCluster("k8stest")
	client := fake.NewSimpleClientset()

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil when the Deployment does not exist yet, got %#v", got)
	}
}

func TestK8sFindDatabasePod_NotReadyContainerIgnored(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	pod.Status.ContainerStatuses[0].Ready = false
	client := fake.NewSimpleClientset(dep, rs, pod)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil while the DB container is not Ready, got %#v", got)
	}
}

// TestK8sFindDatabasePod_OldReplicaSetPodIgnoredEvenWhileRunning is the exact
// scenario a label+phase-only lookup gets wrong (finding: an old-generation
// Pod still Running while its replacement is still Pending -- e.g. an RWO PVC
// blocking the new Pod from scheduling until the old one releases it). The
// old ReplicaSet's Pod must never be selected just because it is the only
// Running one: it belongs to a superseded generation.
func TestK8sFindDatabasePod_OldReplicaSetPodIgnoredEvenWhileRunning(t *testing.T) {
	cluster := newTestCluster("k8stest")
	dep, rsCurrent := k8sTestController("k8stest", "db1")
	rsOld := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db1-rs-old", Namespace: "k8stest", UID: ktypes.UID("db1-rs-old-uid"),
			Labels:          map[string]string{"app": "repication-manager", "tag": "db1"},
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "0"},
			OwnerReferences: []metav1.OwnerReference{{UID: dep.UID}},
		},
	}
	oldPod := k8sResizeTestPod("k8stest", "db1-old", "db1", "128Mi", "", rsOld.UID)
	newPod := k8sResizeTestPod("k8stest", "db1-new", "db1", "128Mi", "", rsCurrent.UID)
	newPod.Status.Phase = apiv1.PodPending
	newPod.Status.ContainerStatuses = nil
	client := fake.NewSimpleClientset(dep, rsCurrent, rsOld, oldPod, newPod)

	got, err := cluster.k8sFindDatabasePod(context.Background(), client, &ServerMonitor{Name: "db1"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil: the only Running Pod belongs to a superseded ReplicaSet, got %#v", got)
	}
}

// --- k8sMemoryResizeConfirmed: spec match alone is not confirmation ---

func TestK8sMemoryResizeConfirmed_SpecMatchWithPendingStatusIsNotConfirmed(t *testing.T) {
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", apiv1.PodResizeStatusInProgress, "")
	if k8sMemoryResizeConfirmed(pod, "db1", resource.MustParse("256Mi")) {
		t.Fatal("expected a non-empty Status.Resize to prevent confirmation even when the spec already matches")
	}
}

func TestK8sMemoryResizeConfirmed_SpecMatchWithClearStatusIsConfirmed(t *testing.T) {
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", "", "")
	if !k8sMemoryResizeConfirmed(pod, "db1", resource.MustParse("256Mi")) {
		t.Fatal("expected a clear Status.Resize with a matching spec to be confirmed")
	}
}

// --- completePendingK8sMemoryResize (via the *WithClient testable half) ---

func TestCompletePendingK8sMemoryResize_NoopWhenNothingPending(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()

	cluster.completePendingK8sMemoryResize(s) // must not panic with no client wired at all
	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to remain nil")
	}
}

func TestCompletePendingK8sMemoryResize_ConfirmedShrinkClearsPending(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	// Observed:true simulates an earlier tick having already seen this
	// dispatch's resize genuinely in flight (Status.Resize non-empty) -- a
	// clear status is only trusted as completion once that has happened, see
	// TestCompletePendingK8sMemoryResize_ClearStatusButNeverObservedIsNotConfirmedYet.
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 128, Grow: false, StartedAt: time.Now(), Observed: true})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared once confirmed")
	}
	if s.HasRestartCookie() {
		t.Fatal("a confirmed resize must not fall back to a restart")
	}
}

func TestCompletePendingK8sMemoryResize_ConfirmedGrowClearsPendingAndRaisesDBMemory(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now(), Observed: true})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "") // exercises applyConfirmedMemoryGrow -> ExecScriptSQL

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared once confirmed")
	}
	if s.HasRestartCookie() {
		t.Fatal("a confirmed resize must not fall back to a restart")
	}
}

func TestCompletePendingK8sMemoryResize_InProgressStatusIsNotConfirmed(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	// Spec already shows the target (the earlier Resize() patch), but kubelet
	// hasn't finished applying it -- must NOT be treated as confirmed (finding:
	// duplicate-invocation-can-report-applied-before-confirmation).
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", apiv1.PodResizeStatusInProgress, rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if s.GetPendingK8sMemoryResize() == nil {
		t.Fatal("expected the pending state to survive while Status.Resize is still InProgress")
	}
	if s.HasRestartCookie() {
		t.Fatal("must not fall back to restart while still within the confirmation timeout")
	}
}

// TestCompletePendingK8sMemoryResize_ClearStatusButNeverObservedIsNotConfirmedYet
// is the exact race Finding 1 flagged: a tick's Pod fetch lands with the spec
// already at target (a resize-subresource Patch mutates spec immediately on
// acceptance) but Status.Resize is STILL clear because kubelet has not yet
// reacted to the patch at all -- not because the resize is genuinely done.
// Without ever having observed a non-empty Status.Resize for this exact
// dispatch, that must NOT be trusted as completion.
func TestCompletePendingK8sMemoryResize_ClearStatusButNeverObservedIsNotConfirmedYet(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	pending := s.GetPendingK8sMemoryResize()
	if pending == nil {
		t.Fatal("expected pending state to survive: never observed in flight, so a clear status is not trusted as completion")
	}
	if pending.Observed {
		t.Fatal("Observed must only be set from a non-empty Status.Resize, never from a clear one")
	}
	if s.HasRestartCookie() {
		t.Fatal("must not fall back to restart while still within the confirmation timeout")
	}
}

// TestCompletePendingK8sMemoryResize_ObservedInProgressThenClearedConfirms
// exercises the full two-tick sequence the Observed gate protects: tick 1
// sees the resize genuinely in flight (Status.Resize InProgress) and records
// it; tick 2, once kubelet has actually finished (Status.Resize clear, spec
// matches), is then correctly trusted as completion.
func TestCompletePendingK8sMemoryResize_ObservedInProgressThenClearedConfirms(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})

	inProgress := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", apiv1.PodResizeStatusInProgress, rs.UID)
	client1 := fake.NewSimpleClientset(dep, rs, inProgress)
	cluster.k8sCompletePendingMemoryResizeWithClient(client1, s, "")

	pending := s.GetPendingK8sMemoryResize()
	if pending == nil || !pending.Observed {
		t.Fatalf("expected the first tick to mark Observed once it saw Status.Resize InProgress, got %#v", pending)
	}

	cleared := k8sResizeTestPod("k8stest", "db1-abc", "db1", "256Mi", "", rs.UID)
	client2 := fake.NewSimpleClientset(dep, rs, cleared)
	cluster.k8sCompletePendingMemoryResizeWithClient(client2, s, "")

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared once genuinely confirmed after having been observed in flight")
	}
	if s.HasRestartCookie() {
		t.Fatal("a confirmed resize must not fall back to a restart")
	}
}

func TestCompletePendingK8sMemoryResize_DeferredFallsBackToRestart(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", apiv1.PodResizeStatusDeferred, rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared on a Deferred verdict")
	}
	if !s.HasRestartCookie() {
		t.Fatal("expected a Deferred resize to fall back to the restart-cookie path")
	}
}

func TestCompletePendingK8sMemoryResize_InfeasibleFallsBackToRestart(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", apiv1.PodResizeStatusInfeasible, rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if !s.HasRestartCookie() {
		t.Fatal("expected an Infeasible resize to fall back to the restart-cookie path")
	}
}

func TestCompletePendingK8sMemoryResize_StillInProgressKeepsWaiting(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	// Still at the OLD value, no Resize status yet: kubelet hasn't applied it.
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if s.GetPendingK8sMemoryResize() == nil {
		t.Fatal("expected the pending state to survive an in-progress tick")
	}
	if s.HasRestartCookie() {
		t.Fatal("must not fall back to restart while still within the confirmation timeout")
	}
}

func TestCompletePendingK8sMemoryResize_TimeoutFallsBackToRestart(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now().Add(-1 * (k8sResizeConfirmTimeout + time.Second))})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared once the confirmation timeout elapses")
	}
	if !s.HasRestartCookie() {
		t.Fatal("expected a timed-out resize to fall back to the restart-cookie path")
	}
}

func TestCompletePendingK8sMemoryResize_MissingPodTimesOutEventually(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	client := fake.NewSimpleClientset()

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now().Add(-1 * (k8sResizeConfirmTimeout + time.Second))})
	cluster.k8sCompletePendingMemoryResizeWithClient(client, s, "")

	if !s.HasRestartCookie() {
		t.Fatal("expected a persistently missing Pod to eventually fall back to restart")
	}
}

func TestCompletePendingK8sMemoryResize_ForcedFailReasonSkipsClient(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})
	cluster.k8sCompletePendingMemoryResizeWithClient(nil, s, "Kubernetes API unreachable")

	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected pending state to be cleared")
	}
	if !s.HasRestartCookie() {
		t.Fatal("expected a forced failure to fall back to the restart-cookie path")
	}
}

// --- Cluster.k8sPendingMemoryResizes: shadow map for reload-survival ---

func TestSetPendingK8sMemoryResize_MirrorsIntoClusterShadowMap(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	s.Id = "db-shadow-test"

	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})

	v, ok := cluster.k8sPendingMemoryResizes.Load(s.Id)
	if !ok {
		t.Fatal("expected SetPendingK8sMemoryResize to mirror into Cluster.k8sPendingMemoryResizes")
	}
	got, isState := v.(*K8sMemoryResizeState)
	if !isState || got.TargetMB != 256 {
		t.Fatalf("expected the shadowed state to match, got %#v", v)
	}
}

func TestSetPendingK8sMemoryResize_NilRemovesFromClusterShadowMap(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	s.Id = "db-shadow-test"
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()})

	s.SetPendingK8sMemoryResize(nil)

	if _, ok := cluster.k8sPendingMemoryResizes.Load(s.Id); ok {
		t.Fatal("expected clearing the pending state to also remove it from the Cluster shadow map")
	}
}

func TestClearPendingK8sMemoryResizeIfSame_RemovesFromClusterShadowMap(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	s.Id = "db-shadow-test"
	pending := &K8sMemoryResizeState{TargetMB: 256, Grow: true, StartedAt: time.Now()}
	s.SetPendingK8sMemoryResize(pending)

	s.clearPendingK8sMemoryResizeIfSame(pending)

	if _, ok := cluster.k8sPendingMemoryResizes.Load(s.Id); ok {
		t.Fatal("expected the CAS clear to also remove the shadowed state from the Cluster map")
	}
}

// --- restorePendingK8sMemoryResize: survives ServerMonitor recreation ---

func TestRestorePendingK8sMemoryResize_RestoresWithObservedReset(t *testing.T) {
	cluster := newTestCluster("k8stest")
	pending := &K8sMemoryResizeState{TargetMB: 512, Grow: true, StartedAt: time.Now().Add(-30 * time.Second), Observed: true}
	cluster.k8sPendingMemoryResizes.Store("db-recreated", pending)

	fresh := &ServerMonitor{Name: "db1", Id: "db-recreated", ClusterGroup: cluster}
	cluster.restorePendingK8sMemoryResize(fresh)

	got := fresh.GetPendingK8sMemoryResize()
	if got == nil {
		t.Fatal("expected the pending resize to be restored onto the recreated ServerMonitor")
	}
	if got.TargetMB != 512 || !got.Grow {
		t.Fatalf("expected target/direction to carry over unchanged, got %#v", got)
	}
	if !got.StartedAt.Equal(pending.StartedAt) {
		t.Fatal("expected StartedAt to carry over unchanged so the original timeout budget is not silently extended by a reload")
	}
	if got.Observed {
		t.Fatal("expected Observed to be reset to false: this process instance never itself witnessed the resize in flight")
	}
}

func TestRestorePendingK8sMemoryResize_NoopWhenNothingTracked(t *testing.T) {
	cluster := newTestCluster("k8stest")
	fresh := &ServerMonitor{Name: "db1", Id: "db-no-history", ClusterGroup: cluster}

	cluster.restorePendingK8sMemoryResize(fresh)

	if fresh.GetPendingK8sMemoryResize() != nil {
		t.Fatal("expected no pending state when nothing was tracked for this server identity")
	}
}

// TestRestorePendingK8sMemoryResize_SurvivesFullServerMonitorReplacementEndToEnd
// simulates exactly what newServerList() (cluster_topo.go) does on a
// reload/config-set: discard the old *ServerMonitor and build a brand new one
// for the same server identity. The pending resize must survive that.
func TestRestorePendingK8sMemoryResize_SurvivesFullServerMonitorReplacementEndToEnd(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	s.Id = "db-e2e"
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 384, Grow: false, StartedAt: time.Now()})

	replacement := &ServerMonitor{Name: "db1", Id: "db-e2e", ClusterGroup: cluster}
	cluster.restorePendingK8sMemoryResize(replacement)

	got := replacement.GetPendingK8sMemoryResize()
	if got == nil || got.TargetMB != 384 || got.Grow {
		t.Fatalf("expected the replacement ServerMonitor to inherit the exact pending resize, got %#v", got)
	}
}

// --- k8sDatabaseMemoryTargets: single authority shared by builder + resizer ---

// TestK8sDatabaseMemoryTargets_AggregateAlwaysEqualsProvMem is the exact
// OpenSVC-parity contract: OpenSVC's own effective service cap (pg_mem_limit)
// is raw prov-db-memory (T) -- so the Kubernetes DB+dbjobs aggregate Pod cap
// must equal that SAME T, split between the two containers, never T plus an
// additional amount on top.
func TestK8sDatabaseMemoryTargets_AggregateAlwaysEqualsProvMem(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvMem = "512M"

	dbMB, jobsMB := cluster.k8sDatabaseMemoryTargets()
	wantTotal, err := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	if err != nil {
		t.Fatalf("unexpected error parsing prov-db-memory: %s", err)
	}
	if dbMB+jobsMB != wantTotal {
		t.Fatalf("dbMB(%d) + jobsMB(%d) = %d, want exactly prov-db-memory = %d", dbMB, jobsMB, dbMB+jobsMB, wantTotal)
	}
	if jobsMB != k8sDBJobsMemoryCapMB {
		t.Fatalf("jobsMB = %d, want the technical minimum %d carved out of T", jobsMB, k8sDBJobsMemoryCapMB)
	}
}

// TestK8sDatabaseMemoryTargets_TinyProvMemShrinksJobsNotBelowFloor proves the
// degrade-safely edge case: when T itself is smaller than dbjobs' own
// technical minimum, dbjobs is the one that shrinks (never the DB container,
// the actually load-bearing process) -- and the aggregate invariant still
// holds exactly.
func TestK8sDatabaseMemoryTargets_TinyProvMemShrinksJobsNotBelowFloor(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvMem = "64M" // smaller than k8sDBJobsMemoryCapMB (128)

	dbMB, jobsMB := cluster.k8sDatabaseMemoryTargets()
	if dbMB+jobsMB != 64 {
		t.Fatalf("dbMB(%d) + jobsMB(%d) = %d, want exactly 64", dbMB, jobsMB, dbMB+jobsMB)
	}
	if jobsMB >= k8sDBJobsMemoryCapMB {
		t.Fatalf("expected jobsMB to shrink below its normal technical minimum when T is too small, got %d", jobsMB)
	}
	if dbMB <= 0 {
		t.Fatalf("expected the DB container to keep the vast majority of a too-small T, got dbMB=%d", dbMB)
	}
}

func TestK8sDatabaseMemoryTargets_InvalidProvMemFallsBackSafelyNeverZero(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvMem = "not-a-valid-value"

	dbMB, jobsMB := cluster.k8sDatabaseMemoryTargets()
	if dbMB <= 0 || jobsMB <= 0 {
		t.Fatalf("expected a safe non-zero fallback for unparseable prov-db-memory, got dbMB=%d jobsMB=%d", dbMB, jobsMB)
	}
}

// --- resourceResizer selection ---

// TestResourceResizer_KubernetesNeverSelectsK8sResizerYet locks in the
// deliberate gate in resourceResizer (cluster_resize_dynamic.go): k8sResizer
// computes GetDBContainerMemoryCapMB()+dbjobs, not the exact OpenSVC
// DEFAULT.pg_mem_limit service-cap target this feature is meant to match, so
// it must not be reachable from production code -- even with
// prov-db-docker-run-args-limit on -- until that contract is proven and this
// gate is deliberately reopened.
func TestResourceResizer_KubernetesNeverSelectsK8sResizerYet(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvOrchestrator = "kube"
	cluster.Conf.ProvDBDockerRunArgsLimit = true

	if _, ok := cluster.resourceResizer().(scriptResizer); !ok {
		t.Fatalf("expected scriptResizer (native k8sResizer must stay gated off until OpenSVC-cap parity is proven), got %T", cluster.resourceResizer())
	}
}

func TestResourceResizer_KubernetesWithoutRunArgsLimitFallsBackToScript(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.ProvOrchestrator = "kube"
	cluster.Conf.ProvDBDockerRunArgsLimit = false

	if _, ok := cluster.resourceResizer().(scriptResizer); !ok {
		t.Fatalf("expected scriptResizer, got %T", cluster.resourceResizer())
	}
}

// --- k8sResizer.ConfigResize (via the *WithClient testable half) ---

func TestK8sResize_NoLivePodSchedulesRestart(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	client := fake.NewSimpleClientset()

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if applied {
		t.Fatal("expected applied=false when no live Pod exists")
	}
	if !s.HasRestartCookie() {
		t.Fatal("expected a missing Pod to fall back to the restart-cookie path")
	}
}

func TestK8sResize_AmbiguousPodSchedulesRestartNotResize(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	dep, rs := k8sTestController("k8stest", "db1")
	old := k8sResizeTestPod("k8stest", "db1-old", "db1", "128Mi", "", rs.UID)
	fresh := k8sResizeTestPod("k8stest", "db1-new", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, old, fresh)

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if applied {
		t.Fatal("expected applied=false when the active Pod is ambiguous")
	}
	if !s.HasRestartCookie() {
		t.Fatal("expected an ambiguous Pod set to fall back to the restart-cookie path")
	}
	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("must not track a pending resize against an ambiguous target")
	}
}

func TestK8sResize_PatchesResizeSubresourceAndTracksPending(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	cluster.Conf.ProvMem = "512M" // dbMB = 512 - k8sDBJobsMemoryCapMB(128) = 384
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "128Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if applied {
		t.Fatal("expected applied=false: a native resize is never synchronously confirmed")
	}
	if s.HasRestartCookie() {
		t.Fatal("a genuinely pending resize must not fall back to restart immediately")
	}
	pending := s.GetPendingK8sMemoryResize()
	if pending == nil || pending.TargetMB != 384 || !pending.Grow {
		t.Fatalf("expected pending state to track the target and direction, got %#v", pending)
	}

	updated, err := client.CoreV1().Pods("k8stest").Get(context.TODO(), pod.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	got := updated.Spec.Containers[0].Resources.Limits[apiv1.ResourceMemory]
	if want := resource.MustParse("384Mi"); got.Cmp(want) != 0 {
		t.Fatalf("expected the resize subresource patch to request %s, got %s", want.String(), got.String())
	}
}

func TestK8sResize_AlreadyConfirmedAtTargetReturnsAppliedTrue(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	cluster.Conf.ProvMem = "512M" // dbMB = 512 - 128 = 384
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "384Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !applied {
		t.Fatal("expected applied=true when the live Pod is already confirmed at the target")
	}
}

func TestK8sResize_SpecMatchButStillInProgressDoesNotReportApplied(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	cluster.Conf.ProvMem = "512M" // dbMB = 512 - 128 = 384
	// The spec already shows the target (an earlier patch), but kubelet is
	// still applying it -- a duplicate Resize() call must NOT short-circuit to
	// applied=true from the spec value alone.
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "384Mi", apiv1.PodResizeStatusInProgress, rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if applied {
		t.Fatal("expected applied=false while Status.Resize is still InProgress, even though the spec already matches")
	}
}

// TestK8sResize_TrackedButNeverObservedDoesNotFalseConfirm is Finding 1's race
// reached through the dispatch entry point instead of the tick: a resize of
// OURS is tracked for this exact target but was never observed genuinely in
// flight (e.g. a duplicate Resize() call landing in the brief window right
// after the first Patch call returned, before kubelet reacted to it) -- the
// live Pod already showing a matching spec + clear status must NOT be trusted
// as confirmation, and the call must fall through to a safe re-patch instead.
func TestK8sResize_TrackedButNeverObservedDoesNotFalseConfirm(t *testing.T) {
	cluster, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	defer cleanup()
	cluster.Conf.ProvMem = "512M" // dbMB = 512 - 128 = 384
	dep, rs := k8sTestController("k8stest", "db1")
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", "384Mi", "", rs.UID)
	client := fake.NewSimpleClientset(dep, rs, pod)
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 384, Grow: true, StartedAt: time.Now()})

	applied, err := cluster.k8sResizeWithClient(client, s, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if applied {
		t.Fatal("expected applied=false: a tracked-but-unobserved resize must not be trusted from spec+clear-status alone")
	}
	if s.HasRestartCookie() {
		t.Fatal("a re-patch attempt must not fall back to restart")
	}
}
