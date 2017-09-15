// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package gtid

import "testing"

func TestGtid(t *testing.T) {
	gtid := "0-1-100,1-2-101"
	list := NewList(gtid)
	domains := list.GetDomainIDs()
	if domains[0] != 0 || domains[1] != 1 {
		t.Error("Domains should be {0,1}")
	}
	servers := list.GetServerIDs()
	if servers[0] != 1 || servers[1] != 2 {
		t.Error("Servers should be {1,2}")
	}
	seqnos := list.GetSeqNos()
	if seqnos[0] != 100 || seqnos[1] != 101 {
		t.Error("Sequences should be {100,101}")
	}
}

func TestEmptyGtid(t *testing.T) {
	gtid := ""
	list := NewList(gtid)
	if len(*list) != 0 {
		t.Error("Expected empty Gtid List slice")
	}
}
