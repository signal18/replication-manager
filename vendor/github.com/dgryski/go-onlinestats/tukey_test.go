package onlinestats

import (
	"reflect"
	"testing"
)

func TestTukey(t *testing.T) {

	data := []float64{5, 6, 7, 13, 43, 45, 46, 55, 56, 60, 61, 62, 65, 66, 66, 67, 90, 100, 104, 132}

	want := []float64{13, 43, 45, 46, 55, 56, 60, 61, 62, 65, 66, 66, 67, 90, 100}

	got := Tukey(data)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tukey()=%v, want %v\n", got, want)
	}
}
