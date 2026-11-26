package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// WARN0328: a Cloud18 instance whose client apps run without the sandbox runtime.
func TestAppSandboxRuntimeState(t *testing.T) {
	open := func(runtime string, cloud18 bool, apps int) bool {
		c := &Cluster{Name: "c1", Conf: &config.Config{ProvAppDockerRuntime: runtime, Cloud18: cloud18},
			SecurityStateMachine: new(state.StateMachine)}
		c.SecurityStateMachine.Init()
		for i := 0; i < apps; i++ {
			c.Apps = append(c.Apps, &App{Name: "web", ClusterGroup: c})
		}
		c.CheckAppSandboxRuntime()
		return c.SecurityStateMachine.CurState.Search("WARN0328")
	}
	cases := []struct {
		runtime string
		cloud18 bool
		apps    int
		want    bool
	}{
		{"none", true, 1, true},
		{"", true, 2, true},
		{" none ", true, 1, true},
		{"auto", true, 1, false}, // the sandbox
		{config.ConstAppDockerRuntimeSandbox, true, 1, false},
		{"kata", true, 1, false},  // another runtime, chosen by the admin
		{"none", true, 0, false},  // no client app: nothing runs unsandboxed
		{"none", false, 1, false}, // not a Cloud18 instance: the node's runtime is the norm
		{"auto", false, 1, false},
		{"bad value!", true, 1, false}, // invalid: the provisioning stops with APPERR012
	}
	for _, tc := range cases {
		if got := open(tc.runtime, tc.cloud18, tc.apps); got != tc.want {
			t.Errorf("runtime %q cloud18 %v apps %d: WARN0328 open = %v, want %v", tc.runtime, tc.cloud18, tc.apps, got, tc.want)
		}
	}
	// the tick's ClearState then the check: open while off, closed once back to auto (the
	// current tick's states: IsInState reads the previous tick's)
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvAppDockerRuntime: "none", Cloud18: true},
		SecurityStateMachine: new(state.StateMachine)}
	c.SecurityStateMachine.Init()
	c.Apps = append(c.Apps, &App{Name: "web", ClusterGroup: c})
	c.CheckAppSandboxRuntime()
	if !c.SecurityStateMachine.CurState.Search("WARN0328") {
		t.Fatal("sandbox off: WARN0328 expected")
	}
	c.SecurityStateMachine.ClearState()
	c.Conf.ProvAppDockerRuntime = config.ConstAppDockerRuntimeAuto
	c.CheckAppSandboxRuntime()
	if c.SecurityStateMachine.CurState.Search("WARN0328") {
		t.Fatal("sandbox back on: WARN0328 closed on the next tick")
	}
	if len(c.SecurityStateMachine.GetOpenStates()) != 0 {
		t.Fatalf("no open security state left: %+v", c.SecurityStateMachine.GetOpenStates())
	}
	// a nil security state machine (a cluster not initialised yet) is not a panic
	(&Cluster{Conf: &config.Config{Cloud18: true}}).CheckAppSandboxRuntime()
}
