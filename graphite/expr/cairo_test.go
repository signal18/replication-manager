// +build cairo

package expr

import (
	"testing"
	"time"
)

func TestEvalExpressionGraph(t *testing.T) {

	now32 := int32(time.Now().Unix())

	tests := []evalTestItem{
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
				},
				argString: "42.42",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("42.42",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
					{valStr: "fourty-two", etype: etString},
				},
				argString: "42.42,'fourty-two'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("fourty-two",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
					{valStr: "fourty-two", etype: etString},
					{valStr: "blue", etype: etString},
				},
				argString: "42.42,'fourty-two','blue'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("fourty-two",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
				},
				namedArgs: map[string]*expr{
					"label": {valStr: "fourty-two", etype: etString},
				},
				argString: "42.42,label='fourty-two'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("fourty-two",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
				},
				namedArgs: map[string]*expr{
					"color": {valStr: "blue", etype: etString},
					//TODO(nnuss): test blue is being set rather than just not causing expression to parse/fail
				},
				argString: "42.42,color='blue'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("42.42",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
				},
				namedArgs: map[string]*expr{
					"label": {valStr: "fourty-two-blue", etype: etString},
					"color": {valStr: "blue", etype: etString},
					//TODO(nnuss): test blue is being set rather than just not causing expression to parse/fail
				},
				argString: "42.42,label='fourty-two-blue',color='blue'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("fourty-two-blue",
				[]float64{42.42, 42.42}, 1, now32)},
		},
		{
			// BUG(nnuss): This test actually fails with color = "" because of
			// how getStringNamedOrPosArgDefault works but we don't notice
			// because we're not testing color is set.
			// You may manually verify with this request URI: /render/?format=png&target=threshold(42.42,"gold",label="fourty-two-aurum")
			&expr{
				target: "threshold",
				etype:  etFunc,
				args: []*expr{
					{val: 42.42, etype: etConst},
					{valStr: "gold", etype: etString},
				},
				namedArgs: map[string]*expr{
					"label": {valStr: "fourty-two-aurum", etype: etString},
				},
				argString: "42.42,'gold',label='fourty-two-aurum'",
			},
			map[MetricRequest][]*MetricData{},
			[]*MetricData{makeResponse("fourty-two-aurum",
				[]float64{42.42, 42.42}, 1, now32)},
		},
	}

	for _, tt := range tests {
		testEvalExpr(t, &tt)
	}
}
