// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package csv

import (
	"bytes"
	"io/ioutil"

	"github.com/gwenn/yacr"
)

func Fuzz(data []byte) int {
	r := yacr.DefaultReader(bytes.NewReader(data))
	for r.Scan() {
		r.Text()
		if r.EndOfRecord() {
			break
		}
	}
	err := r.Err()
	if err != nil {
		return 0
	}

	// Double quotes are not preserved when not strictly needed
	w := yacr.DefaultWriter(ioutil.Discard)
	w.Write(data)
	err = w.Err()
	if err != nil {
		return 0
	}
	return 1
}
