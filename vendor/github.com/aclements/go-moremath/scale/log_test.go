// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

import (
	"fmt"
	"math"
	"testing"

	"github.com/aclements/go-moremath/internal/mathtest"
	"github.com/aclements/go-moremath/vec"
)

func TestLog(t *testing.T) {
	l, err := NewLog(0, 10, 10)
	if _, ok := err.(RangeErr); !ok {
		t.Errorf("want RangeErr; got %v", err)
	}
	l, err = NewLog(-10, 0, 10)
	if _, ok := err.(RangeErr); !ok {
		t.Errorf("want RangeErr; got %v", err)
	}
	l, err = NewLog(-10, 10, 10)
	if _, ok := err.(RangeErr); !ok {
		t.Errorf("want RangeErr; got %v", err)
	}
	l, err = NewLog(10, 20, 0)
	if _, ok := err.(RangeErr); !ok {
		t.Errorf("want RangeErr; got %v", err)
	}

	l, _ = NewLog(1, 10, 10)
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-1:                math.NaN(),
			0:                 math.NaN(),
			0.1:               -1,
			1:                 0,
			math.Pow(10, 0.5): 0.5,
			10:                1,
			100:               2,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			-1:  0.1,
			0:   1,
			0.5: math.Pow(10, 0.5),
			1:   10,
			2:   100,
		})

	l.SetClamp(true)
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-1:                math.NaN(),
			0:                 math.NaN(),
			0.1:               0,
			1:                 0,
			math.Pow(10, 0.5): 0.5,
			10:                1,
			100:               1,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			0:   1,
			0.5: math.Pow(10, 0.5),
			1:   10,
		})

	l, _ = NewLog(-1, -10, 10)
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			1:                  math.NaN(),
			0:                  math.NaN(),
			-0.1:               2,
			-1:                 1,
			-math.Pow(10, 0.5): 0.5,
			-10:                0,
			-100:               -1,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			2:   -0.1,
			1:   -1,
			0.5: -math.Pow(10, 0.5),
			0:   -10,
			-1:  -100,
		})

	l, _ = NewLog(5, 5, 10)
	mathtest.WantFunc(t, fmt.Sprintf("%v.Map", l), l.Map,
		map[float64]float64{
			-1: math.NaN(),
			0:  math.NaN(),
			1:  0.5,
			10: 0.5,
		})
	mathtest.WantFunc(t, fmt.Sprintf("%v.Unmap", l), l.Unmap,
		map[float64]float64{
			0:   5,
			0.5: 5,
			1:   5,
		})
}

func TestLogTicks(t *testing.T) {
	m := func(m int) TickOptions {
		return TickOptions{Max: m}
	}

	// Test the obvious.
	l, _ := NewLog(1, 10, 10)
	major, minor := l.Ticks(m(5))
	wmajor, wminor := vec.Logspace(0, 1, 2, 10), vec.Linspace(1, 10, 10)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test two orders of magnitude.
	l, _ = NewLog(1, 100, 10)
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Logspace(0, 2, 3, 10), vec.Concat(vec.Linspace(1, 9, 9), vec.Linspace(10, 100, 10))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test many orders of magnitude (higher tick levels).
	l, _ = NewLog(1, 1e8, 10)
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Logspace(0, 4, 5, 100), vec.Logspace(0, 8, 9, 10)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	major, minor = l.Ticks(m(4))
	wmajor, wminor = vec.Logspace(0, 2, 3, 10000), vec.Logspace(0, 4, 5, 100)
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test minor ticks outside major ticks.
	l, _ = NewLog(0.91, 200, 10)
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Logspace(0, 2, 3, 10), vec.Concat(vec.Linspace(1, 9, 9), vec.Linspace(10, 100, 10), []float64{200})
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test nicing.
	l.Nice(m(5))
	major, minor = l.Ticks(m(5))
	wmajor, wminor = vec.Logspace(-1, 3, 5, 10), vec.Concat(vec.Linspace(0.1, 0.9, 9), vec.Linspace(1, 9, 9), vec.Linspace(10, 90, 9), vec.Linspace(100, 1000, 10))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test negative ticks.
	neg := vec.Vectorize(func(x float64) float64 { return -x })
	l, _ = NewLog(-1, -100, 10)
	major, minor = l.Ticks(m(5))
	wmajor, wminor = neg(vec.Logspace(2, 0, 3, 10)), neg(vec.Concat(vec.Linspace(100, 10, 10), vec.Linspace(9, 1, 9)))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	major, minor = l.Ticks(m(2))
	wmajor, wminor = neg(vec.Logspace(1, 0, 2, 100)), neg(vec.Logspace(2, 0, 3, 10))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	l.Nice(m(5))
	major, minor = l.Ticks(m(5))
	wmajor, wminor = neg(vec.Logspace(2, 0, 3, 10)), neg(vec.Concat(vec.Linspace(100, 10, 10), vec.Linspace(9, 1, 9)))
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}

	// Test Min==Max.
	l, _ = NewLog(5, 5, 10)
	major, minor = l.Ticks(m(5))
	wmajor, wminor = []float64{5}, []float64{5}
	if !ticksEq(major, wmajor, minor, wminor) {
		t.Errorf("%v.Ticks(5) = %v, %v; want %v, %v", l, major, minor, wmajor, wminor)
	}
}
