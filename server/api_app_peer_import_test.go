package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestBuildPeerAppInventory_UsesPersistedHostNotRuntimeHost is the
// ProvNetCNI mismatch case: NewApp() rewrites app.Host (app.GetHost()) with
// a cluster-svc suffix for runtime routing while app.AppConfig.AppHost stays
// pinned to the original persisted host. The inventory must report the
// persisted identity as Host/Port (the only fields used as an import
// selection key) and the runtime host only as the separate, display-only
// RuntimeHost field. It must also omit apps with no usable persisted
// identity (fail closed) rather than exporting them as importable.
func TestBuildPeerAppInventory_UsesPersistedHostNotRuntimeHost(t *testing.T) {
	mycluster := &cluster.Cluster{
		Name: "cl1",
		Apps: []*cluster.App{
			{
				Name:  "peerapp1",
				Host:  "peerapp1.cl1.svc.k8s", // simulates the ProvNetCNI runtime rewrite
				Port:  "8080",
				Mutex: &sync.Mutex{},
				AppConfig: &config.AppConfig{
					AppHost: "peerapp1",
					AppPort: "8080",
				},
			},
			{
				// Empty persisted host: must be omitted, not offered for import.
				Name:      "badapp",
				Host:      "badapp",
				Port:      "9090",
				Mutex:     &sync.Mutex{},
				AppConfig: &config.AppConfig{AppHost: "", AppPort: "9090"},
			},
			{
				// No AppConfig at all: must be omitted.
				Name:  "nilconfigapp",
				Host:  "nilconfigapp",
				Port:  "1234",
				Mutex: &sync.Mutex{},
			},
		},
	}

	inv := buildPeerAppInventory(mycluster)

	if len(inv.Apps) != 1 {
		t.Fatalf("expected exactly 1 app in inventory (2 with missing persisted identity omitted), got %d: %+v", len(inv.Apps), inv.Apps)
	}
	item := inv.Apps[0]
	if item.Host != "peerapp1" || item.Port != "8080" {
		t.Fatalf("expected inventory Host/Port to be the persisted identity, got %+v", item)
	}
	if item.RuntimeHost != "peerapp1.cl1.svc.k8s" {
		t.Fatalf("expected RuntimeHost to carry the runtime (CNI-rewritten) host for display, got %q", item.RuntimeHost)
	}
}

func newTestRepmanForPeerImport(domain, sub, zone string) *ReplicationManager {
	return &ReplicationManager{
		Conf: &config.Config{
			Cloud18Domain:        domain,
			Cloud18SubDomain:     sub,
			Cloud18SubDomainZone: zone,
		},
	}
}

func TestSamePeerCluster(t *testing.T) {
	repman := newTestRepmanForPeerImport("example", "sub", "zone")
	localURI := repman.registeredInstanceURI()
	if localURI == "" {
		t.Fatalf("expected local URI to be non-empty")
	}

	tests := []struct {
		name    string
		local   string
		peer    PeerAppInventory
		wantErr bool
	}{
		{
			name:  "matching cluster name and uri",
			local: "cluster1",
			peer:  PeerAppInventory{ClusterName: "cluster1", URI: localURI},
		},
		{
			name:    "cluster name mismatch",
			local:   "cluster1",
			peer:    PeerAppInventory{ClusterName: "cluster2", URI: localURI},
			wantErr: true,
		},
		{
			name:    "uri mismatch",
			local:   "cluster1",
			peer:    PeerAppInventory{ClusterName: "cluster1", URI: "other.sub.zone"},
			wantErr: true,
		},
		{
			name:    "empty peer uri never matches",
			local:   "cluster1",
			peer:    PeerAppInventory{ClusterName: "cluster1", URI: ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repman.samePeerCluster(tt.local, tt.peer)
			if (err != nil) != tt.wantErr {
				t.Fatalf("samePeerCluster() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSamePeerCluster_LocalURIIncomplete(t *testing.T) {
	// Local instance not fully registered (missing Cloud18 fields): must fail
	// closed even if the peer happens to report an empty URI too.
	repman := newTestRepmanForPeerImport("", "", "")
	if got := repman.registeredInstanceURI(); got != "" {
		t.Fatalf("expected empty local URI, got %q", got)
	}

	err := repman.samePeerCluster("cluster1", PeerAppInventory{ClusterName: "cluster1", URI: ""})
	if err == nil {
		t.Fatalf("expected error when local URI is incomplete, even with matching (empty) peer URI")
	}
}

// newTestRepmanWithPeer wires a local ReplicationManager (one cluster,
// with a create-monitor-grant user so peerAppLogin can authenticate)
// pointed at peerHandler as its arbitration peer (ArbitrationPeerHosts).
func newTestRepmanWithPeer(t *testing.T, clusterName string, peerHandler http.Handler) (*ReplicationManager, *cluster.Cluster) {
	t.Helper()
	ts := httptest.NewServer(peerHandler)
	t.Cleanup(ts.Close)

	mycluster := &cluster.Cluster{
		Name: clusterName,
		Conf: &config.Config{},
		APIUsers: map[string]cluster.APIUser{
			"tester": {
				User:     "tester",
				Password: "testpass",
				Grants:   map[string]bool{config.GrantClusterCreateMonitor: true},
			},
		},
	}

	repman := &ReplicationManager{
		Conf: &config.Config{
			Cloud18Domain:        "example",
			Cloud18SubDomain:     "sub",
			Cloud18SubDomainZone: "zone",
			ArbitrationPeerHosts: ts.URL,
		},
		Clusters: map[string]*cluster.Cluster{clusterName: mycluster},
	}
	return repman, mycluster
}

// peerImportTestHandler builds a minimal fake-peer mux: /api/login always
// succeeds, and the inventory endpoint returns inv regardless of clusterName
// in the path (single-cluster test fixture).
func peerImportTestHandler(t *testing.T, inv PeerAppInventory) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(AuthTokenWithUpgrade{Token: "faketoken"})
	})
	mux.HandleFunc("/api/clusters/", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= len("/apps/peer-import/inventory") &&
			r.URL.Path[len(r.URL.Path)-len("/apps/peer-import/inventory"):] == "/apps/peer-import/inventory" {
			json.NewEncoder(w).Encode(inv)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

// TestAppImportApply_RejectsAmbiguousPeerHostEvenIfNotSelectedTogether covers
// the gap flagged in review: apply must reject a selection when the PEER's
// own inventory has multiple ports on that host, even if only one of those
// ports was selected (i.e. a caller that skips preview and calls apply
// directly for a single app must get the same rejection preview would show).
func TestAppImportApply_RejectsAmbiguousPeerHostEvenIfNotSelectedTogether(t *testing.T) {
	repman, mycluster := newTestRepmanWithPeer(t, "cl1", peerImportTestHandler(t, PeerAppInventory{
		ClusterName: "cl1",
		URI:         "example.sub.zone",
		Apps: []PeerAppInventoryItem{
			{Host: "hostX", Port: "8080"},
			{Host: "hostX", Port: "9090"},
		},
	}))

	// Only one of the two ambiguous peer ports is selected.
	result, err := repman.appImportApply(mycluster, []appImportSelector{{Host: "hostX", Port: "8080"}})
	if err != nil {
		t.Fatalf("appImportApply returned unexpected top-level error: %v", err)
	}
	if len(result.Apps) != 1 {
		t.Fatalf("expected 1 result item, got %d", len(result.Apps))
	}
	if result.Apps[0].Status != "rejected" {
		t.Fatalf("expected single-port selection from an ambiguous peer host to be rejected, got status=%q reason=%q",
			result.Apps[0].Status, result.Apps[0].Reason)
	}
	// The fake peer handler doesn't implement /export, so any rejection would
	// otherwise report "peer export failed" instead — assert the specific
	// ambiguous-host reason to make sure this actually exercises the
	// peer-inventory ambiguity guard and not some other rejection path.
	const wantReason = "peer has multiple ports on this host; current storage layout is host-based"
	if result.Apps[0].Reason != wantReason {
		t.Fatalf("expected rejection reason %q, got %q", wantReason, result.Apps[0].Reason)
	}
}

// TestAppImportApply_RejectsUnknownPeerApp covers the simpler existence
// guard: a selection absent from the peer's inventory must be rejected, not
// silently skipped or exported.
func TestAppImportApply_RejectsUnknownPeerApp(t *testing.T) {
	repman, mycluster := newTestRepmanWithPeer(t, "cl1", peerImportTestHandler(t, PeerAppInventory{
		ClusterName: "cl1",
		URI:         "example.sub.zone",
		Apps:        []PeerAppInventoryItem{{Host: "hostY", Port: "8080"}},
	}))

	result, err := repman.appImportApply(mycluster, []appImportSelector{{Host: "hostZ", Port: "1234"}})
	if err != nil {
		t.Fatalf("appImportApply returned unexpected top-level error: %v", err)
	}
	if len(result.Apps) != 1 || result.Apps[0].Status != "rejected" {
		t.Fatalf("expected selection absent from peer inventory to be rejected, got %+v", result.Apps)
	}
}
