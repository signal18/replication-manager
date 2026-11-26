package config

import "testing"

func TestValidateAppDockerTrusted(t *testing.T) {
	for _, ok := range []string{"", "ap1", "ap123, ap456", " ap1 ,, ap1 "} {
		if err := ValidateAppDockerTrusted(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"web", "c1/web", "ap", "ap1 --runtime=runc", "ap1;ap2", "AP1", "ap1x"} {
		if err := ValidateAppDockerTrusted(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestIsAppDockerTrusted(t *testing.T) {
	conf := &Config{ProvAppDockerTrusted: " ap1 ,ap22"}
	for id, want := range map[string]bool{"ap1": true, "ap22": true, "ap2": false, "": false, "ap221": false} {
		if got := conf.IsAppDockerTrusted(id); got != want {
			t.Errorf("%q: %v, want %v", id, got, want)
		}
	}
	if got := AppDockerTrustedIDs(" ap1,,ap2 , ap1"); len(got) != 2 || got[0] != "ap1" || got[1] != "ap2" {
		t.Fatalf("AppDockerTrustedIDs: %v", got)
	}
}
