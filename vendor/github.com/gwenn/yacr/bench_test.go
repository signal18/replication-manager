// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yacr_test

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"
	"testing"

	. "github.com/gwenn/yacr"
)

func BenchmarkParsing(b *testing.B) {
	benchmarkParsing(b, "aaaaaaaa,b b b b b b b,cc cc cc cc cc, ddddd ddd\n", false)
}
func BenchmarkQuotedParsing(b *testing.B) {
	benchmarkParsing(b, "aaaaaaaa,b b b b b b b,\"cc cc cc,cc\",cc, ddddd ddd\n", true)
}
func BenchmarkEmbeddedNL(b *testing.B) {
	benchmarkParsing(b, "aaaaaaaa,b b b b b b b,\"fo \n oo\",\"c oh c yes c \", ddddd ddd\n", true)
}

func benchmarkParsing(b *testing.B, s string, quoted bool) {
	b.StopTimer()
	str := strings.Repeat(s, 2000)
	b.SetBytes(int64(len(str)))
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		r := NewReader(strings.NewReader(str), ',', quoted, false)
		nb := 0
		for r.Scan() {
			if r.EndOfRecord() {
				nb++
			}
		}
		if err := r.Err(); err != nil {
			b.Fatal(err)
		}
		if nb != 2000 {
			b.Fatalf("wrong # rows: %d; want %d", nb, 2000)
		}
	}
}

func BenchmarkStdParser(b *testing.B) {
	b.StopTimer()
	s := strings.Repeat("aaaaaaaa,b b b b b b b,\"fo \n oo\",\"c oh c yes c \", ddddd ddd\n", 2000)
	b.SetBytes(int64(len(s)))
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		r := csv.NewReader(strings.NewReader(s))
		//r.TrailingComma = true
		nb := 0
		for {
			_, err := r.Read()
			if err != nil {
				if err != io.EOF {
					b.Fatal(err)
				}
				break
			}
			nb++
		}
		if nb != 2000 {
			b.Fatalf("wrong # rows: %d; want %d", nb, 2000)
		}
	}
}

func BenchmarkYacrParser(b *testing.B) {
	b.StopTimer()
	s := strings.Repeat("aaaaaaaa,b b b b b b b,\"fo \n oo\",\"c oh c yes c \", ddddd ddd\n", 2000)
	b.SetBytes(int64(len(s)))
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		r := DefaultReader(strings.NewReader(s))
		nb := 0
		for r.Scan() {
			if r.EndOfRecord() {
				nb++
			}
		}
		if err := r.Err(); err != nil {
			b.Fatal(err)
		}
		if nb != 2000 {
			b.Fatalf("wrong # rows: %d; want %d", nb, 2000)
		}
	}
}

func BenchmarkYacrWriter(b *testing.B) {
	b.StopTimer()
	s := strings.Repeat("valu,e1 value2\" value3 valu\ne4 value5", 25)
	row := strings.Fields(s)
	b.SetBytes(int64(len(s)))
	out := &bytes.Buffer{}
	b.StartTimer()
	w := DefaultWriter(out)
	for i := 0; i < b.N; i++ {
		for _, field := range row {
			w.WriteString(field)
		}
		w.EndOfRecord()
		w.Flush()
		if err := w.Err(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStdWriter(b *testing.B) {
	b.StopTimer()
	s := strings.Repeat("valu,e1 value2\" value3 valu\ne4 value5", 25)
	row := strings.Fields(s)
	b.SetBytes(int64(len(s)))
	out := &bytes.Buffer{}
	b.StartTimer()
	w := csv.NewWriter(out)
	for i := 0; i < b.N; i++ {
		w.Write(row)
		w.Flush()
		if err := w.Error(); err != nil {
			b.Fatal(err)
		}
	}
}
