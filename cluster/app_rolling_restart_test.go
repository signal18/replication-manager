package cluster

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// fakeInstances: the up instances of a flex service, changed by stop/start like the
// orchestrator; neverUp names an instance whose start never brings it up.
type fakeInstances struct {
	mu      sync.Mutex
	up      map[string]bool
	calls   []string
	minUp   int
	neverUp string
}

func newFakeInstances(nodes ...string) *fakeInstances {
	f := &fakeInstances{up: map[string]bool{}, minUp: len(nodes)}
	for _, n := range nodes {
		f.up[n] = true
	}
	return f
}

func (f *fakeInstances) count() int {
	n := 0
	for _, up := range f.up {
		if up {
			n++
		}
	}
	return n
}

func (f *fakeInstances) stop(node string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop "+node)
	f.up[node] = false
	if c := f.count(); c < f.minUp {
		f.minUp = c
	}
	return nil
}

func (f *fakeInstances) start(node string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "start "+node)
	if node != f.neverUp {
		f.up[node] = true
	}
	return nil
}

func (f *fakeInstances) upNodes() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var up []string
	for n, ok := range f.up {
		if ok {
			up = append(up, n)
		}
	}
	return up, nil
}

func noLog(string, string, ...interface{}) {}

// Node by node: each instance is stopped, seen down, started and seen up before the next
// one; never more than one instance down at a time (#2217).
func TestRollAppInstancesOneAtATime(t *testing.T) {
	f := newFakeInstances("n1", "n2", "n3")
	done, err := rollAppInstances([]string{"n1", "n2", "n3"}, f.stop, f.start, f.upNodes, time.Second, time.Millisecond, noLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(done, ",") != "n1,n2,n3" {
		t.Fatalf("restarted %v, want n1,n2,n3 in order", done)
	}
	want := "stop n1,start n1,stop n2,start n2,stop n3,start n3"
	if got := strings.Join(f.calls, ","); got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}
	if f.minUp != 2 {
		t.Fatalf("at least 2 of 3 instances must stay up, the lowest was %d", f.minUp)
	}
}

// An instance that does not come back ends the roll with an error naming its node; the
// nodes not reached yet are never stopped (#2217).
func TestRollAppInstancesStopsAtFirstFailure(t *testing.T) {
	f := newFakeInstances("n1", "n2", "n3")
	f.neverUp = "n2"
	done, err := rollAppInstances([]string{"n1", "n2", "n3"}, f.stop, f.start, f.upNodes, 50*time.Millisecond, time.Millisecond, noLog)
	if err == nil || !strings.Contains(err.Error(), "n2") || !strings.Contains(err.Error(), "not up") {
		t.Fatalf("want an error naming n2 not up, got %v", err)
	}
	if strings.Join(done, ",") != "n1" {
		t.Fatalf("restarted %v, want n1 only", done)
	}
	for _, c := range f.calls {
		if strings.HasSuffix(c, "n3") {
			t.Fatalf("n3 must stay untouched, got call %q", c)
		}
	}
	if up, _ := f.upNodes(); len(up) != 2 {
		t.Fatalf("n1 and n3 must be up, got %v", up)
	}
}

// A stop refused by the orchestrator ends the roll on that node, nothing else is touched.
func TestRollAppInstancesStopError(t *testing.T) {
	f := newFakeInstances("n1", "n2")
	stop := func(node string) error { return errors.New("refused") }
	done, err := rollAppInstances([]string{"n1", "n2"}, stop, f.start, f.upNodes, time.Second, time.Millisecond, noLog)
	if err == nil || !strings.Contains(err.Error(), "stop on n1") || len(done) != 0 {
		t.Fatalf("want stop on n1 error and nothing done, got %v %v", done, err)
	}
}

// One rolling restart at a time per app; the flag clears when it ends (#2217).
func TestTryStartRollingRestart(t *testing.T) {
	app := &App{Mutex: &sync.Mutex{}}
	if !app.TryStartRollingRestart() {
		t.Fatal("first start must be accepted")
	}
	if app.TryStartRollingRestart() {
		t.Fatal("second start must be refused while one runs")
	}
	if !app.GetAppAPIView().RollingRestartInProgress {
		t.Fatal("the API view must show the rolling restart")
	}
	app.EndRollingRestart()
	if !app.TryStartRollingRestart() {
		t.Fatal("a new start must be accepted once the previous one ended")
	}
}

// No node, ALL or * restart every instance; a node name only that one.
func TestAppRestartAllNodes(t *testing.T) {
	for _, n := range []string{"", "ALL", "all", "*"} {
		if !appRestartAllNodes(n) {
			t.Fatalf("%q: every instance", n)
		}
	}
	if appRestartAllNodes("s18-fr-4") {
		t.Fatal("a node name: that instance only")
	}
}

// The nodes a roll goes over: none up is an error, one up of several is refused (stopping it
// stops the service), a one-copy flex app rolls its instance (#2217, review).
func TestRollingNodes(t *testing.T) {
	if _, err := rollingNodes(nil, 3); err == nil || !strings.Contains(err.Error(), "no instance is up") {
		t.Fatalf("none up: want an error, got %v", err)
	}
	if _, err := rollingNodes([]string{"n1"}, 3); err == nil || !strings.Contains(err.Error(), "only 1 of 3") || !strings.Contains(err.Error(), "n1") {
		t.Fatalf("1 of 3 up: want a refusal naming n1, got %v", err)
	}
	if nodes, err := rollingNodes([]string{"n1"}, 1); err != nil || len(nodes) != 1 {
		t.Fatalf("one-copy flex app: roll its instance, got %v %v", nodes, err)
	}
	if nodes, err := rollingNodes([]string{"n1", "n2"}, 3); err != nil || len(nodes) != 2 {
		t.Fatalf("2 of 3 up: roll those two, got %v %v", nodes, err)
	}
}

// The start timeout falls back to the named default when the setting does not parse.
func TestAppStartTimeout(t *testing.T) {
	app := &App{AppConfig: &config.AppConfig{ProvAppStartTimeout: "15m"}, ClusterGroup: &Cluster{Conf: &config.Config{}}}
	if d := appStartTimeout(app); d != 15*time.Minute {
		t.Fatalf("15m: got %s", d)
	}
	app.AppConfig.ProvAppStartTimeout = "soon"
	if d := appStartTimeout(app); d != 2*time.Minute {
		t.Fatalf("unparsable: want the 2m default, got %s", d)
	}
}
