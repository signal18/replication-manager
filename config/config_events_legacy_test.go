package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The former definition-browser settings existed only on unreleased develop.
// Keep accepting a configuration file that still contains them so an upgrade
// does not refuse the rest of the cluster configuration.
func TestLegacyMonitoringEventStatusKeysAreIgnored(t *testing.T) {
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("monitoring-event-status = true\nmonitoring-event-status-max-definitions = 100\nmonitoring-event-status-max-definition-bytes = 1048576\nmonitoring-schema-events = true\n")); err != nil {
		t.Fatal(err)
	}
	var conf Config
	if err := v.Unmarshal(&conf); err != nil {
		t.Fatalf("legacy event settings must not prevent config decoding: %v", err)
	}
	if !conf.MonitorSchemaEvents {
		t.Fatal("the supported monitoring-schema-events setting was not decoded")
	}
}
