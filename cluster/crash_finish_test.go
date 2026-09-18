package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A finished outcome older than the CURRENT event (a newer working crash for the
// same URL) must not swallow the attempt that new crash is owed: a switchover
// stamped "no-divergence" at T1 must not block the automatic rejoin of the same
// server after a real failover at T2.
func TestRejoinAlreadyAttempted_OutcomeOlderThanCurrentEventDoesNotBlock(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}}
	url := "db1:3306"
	c.FailoverHistory = crashList{{URL: url, UnixTimestamp: 100, RejoinResult: RejoinResultNoDivergence, RejoinResultTs: 100}}
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("with no newer event the outcome must still count as attempted")
	}
	c.Crashes = crashList{{URL: url, UnixTimestamp: 200}}
	if c.rejoinAlreadyAttempted(url) {
		t.Fatalf("a working crash newer than the outcome is a new event: must not be blocked")
	}
	c.FailoverHistory = append(c.FailoverHistory, &Crash{URL: url, UnixTimestamp: 200, RejoinResult: RejoinResultSuccess, RejoinResultTs: 300})
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("an outcome newer than the working crash is this event's own result: must block")
	}
}

// finishCrashRecord ends the given record by pointer, leaving another working
// crash of the same URL untouched, and stamps the result.
func TestFinishCrashRecord_ByPointer(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}}
	stale := &Crash{URL: "db1:3306", UnixTimestamp: 100}
	sw := &Crash{URL: "db1:3306", UnixTimestamp: 200, Switchover: true}
	c.Crashes = crashList{stale, sw}
	got := c.finishCrashRecord(sw, RejoinResultNoDivergence)
	if got != sw || sw.RejoinResult != RejoinResultNoDivergence || sw.RejoinResultTs == 0 {
		t.Fatalf("switchover record must be the one finished: %+v", sw)
	}
	if len(c.Crashes) != 1 || c.Crashes[0] != stale {
		t.Fatalf("the other working crash must stay in the working set: %+v", c.Crashes)
	}
}
