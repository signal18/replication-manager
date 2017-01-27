// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import "math"

// LogHist is a Histogram with logarithmically-spaced bins.
type LogHist struct {
	b         int
	m         float64
	mOverLogb float64
	low, high uint
	bins      []uint
}

// NewLogHist returns an empty logarithmic histogram with bins for
// integral values of m * log_b(x) up to x = max.
func NewLogHist(b int, m float64, max float64) *LogHist {
	// TODO(austin) Minimum value as well?  If the samples are
	// actually integral, having fractional bin boundaries can
	// mess up smoothing.
	mOverLogb := m / math.Log(float64(b))
	nbins := int(math.Ceil(mOverLogb * math.Log(max)))
	return &LogHist{b: b, m: m, mOverLogb: mOverLogb, low: 0, high: 0, bins: make([]uint, nbins)}
}

func (h *LogHist) bin(x float64) int {
	return int(h.mOverLogb * math.Log(x))
}

func (h *LogHist) Add(x float64) {
	bin := h.bin(x)
	if bin < 0 {
		h.low++
	} else if bin >= len(h.bins) {
		h.high++
	} else {
		h.bins[bin]++
	}
}

func (h *LogHist) Counts() (uint, []uint, uint) {
	return h.low, h.bins, h.high
}

func (h *LogHist) BinToValue(bin float64) float64 {
	return math.Pow(float64(h.b), bin/h.m)
}

func (h *LogHist) At(x float64) float64 {
	bin := h.bin(x)
	if bin < 0 || bin >= len(h.bins) {
		return 0
	}
	return float64(h.bins[bin])
}

func (h *LogHist) Bounds() (float64, float64) {
	// XXX Plot will plot this on a linear axis.  Maybe this
	// should be able to return the natural axis?
	// Maybe then we could also give it the bins for the tics.
	lowbin := 0
	if h.low == 0 {
		for bin, count := range h.bins {
			if count > 0 {
				lowbin = bin
				break
			}
		}
	}
	highbin := len(h.bins)
	if h.high == 0 {
		for bin := range h.bins {
			if h.bins[len(h.bins)-bin-1] > 0 {
				highbin = len(h.bins) - bin
				break
			}
		}
	}
	return h.BinToValue(float64(lowbin)), h.BinToValue(float64(highbin))
}
