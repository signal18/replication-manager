// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"sync/atomic"
)

// published holds a value the monitor writes and the API reads: each Store publishes a
// new copy at once, so a reader never sees half of a write (a string or a slice header
// is several words). No lock on the monitor tick (F2). The JSON is the value itself.
type published[T any] struct {
	p atomic.Pointer[T]
}

func (a *published[T]) Store(v T) {
	a.p.Store(&v)
}

// Load returns the last stored value, the zero value before the first Store.
func (a *published[T]) Load() T {
	if p := a.p.Load(); p != nil {
		return *p
	}
	var zero T
	return zero
}

func (a *published[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.Load())
}
