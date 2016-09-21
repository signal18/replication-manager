// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package misc

import "testing"

func TestGetLocalIP(t *testing.T) {
	ip := getLocalIP()
	if ip == "" {
		t.Fatal("getLocalIP() returned empty string, expected IP")
	}
	t.Log("got Local IP:", ip)
}
