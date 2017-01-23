// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"testing"
)

type funnyCDF struct {
	left float64
}

func (f funnyCDF) CDF(x float64) float64 {
	switch {
	case x < f.left:
		return 0
	case x < f.left+1:
		return (x - f.left) / 2
	case x < f.left+2:
		return 0.5
	case x < f.left+3:
		return (x-f.left-2)/2 + 0.5
	default:
		return 1
	}
}

func (f funnyCDF) Bounds() (float64, float64) {
	return f.left, f.left + 3
}

func TestInvCDF(t *testing.T) {
	for _, f := range []funnyCDF{funnyCDF{1}, funnyCDF{-1.5}, funnyCDF{-4}} {
		testFunc(t, fmt.Sprintf("InvCDF(funnyCDF%+v)", f), InvCDF(f),
			map[float64]float64{
				-0.1: nan,
				0:    f.left,
				0.25: f.left + 0.5,
				0.5:  f.left + 1,
				0.75: f.left + 2.5,
				1:    f.left + 3,
				1.1:  nan,
			})
	}
}
