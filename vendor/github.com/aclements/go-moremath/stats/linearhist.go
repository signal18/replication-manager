// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

// LinearHist is a Histogram with uniformly-sized bins.
type LinearHist struct {
	min, max, delta float64
	low, high       uint
	bins            []uint
}

// NewLinearHist returns an empty histogram with nbins uniformly-sized
// bins spanning [min, max].
func NewLinearHist(min, max float64, nbins int) *LinearHist {
	delta := float64(nbins) / (max - min)
	return &LinearHist{min, max, delta, 0, 0, make([]uint, nbins)}
}

func (h *LinearHist) bin(x float64) int {
	return int(h.delta * (x - h.min))
}

func (h *LinearHist) Add(x float64) {
	bin := h.bin(x)
	if bin < 0 {
		h.low++
	} else if bin >= len(h.bins) {
		h.high++
	} else {
		h.bins[bin]++
	}
}

func (h *LinearHist) Counts() (uint, []uint, uint) {
	return h.low, h.bins, h.high
}

func (h *LinearHist) BinToValue(bin float64) float64 {
	return h.min + bin*h.delta
}
