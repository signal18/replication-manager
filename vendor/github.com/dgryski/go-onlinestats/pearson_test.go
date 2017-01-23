package onlinestats

import "testing"

func TestPearson(t *testing.T) {

	// From http://www.statisticshowto.com/how-to-compute-pearsons-correlation-coefficients/

	b := []float64{99, 65, 79, 75, 87, 81}
	a := []float64{43, 21, 25, 42, 57, 59}

	p := Pearson(a, b)

	t.Logf("Pearson(a,b)=%v\n", p)
}
