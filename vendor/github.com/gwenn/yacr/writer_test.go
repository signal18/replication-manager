// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yacr_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	. "github.com/gwenn/yacr"
)

func writeRow(w *Writer, row []string) {
	for _, field := range row {
		if !w.WriteString(field) {
			break
		}
	}
	w.EndOfRecord()
}

// Stolen/adapted from $GOROOT/src/pkg/encoding/csv/writer_test.go
var writeTests = []struct {
	Input   [][]string
	Output  string
	UseCRLF bool
}{
	{Input: [][]string{{"abc"}}, Output: "abc\n"},
	{Input: [][]string{{"abc"}}, Output: "abc\r\n", UseCRLF: true},
	{Input: [][]string{{`"abc"`}}, Output: `"""abc"""` + "\n"},
	{Input: [][]string{{`a"b`}}, Output: `"a""b"` + "\n"},
	{Input: [][]string{{`"a"b"`}}, Output: `"""a""b"""` + "\n"},
	{Input: [][]string{{" abc"}}, Output: " abc\n"}, // differs
	{Input: [][]string{{"abc,def"}}, Output: `"abc,def"` + "\n"},
	{Input: [][]string{{"abc", "def"}}, Output: "abc,def\n"},
	{Input: [][]string{{"abc"}, {"def"}}, Output: "abc\ndef\n"},
	{Input: [][]string{{"abc\ndef"}}, Output: "\"abc\ndef\"\n"},
	{Input: [][]string{{"abc\ndef"}}, Output: "\"abc\ndef\"\r\n", UseCRLF: true}, // differs
	{Input: [][]string{{"abc\rdef"}}, Output: "\"abc\rdef\"\r\n", UseCRLF: true}, // differs
	{Input: [][]string{{"abc\rdef"}}, Output: "\"abc\rdef\"\n", UseCRLF: false},
	{Input: [][]string{{"a", "b,\n", "c\"d"}}, Output: "a,\"b,\n\",\"c\"\"d\"\n"},
	{Input: [][]string{{"à", "é", "è", "ù"}}, Output: "à,é,è,ù\n"},
}

func TestWrite(t *testing.T) {
	for n, tt := range writeTests {
		b := &bytes.Buffer{}
		f := DefaultWriter(b)
		f.UseCRLF = tt.UseCRLF
		for _, row := range tt.Input {
			writeRow(f, row)
		}
		f.Flush()
		err := f.Err()
		if err != nil {
			t.Errorf("Unexpected error: %s\n", err)
		}
		out := b.String()
		if out != tt.Output {
			t.Errorf("#%d: out=%q want %q", n, out, tt.Output)
		}
	}
}

type errorWriter struct{}

func (e errorWriter) Write(b []byte) (int, error) {
	return 0, errors.New("Test")
}

func TestError(t *testing.T) {
	b := &bytes.Buffer{}
	f := DefaultWriter(b)
	writeRow(f, []string{"abc"})
	f.Flush()
	err := f.Err()

	if err != nil {
		t.Errorf("Unexpected error: %s\n", err)
	}

	f = DefaultWriter(errorWriter{})
	writeRow(f, []string{"abc"})
	f.Flush()
	err = f.Err()

	if err == nil {
		t.Error("Error should not be nil")
	}
}

var writeRecordTests = []struct {
	Input  []interface{}
	Output string
}{
	{Input: []interface{}{"abc"}, Output: "abc\n"},
	{Input: []interface{}{nil, "nil", 123, 3.14, time.Unix(0, 0).UTC()}, Output: ",nil,123,3.14,1970-01-01T00:00:00Z\n"},
}

func TestWriteRecord(t *testing.T) {
	for n, tt := range writeRecordTests {
		b := &bytes.Buffer{}
		w := DefaultWriter(b)
		w.WriteRecord(tt.Input...)
		w.Flush()
		err := w.Err()
		if err != nil {
			t.Errorf("Unexpected error: %s\n", err)
		}
		out := b.String()
		if out != tt.Output {
			t.Errorf("#%d: out=%q want %q", n, out, tt.Output)
		}
	}
}
