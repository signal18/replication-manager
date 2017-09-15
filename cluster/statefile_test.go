// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"testing"
	"time"
)

func TestStateFile(t *testing.T) {
	sf := newStateFile("/tmp/mrm.state")
	err := sf.access()
	if err != nil {
		t.Fatal("Error creating file: ", err)
	}
	sf.Count = 3
	time := time.Now()
	sf.Timestamp = time.Unix()
	err = sf.write()
	if err != nil {
		t.Fatal("Error writing bytes: ", err)
	}
	err = sf.read()
	if err != nil {
		t.Fatal("Error reading bytes: ", err)
	}
	if sf.Count != 3 {
		t.Fatalf("Read count %d, expected 3", sf.Count)
	}
	t.Log("Read timestamp", sf.Timestamp)
}
