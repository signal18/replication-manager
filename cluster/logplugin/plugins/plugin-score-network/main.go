// plugin-score-network evaluates network-access compliance checks:
//
//	NoHostnameGrants — no non-locked user account has a DNS hostname as Host.
//
// Hostname grants are a security concern in two independent ways:
//  1. skip_name_resolve=OFF — the server performs DNS lookups, which are vulnerable
//     to DNS spoofing attacks; an attacker controlling DNS can impersonate a host.
//  2. skip_name_resolve=ON  — the server never resolves hostnames, so any account
//     with a DNS hostname as Host can silently never connect (SEC0118).
//
// The recommended practice is to use IP addresses (or '%' / 'localhost' only) for
// all user grants so the setup is safe under both skip_name_resolve settings.
//
// Exclusions:
//   - Locked accounts (AccountLocked=true) — cannot be used to log in.
//   - '%' wildcard, 'localhost', and IP addresses — these are safe.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// NoHostnameGrants: no non-locked user has a DNS hostname as Host.
	noHostnameGrants := true
	var detail string
	for _, u := range req.DatabaseUsers {
		if u.AccountLocked {
			continue
		}
		if isHostname(u.Host) {
			noHostnameGrants = false
			detail = fmt.Sprintf("account '%s'@'%s' uses DNS hostname grant", u.User, u.Host)
			break
		}
	}

	checks := []wire.ScoreCheck{
		{Tag: "NoHostnameGrants", Pass: noHostnameGrants, Detail: detail},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}

// isHostname returns true if host appears to be a DNS hostname rather than
// an IP address, a wildcard ('%'), or a local alias ('localhost').
func isHostname(host string) bool {
	if host == "" || host == "%" || strings.EqualFold(host, "localhost") {
		return false
	}
	if net.ParseIP(host) != nil {
		return false
	}
	return true
}
