package regtest

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	ini "gopkg.in/ini.v1"
)

// TestAppConditionalVariablesOpenSVC (#2282): an app variable's per-agent values reach the
// app container through keywords scoped to each agent. A two-agent flex app gets an env and
// a secret variable with a value per agent and is provisioned; the cfg/sec keys and the
// scoped keywords of the live service definition are checked. One agent's values are then
// removed, the maps pushed and the definition refreshed (update-opensvc-config and
// update-opensvc-template): its keys and keywords must be gone, the other agent's kept.
// The environment inside the containers is not read from here (repman has no access to a
// node's containers); the per-node env check is a lab step.
func (regtest *RegTest) TestAppConditionalVariablesOpenSVC(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST app-conditional-variables: "+format, args...)
	}

	svc := cl.OpenSVCConnect()
	if !svc.IsV3() || cl.Conf.ProvOpensvcUseCollectorAPI {
		test.SkipReason = "the service definition is read through the OpenSVC v3 cluster API"
		return false
	}
	var agents []string
	for _, a := range strings.Split(cl.GetAppAgents(nil), ",") {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(agents, a) {
			agents = append(agents, a)
		}
	}
	if len(agents) < 2 {
		test.SkipReason = fmt.Sprintf("needs two app agents, the cluster has %q", cl.GetAppAgents(nil))
		return false
	}
	a1, a2 := agents[0], agents[1]

	name := fmt.Sprintf("rtcond%d", time.Now().Unix()%100000)
	if err := cl.AddSeededApp(name, "80", "nginx:alpine", "", false); err != nil {
		logf(config.LvlErr, "FAIL adding app %s: %s", name, err)
		return false
	}
	var app *cluster.App
	for _, a := range cl.Apps {
		if a != nil && a.Name == name {
			app = a
			break
		}
	}
	if app == nil {
		logf(config.LvlErr, "FAIL app %s not found after its add", name)
		return false
	}
	provisioned := false
	defer func() {
		if provisioned {
			if err := cl.OpenSVCUnprovisionAppService(app); err != nil {
				logf(config.LvlWarn, "teardown: unprovision of %s failed: %s", name, err)
			} else {
				cl.ClearAppProvisioned(app)
			}
		}
		if err := cl.RemoveAppMonitor(app.GetHost(), app.GetPort()); err != nil {
			logf(config.LvlWarn, "teardown: RemoveAppMonitor %s failed: %s", name, err)
		}
	}()

	encrypt := func(v string) string { return cl.Conf.GetEncryptedString(v) }
	app.Lock()
	// no route: the test needs no gateway entry
	app.AppConfig.Deployment.Routes = nil
	app.AppConfig.Deployment.EnforceSinglePrimary()
	app.AppConfig.ProvAppHATopology = "flex"
	app.AppConfig.ProvAppAgents = a1 + "," + a2
	app.AppConfig.Deployment.Variables = config.VariableMaps{
		{Name: "RT_ENV", Value: "env_default", Type: config.VariableTypeEnv,
			Conditional: config.AVSlice{{Agent: a1, Value: "env_a1"}, {Agent: a2, Value: "env_a2"}}},
		{Name: "RT_SEC", Value: encrypt("sec_default"), Type: config.VariableTypeSecret,
			Conditional: config.AVSlice{{Agent: a1, Value: encrypt("sec_a1")}, {Agent: a2, Value: encrypt("sec_a2")}}},
	}
	app.Unlock()

	provisioned = true
	if err := cl.InitAppService(app); err != nil {
		// the orchestrator may answer late (408) while the provisioning goes on: the
		// checks below read what was written
		logf(config.LvlWarn, "provision of %s returned: %s", name, err)
	}

	// check verifies the keys and the scoped keywords: present agents mapped, absent ones gone
	check := func(step string, present, absent []string) bool {
		ok := true
		fail := func(format string, args ...interface{}) {
			logf(config.LvlErr, "FAIL %s: "+format, append([]interface{}{step}, args...)...)
			ok = false
		}
		cfgKeys, err := svc.ListConfigKeys(cl.Name, name)
		if err != nil {
			fail("listing cfg keys: %s", err)
		}
		secKeys, err := svc.ListSecretKeys(cl.Name, name)
		if err != nil {
			fail("listing sec keys: %s", err)
		}
		raw, err := svc.GetObjectConfigFileV3(cl.Name, "svc", name)
		if err != nil {
			fail("reading the service definition: %s", err)
			return false
		}
		def, err := ini.Load(raw)
		if err != nil {
			fail("parsing the service definition: %s", err)
			return false
		}
		sec := def.Section("container#app")
		if !slices.Contains(cfgKeys, "RT_ENV") || !slices.Contains(secKeys, "RT_SEC") {
			fail("default keys missing: cfg %v sec %v", cfgKeys, secKeys)
		}
		for _, a := range present {
			if !slices.Contains(cfgKeys, "RT_ENV@"+a) || !slices.Contains(secKeys, "RT_SEC@"+a) {
				fail("per-agent keys of %s missing: cfg %v sec %v", a, cfgKeys, secKeys)
			}
			if got, want := sec.Key("configs_environment@"+a).String(), name+"/* RT_ENV="+name+"/RT_ENV@"+a; got != want {
				fail("configs_environment@%s = %q, want %q", a, got, want)
			}
			if got, want := sec.Key("secrets_environment@"+a).String(), name+"/* RT_SEC="+name+"/RT_SEC@"+a; got != want {
				fail("secrets_environment@%s = %q, want %q", a, got, want)
			}
		}
		for _, a := range absent {
			if slices.Contains(cfgKeys, "RT_ENV@"+a) || slices.Contains(secKeys, "RT_SEC@"+a) {
				fail("per-agent keys of %s still there: cfg %v sec %v", a, cfgKeys, secKeys)
			}
			if sec.HasKey("configs_environment@"+a) || sec.HasKey("secrets_environment@"+a) {
				fail("scoped keywords of %s still in the service definition", a)
			}
		}
		if got := sec.Key("configs_environment").String(); got != name+"/*" {
			fail("default configs_environment = %q, want %q", got, name+"/*")
		}
		if ok {
			logf("TEST", "%s: keys and keywords as expected (mapped %v, gone %v)", step, present, absent)
		}
		return ok
	}

	if !check("after provisioning", []string{a1, a2}, nil) {
		return false
	}

	// the instances come up before the next step: an orchestration still running would
	// race the refresh and the teardown
	deadline := time.Now().Add(4 * time.Minute)
	for {
		up, _ := svc.GetServiceNodeFromStateV3(app.GetServiceName())
		if slices.Contains(up, a1) && slices.Contains(up, a2) {
			break
		}
		if time.Now().After(deadline) {
			logf(config.LvlWarn, "instances of %s not both up after 4m (up: %v), going on", name, up)
			break
		}
		time.Sleep(5 * time.Second)
	}

	// remove a2's values, as the GUI does (conditional/modify), then apply them
	app.Lock()
	for i := range app.AppConfig.Deployment.Variables {
		v := &app.AppConfig.Deployment.Variables[i]
		kept := config.AVSlice{}
		for _, cd := range v.Conditional {
			if cd.Agent == a1 {
				kept = append(kept, cd)
			}
		}
		v.Conditional = kept
	}
	app.Unlock()
	if err := cl.OpenSVCPushAppVariableMaps(app); err != nil {
		logf(config.LvlErr, "FAIL update-opensvc-config: %s", err)
		return false
	}
	if err := cl.OpenSVCUpdateAppTemplate(app); err != nil {
		logf(config.LvlErr, "FAIL update-opensvc-template: %s", err)
		return false
	}
	return check("after removing "+a2, []string{a1}, []string{a2})
}
