// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"testing"
)

func TestHypergeometricDist(t *testing.T) {
	dist1 := HypergeometicDist{N: 50, K: 5, Draws: 10}
	testFunc(t, fmt.Sprintf("%+v.PMF", dist1), dist1.PMF,
		map[float64]float64{
			-0.1: 0,
			4:    0.003964583058,
			4.9:  0.003964583058, // Test rounding
			5:    0.000118937492,
			5.9:  0.000118937492,
			6:    0,
		})
	testDiscreteCDF(t, fmt.Sprintf("%+v.CDF", dist1), dist1)
}
