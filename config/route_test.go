// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
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
