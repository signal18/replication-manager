package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/opensvc"
	ini "gopkg.in/ini.v1"
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

// TestIniRoundTripPreservesHashInValues verifies that values containing "#"
// (e.g. "container#01") survive a parse→patch→write cycle without corruption.
// Without IgnoreInlineComment:true the "#" is treated as an inline comment
// delimiter: "container#01" is parsed as value="container" + comment="01",
// then written back as "# 01\nnetns = container".
func TestIniRoundTripPreservesHashInValues(t *testing.T) {
	const raw = `
[container#db]
image          = mariadb:10.11
netns          = container#01
image_pull_policy = always

[container#jobs]
image = mariadb:10.11
netns = container#01
`
	// Demonstrate the broken behaviour (default options).
	broken, err := ini.Load(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("ini.Load: %v", err)
	}
	brokenVal := broken.Section("container#db").Key("netns").Value()
	if brokenVal == "container#01" {
		t.Log("NOTE: default ini.Load already preserves the value – library behaviour may have changed")
	} else if brokenVal != "container" {
		t.Logf("default ini.Load produced unexpected value %q (expected truncation to \"container\")", brokenVal)
	}

	// Verify the fixed behaviour.
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("ini.LoadSources: %v", err)
	}

	// Simulate the patch step (what OpenSVCUpdateDatabaseServiceConfig does).
	for _, sec := range cfg.Sections() {
		if sec.Name() == "container#db" || sec.Name() == "container#jobs" {
			sec.Key("image_pull_policy").SetValue("always")
		}
	}

	var buf bytes.Buffer
	if _, err := cfg.WriteTo(&buf); err != nil {
		t.Fatalf("cfg.WriteTo: %v", err)
	}
	out := buf.String()

	// The written output must contain the full value, not the truncated one.
	for _, sec := range []string{"container#db", "container#jobs"} {
		cfg2, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("re-parse: %v", err)
		}
		got := cfg2.Section(sec).Key("netns").Value()
		if got != "container#01" {
			t.Errorf("[%s] netns = %q, want \"container#01\"\nfull output:\n%s", sec, got, out)
		}
	}

	// Also assert the raw output string doesn't contain the mangled comment form.
	if strings.Contains(out, "# 01") {
		t.Errorf("output contains mangled comment '# 01':\n%s", out)
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
