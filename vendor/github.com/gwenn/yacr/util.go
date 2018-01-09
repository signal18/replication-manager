// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yacr

import (
	"compress/bzip2"
	"compress/gzip"
	"io"
	"io/ioutil"
	"os"
	"path"
)

type zReadCloser struct {
	f  *os.File
	rd io.ReadCloser
}

// Zopen transparently opens gzip/bzip files (based on their extension).
func Zopen(filepath string) (io.ReadCloser, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	var rd io.ReadCloser
	// TODO zip
	ext := path.Ext(f.Name())
	if ext == ".gz" {
		rd, err = gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
	} else if ext == ".bz2" {
		rd = ioutil.NopCloser(bzip2.NewReader(f))
	} else {
		rd = f
	}
	return &zReadCloser{f, rd}, nil
}
func (z *zReadCloser) Read(b []byte) (n int, err error) {
	return z.rd.Read(b)
}
func (z *zReadCloser) Close() (err error) {
	err = z.rd.Close()
	if err != nil {
		_ = z.f.Close()
		return
	}
	return z.f.Close()
}
