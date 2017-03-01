// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package expr

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gogo/protobuf/proto"
	pb "github.com/tanji/replication-manager/graphite/carbonzipper/carbonzipperpb"
)

func deepClone(original map[MetricRequest][]*MetricData) map[MetricRequest][]*MetricData {
	clone := map[MetricRequest][]*MetricData{}
	for key, originalMetrics := range original {
		copiedMetrics := []*MetricData{}
		for _, originalMetric := range originalMetrics {
			copiedMetric := MetricData{
				FetchResponse: pb.FetchResponse{
					Name:      originalMetric.Name,
					StartTime: originalMetric.StartTime,
					StopTime:  originalMetric.StopTime,
					StepTime:  originalMetric.StepTime,
					Values:    make([]float64, len(originalMetric.Values)),
					IsAbsent:  make([]bool, len(originalMetric.IsAbsent)),
				},
			}

			copy(copiedMetric.Values, originalMetric.Values)
			copy(copiedMetric.IsAbsent, originalMetric.IsAbsent)
			copiedMetrics = append(copiedMetrics, &copiedMetric)
		}

		clone[key] = copiedMetrics
	}

	return clone
}

func deepEqual(t *testing.T, target string, original, modified map[MetricRequest][]*MetricData) {
	for key := range original {
		if len(original[key]) == len(modified[key]) {
			for i := range original[key] {
				if !reflect.DeepEqual(original[key][i], modified[key][i]) {
					t.Errorf(
						"%s: source data was modified key %v index %v original:\n%v\n modified:\n%v",
						target,
						key,
						i,
						original[key][i],
						modified[key][i],
					)
				}
			}
		} else {
			t.Errorf(
				"%s: source data was modified key %v original length %d, new length %d",
				target,
				key,
				len(original[key]),
				len(modified[key]),
			)
		}
	}
}

func TestGetBuckets(t *testing.T) {
	tests := []struct {
		start       int32
		stop        int32
		bucketSize  int32
		wantBuckets int32
	}{
		{13, 18, 5, 1},
		{13, 17, 5, 1},
		{13, 19, 5, 2},
	}

	for _, test := range tests {
		buckets := getBuckets(test.start, test.stop, test.bucketSize)
		if buckets != test.wantBuckets {
			t.Errorf("TestGetBuckets failed!\n%v\ngot buckets %d",
				test,
				buckets,
			)
		}
	}
}

func TestAlignToBucketSize(t *testing.T) {
	tests := []struct {
		inputStart int32
		inputStop  int32
		bucketSize int32
		wantStart  int32
		wantStop   int32
	}{
		{
			13, 18, 5,
			10, 20,
		},
		{
			13, 17, 5,
			10, 20,
		},
		{
			13, 19, 5,
			10, 20,
		},
	}

	for _, test := range tests {
		start, stop := alignToBucketSize(test.inputStart, test.inputStop, test.bucketSize)
		if start != test.wantStart || stop != test.wantStop {
			t.Errorf("TestAlignToBucketSize failed!\n%v\ngot start %d stop %d",
				test,
				start,
				stop,
			)
		}
	}
}

func TestAlignToInterval(t *testing.T) {
	tests := []struct {
		inputStart int32
		inputStop  int32
		bucketSize int32
		wantStart  int32
	}{
		{
			91111, 92222, 5,
			91111,
		},
		{
			91111, 92222, 60,
			91080,
		},
		{
			91111, 92222, 3600,
			90000,
		},
		{
			91111, 92222, 86400,
			86400,
		},
	}

	for _, test := range tests {
		start := alignStartToInterval(test.inputStart, test.inputStop, test.bucketSize)
		if start != test.wantStart {
			t.Errorf("TestAlignToInterval failed!\n%v\ngot start %d",
				test,
				start,
			)
		}
	}
}

func TestEvalExpr(t *testing.T) {
	exp, _, err := ParseExpr("summarize(metric1,'1min')")
	if err != nil {
		t.Errorf("error %s", err)
	}

	metricMap := make(map[MetricRequest][]*MetricData)
	request := MetricRequest{
		Metric: "metric1",
		From:   1437127020,
		Until:  1437127140,
	}

	stepTime := int32(60)

	data := MetricData{
		FetchResponse: pb.FetchResponse{
			Name:      &request.Metric,
			StartTime: &request.From,
			StopTime:  &request.Until,
			StepTime:  &stepTime,
			Values:    []float64{343, 407, 385},
			IsAbsent:  []bool{false, false, false},
		},
	}

	metricMap[request] = []*MetricData{
		&data,
	}

	EvalExpr(exp, int32(request.From), int32(request.Until), metricMap)
}

func TestParseExpr(t *testing.T) {

	tests := []struct {
		s string
		e *expr
	}{
		{"metric",
			&expr{target: "metric"},
		},
		{
			"metric.foo",
			&expr{target: "metric.foo"},
		},
		{"metric.*.foo",
			&expr{target: "metric.*.foo"},
		},
		{
			"func(metric)",
			&expr{
				target:    "func",
				etype:     etFunc,
				args:      []*expr{{target: "metric"}},
				argString: "metric",
			},
		},
		{
			"func(metric1,metric2,metric3)",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
		},
		{
			"func1(metric1,func2(metricA, metricB),metric3)",
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "func2",
						etype:     etFunc,
						args:      []*expr{{target: "metricA"}, {target: "metricB"}},
						argString: "metricA, metricB",
					},
					{target: "metric3"}},
				argString: "metric1,func2(metricA, metricB),metric3",
			},
		},

		{
			"3",
			&expr{val: 3, etype: etConst},
		},
		{
			"3.1",
			&expr{val: 3.1, etype: etConst},
		},
		{
			"func1(metric1, 3, 1e2, 2e-3)",
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 3, etype: etConst},
					{val: 100, etype: etConst},
					{val: 0.002, etype: etConst},
				},
				argString: "metric1, 3, 1e2, 2e-3",
			},
		},
		{
			"func1(metric1, 'stringconst')",
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "stringconst", etype: etString},
				},
				argString: "metric1, 'stringconst'",
			},
		},
		{
			`func1(metric1, "stringconst")`,
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "stringconst", etype: etString},
				},
				argString: `metric1, "stringconst"`,
			},
		},
		{
			"func1(metric1, -3)",
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: -3, etype: etConst},
				},
				argString: "metric1, -3",
			},
		},

		{
			"func1(metric1, -3 , 'foo' )",
			&expr{
				target: "func1",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: -3, etype: etConst},
					{valStr: "foo", etype: etString},
				},
				argString: "metric1, -3 , 'foo' ",
			},
		},

		{
			"func(metric, key='value')",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etString, valStr: "value"},
				},
				argString: "metric, key='value'",
			},
		},
		{
			"func(metric, key=true)",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etName, target: "true"},
				},
				argString: "metric, key=true",
			},
		},
		{
			"func(metric, key=1)",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etConst, val: 1},
				},
				argString: "metric, key=1",
			},
		},
		{
			"func(metric, key=0.1)",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etConst, val: 0.1},
				},
				argString: "metric, key=0.1",
			},
		},

		{
			"func(metric, 1, key='value')",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
					{etype: etConst, val: 1},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etString, valStr: "value"},
				},
				argString: "metric, 1, key='value'",
			},
		},
		{
			"func(metric, key='value', 1)",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
					{etype: etConst, val: 1},
				},
				namedArgs: map[string]*expr{
					"key": {etype: etString, valStr: "value"},
				},
				argString: "metric, key='value', 1",
			},
		},
		{
			"func(metric, key1='value1', key2='value2')",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key1": {etype: etString, valStr: "value1"},
					"key2": {etype: etString, valStr: "value2"},
				},
				argString: "metric, key1='value1', key2='value2'",
			},
		},
		{
			"func(metric, key2='value2', key1='value1')",
			&expr{
				target: "func",
				etype:  etFunc,
				args: []*expr{
					{target: "metric"},
				},
				namedArgs: map[string]*expr{
					"key2": {etype: etString, valStr: "value2"},
					"key1": {etype: etString, valStr: "value1"},
				},
				argString: "metric, key2='value2', key1='value1'",
			},
		},

		{
			`foo.{bar,baz}.qux`,
			&expr{
				target: "foo.{bar,baz}.qux",
				etype:  etName,
			},
		},
		{
			`foo.b[0-9].qux`,
			&expr{
				target: "foo.b[0-9].qux",
				etype:  etName,
			},
		},
		{
			`virt.v1.*.text-match:<foo.bar.qux>`,
			&expr{
				target: "virt.v1.*.text-match:<foo.bar.qux>",
				etype:  etName,
			},
		},
	}

	for _, tt := range tests {
		e, _, err := ParseExpr(tt.s)
		if err != nil {
			t.Errorf("parse for %+v failed: err=%v", tt.s, err)
			continue
		}
		if !reflect.DeepEqual(e, tt.e) {
			t.Errorf("parse for %+v failed:\ngot  %+s\nwant %+v", tt.s, spew.Sdump(e), spew.Sdump(tt.e))
		}
	}
}

func makeResponse(name string, values []float64, step, start int32) *MetricData {

	absent := make([]bool, len(values))

	for i, v := range values {
		if math.IsNaN(v) {
			values[i] = 0
			absent[i] = true
		}
	}

	stop := start + int32(len(values))*step

	return &MetricData{FetchResponse: pb.FetchResponse{
		Name:      proto.String(name),
		Values:    values,
		StartTime: proto.Int32(start),
		StepTime:  proto.Int32(step),
		StopTime:  proto.Int32(stop),
		IsAbsent:  absent,
	}}
}

type evalTestItem struct {
	e    *expr
	m    map[MetricRequest][]*MetricData
	want []*MetricData
}

func TestEvalExpression(t *testing.T) {

	now32 := int32(time.Now().Unix())

	tests := []evalTestItem{
		{
			&expr{target: "metric"},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric", 0, 1}: {makeResponse("metric", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("metric", []float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{target: "metric*"},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric2", []float64{2, 3, 4, 5, 6}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1", []float64{1, 2, 3, 4, 5}, 1, now32),
				makeResponse("metric2", []float64{2, 3, 4, 5, 6}, 1, now32),
			},
		},
		{
			&expr{
				target: "sum",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, 3, 4, 5, math.NaN()}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, 3, math.NaN(), 5, 6, math.NaN()}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, 4, 5, 6, math.NaN(), math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("sumSeries(metric1,metric2,metric3)", []float64{6, 9, 8, 15, 11, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "lowPass",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 40, etype: etConst},
				},
				argString: "metric1,40",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 1, now32)},
			},
			[]*MetricData{makeResponse("lowPass(metric1,40)", []float64{0, 1, math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), 8, 9}, 1, now32)},
		},
		{
			&expr{
				target: "countSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), 3, 4, 5, math.NaN()}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), math.NaN(), 5, 6, math.NaN()}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, math.NaN(), 5, 6, math.NaN(), math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("countSeries(metric1,metric2,metric3)", []float64{3, 3, 3, 3, 3, 3}, 1, now32)},
		},
		{
			&expr{
				target: "percentileOfSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 4, etype: etConst},
				},
				argString: "metric1,4",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("percentileOfSeries(metric1,4)", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "percentileOfSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 50, etype: etConst},
				},
				argString: "metric1.foo.*.*,50",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar1.qux", []float64{6, 7, 8, 9, 10, math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15, math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar2.qux", []float64{7, 8, 9, 10, 11, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("percentileOfSeries(metric1.foo.*.*,50)", []float64{7, 8, 9, 10, 11, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "percentileOfSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 50, etype: etConst},
				},
				namedArgs: map[string]*expr{
					"interpolate": {target: "true", etype: etName},
				},
				argString: "metric1.foo.*.*,50,interpolate=true",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar1.qux", []float64{6, 7, 8, 9, 10, math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15, math.NaN()}, 1, now32),
					makeResponse("metric1.foo.bar2.qux", []float64{7, 8, 9, 10, 11, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("percentileOfSeries(metric1.foo.*.*,50,interpolate=true)", []float64{6.5, 7.5, 8.5, 9.5, 11, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "nPercentile",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 50, etype: etConst},
				},
				argString: "metric1,50",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{2, 4, 6, 10, 14, 20, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("nPercentile(metric1,50)", []float64{8, 8, 8, 8, 8, 8, 8}, 1, now32)},
		},
		{
			&expr{
				target: "nonNegativeDerivative",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{2, 4, 6, 10, 14, 20}, 1, now32)},
			},
			[]*MetricData{makeResponse("nonNegativeDerivative(metric1)", []float64{math.NaN(), 2, 2, 4, 4, 6}, 1, now32)},
		},
		{
			&expr{
				target: "nonNegativeDerivative",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{2, 4, 6, 1, 4, math.NaN(), 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("nonNegativeDerivative(metric1)", []float64{math.NaN(), 2, 2, math.NaN(), 3, math.NaN(), math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "nonNegativeDerivative",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"maxValue": {val: 32, etype: etConst},
				},
				argString: "metric1,maxValue=32",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{2, 4, 0, 10, 1, math.NaN(), 8, 40, 37}, 1, now32)},
			},
			[]*MetricData{makeResponse("nonNegativeDerivative(metric1,32)", []float64{math.NaN(), 2, 29, 10, 24, math.NaN(), math.NaN(), 32, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "perSecond",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{27, 19, math.NaN(), 10, 1, 100, 1.5, 10.20}, 1, now32)},
			},
			[]*MetricData{makeResponse("perSecond(metric1)", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), 99, math.NaN(), 8.7}, 1, now32)},
		},
		{
			&expr{
				target: "perSecond",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 32, etype: etConst},
				},
				argString: "metric1,32",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{math.NaN(), 1, 2, 3, 4, 30, 0, 32, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("perSecond(metric1,32)", []float64{math.NaN(), math.NaN(), 1, 1, 1, 26, 3, 32, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "movingAverage",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 4, etype: etConst},
				},
				argString: "metric1,4",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("movingAverage(metric1,4)", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), 1, 1.25, 1.5, 1.75, 2.5, 3.5, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "movingMedian",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 4, etype: etConst},
				},
				argString: "metric1,4",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("movingMedian(metric1,4)", []float64{math.NaN(), math.NaN(), math.NaN(), 1, 1, 1.5, 2, 2, 3, 4, 5, 6}, 1, now32)},
		},
		{
			&expr{
				target: "movingMedian",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 5, etype: etConst},
				},
				argString: "metric1,5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, 1, 2, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("movingMedian(metric1,5)", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), 1, 1, 2, 2, 2, 4, 4, 6, 6, 4, 2}, 1, now32)},
		},
		{
			&expr{
				target: "movingMedian",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "1s", etype: etString},
				},
				argString: "metric1,1s",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", -1, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, 1, 2, 0}, 1, now32)},
			},
			[]*MetricData{makeResponse("movingMedian(metric1,\"1s\")", []float64{1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, 1, 2, 0}, 1, now32)},
		},
		{
			&expr{
				target: "movingMedian",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "3s", etype: etString},
				},
				argString: "metric1,3s",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", -3, 1}: {makeResponse("metric1", []float64{0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 4, 6, 4, 6, 8, 1, 2}, 1, now32)},
			},
			[]*MetricData{makeResponse("movingMedian(metric1,\"3s\")", []float64{0, 1, 1, 1, 1, 2, 2, 2, 4, 4, 6, 6, 6, 2}, 1, now32)},
		},
		{
			&expr{
				target: "pearson",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{val: 6, etype: etConst},
				},
				argString: "metric1,metric2,6",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{43, 21, 25, 42, 57, 59}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{99, 65, 79, 75, 87, 81}, 1, now32)},
			},
			[]*MetricData{makeResponse("pearson(metric1,metric2,6)", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), 0.5298089018901744}, 1, now32)},
		},
		{
			&expr{
				target: "scale",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 2.5, etype: etConst},
				},
				argString: "metric1,2.5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, math.NaN(), 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("scale(metric1,2.5)", []float64{2.5, 5.0, math.NaN(), 10.0, 12.5}, 1, now32)},
		},
		{
			&expr{
				target: "scaleToSeconds",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 5, etype: etConst},
				},
				argString: "metric1,5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{60, 120, math.NaN(), 120, 120}, 60, now32)},
			},
			[]*MetricData{makeResponse("scaleToSeconds(metric1,5)", []float64{5, 10, math.NaN(), 10, 10}, 1, now32)},
		},
		{
			&expr{
				target: "pow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 3, etype: etConst},
				},
				argString: "metric1,3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{5, 1, math.NaN(), 0, 12, 125, 10.4, 1.1}, 60, now32)},
			},
			[]*MetricData{makeResponse("pow(metric1,3)", []float64{125, 1, math.NaN(), 0, 1728, 1953125, 1124.864, 1.331}, 1, now32)},
		},
		{
			&expr{
				target: "keepLastValue",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"limit": {val: 3, etype: etConst},
				},
				argString: "metric1,limit=3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{math.NaN(), 2, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("keepLastValue(metric1,3)", []float64{math.NaN(), 2, 2, 2, 2, math.NaN(), 4, 5}, 1, now32)},
		},

		{
			&expr{
				target: "keepLastValue",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{math.NaN(), 2, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("keepLastValue(metric1)", []float64{math.NaN(), 2, 2, 2, 2, 2, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "keepLastValue",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
				},
				argString: "metric*",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), 4, 5}, 1, now32),
					makeResponse("metric2", []float64{math.NaN(), 2, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 4, 5}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("keepLastValue(metric1)", []float64{1, 1, 1, 1, 1, 1, 4, 5}, 1, now32),
				makeResponse("keepLastValue(metric2)", []float64{math.NaN(), 2, 2, 2, 2, 2, 4, 5}, 1, now32),
			},
		},
		{
			&expr{
				target: "changed",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), 0, 0, 0, math.NaN(), math.NaN(), 1, 1, 2, 3, 4, 4, 5, 5, 5, 6, 7}, 1, now32)},
			},
			[]*MetricData{makeResponse("changed(metric1)",
				[]float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1, 0, 0, 1, 1}, 1, now32)},
		},
		{
			&expr{
				target: "alias",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "renamed", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("renamed",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasByMetric",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
					{val: 1, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("foo", []float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
					{val: 1, etype: etConst},
					{val: 3, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("foo.baz",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
					{val: 1, etype: etConst},
					{val: -2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("foo.bar",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "substr",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
					{val: 1, etype: etConst},
					{val: 3, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("foo.bar",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasSub",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.bar.baz"},
					{valStr: "foo", etype: etString},
					{valStr: "replaced", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.bar.baz", 0, 1}: {makeResponse("metric1.foo.bar.baz", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("metric1.replaced.bar.baz",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasSub",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.TCP100"},
					{valStr: "^.*TCP(\\d+)", etype: etString},
					{valStr: "$1", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.TCP100", 0, 1}: {makeResponse("metric1.TCP100", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("100",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "aliasSub",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.TCP100"},
					{valStr: "^.*TCP(\\d+)", etype: etString},
					{valStr: "\\1", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.TCP100", 0, 1}: {makeResponse("metric1.TCP100", []float64{1, 2, 3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("100",
				[]float64{1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "derivative",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{2, 4, 6, 1, 4, math.NaN(), 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("derivative(metric1)",
				[]float64{math.NaN(), 2, 2, -5, 3, math.NaN(), 4}, 1, now32)},
		},
		{
			&expr{
				target: "avg",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), 2, 3, 4, 5}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 5, 6}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, math.NaN(), 4, 5, 6, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("averageSeries(metric1,metric2,metric3)",
				[]float64{2, math.NaN(), 3, 4, 5, 5.5}, 1, now32)},
		},
		{
			&expr{
				target: "maxSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), 2, 3, 4, 5}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 5, 6}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, math.NaN(), 4, 5, 6, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("maxSeries(metric1,metric2,metric3)",
				[]float64{3, math.NaN(), 4, 5, 6, 6}, 1, now32)},
		},
		{
			&expr{
				target: "minSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"}},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), 2, 3, 4, 5}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 5, 6}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, math.NaN(), 4, 5, 6, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("minSeries(metric1,metric2,metric3)",
				[]float64{1, math.NaN(), 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "asPercent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
				},
				argString: "metric1,metric2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
			},
			[]*MetricData{makeResponse("asPercent(metric1,metric2)",
				[]float64{50, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 200}, 1, now32)},
		},
		{
			&expr{
				target: "divideSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
				},
				argString: "metric1,metric2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
			},
			[]*MetricData{makeResponse("divideSeries(metric1,metric2)",
				[]float64{0.5, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 2}, 1, now32)},
		},
		{
			&expr{
				target: "divideSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric[12]"},
				},
				argString: "metric[12]",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric[12]", 0, 1}: {
					makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32),
					makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("divideSeries(metric[12])",
				[]float64{0.5, math.NaN(), math.NaN(), math.NaN(), math.NaN(), 2}, 1, now32)},
		},
		{
			&expr{
				target: "multiplySeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
				},
				argString: "metric1,metric2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
			},
			[]*MetricData{makeResponse("multiplySeries(metric1,metric2)",
				[]float64{2, math.NaN(), math.NaN(), math.NaN(), 0, 72}, 1, now32)},
		},
		{
			&expr{
				target: "multiplySeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
				},
				argString: "metric[12]",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
			},
			[]*MetricData{makeResponse("multiplySeries(metric[12])",
				[]float64{2, math.NaN(), math.NaN(), math.NaN(), 0, 72}, 1, now32)},
		},
		{
			&expr{
				target: "multiplySeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"},
				},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{3, math.NaN(), 4, math.NaN(), 7, 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("multiplySeries(metric1,metric2,metric3)",
				[]float64{6, math.NaN(), math.NaN(), math.NaN(), 0, 576}, 1, now32)},
		},
		{
			&expr{
				target: "diffSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
				},
				argString: "metric1,metric2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32)},
			},
			[]*MetricData{makeResponse("diffSeries(metric1,metric2)",
				[]float64{-1, math.NaN(), math.NaN(), 3, 4, 6}, 1, now32)},
		},
		{
			&expr{
				target: "diffSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{target: "metric3"},
				},
				argString: "metric1,metric2,metric3",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{5, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
				MetricRequest{"metric2", 0, 1}: {makeResponse("metric2", []float64{3, math.NaN(), 3, math.NaN(), 0, 7}, 1, now32)},
				MetricRequest{"metric3", 0, 1}: {makeResponse("metric3", []float64{1, math.NaN(), 3, math.NaN(), 0, 4}, 1, now32)},
			},
			[]*MetricData{makeResponse("diffSeries(metric1,metric2,metric3)",
				[]float64{1, math.NaN(), math.NaN(), 3, 4, 1}, 1, now32)},
		},
		{
			&expr{
				target: "diffSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
				},
				argString: "metric*",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32),
					makeResponse("metric2", []float64{2, math.NaN(), 3, math.NaN(), 0, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("diffSeries(metric*)",
				[]float64{-1, math.NaN(), math.NaN(), 3, 4, 6}, 1, now32)},
		},
		{
			&expr{
				target: "rangeOfSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
				},
				argString: "metric*",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{math.NaN(), math.NaN(), math.NaN(), 3, 4, 12, -10}, 1, now32),
					makeResponse("metric2", []float64{2, math.NaN(), math.NaN(), 15, 0, 6, 10}, 1, now32),
					makeResponse("metric3", []float64{1, 2, math.NaN(), 4, 5, 6, 7}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("rangeOfSeries(metric*)",
				[]float64{1, math.NaN(), math.NaN(), 12, 5, 6, 20}, 1, now32)},
		},
		{
			&expr{
				target: "transformNull",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
			},
			[]*MetricData{makeResponse("transformNull(metric1)",
				[]float64{1, 0, 0, 3, 4, 12}, 1, now32)},
		},
		{
			&expr{
				target: "transformNull",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"default": {val: 5, etype: etConst},
				},
				argString: "metric1,default=5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, math.NaN(), math.NaN(), 3, 4, 12}, 1, now32)},
			},
			[]*MetricData{makeResponse("transformNull(metric1,5)",
				[]float64{1, 5, 5, 3, 4, 12}, 1, now32)},
		},
		{
			&expr{
				target: "highestMax",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 1, etype: etConst},
				},
				argString: "metric1,1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{1, 1, 3, 3, 12, 11}, 1, now32),
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 10}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricA", // NOTE(dgryski): not sure if this matches graphite
				[]float64{1, 1, 3, 3, 12, 11}, 1, now32)},
		},
		{
			&expr{
				target: "lowestCurrent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 1, etype: etConst},
				},
				argString: "metric1,1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricB", // NOTE(dgryski): not sure if this matches graphite
				[]float64{1, 1, 3, 3, 4, 1}, 1, now32)},
		},
		{
			&expr{
				target: "highestCurrent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 1, etype: etConst},
				},
				argString: "metric1,1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric0", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricC", // NOTE(dgryski): not sure if this matches graphite
				[]float64{1, 1, 3, 3, 4, 15}, 1, now32)},
		},
		{
			&expr{
				target: "highestCurrent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 4, etype: etConst},
				},
				argString: "metric1,4",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric0", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
				makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
				makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
				//NOTE(nnuss): highest* functions filter null-valued series as a side-effect when `n` >= number of series
				//TODO(nnuss): bring lowest* functions into harmony with this side effect or get rid of it
				//makeResponse("metric0", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "highestAverage",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 1, etype: etConst},
				},
				argString: "metric1,1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
					makeResponse("metricB", []float64{1, 5, 5, 5, 5, 5}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 10}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricB", // NOTE(dgryski): not sure if this matches graphite
				[]float64{1, 5, 5, 5, 5, 5}, 1, now32)},
		},
		{
			&expr{
				target: "exclude",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "(Foo|Baz)", etype: etString},
				},
				argString: "metric1,'(Foo|Baz)'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricFoo", []float64{1, 1, 1, 1, 1}, 1, now32),
					makeResponse("metricBar", []float64{2, 2, 2, 2, 2}, 1, now32),
					makeResponse("metricBaz", []float64{3, 3, 3, 3, 3}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricBar", // NOTE(dgryski): not sure if this matches graphite
				[]float64{2, 2, 2, 2, 2}, 1, now32)},
		},
		{
			&expr{
				target: "ewma",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 0.1, etype: etConst},
				},
				argString: "metric1,0.1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{0, 1, 1, 1, math.NaN(), 1, 1}, 1, now32)},
			},
			[]*MetricData{
				makeResponse("ewma(metric1,0.1)", []float64{0, 0.9, 0.99, 0.999, math.NaN(), 0.9999, 0.99999}, 1, now32),
			},
		},
		{
			&expr{
				target: "exponentialWeightedMovingAverage",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 0.1, etype: etConst},
				},
				argString: "metric1,0.1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{0, 1, 1, 1, math.NaN(), 1, 1}, 1, now32)},
			},
			[]*MetricData{
				makeResponse("ewma(metric1,0.1)", []float64{0, 0.9, 0.99, 0.999, math.NaN(), 0.9999, 0.99999}, 1, now32),
			},
		},
		{
			&expr{
				target: "grep",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "Bar", etype: etString},
				},
				argString: "metric1,'Bar'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricFoo", []float64{1, 1, 1, 1, 1}, 1, now32),
					makeResponse("metricBar", []float64{2, 2, 2, 2, 2}, 1, now32),
					makeResponse("metricBaz", []float64{3, 3, 3, 3, 3}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricBar", // NOTE(dgryski): not sure if this matches graphite
				[]float64{2, 2, 2, 2, 2}, 1, now32)},
		},
		{
			&expr{
				target: "logarithm",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 10, 100, 1000, 10000}, 1, now32)},
			},
			[]*MetricData{makeResponse("logarithm(metric1)",
				[]float64{0, 1, 2, 3, 4}, 1, now32)},
		},
		{
			&expr{
				target: "logarithm",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"base": {val: 2, etype: etConst},
				},
				argString: "metric1,base=2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, 4, 8, 16, 32}, 1, now32)},
			},
			[]*MetricData{makeResponse("logarithm(metric1,2)",
				[]float64{0, 1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "absolute",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{0, -1, 2, -3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("absolute(metric1)",
				[]float64{0, 1, 2, 3, 4, 5}, 1, now32)},
		},
		{
			&expr{
				target: "isNonNull",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{math.NaN(), -1, math.NaN(), -3, 4, 5}, 1, now32)},
			},
			[]*MetricData{makeResponse("isNonNull(metric1)",
				[]float64{0, 1, 0, 1, 1, 1}, 1, now32)},
		},
		{
			&expr{
				target: "isNonNull",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricFoo", []float64{math.NaN(), -1, math.NaN(), -3, 4, 5}, 1, now32),
					makeResponse("metricBaz", []float64{1, -1, math.NaN(), -3, 4, 5}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("isNonNull(metricFoo)", []float64{0, 1, 0, 1, 1, 1}, 1, now32),
				makeResponse("isNonNull(metricBaz)", []float64{1, 1, 0, 1, 1, 1}, 1, now32),
			},
		},
		{
			&expr{
				target: "averageAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 5, etype: etConst},
				},
				argString: "metric1,5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
				makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
			},
		},
		{
			&expr{
				target: "averageBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 0, etype: etConst},
				},
				argString: "metric1,0",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{0, 4, 4, 5, 5, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricA",
				[]float64{0, 0, 0, 0, 0, 0}, 1, now32)},
		},
		{
			&expr{
				target: "maximumAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 6, etype: etConst},
				},
				argString: "metric1,6",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricB",
				[]float64{3, 4, 5, 6, 7, 8}, 1, now32)},
		},
		{
			&expr{
				target: "maximumBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 5, etype: etConst},
				},
				argString: "metric1,5",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricA",
				[]float64{0, 0, 0, 0, 0, 0}, 1, now32)},
		},
		{
			&expr{
				target: "minimumAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 1, etype: etConst},
				},
				argString: "metric1,1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{1, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{2, 4, 4, 5, 5, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricC",
				[]float64{2, 4, 4, 5, 5, 6}, 1, now32)},
		},
		{
			&expr{
				target: "minimumBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: -2, etype: etConst},
				},
				argString: "metric1,-2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{-1, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{-2, 4, 4, 5, 5, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricC",
				[]float64{-2, 4, 4, 5, 5, 6}, 1, now32)},
		},
		{
			&expr{
				target: "pearsonClosest",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{target: "metric2"},
					{val: 1, etype: etConst},
				},
				namedArgs: map[string]*expr{
					"direction": {valStr: "abs", etype: etString},
				},
				argString: "metric1,metric2,1,direction='abs'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricX", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
				},
				MetricRequest{"metric2", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, math.NaN(), 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricB",
				[]float64{3, math.NaN(), 5, 6, 7, 8}, 1, now32)},
		},
		{
			&expr{
				target: "invert",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{-4, -2, -1, 0, 1, 2, 4}, 1, now32)},
			},
			[]*MetricData{makeResponse("invert(metric1)",
				[]float64{-0.25, -0.5, -1, math.NaN(), 1, 0.5, 0.25}, 1, now32)},
		},
		{
			&expr{
				target: "offset",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 10, etype: etConst},
				},
				argString: "metric1,10",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{93, 94, 95, math.NaN(), 97, 98, 99, 100, 101}, 1, now32)},
			},
			[]*MetricData{makeResponse("offset(metric1,10)",
				[]float64{103, 104, 105, math.NaN(), 107, 108, 109, 110, 111}, 1, now32)},
		},
		{
			&expr{
				target: "offsetToZero",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{93, 94, 95, math.NaN(), 97, 98, 99, 100, 101}, 1, now32)},
			},
			[]*MetricData{makeResponse("offsetToZero(metric1)",
				[]float64{0, 1, 2, math.NaN(), 4, 5, 6, 7, 8}, 1, now32)},
		},
		{
			&expr{
				target: "currentAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 7, etype: etConst},
				},
				argString: "metric1,7",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricB",
				[]float64{3, 4, 5, 6, 7, 8}, 1, now32)},
		},
		{
			&expr{
				target: "currentBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 0, etype: etConst},
				},
				argString: "metric1,0",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, math.NaN()}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{0, 4, 4, 5, 5, 6}, 1, now32),
				},
			},
			[]*MetricData{makeResponse("metricA",
				[]float64{0, 0, 0, 0, 0, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "integral",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 2, 3, 4, 5, math.NaN(), 7, 8}, 1, now32)},
			},
			[]*MetricData{makeResponse("integral(metric1)",
				[]float64{1, 1, 3, 6, 10, 15, math.NaN(), 22, 30}, 1, now32)},
		},
		{
			&expr{
				target: "sortByTotal",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{5, 5, 5, 5, 5, 5}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 4, 4}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricB", []float64{5, 5, 5, 5, 5, 5}, 1, now32),
				makeResponse("metricC", []float64{4, 4, 5, 5, 4, 4}, 1, now32),
				makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
			},
		},
		{
			&expr{
				target: "sortByMaxima",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{5, 5, 5, 5, 5, 5}, 1, now32),
					makeResponse("metricC", []float64{2, 2, 10, 5, 2, 2}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricC", []float64{2, 2, 10, 5, 2, 2}, 1, now32),
				makeResponse("metricB", []float64{5, 5, 5, 5, 5, 5}, 1, now32),
				makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
			},
		},
		{
			&expr{
				target: "sortByMinima",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
				makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
				makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
			},
		},
		{
			&expr{
				target: "sortByName",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricX", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{0, 0, 2, 0, 0, 0}, 1, now32),
					makeResponse("metricC", []float64{0, 0, 0, 3, 0, 0}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
				makeResponse("metricB", []float64{0, 0, 2, 0, 0, 0}, 1, now32),
				makeResponse("metricC", []float64{0, 0, 0, 3, 0, 0}, 1, now32),
				makeResponse("metricX", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
			},
		},
		{
			&expr{
				target: "sortByName",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"natural": {target: "true", etype: etName},
				},
				argString: "metric1,natural=true",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metric12", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
					makeResponse("metric1234567890", []float64{0, 0, 0, 5, 0, 0}, 1, now32),
					makeResponse("metric2", []float64{0, 0, 2, 0, 0, 0}, 1, now32),
					makeResponse("metric11", []float64{0, 0, 0, 3, 0, 0}, 1, now32),
					makeResponse("metric", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
				makeResponse("metric1", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
				makeResponse("metric2", []float64{0, 0, 2, 0, 0, 0}, 1, now32),
				makeResponse("metric11", []float64{0, 0, 0, 3, 0, 0}, 1, now32),
				makeResponse("metric12", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
				makeResponse("metric1234567890", []float64{0, 0, 0, 5, 0, 0}, 1, now32),
			},
		},
		{
			&expr{
				target: "constantLine",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
				},
				argString: "42.42",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"42.42", 0, 1}: {makeResponse("constantLine", []float64{12.3, 12.3}, 1, now32)},
			},
			[]*MetricData{makeResponse("42.42",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "squareRoot",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, 0, 7, 8, 20, 30, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("squareRoot(metric1)",
				[]float64{1, 1.4142135623730951, 0, 2.6457513110645907, 2.8284271247461903, 4.47213595499958, 5.477225575051661, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "removeEmptySeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
				},
				argString: "metric*",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32),
					makeResponse("metric2", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metric3", []float64{0, 0, 0, 0, 0, 0, 0, 0}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32),
				makeResponse("metric3", []float64{0, 0, 0, 0, 0, 0, 0, 0}, 1, now32),
			},
		},
		{
			&expr{
				target: "removeZeroSeries",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
				},
				argString: "metric*",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32),
					makeResponse("metric2", []float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
					makeResponse("metric3", []float64{0, 0, 0, 0, 0, 0, 0, 0}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "removeBelowValue",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 0, etype: etConst},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("removeBelowValue(metric1, 0)",
				[]float64{1, 2, math.NaN(), 7, 8, 20, 30, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "removeAboveValue",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 10, etype: etConst},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("removeAboveValue(metric1, 10)",
				[]float64{1, 2, -1, 7, 8, math.NaN(), math.NaN(), math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "removeBelowPercentile",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 50, etype: etConst},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("removeBelowPercentile(metric1, 50)",
				[]float64{math.NaN(), math.NaN(), math.NaN(), 7, 8, 20, 30, math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "removeAbovePercentile",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 50, etype: etConst},
				},
				argString: "metric1",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 2, -1, 7, 8, 20, 30, math.NaN()}, 1, now32)},
			},
			[]*MetricData{makeResponse("removeAbovePercentile(metric1, 50)",
				[]float64{1, 2, -1, 7, math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32)},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{math.NaN(), 20531.733333333334, 20196.4, 17925.333333333332, 20950.4, 35168.13333333333, 19965.866666666665, 24556.4, 22266.4, 58039.86666666667}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 58k Max: 58k Min: 18k",
					[]float64{math.NaN(), 20531.733333333334, 20196.4, 17925.333333333332, 20950.4, 35168.13333333333, 19965.866666666665, 24556.4, 22266.4, 58039.86666666667}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{1.432729, 1.434207, 1.404762, 1.414609, 1.399159, 1.411343, 1.406217, 1.407123, 1.392078, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 1 Max: 1 Min: 1",
					[]float64{1.432729, 1.434207, 1.404762, 1.414609, 1.399159, 1.411343, 1.406217, 1.407123, 1.392078, math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
					{valStr: "carrot", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{1.432729, 1.434207, 1.404762, 1.414609, 1.399159, 1.411343, 1.406217, 1.407123, 1.392078, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 1 carrot Max: 1 carrot Min: 1 carrot",
					[]float64{1.432729, 1.434207, 1.404762, 1.414609, 1.399159, 1.411343, 1.406217, 1.407123, 1.392078, math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{math.NaN(), 88364212.53333333, 79008410.93333334, 80312920.0, 69860465.2, 83876830.0, 80399148.8, 90481297.46666667, 79628113.73333333, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 80M Max: 90M Min: 70M",
					[]float64{math.NaN(), 88364212.53333333, 79008410.93333334, 80312920.0, 69860465.2, 83876830.0, 80399148.8, 90481297.46666667, 79628113.73333333, math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{1000}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 1k Max: 1k Min: 1k",
					[]float64{1000}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{1000}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 1000 Max: 1000 Min: 1000",
					[]float64{1000}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
				namedArgs: map[string]*expr{
					"units": {etype: etString, valStr: "apples"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{10}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 10 apples Max: 10 apples Min: 10 apples",
					[]float64{10}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 240 Max: 240 Min: 240",
					[]float64{240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, 240.0, math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{-1.0, -2.0, -1.0, -3.0, -1.0, -1.0, -0.0, -0.0, -0.0}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: 0 Max: 0 Min: -3",
					[]float64{-1.0, -2.0, -1.0, -3.0, -1.0, -1.0, -0.0, -0.0, -0.0}, 1, now32),
			},
		},
		{
			&expr{
				target: "cactiStyle",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "si", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("metric1 Current: NaN Max: NaN Min: NaN",
					[]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "polyfit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 3, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("polyfit(metric1,3)",
					[]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32),
			},
		},
		{
			&expr{
				target: "polyfit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{7.79, 7.7, 7.92, 5.25, 6.24, 7.25, 7.15, 8.56, 7.82, 8.52}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("polyfit(metric1)",
					[]float64{6.94763636364, 7.05260606061, 7.15757575758, 7.26254545455, 7.36751515152,
						7.47248484848, 7.57745454545, 7.68242424242, 7.78739393939, 7.89236363636}, 1, now32),
			},
		},
		{
			&expr{
				target: "polyfit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{7.79, 7.7, 7.92, 5.25, 6.24, math.NaN(), 7.15, 8.56, 7.82, 8.52}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("polyfit(metric1,2)",
					[]float64{7.9733096590909085, 7.364842329545457, 6.933910511363642, 6.680514204545464, 6.604653409090922,
						6.706328125000017, 6.985538352272748, 7.442284090909116, 8.07656534090912, 8.888382102272761}, 1, now32),
			},
		},
		{
			&expr{
				target: "polyfit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 3, etype: etConst},
					{valStr: "5sec", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metric1",
						[]float64{7.79, 7.7, 7.92, 5.25, 6.24, 7.25, 7.15, 8.56, 7.82, 8.52}, 1, now32),
				},
			},
			[]*MetricData{
				makeResponse("polyfit(metric1,3,'5sec')",
					[]float64{8.22944055944, 7.26958041958, 6.73364801865, 6.54653846154, 6.63314685315,
						6.91836829837, 7.3270979021, 7.78423076923, 8.21466200466, 8.54328671329,
						8.695, 8.5946969697, 8.16727272727, 7.33762237762, 6.03064102564}, 1, now32),
			},
		},
	}

	for _, tt := range tests {
		testEvalExpr(t, &tt)
	}
}

func testEvalExpr(t *testing.T, tt *evalTestItem) {
	originalMetrics := deepClone(tt.m)
	testName := tt.e.target + "(" + tt.e.argString + ")"
	g, err := EvalExpr(tt.e, 0, 1, tt.m)
	if err != nil {
		t.Errorf("failed to eval %s: %s", testName, err)
		return
	}
	if len(g) != len(tt.want) {
		t.Errorf("%s returned a different number of metrics, actual %v, want %v", testName, len(g), len(tt.want))
		return

	}
	deepEqual(t, testName, originalMetrics, tt.m)

	for i, want := range tt.want {
		actual := g[i]
		if actual == nil {
			t.Errorf("returned no value %v", tt.e.argString)
			return
		}
		if actual.GetStepTime() == 0 {
			t.Errorf("missing step for %+v", g)
		}
		if actual.GetName() != want.GetName() {
			t.Errorf("bad name for %s metric %d: got %s, want %s", testName, i, actual.GetName(), want.GetName())
		}
		if !nearlyEqualMetrics(actual, want) {
			t.Errorf("different values for %s metric %s: got %v, want %v", testName, actual.GetName(), actual.Values, want.Values)
			return
		}
	}
}

func TestEvalSummarize(t *testing.T) {

	t0, err := time.Parse(time.UnixDate, "Wed Sep 10 10:32:00 CEST 2014")
	if err != nil {
		panic(err)
	}

	tenThirtyTwo := int32(t0.Unix())

	t0, err = time.Parse(time.UnixDate, "Wed Sep 10 10:59:00 CEST 2014")
	if err != nil {
		panic(err)
	}

	tenFiftyNine := int32(t0.Unix())

	t0, err = time.Parse(time.UnixDate, "Wed Sep 10 10:30:00 CEST 2014")
	if err != nil {
		panic(err)
	}

	tenThirty := int32(t0.Unix())

	now32 := tenThirty

	tests := []struct {
		e     *expr
		m     map[MetricRequest][]*MetricData
		w     []float64
		name  string
		step  int32
		start int32
		stop  int32
	}{
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
				},
				argString: "metric1,'5s'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1,
					2, 2, 2, 2, 2,
					3, 3, 3, 3, 3,
					4, 4, 4, 4, 4,
					5, 5, 5, 5, 5,
					math.NaN(), 2, 3, 4, 5,
					math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(),
				}, 1, now32)},
			},
			[]float64{5, 10, 15, 20, 25, 14, math.NaN()},
			"summarize(metric1,'5s')",
			5,
			now32,
			now32 + 35,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
				},
				namedArgs: map[string]*expr{
					"func": {valStr: "avg", etype: etString},
				},
				argString: "metric1,'5s',func='avg'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 5, 5, 5, 5, 5, 1, 2, 3, math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}, 1, now32)},
			},
			[]float64{1, 2, 3, 4, 5, 2, math.NaN()},
			"summarize(metric1,'5s','avg')",
			5,
			now32,
			now32 + 35,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
				},
				namedArgs: map[string]*expr{
					"func": {valStr: "max", etype: etString},
				},
				argString: "metric1,'5s',func='max'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{1, 2, 3, 4.5, 5},
			"summarize(metric1,'5s','max')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
				},
				namedArgs: map[string]*expr{
					"func": {valStr: "min", etype: etString},
				},
				argString: "metric1,'5s',func='min'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{0, 1, 1.5, 2, 5},
			"summarize(metric1,'5s','min')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
				},
				namedArgs: map[string]*expr{
					"func": {valStr: "last", etype: etString},
				},
				argString: "metric1,'5s',func='last'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{1, 2, 3, 4.5, 5},
			"summarize(metric1,'5s','last')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
					{valStr: "p50", etype: etString},
				},
				argString: "metric1,'5s','p50'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{0.5, 1.5, 2, 3, 5},
			"summarize(metric1,'5s','p50')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
					{valStr: "p25", etype: etString},
				},
				argString: "metric1,'5s','p25'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{0, 1, 2, 3, 5},
			"summarize(metric1,'5s','p25')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
					{valStr: "p99.9", etype: etString},
				},
				argString: "metric1,'5s','p99.9'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{1, 2, 3, 4.498, 5},
			"summarize(metric1,'5s','p99.9')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "5s", etype: etString},
					{valStr: "p100.1", etype: etString},
				},
				argString: "metric1,'5s','p100.1'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()},
			"summarize(metric1,'5s','p100.1')",
			5,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "1s", etype: etString},
					{valStr: "p50", etype: etString},
				},
				argString: "metric1,'1s','p50'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5}, 1, now32)},
			},
			[]float64{1, 0, 0, 0.5, 1, 2, 1, 1, 1.5, 2, 3, 2, 2, 1.5, 3, 4, 3, 2, 3, 4.5, 5, 5, 5, 5, 5},
			"summarize(metric1,'1s','p50')",
			1,
			now32,
			now32 + 25*1,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "10min", etype: etString},
				},
				argString: "metric1,'10min'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2,
					3, 3, 3, 3, 3, 4, 4, 4, 4, 4,
					5, 5, 5, 5, 5}, 60, tenThirtyTwo)},
			},
			[]float64{11, 31, 33},
			"summarize(metric1,'10min')",
			600,
			tenThirty,
			tenThirty + 30*60,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "10min", etype: etString},
				},
				namedArgs: map[string]*expr{
					"alignToFrom": {target: "true", etype: etName},
					"func":        {valStr: "sum", etype: etString},
				},
				argString: "metric1,'10min',alignToFrom=true,func='sum'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2,
					3, 3, 3, 3, 3, 4, 4, 4, 4, 4,
					5, 5, 5, 5, 5}, 60, tenThirtyTwo)},
			},
			[]float64{15, 35, 25},
			"summarize(metric1,'10min','sum',true)",
			600,
			tenThirtyTwo,
			tenThirtyTwo + 25*60,
		},
		{
			&expr{
				target: "summarize",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "10min", etype: etString},
				},
				namedArgs: map[string]*expr{
					"alignToFrom": {target: "true", etype: etName},
				},
				argString: "metric1,'10min',alignToFrom=true",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2,
					3, 3, 3, 3, 3, 4, 4, 4, 4, 4,
					5, 5, 5, 5, 5}, 60, tenThirtyTwo)},
			},
			[]float64{15, 35, 25},
			"summarize(metric1,'10min','sum',true)",
			600,
			tenThirtyTwo,
			tenThirtyTwo + 25*60,
		},
		{
			&expr{
				target: "hitcount",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "30s", etype: etString},
				},
				argString: "metric1,'30s'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2,
					2, 2, 2, 2, 3, 3,
					3, 3, 3, 4, 4, 4,
					4, 4, 5, 5, 5, 5,
					math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(),
					5}, 5, now32)},
			},
			[]float64{35, 70, 105, 140, math.NaN(), 25},
			"hitcount(metric1,'30s')",
			30,
			now32,
			now32 + 31*5,
		},
		{
			&expr{
				target: "hitcount",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "1h", etype: etString},
				},
				argString: "metric1,'1h'",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3,
					3, 3, 3, 4, 4, 4, 4, 4, 5, 5, 5, 5,
					5}, 5, tenFiftyNine)},
			},
			[]float64{375},
			"hitcount(metric1,'1h')",
			3600,
			tenFiftyNine,
			tenFiftyNine + 25*5,
		},
		{
			&expr{
				target: "hitcount",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "1h", etype: etString},
					{target: "true", etype: etName},
				},
				argString: "metric1,'1h',true",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3,
					3, 3, 3, 4, 4, 4, 4, 4, 5, 5, 5, 5,
					5}, 5, tenFiftyNine)},
			},
			[]float64{105, 270},
			"hitcount(metric1,'1h',true)",
			3600,
			tenFiftyNine - (59 * 60),
			tenFiftyNine + 25*5,
		},
		{
			&expr{
				target: "hitcount",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{valStr: "1h", etype: etString},
				},
				namedArgs: map[string]*expr{
					"alignToInterval": {target: "true", etype: etName},
				},
				argString: "metric1,'1h',alignToInterval=true",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {makeResponse("metric1", []float64{
					1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3,
					3, 3, 3, 4, 4, 4, 4, 4, 5, 5, 5, 5,
					5}, 5, tenFiftyNine)},
			},
			[]float64{105, 270},
			"hitcount(metric1,'1h',true)",
			3600,
			tenFiftyNine - (59 * 60),
			tenFiftyNine + 25*5,
		},
	}

	for _, tt := range tests {
		originalMetrics := deepClone(tt.m)
		g, err := EvalExpr(tt.e, 0, 1, tt.m)
		if err != nil {
			t.Errorf("failed to eval %v: %s", tt.name, err)
			continue
		}
		deepEqual(t, g[0].GetName(), originalMetrics, tt.m)
		if g[0].GetStepTime() != tt.step {
			t.Errorf("bad step for %s:\ngot  %d\nwant %d", g[0].GetName(), g[0].GetStepTime(), tt.step)
		}
		if g[0].GetStartTime() != tt.start {
			t.Errorf("bad start for %s: got %s want %s", g[0].GetName(), time.Unix(int64(g[0].GetStartTime()), 0).Format(time.StampNano), time.Unix(int64(tt.start), 0).Format(time.StampNano))
		}
		if g[0].GetStopTime() != tt.stop {
			t.Errorf("bad stop for %s: got %s want %s", g[0].GetName(), time.Unix(int64(g[0].GetStopTime()), 0).Format(time.StampNano), time.Unix(int64(tt.stop), 0).Format(time.StampNano))
		}

		if !nearlyEqual(g[0].Values, g[0].IsAbsent, tt.w) {
			t.Errorf("failed: %s:\ngot  %+v,\nwant %+v", g[0].GetName(), g[0].Values, tt.w)
		}
		if g[0].GetName() != tt.name {
			t.Errorf("bad name for %+v: got %v, want %v", g, g[0].GetName(), tt.name)
		}
	}
}

func TestEvalMultipleReturns(t *testing.T) {

	now32 := int32(time.Now().Unix())

	tests := []struct {
		e       *expr
		m       map[MetricRequest][]*MetricData
		name    string
		results map[string][]*MetricData
	}{
		{
			&expr{
				target: "groupByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 3, etype: etConst},
					{valStr: "sum", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar1.qux", []float64{6, 7, 8, 9, 10}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15}, 1, now32),
					makeResponse("metric1.foo.bar2.qux", []float64{7, 8, 9, 10, 11}, 1, now32),
				},
			},
			"groupByNode",
			map[string][]*MetricData{
				"baz": {makeResponse("baz", []float64{12, 14, 16, 18, 20}, 1, now32)},
				"qux": {makeResponse("qux", []float64{13, 15, 17, 19, 21}, 1, now32)},
			},
		},
		{
			&expr{
				target: "groupByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 3, etype: etConst},
					{valStr: "sum", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.01", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar1.10", []float64{6, 7, 8, 9, 10}, 1, now32),
					makeResponse("metric1.foo.bar2.01", []float64{11, 12, 13, 14, 15}, 1, now32),
					makeResponse("metric1.foo.bar2.10", []float64{7, 8, 9, 10, 11}, 1, now32),
				},
			},
			"groupByNode_names_with_int",
			map[string][]*MetricData{
				"01": {makeResponse("01", []float64{12, 14, 16, 18, 20}, 1, now32)},
				"10": {makeResponse("10", []float64{13, 15, 17, 19, 21}, 1, now32)},
			},
		},
		{
			&expr{
				target: "groupByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 3, etype: etConst},
					{valStr: "sum", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.127_0_0_1:2003", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar1.127_0_0_1:2004", []float64{6, 7, 8, 9, 10}, 1, now32),
					makeResponse("metric1.foo.bar2.127_0_0_1:2003", []float64{11, 12, 13, 14, 15}, 1, now32),
					makeResponse("metric1.foo.bar2.127_0_0_1:2004", []float64{7, 8, 9, 10, 11}, 1, now32),
				},
			},
			"groupByNode_names_with_colons",
			map[string][]*MetricData{
				"127_0_0_1:2003": {makeResponse("127_0_0_1:2003", []float64{12, 14, 16, 18, 20}, 1, now32)},
				"127_0_0_1:2004": {makeResponse("127_0_0_1:2004", []float64{13, 15, 17, 19, 21}, 1, now32)},
			},
		},
		{
			&expr{
				target: "applyByNode",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 2, etype: etConst},
					{valStr: "sumSeries(%.baz)", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15}, 1, now32),
				},
				MetricRequest{"metric1.foo.bar1.baz", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, 5}, 1, now32),
				},
				MetricRequest{"metric1.foo.bar2.baz", 0, 1}: {
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15}, 1, now32),
				},
			},
			"applyByNode",
			map[string][]*MetricData{
				"metric1.foo.bar1": {makeResponse("metric1.foo.bar1", []float64{1, 2, 3, 4, 5}, 1, now32)},
				"metric1.foo.bar2": {makeResponse("metric1.foo.bar2", []float64{11, 12, 13, 14, 15}, 1, now32)},
			},
		},
		{
			&expr{
				target: "sumSeriesWithWildcards",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 1, etype: etConst},
					{val: 2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar1.qux", []float64{6, 7, 8, 9, 10}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15}, 1, now32),
					makeResponse("metric1.foo.bar2.qux", []float64{7, 8, 9, 10, 11}, 1, now32),
				},
			},
			"sumSeriesWithWildcards",
			map[string][]*MetricData{
				"sumSeriesWithWildcards(metric1.baz)": {makeResponse("sumSeriesWithWildcards(metric1.baz)", []float64{12, 14, 16, 18, 20}, 1, now32)},
				"sumSeriesWithWildcards(metric1.qux)": {makeResponse("sumSeriesWithWildcards(metric1.qux)", []float64{13, 15, 17, 19, 21}, 1, now32)},
			},
		},
		{
			&expr{
				target: "averageSeriesWithWildcards",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1.foo.*.*"},
					{val: 1, etype: etConst},
					{val: 2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1.foo.*.*", 0, 1}: {
					makeResponse("metric1.foo.bar1.baz", []float64{1, 2, 3, 4, 5}, 1, now32),
					makeResponse("metric1.foo.bar1.qux", []float64{6, 7, 8, 9, 10}, 1, now32),
					makeResponse("metric1.foo.bar2.baz", []float64{11, 12, 13, 14, 15}, 1, now32),
					makeResponse("metric1.foo.bar2.qux", []float64{7, 8, 9, 10, 11}, 1, now32),
				},
			},
			"averageSeriesWithWildcards",
			map[string][]*MetricData{
				"averageSeriesWithWildcards(metric1.baz)": {makeResponse("averageSeriesWithWildcards(metric1.baz)", []float64{6, 7, 8, 9, 10}, 1, now32)},
				"averageSeriesWithWildcards(metric1.qux)": {makeResponse("averageSeriesWithWildcards(metric1.qux)", []float64{6.5, 7.5, 8.5, 9.5, 10.5}, 1, now32)},
			},
		},
		{
			&expr{
				target: "highestCurrent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 2, etype: etConst},
				},
				argString: "metric1,2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
				},
			},
			"highestCurrent",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32)},
				"metricC": {makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32)},
			},
		},
		{
			&expr{
				target: "lowestCurrent",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 3, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32),
					makeResponse("metricC", []float64{1, 1, 3, 3, 4, 15}, 1, now32),
					makeResponse("metricD", []float64{1, 1, 3, 3, 4, 3}, 1, now32),
					makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32),
				},
			},
			"lowestCurrent",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{1, 1, 3, 3, 4, 12}, 1, now32)},
				"metricB": {makeResponse("metricB", []float64{1, 1, 3, 3, 4, 1}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{1, 1, 3, 3, 4, 3}, 1, now32)},
			},
		},
		{
			&expr{
				target: "limit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 2, etype: etConst},
				},
				argString: "metric1, 2",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{0, 0, 1, 0, 0, 0}, 1, now32),
					makeResponse("metricC", []float64{0, 0, 0, 1, 0, 0}, 1, now32),
					makeResponse("metricD", []float64{0, 0, 0, 0, 1, 0}, 1, now32),
					makeResponse("metricE", []float64{0, 0, 0, 0, 0, 1}, 1, now32),
				},
			},
			"limit",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32)},
				"metricB": {makeResponse("metricB", []float64{0, 0, 1, 0, 0, 0}, 1, now32)},
			},
		},
		{
			&expr{
				target: "limit",
				etype:  etFunc,
				args: []*expr{
					{target: "metric1"},
					{val: 20, etype: etConst},
				},
				argString: "metric1, 20",
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric1", 0, 1}: {
					makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{0, 0, 1, 0, 0, 0}, 1, now32),
					makeResponse("metricC", []float64{0, 0, 0, 1, 0, 0}, 1, now32),
					makeResponse("metricD", []float64{0, 0, 0, 0, 1, 0}, 1, now32),
					makeResponse("metricE", []float64{0, 0, 0, 0, 0, 1}, 1, now32),
				},
			},
			"limit",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{0, 1, 0, 0, 0, 0}, 1, now32)},
				"metricB": {makeResponse("metricB", []float64{0, 0, 1, 0, 0, 0}, 1, now32)},
				"metricC": {makeResponse("metricC", []float64{0, 0, 0, 1, 0, 0}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{0, 0, 0, 0, 1, 0}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{0, 0, 0, 0, 0, 1}, 1, now32)},
			},
		},
		{
			&expr{
				target: "mostDeviant",
				etype:  etFunc,
				args: []*expr{
					{val: 2, etype: etConst},
					{target: "metric*"},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32),
				},
			},
			"mostDeviant",
			map[string][]*MetricData{
				"metricB": {makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32)},
			},
		},
		{
			&expr{
				target: "mostDeviant",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32),
				},
			},
			"mostDeviant",
			map[string][]*MetricData{
				"metricB": {makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32)},
			},
		},
		{
			&expr{
				target: "pearsonClosest",
				etype:  etFunc,
				args: []*expr{
					{target: "metricC"},
					{target: "metric*"},
					{val: 2, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32),
				},
				MetricRequest{"metricC", 0, 1}: {
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			"pearsonClosest",
			map[string][]*MetricData{
				"metricC": {makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32)},
			},
		},
		{
			&expr{
				target: "pearsonClosest",
				etype:  etFunc,
				args: []*expr{
					{target: "metricC"},
					{target: "metric*"},
					{val: 3, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{0, 0, 0, 0, 0, 0}, 1, now32),
					makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32),
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
					makeResponse("metricE", []float64{4, 7, 7, 7, 7, 1}, 1, now32),
				},
				MetricRequest{"metricC", 0, 1}: {
					makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32),
				},
			},
			"pearsonClosest",
			map[string][]*MetricData{
				"metricB": {makeResponse("metricB", []float64{3, 4, 5, 6, 7, 8}, 1, now32)},
				"metricC": {makeResponse("metricC", []float64{4, 4, 5, 5, 6, 6}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{4, 4, 5, 5, 6, 6}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			"tukeyAbove",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 3, etype: etConst},
					{val: 5, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			"tukeyAbove",
			map[string][]*MetricData{
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
					{val: 6, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{20, 20, 20, 20, 21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 20, 20, 20, 20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{20, 20, 20, 20, 19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{20, 20, 20, 20, 18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{20, 20, 20, 20, 17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			"tukeyAbove(metric*, 1.5, 5, 6)",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{20, 20, 20, 20, 21, 17, 20, 20, 10, 29}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{20, 20, 20, 20, 18, 20, 22, 14, 26, 20}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{20, 20, 20, 20, 17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyAbove",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
					{valStr: "6s", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{20, 20, 20, 20, 21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 20, 20, 20, 20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{20, 20, 20, 20, 19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{20, 20, 20, 20, 18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{20, 20, 20, 20, 17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			`tukeyAbove(metric*, 1.5, 5, "6s")`,
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{20, 20, 20, 20, 21, 17, 20, 20, 10, 29}, 1, now32)},
				"metricD": {makeResponse("metricD", []float64{20, 20, 20, 20, 18, 20, 22, 14, 26, 20}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{20, 20, 20, 20, 17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			"tukeyBelow",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
					{val: -4, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28, 20, 20, 20, 20}, 1, now32),
				},
			},

			"tukeyBelow",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29, 20, 20, 20, 20}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28, 20, 20, 20, 20}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 1.5, etype: etConst},
					{val: 5, etype: etConst},
					{valStr: "-4s", etype: etString},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20, 20, 20, 20, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28, 20, 20, 20, 20}, 1, now32),
				},
			},

			"tukeyBelow",
			map[string][]*MetricData{
				"metricA": {makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29, 20, 20, 20, 20}, 1, now32)},
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28, 20, 20, 20, 20}, 1, now32)},
			},
		},
		{
			&expr{
				target: "tukeyBelow",
				etype:  etFunc,
				args: []*expr{
					{target: "metric*"},
					{val: 3, etype: etConst},
					{val: 5, etype: etConst},
				},
			},
			map[MetricRequest][]*MetricData{
				MetricRequest{"metric*", 0, 1}: {
					makeResponse("metricA", []float64{21, 17, 20, 20, 10, 29}, 1, now32),
					makeResponse("metricB", []float64{20, 18, 21, 19, 20, 20}, 1, now32),
					makeResponse("metricC", []float64{19, 19, 21, 17, 23, 20}, 1, now32),
					makeResponse("metricD", []float64{18, 20, 22, 14, 26, 20}, 1, now32),
					makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32),
				},
			},

			"tukeyBelow",
			map[string][]*MetricData{
				"metricE": {makeResponse("metricE", []float64{17, 21, 8, 30, 18, 28}, 1, now32)},
			},
		},
	}

	for _, tt := range tests {
		originalMetrics := deepClone(tt.m)
		g, err := EvalExpr(tt.e, 0, 1, tt.m)
		if err != nil {
			t.Errorf("failed to eval %v: %s", tt.name, err)
			continue
		}
		deepEqual(t, tt.name, originalMetrics, tt.m)
		if len(g) == 0 {
			t.Errorf("returned no data %v", tt.name)
			continue
		}
		if g[0] == nil {
			t.Errorf("returned no value %v", tt.name)
			continue
		}
		if g[0].GetStepTime() == 0 {
			t.Errorf("missing step for %+v", g)
		}
		if len(g) != len(tt.results) {
			t.Errorf("unexpected results len: got %d, want %d", len(g), len(tt.results))
		}
		for _, gg := range g {
			r, ok := tt.results[gg.GetName()]
			if !ok {
				t.Errorf("missing result name: %v", gg.GetName())
				continue
			}
			if r[0].GetName() != gg.GetName() {
				t.Errorf("result name mismatch, got\n%#v,\nwant\n%#v", gg.GetName(), r[0].GetName())
			}
			if !reflect.DeepEqual(r[0].Values, gg.Values) || !reflect.DeepEqual(r[0].IsAbsent, gg.IsAbsent) ||
				r[0].GetStartTime() != gg.GetStartTime() ||
				r[0].GetStopTime() != gg.GetStopTime() ||
				r[0].GetStepTime() != gg.GetStepTime() {
				t.Errorf("result mismatch, got\n%#v,\nwant\n%#v", gg, r)
			}
		}
	}
}

func TestExtractMetric(t *testing.T) {

	var tests = []struct {
		input  string
		metric string
	}{
		{
			"f",
			"f",
		},
		{
			"func(f)",
			"f",
		},
		{
			"foo.bar.baz",
			"foo.bar.baz",
		},
		{
			"nonNegativeDerivative(foo.bar.baz)",
			"foo.bar.baz",
		},
		{
			"movingAverage(foo.bar.baz,10)",
			"foo.bar.baz",
		},
		{
			"scale(scaleToSeconds(nonNegativeDerivative(foo.bar.baz),60),60)",
			"foo.bar.baz",
		},
		{
			"divideSeries(foo.bar.baz,baz.qux.zot)",
			"foo.bar.baz",
		},
		{
			"{something}",
			"{something}",
		},
	}

	for _, tt := range tests {
		if m := extractMetric(tt.input); m != tt.metric {
			t.Errorf("extractMetric(%q)=%q, want %q", tt.input, m, tt.metric)
		}
	}
}

func TestEvalCustomFromUntil(t *testing.T) {

	tests := []struct {
		e     *expr
		m     map[MetricRequest][]*MetricData
		w     []float64
		name  string
		from  int32
		until int32
	}{
		{
			&expr{
				target: "timeFunction",
				etype:  etFunc,
				args: []*expr{
					{valStr: "footime", etype: etString},
				},
				argString: "footime",
			},
			map[MetricRequest][]*MetricData{},
			[]float64{4200.0, 4260.0, 4320.0},
			"footime",
			4200,
			4350,
		},
	}

	for _, tt := range tests {
		originalMetrics := deepClone(tt.m)
		g, err := EvalExpr(tt.e, tt.from, tt.until, tt.m)
		if err != nil {
			t.Errorf("failed to eval %v: %s", tt.name, err)
			continue
		}
		if g[0] == nil {
			t.Errorf("returned no value %v", tt.e.argString)
			continue
		}

		deepEqual(t, tt.e.target, originalMetrics, tt.m)

		if g[0].GetStepTime() == 0 {
			t.Errorf("missing step for %+v", g)
		}
		if !nearlyEqual(g[0].Values, g[0].IsAbsent, tt.w) {
			t.Errorf("failed: %s: got %+v, want %+v", g[0].GetName(), g[0].Values, tt.w)
		}
		if g[0].GetName() != tt.name {
			t.Errorf("bad name for %+v: got %v, want %v", g, g[0].GetName(), tt.name)
		}
	}
}

const eps = 0.0000000001

func nearlyEqual(a []float64, absent []bool, b []float64) bool {

	if len(a) != len(b) {
		return false
	}

	for i, v := range a {
		// "same"
		if absent[i] && math.IsNaN(b[i]) {
			continue
		}
		if absent[i] || math.IsNaN(b[i]) {
			// unexpected NaN
			return false
		}
		// "close enough"
		if math.Abs(v-b[i]) > eps {
			return false
		}
	}

	return true
}

func nearlyEqualMetrics(a, b *MetricData) bool {

	if len(a.IsAbsent) != len(b.IsAbsent) {
		return false
	}

	for i := range a.IsAbsent {
		if a.IsAbsent[i] != b.IsAbsent[i] {
			return false
		}
		// "close enough"
		if math.Abs(a.Values[i]-b.Values[i]) > eps {
			return false
		}
	}

	return true
}
