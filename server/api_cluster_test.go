package server

import "testing"

func TestNormalizeCompressionOverride(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{"Auto", "auto", "auto", false},
		{"Empty", "", "auto", false},
		{"True", "true", "true", false},
		{"False", "false", "false", false},
		{"On", "on", "true", false},
		{"Off", "off", "false", false},
		{"One", "1", "true", false},
		{"Zero", "0", "false", false},
		{"Yes", "yes", "true", false},
		{"No", "no", "false", false},
		{"Invalid", "maybe", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeCompressionOverride(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeCompressionOverride(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
