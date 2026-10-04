package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	apps "k8s.io/api/apps/v1"
)

const testHelperImage = "percona/percona-xtrabackup:8.4"

func resetHelperState() {
	xtrabackupHelperMu.Lock()
	xtrabackupHelperEntries = map[string]*xtrabackupHelperEntry{}
	xtrabackupHelperInflight = map[string]*xtrabackupHelperFlight{}
	xtrabackupHelperMu.Unlock()
}

// expireHelperVerdicts makes every kept answer old, as if xtrabackupHelperTTL had passed.
func expireHelperVerdicts() {
	xtrabackupHelperMu.Lock()
	for _, e := range xtrabackupHelperEntries {
		e.until = time.Now().Add(-time.Second)
	}
	xtrabackupHelperMu.Unlock()
}

// stubHelperCheck replaces the registry question for one test and counts the questions asked.
func stubHelperCheck(t *testing.T, answer func(image string) error) *int {
	t.Helper()
	var mu sync.Mutex
	asked := 0
	previous := xtrabackupHelperCheck
	xtrabackupHelperCheck = func(_ context.Context, image string) error {
		mu.Lock()
		asked++
		mu.Unlock()
		return answer(image)
	}
	resetHelperState()
	t.Cleanup(func() {
		xtrabackupHelperCheck = previous
		resetHelperState()
	})
	return &asked
}

func registryAnswer(status int) error { return &transport.Error{StatusCode: status} }

func TestXtrabackupHelperRefused(t *testing.T) {
	_, badName := name.ParseReference("not a reference")
	for desc, tc := range map[string]struct {
		err  error
		want bool
	}{
		"not found":          {registryAnswer(http.StatusNotFound), true},
		"unauthorized":       {registryAnswer(http.StatusUnauthorized), true},
		"forbidden":          {registryAnswer(http.StatusForbidden), true},
		"bad reference":      {badName, true},
		"server error":       {registryAnswer(http.StatusBadGateway), false},
		"too many requests":  {registryAnswer(http.StatusTooManyRequests), false},
		"timeout":            {context.DeadlineExceeded, false},
		"connection refused": {errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), false},
	} {
		if got := xtrabackupHelperRefused(tc.err); got != tc.want {
			t.Errorf("%s: refused = %v, want %v", desc, got, tc.want)
		}
	}
}

// A helper image the registry refuses must leave the whole injection off: nothing rendered (the database starts), on
// both orchestrators, and the PATH of the jobs container untouched.
func TestXtrabackupHelperRefusedLeavesInjectionOff(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden} {
		stubHelperCheck(t, func(string) error { return registryAnswer(status) })
		cluster := newBundleCluster("mysql:8.4", "percona/percona-xtrabackup:8.4-typo")
		if cluster.xtrabackupBundleEnabled() || cluster.xtrabackupBundlePath() != "" {
			t.Errorf("registry answer %d: the injection must be off", status)
		}
		if len(cluster.OpenSVCGetXtrabackupBundleContainerSection()) != 0 {
			t.Errorf("registry answer %d: the OpenSVC init container must not be rendered", status)
		}
		if got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetJobsContainerSection()["environment"]; got != "MYSQL_INITDB_SKIP_TZINFO=yes" {
			t.Errorf("registry answer %d: jobs environment = %q", status, got)
		}
	}
}

// The registry cannot be reached: an image never confirmed is not rendered (a helper that cannot be pulled would keep
// the pod from starting), one confirmed earlier is (a cluster that runs with the injection keeps it through an outage).
func TestXtrabackupHelperNetworkError(t *testing.T) {
	down := true
	stubHelperCheck(t, func(string) error {
		if down {
			return context.DeadlineExceeded
		}
		return nil
	})
	cluster := newBundleCluster("mysql:8.4", testHelperImage)
	if cluster.xtrabackupBundleEnabled() {
		t.Fatal("registry unreachable and the image never confirmed: the injection must not be rendered")
	}
	down = false
	expireHelperVerdicts()
	if !cluster.xtrabackupBundleEnabled() {
		t.Fatal("registry reachable again: the injection must be rendered")
	}
	down = true
	expireHelperVerdicts()
	if !cluster.xtrabackupBundleEnabled() {
		t.Error("registry unreachable but the image confirmed earlier: the injection must stay rendered")
	}
}

// One render asks the question once, and an answer is only kept for the TTL: a tag that is gone, or access that is
// revoked, is seen at the next provisioning after it, and a registry that comes back as well.
func TestXtrabackupHelperAnswersExpire(t *testing.T) {
	var answer error
	asked := stubHelperCheck(t, func(string) error { return answer })
	cluster := newBundleCluster("mysql:8.4", testHelperImage)
	for i := 0; i < 5; i++ {
		if !cluster.xtrabackupBundleEnabled() {
			t.Fatal("the helper must be enabled")
		}
	}
	if *asked != 1 {
		t.Errorf("the registry was asked %d times for 5 renders, want 1", *asked)
	}

	answer = registryAnswer(http.StatusNotFound) // the tag was removed
	if !cluster.xtrabackupBundleEnabled() {
		t.Error("within the TTL the answer is kept")
	}
	expireHelperVerdicts()
	if cluster.xtrabackupBundleEnabled() {
		t.Error("after the TTL a tag that is gone must turn the injection off: the next provisioning would block the pod")
	}
	if *asked != 2 {
		t.Errorf("asked %d times, want 2", *asked)
	}

	// refused wipes the earlier confirmation: an outage afterwards must not bring the injection back
	answer = context.DeadlineExceeded
	expireHelperVerdicts()
	if cluster.xtrabackupBundleEnabled() {
		t.Error("refused, then the registry unreachable: the injection must stay off")
	}

	answer = nil // corrected
	expireHelperVerdicts()
	if !cluster.xtrabackupBundleEnabled() {
		t.Error("after the TTL a corrected registry must be noticed")
	}
}

// Callers asking for the same image meanwhile share one request, and a request that is slow for one image holds
// nothing for another (no lock is held while the network is used).
func TestXtrabackupHelperSharedAndLockFree(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 8)
	asked := stubHelperCheck(t, func(image string) error {
		if image == "slow/image:1" {
			started <- image
			<-release
		}
		return nil
	})
	cluster := newBundleCluster("mysql:8.4", testHelperImage)

	var wg sync.WaitGroup
	results := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- cluster.xtrabackupHelperPullable("slow/image:1") }()
	}
	<-started // the slow request is in flight

	other := make(chan bool)
	go func() { other <- cluster.xtrabackupHelperPullable("fast/image:1") }()
	select {
	case ok := <-other:
		if !ok {
			t.Error("the other image must be answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a slow request for one image blocked the answer for another")
	}

	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Error("the callers of the slow image must all get its answer")
		}
	}
	if *asked != 2 {
		t.Errorf("the registry was asked %d times, want 2 (one per image, the five callers shared one)", *asked)
	}
}

// The setting is inert on a MariaDB image (it ships its own tools): nothing is rendered on OpenSVC or Kubernetes,
// whatever the value, and the registry is not even asked.
func TestXtrabackupInertOnMariaDB(t *testing.T) {
	asked := stubHelperCheck(t, func(string) error { return nil })
	for _, image := range []string{"mariadb:10.11", "mariadb:11.8", "docker.io/library/mariadb:11.4"} {
		for _, setting := range []string{"auto", testHelperImage} {
			cluster := newBundleCluster(image, setting)
			if cluster.xtrabackupBundleEnabled() || cluster.xtrabackupBundlePath() != "" {
				t.Errorf("%s with %q: the injection must be off", image, setting)
			}
			if len(cluster.OpenSVCGetXtrabackupBundleContainerSection()) != 0 {
				t.Errorf("%s with %q: the OpenSVC init container must not be rendered", image, setting)
			}
			sm := &ServerMonitor{ClusterGroup: cluster}
			if got := sm.OpenSVCGetJobsContainerSection()["environment"]; got != "MYSQL_INITDB_SKIP_TZINFO=yes" {
				t.Errorf("%s with %q: jobs environment = %q", image, setting, got)
			}
		}
	}
	if *asked != 0 {
		t.Errorf("the registry was asked %d times for MariaDB images, want 0", *asked)
	}
}

// What a cluster confirmed is its own: another cluster may reach another registry or run on other workers. When the
// registry cannot be reached, a cluster that never confirmed the image does not render it, whatever another cluster
// confirmed, and the answer kept for one cluster is not given to the other.
func TestXtrabackupHelperConfirmationIsPerCluster(t *testing.T) {
	down := false
	stubHelperCheck(t, func(string) error {
		if down {
			return context.DeadlineExceeded
		}
		return nil
	})
	a := newBundleCluster("mysql:8.4", testHelperImage)
	a.Name = "cluster-a"
	b := newBundleCluster("mysql:8.4", testHelperImage)
	b.Name = "cluster-b"

	if !a.xtrabackupBundleEnabled() {
		t.Fatal("cluster A: the registry answers, the injection must be rendered")
	}
	down = true
	expireHelperVerdicts()
	if !a.xtrabackupBundleEnabled() {
		t.Error("cluster A confirmed the image: it keeps the injection through an outage")
	}
	// B asks while the registry is unreachable and A holds a fresh "render it" answer for the same image
	if b.xtrabackupBundleEnabled() {
		t.Error("cluster B never confirmed the image: A's confirmation, or A's kept answer, must not be inherited")
	}
	if !a.xtrabackupBundleEnabled() {
		t.Error("B's answer must not change A's")
	}
}

// The setting renders nothing on Kubernetes either, for a MariaDB image (it ships its own tools) and for a database
// image that is not an official one: the whole pod spec is the one the empty setting gives, so there is no emptyDir,
// no helper init container, no mount and no PATH.
func TestXtrabackupInertOnMariaDBKubernetes(t *testing.T) {
	asked := stubHelperCheck(t, func(string) error { return nil })
	build := func(dbImage, setting string) *apps.Deployment {
		cluster := newTestCluster("k8stest")
		cluster.Conf.ProvDbImg = dbImage
		cluster.Conf.ProvDbDockerXtrabackupImg = setting
		return cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1", Port: "3306"}, 3306, "node-a")
	}
	for _, image := range []string{"mariadb:10.11", "mariadb:11.8", "docker.io/library/mariadb:11.4", "myco/mysql:8.0-custom", "registry.example/mirror/mysql:8.4"} {
		legacy := build(image, "").Spec
		for _, setting := range []string{"auto", testHelperImage} {
			got := build(image, setting).Spec
			if !reflect.DeepEqual(legacy, got) {
				t.Errorf("%s with %q: the pod spec differs from the one of the empty setting", image, setting)
			}
			for _, v := range got.Template.Spec.Volumes {
				if v.Name == "db1-xtrabackup" {
					t.Errorf("%s with %q: an xtrabackup volume was rendered", image, setting)
				}
			}
		}
	}
	if *asked != 0 {
		t.Errorf("the registry was asked %d times for images the injection does not apply to, want 0", *asked)
	}
}

// The memory of the check is bounded (F4): an entry whose answer and confirmation have both expired is dropped, a
// confirmation expires, and the table never holds more than xtrabackupHelperMaxEntries however many clusters come.
func TestXtrabackupHelperMemoryIsBounded(t *testing.T) {
	stubHelperCheck(t, func(string) error { return nil })
	size := func() int {
		xtrabackupHelperMu.Lock()
		defer xtrabackupHelperMu.Unlock()
		return len(xtrabackupHelperEntries)
	}

	// many clusters, each with its own entry: the table stops at the bound
	for i := 0; i < xtrabackupHelperMaxEntries*2; i++ {
		c := newBundleCluster("mysql:8.4", testHelperImage)
		c.Name = fmt.Sprintf("cluster-%d", i)
		c.xtrabackupHelperPullable(testHelperImage)
	}
	if got := size(); got > xtrabackupHelperMaxEntries {
		t.Errorf("%d entries after %d clusters, want at most %d", got, xtrabackupHelperMaxEntries*2, xtrabackupHelperMaxEntries)
	}

	// the setting changes again and again: old images do not pile up once they expire
	resetHelperState()
	c := newBundleCluster("mysql:8.4", testHelperImage)
	c.Name = "changing"
	for i := 0; i < 50; i++ {
		c.xtrabackupHelperPullable(fmt.Sprintf("percona/percona-xtrabackup:8.4-%d", i))
		// answer and confirmation of what was asked so far expire
		xtrabackupHelperMu.Lock()
		for _, e := range xtrabackupHelperEntries {
			e.until = time.Now().Add(-time.Second)
			e.confirmedUntil = time.Now().Add(-time.Second)
		}
		xtrabackupHelperMu.Unlock()
	}
	if got := size(); got > 1 {
		t.Errorf("%d entries kept for 50 images that all expired, want at most the last one", got)
	}
}

// A confirmation does not last forever: after xtrabackupHelperConfirmTTL without the registry handing the image out,
// an unreachable registry no longer renders the injection.
func TestXtrabackupHelperConfirmationExpires(t *testing.T) {
	down := false
	stubHelperCheck(t, func(string) error {
		if down {
			return context.DeadlineExceeded
		}
		return nil
	})
	cluster := newBundleCluster("mysql:8.4", testHelperImage)
	cluster.Name = "c"
	if !cluster.xtrabackupBundleEnabled() {
		t.Fatal("must be enabled")
	}
	down = true
	expireHelperVerdicts()
	if !cluster.xtrabackupBundleEnabled() {
		t.Fatal("a recent confirmation keeps the injection through an outage")
	}
	xtrabackupHelperMu.Lock()
	for _, e := range xtrabackupHelperEntries {
		e.confirmedUntil = time.Now().Add(-time.Second)
	}
	xtrabackupHelperMu.Unlock()
	expireHelperVerdicts()
	if cluster.xtrabackupBundleEnabled() {
		t.Error("an old confirmation must not render the injection when the registry cannot be reached")
	}
}

// The requests in flight are bounded (F4): however many clusters provision at once, no more than
// xtrabackupHelperMaxInflight reach the registry and the table of flights stays within that. The callers beyond the
// bound take turns: each waits for a slot, so every cluster is asked in the end.
func TestXtrabackupHelperInflightIsBoundedAndTakesTurns(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	running, peak := 0, 0
	asked := stubHelperCheck(t, func(string) error {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	})

	const callers = xtrabackupHelperMaxInflight + 12
	results := make(chan bool, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		c := newBundleCluster("mysql:8.4", testHelperImage)
		c.Name = fmt.Sprintf("parallel-%d", i)
		wg.Add(1)
		go func() { defer wg.Done(); results <- c.xtrabackupHelperPullable(testHelperImage) }()
	}

	// the registry is slow: only xtrabackupHelperMaxInflight requests are out, the other callers wait for a turn
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := running
		mu.Unlock()
		if n == xtrabackupHelperMaxInflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d requests running, want %d", n, xtrabackupHelperMaxInflight)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the others must not slip through
	xtrabackupHelperMu.Lock()
	flights := len(xtrabackupHelperInflight)
	xtrabackupHelperMu.Unlock()
	mu.Lock()
	out := running
	mu.Unlock()
	if flights > xtrabackupHelperMaxInflight || out > xtrabackupHelperMaxInflight {
		t.Errorf("%d flights and %d requests out, want at most %d", flights, out, xtrabackupHelperMaxInflight)
	}

	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Error("every cluster must be answered by the registry in the end, the ones that waited included")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > xtrabackupHelperMaxInflight {
		t.Errorf("the registry was reached by %d requests at once, want at most %d", peak, xtrabackupHelperMaxInflight)
	}
	if *asked != callers {
		t.Errorf("%d clusters were asked, want %d (each takes its turn)", *asked, callers)
	}
}

// A turn that does not come within xtrabackupHelperSlotWait: the registry could not be asked, so the caller is answered
// as when it cannot be reached (rendered only when confirmed) and nothing is kept for it.
func TestXtrabackupHelperNoTurnInTime(t *testing.T) {
	release := make(chan struct{})
	asked := stubHelperCheck(t, func(string) error { <-release; return nil })
	previous := xtrabackupHelperSlotWait
	xtrabackupHelperSlotWait = 100 * time.Millisecond
	t.Cleanup(func() { xtrabackupHelperSlotWait = previous })

	var wg sync.WaitGroup
	for i := 0; i < xtrabackupHelperMaxInflight; i++ {
		c := newBundleCluster("mysql:8.4", testHelperImage)
		c.Name = fmt.Sprintf("holder-%d", i)
		wg.Add(1)
		go func() { defer wg.Done(); c.xtrabackupHelperPullable(testHelperImage) }()
	}
	for len(xtrabackupHelperSlots) < xtrabackupHelperMaxInflight {
		time.Sleep(5 * time.Millisecond)
	}

	late := newBundleCluster("mysql:8.4", testHelperImage)
	late.Name = "late"
	start := time.Now()
	if late.xtrabackupHelperPullable(testHelperImage) {
		t.Error("no turn in time and never confirmed: the injection must not be rendered")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the caller waited %s for a turn, want about %s", time.Since(start), xtrabackupHelperSlotWait)
	}
	xtrabackupHelperMu.Lock()
	_, kept := xtrabackupHelperEntries["late\x00"+testHelperImage]
	xtrabackupHelperMu.Unlock()
	if kept {
		t.Error("an answer that was not asked for must not be kept")
	}

	// a cluster that confirmed the image earlier keeps its injection
	confirmed := newBundleCluster("mysql:8.4", testHelperImage)
	confirmed.Name = "confirmed"
	xtrabackupHelperMu.Lock()
	xtrabackupHelperEntries["confirmed\x00"+testHelperImage] = &xtrabackupHelperEntry{pullable: true, until: time.Now().Add(-time.Second), confirmedUntil: time.Now().Add(time.Hour), touched: time.Now()}
	xtrabackupHelperMu.Unlock()
	if !confirmed.xtrabackupHelperPullable(testHelperImage) {
		t.Error("no turn in time but confirmed earlier: the injection must stay rendered")
	}

	close(release)
	wg.Wait()
	if *asked != xtrabackupHelperMaxInflight {
		t.Errorf("%d requests reached the registry, want %d", *asked, xtrabackupHelperMaxInflight)
	}
}

// The queue is bounded too (F4): when the slots are taken and xtrabackupHelperMaxWaiting callers already wait, a caller
// is not queued, it is answered at once as when the registry cannot be reached, and nothing is kept for it. The ones
// that wait still get their turn.
func TestXtrabackupHelperQueueIsBounded(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	running := 0
	asked := stubHelperCheck(t, func(string) error {
		mu.Lock()
		running++
		mu.Unlock()
		<-release
		return nil
	})

	const callers = xtrabackupHelperMaxInflight + xtrabackupHelperMaxWaiting + 10
	results := make(chan bool, callers)
	var wg sync.WaitGroup
	start := func(i int) {
		c := newBundleCluster("mysql:8.4", testHelperImage)
		c.Name = fmt.Sprintf("queued-%d", i)
		wg.Add(1)
		go func() { defer wg.Done(); results <- c.xtrabackupHelperPullable(testHelperImage) }()
	}
	// fill the slots, then the queue, one step at a time so that the overflow is the last ones
	for i := 0; i < xtrabackupHelperMaxInflight; i++ {
		start(i)
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("the slots to be taken", func() bool { return len(xtrabackupHelperSlots) == xtrabackupHelperMaxInflight })
	for i := xtrabackupHelperMaxInflight; i < xtrabackupHelperMaxInflight+xtrabackupHelperMaxWaiting; i++ {
		start(i)
	}
	waitFor("the queue to fill", func() bool {
		xtrabackupHelperMu.Lock()
		defer xtrabackupHelperMu.Unlock()
		return xtrabackupHelperWaiting == xtrabackupHelperMaxWaiting
	})

	// the queue is full: the next callers come back at once, not rendered, without waiting for a request
	for i := xtrabackupHelperMaxInflight + xtrabackupHelperMaxWaiting; i < callers; i++ {
		start(i)
	}
	for i := 0; i < 10; i++ {
		select {
		case ok := <-results:
			if ok {
				t.Fatal("a caller that found the queue full was rendered without being confirmed")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 10 callers beyond the queue came back while the registry was slow", i)
		}
	}
	xtrabackupHelperMu.Lock()
	waiting := xtrabackupHelperWaiting
	xtrabackupHelperMu.Unlock()
	if waiting > xtrabackupHelperMaxWaiting {
		t.Errorf("%d callers waiting, want at most %d", waiting, xtrabackupHelperMaxWaiting)
	}

	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Error("the callers that waited must get their turn and the registry's answer")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if *asked != xtrabackupHelperMaxInflight+xtrabackupHelperMaxWaiting {
		t.Errorf("%d clusters were asked, want %d (the slots and the queue)", *asked, xtrabackupHelperMaxInflight+xtrabackupHelperMaxWaiting)
	}
	xtrabackupHelperMu.Lock()
	defer xtrabackupHelperMu.Unlock()
	if xtrabackupHelperWaiting != 0 || len(xtrabackupHelperInflight) != 0 {
		t.Errorf("after all the callers: %d waiting, %d in flight, want none", xtrabackupHelperWaiting, len(xtrabackupHelperInflight))
	}
}

// The callers that join a request already out for the same cluster and image are bounded too (F4): they count in the
// same queue as the ones waiting for a slot, so the goroutines and timers they take stay within xtrabackupHelperMaxWaiting.
// The ones beyond it are answered at once, not rendered unless confirmed, and the others get the answer of the request.
func TestXtrabackupHelperSharedWaitersAreBounded(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	asked := stubHelperCheck(t, func(string) error {
		started <- struct{}{}
		<-release
		return nil
	})
	cluster := newBundleCluster("mysql:8.4", testHelperImage)
	cluster.Name = "same"

	const extra = 10
	results := make(chan bool, 1+xtrabackupHelperMaxWaiting+extra)
	var wg sync.WaitGroup
	call := func() {
		wg.Add(1)
		go func() { defer wg.Done(); results <- cluster.xtrabackupHelperPullable(testHelperImage) }()
	}
	call() // the one that asks
	<-started
	for i := 0; i < xtrabackupHelperMaxWaiting; i++ {
		call()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		xtrabackupHelperMu.Lock()
		n := xtrabackupHelperWaiting
		xtrabackupHelperMu.Unlock()
		if n == xtrabackupHelperMaxWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d callers waiting on the shared request, want %d", n, xtrabackupHelperMaxWaiting)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// the queue is full: more callers for the same cluster and image come back at once
	for i := 0; i < extra; i++ {
		call()
	}
	for i := 0; i < extra; i++ {
		select {
		case ok := <-results:
			if ok {
				t.Fatal("a caller that found the queue full was rendered without being confirmed")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d callers beyond the queue came back while the request was out", i, extra)
		}
	}
	xtrabackupHelperMu.Lock()
	waiting := xtrabackupHelperWaiting
	xtrabackupHelperMu.Unlock()
	if waiting > xtrabackupHelperMaxWaiting {
		t.Errorf("%d callers waiting, want at most %d", waiting, xtrabackupHelperMaxWaiting)
	}

	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Error("the leader and the callers that waited must get the registry's answer")
		}
	}
	if *asked != 1 {
		t.Errorf("the registry was asked %d times, want 1", *asked)
	}
	xtrabackupHelperMu.Lock()
	defer xtrabackupHelperMu.Unlock()
	if xtrabackupHelperWaiting != 0 || len(xtrabackupHelperInflight) != 0 {
		t.Errorf("after all the callers: %d waiting, %d in flight, want none", xtrabackupHelperWaiting, len(xtrabackupHelperInflight))
	}
}

// A caller that joins a request which is slow does not wait for ever: after xtrabackupHelperSlotWait it is answered as
// when the registry cannot be reached, and the request itself still ends with its answer for the next render.
func TestXtrabackupHelperSharedWaiterTimesOut(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	stubHelperCheck(t, func(string) error {
		started <- struct{}{}
		<-release
		return nil
	})
	previous := xtrabackupHelperSlotWait
	xtrabackupHelperSlotWait = 100 * time.Millisecond
	t.Cleanup(func() { xtrabackupHelperSlotWait = previous })

	cluster := newBundleCluster("mysql:8.4", testHelperImage)
	cluster.Name = "slow"
	leader := make(chan bool, 1)
	go func() { leader <- cluster.xtrabackupHelperPullable(testHelperImage) }()
	<-started

	begin := time.Now()
	if cluster.xtrabackupHelperPullable(testHelperImage) {
		t.Error("no answer in time and never confirmed: the injection must not be rendered")
	}
	if time.Since(begin) > 5*time.Second {
		t.Errorf("the follower waited %s, want about %s", time.Since(begin), xtrabackupHelperSlotWait)
	}
	xtrabackupHelperMu.Lock()
	waiting := xtrabackupHelperWaiting
	xtrabackupHelperMu.Unlock()
	if waiting != 0 {
		t.Errorf("%d callers still counted as waiting after the timeout, want 0", waiting)
	}

	close(release)
	if !<-leader {
		t.Error("the request itself must end with the registry's answer")
	}
	if !cluster.xtrabackupHelperPullable(testHelperImage) {
		t.Error("the next render must find the answer of the request")
	}
}
