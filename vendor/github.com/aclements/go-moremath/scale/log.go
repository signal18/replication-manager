// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

import "math"

type Log struct {
	private struct{}

	// Min and Max specify the lower and upper bounds of the input
	// domain. The input range [Min, Max] will be mapped to the
	// output range [0, 1]. The range [Min, Max] must not include
	// 0.
	Min, Max float64

	// Base specifies the base of the logarithm for computing
	// ticks. Ticks will be placed at Base^((2^l)*n) for tick
	// level l ∈ ℕ and n ∈ ℤ. Typically l is 0, in which case this
	// is simply Base^n.
	Base int

	// If Clamp is true, the input is clamped to [Min, Max].
	Clamp bool

	// TODO: Let the user specify the minor ticks. Default to [1,
	// .. 9], but [1, 3] and [1, 2, 5] are common.
}

// *Log is a Quantitative scale.
var _ Quantitative = &Log{}

// NewLog constructs a Log scale. If the arguments are out of range,
// it returns a RangeErr.
func NewLog(min, max float64, base int) (Log, error) {
	if min > max {
		min, max = max, min
	}

	if base <= 1 {
		return Log{}, RangeErr("Log scale base must be 2 or more")
	}
	if min <= 0 && max >= 0 {
		return Log{}, RangeErr("Log scale range cannot include 0")
	}

	return Log{Min: min, Max: max, Base: base}, nil
}

func (s *Log) ebounds() (bool, float64, float64) {
	if s.Min < 0 {
		return true, -s.Max, -s.Min
	}
	return false, s.Min, s.Max
}

func (s Log) Map(x float64) float64 {
	neg, min, max := s.ebounds()
	if neg {
		x = -x
	}
	if x <= 0 {
		return math.NaN()
	}
	if min == max {
		return 0.5
	}

	logMin, logMax := math.Log(min), math.Log(max)
	y := (math.Log(x) - logMin) / (logMax - logMin)
	if neg {
		y = 1 - y
	}
	if s.Clamp {
		y = clamp(y)
	}
	return y
}

func (s Log) Unmap(y float64) float64 {
	neg, min, max := s.ebounds()
	if neg {
		y = 1 - y
	}
	logMin, logMax := math.Log(min), math.Log(max)
	x := math.Exp(y*(logMax-logMin) + logMin)
	if neg {
		x = -x
	}
	return x
}

func (s *Log) SetClamp(clamp bool) {
	s.Clamp = clamp
}

// The tick levels are:
//
// Level 0 is a major tick at Base^n (1, 10, 100, ...)
// Level 1 is a major tick at Base^(2*n) (1, 100, 10000, ...)
// Level 2 is a major tick at Base^(4*n) (1, 10000, 100000000, ...)
//
// That is, each level eliminates every other tick. Levels below 0 are
// not defined.

func logb(x float64, b float64) float64 {
	return math.Log(x) / math.Log(b)
}

func (s *Log) spacingAtLevel(level int, roundOut bool) (firstN, lastN, ebase float64) {
	_, min, max := s.ebounds()

	// Compute the effective base at this level.
	ebase = math.Pow(float64(s.Base), math.Pow(2, float64(level)))
	lmin, lmax := logb(min, ebase), logb(max, ebase)

	// Add a tiny bit of slack to the floor and ceiling so that
	// rounding errors don't significantly affect tick marks.
	slack := (lmax - lmin) * 1e-10

	if roundOut {
		firstN = math.Floor(lmin + slack)
		lastN = math.Ceil(lmax - slack)
	} else {
		firstN = math.Ceil(lmin - slack)
		lastN = math.Floor(lmax + slack)
	}

	return
}

func (s *Log) CountTicks(level int) int {
	return logTicker{s, false}.CountTicks(level)
}

func (s *Log) TicksAtLevel(level int) interface{} {
	return logTicker{s, false}.TicksAtLevel(level)
}

type logTicker struct {
	s        *Log
	roundOut bool
}

func (t logTicker) CountTicks(level int) int {
	if level < 0 {
		const maxInt = int(^uint(0) >> 1)
		return maxInt
	}

	firstN, lastN, _ := t.s.spacingAtLevel(level, t.roundOut)
	return int(lastN - firstN + 1)
}

func (t logTicker) TicksAtLevel(level int) interface{} {
	neg, min, max := t.s.ebounds()
	ticks := []float64{}

	if level < 0 {
		// Minor ticks for level 0. Get the major
		// ticks, but round out so we can fill in
		// minor ticks outside of the major ticks.
		firstN, lastN, _ := t.s.spacingAtLevel(0, true)
		for n := firstN; n <= lastN; n++ {
			tick := math.Pow(float64(t.s.Base), n)
			step := tick
			for i := 0; i < t.s.Base-1; i++ {
				if min <= tick && tick <= max {
					ticks = append(ticks, tick)
				}
				tick += step
			}
		}
	} else {
		firstN, lastN, base := t.s.spacingAtLevel(level, t.roundOut)
		for n := firstN; n <= lastN; n++ {
			ticks = append(ticks, math.Pow(base, n))
		}
	}

	if neg {
		// Negate and reverse order of ticks.
		for i := 0; i < (len(ticks)+1)/2; i++ {
			j := len(ticks) - i - 1
			ticks[i], ticks[j] = -ticks[j], -ticks[i]
		}
	}

	return ticks
}

func (s Log) Ticks(o TickOptions) (major, minor []float64) {
	if o.Max <= 0 {
		return nil, nil
	} else if s.Min == s.Max {
		return []float64{s.Min}, []float64{s.Max}
	}
	t := logTicker{&s, false}

	level, ok := o.FindLevel(t, 0)
	if !ok {
		return nil, nil
	}
	return t.TicksAtLevel(level).([]float64), t.TicksAtLevel(level - 1).([]float64)
}

func (s *Log) Nice(o TickOptions) {
	if s.Min == s.Max {
		return
	}
	neg, _, _ := s.ebounds()
	t := logTicker{s, true}

	level, ok := o.FindLevel(t, 0)
	if !ok {
		return
	}
	firstN, lastN, base := s.spacingAtLevel(level, true)
	s.Min = math.Pow(base, firstN)
	s.Max = math.Pow(base, lastN)
	if neg {
		s.Min, s.Max = -s.Max, -s.Min
	}
}
