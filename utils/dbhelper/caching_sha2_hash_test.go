// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import "testing"

// #2360: only a caching_sha2_password hash is recognised; native hashes, MariaDB ones, empty and
// "invalid" are not.
func TestIsCachingSha2Hash(t *testing.T) {
	cases := map[string]bool{
		"$A$005$abcdefghijklmnopqrstABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghij": true,
		"*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19":                                 false,
		"":           false,
		"invalid":    false,
		"$A$":        false,
		"$5$abc$def": false,
	}
	for in, want := range cases {
		if got := IsCachingSha2Hash(in); got != want {
			t.Errorf("IsCachingSha2Hash(%q) = %v, want %v", in, got, want)
		}
	}
}
