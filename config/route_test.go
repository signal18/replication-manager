// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Route.Validate — structural checks
// ---------------------------------------------------------------------------

func TestValidate_PortRouteRequiresCName(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "http",
		SourcePort:      "9000",
		DestinationPort: "9000",
		// CName intentionally omitted
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected validation error for port route without cname, got nil")
	}
}

func TestValidate_PortRouteWithCNamePasses(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "http",
		CName:           "api.gateway.example.com",
		SourcePort:      "9000",
		DestinationPort: "9000",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidate_HostRouteRequiresCName(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		DestinationPort: "80",
		// CName intentionally omitted
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected validation error for host route without cname, got nil")
	}
}

func TestValidate_PortRouteRejectsColonInSourcePort(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "http",
		CName:           "api.gateway.example.com",
		SourcePort:      "9000:9001",
		DestinationPort: "9000",
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for colon in sourcePort, got nil")
	}
}

func TestValidate_LegacyPortColonRejected(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "app.example.com",
		Port:            "9000:9001",
		DestinationPort: "9000",
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for colon in legacy port, got nil")
	}
}

func TestValidate_HostModeTCPRejected(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "tcp",
		CName:           "app.example.com",
		DestinationPort: "3306",
	}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for host-mode tcp route, got nil")
	}
}

// ---------------------------------------------------------------------------
// CheckGatewayConflicts — collision detection
// ---------------------------------------------------------------------------

func TestCheckGatewayConflicts_DuplicateHostCName(t *testing.T) {
	routes := []Route{
		{Mode: "host", CName: "app.example.com", DestinationPort: "80"},
	}
	others := [][]Route{
		{{Mode: "host", CName: "app.example.com", DestinationPort: "8080"}},
	}
	if err := CheckGatewayConflicts(routes, others...); err == nil {
		t.Fatal("expected conflict error for duplicate host cname, got nil")
	}
}

func TestCheckGatewayConflicts_SameCNameSameSourcePort_Conflict(t *testing.T) {
	routes := []Route{
		{Mode: "port", CName: "gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "gw.example.com", SourcePort: "9000", DestinationPort: "9001"}},
	}
	if err := CheckGatewayConflicts(routes, others...); err == nil {
		t.Fatal("expected conflict for same cname+sourcePort, got nil")
	}
}

func TestCheckGatewayConflicts_SameCNameDifferentSourcePort_NoConflict(t *testing.T) {
	routes := []Route{
		{Mode: "port", CName: "gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "gw.example.com", SourcePort: "9001", DestinationPort: "9001"}},
	}
	if err := CheckGatewayConflicts(routes, others...); err != nil {
		t.Fatalf("expected no conflict for same cname with different sourcePort, got: %v", err)
	}
}

func TestCheckGatewayConflicts_DifferentCNameSameSourcePort_NoConflict(t *testing.T) {
	routes := []Route{
		{Mode: "port", CName: "a.gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "b.gw.example.com", SourcePort: "9000", DestinationPort: "9001"}},
	}
	if err := CheckGatewayConflicts(routes, others...); err != nil {
		t.Fatalf("expected no conflict for different cnames same sourcePort, got: %v", err)
	}
}

func TestCheckGatewayConflicts_HostAndPortSameCName_NoConflict(t *testing.T) {
	// A host route and a port route may share the same CNAME — different key spaces.
	hostRoute := []Route{
		{Mode: "host", CName: "minio.example.com", DestinationPort: "9001", Protocol: "https"},
	}
	portRoute := [][]Route{
		{{Mode: "port", CName: "minio.example.com", SourcePort: "9000", DestinationPort: "9000", Protocol: "http"}},
	}
	if err := CheckGatewayConflicts(hostRoute, portRoute...); err != nil {
		t.Fatalf("expected no conflict for host+port routes sharing a cname, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// BuildRouteToken — hash includes cname for port routes
// ---------------------------------------------------------------------------

func TestBuildRouteToken_PortRouteHashIncludesCName(t *testing.T) {
	base := Route{
		Mode:            "port",
		SourcePort:      "9000",
		DestinationPort: "9000",
	}
	withCName := base
	withCName.CName = "api.gw.example.com"
	withoutCName := base

	tokenWith := BuildRouteToken(withCName)
	tokenWithout := BuildRouteToken(withoutCName)

	if tokenWith == tokenWithout {
		t.Fatalf("expected different tokens when CName differs, both got %q", tokenWith)
	}
}

func TestBuildRouteToken_TwoDifferentCNamesProduceDifferentTokens(t *testing.T) {
	r1 := Route{Mode: "port", CName: "a.gw.example.com", SourcePort: "9000", DestinationPort: "9000"}
	r2 := Route{Mode: "port", CName: "b.gw.example.com", SourcePort: "9000", DestinationPort: "9000"}
	if BuildRouteToken(r1) == BuildRouteToken(r2) {
		t.Fatal("expected different route tokens for different cnames on same port")
	}
}

// ---------------------------------------------------------------------------
// BuildRouteStateKey — stable monitoring debounce key
// ---------------------------------------------------------------------------

func TestBuildRouteStateKey_HostRouteIncludesProtocolAndDestPort(t *testing.T) {
	r := Route{Mode: "host", Protocol: "https", CName: "app.example.com", DestinationPort: "9001"}
	key := BuildRouteStateKey(r)
	if key == "" {
		t.Fatal("expected non-empty state key")
	}
	// Key must not change when only the route Name changes (cosmetic rename).
	r2 := r
	r2.Name = "renamed"
	if BuildRouteStateKey(r2) != key {
		t.Fatal("expected stable key after cosmetic name change")
	}
}

func TestBuildRouteStateKey_PortRouteIncludesSourcePortAndProtocol(t *testing.T) {
	r := Route{Mode: "port", Protocol: "http", CName: "minio.example.com", SourcePort: "9000", DestinationPort: "9000"}
	key := BuildRouteStateKey(r)
	// Changing source port must produce a different key.
	r2 := r
	r2.SourcePort = "9001"
	if BuildRouteStateKey(r2) == key {
		t.Fatal("expected different key for different sourcePort")
	}
}

func TestBuildRouteStateKey_ProtocolChangeProducesDifferentKey(t *testing.T) {
	http := Route{Mode: "port", Protocol: "http", CName: "api.example.com", SourcePort: "8080", DestinationPort: "8080"}
	tcp := http
	tcp.Protocol = "tcp"
	if BuildRouteStateKey(http) == BuildRouteStateKey(tcp) {
		t.Fatal("expected different keys for different protocols on same port route")
	}
}

func TestBuildRouteStateKey_HostAndPortRoutesDifferEvenWithSameCName(t *testing.T) {
	host := Route{Mode: "host", Protocol: "https", CName: "shared.example.com", DestinationPort: "9001"}
	port := Route{Mode: "port", Protocol: "http", CName: "shared.example.com", SourcePort: "9000", DestinationPort: "9000"}
	if BuildRouteStateKey(host) == BuildRouteStateKey(port) {
		t.Fatal("expected host and port routes to produce different state keys even when cname matches")
	}
}

// ---------------------------------------------------------------------------
// Normalize — backward-compatible legacy routes
// ---------------------------------------------------------------------------

func TestNormalize_LegacyHostRoute(t *testing.T) {
	r := Route{
		CName:    "myapp.example.com",
		Port:     "80",
		Protocol: "https",
		Primary:  true,
	}
	r.Normalize()
	if r.Mode != "host" {
		t.Errorf("expected mode=host, got %q", r.Mode)
	}
	if r.DestinationPort != "80" {
		t.Errorf("expected destPort=80, got %q", r.DestinationPort)
	}
	if r.SourcePort != "" {
		t.Errorf("expected sourcePort empty for host route, got %q", r.SourcePort)
	}
}

func TestNormalize_PortRouteFromLegacyTCPPort(t *testing.T) {
	r := Route{
		CName:    "tcp.example.com",
		Port:     "3306",
		Protocol: "tcp",
	}
	r.Normalize()
	if r.Mode != "port" {
		t.Errorf("expected mode=port for tcp protocol, got %q", r.Mode)
	}
	if r.SourcePort != "3306" {
		t.Errorf("expected sourcePort=3306, got %q", r.SourcePort)
	}
	if r.DestinationPort != "3306" {
		t.Errorf("expected destPort=3306, got %q", r.DestinationPort)
	}
}

// ---------------------------------------------------------------------------
// RouteMonitor.Normalize — defaults and canonicalization
// ---------------------------------------------------------------------------

func TestMonitorNormalize_EmptyBecomesDefaults(t *testing.T) {
	var m RouteMonitor
	m.Normalize()
	if m.Path != "/" {
		t.Errorf("expected path=/, got %q", m.Path)
	}
	if m.ExpectStatus != "200" {
		t.Errorf("expected expectStatus=200, got %q", m.ExpectStatus)
	}
	if m.AuthType != "" {
		t.Errorf("expected authType empty, got %q", m.AuthType)
	}
}

func TestMonitorNormalize_NoneAuthTypeCleared(t *testing.T) {
	m := RouteMonitor{AuthType: "none"}
	m.Normalize()
	if m.AuthType != "" {
		t.Errorf("expected authType empty after normalizing 'none', got %q", m.AuthType)
	}
}

func TestMonitorNormalize_PathWithoutLeadingSlashFixed(t *testing.T) {
	m := RouteMonitor{Path: "health"}
	m.Normalize()
	if m.Path != "/health" {
		t.Errorf("expected /health, got %q", m.Path)
	}
}

func TestMonitorNormalize_AuthTypeLowercased(t *testing.T) {
	m := RouteMonitor{AuthType: "BASIC", AuthUser: "u", AuthSecretVar: "S"}
	m.Normalize()
	if m.AuthType != "basic" {
		t.Errorf("expected basic, got %q", m.AuthType)
	}
}

// ---------------------------------------------------------------------------
// RouteMonitor.Validate — structural checks
// ---------------------------------------------------------------------------

func TestMonitorValidate_EmptyMonitorPasses(t *testing.T) {
	var m RouteMonitor
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatalf("expected empty monitor to pass, got: %v", err)
	}
}

func TestMonitorValidate_BasicRequiresAuthUser(t *testing.T) {
	m := RouteMonitor{AuthType: "basic", AuthSecretVar: "MY_SECRET"}
	m.Normalize()
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for basic auth without auth-user, got nil")
	}
}

func TestMonitorValidate_BasicRequiresAuthSecretVar(t *testing.T) {
	m := RouteMonitor{AuthType: "basic", AuthUser: "monitor"}
	m.Normalize()
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for basic auth without auth-secret-var, got nil")
	}
}

func TestMonitorValidate_BasicWithBothFieldsPasses(t *testing.T) {
	m := RouteMonitor{AuthType: "basic", AuthUser: "monitor", AuthSecretVar: "MY_SECRET"}
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected error for valid basic auth monitor: %v", err)
	}
}

func TestMonitorValidate_BearerRequiresAuthSecretVar(t *testing.T) {
	m := RouteMonitor{AuthType: "bearer"}
	m.Normalize()
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for bearer auth without auth-secret-var, got nil")
	}
}

func TestMonitorValidate_BearerWithSecretVarPasses(t *testing.T) {
	m := RouteMonitor{AuthType: "bearer", AuthSecretVar: "MY_TOKEN"}
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected error for valid bearer auth monitor: %v", err)
	}
}

func TestMonitorValidate_InvalidAuthTypeFails(t *testing.T) {
	m := RouteMonitor{AuthType: "digest"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for invalid auth-type 'digest', got nil")
	}
}

func TestMonitorValidate_InvalidExpectStatusFails(t *testing.T) {
	m := RouteMonitor{ExpectStatus: "ok"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for non-numeric expect-status, got nil")
	}
}

func TestMonitorValidate_ValidExpectStatusPasses(t *testing.T) {
	m := RouteMonitor{ExpectStatus: "200,204"}
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected error for valid expect-status '200,204': %v", err)
	}
}

// ---------------------------------------------------------------------------
// ParseExpectStatus
// ---------------------------------------------------------------------------

func TestParseExpectStatus_SingleCode(t *testing.T) {
	codes, err := ParseExpectStatus("200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 1 || codes[0] != 200 {
		t.Fatalf("expected [200], got %v", codes)
	}
}

func TestParseExpectStatus_MultipleCodesWithSpaces(t *testing.T) {
	codes, err := ParseExpectStatus("200, 204, 302")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %v", codes)
	}
}

func TestParseExpectStatus_DuplicatesDeduped(t *testing.T) {
	codes, err := ParseExpectStatus("200,200,204")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("expected 2 codes after dedup, got %v", codes)
	}
}

func TestParseExpectStatus_EmptyElementFails(t *testing.T) {
	if _, err := ParseExpectStatus("200,,204"); err == nil {
		t.Fatal("expected error for empty element in '200,,204', got nil")
	}
}

func TestParseExpectStatus_NonNumericFails(t *testing.T) {
	if _, err := ParseExpectStatus("ok"); err == nil {
		t.Fatal("expected error for non-numeric code, got nil")
	}
}

func TestParseExpectStatus_OutOfRangeFails(t *testing.T) {
	if _, err := ParseExpectStatus("9999"); err == nil {
		t.Fatal("expected error for out-of-range code 9999, got nil")
	}
}

func TestParseExpectStatus_BoundaryCodesAccepted(t *testing.T) {
	codes, err := ParseExpectStatus("100,599")
	if err != nil {
		t.Fatalf("unexpected error for boundary codes: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("expected 2 codes, got %v", codes)
	}
}

// ---------------------------------------------------------------------------
// RouteMonitor.ValidateSecretRef
// ---------------------------------------------------------------------------

func TestValidateSecretRef_NilReceiverPasses(t *testing.T) {
	var m *RouteMonitor
	if err := m.ValidateSecretRef(VariableMaps{}); err != nil {
		t.Fatalf("expected nil for nil receiver, got: %v", err)
	}
}

func TestValidateSecretRef_EmptyVarNamePasses(t *testing.T) {
	m := RouteMonitor{}
	vars := VariableMaps{{Name: "FOO", Type: VariableTypeSecret}}
	if err := m.ValidateSecretRef(vars); err != nil {
		t.Fatalf("expected nil for empty auth-secret-var, got: %v", err)
	}
}

func TestValidateSecretRef_MissingVarFails(t *testing.T) {
	m := RouteMonitor{AuthSecretVar: "MISSING"}
	vars := VariableMaps{}
	if err := m.ValidateSecretRef(vars); err == nil {
		t.Fatal("expected error for missing variable, got nil")
	}
}

func TestValidateSecretRef_NonSecretTypeFails(t *testing.T) {
	m := RouteMonitor{AuthSecretVar: "MY_VAR"}
	vars := VariableMaps{{Name: "MY_VAR", Type: VariableTypeEnv}}
	if err := m.ValidateSecretRef(vars); err == nil {
		t.Fatal("expected error when referenced variable is not type 'secret', got nil")
	}
}

func TestValidateSecretRef_SecretTypePasses(t *testing.T) {
	m := RouteMonitor{AuthSecretVar: "MY_SECRET"}
	vars := VariableMaps{{Name: "MY_SECRET", Type: VariableTypeSecret}}
	if err := m.ValidateSecretRef(vars); err != nil {
		t.Fatalf("unexpected error for valid secret ref: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Route.Validate — monitor block integrated
// ---------------------------------------------------------------------------

func TestValidate_RouteWithValidMonitorPasses(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "app.example.com",
		DestinationPort: "8080",
		Monitor:         &RouteMonitor{Path: "/health", ExpectStatus: "200,204"},
	}
	r.Normalize()
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected error for route with valid monitor: %v", err)
	}
}

func TestValidate_RouteWithInvalidMonitorFails(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "app.example.com",
		DestinationPort: "8080",
		Monitor:         &RouteMonitor{AuthType: "basic"}, // missing auth-user and auth-secret-var
	}
	r.Normalize()
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for route with invalid monitor (basic auth missing fields), got nil")
	}
}

// ---------------------------------------------------------------------------
// Route.Clone — pointer safety
// ---------------------------------------------------------------------------

func TestRouteClone_NilMonitorClonesClean(t *testing.T) {
	r := Route{Mode: "host", Protocol: "https", CName: "app.example.com", DestinationPort: "80"}
	c := r.Clone()
	if c.Monitor != nil {
		t.Fatal("expected nil monitor on clone of route with no monitor")
	}
}

func TestRouteClone_MonitorIsDeepCopied(t *testing.T) {
	m := &RouteMonitor{Path: "/health"}
	r := Route{Monitor: m}
	c := r.Clone()
	c.Monitor.Path = "/changed"
	if m.Path != "/health" {
		t.Fatal("clone modified the original monitor — Monitor pointer was not deep-copied")
	}
}

// ---------------------------------------------------------------------------
// NormalizedCopy — legacy route does not acquire a monitor
// ---------------------------------------------------------------------------

func TestNormalizedCopy_LegacyRouteKeepsNilMonitor(t *testing.T) {
	routes := []Route{
		{CName: "legacy.example.com", Port: "80", Protocol: "https"},
	}
	out := NormalizedCopy(routes)
	if out[0].Monitor != nil {
		t.Fatal("NormalizedCopy injected a synthetic monitor into a legacy route that had none")
	}
}

// ---------------------------------------------------------------------------
// Deployment.ValidateRoutes — secret-ref checked in shared path
// ---------------------------------------------------------------------------

func TestDeploymentValidateRoutes_BrokenSecretRefFails(t *testing.T) {
	d := &Deployment{
		Routes: Routes{
			{
				Mode: "host", Protocol: "https", CName: "app.example.com", DestinationPort: "80",
				Monitor: &RouteMonitor{AuthType: "bearer", AuthSecretVar: "MISSING_VAR"},
			},
		},
		Variables: VariableMaps{},
	}
	d.NormalizeRoutes()
	if err := d.ValidateRoutes(); err == nil {
		t.Fatal("expected validation error for broken secret ref in ValidateRoutes, got nil")
	}
}

func TestDeploymentValidateRoutes_ValidSecretRefPasses(t *testing.T) {
	d := &Deployment{
		Routes: Routes{
			{
				Mode: "host", Protocol: "https", CName: "app.example.com", DestinationPort: "80",
				Monitor: &RouteMonitor{AuthType: "bearer", AuthSecretVar: "MY_TOKEN"},
			},
		},
		Variables: VariableMaps{{Name: "MY_TOKEN", Type: VariableTypeSecret}},
	}
	d.NormalizeRoutes()
	if err := d.ValidateRoutes(); err != nil {
		t.Fatalf("unexpected error for valid secret ref: %v", err)
	}
}

func TestDeploymentValidateRoutes_NoMonitorNoSecretCheck(t *testing.T) {
	d := &Deployment{
		Routes: Routes{
			{Mode: "host", Protocol: "https", CName: "legacy.example.com", DestinationPort: "443"},
		},
		Variables: VariableMaps{},
	}
	d.NormalizeRoutes()
	if err := d.ValidateRoutes(); err != nil {
		t.Fatalf("backward-compat: unexpected error for route without monitor: %v", err)
	}
}

func TestValidate_RouteWithoutMonitorBackwardCompat(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "legacy.example.com",
		DestinationPort: "443",
	}
	r.Normalize()
	if err := r.Validate(); err != nil {
		t.Fatalf("backward-compat: unexpected error for route without monitor: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Asymmetric port-mode route — Normalize preserves explicit ports
// ---------------------------------------------------------------------------

// TestNormalize_AsymmetricPortRoute verifies that Normalize does not overwrite
// SourcePort or DestinationPort when they are already set, preserving an
// intentionally asymmetric port-mode route (SourcePort != DestinationPort).
//
// This is the invariant the API handler relies on when rejecting a "port" field
// edit on an asymmetric route: clearing both derived fields and calling
// Normalize would silently collapse them to the same value.
// TestNormalize_LegacyTCPRoute_InferredAsPortMode verifies that Normalize
// promotes Mode=="" + Protocol=="tcp" to Mode=="port".  The API handler's
// asymmetry guard uses the same inference so that legacy routes without an
// explicit mode field cannot bypass the guard.
func TestNormalize_LegacyTCPRoute_InferredAsPortMode(t *testing.T) {
	r := Route{
		// Mode intentionally absent — legacy on-disk format before the mode field.
		Protocol:        "tcp",
		CName:           "db.gateway.example.com",
		SourcePort:      "33060",
		DestinationPort: "3306",
	}
	r.Normalize()
	if r.Mode != "port" {
		t.Errorf("expected Mode inferred as %q for tcp route without explicit mode, got %q", "port", r.Mode)
	}
}

func TestNormalize_AsymmetricPortRoute_PreservesDistinctPorts(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "tcp",
		CName:           "db.gateway.example.com",
		Port:            "3306",
		SourcePort:      "33060",
		DestinationPort: "3306",
	}
	r.Normalize()

	if r.SourcePort != "33060" {
		t.Errorf("Normalize overwrote SourcePort: got %q, want %q", r.SourcePort, "33060")
	}
	if r.DestinationPort != "3306" {
		t.Errorf("Normalize overwrote DestinationPort: got %q, want %q", r.DestinationPort, "3306")
	}
}

// TestNormalize_AsymmetricPortRoute_CollapseAfterClear documents the dangerous
// pattern that the API handler guards against: explicitly clearing SourcePort
// and DestinationPort before calling Normalize causes them to be re-derived
// from Port, making a previously asymmetric route symmetric.
func TestNormalize_AsymmetricPortRoute_CollapseAfterClear(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "tcp",
		CName:           "db.gateway.example.com",
		Port:            "9999",
		SourcePort:      "33060",
		DestinationPort: "3306",
	}

	// Simulate the dangerous pattern: clear both derived ports, then Normalize.
	r.SourcePort = ""
	r.DestinationPort = ""
	r.Normalize()

	// Both ports are now equal to Port — the asymmetry is lost.
	if r.SourcePort != "9999" {
		t.Errorf("expected SourcePort collapsed to Port value, got %q", r.SourcePort)
	}
	if r.DestinationPort != "9999" {
		t.Errorf("expected DestinationPort collapsed to Port value, got %q", r.DestinationPort)
	}
}

// ---------------------------------------------------------------------------
// Backward-compatibility layer — Port back-population
// ---------------------------------------------------------------------------

func TestNormalize_LegacyHostRoute_PortBackPopulated(t *testing.T) {
	r := Route{CName: "app.example.com", Protocol: "https", Port: "8080"}
	r.Normalize()
	if r.DestinationPort != "8080" {
		t.Errorf("expected destPort=8080, got %q", r.DestinationPort)
	}
	if r.Port != "8080" {
		t.Errorf("expected Port=8080 for old clients, got %q", r.Port)
	}
}

func TestNormalize_LegacyTCPSinglePort_PortBackPopulated(t *testing.T) {
	r := Route{CName: "gw.example.com", Protocol: "tcp", Port: "3306"}
	r.Normalize()
	if r.SourcePort != "3306" || r.DestinationPort != "3306" {
		t.Errorf("expected src=3306 dst=3306, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "3306" {
		t.Errorf("expected Port=3306 for old clients, got %q", r.Port)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestNormalize_ColonPort_AsymmetricSplit(t *testing.T) {
	r := Route{CName: "gw.example.com", Protocol: "tcp", Port: "9000:9001"}
	r.Normalize()
	if r.SourcePort != "9000" || r.DestinationPort != "9001" {
		t.Errorf("expected src=9000 dst=9001, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "9000:9001" {
		t.Errorf("expected Port=9000:9001 for old clients, got %q", r.Port)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected validation error after colon-port split: %v", err)
	}
}

func TestNormalize_ColonPort_WithSourcePortPreset_FillsDestinationOnly(t *testing.T) {
	r := Route{
		Mode:       "port",
		Protocol:   "tcp",
		CName:      "gw.example.com",
		Port:       "9000:9001",
		SourcePort: "9000",
	}
	r.Normalize()
	if r.SourcePort != "9000" || r.DestinationPort != "9001" {
		t.Fatalf("expected src=9000 dst=9001, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "9000:9001" {
		t.Fatalf("expected Port=9000:9001 after partial migration normalize, got %q", r.Port)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected validation error after partial migration normalize: %v", err)
	}
}

func TestNormalize_ColonPort_WithDestinationPortPreset_FillsSourceOnly(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "tcp",
		CName:           "gw.example.com",
		Port:            "9000:9001",
		DestinationPort: "9001",
	}
	r.Normalize()
	if r.SourcePort != "9000" || r.DestinationPort != "9001" {
		t.Fatalf("expected src=9000 dst=9001, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "9000:9001" {
		t.Fatalf("expected Port=9000:9001 after partial migration normalize, got %q", r.Port)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected validation error after partial migration normalize: %v", err)
	}
}

func TestNormalize_CanonicalPortOverridesStaleLegacyPort(t *testing.T) {
	r := Route{
		Mode:            "port",
		Protocol:        "tcp",
		CName:           "gw.example.com",
		Port:            "3306",
		SourcePort:      "9000",
		DestinationPort: "9001",
	}
	r.Normalize()
	if r.Port != "9000:9001" {
		t.Fatalf("expected Port re-derived from canonical fields to be 9000:9001, got %q", r.Port)
	}
}

func TestNormalize_HostCanonicalDestPortOverridesStaleLegacyPort(t *testing.T) {
	r := Route{
		Mode:            "host",
		Protocol:        "https",
		CName:           "app.example.com",
		Port:            "8080",
		DestinationPort: "9090",
	}
	r.Normalize()
	if r.Port != "9090" {
		t.Fatalf("expected Port to follow DestinationPort=9090, got %q", r.Port)
	}
}

func TestNormalize_Idempotent_PortConsistent(t *testing.T) {
	r := Route{CName: "gw.example.com", Protocol: "tcp", Port: "9000:9001"}
	r.Normalize()
	port1, src1, dst1 := r.Port, r.SourcePort, r.DestinationPort
	r.Normalize()
	if r.Port != port1 || r.SourcePort != src1 || r.DestinationPort != dst1 {
		t.Errorf("Normalize not idempotent: first={%s/%s/%s} second={%s/%s/%s}",
			port1, src1, dst1, r.Port, r.SourcePort, r.DestinationPort)
	}
}

func TestNormalize_SymmetricPortEdit_OldClient(t *testing.T) {
	r := Route{Mode: "port", Protocol: "tcp", CName: "gw.example.com",
		Port: "3306", SourcePort: "3306", DestinationPort: "3306"}
	r.Port = "5432"
	r.SourcePort = ""
	r.DestinationPort = ""
	r.Normalize()
	if r.SourcePort != "5432" || r.DestinationPort != "5432" {
		t.Errorf("expected src=5432 dst=5432, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "5432" {
		t.Errorf("expected Port=5432 for old clients, got %q", r.Port)
	}
}

func TestNormalize_AsymmetricPortEdit_ColonForm_OldClient(t *testing.T) {
	r := Route{Mode: "port", Protocol: "tcp", CName: "gw.example.com",
		Port: "9000:9001", SourcePort: "9000", DestinationPort: "9001"}
	r.Port = "8000:8001"
	r.SourcePort = ""
	r.DestinationPort = ""
	r.Normalize()
	if r.SourcePort != "8000" || r.DestinationPort != "8001" {
		t.Errorf("expected src=8000 dst=8001, got src=%q dst=%q", r.SourcePort, r.DestinationPort)
	}
	if r.Port != "8000:8001" {
		t.Errorf("expected Port=8000:8001 for old clients, got %q", r.Port)
	}
}

func TestNormalize_DestPortEdit_PortBackPopulated(t *testing.T) {
	r := Route{Mode: "host", Protocol: "https", CName: "app.example.com",
		Port: "8080", DestinationPort: "8080"}
	r.DestinationPort = "9090"
	r.Port = ""
	r.Normalize()
	if r.Port != "9090" {
		t.Errorf("expected Port=9090 after destport edit, got %q", r.Port)
	}
}

func TestRoute_JSONMarshal_LegacyPortPresent(t *testing.T) {
	r := Route{CName: "app.example.com", Protocol: "https", Port: "8080"}
	r.Normalize()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m["port"] != "8080" {
		t.Fatalf("expected port=8080 in JSON for old clients, got %v", m["port"])
	}
}

func TestRoute_JSONMarshal_AsymmetricPortPresent(t *testing.T) {
	r := Route{CName: "gw.example.com", Protocol: "tcp", Port: "9000:9001"}
	r.Normalize()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m["port"] != "9000:9001" {
		t.Fatalf("expected port=9000:9001 in JSON for old clients, got %v", m["port"])
	}
}

func TestValidate_HostRouteColonPortStillRejected(t *testing.T) {
	r := Route{Mode: "host", Protocol: "https", CName: "app.example.com",
		Port: "9000:9001", DestinationPort: "9000"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error: host route with colon Port must be rejected by Validate()")
	}
}

func TestNormalize_ColonPort_ProvisioningFields(t *testing.T) {
	r := Route{Protocol: "tcp", CName: "gw.example.com", Port: "9000:9001"}
	r.Normalize()
	if r.SourcePort != "9000" {
		t.Errorf("HAProxy bind port: expected SourcePort=9000, got %q", r.SourcePort)
	}
	if r.DestinationPort != "9001" {
		t.Errorf("HAProxy backend port: expected DestinationPort=9001, got %q", r.DestinationPort)
	}
}
