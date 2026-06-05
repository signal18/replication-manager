package cluster

import (
	"hash/crc64"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// newDNSTestCluster creates a minimal cluster configured for managed-CNAME tests.
// The managed suffix will be: .<clusterName>.dev2.signal18.cloud18.io
func newDNSTestCluster(t *testing.T, clusterName, dropScript string) *Cluster {
	t.Helper()
	return &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir:              t.TempDir(),
			Cloud18Domain:           "signal18",
			Cloud18SubDomain:        "dev2",
			Cloud18GatewayDomainName: "cloud18.io",
			Cloud18DomainDropScript: dropScript,
		},
		crcTable: crc64.MakeTable(crc64.ISO),
	}
}

func newDNSTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return &App{
		Name:    "testapp",
		Datadir: dir,
		Mutex:   &sync.Mutex{},
	}
}

// managedFQDN builds a FQDN that is under the managed suffix for clusterName.
func managedFQDN(label, clusterName string) string {
	return label + "." + clusterName + ".dev2.signal18.cloud18.io"
}

// ---------------------------------------------------------------------------
// Case 1 — missing drop script does not block reconcile
// ---------------------------------------------------------------------------

func TestReconcileStaleManagedCNAMEs_NoDropScript_WarnsAndContinues(t *testing.T) {
	cl := newDNSTestCluster(t, "c1", "") // no drop script
	app := newDNSTestApp(t)

	staleFQDN := managedFQDN("stale", "c1")
	old := map[string]struct{}{staleFQDN: {}}
	new_ := map[string]struct{}{} // route removed

	persisted, err := cl.reconcileStaleManagedCNAMEs(app, old, new_)
	if err != nil {
		t.Fatalf("expected no error when drop script is unset, got: %v", err)
	}
	if _, ok := persisted[staleFQDN]; !ok {
		t.Fatalf("expected stale CNAME %q to be retained in persisted set, got %v", staleFQDN, persisted)
	}
}

// Case 1b — port-route-only app with old host-route DNS state and no drop script.
func TestReconcileStaleManagedCNAMEs_PortRouteOnlyApp_NoDropScript_Continues(t *testing.T) {
	cl := newDNSTestCluster(t, "c1", "")
	app := newDNSTestApp(t)

	staleFQDN := managedFQDN("oldhost", "c1")
	old := map[string]struct{}{staleFQDN: {}}
	new_ := map[string]struct{}{} // current routes are port-only, no managed CNAMEs

	persisted, err := cl.reconcileStaleManagedCNAMEs(app, old, new_)
	if err != nil {
		t.Fatalf("expected no error for port-route-only app with stale host DNS and no drop script, got: %v", err)
	}
	if _, ok := persisted[staleFQDN]; !ok {
		t.Fatalf("expected stale CNAME to be retained for later cleanup, got %v", persisted)
	}
}

// ---------------------------------------------------------------------------
// Case 2 — no stale CNAMEs: persisted == new set, no side effects
// ---------------------------------------------------------------------------

func TestReconcileStaleManagedCNAMEs_NoStaleCNAMEs_PersistsNewSet(t *testing.T) {
	cl := newDNSTestCluster(t, "c1", "")
	app := newDNSTestApp(t)

	fqdn := managedFQDN("wanted", "c1")
	old := map[string]struct{}{fqdn: {}}
	new_ := map[string]struct{}{fqdn: {}}

	persisted, err := cl.reconcileStaleManagedCNAMEs(app, old, new_)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected 1 persisted entry, got %d: %v", len(persisted), persisted)
	}
	if _, ok := persisted[fqdn]; !ok {
		t.Fatalf("expected %q in persisted set", fqdn)
	}
}

// ---------------------------------------------------------------------------
// Case 3 — drop script configured: deletion runs, persisted == new set only
// ---------------------------------------------------------------------------

func TestReconcileStaleManagedCNAMEs_DropScriptConfigured_DeletesStale(t *testing.T) {
	// Write a tiny shell script that accepts arguments and exits 0.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "drop.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write drop script: %v", err)
	}

	cl := newDNSTestCluster(t, "c1", scriptPath)
	app := newDNSTestApp(t)

	staleFQDN := managedFQDN("stale", "c1")
	keptFQDN := managedFQDN("kept", "c1")

	old := map[string]struct{}{staleFQDN: {}}
	new_ := map[string]struct{}{keptFQDN: {}}

	persisted, err := cl.reconcileStaleManagedCNAMEs(app, old, new_)
	if err != nil {
		t.Fatalf("expected no error with valid drop script, got: %v", err)
	}
	// Stale entry must NOT be in persisted set.
	if _, ok := persisted[staleFQDN]; ok {
		t.Fatalf("expected stale CNAME %q to be removed from persisted set", staleFQDN)
	}
	// Kept entry must survive.
	if _, ok := persisted[keptFQDN]; !ok {
		t.Fatalf("expected %q to be present in persisted set", keptFQDN)
	}
}

// ---------------------------------------------------------------------------
// stale CNAME from a previous managed suffix — should warn and retain
// ---------------------------------------------------------------------------

func TestReconcileStaleManagedCNAMEs_StaleUnderOldSuffix_WarnsAndRetains(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "drop.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write drop script: %v", err)
	}

	cl := newDNSTestCluster(t, "c1", scriptPath)
	app := newDNSTestApp(t)

	// This FQDN was provisioned under a different cluster name/suffix.
	externalFQDN := "stale.old-cluster.dev2.signal18.cloud18.io"
	old := map[string]struct{}{externalFQDN: {}}
	new_ := map[string]struct{}{}

	persisted, err := cl.reconcileStaleManagedCNAMEs(app, old, new_)
	if err != nil {
		t.Fatalf("expected no error when stale CNAME is under old suffix, got: %v", err)
	}
	// Entry must be retained in persisted for manual cleanup, not silently dropped.
	if _, ok := persisted[externalFQDN]; !ok {
		t.Fatalf("expected stale CNAME %q to be retained in persisted set for manual cleanup, got %v", externalFQDN, persisted)
	}
}
