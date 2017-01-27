// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"github.com/aclements/go-moremath/scale"
	"github.com/aclements/go-moremath/stats"
	"github.com/aclements/go-moremath/vec"
)

const (
	// printSamples is the number of points on the X axis to
	// sample a function at for printing.
	printSamples = 500

	// printWidth is the width of the plot area in dots.
	printWidth = 70 * 2
	// printHeight is the height of the plot area in dots.
	printHeight = 3 * 4

	printXMargin = 1
	printYMargin = 1
)

// FprintPDF prints a Unicode representation of the PDF of each
// distribution in dists to w. Multiple distributions are printed
// stacked vertically and on the same X axis (but possibly different Y
// axes).
func FprintPDF(w io.Writer, dists ...stats.Dist) error {
	xscale, xs := commonScale(dists...)
	for _, d := range dists {
		if err := fprintFn(w, d.PDF, xscale, xs); err != nil {
			return err
		}
	}
	return fprintScale(w, xscale)
}

// FprintCDF is equivalent to FprintPDF, but prints the CDF of each
// distribution.
func FprintCDF(w io.Writer, dists ...stats.Dist) error {
	xscale, xs := commonScale(dists...)
	for _, d := range dists {
		if err := fprintFn(w, d.CDF, xscale, xs); err != nil {
			return err
		}
	}
	return fprintScale(w, xscale)
}

// makeScale creates a linear scale from [x1, x2) to [y1, y2).
func makeScale(x1, x2 float64, y1, y2 int) scale.QQ {
	return scale.QQ{
		Src:  &scale.Linear{Min: x1, Max: x2, Clamp: true},
		Dest: &scale.Linear{Min: float64(y1), Max: float64(y2) - 1e-10},
	}
}

func commonScale(dist ...stats.Dist) (xscale scale.QQ, xs []float64) {
	var l, h float64
	if len(dist) == 0 {
		l, h = -1, 1
	} else {
		l, h = dist[0].Bounds()
		for _, d := range dist[1:] {
			dl, dh := d.Bounds()
			l, h = math.Min(l, dl), math.Max(h, dh)
		}
	}
	xscale = makeScale(l, h, printXMargin, printWidth-printXMargin)
	//xscale.Src.Nice(10)
	src := xscale.Src.(*scale.Linear)
	xs = vec.Linspace(src.Min, src.Max, printSamples)
	return
}

func fprintScale(w io.Writer, sc scale.QQ) error {
	img := make([][]bool, printWidth)
	for i := range img {
		if i < printXMargin || i >= printWidth-printXMargin {
			img[i] = make([]bool, 2)
		} else {
			img[i] = []bool{true, false}
		}
	}
	major, _ := sc.Src.Ticks(scale.TickOptions{Max: 3})
	labels := make([]string, len(major))
	lpos := make([]int, len(major))
	for i, tick := range major {
		x := int(sc.Map(tick))
		img[x][1] = true
		// TODO: It would be nice if the scale could format
		// these ticks in a consistent way.
		labels[i] = fmt.Sprintf("%g", tick)
		width := len(labels[i])
		lpos[i] = minint(maxint(x/2-width/2, 0), (printWidth+1)/2-width)
	}
	if err := fprintImage(w, img, []string{""}); err != nil {
		return err
	}
	curpos := 0
	for i, label := range labels {
		gap := lpos[i] - curpos
		if i > 0 {
			gap = maxint(gap, 1)
		}
		_, err := fmt.Fprintf(w, "%*s%s", gap, "", label)
		if err != nil {
			return err
		}
		curpos += gap + len(label)
	}
	_, err := fmt.Fprintf(w, "\n")
	return err
}

func fprintFn(w io.Writer, fn func(float64) float64, xscale scale.QQ, xs []float64) error {
	ys := vec.Map(fn, xs)

	yl, yh := stats.Bounds(ys)
	if yl > 0 && yl-(yh-yl)*0.1 <= 0 {
		yl = 0
	}
	yscale := makeScale(yh, yl, printYMargin, printHeight-printYMargin)

	// Render the function to an image.
	img := make([][]bool, printWidth+2)
	for i := range img {
		img[i] = make([]bool, printHeight)
	}
	for i, x := range xs {
		img[int(xscale.Map(x))][int(yscale.Map(ys[i]))] = true
	}

	// Render Y axis.
	ypos := printWidth
	for y := printYMargin; y < printHeight-printYMargin; y++ {
		img[ypos][y] = true
	}
	img[ypos+1][printYMargin] = true
	img[ypos+1][len(img[0])-1-printYMargin] = true

	trail := make([]string, (printHeight+3)/4)
	trail[0] = fmt.Sprintf(" %4.3f", yh)
	trail[len(trail)-1] = fmt.Sprintf(" %4.3f", yl)

	return fprintImage(w, img, trail)
}

func fprintImage(w io.Writer, img [][]bool, trail []string) error {
	var x, y int
	bit := func(ox, oy int) byte {
		if x+ox < len(img) && y+oy < len(img[x+ox]) && img[x+ox][y+oy] {
			return 1
		}
		return 0
	}

	maxTrail := len(trail[0])
	for _, trail1 := range trail {
		maxTrail = maxint(maxTrail, len(trail1))
	}
	buf := make([]byte, 3*(len(img)+1)/2+maxTrail+1)
	for y = 0; y < len(img[0]); y += 4 {
		bufpos := 0
		for x = 0; x < len(img); x += 2 {
			// Grab the 2x4 cell of pixels and encode it
			// into a byte with the following bit layout:
			//  0 3
			//  1 4
			//  2 5
			//  6 7
			cell := bit(0, 0)<<0 | bit(1, 0)<<3
			cell |= bit(0, 1)<<1 | bit(1, 1)<<4
			cell |= bit(0, 2)<<2 | bit(1, 2)<<5
			cell |= bit(0, 3)<<6 | bit(1, 3)<<7
			// Translate cell into the Unicode Braille space.
			r := 0x2800 + rune(cell)
			bufpos += utf8.EncodeRune(buf[bufpos:], r)
		}
		bufpos += copy(buf[bufpos:], trail[y/4])
		buf[bufpos] = '\n'
		if _, err := w.Write(buf[:bufpos+1]); err != nil {
			return err
		}
	}
	return nil
}

// TODO: These should be exported by go-moremath.

func maxint(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minint(a, b int) int {
	if a < b {
		return a
	}
	return b
}
