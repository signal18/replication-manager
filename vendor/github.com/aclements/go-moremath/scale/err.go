// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

// RangeErr is an error that indicates some argument or value is out
// of range.
type RangeErr string

func (r RangeErr) Error() string {
	return string(r)
}
