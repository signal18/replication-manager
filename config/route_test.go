// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
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
// CheckGatewayConflicts — structural (no resolver)
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

func TestCheckGatewayConflicts_DuplicatePortRouteSourcePort(t *testing.T) {
	routes := []Route{
		{Mode: "port", CName: "a.gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "b.gw.example.com", SourcePort: "9000", DestinationPort: "9001"}},
	}
	// Without resolver, keyed on cname:sourcePort — different cnames, same port = no conflict.
	if err := CheckGatewayConflicts(routes, others...); err != nil {
		t.Fatalf("expected no conflict for different cnames without resolver, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CheckGatewayConflictsWithResolver — environmental checks
// ---------------------------------------------------------------------------

func makeResolver(mapping map[string]string) PortRouteBindResolver {
	return func(cname string) (string, error) {
		if addr, ok := mapping[cname]; ok {
			return addr, nil
		}
		return "", errors.New("cname not in gateway bind addresses")
	}
}

func TestCheckGatewayConflictsWithResolver_SameBindAddrAndPort(t *testing.T) {
	resolver := makeResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		"b.gw.example.com": "10.10.10.25",
	})
	current := []Route{
		{Mode: "port", CName: "a.gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "b.gw.example.com", SourcePort: "9000", DestinationPort: "9001"}},
	}
	// Both CNAMEs resolve to the same VIP, same source port — must conflict.
	if err := CheckGatewayConflictsWithResolver(resolver, current, others...); err == nil {
		t.Fatal("expected conflict for same resolved bindAddress:sourcePort, got nil")
	}
}

func TestCheckGatewayConflictsWithResolver_DifferentBindAddrSamePort(t *testing.T) {
	resolver := makeResolver(map[string]string{
		"a.gw.example.com": "10.10.10.25",
		"b.gw.example.com": "10.10.10.26",
	})
	current := []Route{
		{Mode: "port", CName: "a.gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	others := [][]Route{
		{{Mode: "port", CName: "b.gw.example.com", SourcePort: "9000", DestinationPort: "9001"}},
	}
	// Different VIPs, same source port — must NOT conflict.
	if err := CheckGatewayConflictsWithResolver(resolver, current, others...); err != nil {
		t.Fatalf("expected no conflict for different bind addresses, got: %v", err)
	}
}

func TestCheckGatewayConflictsWithResolver_ResolverErrorFailsClosed(t *testing.T) {
	failResolver := PortRouteBindResolver(func(cname string) (string, error) {
		return "", errors.New("DNS lookup failed")
	})
	current := []Route{
		{Mode: "port", CName: "broken.gw.example.com", SourcePort: "9000", DestinationPort: "9000"},
	}
	// Resolver error must propagate — fail closed, not fall back to cname:sourcePort.
	if err := CheckGatewayConflictsWithResolver(failResolver, current); err == nil {
		t.Fatal("expected error when resolver fails, got nil (should fail closed)")
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
