package cache

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"time"

	"github.com/bradfitz/gomemcache/memcache"

	"github.com/dgryski/go-expirecache"
)

var (
	ErrTimeout  = errors.New("cache: timeout")
	ErrNotFound = errors.New("cache: not found")
)

type BytesCache interface {
	Get(k string) ([]byte, error)
	Set(k string, v []byte, expire int32)
}

type NullCache struct{}

func (NullCache) Get(string) ([]byte, error) { return nil, ErrNotFound }
func (NullCache) Set(string, []byte, int32)  {}

func NewExpireCache(maxsize uint64) BytesCache {
	ec := expirecache.New(maxsize)
	go ec.ApproximateCleaner(10 * time.Second)
	return &ExpireCache{ec: ec}
}

type ExpireCache struct {
	ec *expirecache.Cache
}

func (ec ExpireCache) Get(k string) ([]byte, error) {
	v, ok := ec.ec.Get(k)

	if !ok {
		return nil, ErrNotFound
	}

	return v.([]byte), nil
}

func (ec ExpireCache) Set(k string, v []byte, expire int32) {
	ec.ec.Set(k, v, uint64(len(v)), expire)
}

func (ec ExpireCache) Items() int { return ec.ec.Items() }

func (ec ExpireCache) Size() uint64 { return ec.ec.Size() }

func NewMemcached(prefix string, servers ...string) BytesCache {
	return &MemcachedCache{prefix: prefix, client: memcache.New(servers...)}
}

type MemcachedCache struct {
	prefix   string
	client   *memcache.Client
	timeouts uint64
}

func (m *MemcachedCache) Get(k string) ([]byte, error) {
	key := sha1.Sum([]byte(k))
	hk := hex.EncodeToString(key[:])
	done := make(chan bool, 1)

	var err error
	var item *memcache.Item

	go func() {
		item, err = m.client.Get(m.prefix + hk)
		done <- true
	}()

	timeout := time.After(50 * time.Millisecond)

	select {
	case <-timeout:
		atomic.AddUint64(&m.timeouts, 1)
		return nil, ErrTimeout
	case <-done:
	}

	if err != nil {
		// translate to internal cache miss error
		if err == memcache.ErrCacheMiss {
			err = ErrNotFound
		}
		return nil, err
	}

	return item.Value, nil
}

func (m *MemcachedCache) Set(k string, v []byte, expire int32) {
	key := sha1.Sum([]byte(k))
	hk := hex.EncodeToString(key[:])
	go m.client.Set(&memcache.Item{Key: m.prefix + hk, Value: v, Expiration: expire})
}

func (m *MemcachedCache) Timeouts() uint64 {
	return atomic.LoadUint64(&m.timeouts)
}
