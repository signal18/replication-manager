// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package state

import (
	"log"
	"testing"
)

func TestSMWithSLA(t *testing.T) {
	sme := new(StateMachine)
	sme.Init()

	sla := sme.GetSla()
	log.Printf("sla: %v", sla)
	if sla.Firsttime == 0 {
		t.Errorf("Firsttime not set")
	}
}
