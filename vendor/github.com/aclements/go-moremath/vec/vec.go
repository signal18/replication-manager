// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vec

import "math"

// Vectorize returns a function g(xs) that applies f to each x in xs.
//
// f may be evaluated in parallel and in any order.
func Vectorize(f func(float64) float64) func(xs []float64) []float64 {
	return func(xs []float64) []float64 {
		return Map(f, xs)
	}
}

// Map returns f(x) for each x in xs.
//
// f may be evaluated in parallel and in any order.
func Map(f func(float64) float64, xs []float64) []float64 {
	// TODO(austin) Parallelize
	res := make([]float64, len(xs))
	for i, x := range xs {
		res[i] = f(x)
	}
	return res
}

// Linspace returns num values spaced evenly between lo and hi,
// inclusive. If num is 1, this returns an array consisting of lo.
func Linspace(lo, hi float64, num int) []float64 {
	res := make([]float64, num)
	if num == 1 {
		res[0] = lo
		return res
	}
	for i := 0; i < num; i++ {
		res[i] = lo + float64(i)*(hi-lo)/float64(num-1)
	}
	return res
}

// Logspace returns num values spaced evenly on a logarithmic scale
// between base**lo and base**hi, inclusive.
func Logspace(lo, hi float64, num int, base float64) []float64 {
	res := Linspace(lo, hi, num)
	for i, x := range res {
		res[i] = math.Pow(base, x)
	}
	return res
}

// Sum returns the sum of xs.
func Sum(xs []float64) float64 {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum
}

// Concat returns the concatenation of its arguments. It does not
// modify its inputs.
func Concat(xss ...[]float64) []float64 {
	total := 0
	for _, xs := range xss {
		total += len(xs)
	}
	out := make([]float64, total)
	pos := 0
	for _, xs := range xss {
		pos += copy(out[pos:], xs)
	}
	return out
}
