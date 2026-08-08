package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestCanSendGraphiteMetrics(t *testing.T) {
	cases := []struct {
		name     string
		metrics  bool
		status   string
		embedded bool
		want     bool
	}{
		{"active sends", true, ConstMonitorActif, false, true},
		{"active sends (embedded too)", true, ConstMonitorActif, true, true},
		{"standby embedded records own view", true, ConstMonitorStandby, true, true},
		{"standby non-embedded stays silent", true, ConstMonitorStandby, false, false},
		{"metrics disabled: never", false, ConstMonitorActif, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{
				Status: c.status,
				Conf:   &config.Config{GraphiteMetrics: c.metrics, GraphiteEmbedded: c.embedded},
			}
			if got := cl.CanSendGraphiteMetrics(); got != c.want {
				t.Fatalf("CanSendGraphiteMetrics() = %v, want %v", got, c.want)
			}
		})
	}
}
