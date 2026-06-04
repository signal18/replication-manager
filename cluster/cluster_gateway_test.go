package cluster

import (
	"errors"
	"hash/crc64"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestClusterForGateway(t *testing.T, name, gatewayService, gatewayBindAddrs string) *Cluster {
	t.Helper()
	return &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir:                  t.TempDir(),
			Cloud18GatewayService:       gatewayService,
			Cloud18GatewayBindAddresses: gatewayBindAddrs,
		},
		crcTable: crc64.MakeTable(crc64.ISO),
	}
}

func portRouteApp(host, cname, sourcePort, destPort string) *config.AppConfig {
	return &config.AppConfig{
		AppHost: host,
		AppPort: "80",
		Deployment: &config.Deployment{
			Routes: config.Routes{
				{
					Mode:            "port",
					Protocol:        "http",
					CName:           cname,
					SourcePort:      sourcePort,
					DestinationPort: destPort,
					Primary:         true,
				},
			},
		},
	}
}

func hostRouteApp(host, cname, destPort string) *config.AppConfig {
	return &config.AppConfig{
		AppHost: host,
		AppPort: "80",
		Deployment: &config.Deployment{
			Routes: config.Routes{
				{
					Mode:            "host",
					Protocol:        "https",
					CName:           cname,
					DestinationPort: destPort,
					Primary:         true,
				},
			},
		},
	}
}

func staticResolver(mapping map[string]string) config.PortRouteBindResolver {
	return func(cname string) (string, error) {
		if addr, ok := mapping[cname]; ok {
			return addr, nil
		}
		return "", errors.New("cname not in gateway bind addresses")
	}
}

// ---------------------------------------------------------------------------
// validateIntraClusterGatewayRoutesWithResolver
// ---------------------------------------------------------------------------

func TestValidateIntraCluster_NoConflict(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "10.10.10.25")
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("minio", "s3.gw.example.com", "9000", "9000"),
		portRouteApp("console", "console.gw.example.com", "9001", "9001"),
	}

	resolver := staticResolver(map[string]string{
		"s3.gw.example.com":      "10.10.10.25",
		"console.gw.example.com": "10.10.10.26", // different VIP → no conflict
	})

	if err := cl.validateIntraClusterGatewayRoutesWithResolver(resolver); err != nil {
		t.Fatalf("expected no conflict, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected 2 apps to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_ConflictEjectsLaterApp(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "10.10.10.25")
	appA := portRouteApp("app-a", "a.gw.example.com", "9000", "9000")
	appB := portRouteApp("app-b", "b.gw.example.com", "9000", "9001") // same resolved VIP:port
	cl.Conf.Apps = []*config.AppConfig{appA, appB}

	// Both CNAMEs resolve to the same VIP — same source port → conflict.
	resolver := staticResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		"b.gw.example.com": "10.10.10.25",
	})

	err := cl.validateIntraClusterGatewayRoutesWithResolver(resolver)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}

	// app-a (first-loaded) must survive; app-b must be ejected.
	if len(cl.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app to remain after eject, got %d", len(cl.Conf.Apps))
	}
	if cl.Conf.Apps[0].AppHost != "app-a" {
		t.Errorf("expected app-a to remain, got %q", cl.Conf.Apps[0].AppHost)
	}
}

func TestValidateIntraCluster_HostRouteDuplicateCName(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "")
	cl.Conf.Apps = []*config.AppConfig{
		hostRouteApp("svc-a", "shared.example.com", "8080"),
		hostRouteApp("svc-b", "shared.example.com", "9090"),
	}

	// Host routes don't use the resolver; nil is safe.
	err := cl.validateIntraClusterGatewayRoutesWithResolver(nil)
	if err == nil {
		t.Fatal("expected conflict error for duplicate host cname, got nil")
	}
	if len(cl.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_ResolverErrorEjectsApp(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "10.10.10.25")
	appA := portRouteApp("app-a", "a.gw.example.com", "9000", "9000")
	appB := portRouteApp("app-b", "broken.gw.example.com", "9001", "9001")
	cl.Conf.Apps = []*config.AppConfig{appA, appB}

	resolver := staticResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		// broken.gw.example.com intentionally absent → resolver error
	})

	err := cl.validateIntraClusterGatewayRoutesWithResolver(resolver)
	if err == nil {
		t.Fatal("expected error when resolver fails for app-b, got nil")
	}
	// app-a should survive; app-b should be ejected because its cname can't resolve.
	if len(cl.Conf.Apps) != 1 || cl.Conf.Apps[0].AppHost != "app-a" {
		t.Fatalf("expected only app-a to remain, got %v", cl.Conf.Apps)
	}
}

func TestValidateIntraCluster_NoGatewayConfigured(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "", "") // no gateway service
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
		portRouteApp("app-b", "a.gw.example.com", "9000", "9001"), // would conflict if gateway were set
	}

	if err := cl.validateIntraClusterGatewayRoutesWithResolver(nil); err != nil {
		t.Fatalf("expected no validation when gateway not configured, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected both apps to remain when gateway not configured, got %d", len(cl.Conf.Apps))
	}
}

// ---------------------------------------------------------------------------
// validateGatewayRouteConflictsWithResolver (cross-cluster)
// ---------------------------------------------------------------------------

func TestValidateCrossCluster_ConflictEjectsApp(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	// Cluster 1 owns app-a on 10.10.10.25:9000.
	cl1 := newTestClusterForGateway(t, "cluster1", gw, "10.10.10.25")
	appACnf := portRouteApp("app-a", "a.gw.example.com", "9000", "9000")
	cl1.Conf.Apps = []*config.AppConfig{appACnf}

	// Cluster 2 tries to also bind 10.10.10.25:9000 — conflict.
	cl2 := newTestClusterForGateway(t, "cluster2", gw, "10.10.10.25")
	appBCnf := portRouteApp("app-b", "b.gw.example.com", "9000", "9001")
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	// Pre-populate the live app list using the same AppConfig pointers so the
	// pruneEjectedAppsFromLiveList path can be exercised.
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}
	cl2.clusterList = map[string]*Cluster{"cluster1": cl1}

	resolver := staticResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		"b.gw.example.com": "10.10.10.25",
	})

	err := cl2.validateGatewayRouteConflictsWithResolver(resolver)
	if err == nil {
		t.Fatal("expected cross-cluster conflict error, got nil")
	}
	// app-b must be ejected from the config slice.
	if len(cl2.Conf.Apps) != 0 {
		t.Fatalf("expected app-b to be ejected from cluster2 Conf.Apps, got %d", len(cl2.Conf.Apps))
	}
	// app-b must also be ejected from the live runtime app list.
	if len(cl2.Apps) != 0 {
		t.Fatalf("expected app-b to be ejected from cluster2 Apps, got %d", len(cl2.Apps))
	}
	// app-a in cluster1 must be untouched.
	if len(cl1.Conf.Apps) != 1 {
		t.Fatalf("expected app-a in cluster1 to be untouched, got %d apps", len(cl1.Conf.Apps))
	}
}

func TestValidateCrossCluster_NoConflict_LiveListUnchanged(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	cl1 := newTestClusterForGateway(t, "cluster1", gw, "10.10.10.25")
	cl1.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
	}

	cl2 := newTestClusterForGateway(t, "cluster2", gw, "10.10.10.25")
	appBCnf := portRouteApp("app-b", "b.gw.example.com", "9001", "9001") // different port — no conflict
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}
	cl2.clusterList = map[string]*Cluster{"cluster1": cl1}

	resolver := staticResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		"b.gw.example.com": "10.10.10.25",
	})

	if err := cl2.validateGatewayRouteConflictsWithResolver(resolver); err != nil {
		t.Fatalf("expected no conflict, got: %v", err)
	}
	// Both Conf.Apps and live Apps must be intact.
	if len(cl2.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app in Conf.Apps, got %d", len(cl2.Conf.Apps))
	}
	if len(cl2.Apps) != 1 {
		t.Fatalf("expected 1 app in live Apps list, got %d", len(cl2.Apps))
	}
}

func TestValidateCrossCluster_NoPeerOnSameGateway(t *testing.T) {
	cl1 := newTestClusterForGateway(t, "cluster1", "ns/svc/gw-a", "10.10.10.25")
	cl1.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
	}

	cl2 := newTestClusterForGateway(t, "cluster2", "ns/svc/gw-b", "10.10.10.25") // different gateway
	cl2.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-b", "a.gw.example.com", "9000", "9001"), // same cname+port but different gateway
	}
	cl1.clusterList = map[string]*Cluster{"cluster2": cl2}

	resolver := staticResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
	})

	// No peers on the same gateway — no conflict possible.
	if err := cl1.validateGatewayRouteConflictsWithResolver(resolver); err != nil {
		t.Fatalf("expected no conflict across different gateways, got: %v", err)
	}
	if len(cl1.Conf.Apps) != 1 {
		t.Fatalf("expected app-a to remain, got %d apps", len(cl1.Conf.Apps))
	}
}

// ---------------------------------------------------------------------------
// pruneEjectedAppsFromLiveList — runtime index consistency
// ---------------------------------------------------------------------------

func TestPruneEjected_UpdatesS3ProvidersAndEpoch(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "10.10.10.25")

	keepCnf := portRouteApp("keep", "keep.gw.example.com", "9000", "9000")
	ejectCnf := portRouteApp("eject", "eject.gw.example.com", "9001", "9001")
	ejectCnf.AppS3Provider = true // mark as S3 provider so its removal is tracked

	// Start with both apps in the live list.
	cl.Conf.Apps = []*config.AppConfig{keepCnf} // "eject" already removed from conf
	cl.Apps = []*App{
		{Name: "keep", Host: "keep", Port: "80", AppConfig: keepCnf, Mutex: &sync.Mutex{}},
		{Name: "eject", Host: "eject", Port: "80", AppConfig: ejectCnf, Mutex: &sync.Mutex{}},
	}
	cl.AppS3Providers = []string{"eject:80"}

	epochBefore := cl.appListVersion()

	cl.pruneEjectedAppsFromLiveList()

	// Live app list must be pruned.
	if len(cl.Apps) != 1 || cl.Apps[0].Name != "keep" {
		t.Fatalf("expected only 'keep' in live Apps, got %v", cl.Apps)
	}
	// S3 provider list must no longer contain the ejected app.
	if len(cl.AppS3Providers) != 0 {
		t.Fatalf("expected empty AppS3Providers after eject, got %v", cl.AppS3Providers)
	}
	// Epoch must have been bumped so snapshot consumers see the change.
	if cl.appListVersion() <= epochBefore {
		t.Fatalf("expected appListEpoch to increase after prune, before=%d after=%d",
			epochBefore, cl.appListVersion())
	}
}

func TestPruneEjected_S3ProviderSurvivedWhenKept(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw", "10.10.10.25")

	keepCnf := portRouteApp("keep", "keep.gw.example.com", "9000", "9000")
	keepCnf.AppS3Provider = true

	cl.Conf.Apps = []*config.AppConfig{keepCnf}
	cl.Apps = []*App{
		{Name: "keep", Host: "keep", Port: "80", AppConfig: keepCnf, Mutex: &sync.Mutex{}},
	}
	cl.AppS3Providers = []string{"keep:80"}

	cl.pruneEjectedAppsFromLiveList()

	if len(cl.AppS3Providers) != 1 || cl.AppS3Providers[0] != "keep:80" {
		t.Fatalf("expected S3 provider 'keep:80' to survive, got %v", cl.AppS3Providers)
	}
}

// ---------------------------------------------------------------------------
// uniqueBindMatches — deduplication of DNS answers
// ---------------------------------------------------------------------------

func TestUniqueBindMatches_DeduplicatesSameIP(t *testing.T) {
	bindSet := map[string]bool{"10.10.10.25": true}
	// Simulate DNS returning the same IP twice.
	ips := []string{"10.10.10.25", "10.10.10.25"}
	matches := uniqueBindMatches(ips, bindSet)
	if len(matches) != 1 {
		t.Fatalf("expected 1 unique match, got %d: %v", len(matches), matches)
	}
}

func TestUniqueBindMatches_NonBindIPsIgnored(t *testing.T) {
	bindSet := map[string]bool{"10.10.10.25": true}
	ips := []string{"1.2.3.4", "10.10.10.25", "1.2.3.4"}
	matches := uniqueBindMatches(ips, bindSet)
	if len(matches) != 1 || matches[0] != "10.10.10.25" {
		t.Fatalf("expected exactly [10.10.10.25], got %v", matches)
	}
}

func TestUniqueBindMatches_NoMatchReturnsEmpty(t *testing.T) {
	bindSet := map[string]bool{"10.10.10.25": true}
	ips := []string{"1.2.3.4", "5.6.7.8"}
	matches := uniqueBindMatches(ips, bindSet)
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %v", matches)
	}
}

func TestUniqueBindMatches_MultipleDistinctMatchesPreserved(t *testing.T) {
	bindSet := map[string]bool{"10.10.10.25": true, "10.10.10.26": true}
	ips := []string{"10.10.10.25", "10.10.10.26"}
	matches := uniqueBindMatches(ips, bindSet)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}
