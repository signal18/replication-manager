package config

import (
	"testing"
)

func TestParseUnitMeasurementToInt_Memory(t *testing.T) {
	// Memory base unit is M (megabytes)
	tag := "M,bytes,required"

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"bare number treated as M", "256", 256, false},
		{"explicit M suffix", "256M", 256, false},
		{"lowercase m suffix", "256m", 256, false},
		{"G suffix converts to M", "1G", 1024, false},
		{"G suffix larger value", "4G", 4096, false},
		{"MB suffix", "256MB", 256, false},
		{"GB suffix", "1GB", 1024, false},
		{"empty required value", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUnitMeasurementToInt(tag, tt.input, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseUnitMeasurementToInt(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseUnitMeasurementToInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseUnitMeasurementToInt_Disk(t *testing.T) {
	// Disk base unit is G (gigabytes)
	tag := "G,bytes,required"

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"bare number treated as G", "20", 20, false},
		{"explicit G suffix", "20G", 20, false},
		{"lowercase g suffix", "20g", 20, false},
		{"T suffix converts to G", "1T", 1024, false},
		{"GB suffix", "100GB", 100, false},
		{"M suffix rejected below base", "256M", 0, true},
		{"empty required value", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUnitMeasurementToInt(tag, tt.input, true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseUnitMeasurementToInt(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseUnitMeasurementToInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
