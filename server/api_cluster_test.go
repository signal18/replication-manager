package server

import (
	"net/url"
	"testing"
)

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

func TestValidateResticPurgePathList(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"Empty", "", false},
		{"Whitespace", "  \t\n ", false},
		{"SingleAbsolute", "/var/lib/mysql", false},
		{"MultipleAbsolute", "/var/lib/mysql, /data /srv", false},
		{"Relative", "data", true},
		{"Mixed", "/var/lib/mysql, data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticPurgePathList(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
		})
	}
}

func TestValidateResticSizeValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"Empty", "", false},
		{"NumberOnly", "1024", false},
		{"UpperSuffix", "1G", false},
		{"LowerSuffix", "500m", false},
		{"SuffixWithB", "2TB", false},
		{"InvalidSuffix", "1Z", true},
		{"InvalidFormat", "1.5G", true},
		{"Whitespace", " 1G ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResticSizeValue(tt.value, "backup-restic-purge-prune-max-unused")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.value, err)
			}
		})
	}
}

func TestShouldDownloadFromQuery(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"Empty", "", true},
		{"False", "false", false},
		{"Zero", "0", false},
		{"No", "no", false},
		{"True", "true", true},
		{"Yes", "yes", true},
		{"Other", "maybe", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := url.Values{}
			if tt.value != "" {
				values.Set("download", tt.value)
			}
			if got := shouldDownloadFromQuery(values); got != tt.want {
				t.Fatalf("shouldDownloadFromQuery(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
