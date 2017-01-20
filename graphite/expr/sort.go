package expr

import (
	"path/filepath"
	"sort"
	"strings"
)

// type for sorting a list of metrics by the nth part of the metric name.
// Implements sort.Interface minus Less, which needs to be provided by a struct
// that embeds this one. Provides compareBy for the benefit of that struct, which
// turns a function that compares two strings into a suitable Less function. Caches
// the relevant metric name part to avoid excessive calls to strings.Split.
type byPartBase struct {
	// the metrics to be sorted
	metrics []*MetricData
	// which part of the name we are sorting on
	part int
	// a cache of the relevant part of the name for each metric in metrics
	keys []*string
}

func (b byPartBase) Len() int { return len(b.metrics) }

func (b byPartBase) Swap(i, j int) {
	b.metrics[i], b.metrics[j] = b.metrics[j], b.metrics[i]
	b.keys[i], b.keys[j] = b.keys[j], b.keys[i]
}

func getPart(metric *MetricData, part int) string {
	parts := strings.Split(metric.GetName(), ".")
	return parts[part]
}

// Given two indices, i and j, and a comparator function that returns whether
// one metric name segment should sort before another, extracts the 'part'th part
// of the metric names, consults the comparator function, and returns a boolean
// suitable for use as the Less() method of a sort.Interface.
func (b byPartBase) compareBy(i, j int, comparator func(string, string) bool) bool {
	if b.keys[i] == nil {
		part := getPart(b.metrics[i], b.part)
		b.keys[i] = &part
	}
	if b.keys[j] == nil {
		part := getPart(b.metrics[j], b.part)
		b.keys[j] = &part
	}
	return comparator(*b.keys[i], *b.keys[j])
}

// ByPart returns a byPartBase suitable for sorting 'metrics' by 'part'.
func ByPart(metrics []*MetricData, part int) byPartBase {
	return byPartBase{
		metrics: metrics,
		keys:    make([]*string, len(metrics)),
		part:    part,
	}
}

// type for sorting a list of metrics 'alphabetically' (go string compare order)
type byPartAlphabetical struct {
	byPartBase
}

func (b byPartAlphabetical) Less(i, j int) bool {
	return b.compareBy(i, j, func(first, second string) bool {
		return first < second
	})
}

// AlphabeticallyByPart returns a byPartAlphabetical that will sort 'metrics' alphabetically by 'part'.
func AlphabeticallyByPart(metrics []*MetricData, part int) sort.Interface {
	return byPartAlphabetical{ByPart(metrics, part)}
}

func sortByBraces(metrics []*MetricData, part int, pattern string) {
	bStart := strings.IndexRune(pattern, '{')
	bEnd := strings.IndexRune(pattern, '}')
	if bStart == -1 || bEnd <= bStart {
		return
	}

	parts := make([]string, len(metrics))
	for i, metric := range metrics {
		parts[i] = getPart(metric, part)
	}
	src := make([]*MetricData, len(metrics))
	used := make([]bool, len(metrics))
	copy(src, metrics)
	j := 0

	alternatives := strings.Split(pattern[bStart+1:bEnd], ",")
	for _, alternative := range alternatives {
		glob := pattern[:bStart] + alternative + pattern[bEnd+1:]
		for i := 0; i < len(src); i++ {
			if used[i] {
				continue
			}
			if match, _ := filepath.Match(glob, parts[i]); match {
				metrics[j] = src[i]
				j = j + 1
				used[i] = true
			}
		}
	}
	for i, metric := range src { // catch any leftovers
		if !used[i] {
			metrics[j] = metric
			j = j + 1
		}
	}
}

func SortMetrics(metrics []*MetricData, mfetch MetricRequest) {
	// Don't do any work if there are no globs in the metric name
	if !strings.ContainsAny(mfetch.Metric, "*?[{") {
		return
	}
	parts := strings.Split(mfetch.Metric, ".")
	// Proceed backwards by segments, sorting once for each segment that has a glob that calls for sorting.
	// By using a stable sort, the rightmost segments will be preserved as "sub-sorts" of any more leftward segments.
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.ContainsAny(parts[i], "*?[{") {
			sort.Stable(AlphabeticallyByPart(metrics, i))
		}
		if strings.ContainsRune(parts[i], '{') {
			sortByBraces(metrics, i, parts[i])
		}
	}
}
