// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cache

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func BenchmarkXlogGetRWMutex(b *testing.B) {
	cache := struct {
		xlogMutex sync.RWMutex
		xlog      io.Writer
	}{}

	cnt := 0 // avoid optimizations

	for n := 0; n < b.N; n++ {
		cache.xlogMutex.RLock()
		xlog := cache.xlog
		cache.xlogMutex.RUnlock()

		if xlog != nil {
			cnt++
		}
	}

	if cnt != 0 {
		b.FailNow()
	}
}

func BenchmarkXlogDirect(b *testing.B) {
	cache := struct {
		xlog io.Writer
	}{}

	cnt := 0 // avoid optimizations

	for n := 0; n < b.N; n++ {
		xlog := cache.xlog

		if xlog != nil {
			cnt++
		}
	}

	if cnt != 0 {
		b.FailNow()
	}
}

func BenchmarkXlogAtomicValue(b *testing.B) {
	cache := struct {
		xlog atomic.Value
	}{}

	cnt := 0 // avoid optimizations

	for n := 0; n < b.N; n++ {
		xlog := cache.xlog.Load()

		if xlog != nil {
			cnt++
		}
	}

	if cnt != 0 {
		b.FailNow()
	}
}
