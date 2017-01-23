// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

import (
	"fmt"
	"testing"

	"github.com/aclements/go-moremath/internal/mathtest"
	"github.com/aclements/go-moremath/vec"
)

func TestLinear(t *testing.T) {
	l := Linear{Min: -10, Max: 10}
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-20: -0.5,
			-10: 0,
			0:   0.5,
			10:  1,
			20:  1.5,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			-0.5: -20,
			0:    -10,
			0.5:  0,
			1:    10,
			1.5:  20,
		})

	l.SetClamp(true)
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-20: 0,
			-10: 0,
			0:   0.5,
			10:  1,
			20:  1,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			0:   -10,
			0.5: 0,
			1:   10,
		})

	l = Linear{Min: 5, Max: 5}
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-10: 0.5,
			0:   0.5,
			10:  0.5,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			0:   5,
			0.5: 5,
			1:   5,
		})
}

func ticksEq(major, wmajor, minor, wminor []float64) bool {
	// TODO: It would be nice to have a deep Aeq. It could also
	// support checking predicates like LE(5) or IsNaN within
	// structures, which could be used in WantFunc. Heck, deep Aeq
	// could subsume WantFunc where the left side is a function
	// and the right side is a map from arguments to results, but
	// maybe it would be harder to produce a good error message.
	if len(major) != len(wmajor) || len(minor) != len(wminor) {
		return false
	}
	for i, v := range major {
		if !mathtest.Aeq(wmajor[i], v) {
			return false
		}
	}
	for i, v := range minor {
		if !mathtest.Aeq(wminor[i], v) {
			return false
		}
	}
	return true
}

func TestLinearTicks(t *testing.T) {
	m := func(m int) TickOptions {
		return TickOptions{Max: m}
	}

	l := Linear{Min: 0, Max: 100}
	major, minor := l.Ticks(m(5))
	wmajor, wminor := vec.Linspace(0, 100, 3), vec.Linspace(0, 100, 11)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	major, minor = l.Ticks(m(2))
	wmajor, wminor = vec.Linspace(0, 100, 2), vec.Linspace(0, 100, 3)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(2) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(2))
	major, minor = l.Ticks(m(2))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(2) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l = Linear{Min: 15.4, Max: 16.6}
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Linspace(15.5, 16.5, 3), vec.Linspace(15.4, 16.6, 13)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(5))
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Linspace(15, 17, 5), vec.Linspace(15, 17, 21)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test negative tick levels.
	l = Linear{Min: 9.9989, Max: 10}
	major, minor = l.Ticks(m(2))
	wmajor, wminor = vec.Linspace(9.999, 10, 2), vec.Linspace(9.999, 10, 3)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(2) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(2))
	major, minor = l.Ticks(m(2))
	wmajor, wminor = vec.Linspace(9.995, 10, 2), vec.Linspace(9.995, 10, 6)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(2) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test non-default bases.
	l = Linear{Min: 2, Max: 9, Base: 2}
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Linspace(2, 8, 4), vec.Linspace(2, 9, 8)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(5))
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Linspace(2, 10, 5), vec.Linspace(2, 10, 9)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test Min==Max.
	l = Linear{Min: 2, Max: 2}
	major, minor = l.Ticks(m(5))
	wmajor, wminor = []float64{2}, []float64{2}
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(5))
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Linspace(1.5, 2.5, 3), vec.Linspace(1.5, 2.5, 11)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

}
