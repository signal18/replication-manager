package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestProvDbDockerXtrabackupImgDecodesTOML(t *testing.T) {
	for _, tc := range []struct{ toml, want string }{
		{"", ""},
		{"prov-db-docker-xtrabackup-img = \"percona/percona-xtrabackup:8.4\"\n", "percona/percona-xtrabackup:8.4"},
		{"prov-db-docker-xtrabackup-img = \"auto\"\n", "auto"},
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
		if conf.ProvDbDockerXtrabackupImg != tc.want {
			t.Errorf("%q decoded as %q, want %q", strings.TrimSpace(tc.toml), conf.ProvDbDockerXtrabackupImg, tc.want)
		}
	}
}
