package expr

import (
	"testing"
)

func TestSortMetrics(t *testing.T) {
	const (
		gold   = "a.gold.c.d"
		silver = "a.silver.c.d"
		bronze = "a.bronze.c.d"
		first  = "a.first.c.d"
		second = "a.second.c.d"
		third  = "a.third.c.d"
		fourth = "a.fourth.c.d"
	)
	tests := []struct {
		metrics []*MetricData
		mfetch  MetricRequest
		sorted  []*MetricData
	}{
		{
			[]*MetricData{
				//NOTE(nnuss): keep these lines lexically sorted ;)
				makeResponse(bronze, []float64{}, 1, 0),
				makeResponse(first, []float64{}, 1, 0),
				makeResponse(fourth, []float64{}, 1, 0),
				makeResponse(gold, []float64{}, 1, 0),
				makeResponse(second, []float64{}, 1, 0),
				makeResponse(silver, []float64{}, 1, 0),
				makeResponse(third, []float64{}, 1, 0),
			},
			MetricRequest{
				Metric: "a.{first,second,third,fourth}.c.d",
				From:   0,
				Until:  1,
			},
			[]*MetricData{
				//These are in the brace appearance order
				makeResponse(first, []float64{}, 1, 0),
				makeResponse(second, []float64{}, 1, 0),
				makeResponse(third, []float64{}, 1, 0),
				makeResponse(fourth, []float64{}, 1, 0),

				//These are in the slice order as above and come after
				makeResponse(bronze, []float64{}, 1, 0),
				makeResponse(gold, []float64{}, 1, 0),
				makeResponse(silver, []float64{}, 1, 0),
			},
		},
	}
	for i, test := range tests {
		if len(test.metrics) != len(test.sorted) {
			t.Skipf("Error in test %d : length mismatch %d vs. %d", i, len(test.metrics), len(test.sorted))
		}
		SortMetrics(test.metrics, test.mfetch)
		for i := range test.metrics {
			if *test.metrics[i].Name != *test.sorted[i].Name {
				t.Errorf("[%d] Expected %q but have %q", i, *test.sorted[i].Name, *test.metrics[i].Name)
			}
		}
	}
}
