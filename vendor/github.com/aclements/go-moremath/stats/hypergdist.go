// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"math"

	"github.com/aclements/go-moremath/mathx"
)

// HypergeometicDist is a hypergeometric distribution.
type HypergeometicDist struct {
	// N is the size of the population. N >= 0.
	N int

	// K is the number of successes in the population. 0 <= K <= N.
	K int

	// Draws is the number of draws from the population. This is
	// usually written "n", but is called Draws here because of
	// limitations on Go identifier naming. 0 <= Draws <= N.
	Draws int
}

// PMF is the probability of getting exactly int(k) successes in
// d.Draws draws with replacement from a population of size d.N that
// contains exactly d.K successes.
func (d HypergeometicDist) PMF(k float64) float64 {
	ki := int(math.Floor(k))
	l, h := d.bounds()
	if ki < l || ki > h {
		return 0
	}
	return d.pmf(ki)
}

func (d HypergeometicDist) pmf(k int) float64 {
	return math.Exp(mathx.Lchoose(d.K, k) + mathx.Lchoose(d.N-d.K, d.Draws-k) - mathx.Lchoose(d.N, d.Draws))
}

// CDF is the probability of getting int(k) or fewer successes in
// d.Draws draws with replacement from a population of size d.N that
// contains exactly d.K successes.
func (d HypergeometicDist) CDF(k float64) float64 {
	// Based on Klotz, A Computational Approach to Statistics.
	ki := int(math.Floor(k))
	l, h := d.bounds()
	if ki < l {
		return 0
	} else if ki >= h {
		return 1
	}
	// Use symmetry to compute the smaller sum.
	flip := false
	if ki > (d.Draws+1)/(d.N+1)*(d.K+1) {
		flip = true
		ki = d.K - ki - 1
		d.Draws = d.N - d.Draws
	}
	p := d.pmf(ki) * d.sum(ki)
	if flip {
		p = 1 - p
	}
	return p
}

func (d HypergeometicDist) sum(k int) float64 {
	const epsilon = 1e-14
	sum, ak := 1.0, 1.0
	L := maxint(0, d.Draws+d.K-d.N)
	for dk := 1; dk <= k-L && ak/sum > epsilon; dk++ {
		ak *= float64(1+k-dk) / float64(d.Draws-k+dk)
		ak *= float64(d.N-d.K-d.Draws+k+1-dk) / float64(d.K-k+dk)
		sum += ak
	}
	return sum
}

func (d HypergeometicDist) bounds() (int, int) {
	return maxint(0, d.Draws+d.K-d.N), minint(d.Draws, d.K)
}

func (d HypergeometicDist) Bounds() (float64, float64) {
	l, h := d.bounds()
	return float64(l), float64(h)
}

func (d HypergeometicDist) Step() float64 {
	return 1
}

func (d HypergeometicDist) Mean() float64 {
	return float64(d.Draws*d.K) / float64(d.N)
}

func (d HypergeometicDist) Variance() float64 {
	return float64(d.Draws*d.K*(d.N-d.K)*(d.N-d.Draws)) /
		float64(d.N*d.N*(d.N-1))
}
