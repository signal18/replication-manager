package expr

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func TestJSONResponse(t *testing.T) {

	tests := []struct {
		results []*MetricData
		out     []byte
	}{
		{
			[]*MetricData{
				makeResponse("metric1", []float64{1, 1.5, 2.25, math.NaN()}, 100, 100),
				makeResponse("metric2", []float64{2, 2.5, 3.25, 4, 5}, 100, 100),
			},
			[]byte(`[{"target":"metric1","datapoints":[[1,100],[1.5,200],[2.25,300],[null,400]]},{"target":"metric2","datapoints":[[2,100],[2.5,200],[3.25,300],[4,400],[5,500]]}]`),
		},
	}

	for _, tt := range tests {
		b := MarshalJSON(tt.results)
		if !bytes.Equal(b, tt.out) {
			t.Errorf("marshalJSON(%+v)=%+v, want %+v", tt.results, string(b), string(tt.out))
		}
	}
}

func TestRawResponse(t *testing.T) {

	tests := []struct {
		results []*MetricData
		out     []byte
	}{
		{
			[]*MetricData{
				makeResponse("metric1", []float64{1, 1.5, 2.25, math.NaN()}, 100, 100),
				makeResponse("metric2", []float64{2, 2.5, 3.25, 4, 5}, 100, 100),
			},
			[]byte(`metric1,100,500,100|1,1.5,2.25,None` + "\n" + `metric2,100,600,100|2,2.5,3.25,4,5` + "\n"),
		},
	}

	for _, tt := range tests {
		b := MarshalRaw(tt.results)
		if !bytes.Equal(b, tt.out) {
			t.Errorf("marshalRaw(%+v)=%+v, want %+v", tt.results, string(b), string(tt.out))
		}
	}
}

func getData(rangeSize int) []float64 {
	var data = make([]float64, rangeSize)
	var r = rand.New(rand.NewSource(99))
	for i := range data {
		data[i] = math.Floor(1000 * r.Float64())
	}

	return data
}

func BenchmarkMarshalJSON(b *testing.B) {
	data := []*MetricData{
		makeResponse("metric1", getData(10000), 100, 100),
		makeResponse("metric2", getData(100000), 100, 100),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MarshalJSON(data)
	}
}
