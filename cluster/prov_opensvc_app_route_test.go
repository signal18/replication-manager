package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestBuildRouteFragment_HostRouteReturnsErrorNotSingleRoute(t *testing.T) {
	route := config.Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "console.example.com",
		DestinationPort: "9001",
	}
	_, _, err := buildRouteFragment(route, "minio.crm.svc.cluster.local", 2)
	if err == nil {
		t.Fatal("expected error for host-mode route, got nil")
	}
}

func TestBuildRouteFragment_PortRouteUsesReadableNames(t *testing.T) {
	route := config.Route{
		Mode:            "port",
		Protocol:        "http",
		CName:           "api.example.com",
		SourcePort:      "9000",
		DestinationPort: "9001",
	}

	key, fragment, err := buildRouteFragment(route, "minio.crm.svc.cluster.local", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	base := "minio.crm.svc.cluster.local_api.example.com_9000_9001"
	if key != "haproxy.cfg.d/"+base {
		t.Fatalf("unexpected fragment key: got %q want %q", key, "haproxy.cfg.d/"+base)
	}
	if !strings.Contains(fragment, "frontend "+base+"_frontend") {
		t.Fatalf("port frontend name mismatch: %s", fragment)
	}
	if !strings.Contains(fragment, "default_backend "+base+"_backend") {
		t.Fatalf("port default backend name mismatch: %s", fragment)
	}
	if !strings.Contains(fragment, "backend "+base+"_backend") {
		t.Fatalf("port backend name mismatch: %s", fragment)
	}
	if !strings.Contains(fragment, "timeout tunnel 1h") {
		t.Fatalf("port fragment missing websocket timeout tunnel: %s", fragment)
	}
	if strings.Contains(fragment, "be_") || strings.Contains(fragment, "fe_") || strings.Contains(fragment, "repman_") {
		t.Fatalf("port fragment still contains tokenized naming: %s", fragment)
	}
}

func TestBuildRouteFragment_PortRoutesDifferentCNamesHaveDifferentKeys(t *testing.T) {
	routeA := config.Route{
		Mode:            "port",
		Protocol:        "tcp",
		CName:           "api-a.example.com",
		SourcePort:      "9000",
		DestinationPort: "9000",
	}
	routeB := routeA
	routeB.CName = "api-b.example.com"

	keyA, _, err := buildRouteFragment(routeA, "minio.crm.svc.cluster.local", 1)
	if err != nil {
		t.Fatalf("unexpected error for routeA: %v", err)
	}
	keyB, _, err := buildRouteFragment(routeB, "minio.crm.svc.cluster.local", 1)
	if err != nil {
		t.Fatalf("unexpected error for routeB: %v", err)
	}

	if keyA == keyB {
		t.Fatalf("expected distinct fragment keys for distinct port-route cnames, both got %q", keyA)
	}
}

func TestBuildRouteFragment_HostRouteReturnsError(t *testing.T) {
	route := config.Route{
		Mode:            "host",
		CName:           "app.example.com",
		DestinationPort: "8080",
	}
	_, _, err := buildRouteFragment(route, "app.cluster.svc.local", 2)
	if err == nil {
		t.Fatal("expected error for host-mode route, got nil")
	}
}

func TestNormalizeRouteCNAME(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"app.example.com", "app.example.com"},
		{"App.Example.COM.", "app.example.com"},
		{"  WWW.EXAMPLE.COM.  ", "www.example.com"},
		{"UPPER.", "upper"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		got := normalizeRouteCNAME(tt.input)
		if got != tt.want {
			t.Errorf("normalizeRouteCNAME(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildGroupedHostRouteFragment_SingleRoute(t *testing.T) {
	routes := []config.Route{
		{Mode: "host", CName: "app.example.com", DestinationPort: "8080"},
	}
	key, frag, err := buildGroupedHostRouteFragment(routes, "app.cluster.svc.local", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "haproxy.cfg.d/app.cluster.svc.local_8080" {
		t.Fatalf("unexpected fragment key: got %q", key)
	}
	if !strings.Contains(frag, "use_backend app.cluster.svc.local_8080 if { hdr(host) -i app.example.com }") {
		t.Fatalf("missing use_backend rule in fragment:\n%s", frag)
	}
	if !strings.Contains(frag, "backend app.cluster.svc.local_8080") {
		t.Fatalf("missing backend section in fragment:\n%s", frag)
	}
	if !strings.Contains(frag, "dynamic-cookie-key mysecretphrase") {
		t.Fatalf("missing dynamic-cookie-key in fragment:\n%s", frag)
	}
	if !strings.Contains(frag, "timeout tunnel 1h") {
		t.Fatalf("missing timeout tunnel in fragment:\n%s", frag)
	}
}

func TestBuildGroupedHostRouteFragment_MultipleHostsSamePort(t *testing.T) {
	routes := []config.Route{
		{Mode: "host", CName: "app.example.com", DestinationPort: "8080"},
		{Mode: "host", CName: "www.example.com", DestinationPort: "8080"},
	}
	key, frag, err := buildGroupedHostRouteFragment(routes, "app.cluster.svc.local", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "haproxy.cfg.d/app.cluster.svc.local_8080" {
		t.Fatalf("unexpected fragment key: got %q", key)
	}
	if !strings.Contains(frag, "use_backend app.cluster.svc.local_8080 if { hdr(host) -i app.example.com }") {
		t.Fatalf("missing first use_backend rule:\n%s", frag)
	}
	if !strings.Contains(frag, "use_backend app.cluster.svc.local_8080 if { hdr(host) -i www.example.com }") {
		t.Fatalf("missing second use_backend rule:\n%s", frag)
	}
	// Exactly one backend section (not counting use_backend lines).
	backendSections := 0
	for _, line := range strings.Split(frag, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "backend ") {
			backendSections++
		}
	}
	if backendSections != 1 {
		t.Fatalf("expected 1 backend section, found %d in:\n%s", backendSections, frag)
	}
}

func TestBuildGroupedHostRouteFragment_NormalizesHostnames(t *testing.T) {
	routes := []config.Route{
		{Mode: "host", CName: "APP.EXAMPLE.COM.", DestinationPort: "9000"},
		{Mode: "host", CName: "WWW.EXAMPLE.COM. ", DestinationPort: "9000"},
	}
	_, frag, err := buildGroupedHostRouteFragment(routes, "svc.ns.svc.local", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(frag, "{ hdr(host) -i app.example.com }") {
		t.Fatalf("first CNAME not normalized to lowercase:\n%s", frag)
	}
	if !strings.Contains(frag, "{ hdr(host) -i www.example.com }") {
		t.Fatalf("second CNAME not normalized to lowercase:\n%s", frag)
	}
	if strings.Contains(frag, "APP.EXAMPLE.COM") || strings.Contains(frag, "WWW.EXAMPLE.COM") {
		t.Fatalf("mixed-case hostname leaked into fragment:\n%s", frag)
	}
}

func TestBuildGroupedHostRouteFragment_EmptyRoutes_Errors(t *testing.T) {
	_, _, err := buildGroupedHostRouteFragment(nil, "svc.ns.svc.local", 1)
	if err == nil {
		t.Fatal("expected error for empty routes slice")
	}
}
