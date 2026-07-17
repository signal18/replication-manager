package wire

import "testing"

func TestMaskIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"window", "wi???ow"},
		{"customer_ssn", "cu???sn"},
		{"cc", "????"},
		{"ssn", "????"},
		{"dob", "????"},
		{"abcd", "????"},
		{"abcde", "ab???de"},
	}
	for _, tc := range cases {
		if got := MaskIdentifier(tc.in); got != tc.want {
			t.Errorf("MaskIdentifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMaskQualified(t *testing.T) {
	got := MaskQualified("payments", "credit_cards", "pan")
	want := "pa???ts.cr???ds.????"
	if got != want {
		t.Errorf("MaskQualified(...) = %q, want %q", got, want)
	}
	if got := MaskQualified("s", "", "t"); got != "????.????" {
		t.Errorf("MaskQualified with empty part = %q, want %q (empty parts skipped)", got, "????.????")
	}
}
