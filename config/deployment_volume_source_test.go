// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// A volume mount source is a path inside one of the app's volumes, never on the host.
func TestVolumeSourceStaysInsideTheVolume(t *testing.T) {
	ok := map[[2]string]string{
		{"api-drbd", "var/crm-api/www/"}: "api-drbd/var/crm-api/www",
		{"bo-tank", "data/"}:             "bo-tank/data",
		{"www-drbd", ""}:                 "www-drbd",
		{"pg1-drbd", "/data"}:            "pg1-drbd/data",
		{"hostfs", ""}:                   "hostfs", // a volume of the app's own, whatever its name
	}
	for in, want := range ok {
		got, err := VolumeSource(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("VolumeSource(%q, %q) = %q, %v; want %q", in[0], in[1], got, err, want)
		}
	}
	for _, in := range [][2]string{
		{"/", ""}, {"/", "etc"}, {"", "/etc"}, {"..", ""}, {"..", ".."},
		{"../../etc/opensvc", ""}, {"vol", "../.."}, {"vol", "data/../../x"},
		{"a/b", ""}, {".hidden", ""}, {"vol name", ""},
	} {
		if got, err := VolumeSource(in[0], in[1]); err == nil {
			t.Errorf("VolumeSource(%q, %q) = %q, want an error", in[0], in[1], got)
		}
	}
}
