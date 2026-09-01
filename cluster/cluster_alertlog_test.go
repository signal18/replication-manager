package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// TestShouldForwardAlertLog_ThrottlesIdenticalLines guards the anti-flood
// contract for the alert channels: a raw ERROR/WARN log line repeated by a
// hot loop is forwarded at most once per alert-log-throttle interval, the
// suppression is counted (visible state), distinct call sites are throttled
// independently, and 0 disables throttling entirely.
func TestShouldForwardAlertLog_ThrottlesIdenticalLines(t *testing.T) {
	c := new(Cluster)
	c.Conf = &config.Config{AlertLogThrottle: 600}

	if !c.shouldForwardAlertLog("ERROR", "Cannot load OpenSVC cluster certificate %s ") {
		t.Fatal("first occurrence must be forwarded")
	}
	if c.shouldForwardAlertLog("ERROR", "Cannot load OpenSVC cluster certificate %s ") {
		t.Fatal("identical line within the interval must be suppressed")
	}
	if c.AlertLogThrottled != 1 {
		t.Fatalf("throttled sends must be counted, got %d", c.AlertLogThrottled)
	}
	if !c.shouldForwardAlertLog("WARN", "Cannot load OpenSVC cluster certificate %s ") {
		t.Fatal("same format at a different level is a distinct key")
	}
	if !c.shouldForwardAlertLog("ERROR", "some other failure %s") {
		t.Fatal("a different call site must not be throttled by the first one")
	}

	// Interval expiry re-opens the gate.
	c.alertLogLastSent["ERROR|Cannot load OpenSVC cluster certificate %s "] = time.Now().Add(-601 * time.Second)
	if !c.shouldForwardAlertLog("ERROR", "Cannot load OpenSVC cluster certificate %s ") {
		t.Fatal("after the interval the line must be forwarded again")
	}

	// 0 disables throttling (T14 off-switch).
	c.Conf.AlertLogThrottle = 0
	if !c.shouldForwardAlertLog("ERROR", "Cannot load OpenSVC cluster certificate %s ") ||
		!c.shouldForwardAlertLog("ERROR", "Cannot load OpenSVC cluster certificate %s ") {
		t.Fatal("throttle 0 must forward everything")
	}
}
