// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"math"
)

// TODO(austin) Unify more with Sample interface

// StreamStats tracks basic statistics for a stream of data in O(1)
// space.
//
// StreamStats should be initialized to its zero value.
type StreamStats struct {
	Count           uint
	Total, Min, Max float64

	// Numerically stable online mean
	mean          float64
	meanOfSquares float64

	// Online variance
	vM2 float64
}

// Add updates s's statistics with sample value x.
func (s *StreamStats) Add(x float64) {
	s.Total += x
	if s.Count == 0 {
		s.Min, s.Max = x, x
	} else {
		if x < s.Min {
			s.Min = x
		}
		if x > s.Max {
			s.Max = x
		}
	}
	s.Count++

	// Update online mean, mean of squares, and variance.  Online
	// variance based on Wikipedia's presentation ("Algorithms for
	// calculating variance") of Knuth's formulation of Welford
	// 1962.
	delta := x - s.mean
	s.mean += delta / float64(s.Count)
	s.meanOfSquares += (x*x - s.meanOfSquares) / float64(s.Count)
	s.vM2 += delta * (x - s.mean)
}

func (s *StreamStats) Weight() float64 {
	return float64(s.Count)
}

func (s *StreamStats) Mean() float64 {
	return s.mean
}

func (s *StreamStats) Variance() float64 {
	return s.vM2 / float64(s.Count-1)
}

func (s *StreamStats) StdDev() float64 {
	return math.Sqrt(s.Variance())
}

func (s *StreamStats) RMS() float64 {
	return math.Sqrt(s.meanOfSquares)
}

// Combine updates s's statistics as if all samples added to o were
// added to s.
func (s *StreamStats) Combine(o *StreamStats) {
	count := s.Count + o.Count

	// Compute combined online variance statistics
	delta := o.mean - s.mean
	mean := s.mean + delta*float64(o.Count)/float64(count)
	vM2 := s.vM2 + o.vM2 + delta*delta*float64(s.Count)*float64(o.Count)/float64(count)

	s.Count = count
	s.Total += o.Total
	if o.Min < s.Min {
		s.Min = o.Min
	}
	if o.Max > s.Max {
		s.Max = o.Max
	}
	s.mean = mean
	s.meanOfSquares += (o.meanOfSquares - s.meanOfSquares) * float64(o.Count) / float64(count)
	s.vM2 = vM2
}

func (s *StreamStats) String() string {
	return fmt.Sprintf("Count=%d Total=%g Min=%g Mean=%g RMS=%g Max=%g StdDev=%g", s.Count, s.Total, s.Min, s.Mean(), s.RMS(), s.Max, s.StdDev())
}
