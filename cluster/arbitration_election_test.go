package cluster

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// The election against a scripted arbitrator: a 503 (JSON from the arbitrator, or HTML
// from a gateway with no instance left) and an "error" verdict touch the unreachable
// streak and never the status; looser moves the cluster only after the streak; a winner
// restores it and resets the counts (#1929).
func TestArbitratorElectionVerdictHandling(t *testing.T) {
	var mu sync.Mutex
	answers := []func(w http.ResponseWriter){}
	next := func() func(w http.ResponseWriter) {
		mu.Lock()
		defer mu.Unlock()
		if len(answers) == 0 {
			return func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"arbitration":"winner"}`))
			}
		}
		a := answers[0]
		answers = answers[1:]
		return a
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next()(w) }))
	defer srv.Close()
	queue := func(fns ...func(w http.ResponseWriter)) {
		mu.Lock()
		answers = append(answers, fns...)
		mu.Unlock()
	}
	json503 := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"arbitration":"error","error":"arbitration store unavailable"}`))
	}
	html503 := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`<html><body><h1>503 Service Unavailable</h1></body></html>`))
	}
	looser := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"arbitration":"looser","master":""}`))
	}
	winner := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"arbitration":"winner","master":""}`))
	}
	errorVerdict := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"arbitration":"error","error":"x"}`))
	}

	cl := &Cluster{Name: "t", Status: ConstMonitorActif, StateMachine: new(state.StateMachine), Conf: &config.Config{
		Arbitration: true, ArbitrationSasHosts: srv.URL, ArbitrationSasSecret: "s", MonitoringTicker: 2, ArbitrationReadTimout: 800, ArbitrationVerdictStreak: 3,
	}}
	cl.StateMachine.Init()

	// 503s and an error verdict: unreachable streak, never a loser count; the status is
	// kept until the streak is reached, then the minority fail-safe yields
	queue(json503, html503, errorVerdict)
	for i := 1; i <= 3; i++ {
		if err := cl.arbitratorElection(); err == nil {
			t.Fatalf("answer %d must be an error", i)
		}
		want := ConstMonitorActif
		if i == 3 {
			want = ConstMonitorStandby // the streak is reached: the fail-safe yields
		}
		if cl.Status != want {
			t.Fatalf("answer %d: status %s, want %s", i, cl.Status, want)
		}
		if !cl.IsFailedArbitrator || cl.arbUnreachableStreak != i || cl.arbLoserStreak != 0 {
			t.Fatalf("answer %d: failed=%v unreachable=%d loser=%d", i, cl.IsFailedArbitrator, cl.arbUnreachableStreak, cl.arbLoserStreak)
		}
	}
	// a winner clears the arbitrator failure and the unreachable streak
	queue(winner)
	if err := cl.arbitratorElection(); err != nil || cl.IsFailedArbitrator || cl.arbUnreachableStreak != 0 || cl.Status != ConstMonitorActif {
		t.Fatalf("winner: err=%v failed=%v unreachable=%d status=%s", err, cl.IsFailedArbitrator, cl.arbUnreachableStreak, cl.Status)
	}
	// two loosers keep the status, the third moves it
	queue(looser, looser)
	for i := 1; i <= 2; i++ {
		if err := cl.arbitratorElection(); err != nil || cl.Status != ConstMonitorActif || cl.arbLoserStreak != i {
			t.Fatalf("looser %d: err=%v status=%s streak=%d", i, err, cl.Status, cl.arbLoserStreak)
		}
	}
	queue(looser)
	if err := cl.arbitratorElection(); err != nil || cl.Status != ConstMonitorStandby {
		t.Fatalf("third looser: err=%v status=%s", err, cl.Status)
	}
	// one winner restores active and resets the loser count
	queue(winner)
	if err := cl.arbitratorElection(); err != nil || cl.Status != ConstMonitorActif || cl.arbLoserStreak != 0 {
		t.Fatalf("winner after loss: err=%v status=%s streak=%d", err, cl.Status, cl.arbLoserStreak)
	}
	// streak 1: every answer counts, as before
	cl.Conf.ArbitrationVerdictStreak = 1
	queue(looser)
	if err := cl.arbitratorElection(); err != nil || cl.Status != ConstMonitorStandby {
		t.Fatalf("streak 1: one looser moves the cluster, err=%v status=%s", err, cl.Status)
	}
}
