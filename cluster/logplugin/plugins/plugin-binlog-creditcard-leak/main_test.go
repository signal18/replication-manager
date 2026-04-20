package main

import "testing"

// ---- luhn -------------------------------------------------------------------

func TestLuhn_ValidNumbers(t *testing.T) {
	// Standard test card numbers (PayPal / Stripe sandbox numbers)
	valid := []string{
		"4532015112830366", // Visa
		"5425233430109903", // Mastercard
		"378282246310005",  // Amex (15 digits)
		"6011111111111117", // Discover
		"4111111111111111", // classic Visa test number
	}
	for _, n := range valid {
		if !luhn(n) {
			t.Errorf("luhn(%q) = false, want true", n)
		}
	}
}

func TestLuhn_InvalidNumbers(t *testing.T) {
	invalid := []string{
		"4532015112830367", // last digit off by one from a valid Visa
		"1234567890123456", // random digits
		"4111111111111112", // one off from classic test number
	}
	for _, n := range invalid {
		if luhn(n) {
			t.Errorf("luhn(%q) = true, want false", n)
		}
	}
}

func TestLuhn_LengthBounds(t *testing.T) {
	// Below minimum (13 digits)
	if luhn("411111111111") {
		t.Error("luhn(12-digit) should be false (< 13)")
	}
	// Above maximum (19 digits)
	if luhn("12345678901234567890") {
		t.Error("luhn(20-digit) should be false (> 19)")
	}
	// Exactly 13 digits — valid Luhn
	if !luhn("4222222222222") {
		t.Error("luhn(13-digit valid) should be true")
	}
}

// ---- maskPAN ----------------------------------------------------------------

func TestMaskPAN(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"4532015112830366", "************0366"},
		{"378282246310005", "***********0005"},
		{"1234", "****"},
		{"12345", "*2345"},
	}
	for _, tc := range cases {
		got := maskPAN(tc.in)
		if got != tc.want {
			t.Errorf("maskPAN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
