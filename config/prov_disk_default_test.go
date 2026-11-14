package config

import "testing"

// DefaultProvDiskSize is the default of prov-db-disk-size and prov-proxy-disk-size and the
// fallback of every consumer of an unusable size: it must itself be a valid size under the
// measurement those settings carry, and at least one gigabyte.
func TestDefaultProvDiskSizeIsValid(t *testing.T) {
	gb, err := ParseUnitMeasurementToInt("G,bytes,required", DefaultProvDiskSize, true)
	if err != nil {
		t.Fatalf("DefaultProvDiskSize %q is not a valid size: %v", DefaultProvDiskSize, err)
	}
	if gb < 1 {
		t.Fatalf("DefaultProvDiskSize %q resolves to %d gigabytes, want at least 1", DefaultProvDiskSize, gb)
	}
}
