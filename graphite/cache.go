// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package graphite

import (
	"crypto/sha1"
	"encoding/hex"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	ecache "github.com/dgryski/go-expirecache"
)

type bytesCache interface {
	get(k string) ([]byte, bool)
	set(k string, v []byte, expire int32)
}

type nullCache struct{}

func (nullCache) get(string) ([]byte, bool) { return nil, false }
func (nullCache) set(string, []byte, int32) {}

type expireCache struct {
	ec *ecache.Cache
}

func (ec expireCache) get(k string) ([]byte, bool) {
	v, ok := ec.ec.Get(k)

	if !ok {
		return nil, false
	}

	return v.([]byte), true
}

func (ec expireCache) set(k string, v []byte, expire int32) {
	ec.ec.Set(k, v, uint64(len(v)), expire)
}

type memcachedCache struct {
	client *memcache.Client
}

func (m *memcachedCache) get(k string) ([]byte, bool) {
	key := sha1.Sum([]byte(k))
	hk := hex.EncodeToString(key[:])
	done := make(chan bool, 1)

	var err error
	var item *memcache.Item

	go func() {
		item, err = m.client.Get(hk)
		done <- true
	}()

	timeout := time.After(50 * time.Millisecond)

	select {
	case <-timeout:
		Metrics.MemcacheTimeouts.Add(1)
		return nil, false
	case <-done:
	}

	if err != nil {
		return nil, false
	}

	return item.Value, true
}

func (m *memcachedCache) set(k string, v []byte, expire int32) {
	key := sha1.Sum([]byte(k))
	hk := hex.EncodeToString(key[:])
	go m.client.Set(&memcache.Item{Key: hk, Value: v, Expiration: expire})
}
