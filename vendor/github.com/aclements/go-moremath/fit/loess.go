// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fit

import (
	"math"
	"sort"
)

// LOESS computes the locally-weighted least squares polynomial
// regression to the data (xs[i], ys[i]). 0 < span <= 1 is the
// smoothing parameter, where smaller values fit the data more
// tightly. Degree is typically 2 and span is typically between 0.5
// and 0.75.
//
// The regression is "local" because the weights used for the
// polynomial regression depend on the x at which the regression
// function is evaluated. The weight of observation i is
// W((x-xs[i])/d(x)) where d(x) is the distance from x to the
// span*len(xs)'th closest point to x and W is the tricube weight
// function W(u) = (1-|u|³)³ for |u| < 1, 0 otherwise. One consequence
// of this is that only the span*len(xs) points closest to x affect
// the regression at x, and that the effect of these points falls off
// further from x.
//
// References
//
// Cleveland, William S., and Susan J. Devlin. "Locally weighted
// regression: an approach to regression analysis by local fitting."
// Journal of the American Statistical Association 83.403 (1988):
// 596-610.
//
// http://www.itl.nist.gov/div898/handbook/pmd/section1/dep/dep144.htm
func LOESS(xs, ys []float64, degree int, span float64) func(x float64) float64 {
	if degree < 0 {
		panic("degree must be non-negative")
	}
	if span <= 0 {
		panic("span must be positive")
	}

	// q is the window width in data points.
	q := int(math.Ceil(span * float64(len(xs))))
	if q >= len(xs) {
		q = len(xs)
	}

	// Sort xs.
	if !sort.Float64sAreSorted(xs) {
		xs = append([]float64(nil), xs...)
		ys = append([]float64(nil), ys...)
		sort.Sort(&pairSlice{xs, ys})
	}

	return func(x float64) float64 {
		// Find the q points closest to x.
		n := 0
		if len(xs) > q {
			n = sort.Search(len(xs)-q, func(i int) bool {
				// The cut-off between xs[i:i+q] and
				// xs[i+1:i+1+q] is avg(xs[i],
				// xs[i+q]).
				return (xs[i] + xs[i+q]) >= x*2
			})
		}
		closest := xs[n : n+q]

		// Compute the distance to the q'th farthest point.
		// This will be either the first or last point in
		// closest.
		d := x - closest[0]
		if closest[q-1]-x > d {
			d = closest[q-1] - x
		}

		// Compute the weights.
		weights := make([]float64, q)
		for i, c := range closest {
			// u is the normalized distance from x to
			// closest[i].
			u := math.Abs(x-c) / d
			// Compute the tricube weight (1-|u|³)³ for
			// |u| < 1. We know 0 <= u <= 1, so we can
			// simplify this a bit.
			tmp := 1 - u*u*u
			weights[i] = tmp * tmp * tmp
		}

		// Compute the polynomial regression at x.
		pr := PolynomialRegression(closest, ys[n:n+q], weights, degree)

		// Evaluate the polynomial at x.
		return pr.F(x)
	}
}

type pairSlice struct {
	xs, ys []float64
}

func (s *pairSlice) Len() int {
	return len(s.xs)
}

func (s *pairSlice) Less(i, j int) bool {
	return s.xs[i] < s.xs[j]
}

func (s *pairSlice) Swap(i, j int) {
	s.xs[i], s.xs[j] = s.xs[j], s.xs[i]
	s.ys[i], s.ys[j] = s.ys[j], s.ys[i]
}
