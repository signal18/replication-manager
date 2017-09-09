// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package graphite

import (
	"fmt"
	"time"
)

// Metric is a struct that defines the relevant properties of a graphite metric
type Metric struct {
	Name      string
	Value     string
	Timestamp int64
}

func NewMetric(name, value string, timestamp int64) Metric {
	return Metric{
		Name:      name,
		Value:     value,
		Timestamp: timestamp,
	}
}

func (metric Metric) String() string {
	return fmt.Sprintf(
		"%s %s %s",
		metric.Name,
		metric.Value,
		time.Unix(metric.Timestamp, 0).Format("2006-01-02 15:04:05"),
	)
}
