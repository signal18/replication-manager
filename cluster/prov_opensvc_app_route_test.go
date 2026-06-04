package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestBuildRouteFragment_HostRouteUsesOriginDevelopNames(t *testing.T) {
	route := config.Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "console.example.com",
		DestinationPort: "9001",
	}

	key, fragment, err := buildRouteFragment(route, "minio.crm.svc.cluster.local", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "haproxy.cfg.d/minio.crm.svc.cluster.local_9001"
	if key != want {
		t.Fatalf("unexpected fragment key: got %q want %q", key, want)
	}
	if !strings.Contains(fragment, "use_backend minio.crm.svc.cluster.local_9001 if { hdr(host) -i console.example.com }") {
		t.Fatalf("host fragment lost origin/develop backend naming: %s", fragment)
	}
	if !strings.Contains(fragment, "backend minio.crm.svc.cluster.local_9001") {
		t.Fatalf("host backend name mismatch: %s", fragment)
	}
	if strings.Contains(fragment, "be_") || strings.Contains(fragment, "repman_") {
		t.Fatalf("host fragment still contains tokenized naming: %s", fragment)
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
