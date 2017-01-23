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

package main

import (
	"github.com/martine/gocairo/cairo"
	"github.com/martine/gocairo/xlib"
)

type callbacks struct{}

func (c *callbacks) Draw(cr *cairo.Context, surf *cairo.XlibSurface) {
	w, h := surf.GetWidth(), surf.GetHeight()
	grid := 32

	cr.SetSourceRGB(0, 0, 0)
	cr.Paint()

	cr.SetAntialias(cairo.AntialiasBest)
	// Offset by 0.5 to get pixel-aligned lines.
	cr.Translate(0.5, 0.5)
	cr.SetSourceRGB(1, 0, 0)
	for x := 0; x <= w; x += grid {
		for y := 0; y <= h; y += grid {
			ofs := x/grid + y/grid
			cr.Rectangle(float64(x+ofs), float64(y+ofs),
				float64(grid-(2*ofs)), float64(grid-(2*ofs)))
			cr.Fill()
		}
	}
}

func main() {
	var callbacks callbacks
	xlib.XMain(&callbacks)
}
