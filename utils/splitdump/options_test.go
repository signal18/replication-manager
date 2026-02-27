package splitdump

import "testing"

func TestParseSizeBytes(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "empty", input: "", want: 0},
		{name: "bytes", input: "42", want: 42},
		{name: "zero", input: "0", want: 0},
		{name: "kilo", input: "16K", want: 16 * 1000},
		{name: "kilo-bytes", input: "16KB", want: 16 * 1000},
		{name: "mega", input: "16M", want: 16 * 1000 * 1000},
		{name: "mega-bytes", input: "16MB", want: 16 * 1000 * 1000},
		{name: "giga", input: "1G", want: 1 * 1000 * 1000 * 1000},
		{name: "giga-bytes", input: "1GB", want: 1 * 1000 * 1000 * 1000},
		{name: "space-before-suffix", input: "16 MB", want: 16 * 1000 * 1000},
		{name: "kibi", input: "16KiB", want: 16 * 1024},
		{name: "mebi", input: "16MiB", want: 16 * 1024 * 1024},
		{name: "gibi", input: "1GiB", want: 1 * 1024 * 1024 * 1024},
		{name: "invalid-suffix", input: "5TB", wantErr: true},
		{name: "invalid-number", input: "MiB", wantErr: true},
		{name: "invalid-decimal", input: "1.5G", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSizeBytes(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSizeBytes(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeSplitDumpOptionsRejectsNegativeWhenSet(t *testing.T) {
	_, err := normalizeSplitDumpOptions(SplitDumpOptions{StreamSizeMax: -1, StreamSizeMaxSet: true})
	if err == nil {
		t.Fatal("expected error for negative StreamSizeMax when set")
	}
}
