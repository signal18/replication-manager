package server

import "testing"

func TestRequiresGlobalSettingsForAppSetting(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		value   string
		want    bool
	}{
		{"memory is restricted", "prov-app-memory", "4096", true},
		{"disk size is restricted", "prov-app-disk-size", "10", true},
		{"cpu cores is restricted", "prov-app-cpu-cores", "2", true},
		{"switching to manual is restricted", "prov-app-sizing-mode", "manual", true},
		{"switching to unit is not restricted", "prov-app-sizing-mode", "unit", false},
		{"credit planned is not restricted", "prov-app-credit-planned", "8", false},
		{"agents are not restricted", "prov-app-agents", "node1,node2", false},
		{"docker image is not restricted", "prov-app-docker-img", "mysql:8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresGlobalSettingsForAppSetting(tt.setting, tt.value); got != tt.want {
				t.Fatalf("requiresGlobalSettingsForAppSetting(%q, %q) = %v, want %v", tt.setting, tt.value, got, tt.want)
			}
		})
	}
}
