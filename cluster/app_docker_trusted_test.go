package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// prov-app-docker-trusted: a trusted app renders under the node's runtime (no --runtime, the
// reload as a docker task in the app's PID namespace); the other apps keep the sandbox.
func TestAppDockerTrustedRendersWithoutSandbox(t *testing.T) {
	c, a := sandboxTestApp("auto", true)
	a.Id = "ap111"
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -HUP 1"}}

	c.Conf.ProvAppDockerTrusted = "ap999, ap111"
	s, err := c.OpenSVCGetAppTemplateSectionMap(a)
	if err != nil {
		t.Fatal(err)
	}
	for name, sec := range s {
		if strings.Contains(sec["run_args"], "--runtime") {
			t.Fatalf("trusted app: %s has a runtime: %q", name, sec["run_args"])
		}
	}
	if r := s["task#reload"]; r["type"] != "docker" || r["pidns"] != "container#app" {
		t.Fatalf("trusted app: task#reload must be the docker task: %v", r)
	}

	// another app of the list is trusted, not this one: the sandbox stays
	c.Conf.ProvAppDockerTrusted = "ap999"
	s, err = c.OpenSVCGetAppTemplateSectionMap(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := s["container#app"]["run_args"]; !strings.HasSuffix(got, " --runtime=runsc-hostnet") {
		t.Fatalf("untrusted app keeps the sandbox: %q", got)
	}

	// an invalid runtime stops the rendering of a trusted app too: the setting is wrong
	c.Conf.ProvAppDockerTrusted = "ap111"
	c.Conf.ProvAppDockerRuntime = "runc --privileged"
	if _, err := c.OpenSVCGetAppTemplateSectionMap(a); err == nil {
		t.Fatal("an invalid runtime renders nothing, trusted app or not")
	}
}

// An app without an id (not yet listed) is never trusted, whatever the list.
func TestAppDockerTrustedNeedsAnID(t *testing.T) {
	c, a := sandboxTestApp("runsc-hostnet", false)
	c.Conf.ProvAppDockerTrusted = "ap1"
	if rt, err := c.appDockerRuntime(a); err != nil || rt != "runsc-hostnet" {
		t.Fatalf("app without id: %q, %v", rt, err)
	}
}

// WARN0329: the trusted apps of the cluster, named, while the sandbox is on; nothing when the
// sandbox is off (WARN0328 says it), when no app of this cluster is trusted, or once untrusted.
func TestAppDockerTrustedState(t *testing.T) {
	newCluster := func(runtime, trusted string, cloud18 bool) *Cluster {
		c := &Cluster{Name: "c1", Conf: &config.Config{ProvAppDockerRuntime: runtime, ProvAppDockerTrusted: trusted, Cloud18: cloud18},
			SecurityStateMachine: new(state.StateMachine)}
		c.SecurityStateMachine.Init()
		c.Apps = []*App{{Id: "ap1", Name: "bo", ClusterGroup: c}, {Id: "ap2", Name: "shop", ClusterGroup: c}}
		return c
	}
	for _, tc := range []struct {
		runtime, trusted string
		cloud18, want    bool
	}{
		{"auto", "ap1", true, true},
		{"runsc-hostnet", "ap1", false, true},
		{"auto", "ap1", false, false}, // auto off Cloud18: no sandbox to leave
		{"none", "ap1", true, false},  // sandbox off: WARN0328's case
		{"auto", "ap9", true, false},  // an app of another cluster
		{"auto", "", true, false},
	} {
		c := newCluster(tc.runtime, tc.trusted, tc.cloud18)
		c.CheckAppDockerTrusted()
		if got := c.SecurityStateMachine.CurState.Search("WARN0329"); got != tc.want {
			t.Errorf("%+v: WARN0329 open = %v", tc, got)
		}
	}
	c := newCluster("auto", "ap2,ap1", true)
	c.CheckAppDockerTrusted()
	st := (*c.SecurityStateMachine.CurState)["WARN0329"]
	if st.ErrType != "WARNING" || st.ErrFrom != "SECURITY" || !strings.Contains(st.ErrDesc, "bo, shop") || !strings.Contains(st.ErrDesc, "runsc-hostnet") {
		t.Fatalf("WARN0329: %+v", st)
	}
}
