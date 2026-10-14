// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package hostmatch classifies the host part of a database account.
package hostmatch

import "strings"

// IsDNSHost reports whether an account host names a DNS host (or a DNS pattern such as
// %.example.com): the accounts a server running skip_name_resolve ignores when it loads
// its privileges. Not one: empty, localhost, '%', an IPv4 address or pattern
// (10.60.%), a netmask (10.0.0.0/255.0.0.0) or an IPv6 address.
func IsDNSHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" || h == "%" || strings.EqualFold(h, "localhost") || strings.ContainsAny(h, ":/") {
		return false
	}
	for _, c := range h {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}
