// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"testing"
)

func TestKDEOneSample(t *testing.T) {
	x := float64(5)

	// Unweighted, fixed bandwidth
	kde := KDE{
		Sample:    Sample{Xs: []float64{x}},
		Kernel:    GaussianKernel,
		Bandwidth: 1,
	}
	if e, g := StdNormal.PDF(0), kde.PDF(x); !aeq(e, g) {
		t.Errorf("bad PDF value at sample: expected %g, got %g", e, g)
	}
	if e, g := 0.0, kde.PDF(-10000); !aeq(e, g) {
		t.Errorf("bad PDF value at low tail: expected %g, got %g", e, g)
	}
	if e, g := 0.0, kde.PDF(10000); !aeq(e, g) {
		t.Errorf("bad PDF value at high tail: expected %g, got %g", e, g)
	}

	if e, g := 0.5, kde.CDF(x); !aeq(e, g) {
		t.Errorf("bad CDF value at sample: expected %g, got %g", e, g)
	}
	if e, g := 0.0, kde.CDF(-10000); !aeq(e, g) {
		t.Errorf("bad CDF value at low tail: expected %g, got %g", e, g)
	}
	if e, g := 1.0, kde.CDF(10000); !aeq(e, g) {
		t.Errorf("bad CDF value at high tail: expected %g, got %g", e, g)
	}

	low, high := kde.Bounds()
	if e, g := x-2, low; e < g {
		t.Errorf("bad low bound: expected %g, got %g", e, g)
	}
	if e, g := x+2, high; e > g {
		t.Errorf("bad high bound: expected %g, got %g", e, g)
	}

	kde = KDE{
		Sample:    Sample{Xs: []float64{x}},
		Kernel:    EpanechnikovKernel,
		Bandwidth: 2,
	}
	testFunc(t, fmt.Sprintf("%+v.PDF", kde), kde.PDF, map[float64]float64{
		x - 2: 0,
		x - 1: 0.5625 / 2,
		x:     0.75 / 2,
		x + 1: 0.5625 / 2,
		x + 2: 0,
	})
	testFunc(t, fmt.Sprintf("%+v.CDF", kde), kde.CDF, map[float64]float64{
		x - 2: 0,
		x - 1: 0.15625,
		x:     0.5,
		x + 1: 0.84375,
		x + 2: 1,
	})
}

func TestKDETwoSamples(t *testing.T) {
	kde := KDE{
		Sample:    Sample{Xs: []float64{1, 3}},
		Kernel:    GaussianKernel,
		Bandwidth: 2,
	}
	testFunc(t, "PDF", kde.PDF, map[float64]float64{
		0: 0.120395730,
		1: 0.160228251,
		2: 0.176032663,
		3: 0.160228251,
		4: 0.120395730})

	testFunc(t, "CDF", kde.CDF, map[float64]float64{
		0: 0.187672369,
		1: 0.329327626,
		2: 0.5,
		3: 0.670672373,
		4: 0.812327630})
}
