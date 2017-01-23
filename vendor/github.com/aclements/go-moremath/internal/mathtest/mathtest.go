// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mathtest

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
)

var (
	aeqDigits int
	aeqFactor float64
)

func SetAeqDigits(digits int) int {
	old := aeqDigits
	aeqDigits = digits
	aeqFactor = 1 - math.Pow(10, float64(-digits+1))
	return old
}

func init() {
	SetAeqDigits(8)
}

// Aeq returns true if expect and got are equal up to the current
// number of aeq digits set by SetAeqDigits. By default, this is 8
// significant figures (1 part in 100 million).
func Aeq(expect, got float64) bool {
	if expect < 0 && got < 0 {
		expect, got = -expect, -got
	}
	return expect*aeqFactor <= got && got*aeqFactor <= expect
}

func WantFunc(t *testing.T, name string, f func(float64) float64, vals map[float64]float64) {
	xs := make([]float64, 0, len(vals))
	for x := range vals {
		xs = append(xs, x)
	}
	sort.Float64s(xs)

	for _, x := range xs {
		want, got := vals[x], f(x)
		if math.IsNaN(want) && math.IsNaN(got) || Aeq(want, got) {
			continue
		}
		var label string
		if strings.Contains(name, "%v") {
			label = fmt.Sprintf(name, x)
		} else {
			label = fmt.Sprintf("%s(%v)", name, x)
		}
		t.Errorf("want %s=%v, got %v", label, want, got)
	}
}
