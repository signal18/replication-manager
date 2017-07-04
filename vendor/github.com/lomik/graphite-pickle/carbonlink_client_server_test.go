package pickle

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestCarbonlinkClientServer(t *testing.T) {
	cache := map[string][]DataPoint{
		"hello.world": []DataPoint{{1498074512, 42.0}},
		"m1":          []DataPoint{{1498074512, 42.0}, {1498074513, 15.0}},
		"m2":          []DataPoint{},
		"m3":          nil,
	}

	server := NewCarbonlinkServer(time.Second, time.Second)
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		t.FailNow()
	}

	server.HandleCacheQuery(func(metric string) ([]DataPoint, error) {
		v, ok := cache[metric]
		if !ok {
			return nil, nil
		}
		return v, nil
	})

	server.Listen(addr)
	defer server.Stop()

	client := NewCarbonlinkClient(server.Addr().String(), 3, 3, time.Second, time.Second)

	for k, v := range cache {
		res, err := client.CacheQuery(context.Background(), k)
		if err != nil {
			t.FailNow()
		}
		if len(v) == 0 {
			v = nil
		}
		if !reflect.DeepEqual(res, v) {
			t.Errorf("%#v (actual) != %#v (expected)", res, v)
		}
	}
}

func TestStopCarbonlinkServer(t *testing.T) {
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.FailNow()
	}

	for i := 0; i < 10; i++ {
		server := NewCarbonlinkServer(time.Second, time.Second)
		err := server.Listen(addr)
		if err != nil {
			t.FailNow()
		}
		addr = server.Addr().(*net.TCPAddr) // listen same port in next iteration
		server.Stop()
	}
}
