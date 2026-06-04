package cluster

import (
	"hash/crc64"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestClusterForGateway(t *testing.T, name, gatewayService string) *Cluster {
	t.Helper()
	return &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir:            t.TempDir(),
			Cloud18GatewayService: gatewayService,
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

// ---------------------------------------------------------------------------
// validateIntraClusterGatewayRoutes
// ---------------------------------------------------------------------------

func TestValidateIntraCluster_NoConflict_DifferentCNameDifferentPort(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("minio", "s3.gw.example.com", "9000", "9000"),
		portRouteApp("console", "console.gw.example.com", "9001", "9001"),
	}

	if err := cl.validateIntraClusterGatewayRoutes(); err != nil {
		t.Fatalf("expected no conflict, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected 2 apps to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_NoConflict_DifferentCNameSamePort(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
		portRouteApp("app-b", "b.gw.example.com", "9000", "9001"),
	}

	// Different CNAMEs on same sourcePort are not a conflict (key = cname:sourcePort).
	if err := cl.validateIntraClusterGatewayRoutes(); err != nil {
		t.Fatalf("expected no conflict for different cnames same sourcePort, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected 2 apps to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_ConflictEjectsLaterApp(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	appA := portRouteApp("app-a", "gw.example.com", "9000", "9000")
	appB := portRouteApp("app-b", "gw.example.com", "9000", "9001") // same cname + sourcePort
	cl.Conf.Apps = []*config.AppConfig{appA, appB}

	err := cl.validateIntraClusterGatewayRoutes()
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
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		hostRouteApp("svc-a", "shared.example.com", "8080"),
		hostRouteApp("svc-b", "shared.example.com", "9090"),
	}

	err := cl.validateIntraClusterGatewayRoutes()
	if err == nil {
		t.Fatal("expected conflict error for duplicate host cname, got nil")
	}
	if len(cl.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_HostAndPortSameCName_NoConflict(t *testing.T) {
	// A host route and a port route may share the same CNAME.
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		hostRouteApp("minio-host", "minio.example.com", "9001"),
		portRouteApp("minio-port", "minio.example.com", "9000", "9000"),
	}

	if err := cl.validateIntraClusterGatewayRoutes(); err != nil {
		t.Fatalf("expected no conflict for host+port routes sharing a cname, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected 2 apps to remain, got %d", len(cl.Conf.Apps))
	}
}

func TestValidateIntraCluster_NoGatewayConfigured(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "") // no gateway service
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "gw.example.com", "9000", "9000"),
		portRouteApp("app-b", "gw.example.com", "9000", "9001"), // would conflict if gateway were set
	}

	if err := cl.validateIntraClusterGatewayRoutes(); err != nil {
		t.Fatalf("expected no validation when gateway not configured, got: %v", err)
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("expected both apps to remain when gateway not configured, got %d", len(cl.Conf.Apps))
	}
}

// ---------------------------------------------------------------------------
// ValidateGatewayRouteConflicts (cross-cluster)
// ---------------------------------------------------------------------------

func TestValidateCrossCluster_ConflictEjectsApp(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	cl1 := newTestClusterForGateway(t, "cluster1", gw)
	appACnf := portRouteApp("app-a", "gw.example.com", "9000", "9000")
	cl1.Conf.Apps = []*config.AppConfig{appACnf}

	// cluster2 claims the same cname:sourcePort — conflict.
	cl2 := newTestClusterForGateway(t, "cluster2", gw)
	appBCnf := portRouteApp("app-b", "gw.example.com", "9000", "9001")
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	// Simulate server startup loop: cl1 was processed first, so pass its routes as prior.
	priorRoutes := cl1.OwnGatewayRoutes(gw)
	err := cl2.ValidateGatewayRouteConflicts(priorRoutes)
	if err == nil {
		t.Fatal("expected cross-cluster conflict error, got nil")
	}
	if len(cl2.Conf.Apps) != 0 {
		t.Fatalf("expected app-b to be ejected from cluster2 Conf.Apps, got %d", len(cl2.Conf.Apps))
	}
	if len(cl2.Apps) != 0 {
		t.Fatalf("expected app-b to be ejected from cluster2 Apps, got %d", len(cl2.Apps))
	}
	if len(cl1.Conf.Apps) != 1 {
		t.Fatalf("expected app-a in cluster1 to be untouched, got %d apps", len(cl1.Conf.Apps))
	}
}

func TestValidateCrossCluster_NoConflict_DifferentCNameSamePort(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	cl1 := newTestClusterForGateway(t, "cluster1", gw)
	cl1.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
	}

	// Different CNAME, same sourcePort — no conflict.
	cl2 := newTestClusterForGateway(t, "cluster2", gw)
	appBCnf := portRouteApp("app-b", "b.gw.example.com", "9000", "9001")
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	priorRoutes := cl1.OwnGatewayRoutes(gw)
	if err := cl2.ValidateGatewayRouteConflicts(priorRoutes); err != nil {
		t.Fatalf("expected no conflict for different cnames, got: %v", err)
	}
	if len(cl2.Conf.Apps) != 1 {
		t.Fatalf("expected 1 app in Conf.Apps, got %d", len(cl2.Conf.Apps))
	}
	if len(cl2.Apps) != 1 {
		t.Fatalf("expected 1 app in live Apps list, got %d", len(cl2.Apps))
	}
}

func TestValidateCrossCluster_NoPeerOnSameGateway(t *testing.T) {
	const gwA = "ns/svc/gw-a"
	cl1 := newTestClusterForGateway(t, "cluster1", gwA)
	cl1.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "gw.example.com", "9000", "9000"),
	}

	// cl2 is on a different gateway — its routes must not be passed as prior for cl1.
	if err := cl1.ValidateGatewayRouteConflicts(nil); err != nil {
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
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")

	keepCnf := portRouteApp("keep", "keep.gw.example.com", "9000", "9000")
	ejectCnf := portRouteApp("eject", "eject.gw.example.com", "9001", "9001")
	ejectCnf.AppS3Provider = true // mark as S3 provider so its removal is tracked

	cl.Conf.Apps = []*config.AppConfig{keepCnf} // "eject" already removed from conf
	cl.Apps = []*App{
		{Name: "keep", Host: "keep", Port: "80", AppConfig: keepCnf, Mutex: &sync.Mutex{}},
		{Name: "eject", Host: "eject", Port: "80", AppConfig: ejectCnf, Mutex: &sync.Mutex{}},
	}
	cl.AppS3Providers = []string{"eject:80"}

	epochBefore := cl.appListVersion()

	cl.pruneEjectedAppsFromLiveList()

	if len(cl.Apps) != 1 || cl.Apps[0].Name != "keep" {
		t.Fatalf("expected only 'keep' in live Apps, got %v", cl.Apps)
	}
	if len(cl.AppS3Providers) != 0 {
		t.Fatalf("expected empty AppS3Providers after eject, got %v", cl.AppS3Providers)
	}
	if cl.appListVersion() <= epochBefore {
		t.Fatalf("expected appListEpoch to increase after prune, before=%d after=%d",
			epochBefore, cl.appListVersion())
	}
}

func TestPruneEjected_S3ProviderSurvivedWhenKept(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")

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
