package hostmatch

import "testing"

func TestIsDNSHost(t *testing.T) {
	dns := []string{"repman.s18.svc.cloud18", "db1", "%.example.com", "app-1.ns.svc"}
	not := []string{"", "%", "localhost", "LOCALHOST", "10.60.18.167", "10.60.%", "10.0.0.0/255.0.0.0", "::1", "fd00::12", "192.168.1._"}
	for _, h := range dns {
		if !IsDNSHost(h) {
			t.Errorf("%q is a DNS host", h)
		}
	}
	for _, h := range not {
		if IsDNSHost(h) {
			t.Errorf("%q is not a DNS host", h)
		}
	}
}
