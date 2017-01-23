// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stats

import (
	"fmt"
	"math"
)

// A KDE is a distribution that estimates the underlying distribution
// of a Sample using kernel density estimation.
//
// Kernel density estimation is a method for constructing an estimate
// ƒ̂(x) of a unknown distribution ƒ(x) given a sample from that
// distribution. Unlike many techniques, kernel density estimation is
// non-parametric: in general, it doesn't assume any particular true
// distribution (note, however, that the resulting distribution
// depends deeply on the selected bandwidth, and many bandwidth
// estimation techniques assume normal reference rules).
//
// A kernel density estimate is similar to a histogram, except that it
// is a smooth probability estimate and does not require choosing a
// bin size and discretizing the data.
//
// Sample is the only required field. All others have reasonable
// defaults.
type KDE struct {
	// Sample is the data sample underlying this KDE.
	Sample Sample

	// Kernel is the kernel to use for the KDE.
	Kernel KDEKernel

	// Bandwidth is the bandwidth to use for the KDE.
	//
	// If this is zero, the bandwidth is computed from the
	// provided data using a default bandwidth estimator
	// (currently BandwidthScott).
	Bandwidth float64

	// BoundaryMethod is the boundary correction method to use for
	// the KDE. The default value is BoundaryReflect; however, the
	// default bounds are effectively +/-inf, which is equivalent
	// to performing no boundary correction.
	BoundaryMethod KDEBoundaryMethod

	// [BoundaryMin, BoundaryMax) specify a bounded support for
	// the KDE. If both are 0 (their default values), they are
	// treated as +/-inf.
	//
	// To specify a half-bounded support, set Min to math.Inf(-1)
	// or Max to math.Inf(1).
	BoundaryMin float64
	BoundaryMax float64
}

// BandwidthSilverman is a bandwidth estimator implementing
// Silverman's Rule of Thumb. It's fast, but not very robust to
// outliers as it assumes data is approximately normal.
//
// Silverman, B. W. (1986) Density Estimation.
func BandwidthSilverman(data interface {
	StdDev() float64
	Weight() float64
}) float64 {
	return 1.06 * data.StdDev() * math.Pow(data.Weight(), -1.0/5)
}

// BandwidthScott is a bandwidth estimator implementing Scott's Rule.
// This is generally robust to outliers: it chooses the minimum
// between the sample's standard deviation and an robust estimator of
// a Gaussian distribution's standard deviation.
//
// Scott, D. W. (1992) Multivariate Density Estimation: Theory,
// Practice, and Visualization.
func BandwidthScott(data interface {
	StdDev() float64
	Weight() float64
	Quantile(float64) float64
}) float64 {
	iqr := data.Quantile(0.75) - data.Quantile(0.25)
	hScale := 1.06 * math.Pow(data.Weight(), -1.0/5)
	stdDev := data.StdDev()
	if stdDev < iqr/1.349 {
		// Use Silverman's Rule of Thumb
		return hScale * stdDev
	} else {
		// Use IQR/1.349 as a robust estimator of the standard
		// deviation of a Gaussian distribution.
		return hScale * (iqr / 1.349)
	}
}

// TODO(austin) Implement bandwidth estimator from Botev, Grotowski,
// Kroese. (2010) Kernel Density Estimation via Diffusion.

// KDEKernel represents a kernel to use for a KDE.
type KDEKernel int

//go:generate stringer -type=KDEKernel

const (
	// An EpanechnikovKernel is a smooth kernel with bounded
	// support. As a result, the KDE will also have bounded
	// support. It is "optimal" in the sense that it minimizes the
	// asymptotic mean integrated squared error (AMISE).
	EpanechnikovKernel KDEKernel = iota

	// A GaussianKernel is a Gaussian (normal) kernel.
	GaussianKernel

	// A DeltaKernel is a Dirac delta function. The PDF of such a
	// KDE is not well-defined, but the CDF will represent each
	// sample as an instantaneous increase. This kernel ignores
	// bandwidth and never requires boundary correction.
	DeltaKernel
)

// KDEBoundaryMethod represents a boundary correction method for
// constructing a KDE with bounded support.
type KDEBoundaryMethod int

//go:generate stringer -type=KDEBoundaryMethod

const (
	// BoundaryReflect reflects the density estimate at the
	// boundaries.  For example, for a KDE with support [0, inf),
	// this is equivalent to ƒ̂ᵣ(x)=ƒ̂(x)+ƒ̂(-x) for x>=0.  This is a
	// simple and fast technique, but enforces that ƒ̂ᵣ'(0)=0, so
	// it may not be applicable to all distributions.
	BoundaryReflect KDEBoundaryMethod = iota
)

type kdeKernel interface {
	pdfEach(xs []float64) []float64
	cdfEach(xs []float64) []float64
}

func (k *KDE) prepare() (kdeKernel, bool) {
	// Compute bandwidth.
	if k.Bandwidth == 0 {
		k.Bandwidth = BandwidthScott(k.Sample)
	}

	// Construct kernel.
	kernel := kdeKernel(nil)
	switch k.Kernel {
	default:
		panic(fmt.Sprint("unknown kernel", k))
	case EpanechnikovKernel:
		kernel = epanechnikovKernel{k.Bandwidth}
	case GaussianKernel:
		kernel = NormalDist{0, k.Bandwidth}
	case DeltaKernel:
		kernel = DeltaDist{0}
	}

	// Use boundary correction?
	bc := k.BoundaryMin != 0 || k.BoundaryMax != 0

	return kernel, bc
}

// TODO: For KDEs of histograms, make histograms able to create a
// weighted Sample and simply require the caller to provide a
// good bandwidth from a StreamStats.

// normalizedXs returns x - kde.Sample.Xs. Evaluating kernels shifted
// by kde.Sample.Xs all at x is equivalent to evaluating one unshifted
// kernel at x - kde.Sample.Xs.
func (kde *KDE) normalizedXs(x float64) []float64 {
	txs := make([]float64, len(kde.Sample.Xs))
	for i, xi := range kde.Sample.Xs {
		txs[i] = x - xi
	}
	return txs
}

func (kde *KDE) PDF(x float64) float64 {
	kernel, bc := kde.prepare()

	// Apply boundary
	if bc && (x < kde.BoundaryMin || x >= kde.BoundaryMax) {
		return 0
	}

	y := func(x float64) float64 {
		// Shift kernel to each of kde.xs and evaluate at x
		ys := kernel.pdfEach(kde.normalizedXs(x))

		// Kernel samples are weighted according to the weights of xs
		wys := Sample{Xs: ys, Weights: kde.Sample.Weights}

		return wys.Sum() / wys.Weight()
	}
	if !bc {
		return y(x)
	}
	switch kde.BoundaryMethod {
	default:
		panic("unknown boundary correction method")
	case BoundaryReflect:
		if math.IsInf(kde.BoundaryMax, 1) {
			return y(x) + y(2*kde.BoundaryMin-x)
		} else if math.IsInf(kde.BoundaryMin, -1) {
			return y(x) + y(2*kde.BoundaryMax-x)
		} else {
			d := 2 * (kde.BoundaryMax - kde.BoundaryMin)
			w := 2 * (x - kde.BoundaryMin)
			return series(func(n float64) float64 {
				// Points >= x
				return y(x+n*d) + y(x+n*d-w)
			}) + series(func(n float64) float64 {
				// Points < x
				return y(x-(n+1)*d+w) + y(x-(n+1)*d)
			})
		}
	}
}

func (kde *KDE) CDF(x float64) float64 {
	kernel, bc := kde.prepare()

	// Apply boundary
	if bc {
		if x < kde.BoundaryMin {
			return 0
		} else if x >= kde.BoundaryMax {
			return 1
		}
	}

	y := func(x float64) float64 {
		// Shift kernel integral to each of cdf.xs and evaluate at x
		ys := kernel.cdfEach(kde.normalizedXs(x))

		// Kernel samples are weighted according to the weights of xs
		wys := Sample{Xs: ys, Weights: kde.Sample.Weights}

		return wys.Sum() / wys.Weight()
	}
	if !bc {
		return y(x)
	}
	switch kde.BoundaryMethod {
	default:
		panic("unknown boundary correction method")
	case BoundaryReflect:
		if math.IsInf(kde.BoundaryMax, 1) {
			return y(x) - y(2*kde.BoundaryMin-x)
		} else if math.IsInf(kde.BoundaryMin, -1) {
			return y(x) + (1 - y(2*kde.BoundaryMax-x))
		} else {
			d := 2 * (kde.BoundaryMax - kde.BoundaryMin)
			w := 2 * (x - kde.BoundaryMin)
			return series(func(n float64) float64 {
				// Windows >= x-w
				return y(x+n*d) - y(x+n*d-w)
			}) + series(func(n float64) float64 {
				// Windows < x-w
				return y(x-(n+1)*d) - y(x-(n+1)*d-w)
			})
		}
	}
}

func (kde *KDE) Bounds() (low float64, high float64) {
	_, bc := kde.prepare()

	// TODO(austin) If this KDE came from a histogram, we'd better
	// not sample at a significantly higher rate than the
	// histogram.  Maybe we want to just return the bounds of the
	// histogram?

	// TODO(austin) It would be nice if this could be instructed
	// to include all original data points, even if they are in
	// the tail.  Probably that should just be up to the caller to
	// pass an axis derived from the bounds of the original data.

	// Use the lowest and highest samples as starting points
	lowX, highX := kde.Sample.Bounds()
	if lowX == highX {
		lowX -= 1
		highX += 1
	}

	// Find the end points that contain 99% of the CDF's weight.
	// Since bisect requires that the root be bracketed, start by
	// expanding our range if necessary.  TODO(austin) This can
	// definitely be done faster.
	const (
		lowY      = 0.005
		highY     = 0.995
		tolerance = 0.001
	)
	for kde.CDF(lowX) > lowY {
		lowX -= highX - lowX
	}
	for kde.CDF(highX) < highY {
		highX += highX - lowX
	}
	// Explicitly accept discontinuities, since we may be using a
	// discontiguous kernel.
	low, _ = bisect(func(x float64) float64 { return kde.CDF(x) - lowY }, lowX, highX, tolerance)
	high, _ = bisect(func(x float64) float64 { return kde.CDF(x) - highY }, lowX, highX, tolerance)

	// Expand width by 20% to give some margins
	width := high - low
	low, high = low-0.1*width, high+0.1*width

	// Limit to bounds
	if bc {
		low = math.Max(low, kde.BoundaryMin)
		high = math.Min(high, kde.BoundaryMax)
	}

	return
}

type epanechnikovKernel struct {
	h float64
}

func (d epanechnikovKernel) pdfEach(xs []float64) []float64 {
	ys := make([]float64, len(xs))
	a := 0.75 / d.h
	invhh := 1 / (d.h * d.h)
	for i, x := range xs {
		if -d.h < x && x < d.h {
			ys[i] = a * (1 - x*x*invhh)
		}
	}
	return ys
}

func (d epanechnikovKernel) cdfEach(xs []float64) []float64 {
	ys := make([]float64, len(xs))
	invh := 1 / d.h
	for i, x := range xs {
		if x > d.h {
			ys[i] = 1
		} else if x > -d.h {
			u := x * invh
			ys[i] = 0.25 * (2 + 3*u - u*u*u)
		}
	}
	return ys
}
