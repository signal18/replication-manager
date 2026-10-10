package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A looser verdict moves a cluster only after arbitration-verdict-streak consecutive ones;
// a winner resets the count; streak 1 (or unset) acts on every answer as before (#1929).
func TestArbitrationVerdictStreak(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{ArbitrationVerdictStreak: 3}}
	if cl.arbitrationLossAccepted() || cl.arbitrationLossAccepted() {
		t.Fatal("two losses must not move the cluster with a streak of 3")
	}
	if !cl.arbitrationLossAccepted() {
		t.Fatal("the third consecutive loss moves the cluster")
	}
	if !cl.arbitrationLossAccepted() {
		t.Fatal("a fourth loss keeps the verdict accepted")
	}
	cl.arbLoserStreak = 0 // what a winner does
	if cl.arbitrationLossAccepted() {
		t.Fatal("after a winner the count starts again")
	}
	if cl.arbitrationUnreachableAccepted() || cl.arbitrationUnreachableAccepted() {
		t.Fatal("two unreachable answers must not fire the fail-safe with a streak of 3")
	}
	if !cl.arbitrationUnreachableAccepted() {
		t.Fatal("the third unreachable answer fires the fail-safe")
	}
	one := &Cluster{Conf: &config.Config{}}
	if !one.arbitrationLossAccepted() {
		t.Fatal("unset streak acts on every answer")
	}
}

// The gateway's health check follows the route monitor: a path other than the root with
// its expected status, nothing without a monitor or on the root (#1929).
func TestRouteHealthCheckLines(t *testing.T) {
	if got := routeHealthCheckLines(config.Route{}); got != "" {
		t.Fatalf("no monitor: no check, got %q", got)
	}
	if got := routeHealthCheckLines(config.Route{Monitor: &config.RouteMonitor{Path: "/"}}); got != "" {
		t.Fatalf("root path: no check, got %q", got)
	}
	got := routeHealthCheckLines(config.Route{Monitor: &config.RouteMonitor{Path: "/health"}})
	if !strings.Contains(got, "option httpchk GET /health") || !strings.Contains(got, "http-check expect status 200") {
		t.Fatalf("health path with the default status: %q", got)
	}
	got = routeHealthCheckLines(config.Route{Monitor: &config.RouteMonitor{Path: "/api/method/ping", ExpectStatus: "204"}})
	if !strings.Contains(got, "GET /api/method/ping") || !strings.Contains(got, "expect status 204") {
		t.Fatalf("explicit status: %q", got)
	}
	for _, bad := range []config.RouteMonitor{{Path: "/health\n    option foo"}, {Path: "/he alth"}, {Path: "/health", ExpectStatus: "200 or 1"}, {Path: "/health", ExpectStatus: "ok"}} {
		if got := routeHealthCheckLines(config.Route{Monitor: &bad}); got != "" {
			t.Fatalf("a path or status that could carry a directive never reaches the config: %+v -> %q", bad, got)
		}
	}
	_, frag, err := buildGroupedHostRouteFragment([]config.Route{{Mode: "host", Protocol: "https", CName: "arb.example", DestinationPort: "10001", Monitor: &config.RouteMonitor{Path: "/health"}}}, "arbitrator.crm.svc.cloud18", 3)
	if err != nil || !strings.Contains(frag, "option httpchk GET /health\n") || !strings.Contains(frag, "server-template srv 3") {
		t.Fatalf("host fragment carries the check before the servers: %v %q", err, frag)
	}
}
