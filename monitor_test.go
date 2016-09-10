// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import "testing"

var Tservers serverList

func TestMain(m *testing.M) {
	Tservers = make(serverList, 3)
	Tservers[0] = &ServerMonitor{URL: "192.168.0.0", MasterServerID: 1}
	Tservers[1] = &ServerMonitor{URL: "192.168.0.1", MasterServerID: 1}
	Tservers[2] = &ServerMonitor{URL: "192.168.0.2", MasterServerID: 1}
	m.Run()
}

func TestDelete(t *testing.T) {
	Tservers[1].delete(&Tservers)
	if len(Tservers) != 2 {
		t.Fatalf("List length was %d, expected 2", len(servers))
	}
}

func TestHasSiblings(t *testing.T) {
	if !Tservers[0].hasSiblings(Tservers) {
		t.Fatal("Returned false, expected true")
	}
}
