package config

import "testing"

func TestGatewayLists(t *testing.T) {
	c := &Config{Cloud18GatewayService: " s18/svc/haproxy, Sagacita/svc/haproxy ,", Cloud18GatewayDomainName: "igw-a.example, igw-b.example"}
	if got := c.GatewayServices(); len(got) != 2 || got[0] != "s18/svc/haproxy" || got[1] != "Sagacita/svc/haproxy" {
		t.Fatalf("GatewayServices=%v", got)
	}
	if c.PrimaryGatewayService() != "s18/svc/haproxy" || c.PrimaryGatewayDomain() != "igw-a.example" {
		t.Fatalf("primary: %q %q", c.PrimaryGatewayService(), c.PrimaryGatewayDomain())
	}
	if !c.HasGateway("SAGACITA/svc/haproxy") || c.HasGateway("x/svc/haproxy") || c.HasGateway("") {
		t.Fatalf("HasGateway")
	}
	other := &Config{Cloud18GatewayService: "sagacita/svc/haproxy"}
	none := &Config{}
	if !c.SharesGateway(other) || c.SharesGateway(none) || none.SharesGateway(c) || c.SharesGateway(nil) {
		t.Fatalf("SharesGateway")
	}
	if ns, name, ok := GatewayServiceParts("s18/svc/haproxy"); !ok || ns != "s18" || name != "haproxy" {
		t.Fatalf("parts: %q %q %v", ns, name, ok)
	}
	if _, _, ok := GatewayServiceParts("haproxy"); ok {
		t.Fatalf("parts must refuse a bare name")
	}
	if single := (&Config{Cloud18GatewayService: "s18/svc/haproxy"}); single.PrimaryGatewayService() != "s18/svc/haproxy" || len(single.GatewayServices()) != 1 {
		t.Fatalf("single value must keep working")
	}
}
