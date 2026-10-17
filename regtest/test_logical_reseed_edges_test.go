// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "testing"

// The second GTID source continues after its highest number: one range, not a number per run.
func TestNextGTIDNumber(t *testing.T) {
	const b5 = "a5ce0000-0000-4000-8000-00000000b005"
	for _, c := range []struct {
		name, executed string
		want           uint64
	}{
		{"no such source", "f1e3c82b-c6ea-11f1-add5-2a71ebf98a95:1-2786", 1},
		{"a range", b5 + ":1-12,f1e3c82b-c6ea-11f1-add5-2a71ebf98a95:1-2786", 13},
		{"single numbers of earlier runs, upper case, line breaks",
			"A5CE0000-0000-4000-8000-00000000B005:1791939662:1792216439:1792217214,\nF1E3C82B-C6EA-11F1-ADD5-2A71EBF98A95:1-2786", 1792217215},
		{"empty set", "", 1},
	} {
		if got := nextGTIDNumber(c.executed, b5); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
