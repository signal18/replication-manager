// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import "math"

// TODO: Implement histograms on top of scales.

type Histogram interface {
	// Add adds a sample with value x to histogram h.
	Add(x float64)

	// Counts returns the number of samples less than the lowest
	// bin, a slice of the number of samples in each bin,
	// and the number of samples greater than the highest bin.
	Counts() (under uint, counts []uint, over uint)

	// BinToValue returns the value that would appear at the given
	// bin index.
	//
	// For integral values of bin, BinToValue returns the lower
	// bound of bin.  That is, a sample value x will be in bin if
	// bin is integral and
	//
	//    BinToValue(bin) <= x < BinToValue(bin + 1)
	//
	// For non-integral values of bin, BinToValue interpolates
	// between the lower and upper bounds of math.Floor(bin).
	//
	// BinToValue is undefined if bin > 1 + the number of bins.
	BinToValue(bin float64) float64
}

// HistogramQuantile returns the x such that n*q samples in hist are
// <= x, assuming values are distibuted within each bin according to
// hist's distribution.
//
// If the q'th sample falls below the lowest bin or above the highest
// bin, returns NaN.
func HistogramQuantile(hist Histogram, q float64) float64 {
	under, counts, over := hist.Counts()
	total := under + over
	for _, count := range counts {
		total += count
	}

	goal := uint(float64(total) * q)
	if goal <= under || goal > total-over {
		return math.NaN()
	}
	for bin, count := range counts {
		if count > goal {
			return hist.BinToValue(float64(bin) + float64(goal)/float64(count))
		}
		goal -= count
	}
	panic("goal count not reached")
}

// HistogramIQR returns the interquartile range of the samples in
// hist.
func HistogramIQR(hist Histogram) float64 {
	return HistogramQuantile(hist, 0.75) - HistogramQuantile(hist, 0.25)
}
