// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package expr

import (
	"bytes"
	"math"
	"strconv"
	"time"

	pb "github.com/tanji/replication-manager/graphite/carbonzipper/carbonzipperpb"
	pickle "github.com/tanji/replication-manager/graphite/og-rek"
)

type MetricData struct {
	pb.FetchResponse

	graphOptions

	valuesPerPoint    int
	aggregatedValues  []float64
	aggregatedAbsent  []bool
	aggregateFunction func([]float64, []bool) (float64, bool)
}

func MarshalCSV(results []*MetricData) []byte {

	var b []byte

	for _, r := range results {

		step := r.GetStepTime()
		t := r.GetStartTime()
		for i, v := range r.Values {
			b = append(b, '"')
			b = append(b, r.GetName()...)
			b = append(b, '"')
			b = append(b, ',')
			b = append(b, time.Unix(int64(t), 0).Format("2006-01-02 15:04:05")...)
			b = append(b, ',')
			if !r.IsAbsent[i] {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}
			b = append(b, '\n')
			t += step
		}
	}
	return b
}

func ConsolidateJSON(maxDataPoints int, results []*MetricData) {
	var startTime int32 = -1
	var endTime int32 = -1

	for _, r := range results {
		t := r.GetStartTime()
		if startTime == -1 || startTime > t {
			startTime = t
		}
		t = r.GetStopTime()
		if endTime == -1 || endTime < t {
			endTime = t
		}
	}

	timeRange := endTime - startTime

	if timeRange <= 0 {
		return
	}

	for _, r := range results {
		numberOfDataPoints := math.Floor(float64(timeRange / r.GetStepTime()))
		if numberOfDataPoints > float64(maxDataPoints) {
			valuesPerPoint := math.Ceil(numberOfDataPoints / float64(maxDataPoints))
			r.setValuesPerPoint(int(valuesPerPoint))
		}
	}
}

func MarshalJSON(results []*MetricData) []byte {
	var b []byte
	b = append(b, '[')

	var topComma bool
	for _, r := range results {
		if r == nil {
			continue
		}

		if topComma {
			b = append(b, ',')
		}
		topComma = true

		b = append(b, `{"target":`...)
		b = strconv.AppendQuoteToASCII(b, r.GetName())
		b = append(b, `,"datapoints":[`...)

		var innerComma bool
		t := r.GetStartTime()
		absent := r.AggregatedAbsent()
		for i, v := range r.AggregatedValues() {
			if innerComma {
				b = append(b, ',')
			}
			innerComma = true

			b = append(b, '[')

			if absent[i] || math.IsInf(v, 0) || math.IsNaN(v) {
				b = append(b, "null"...)
			} else {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}

			b = append(b, ',')

			b = strconv.AppendInt(b, int64(t), 10)

			b = append(b, ']')

			t += r.AggregatedTimeStep()
		}

		b = append(b, `]}`...)
	}

	b = append(b, ']')

	return b
}

func MarshalPickle(results []*MetricData) []byte {

	var p []map[string]interface{}

	for _, r := range results {
		values := make([]interface{}, len(r.Values))
		for i, v := range r.Values {
			if r.IsAbsent[i] {
				values[i] = pickle.None{}
			} else {
				values[i] = v
			}

		}
		p = append(p, map[string]interface{}{
			"name":   r.GetName(),
			"start":  r.GetStartTime(),
			"end":    r.GetStopTime(),
			"step":   r.GetStepTime(),
			"values": values,
		})
	}

	var buf bytes.Buffer

	penc := pickle.NewEncoder(&buf)
	penc.Encode(p)

	return buf.Bytes()
}

func MarshalProtobuf(results []*MetricData) ([]byte, error) {
	response := pb.MultiFetchResponse{}
	for _, metric := range results {
		response.Metrics = append(response.Metrics, &((*metric).FetchResponse))
	}
	b, err := response.Marshal()
	if err != nil {
		return nil, err
	}

	return b, nil
}

func MarshalRaw(results []*MetricData) []byte {

	var b []byte

	for _, r := range results {

		b = append(b, r.GetName()...)

		b = append(b, ',')
		b = strconv.AppendInt(b, int64(r.GetStartTime()), 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, int64(r.GetStopTime()), 10)
		b = append(b, ',')
		b = strconv.AppendInt(b, int64(r.GetStepTime()), 10)
		b = append(b, '|')

		var comma bool
		for i, v := range r.Values {
			if comma {
				b = append(b, ',')
			}
			comma = true
			if r.IsAbsent[i] {
				b = append(b, "None"...)
			} else {
				b = strconv.AppendFloat(b, v, 'f', -1, 64)
			}
		}

		b = append(b, '\n')
	}
	return b
}

func (r *MetricData) setValuesPerPoint(v int) {
	r.valuesPerPoint = v
	r.aggregatedValues = nil
	r.aggregatedAbsent = nil
}

func (r *MetricData) AggregatedTimeStep() int32 {
	if r.valuesPerPoint == 1 || r.valuesPerPoint == 0 {
		return r.GetStepTime()
	}

	return r.GetStepTime() * int32(r.valuesPerPoint)
}

func (r *MetricData) AggregatedValues() []float64 {
	if r.aggregatedValues == nil {
		r.AggregateValues()
	}
	return r.aggregatedValues
}

func (r *MetricData) AggregatedAbsent() []bool {
	if r.aggregatedAbsent == nil {
		r.AggregateValues()
	}
	return r.aggregatedAbsent
}
func (r *MetricData) AggregateValues() {
	if r.valuesPerPoint == 1 || r.valuesPerPoint == 0 {
		r.aggregatedValues = make([]float64, len(r.Values))
		r.aggregatedAbsent = make([]bool, len(r.Values))
		copy(r.aggregatedValues, r.Values)
		copy(r.aggregatedAbsent, r.IsAbsent)
		return
	}

	if r.aggregateFunction == nil {
		r.aggregateFunction = aggMean
	}

	n := len(r.Values)/r.valuesPerPoint + 1
	aggV := make([]float64, 0, n)
	aggA := make([]bool, 0, n)

	v := r.Values
	absent := r.IsAbsent

	for len(v) >= r.valuesPerPoint {
		val, abs := r.aggregateFunction(v[:r.valuesPerPoint], absent[:r.valuesPerPoint])
		aggV = append(aggV, val)
		aggA = append(aggA, abs)
		v = v[r.valuesPerPoint:]
		absent = absent[r.valuesPerPoint:]
	}

	if len(v) > 0 {
		val, abs := r.aggregateFunction(v, absent)
		aggV = append(aggV, val)
		aggA = append(aggA, abs)
	}

	r.aggregatedValues = aggV
	r.aggregatedAbsent = aggA
}

func aggMean(v []float64, absent []bool) (float64, bool) {
	var sum float64
	var n int
	for i, vv := range v {
		if !math.IsNaN(vv) && !absent[i] {
			sum += vv
			n++
		}
	}
	return sum / float64(n), n == 0
}

func aggMax(v []float64, absent []bool) (float64, bool) {
	var m = math.Inf(-1)
	var abs = true
	for i, vv := range v {
		if !absent[i] && !math.IsNaN(vv) {
			abs = false
			if m < vv {
				m = vv
			}
		}
	}
	return m, abs
}

func aggMin(v []float64, absent []bool) (float64, bool) {
	var m = math.Inf(1)
	var abs = true
	for i, vv := range v {
		if !absent[i] && !math.IsNaN(vv) {
			abs = false
			if m > vv {
				m = vv
			}
		}
	}
	return m, abs
}

func aggSum(v []float64, absent []bool) (float64, bool) {
	var sum float64
	var abs = true
	for i, vv := range v {
		if !math.IsNaN(vv) && !absent[i] {
			sum += vv
			abs = false
		}
	}
	return sum, abs
}
