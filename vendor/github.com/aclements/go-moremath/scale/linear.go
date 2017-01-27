// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

import (
	"math"

	"github.com/aclements/go-moremath/vec"
)

type Linear struct {
	// Min and Max specify the lower and upper bounds of the input
	// domain. The input domain [Min, Max] will be linearly mapped
	// to the output range [0, 1].
	Min, Max float64

	// Base specifies a base for computing ticks. Ticks will be
	// placed at powers of Base; that is at n*Base^l for n ∈ ℤ and
	// some integer tick level l. As a special case, a base of 0
	// alternates between ticks at n*10^⌊l/2⌋ and ticks at
	// 5n*10^⌊l/2⌋.
	Base int

	// If Clamp is true, the input is clamped to [Min, Max].
	Clamp bool
}

// *Linear is a Quantitative scale.
var _ Quantitative = &Linear{}

func (s Linear) Map(x float64) float64 {
	if s.Min == s.Max {
		return 0.5
	}
	y := (x - s.Min) / (s.Max - s.Min)
	if s.Clamp {
		y = clamp(y)
	}
	return y
}

func (s Linear) Unmap(y float64) float64 {
	return y*(s.Max-s.Min) + s.Min
}

func (s *Linear) SetClamp(clamp bool) {
	s.Clamp = clamp
}

// ebase sanity checks and returns the "effective base" of this scale.
// If s.Base is 0, it returns 10. If s.Base is 1 or negative, it
// panics.
func (s Linear) ebase() int {
	if s.Base == 0 {
		return 10
	} else if s.Base == 1 {
		panic("scale.Linear cannot have a base of 1")
	} else if s.Base < 0 {
		panic("scale.Linear cannot have a negative base")
	}
	return s.Base
}

// In the default base, the tick levels are:
//
// Level -2 is a major tick at -0.1, 0, 0.1, etc.
// Level -1 is a major tick at -1, -0.5, 0, 0.5, 1, etc.
// Level 0 is a major tick at -1, 0, 1, etc.
// Level 1 is a major tick at -10, -5, 0, 5, 10, etc.
// Level 2 is a major tick at -10, 0, 10, etc.
//
// That is, level 0 is unit intervals, and we alternate between
// interval *= 5 and interval *= 2. Combined, these give us interval
// *= 10 at every other level.
//
// In non-default bases, level 0 is the same and we alternate between
// interval *= 1 (for consistency) and interval *= base.

func (s *Linear) guessLevel() int {
	return 2 * int(math.Log(s.Max-s.Min)/math.Log(float64(s.ebase())))
}

func (s *Linear) spacingAtLevel(level int, roundOut bool) (firstN, lastN, spacing float64) {
	// Watch out! Integer division is round toward zero, but we
	// need round down, and modulus is signed.
	exp, double := math.Floor(float64(level)/2), (level%2 == 1 || level%2 == -1)
	spacing = math.Pow(float64(s.ebase()), exp)
	if double && s.Base == 0 {
		spacing *= 5
	}

	// Add a tiny bit of slack to the floor and ceiling below so
	// that rounding errors don't significantly affect tick marks.
	slack := (s.Max - s.Min) * 1e-10

	if roundOut {
		firstN = math.Floor((s.Min + slack) / spacing)
		lastN = math.Ceil((s.Max - slack) / spacing)
	} else {
		firstN = math.Ceil((s.Min - slack) / spacing)
		lastN = math.Floor((s.Max + slack) / spacing)
	}
	return
}

// CountTicks returns the number of ticks in [s.Min, s.Max] at the
// given tick level.
func (s Linear) CountTicks(level int) int {
	return linearTicker{&s, false}.CountTicks(level)
}

// TicksAtLevel returns the tick locations in [s.Min, s.Max] as a
// []float64 at the given tick level in ascending order.
func (s Linear) TicksAtLevel(level int) interface{} {
	return linearTicker{&s, false}.TicksAtLevel(level)
}

type linearTicker struct {
	s        *Linear
	roundOut bool
}

func (t linearTicker) CountTicks(level int) int {
	firstN, lastN, _ := t.s.spacingAtLevel(level, t.roundOut)
	return int(lastN - firstN + 1)
}

func (t linearTicker) TicksAtLevel(level int) interface{} {
	firstN, lastN, spacing := t.s.spacingAtLevel(level, t.roundOut)
	n := int(lastN - firstN + 1)
	return vec.Linspace(firstN*spacing, lastN*spacing, n)
}

func (s Linear) Ticks(o TickOptions) (major, minor []float64) {
	if o.Max <= 0 {
		return nil, nil
	} else if s.Min == s.Max {
		return []float64{s.Min}, []float64{s.Min}
	} else if s.Min > s.Max {
		s.Min, s.Max = s.Max, s.Min
	}

	level, ok := o.FindLevel(linearTicker{&s, false}, s.guessLevel())
	if !ok {
		return nil, nil
	}
	return s.TicksAtLevel(level).([]float64), s.TicksAtLevel(level - 1).([]float64)
}

func (s *Linear) Nice(o TickOptions) {
	if s.Min == s.Max {
		s.Min -= 0.5
		s.Max += 0.5
	} else if s.Min > s.Max {
		s.Min, s.Max = s.Max, s.Min
	}

	level, ok := o.FindLevel(linearTicker{s, true}, s.guessLevel())
	if !ok {
		return
	}

	firstN, lastN, spacing := s.spacingAtLevel(level, true)
	s.Min = firstN * spacing
	s.Max = lastN * spacing
}
