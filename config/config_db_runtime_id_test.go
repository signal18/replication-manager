package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// prov-db-run-as-uid and prov-db-volume-uid are strings so that "not set" (empty) differs
// from root ("0") and so that they can carry UID:GID; operators may still write a
// bare TOML integer for a UID alone.
func TestProvDBIdentityDecodesTOML(t *testing.T) {
	for _, tc := range []struct{ toml, runAs, chown string }{
		{"prov-db-run-as-uid = 1001\n", "1001", ""},
		{"prov-db-volume-uid = 0\n", "", "0"},
		{"prov-db-run-as-uid = \"0\"\nprov-db-volume-uid = \"999:1001\"\n", "0", "999:1001"},
	} {
		v := viper.New()
		v.SetConfigType("toml")
		if err := v.ReadConfig(strings.NewReader(tc.toml)); err != nil {
			t.Fatal(err)
		}
		var conf Config
		if err := v.Unmarshal(&conf); err != nil {
			t.Fatal(err)
		}
		if conf.ProvDBRunAsUID != tc.runAs || conf.ProvDBVolumeUID != tc.chown {
			t.Errorf("%q decoded as run-as %q chown %q, want %q and %q", strings.TrimSpace(tc.toml), conf.ProvDBRunAsUID, conf.ProvDBVolumeUID, tc.runAs, tc.chown)
		}
	}
}
