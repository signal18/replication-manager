// +build !cairo

package expr

import "net/http"

const haveGraphSupport = false

type graphOptions struct {
}

func evalExprGraph(e *expr, from, until int32, values map[MetricRequest][]*MetricData) ([]*MetricData, error) {
	return nil, nil
}

func MarshalPNG(r *http.Request, results []*MetricData) []byte {
	return nil
}

func MarshalSVG(r *http.Request, results []*MetricData) []byte {
	return nil
}
