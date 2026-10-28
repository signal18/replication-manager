package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/regtest"
)

// The must-set variables of a test go through setClusterSetting, as the API sets them; an
// unknown key stops the test from running (#2198).
func TestSetTestVariables(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = config.NewSecrets(nil)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)
	cl.Conf.RplChecks = false
	cl.Conf.FailMaxDelay = 30

	if err := repman.setTestVariables(cl, "testSwitchover", map[string]string{"check-replication-state": "true", "failover-max-slave-delay": "0"}); err != nil {
		t.Fatalf("setTestVariables: %v", err)
	}
	if !cl.Conf.RplChecks || cl.Conf.FailMaxDelay != 0 {
		t.Fatalf("check-replication-state %t, failover-max-slave-delay %d", cl.Conf.RplChecks, cl.Conf.FailMaxDelay)
	}
	if err := repman.setTestVariables(cl, "testSwitchover", map[string]string{"no-such-setting": "1"}); err == nil {
		t.Fatal("an unknown setting must stop the test")
	}
}

// Every must-set variable a test declares is a key the API sets: testFailoverManual's
// failover-falsepositive-heartbeat was not, and the test ended ERR (#2208).
func TestEveryMustSetVariableIsSettable(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = config.NewSecrets(nil)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)
	for name, sets := range regtest.ConfigVariablesMustSet() {
		for _, variables := range sets {
			if err := repman.setTestVariables(cl, name, variables); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}
