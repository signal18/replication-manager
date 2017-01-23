// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

import "testing"

type testTicker struct{}

func (testTicker) CountTicks(level int) int {
	c := 10 - level
	if c < 1 {
		c = 1
	}
	return c
}

func (t testTicker) TicksAtLevel(level int) interface{} {
	m := make([]float64, t.CountTicks(level))
	for i := 0; i < len(m); i++ {
		m[i] = float64(i)
	}
	return m
}

func TestTicks(t *testing.T) {
	check := func(o TickOptions, want int) {
		wantL, wantOK := want, true
		if want == -999 {
			wantL, wantOK = 0, false
		}
		for _, guess := range []int{0, -50, 50} {
			l, ok := o.FindLevel(testTicker{}, guess)
			if l != wantL || ok != wantOK {
				t.Errorf("%+v.FindLevel with guess %v returned %v, %v; wanted %v, %v", o, guess, l, ok, wantL, wantOK)
			}
		}
	}

	// Argument sanity checking.
	check(TickOptions{}, -999)
	check(TickOptions{MinLevel: 10, MaxLevel: 9}, -999)

	// Just max constraint.
	check(TickOptions{Max: 1}, 9)
	check(TickOptions{Max: 6}, 4)
	check(TickOptions{Max: 20}, -10)

	// Max and level constraints.
	check(TickOptions{Max: 1, MaxLevel: 9}, 9)
	check(TickOptions{Max: 1, MaxLevel: 8}, -999)
	check(TickOptions{Max: 1, MinLevel: 9, MaxLevel: 1000}, 9)
	check(TickOptions{Max: 1, MinLevel: 10, MaxLevel: 1000}, 10)

	check(TickOptions{Max: 6, MaxLevel: 9}, 4)
	check(TickOptions{Max: 6, MaxLevel: 3}, -999)
	check(TickOptions{Max: 6, MinLevel: 10, MaxLevel: 11}, 10)
}
