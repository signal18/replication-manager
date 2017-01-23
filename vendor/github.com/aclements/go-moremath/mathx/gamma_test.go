// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mathx

import (
	"testing"

	. "github.com/aclements/go-moremath/internal/mathtest"
)

func TestGammaInc(t *testing.T) {
	WantFunc(t, "GammaInc(1, %v)",
		func(x float64) float64 { return GammaInc(1, x) },
		map[float64]float64{
			0.1: 0.095162581964040441,
			0.2: 0.18126924692201815,
			0.3: 0.25918177931828207,
			0.4: 0.32967995396436056,
			0.5: 0.39346934028736652,
			0.6: 0.45118836390597361,
			0.7: 0.50341469620859047,
			0.8: 0.55067103588277833,
			0.9: 0.59343034025940089,
			1:   0.63212055882855778,
			2:   0.86466471676338730,
			3:   0.95021293163213605,
			4:   0.98168436111126578,
			5:   0.99326205300091452,
			6:   0.99752124782333362,
			7:   0.99908811803444553,
			8:   0.99966453737209748,
			9:   0.99987659019591335,
			10:  0.99995460007023750,
		})
	WantFunc(t, "GammaInc(2, %v)",
		func(x float64) float64 { return GammaInc(2, x) },
		map[float64]float64{
			1:  0.26424111765711528,
			2:  0.59399415029016167,
			3:  0.80085172652854419,
			4:  0.90842180555632912,
			5:  0.95957231800548726,
			6:  0.98264873476333547,
			7:  0.99270494427556388,
			8:  0.99698083634887735,
			9:  0.99876590195913317,
			10: 0.99950060077261271,
		})

	// TODO: Test strange values.
}
