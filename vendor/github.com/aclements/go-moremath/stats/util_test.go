// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"testing"

	"github.com/aclements/go-moremath/internal/mathtest"
	"github.com/aclements/go-moremath/vec"
)

var aeq = mathtest.Aeq
var testFunc = mathtest.WantFunc

func testDiscreteCDF(t *testing.T, name string, dist DiscreteDist) {
	// Build the expected CDF out of the PMF.
	l, h := dist.Bounds()
	s := dist.Step()
	want := map[float64]float64{l - 0.1: 0, h: 1}
	sum := 0.0
	for x := l; x < h; x += s {
		sum += dist.PMF(x)
		want[x] = sum
		want[x+s/2] = sum
	}

	testFunc(t, name, dist.CDF, want)
}

func testInvCDF(t *testing.T, dist Dist, bounded bool) {
	inv := InvCDF(dist)
	name := fmt.Sprintf("InvCDF(%+v)", dist)
	cdfName := fmt.Sprintf("CDF(%+v)", dist)

	// Test bounds.
	vals := map[float64]float64{-0.01: nan, 1.01: nan}
	if !bounded {
		vals[0] = -inf
		vals[1] = inf
	}
	testFunc(t, name, inv, vals)

	if bounded {
		lo, hi := inv(0), inv(1)
		vals := map[float64]float64{
			lo - 0.01: 0, lo: 0,
			hi: 1, hi + 0.01: 1,
		}
		testFunc(t, cdfName, dist.CDF, vals)
		if got := dist.CDF(lo + 0.01); !(got > 0) {
			t.Errorf("%s(0)=%v, but %s(%v)=0", name, lo, cdfName, lo+0.01)
		}
		if got := dist.CDF(hi - 0.01); !(got < 1) {
			t.Errorf("%s(1)=%v, but %s(%v)=1", name, hi, cdfName, hi-0.01)
		}
	}

	// Test points between.
	vals = map[float64]float64{}
	for _, p := range vec.Linspace(0, 1, 11) {
		if p == 0 || p == 1 {
			continue
		}
		x := inv(p)
		vals[x] = x
	}
	testFunc(t, fmt.Sprintf("InvCDF(CDF(%+v))", dist),
		func(x float64) float64 {
			return inv(dist.CDF(x))
		},
		vals)
}
