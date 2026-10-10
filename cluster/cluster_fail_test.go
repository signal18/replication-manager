// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

func TestChangeMasterStrategyFor(t *testing.T) {
	tests := []struct {
		name    string
		v       *version.Version
		hasGTID bool
		want    changeMasterStrategy
	}{
		{"MySQL 5.6 without GTID", &version.Version{Flavor: "MySQL", Major: 5, Minor: 6}, false, changeMasterPositional},
		{"Percona 5.6 without GTID", &version.Version{Flavor: "Percona", Major: 5, Minor: 6}, false, changeMasterPositional},
		{"MySQL 5.5 without GTID", &version.Version{Flavor: "MySQL", Major: 5, Minor: 5}, false, changeMasterPositional},
		{"MySQL 5.7 with GTID", &version.Version{Flavor: "MySQL", Major: 5, Minor: 7}, true, changeMasterAutoPosition},
		{"Percona 8.0 with GTID", &version.Version{Flavor: "Percona", Major: 8, Minor: 0}, true, changeMasterAutoPosition},
		{"MySQL 5.6 with GTID reported", &version.Version{Flavor: "MySQL", Major: 5, Minor: 6}, true, changeMasterAutoPosition},
		{"MariaDB 10.3", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 3}, false, changeMasterSlavePos},
		{"MariaDB with GTID", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 3}, true, changeMasterSlavePos},
		{"nil version", nil, false, changeMasterSlavePos},
		{"nil version with GTID reported", nil, true, changeMasterSlavePos},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changeMasterStrategyFor(tt.v, tt.hasGTID)
			if got != tt.want {
				t.Fatalf("changeMasterStrategyFor(%+v, %v) = %v, want %v", tt.v, tt.hasGTID, got, tt.want)
			}
		})
	}
}
