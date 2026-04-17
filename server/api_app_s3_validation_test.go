package server

import "testing"

func TestValidateCustomEndpointCredentialPair_BothBlankAllowed(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("", ""); err != nil {
		t.Fatalf("expected blank pair to be allowed, got error: %v", err)
	}
}

func TestValidateCustomEndpointCredentialPair_BothSetAllowed(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("AKIA", "SECRET"); err != nil {
		t.Fatalf("expected complete pair to be allowed, got error: %v", err)
	}
}

func TestValidateCustomEndpointCredentialPair_RejectsPartialPair(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("AKIA", ""); err == nil {
		t.Fatal("expected error for partial pair with missing secretkey")
	}
	if err := validateCustomEndpointCredentialPair("", "SECRET"); err == nil {
		t.Fatal("expected error for partial pair with missing accesskey")
	}
}
