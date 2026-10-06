// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestEffectiveMonitorEventStatusBounds(t *testing.T) {
	tests := []struct {
		name       string
		value      int
		wantEvents int
		wantBytes  int
	}{
		{name: "configured", value: 42, wantEvents: 42, wantBytes: 42},
		{name: "zero falls back", value: 0, wantEvents: DefaultMonitorEventStatusMaxDefinitions, wantBytes: DefaultMonitorEventStatusMaxDefinitionBytes},
		{name: "negative falls back", value: -1, wantEvents: DefaultMonitorEventStatusMaxDefinitions, wantBytes: DefaultMonitorEventStatusMaxDefinitionBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveMonitorEventStatusMaxDefinitions(tt.value); got != tt.wantEvents {
				t.Fatalf("EffectiveMonitorEventStatusMaxDefinitions(%d) = %d, want %d", tt.value, got, tt.wantEvents)
			}
			if got := EffectiveMonitorEventStatusMaxDefinitionBytes(tt.value); got != tt.wantBytes {
				t.Fatalf("EffectiveMonitorEventStatusMaxDefinitionBytes(%d) = %d, want %d", tt.value, got, tt.wantBytes)
			}
		})
	}
}
