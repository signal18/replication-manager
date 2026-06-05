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
// DetectIntraClusterGatewayConflicts
// ---------------------------------------------------------------------------

func TestDetectIntraCluster_NoConflict_DifferentCNameDifferentPort(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("minio", "s3.gw.example.com", "9000", "9000"),
		portRouteApp("console", "console.gw.example.com", "9001", "9001"),
	}

	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err != nil {
		t.Fatalf("expected no conflict, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

func TestDetectIntraCluster_NoConflict_DifferentCNameSamePort(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "a.gw.example.com", "9000", "9000"),
		portRouteApp("app-b", "b.gw.example.com", "9000", "9001"),
	}

	// Different CNAMEs on same sourcePort are not a conflict (key = cname:sourcePort).
	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err != nil {
		t.Fatalf("expected no conflict for different cnames same sourcePort, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

// intra-cluster duplicate port listener → detector reports conflict, apps remain loaded
func TestDetectIntraCluster_DuplicatePortListener_ReportsConflict(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	appA := portRouteApp("app-a", "gw.example.com", "9000", "9000")
	appB := portRouteApp("app-b", "gw.example.com", "9000", "9001") // same cname + sourcePort
	cl.Conf.Apps = []*config.AppConfig{appA, appB}

	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict (app-b), got %d", len(conflicts))
	}
	if conflicts[0].AppHost != "app-b" {
		t.Errorf("expected app-b as conflicting app, got %q", conflicts[0].AppHost)
	}

	// Detector must NOT mutate Conf.Apps; both apps remain.
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

// intra-cluster duplicate host route → detector reports conflict, apps remain loaded
func TestDetectIntraCluster_DuplicateHostRoute_ReportsConflict(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	appA := hostRouteApp("svc-a", "shared.example.com", "8080")
	appB := hostRouteApp("svc-b", "shared.example.com", "9090")
	cl.Conf.Apps = []*config.AppConfig{appA, appB}

	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err == nil {
		t.Fatal("expected conflict error for duplicate host cname, got nil")
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	// Detector must NOT mutate Conf.Apps; both apps remain.
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

func TestDetectIntraCluster_HostAndPortSameCName_NoConflict(t *testing.T) {
	// A host route and a port route may share the same CNAME.
	cl := newTestClusterForGateway(t, "c1", "ns/svc/gw")
	cl.Conf.Apps = []*config.AppConfig{
		hostRouteApp("minio-host", "minio.example.com", "9001"),
		portRouteApp("minio-port", "minio.example.com", "9000", "9000"),
	}

	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err != nil {
		t.Fatalf("expected no conflict for host+port routes sharing a cname, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

func TestDetectIntraCluster_NoGatewayConfigured(t *testing.T) {
	cl := newTestClusterForGateway(t, "c1", "") // no gateway service
	cl.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "gw.example.com", "9000", "9000"),
		portRouteApp("app-b", "gw.example.com", "9000", "9001"), // would conflict if gateway were set
	}

	conflicts, err := cl.DetectIntraClusterGatewayConflicts()
	if err != nil {
		t.Fatalf("expected no validation when gateway not configured, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts when gateway not configured, got %d", len(conflicts))
	}
	if len(cl.Conf.Apps) != 2 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 2, got %d", len(cl.Conf.Apps))
	}
}

// ---------------------------------------------------------------------------
// DetectCrossClusterGatewayConflicts
// ---------------------------------------------------------------------------

// cross-cluster duplicate route → detector reports conflict, no mutation, error returned
func TestDetectCrossCluster_DuplicateRoute_ReportsConflict(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	cl1 := newTestClusterForGateway(t, "cluster1", gw)
	appACnf := portRouteApp("app-a", "gw.example.com", "9000", "9000")
	cl1.Conf.Apps = []*config.AppConfig{appACnf}
	cl1.Apps = []*App{
		{Name: "app-a", Host: "app-a", Port: "80", AppConfig: appACnf, Mutex: &sync.Mutex{}},
	}

	// cluster2 claims the same cname:sourcePort — conflict.
	cl2 := newTestClusterForGateway(t, "cluster2", gw)
	appBCnf := portRouteApp("app-b", "gw.example.com", "9000", "9001")
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	priorRoutes := cl1.OwnGatewayRoutes(gw)
	conflicts, err := cl2.DetectCrossClusterGatewayConflicts(priorRoutes)
	if err == nil {
		t.Fatal("expected cross-cluster conflict error, got nil")
	}
	if len(conflicts) != 1 || conflicts[0].AppHost != "app-b" {
		t.Fatalf("expected 1 conflict for app-b, got %v", conflicts)
	}

	// Detector must NOT mutate Conf.Apps or live Apps.
	if len(cl2.Conf.Apps) != 1 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 1, got %d", len(cl2.Conf.Apps))
	}
	if len(cl2.Apps) != 1 {
		t.Fatalf("detector must not mutate live Apps; expected 1, got %d", len(cl2.Apps))
	}
	if len(cl1.Conf.Apps) != 1 {
		t.Fatalf("cl1 must be untouched; expected 1, got %d", len(cl1.Conf.Apps))
	}
}

func TestDetectCrossCluster_NoConflict_DifferentCNameSamePort(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	cl1 := newTestClusterForGateway(t, "cluster1", gw)
	appACnf := portRouteApp("app-a", "a.gw.example.com", "9000", "9000")
	cl1.Conf.Apps = []*config.AppConfig{appACnf}
	cl1.Apps = []*App{
		{Name: "app-a", Host: "app-a", Port: "80", AppConfig: appACnf, Mutex: &sync.Mutex{}},
	}

	// Different CNAME, same sourcePort — no conflict.
	cl2 := newTestClusterForGateway(t, "cluster2", gw)
	appBCnf := portRouteApp("app-b", "b.gw.example.com", "9000", "9001")
	cl2.Conf.Apps = []*config.AppConfig{appBCnf}
	cl2.Apps = []*App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	priorRoutes := cl1.OwnGatewayRoutes(gw)
	conflicts, err := cl2.DetectCrossClusterGatewayConflicts(priorRoutes)
	if err != nil {
		t.Fatalf("expected no conflict for different cnames, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
	if len(cl2.Conf.Apps) != 1 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 1, got %d", len(cl2.Conf.Apps))
	}
	if len(cl2.Apps) != 1 {
		t.Fatalf("detector must not mutate live Apps; expected 1, got %d", len(cl2.Apps))
	}
}

func TestDetectCrossCluster_NoPeerOnSameGateway(t *testing.T) {
	const gwA = "ns/svc/gw-a"
	cl1 := newTestClusterForGateway(t, "cluster1", gwA)
	cl1.Conf.Apps = []*config.AppConfig{
		portRouteApp("app-a", "gw.example.com", "9000", "9000"),
	}

	// nil prior routes — first cluster on this gateway, no conflicts possible.
	conflicts, err := cl1.DetectCrossClusterGatewayConflicts(nil)
	if err != nil {
		t.Fatalf("expected no conflict across different gateways, got: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %d", len(conflicts))
	}
	if len(cl1.Conf.Apps) != 1 {
		t.Fatalf("detector must not mutate Conf.Apps; expected 1, got %d", len(cl1.Conf.Apps))
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
