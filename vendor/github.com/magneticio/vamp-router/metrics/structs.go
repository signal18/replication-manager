package metrics

type Metric struct {
	Tags      []string `json:"tags"`
	Value     int      `json:"value"`
	Timestamp string   `json:"timestamp"`
	Type      string   `json:"type"`
}
