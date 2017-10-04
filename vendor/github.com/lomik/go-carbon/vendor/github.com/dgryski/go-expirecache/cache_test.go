package expirecache

import (
	"bytes"
	"testing"
	"time"
)

func TestCacheExpire(t *testing.T) {

	c := &Cache{cache: make(map[string]element)}

	sleep := make(chan bool)
	cleanerSleep = func(_ time.Duration) { <-sleep }
	done := make(chan bool)
	cleanerDone = func() { <-done }

	defer func() {
		cleanerSleep = time.Sleep
		cleanerDone = func() {}
		timeNow = time.Now
	}()

	go c.Cleaner(5 * time.Minute)
	t0 := time.Now()

	timeNow = func() time.Time { return t0 }

	c.Set("foo", []byte("bar"), 3, 30)
	c.Set("baz", []byte("qux"), 3, 60)
	c.Set("zot", []byte("bork"), 4, 120)

	type expireTest struct {
		key string
		ok  bool
	}

	// test expiration logic in get()

	present := []expireTest{
		{"foo", true},
		{"baz", true},
		{"zot", true},
	}

	// unexpired
	for _, p := range present {

		b, ok := c.Get(p.key)

		if ok != p.ok || (ok != (b != nil)) {
			t.Errorf("expireCache: bad unexpired cache.Get(%v)=(%v,%v), want %v", p.key, string(b.([]byte)), ok, p.ok)
		}
	}

	if len(c.keys) != 3 {
		t.Errorf("unexpired keys array length mismatch: got %d, want %d", len(c.keys), 3)
	}

	if c.totalSize != 3+3+4 {
		t.Errorf("unexpired cache size mismatch: got %d, want %d", c.totalSize, 3+3+4)
	}

	c.Set("baz", []byte("snork"), 5, 60)

	if len(c.keys) != 3 {
		t.Errorf("unexpired extra keys array length mismatch: got %d, want %d", len(c.keys), 3)
	}

	if c.totalSize != 3+5+4 {
		t.Errorf("unexpired extra cache size mismatch: got %d, want %d", c.totalSize, 3+3+4)
	}

	// expire key `foo`
	timeNow = func() time.Time { return t0.Add(45 * time.Second) }

	present = []expireTest{
		{"foo", false},
		{"baz", true},
		{"zot", true},
	}

	for _, p := range present {
		b, ok := c.Get(p.key)
		if ok != p.ok || (ok != (b != nil)) {
			t.Errorf("expireCache: bad partial expire cache.Get(%v)=(%v,%v), want %v", p.key, string(b.([]byte)), ok, p.ok)
		}
	}

	// let the cleaner run
	timeNow = func() time.Time { return t0.Add(75 * time.Second) }
	sleep <- true
	done <- true

	present = []expireTest{
		{"foo", false},
		{"baz", false},
		{"zot", true},
	}

	for _, p := range present {
		b, ok := c.Get(p.key)
		if ok != p.ok || (ok != (b != nil)) {
			t.Errorf("expireCache: bad partial expire cache.Get(%v)=(%v,%v), want %v", p.key, string(b.([]byte)), ok, p.ok)
		}
	}

	if len(c.keys) != 1 {
		t.Errorf("unexpired keys array length mismatch: got %d, want %d", len(c.keys), 3)
	}

	if c.totalSize != 4 {
		t.Errorf("unexpired cache size mismatch: got %d, want %d", c.totalSize, 3+3+4)
	}

	// getOrSet test
	d := []byte("bar")
	b := c.GetOrSet("bork", d, 3, 30)
	if bytes.Compare(b.([]byte), d) != 0 {
		t.Errorf("GetOrSet should return the same object if key doesn't exist")
	}

	d2 := []byte("baz")
	b = c.GetOrSet("bork", d2, 3, 30)
	if bytes.Compare(b.([]byte), d) != 0 {
		t.Errorf("GetOrSet should return existing key if it already exist")
	}

}
