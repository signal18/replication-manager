package regtest

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/tidwall/gjson"
	ini "gopkg.in/ini.v1"
)

// The regtest app serves its own environment: at start the container writes `env` into
// /www/env, served by busybox httpd, so the regtest reads each node's container env.
const (
	rtCondImage   = "busybox:1.36"
	rtCondEnvPort = "8080"
	rtCondEnvCmd  = "sh -c 'mkdir -p /www && env > /www/env && exec httpd -f -p 8080 -h /www'"
)

// TestAppConditionalVariablesOpenSVC (#2282): an app variable's per-agent values reach the
// app container through keywords scoped to each agent. One app, two agents, an env and a
// secret variable with a value per agent: after provisioning, the cfg/sec keys and the
// scoped keywords of the live service definition are checked, and the env of each node's
// container carries that node's values. One agent's values are then removed and applied as
// the issue states it, update-opensvc-config plus a recreate of the container (an instance
// restart that does not refresh the definition itself): its keys and keywords are gone, its
// container gets the default values, the other node keeps its own.
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
	if err := cl.AddSeededApp(name, rtCondEnvPort, rtCondImage, "", false); err != nil {
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
	app.AppConfig.ProvAppDockerCmd = rtCondEnvCmd
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

	// instanceIP reads a node's address of the app from the object status (ip#01)
	instanceIP := func(node string) string {
		raw, err := svc.GetObjectV3(cl.Name, "svc", name, func(b []byte) ([]byte, error) { return b, nil })
		if err != nil {
			return ""
		}
		return gjson.GetBytes(raw, "data.instances."+gjson.Escape(node)+`.status.resources.ip\#01.info.ipaddr`).String()
	}
	// readEnv fetches the environment the node's container started with
	readEnv := func(node string) (map[string]string, error) {
		ip := instanceIP(node)
		if ip == "" {
			return nil, fmt.Errorf("no ip#01 address for %s", node)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + net.JoinHostPort(ip, rtCondEnvPort) + "/env")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, ip)
		}
		env := make(map[string]string)
		for _, line := range strings.Split(string(body), "\n") {
			if k, v, found := strings.Cut(line, "="); found {
				env[k] = v
			}
		}
		return env, nil
	}
	// checkEnv waits until the node's container carries the expected values (a container not
	// recreated yet still serves its old env), fails at the deadline. The secret value is
	// logged only as matching or not.
	checkEnv := func(step, node, wantEnv, wantSec string) bool {
		deadline := time.Now().Add(4 * time.Minute)
		var gotEnv string
		var secOK bool
		var lastErr error
		for {
			env, err := readEnv(node)
			if err == nil {
				gotEnv, secOK = env["RT_ENV"], env["RT_SEC"] == wantSec
				if gotEnv == wantEnv && secOK {
					logf("TEST", "%s: %s container has RT_ENV=%s and the expected RT_SEC", step, node, gotEnv)
					return true
				}
			}
			lastErr = err
			if time.Now().After(deadline) {
				logf(config.LvlErr, "FAIL %s: %s container RT_ENV=%q (want %q), RT_SEC as expected: %v, last error: %v", step, node, gotEnv, wantEnv, secOK, lastErr)
				return false
			}
			time.Sleep(5 * time.Second)
		}
	}

	if !check("after provisioning", []string{a1, a2}, nil) {
		return false
	}
	if !checkEnv("after provisioning", a1, "env_a1", "sec_a1") || !checkEnv("after provisioning", a2, "env_a2", "sec_a2") {
		return false
	}

	// remove a2's values, as the GUI does (conditional/modify), then apply them as the issue
	// states it: update-opensvc-config, then a recreate of a2's container
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
	if err := cl.OpenSVCUpdateAppConfig(app); err != nil {
		logf(config.LvlErr, "FAIL update-opensvc-config: %s", err)
		return false
	}
	if !check("after removing "+a2+" and update-opensvc-config", []string{a1}, []string{a2}) {
		return false
	}
	// an instance restart that does not refresh the definition: what update-opensvc-config
	// wrote must be enough
	if err := svc.RestartInstanceV3(a2, app.GetServiceName()); err != nil {
		logf(config.LvlErr, "FAIL restarting the instance on %s: %s", a2, err)
		return false
	}
	return checkEnv("after removing "+a2, a2, "env_default", "sec_default") &&
		checkEnv("after removing "+a2, a1, "env_a1", "sec_a1")
}
