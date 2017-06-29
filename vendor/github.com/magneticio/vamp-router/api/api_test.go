package api

import (
	"github.com/magneticio/vamp-router/haproxy"
	"github.com/magneticio/vamp-router/helpers"
	"github.com/magneticio/vamp-router/metrics"
	gologger "github.com/op/go-logging"
	"testing"
)

const (
	TEMPLATE_FILE = "../configuration/templates/haproxy_config.template"
	CONFIG_FILE   = "/tmp/vamp_lb_test.cfg"
	EXAMPLE       = "../test/test_config1.json"
	JSON_FILE     = "/tmp/vamp_lb_test.json"
	PID_FILE      = "/tmp/vamp_lb_test.pid"
	LOG_PATH      = "/tmp/vamp_lb_test.log"
)

func TestApi_CreateAPI(t *testing.T) {

	log := gologger.MustGetLogger("vamp-router")

	sseChannel := make(chan metrics.Metric)

	sseBroker := &metrics.SSEBroker{
		make(map[chan metrics.Metric]bool),
		make(chan (chan metrics.Metric)),
		make(chan (chan metrics.Metric)),
		sseChannel,
		log,
	}

	haConfig := haproxy.Config{TemplateFile: TEMPLATE_FILE, ConfigFile: CONFIG_FILE, JsonFile: JSON_FILE, PidFile: PID_FILE}
	haRuntime := haproxy.Runtime{Binary: helpers.HaproxyLocation()}

	if _, err := CreateApi(log, &haConfig, &haRuntime, sseBroker, "v.test"); err != nil {
		t.Errorf("Failed to create API")
	}

}
