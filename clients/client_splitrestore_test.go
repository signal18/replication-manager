//go:build clients
// +build clients

package clients

import "testing"

func TestSplitMysqlArgs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "simple flags",
			input: "--force --batch",
			want:  []string{"--force", "--batch"},
		},
		{
			name:  "double quoted space",
			input: "--password=\"a b\"",
			want:  []string{"--password=a b"},
		},
		{
			name:  "single quoted space",
			input: "--user='my user'",
			want:  []string{"--user=my user"},
		},
		{
			name:  "escaped spaces",
			input: "--socket=/path\\ with\\ space",
			want:  []string{"--socket=/path with space"},
		},
		{
			name:  "escaped quotes in double quotes",
			input: "--arg=\"a \\\"b\\\" c\"",
			want:  []string{"--arg=a \"b\" c"},
		},
		{
			name:  "empty quoted arg",
			input: "\"\"",
			want:  []string{""},
		},
		{
			name:    "unmatched quote",
			input:   "--user=\"broken",
			wantErr: true,
		},
		{
			name:    "trailing escape",
			input:   "--socket=path\\",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitMysqlArgs(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d args, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("arg %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
