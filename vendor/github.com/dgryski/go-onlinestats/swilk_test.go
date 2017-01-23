package onlinestats

import (
	"math/rand"
	"testing"
)

func TestSWilk(t *testing.T) {

	const l = 1000

	r := rand.New(rand.NewSource(1))

	zr := rand.NewZipf(r, 1.01, 1, 10000)

	zipf := make([]float64, l)
	for i := 0; i < l; i++ {
		zipf[i] = float64(zr.Uint64())
	}

	var w, pw float64
	var err error

	w, pw, err = SWilk(zipf)
	t.Logf("zipf: w=%f pw=%f err=%v", w, pw, err)

	// fly wing lengths in mm are normally distributed
	// via http://www.seattlecentral.edu/qelp/sets/057/057.html
	var wings = []float64{
		43, 48, 45, 48, 45, 39, 47, 43, 37, 46, 38, 47, 53, 43, 42, 44,
		51, 42, 48, 42, 36, 46, 44, 41, 50, 47, 47, 44, 45, 46, 46, 40,
		49, 40, 42, 45, 41, 51, 45, 44, 38, 50, 51, 41, 46, 49, 48, 47,
		40, 42, 44, 45, 47, 42, 45, 46, 47, 42, 46, 47, 39, 45, 40, 50,
		49, 52, 48, 45, 45, 54, 50, 41, 46, 48, 43, 43, 53, 41, 51, 46,
		41, 48, 43, 47, 43, 48, 43, 44, 50, 44, 52, 49, 44, 46, 55, 50,
		49, 44, 49, 49,
	}

	w, pw, err = SWilk(wings)
	t.Logf("wings: w=%f pw=%f err=%v", w, pw, err)
}
