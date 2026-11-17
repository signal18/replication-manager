package cluster

// Overlapping Start operations on a Deployment at replicas=0 (issue #2389 review).
//
// Two clientsets share one object tracker, so each Start has its own reactors and the two calls can
// be ordered exactly (the fake clientset holds its lock while a reactor runs, so one client cannot
// serve both a blocked call and the operation that must run meanwhile). The tests prove repman's
// logic on the fake API, not the ordering of a real Deployment controller.

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// swTwoClients returns two clientsets over the same tracker, seeded with objs.
func swTwoClients(objs ...runtime.Object) (a, b *fake.Clientset) {
	a = fake.NewSimpleClientset(objs...)
	b = &fake.Clientset{}
	b.AddReactor("*", "*", k8stesting.ObjectReaction(a.Tracker()))
	return a, b
}

func swSetReplicas(t *testing.T, client *fake.Clientset, n int) {
	t.Helper()
	patch := []byte(`{"spec":{"replicas":` + string(rune('0'+n)) + `}}`)
	if _, err := client.AppsV1().Deployments(swNS).Patch(context.TODO(), "db1", types.StrategicMergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
}

// swHoldPodList blocks the n-th Pod list of client until release is closed; held is closed when it blocks.
func swHoldPodList(client *fake.Clientset, n int) (held, release chan struct{}) {
	held, release = make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	calls := 0
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		calls++
		hit := calls == n
		mu.Unlock()
		if hit {
			close(held)
			<-release
		}
		return false, nil, nil
	})
	return held, release
}

func swWaitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func swStartAsync(cluster *Cluster, client *fake.Clientset, timeout time.Duration) chan error {
	out := make(chan error, 1)
	go func() { out <- cluster.k8sStartDatabaseServiceWithWait(client, "db1", timeout, 5*time.Millisecond) }()
	return out
}

// swCalls counts the Pod lists and Deployment reads one clientset served. The counters are atomics
// read without touching the clientset: a clientset whose reactor is blocked (swHoldPodList) holds its own
// lock, so asking it for its recorded actions would never return.
type swCalls struct{ lists, gets atomic.Int32 }

func swCount(client *fake.Clientset) *swCalls {
	c := &swCalls{}
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		c.lists.Add(1)
		return false, nil, nil
	})
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		c.gets.Add(1)
		return false, nil, nil
	})
	return c
}

// swRequireStillWaiting is the precondition of every interleaving below: the second Start is really
// inside its wait (it has read the Deployment, re-read it at least once, listed the Pods wantLists times
// and not returned) when the other operation acts; otherwise the test would not have exercised the
// overlap it claims to.
func swRequireStillWaiting(t *testing.T, c *swCalls, ch chan error, wantLists int32) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); c.lists.Load() < wantLists; {
		if time.Now().After(deadline) {
			t.Fatalf("PRECONDITION: the second Start made %d Pod lists, wanted %d", c.lists.Load(), wantLists)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-ch:
		t.Fatalf("PRECONDITION: the second Start returned (%v) before the other operation acted", err)
	default:
	}
	if c.gets.Load() < 2 {
		t.Fatalf("PRECONDITION: the second Start has not re-read the Deployment while waiting (%d reads)", c.gets.Load())
	}
}

func swResult(t *testing.T, ch chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(within):
		t.Fatalf("the second Start did not return within %s", within)
		return nil
	}
}

// B read replicas=0 and waits for an old Pod; while it is held inside a Pod list, A (a whole Start,
// for real) scales the Deployment up and the controller creates the new Pod. B must not fail on that
// Pod: it sees the Deployment is no longer at zero and returns, without a second patch.
func TestK8SStart_ConcurrentStart_SecondStartDoesNotTimeOutOnTheFirstStartsPod(t *testing.T) {
	clientA, clientB := swTwoClients(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	cluster := newTestCluster(swNS)
	held, release := swHoldPodList(clientB, 3)
	callsB := swCount(clientB) // after the hold: prepended reactors run first, so the held call is counted

	errB := swStartAsync(cluster, clientB, 2*time.Second)
	swWaitFor(t, held, "B's third Pod list")
	swRequireStillWaiting(t, callsB, errB, 3)
	if got := swReplicas(t, clientA); got != 0 {
		t.Fatalf("PRECONDITION: replicas %d before the first Start, want 0", got)
	}

	if err := clientA.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := cluster.k8sStartDatabaseServiceWithWait(clientA, "db1", swTimeout, swPoll); err != nil {
		t.Fatalf("first Start must succeed: %s", err)
	}
	if _, err := clientA.CoreV1().Pods(swNS).Create(context.TODO(), swPod("db1-new", swClaim, false), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	close(release)

	start := time.Now()
	if err := swResult(t, errB, time.Second); err != nil {
		t.Fatalf("second Start must succeed once the first one has: %s", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("second Start took %s after the first one succeeded", took)
	}
	if got := swReplicas(t, clientA); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
	if n := swCountActions(clientB, "patch", "deployments"); n != 0 {
		t.Fatalf("the second Start must not patch a Deployment that is already scaled up, it made %d patch(es)", n)
	}
	if n := swCountActions(clientA, "patch", "deployments"); n != 1 {
		t.Fatalf("expected exactly one scale-up patch overall, got %d", n)
	}
}

// Someone else scales the Deployment to more than one while the Start waits: the Start still ends
// by bringing it back to one, as it always did on a Deployment that already runs.
func TestK8SStart_ConcurrentStart_ExternalScaleAboveOneIsBroughtBackToOne(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	cluster := newTestCluster(swNS)
	calls := swCount(client)
	errB := swStartAsync(cluster, client, 2*time.Second)
	swRequireStillWaiting(t, calls, errB, 3)
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("PRECONDITION: replicas %d before the external scale-up, want 0", got)
	}
	swSetReplicas(t, client, 3)
	if err := swResult(t, errB, time.Second); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

// Replicas already non-zero while an old Pod still holds the claim (an external scale-up, not a
// guarded Start): the second Start writes nothing and does not wait for the full deadline. It is the
// decision a Start makes on a Deployment that already runs; it never scaled up itself.
func TestK8SStart_ConcurrentStart_ExternalScaleUpWritesNothing(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	cluster := newTestCluster(swNS)
	calls := swCount(client)
	errB := swStartAsync(cluster, client, 2*time.Second)
	swRequireStillWaiting(t, calls, errB, 3)
	if got := swReplicas(t, client); got != 0 {
		t.Fatalf("PRECONDITION: replicas %d before the external scale-up, want 0", got)
	}
	swSetReplicas(t, client, 1)
	if err := swResult(t, errB, time.Second); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	// the one patch is the test's own
	if n := swCountActions(client, "patch", "deployments"); n != 1 {
		t.Fatalf("the Start must not patch: %d patches recorded, want only the test's", n)
	}
}

// A scale-up that is undone (a Stop) while the second Start waits must not release it: the Deployment is
// at zero again and the old Pod still holds the claim, so the Start keeps waiting, and it scales up only
// after that Pod is gone.
func TestK8SStart_ConcurrentStart_ScaleUpThenScaleDownKeepsWaiting(t *testing.T) {
	clientA, clientB := swTwoClients(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	cluster := newTestCluster(swNS)
	var podGone atomic.Bool
	var patchedBeforeRelease atomic.Bool
	clientB.PrependReactor("patch", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if !podGone.Load() {
			patchedBeforeRelease.Store(true)
		}
		return false, nil, nil
	})
	held, release := swHoldPodList(clientB, 3)
	callsB := swCount(clientB) // after the hold: prepended reactors run first, so the held call is counted

	errB := swStartAsync(cluster, clientB, 3*time.Second)
	swWaitFor(t, held, "B's third Pod list")
	swRequireStillWaiting(t, callsB, errB, 3)
	swSetReplicas(t, clientA, 1) // another Start scales up ...
	swSetReplicas(t, clientA, 0) // ... and a Stop undoes it before B looks again
	close(release)

	select {
	case err := <-errB:
		t.Fatalf("the Start returned (%v) while the old Pod still holds the claim", err)
	case <-time.After(150 * time.Millisecond): // still waiting, as it must
	}
	podGone.Store(true)
	if err := clientA.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := swResult(t, errB, time.Second); err != nil {
		t.Fatalf("the Start must go through once the Pod is gone: %s", err)
	}
	if patchedBeforeRelease.Load() {
		t.Fatal("scaled up while the old Pod still held the PVC")
	}
	if got := swReplicas(t, clientA); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

// A Deployment read that fails never counts as "scaled up" and never lets the Start through: it
// retries until the deadline, scales nothing, and says why.
func TestK8SStart_ConcurrentStart_RereadErrorFailsClosed(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	var gets atomic.Int32
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if gets.Add(1) > 1 { // the first read (before the wait) succeeds, every later one fails
			return true, nil, apierrors.NewServiceUnavailable("etcd restarting")
		}
		return false, nil, nil
	})
	go func() { // the old Pod goes: with a healthy read the Start would now scale up
		time.Sleep(30 * time.Millisecond)
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{})
	}()
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll)
	if gets.Load() < 2 {
		t.Fatalf("PRECONDITION: the injected re-read failure never fired (%d Deployment reads)", gets.Load())
	}
	if pods, _ := client.CoreV1().Pods(swNS).List(context.TODO(), metav1.ListOptions{}); len(pods.Items) != 0 {
		t.Fatalf("PRECONDITION: the old Pod was still there, so the failure is not what stopped the Start")
	}
	if err == nil || !strings.Contains(err.Error(), "cannot confirm") || !strings.Contains(err.Error(), "etcd restarting") {
		t.Fatalf("expected a fail-closed error naming the read failure, got %v", err)
	}
	if n := swCountActions(client, "patch", "deployments"); n != 0 {
		t.Fatalf("nothing may be scaled while the Deployment cannot be read, %d patch(es)", n)
	}
}

func TestK8SStart_ConcurrentStart_TransientRereadErrorIsRetried(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	var gets atomic.Int32
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if n := gets.Add(1); n == 2 || n == 3 {
			return true, nil, apierrors.NewTimeoutError("hiccup", 1)
		}
		return false, nil, nil
	})
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = client.CoreV1().Pods(swNS).Delete(context.TODO(), "db1-old", metav1.DeleteOptions{})
	}()
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 2*time.Second, swPoll); err != nil {
		t.Fatalf("a transient read error must be retried: %s", err)
	}
	if gets.Load() < 4 {
		t.Fatalf("PRECONDITION: the two injected read failures did not both fire (%d Deployment reads)", gets.Load())
	}
	if got := swReplicas(t, client); got != 1 {
		t.Fatalf("replicas %d, want 1", got)
	}
}

func TestK8SStart_ConcurrentStart_RereadForbiddenFailsAtOnce(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	var gets atomic.Int32
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if gets.Add(1) > 1 {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "db1", context.DeadlineExceeded)
		}
		return false, nil, nil
	})
	start := time.Now()
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", 5*time.Second, swPoll)
	if err == nil || !strings.Contains(err.Error(), "`get` on `deployments`") {
		t.Fatalf("expected an error naming the missing permission, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("a refusal must not wait for the deadline: %s", time.Since(start))
	}
	if n := swCountActions(client, "patch", "deployments"); n != 0 {
		t.Fatalf("no patch expected, %d", n)
	}
}

// The common path is unchanged: with no Pod holding the claim there is one Deployment read and one
// patch, no re-read; and the deadline still bounds a wait that nothing ends.
func TestK8SStart_ConcurrentStart_CommonPathAndDeadlineUnchanged(t *testing.T) {
	client := fake.NewSimpleClientset(swDeployment(0, swRecreate()))
	if err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(client, "db1", swTimeout, swPoll); err != nil {
		t.Fatal(err)
	}
	if g, p := swCountActions(client, "get", "deployments"), swCountActions(client, "patch", "deployments"); g != 1 || p != 1 {
		t.Fatalf("want 1 read and 1 patch with nothing to wait for, got %d and %d", g, p)
	}

	stuck := fake.NewSimpleClientset(swDeployment(0, swRecreate()), swPod("db1-old", swClaim, true))
	start := time.Now()
	err := newTestCluster(swNS).k8sStartDatabaseServiceWithWait(stuck, "db1", swTimeout, swPoll)
	if err == nil || !strings.Contains(err.Error(), "Pod(s) still use PVC") {
		t.Fatalf("expected the fail-closed timeout, got %v", err)
	}
	if took := time.Since(start); took < swTimeout || took > swTimeout+500*time.Millisecond {
		t.Fatalf("the deadline is not respected: %s for %s", took, swTimeout)
	}
	if n := swCountActions(stuck, "patch", "deployments"); n != 0 {
		t.Fatalf("no patch expected on a timeout, %d", n)
	}
}
