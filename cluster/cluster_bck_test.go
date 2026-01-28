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
		{`"role:primary,critical" "env:prod"`, []string{`"role:primary,critical"`, `"env:prod"`}},
		{`cluster,"env:prod,team:dev"`, []string{`cluster`, `"env:prod,team:dev"`}},
		{`  tag1   tag2  `, []string{`tag1`, `tag2`}},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := splitResticKeepTagTemplates(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("unexpected split result: got %v, want %v", got, tc.expected)
			}
		})
	}
}
