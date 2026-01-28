package cluster

import (
	"reflect"
	"testing"
)

func TestSplitResticKeepTagTemplates(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{``, []string{}},
		{`   `, []string{}},
		{`"role:primary,critical" "env:prod"`, []string{`"role:primary,critical"`, `"env:prod"`}},
		{`cluster,"env:prod,team:dev"`, []string{`cluster`, `"env:prod,team:dev"`}},
		{`  tag1   tag2  `, []string{`tag1`, `tag2`}},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, hadUnmatched := splitResticKeepTagTemplates(tc.input)
			if hadUnmatched {
				t.Fatalf("unexpected unmatched quotes for input %q", tc.input)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("unexpected split result: got %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSplitResticKeepTagTemplates_UnmatchedQuotes(t *testing.T) {
	got, hadUnmatched := splitResticKeepTagTemplates(`"role:primary,critical env:prod`)
	if !hadUnmatched {
		t.Fatalf("expected unmatched quotes to be detected")
	}
	if len(got) != 0 {
		t.Fatalf("expected unmatched token to be dropped, got %v", got)
	}
}

func TestValidateResticKeepTagTemplatesStrict(t *testing.T) {
	if err := validateResticKeepTagTemplatesStrict(`"role:primary,critical env:prod`); err == nil {
		t.Fatalf("expected error for unmatched quotes")
	}
	if err := validateResticKeepTagTemplatesStrict(`line:adhoc env:prod`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
