package trigram

import (
	"reflect"
	"testing"
)

func mktri(s string) T { return T(uint32(s[0])<<16 | uint32(s[1])<<8 | uint32(s[2])) }

func mktris(ss ...string) []T {
	var ts []T
	for _, s := range ss {
		ts = append(ts, mktri(s))
	}
	return ts
}

func TestExtract(t *testing.T) {

	tests := []struct {
		s    string
		want []T
	}{
		{"", nil},
		{"a", nil},
		{"ab", nil},
		{"abc", mktris("abc")},
		{"abcabc", mktris("abc", "bca", "cab")},
		{"abcd", mktris("abc", "bcd")},
	}

	for _, tt := range tests {
		if got := Extract(tt.s, nil); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Extract(%q)=%+v, want %+v", tt.s, got, tt.want)
		}
	}
}

func TestQuery(t *testing.T) {

	s := []string{
		"foo",
		"foobar",
		"foobfoo",
		"quxzoot",
		"zotzot",
		"azotfoba",
	}

	idx := NewIndex(s)

	tests := []struct {
		q   string
		ids []DocID
	}{
		{"", []DocID{0, 1, 2, 3, 4, 5}},
		{"foo", []DocID{0, 1, 2}},
		{"foob", []DocID{1, 2}},
		{"zot", []DocID{4, 5}},
		{"oba", []DocID{1, 5}},
	}

	for _, tt := range tests {
		if got := idx.Query(tt.q); !reflect.DeepEqual(got, tt.ids) {
			t.Errorf("Query(%q)=%+v, want %+v", tt.q, got, tt.ids)
		}
	}

	idx.Add("zlot")
	docs := idx.Query("lot")
	if len(docs) != 1 || docs[0] != 6 {
		t.Errorf("Query(`lot`)=%+v, want []DocID{6}", docs)
	}

	idx.Delete("foobar", 1)
	docs = idx.Query("fooba")
	if len(docs) != 0 {
		t.Errorf("Query(`fooba`)=%+v, want []DocID{}", docs)
	}
}

func TestFullPrune(t *testing.T) {

	s := []string{
		"foo",
		"foobar",
		"foobfoo",
		"quxzoot",
		"zotzot",
		"azotfoba",
	}

	idx := NewIndex(s)
	idx.Prune(0)

	tests := []struct {
		q   string
		ids []DocID
	}{
		{"", []DocID{0, 1, 2, 3, 4, 5}},
		{"foo", []DocID{0, 1, 2, 3, 4, 5}},
		{"foob", []DocID{0, 1, 2, 3, 4, 5}},
		{"zot", []DocID{0, 1, 2, 3, 4, 5}},
		{"oba", []DocID{0, 1, 2, 3, 4, 5}},
	}

	for _, tt := range tests {
		if got := idx.Query(tt.q); !reflect.DeepEqual(got, tt.ids) {
			t.Errorf("Query(%q)=%+v, want %+v", tt.q, got, tt.ids)
		}
	}

	idx.Add("ahafoo")
	tests = []struct {
		q   string
		ids []DocID
	}{
		{"", []DocID{0, 1, 2, 3, 4, 5, 6}},
		{"foo", []DocID{0, 1, 2, 3, 4, 5, 6}},
		{"foob", []DocID{0, 1, 2, 3, 4, 5, 6}},
		{"zot", []DocID{0, 1, 2, 3, 4, 5, 6}},
		{"oba", []DocID{0, 1, 2, 3, 4, 5, 6}},
	}

	for _, tt := range tests {
		if got := idx.Query(tt.q); !reflect.DeepEqual(got, tt.ids) {
			t.Errorf("Query(%q)=%+v, want %+v", tt.q, got, tt.ids)
		}
	}
}
