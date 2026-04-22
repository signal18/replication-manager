package cluster

import (
	"errors"
	"fmt"
	"testing"

	"github.com/signal18/replication-manager/opensvc"
)

func TestIsOpenSVCAlreadyExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "sentinel", err: opensvc.ErrObjectAlreadyExists, want: true},
		{name: "wrapped sentinel", err: fmt.Errorf("wrapped: %w", opensvc.ErrObjectAlreadyExists), want: true},
		{name: "status 409", err: &opensvc.StatusError{StatusCode: 409, Body: "conflict"}, want: true},
		{name: "status 500", err: &opensvc.StatusError{StatusCode: 500, Body: "server error"}, want: false},
		{name: "generic error", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOpenSVCAlreadyExists(tt.err)
			if got != tt.want {
				t.Fatalf("isOpenSVCAlreadyExists(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestNormalizeOpenSVCV3ProvisionDelay(t *testing.T) {
	tests := []struct {
		name  string
		delay int
		want  int
	}{
		{name: "negative uses default", delay: -1, want: defaultOpenSVCV3ProvisionDelay},
		{name: "zero allowed", delay: 0, want: 0},
		{name: "positive unchanged", delay: 3, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOpenSVCV3ProvisionDelay(tt.delay)
			if got != tt.want {
				t.Fatalf("normalizeOpenSVCV3ProvisionDelay(%d) = %d, want %d", tt.delay, got, tt.want)
			}
		})
	}
}

func TestProxySetTemplateMD5SetsBothFields(t *testing.T) {
	p := &Proxy{}
	p.SetTemplateMD5("abc123")

	if p.TemplateMD5Prov != "abc123" {
		t.Fatalf("TemplateMD5Prov = %q, want %q", p.TemplateMD5Prov, "abc123")
	}

	if p.TemplateMD5 != "abc123" {
		t.Fatalf("TemplateMD5 = %q, want %q", p.TemplateMD5, "abc123")
	}
}
