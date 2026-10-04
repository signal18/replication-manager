// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"strconv"
	"strings"
)

// Several Cloud18 gateways at once (#1873): cloud18-gateway-service and
// cloud18-gateway-domain-name are comma-separated lists, order aligned. A single
// value keeps its meaning. The first entry is the primary one: the app CNAMEs point
// at the first domain (the DNS side round-robins the VIPs under one name).

// SplitGatewayList splits a comma-separated setting, trimmed, empty entries dropped.
func SplitGatewayList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GatewayServices: the OpenSVC services of the gateways (namespace/kind/name), as written.
func (conf *Config) GatewayServices() []string {
	return SplitGatewayList(conf.Cloud18GatewayService)
}

// GatewayServicesLower: the same, lower-cased, the form every gateway comparison uses.
func (conf *Config) GatewayServicesLower() []string {
	out := conf.GatewayServices()
	for i := range out {
		out[i] = strings.ToLower(out[i])
	}
	return out
}

// GatewayDomains: the gateway VIP domains, order aligned with GatewayServices.
func (conf *Config) GatewayDomains() []string {
	return SplitGatewayList(conf.Cloud18GatewayDomainName)
}

// PrimaryGatewayService is the first gateway service, "" when none.
func (conf *Config) PrimaryGatewayService() string {
	if l := conf.GatewayServices(); len(l) > 0 {
		return l[0]
	}
	return ""
}

// PrimaryGatewayDomain is the first gateway domain, the one the CNAMEs point at.
func (conf *Config) PrimaryGatewayDomain() string {
	if l := conf.GatewayDomains(); len(l) > 0 {
		return l[0]
	}
	return ""
}

// HasGateway reports whether gw (any case) is one of the cluster's gateways.
func (conf *Config) HasGateway(gw string) bool {
	gw = strings.ToLower(strings.TrimSpace(gw))
	if gw == "" {
		return false
	}
	for _, g := range conf.GatewayServicesLower() {
		if g == gw {
			return true
		}
	}
	return false
}

// SharesGateway: two clusters are gateway peers when they have one gateway in common.
func (conf *Config) SharesGateway(other *Config) bool {
	if other == nil {
		return false
	}
	for _, g := range conf.GatewayServicesLower() {
		if other.HasGateway(g) {
			return true
		}
	}
	return false
}

// GatewayServiceParts splits "namespace/kind/name" into its namespace and name.
func GatewayServiceParts(gw string) (namespace, name string, ok bool) {
	parts := strings.Split(strings.TrimSpace(gw), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// GatewayBandwidthMbit is the uplink capacity of the i-th gateway in Mb/s
// (cloud18-gateway-bandwidth-mbit, a list aligned with the gateways; a single value
// applies to every gateway; 1000 when unset or unreadable).
func (conf *Config) GatewayBandwidthMbit(i int) float64 {
	l := SplitGatewayList(conf.Cloud18GatewayBandwidthMbit)
	v := ""
	switch {
	case len(l) == 0:
	case i < len(l):
		v = l[i]
	default:
		v = l[len(l)-1]
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
		return f
	}
	return 1000
}

// GatewayBandwidthTotalMbit sums the capacity of every gateway.
func (conf *Config) GatewayBandwidthTotalMbit() float64 {
	n := len(conf.GatewayServices())
	if n == 0 {
		n = len(conf.GatewayDomains())
	}
	total := 0.0
	for i := 0; i < n; i++ {
		total += conf.GatewayBandwidthMbit(i)
	}
	return total
}
