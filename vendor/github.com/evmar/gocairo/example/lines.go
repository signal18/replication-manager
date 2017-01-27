// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Draws a figure with some lines, demonstrating paths and lines.
package main

import (
	"os"

	"github.com/martine/gocairo/cairo"
)

func main() {
	size := 320
	margin := 10
	innerSize := size - (margin * 2)
	step := 15

	surf := cairo.ImageSurfaceCreate(cairo.FormatARGB32, size, size)
	cr := cairo.Create(surf.Surface)

	cr.SetSourceRGB(1, 1, 1)
	cr.Paint()

	cr.SetAntialias(cairo.AntialiasBest)
	// Offset by 0.5 to get pixel-aligned lines.
	cr.Translate(float64(margin)+0.5, float64(margin)+0.5)
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(1)
	for i := 0; i <= innerSize; i += step {
		cr.MoveTo(0, float64(i))
		cr.LineTo(float64(i), float64(innerSize))
		cr.Stroke()

		cr.MoveTo(float64(i), 0)
		cr.LineTo(float64(innerSize), float64(i))
		cr.Stroke()
	}
	surf.Flush()

	f, err := os.Create("example.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	err = surf.WriteToPNG(f)
	if err != nil {
		panic(err)
	}
}
