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
	"log"
	"os"

	"github.com/martine/gocairo/cairo"
)

func main() {
	log.Printf("cairo version %d/%s", cairo.Version(), cairo.VersionString())

	surf := cairo.ImageSurfaceCreate(cairo.FormatRGB24, 640, 480)
	cr := cairo.Create(surf.Surface)

	cr.SetSourceRGB(0, 0, 0)
	cr.Paint()

	cr.SetSourceRGB(1, 0, 0)
	cr.SelectFontFace("monospace", cairo.FontSlantNormal, cairo.FontWeightNormal)
	cr.SetFontSize(50)
	cr.MoveTo(640/10, 480/2)
	cr.ShowText("hello, world")

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
