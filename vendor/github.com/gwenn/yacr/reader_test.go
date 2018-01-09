// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yacr_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/gwenn/yacr"
)

func TestLongLine(t *testing.T) {
	content := strings.Repeat("1,2,3,4,5,6,7,8,9,10,", 200)
	r := NewReader(strings.NewReader(content), ',', true, false)
	values := make([]string, 0, 10)
	for r.Scan() {
		values = append(values, r.Text())
		if r.EndOfRecord() {
			break
		}
	}
	err := r.Err()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2001 {
		t.Errorf("got %d value(s) (%#v); want %d", len(values), values, 2001)
	}
}

// Stolen/adapted from $GOROOT/src/pkg/encoding/csv/reader_test.go
var readTests = []struct {
	Name   string
	Input  string
	Output [][]string

	// These fields are copied into the Reader
	Sep     byte
	Quoted  bool
	Lazy    bool
	Guess   byte
	Trim    bool
	Comment byte

	Error  string
	Line   int // Expected error line if != 0
	Column int // Expected error column if line != 0
}{
	{
		Name:   "Simple",
		Input:  "a,b,c\n",
		Output: [][]string{{"a", "b", "c"}},
	},
	{
		Name:   "CRLF",
		Input:  "a,b\r\nc,d\r\n",
		Output: [][]string{{"a", "b"}, {"c", "d"}},
	},
	{
		Name:   "CRLFQuoted",
		Quoted: true,
		Input:  "a,b\r\nc,\"d\"\r\n",
		Output: [][]string{{"a", "b"}, {"c", "d"}},
	},
	{
		Name:   "BareCR",
		Input:  "a,b\rc,d\r\n",
		Output: [][]string{{"a", "b\rc", "d"}},
	},
	{
		Name: "RFC4180test",
		Input: `#field1,field2,field3
"aaa","bb
b","ccc"
"a,a","b""bb","ccc"
zzz,yyy,xxx
`,
		Quoted: true,
		Output: [][]string{
			{"#field1", "field2", "field3"},
			{"aaa", "bb\nb", "ccc"},
			{"a,a", `b"bb`, "ccc"},
			{"zzz", "yyy", "xxx"},
		},
	},
	{
		Name:   "NoEOLTest",
		Input:  "a,b,c",
		Output: [][]string{{"a", "b", "c"}},
	},
	{
		Name:   "Semicolon",
		Sep:    ';',
		Input:  "a;b;c\n",
		Output: [][]string{{"a", "b", "c"}},
	},
	{
		Name: "MultiLine",
		Input: `"two
line","one line","three
line
field"`,
		Quoted: true,
		Output: [][]string{{"two\nline", "one line", "three\nline\nfield"}},
	},
	{
		Name: "EmbeddedNewline",
		Input: `a,"b
b","c

",d`,
		Quoted: true,
		Output: [][]string{{"a", "b\nb", "c\n\n", "d"}},
	},
	{
		Name:   "EscapedQuoteAndEmbeddedNewLine",
		Input:  "\"a\"\"b\",\"c\"\"\r\nd\"",
		Quoted: true,
		Output: [][]string{{"a\"b", "c\"\r\nd"}},
	},
	{
		Name:   "BlankLine",
		Quoted: true,
		Input:  "a,b,\"c\"\n\nd,e,f\n\n",
		Output: [][]string{
			{"a", "b", "c"},
			{"d", "e", "f"},
		},
	},
	{
		Name:   "TrimSpace",
		Input:  " a,  b,   c\n",
		Trim:   true,
		Output: [][]string{{"a", "b", "c"}},
	},
	{
		Name:   "TrimSpaceQuoted",
		Quoted: true,
		Input:  " a,b ,\" c \", d \n",
		Trim:   true,
		Output: [][]string{{"a", "b", " c ", "d"}},
	},
	{
		Name:   "LeadingSpace",
		Input:  " a,  b,   c\n",
		Output: [][]string{{" a", "  b", "   c"}},
	},
	{
		Name:    "Comment",
		Comment: '#',
		Input:   "#1,2,3\na,b,#\n#comment\nc\n# comment",
		Output:  [][]string{{"a", "b", "#"}, {"c"}},
	},
	{
		Name:   "NoComment",
		Input:  "#1,2,3\na,b,c",
		Output: [][]string{{"#1", "2", "3"}, {"a", "b", "c"}},
	},
	{
		Name:   "StrictQuotes",
		Quoted: true,
		Input:  `a "word","1"2",a","b`,
		Output: [][]string{{`a "word"`, `1"2`, `a"`, `b`}},
		Error:  `unescaped " character`, Line: 1, Column: 2,
	},
	{
		Name:   "LazyQuotes", // differs
		Quoted: true,
		Lazy:   true,
		Input:  `a "word","1"2",a","b"`,
		Output: [][]string{{`a "word"`, `1"2`, `a"`, `b`}},
	},
	{
		Name:   "BareQuotes",
		Quoted: true,
		Lazy:   true,
		Input:  `a "word","1"2",a"`,
		Output: [][]string{{`a "word"`, `1"2`, `a"`}},
	},
	{
		Name:   "BareDoubleQuotes",
		Quoted: true,
		Input:  `a""b,c`,
		Output: [][]string{{`a""b`, `c`}},
	},
	{
		Name:   "TrimQuote", // differs
		Quoted: true,
		Input:  ` "a"," b",c`,
		Trim:   true,
		Output: [][]string{{`"a"`, " b", "c"}},
	},
	{
		Name:   "BareQuote", // differs
		Quoted: true,
		Input:  `a "word","b"`,
		Output: [][]string{{`a "word"`, "b"}},
	},
	{
		Name:   "TrailingQuote", // differs
		Quoted: true,
		Input:  `"a word",b"`,
		Output: [][]string{{"a word", `b"`}},
	},
	{
		Name:   "ExtraneousQuote", // differs
		Quoted: true,
		Input:  `"a "word","b"`,
		Error:  `unescaped " character`, Line: 1, Column: 1,
	},
	{
		Name:   "FieldCount",
		Input:  "a,b,c\nd,e",
		Output: [][]string{{"a", "b", "c"}, {"d", "e"}},
	},
	{
		Name:   "TrailingCommaEOF",
		Input:  "a,b,c,",
		Output: [][]string{{"a", "b", "c", ""}},
	},
	{
		Name:   "TrailingCommaEOL",
		Input:  "a,b,c,\n",
		Output: [][]string{{"a", "b", "c", ""}},
	},
	{
		Name:   "TrailingCommaSpaceEOF",
		Trim:   true,
		Input:  "a,b,c, ",
		Output: [][]string{{"a", "b", "c", ""}},
	},
	{
		Name:   "TrailingCommaSpaceEOL",
		Trim:   true,
		Input:  "a,b,c, \n",
		Output: [][]string{{"a", "b", "c", ""}},
	},
	{
		Name:   "TrailingCommaLine3",
		Trim:   true,
		Input:  "a,b,c\nd,e,f\ng,hi,",
		Output: [][]string{{"a", "b", "c"}, {"d", "e", "f"}, {"g", "hi", ""}},
	},
	{
		Name:   "NotTrailingComma3",
		Input:  "a,b,c, \n",
		Output: [][]string{{"a", "b", "c", " "}},
	},
	{
		Name:   "CommaFieldTest",
		Quoted: true,
		Input: `x,y,z,w
x,y,z,
x,y,,
x,,,
,,,
"x","y","z","w"
"x","y","z",""
"x","y","",""
"x","","",""
"","","",""
`,
		Output: [][]string{
			{"x", "y", "z", "w"},
			{"x", "y", "z", ""},
			{"x", "y", "", ""},
			{"x", "", "", ""},
			{"", "", "", ""},
			{"x", "y", "z", "w"},
			{"x", "y", "z", ""},
			{"x", "y", "", ""},
			{"x", "", "", ""},
			{"", "", "", ""},
		},
	},
	{
		Name:  "TrailingCommaIneffective1",
		Input: "a,b,\nc,d,e",
		Output: [][]string{
			{"a", "b", ""},
			{"c", "d", "e"},
		},
	},
	{
		Name:   "Guess",
		Guess:  ';',
		Input:  "a,b;c\td:e|f;g",
		Output: [][]string{{"a,b", "c\td:e|f", "g"}},
	},
	{
		Name:   "6287",
		Input:  `Field1,Field2,"LazyQuotes" Field3,Field4,Field5`,
		Output: [][]string{{"Field1", "Field2", "\"LazyQuotes\" Field3", "Field4", "Field5"}},
	},
	{
		Name:   "6258",
		Quoted: true,
		Input:  `"Field1","Field2 "LazyQuotes"","Field3","Field4"`,
		Output: [][]string{{"Field1", "Field2 \"LazyQuotes\"", "Field3", "Field4"}},
		Error:  `unescaped " character`, Line: 1, Column: 2,
	},
	{
		Name: "3150",
		Sep:  '\t',
		Input: `3376027	”S” Falls	"S" Falls		4.53333`,
		Output: [][]string{{"3376027", `”S” Falls`, `"S" Falls`, "", "4.53333"}},
	},
	//
}

func TestRead(t *testing.T) {
	for _, tt := range readTests {
		var sep byte = ','
		if tt.Sep != 0 {
			sep = tt.Sep
		}
		r := NewReader(strings.NewReader(tt.Input), sep, tt.Quoted, tt.Guess != 0)
		r.Comment = tt.Comment
		r.Trim = tt.Trim
		r.Lazy = tt.Lazy

		i, j := 0, 0
		for r.Scan() {
			if j == 0 && r.EndOfRecord() && len(r.Bytes()) == 0 { // skip empty lines
				continue
			}
			if i >= len(tt.Output) {
				t.Errorf("%s: unexpected number of row %d; want %d max", tt.Name, i+1, len(tt.Output))
				break
			} else if j >= len(tt.Output[i]) {
				t.Errorf("%s: unexpected number of column %d; want %d at line %d", tt.Name, j+1, len(tt.Output[i]), i+1)
				break
			}
			if r.Text() != tt.Output[i][j] {
				t.Errorf("%s: unexpected value %s; want %s at line %d, column %d", tt.Name, r.Text(), tt.Output[i][j], i+1, j+1)
			}
			if r.EndOfRecord() {
				j = 0
				i++
			} else {
				j++
			}
		}
		err := r.Err()
		if tt.Error != "" {
			if err == nil || !strings.Contains(err.Error(), tt.Error) {
				t.Errorf("%s: error %v, want error %q", tt.Name, err, tt.Error)
			} else if tt.Line != 0 && (tt.Line != r.LineNumber() || tt.Column != j+1) {
				t.Errorf("%s: error at %d:%d expected %d:%d", tt.Name, r.LineNumber(), j+1, tt.Line, tt.Column)
			}
		} else if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
		} else if i != len(tt.Output) {
			t.Errorf("%s: unexpected number of row %d; want %d", tt.Name, i, len(tt.Output))
		}
		if tt.Guess != 0 && tt.Guess != r.Sep() {
			t.Errorf("%s: got '%c'; want '%c'", tt.Name, r.Sep(), tt.Guess)
		}
	}
}

func TestScanRecord(t *testing.T) {
	for _, tt := range readTests {
		var sep byte = ','
		if tt.Sep != 0 {
			sep = tt.Sep
		}
		r := NewReader(strings.NewReader(tt.Input), sep, tt.Quoted, tt.Guess != 0)
		r.Comment = tt.Comment
		r.Trim = tt.Trim
		r.Lazy = tt.Lazy

		values := make([]string, 5)
		i, j := 0, 0
		var err error
		for {
			if j, err = r.ScanRecord(&values[0], &values[1], &values[2], &values[3], &values[4]); err != nil || j == 0 {
				break
			}
			if i >= len(tt.Output) {
				t.Errorf("%s: unexpected number of row %d; want %d max", tt.Name, i+1, len(tt.Output))
				break
			} else if j != len(tt.Output[i]) {
				t.Errorf("%s: unexpected number of column %d; want %d at line %d", tt.Name, j, len(tt.Output[i]), i+1)
				break
			}
			for k, value := range values[0:j] {
				if value != tt.Output[i][k] {
					t.Errorf("%s: unexpected value: %s; want: %s at line %d, column %d", tt.Name, r.Text(), tt.Output[i][j], i+1, k+1)
				}
			}
			i++
		}
		if tt.Error != "" {
			if err == nil || !strings.Contains(err.Error(), tt.Error) {
				t.Errorf("%s: error %v, want error %q", tt.Name, err, tt.Error)
			} else if tt.Line != 0 && tt.Line != r.LineNumber() {
				t.Errorf("%s: error at %d expected %d:%d", tt.Name, r.LineNumber(), tt.Line, tt.Column)
			}
		} else if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
		} else if i != len(tt.Output) {
			t.Errorf("%s: unexpected number of row %d; want %d", tt.Name, i, len(tt.Output))
		}
		if tt.Guess != 0 && tt.Guess != r.Sep() {
			t.Errorf("%s: got '%c'; want '%c'", tt.Name, r.Sep(), tt.Guess)
		}
	}
}

func TestScanTypedRecord(t *testing.T) {
	r := DefaultReader(strings.NewReader(",nil,123,3.14,1970-01-01T00:00:00Z\n"))
	var str string
	var i int
	var f float64
	var d time.Time
	n, err := r.ScanRecord(nil, &str, &i, &f, &d)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("want %d, got %d", 5, n)
	}
	if str != "nil" {
		t.Errorf("want %s, got %s", "nil", str)
	}
	if i != 123 {
		t.Errorf("want %d, got %d", 123, i)
	}
	if f != 3.14 {
		t.Errorf("want %f, got %f", 3.14, f)
	}
	if d != time.Unix(0, 0).UTC() {
		t.Errorf("want %v, got %v", time.Unix(0, 0).UTC(), d)
	}
}

var recordTests = []struct {
	Name  string
	Input string
	N     int
}{
	{
		Name:  "Too short line",
		Input: "a,b,c\n",
		N:     3,
	},
	{
		Name:  "Good line",
		Input: "a,b,c,d\n",
		N:     4,
	},
	{
		Name:  "Too long line",
		Input: "a,b,c,d,e\n",
		N:     5,
	},
}

func TestScanRecordCount(t *testing.T) {
	for _, tt := range recordTests {
		r := DefaultReader(strings.NewReader(tt.Input))
		n, err := r.ScanRecord(nil, nil, nil, nil)
		if err != nil {
			t.Errorf("%s: error: %q", tt.Name, err)
		}
		if n != tt.N {
			t.Errorf("%s: want %d, got %d", tt.Name, tt.N, n)
		}
	}
}

var skipTests = []struct {
	Name    string
	Input   string
	Output  []string
	N       int
	Comment byte
}{
	{
		Name:   "SingleLine",
		Input:  "a,b,c\n",
		N:      1,
		Output: []string{},
	},
	{
		Name:   "Empty",
		Input:  "",
		N:      1,
		Output: []string{},
	},
	{
		Name:   "TwoLines",
		Input:  "a,b,c\nd,e\n",
		N:      1,
		Output: []string{"d", "e"},
	},
	{
		Name:    "Comment",
		Input:   "#a,b,c\nd,e\n",
		N:       1,
		Comment: '#',
		Output:  []string{},
	},
}

func TestSkipRecords(t *testing.T) {
	for _, tt := range skipTests {
		r := DefaultReader(strings.NewReader(tt.Input))
		r.Comment = tt.Comment

		var err error
		if err = r.SkipRecords(tt.N); err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
		}

		values := make([]string, 2)
		j := 0
		if j, err = r.ScanRecord(&values[0], &values[1]); err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
			continue
		}
		if j != len(tt.Output) {
			t.Errorf("%s: unexpected number of column %d; want %d", tt.Name, j, len(tt.Output))
			continue
		}
		for k, value := range values[0:j] {
			if value != tt.Output[k] {
				t.Errorf("%s: unexpected value: %s; want: %s at column %d", tt.Name, r.Text(), tt.Output[j], k+1)
			}
		}
	}
}

var fields = make([]string, 3)

var headerTests = []struct {
	Name    string
	Input   string
	Headers map[string]int
	Args    []interface{}
	Output  []string
}{
	{
		Name:  "Simple",
		Input: "A,B,C\na,b,c\n",
		Headers: map[string]int{
			"A": 1,
			"B": 2,
			"C": 3,
		},
		Args:   []interface{}{"A", &fields[0], "B", &fields[1], "C", &fields[2]},
		Output: []string{"a", "b", "c"},
	},
}

func TestScanRecordByName(t *testing.T) {
	for _, tt := range headerTests {
		r := DefaultReader(strings.NewReader(tt.Input))
		err := r.ScanHeaders()
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
			continue
		}
		if !reflect.DeepEqual(r.Headers, tt.Headers) {
			t.Errorf("%s: unexpected headers: %v; want: %v", tt.Name, r.Headers, tt.Headers)
			continue
		}
		n, err := r.ScanRecordByName(tt.Args...)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.Name, err)
			continue
		}
		if n != len(tt.Output) {
			t.Errorf("%s: unexpected number of column %d; want %d", tt.Name, n, len(tt.Output))
			continue
		}
		if !reflect.DeepEqual(tt.Output, fields) {
			t.Errorf("%s: unexpected values: %v; want: %v", tt.Name, fields, tt.Output)
		}
	}
}

var numberTests = []struct {
	Input  string
	IsNum  bool
	IsReal bool
}{
	{Input: ""},
	{Input: "-"},
	{Input: "+"},
	{Input: "."},
	{Input: "-."},
	{Input: "+."},
	{Input: "-.e0"},
	{Input: "+.e0"},
	{Input: "+e0"},
	{Input: ".e0"},
	{Input: "e0"},
	{Input: "0e"},
	{Input: "0", IsNum: true, IsReal: false},
	{Input: "-0", IsNum: true, IsReal: false},
	{Input: "+0", IsNum: true, IsReal: false},
	{Input: "+0x"},
	{Input: "0.", IsNum: true, IsReal: true},
	{Input: "-0.", IsNum: true, IsReal: true},
	{Input: "+0.", IsNum: true, IsReal: true},
	{Input: "+0.x"},
	{Input: ".0", IsNum: true, IsReal: true},
	{Input: "-.0", IsNum: true, IsReal: true},
	{Input: "+.0", IsNum: true, IsReal: true},
	{Input: "+.0x"},
	{Input: "0e0", IsNum: true, IsReal: true},
	{Input: "0e0x"},
	{Input: "0e-0", IsNum: true, IsReal: true},
	{Input: "0e+0", IsNum: true, IsReal: true},
	{Input: "0e-0."},
	{Input: "0123456789", IsNum: true, IsReal: false},
	{Input: "3.14", IsNum: true, IsReal: true},
	{Input: ".314e1", IsNum: true, IsReal: true},
	{Input: "1e10", IsNum: true, IsReal: true},
	{Input: "1.1."},
	{Input: "1e-"},
}

func TestIsNumber(t *testing.T) {
	for _, tt := range numberTests {
		isNum, isReal := IsNumber([]byte(tt.Input))
		if isNum != tt.IsNum {
			if isNum {
				t.Errorf("%q: is not a number", tt.Input)
			} else {
				t.Errorf("%q: is a number", tt.Input)
			}
		}
		if isReal != tt.IsReal {
			if isReal {
				t.Errorf("%q: is not a real", tt.Input)
			} else {
				t.Errorf("%q: is a real", tt.Input)
			}
		}
		var err error
		if !tt.IsReal {
			_, err = strconv.Atoi(tt.Input)
		} else {
			_, err = strconv.ParseFloat(tt.Input, 64)
		}
		if tt.IsNum && err != nil {
			t.Errorf("%q: unexpected error %s", tt.Input, err)
		} else if !tt.IsNum && err == nil {
			t.Errorf("%q: error expected", tt.Input)
		}
	}
}
