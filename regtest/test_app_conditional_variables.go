package regtest

import (
	"encoding/base64"
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

// The proxies regtest app answers one HTTP request with one query through its DB_HOST: 200
// with the client address the primary saw (the proxy the connection came through), 503 when
// the query fails. The handler is written from base64 at start: no quoting or {...} for the
// orchestrator to read in the run command.
const (
	rtProxApp   = "rtcondprx"
	rtProxImage = "mariadb:11.4"
	rtProxPort  = "8080"
	rtProxHTTP  = `#!/bin/bash
read -r line
while read -r h; do h=$(printf "%s" "$h" | tr -d "\r"); [ -z "$h" ] && break; done
r=$(MYSQL_PWD="$DB_PASS" mariadb -h"$DB_HOST" -P"$DB_PORT" -u"$DB_USER" --skip-ssl-verify-server-cert --connect-timeout=2 -N -s -e "select host from information_schema.processlist where id=connection_id()" 2>&1 | tr "\n" " ")
case "$r" in [0-9]*) s="200 OK";; *) s="503 Service Unavailable";; esac
b="db_saw=$r"
printf "HTTP/1.0 %s\r\nContent-Type: text/plain\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s\n" "$s" "$(( $(printf "%s\n" "$b" | wc -c) ))" "$b"
`
)

// TestAppConditionalProxiesOpenSVC (#2282, the issue's "Observed" case): a flex app on two
// agents reaches its database through {{proxies.0.host}} by default, and through the proxy
// placed on its own agent by a per-agent value of DB_HOST. Each instance must answer through
// its own proxy; with one agent's app instance and proxy stopped, the other instance keeps
// answering through its proxy (both ways); with one proxy only stopped, the instance of that
// agent fails (no fallback proxy, expected) and recovers when the proxy is back, without an
// app restart. Needs two proxies placed on two app agents (else SKIP). The checks call each
// instance's address, never the app name (the orchestrator lists a restarted instance in the
// name only ~45 s after it is up). The app keeps a fixed name: its schema and user, created
// by app-db-auto-create and never dropped by repman, are reused by the next run.
func (regtest *RegTest) TestAppConditionalProxiesOpenSVC(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST app-conditional-proxies: "+format, args...)
	}

	svc := cl.OpenSVCConnect()
	if !svc.IsV3() || cl.Conf.ProvOpensvcUseCollectorAPI {
		test.SkipReason = "instances are stopped and started through the OpenSVC v3 cluster API"
		return false
	}
	if cl.GetMaster() == nil {
		test.SkipReason = "the cluster has no primary"
		return false
	}
	// one proxy placed on each of two app agents
	byAgent := map[string]cluster.DatabaseProxy{}
	for _, p := range cl.Proxies {
		if p == nil || p.GetWritePort() == 0 {
			continue
		}
		if _, seen := byAgent[p.GetAgent()]; !seen && p.GetAgent() != "" {
			byAgent[p.GetAgent()] = p
		}
	}
	var agents []string
	for _, a := range strings.Split(cl.GetAppAgents(nil), ",") {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(agents, a) && byAgent[a] != nil {
			agents = append(agents, a)
		}
	}
	if len(agents) < 2 || len(cl.Proxies) == 0 {
		test.SkipReason = fmt.Sprintf("needs two proxies placed on two app agents (app agents %q, %d proxies)", cl.GetAppAgents(nil), len(cl.Proxies))
		return false
	}
	a1, a2 := agents[0], agents[1]
	p1, p2 := byAgent[a1], byAgent[a2]

	if err := cl.AddSeededApp(rtProxApp, rtProxPort, rtProxImage, "", false); err != nil {
		logf(config.LvlErr, "FAIL adding app %s: %s", rtProxApp, err)
		return false
	}
	var app *cluster.App
	for _, a := range cl.Apps {
		if a != nil && a.Name == rtProxApp {
			app = a
			break
		}
	}
	if app == nil {
		logf(config.LvlErr, "FAIL app %s not found after its add", rtProxApp)
		return false
	}
	stopped := map[string]string{} // service name -> node, restarted by the teardown
	provisioned := false
	defer func() {
		for s, node := range stopped {
			if err := svc.StartInstanceV3(node, s); err != nil {
				logf(config.LvlWarn, "teardown: starting %s on %s failed: %s", s, node, err)
			}
		}
		if provisioned {
			if err := cl.OpenSVCUnprovisionAppService(app); err != nil {
				logf(config.LvlWarn, "teardown: unprovision of %s failed: %s", rtProxApp, err)
			} else {
				cl.ClearAppProvisioned(app)
			}
		}
		if err := cl.RemoveAppMonitor(app.GetHost(), app.GetPort()); err != nil {
			logf(config.LvlWarn, "teardown: RemoveAppMonitor %s failed: %s", rtProxApp, err)
		}
	}()

	// resolved before the app lock: the substitution view takes that lock itself
	defaultHost := cl.ResolveEnvVariableValue(app, "{{proxies.0.host}}")
	app.Lock()
	app.AppConfig.Deployment.Routes = nil // no gateway entry
	app.AppConfig.Deployment.EnforceSinglePrimary()
	app.AppConfig.ProvAppHATopology = "flex"
	app.AppConfig.ProvAppAgents = a1 + "," + a2
	app.AppConfig.ProvAppDockerCmd = `bash -c "echo ` + base64.StdEncoding.EncodeToString([]byte(rtProxHTTP)) +
		` | base64 -d > /tmp/h.sh && exec socat TCP-LISTEN:` + rtProxPort + `,reuseaddr,fork EXEC:'bash /tmp/h.sh'"`
	app.AppConfig.AppDbAutoCreate = true
	app.AppConfig.AppDbOwned = true // a previous run's schema and user are this app's
	if err := cl.ApplyAppDbDefaults(app.AppConfig); err != nil {
		app.Unlock()
		logf(config.LvlErr, "FAIL app database defaults: %s", err)
		return false
	}
	port := fmt.Sprintf("%d", p1.GetWritePort())
	app.AppConfig.Deployment.Variables = config.VariableMaps{
		{Name: "DB_HOST", Value: defaultHost, Type: config.VariableTypeEnv,
			Conditional: config.AVSlice{{Agent: a1, Value: p1.GetHost()}, {Agent: a2, Value: p2.GetHost()}}},
		{Name: "DB_PORT", Value: port, Type: config.VariableTypeEnv},
		{Name: "DB_USER", Value: app.AppConfig.AppDbUser, Type: config.VariableTypeEnv},
		// the stored (encrypted) password: a secret variable is decrypted into the app's sec object
		{Name: "DB_PASS", Value: app.AppConfig.AppDbPass, Type: config.VariableTypeSecret},
	}
	app.Unlock()
	logf("TEST", "app %s on %s and %s, DB_HOST %s on %s, %s on %s (default %s)", rtProxApp, a1, a2,
		p1.GetHost(), a1, p2.GetHost(), a2, app.AppConfig.Deployment.Variables[0].Value)

	provisioned = true
	if err := cl.InitAppService(app); err != nil {
		// the orchestrator may answer late (408) while the provisioning goes on
		logf(config.LvlWarn, "provision of %s returned: %s", rtProxApp, err)
	}

	// ipOf reads a service instance's address on node from the object status (ip#01)
	ipOf := func(serviceName, node string) string {
		parts := strings.SplitN(serviceName, "/", 3)
		if len(parts) != 3 {
			return ""
		}
		raw, err := svc.GetObjectV3(parts[0], parts[1], parts[2], func(b []byte) ([]byte, error) { return b, nil })
		if err != nil {
			return ""
		}
		return gjson.GetBytes(raw, "data.instances."+gjson.Escape(node)+`.status.resources.ip\#01.info.ipaddr`).String()
	}
	// ask sends one request to the node's instance: the HTTP status and the address the
	// primary saw (0 and "" when the instance does not answer)
	ask := func(ip string) (int, string) {
		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Get("http://" + net.JoinHostPort(ip, rtProxPort) + "/")
		if err != nil {
			return 0, ""
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		saw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(body)), "db_saw="))
		host, _, _ := net.SplitHostPort(strings.Fields(saw + " ")[0])
		return resp.StatusCode, host
	}
	// throughOwnProxy waits until the node's instance answers 200 through its own proxy
	throughOwnProxy := func(step, node string, p cluster.DatabaseProxy) bool {
		deadline := time.Now().Add(4 * time.Minute)
		var code int
		var saw, appIP, proxyIP string
		for {
			appIP, proxyIP = ipOf(app.GetServiceName(), node), ipOf(p.GetServiceName(), node)
			if appIP != "" && proxyIP != "" {
				code, saw = ask(appIP)
				if code == http.StatusOK && saw == proxyIP {
					logf("TEST", "%s: %s instance %s answers 200 through %s (%s)", step, node, appIP, p.GetName(), proxyIP)
					return true
				}
			}
			if time.Now().After(deadline) {
				logf(config.LvlErr, "FAIL %s: %s instance %q answered %d through %q, want 200 through %s (%q)", step, node, appIP, code, saw, p.GetName(), proxyIP)
				return false
			}
			time.Sleep(5 * time.Second)
		}
	}
	// holds asks the node's instance every second for d: every answer must be 200 through
	// its own proxy
	holds := func(step, node string, p cluster.DatabaseProxy, d time.Duration) bool {
		appIP, proxyIP := ipOf(app.GetServiceName(), node), ipOf(p.GetServiceName(), node)
		n, bad := 0, 0
		for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(time.Second) {
			n++
			if code, saw := ask(appIP); code != http.StatusOK || saw != proxyIP {
				bad++
				logf(config.LvlErr, "FAIL %s: %s instance answered %d through %q, want 200 through %s (%s)", step, node, code, saw, p.GetName(), proxyIP)
			}
		}
		if bad == 0 {
			logf("TEST", "%s: %s instance answered %d/%d requests 200 through %s", step, node, n, n, p.GetName())
		}
		return bad == 0
	}
	stop := func(node, serviceName string) bool {
		if err := svc.StopInstanceV3(node, serviceName); err != nil {
			logf(config.LvlErr, "FAIL stopping %s on %s: %s", serviceName, node, err)
			return false
		}
		stopped[serviceName] = node
		return true
	}
	start := func(node, serviceName string) bool {
		if err := svc.StartInstanceV3(node, serviceName); err != nil {
			logf(config.LvlErr, "FAIL starting %s on %s: %s", serviceName, node, err)
			return false
		}
		delete(stopped, serviceName)
		return true
	}
	// stopPair stops one agent's app instance and proxy, the other instance must hold
	stopPair := func(down string, pd cluster.DatabaseProxy, up string, pu cluster.DatabaseProxy) bool {
		step := "with " + down + "'s app instance and " + pd.GetName() + " stopped"
		upIP := ipOf(app.GetServiceName(), up)
		if !stop(down, app.GetServiceName()) || !stop(down, pd.GetServiceName()) {
			return false
		}
		// the stop returns once requested: hold until both are really down, then 30 s more
		for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(time.Second) {
			if code, _ := ask(ipOf(app.GetServiceName(), down)); code == 0 || ipOf(app.GetServiceName(), down) == "" {
				break
			}
			if time.Now().After(deadline) {
				logf(config.LvlErr, "FAIL %s: %s instance still answers", step, down)
				return false
			}
			if code, saw := ask(upIP); code != http.StatusOK || saw != ipOf(pu.GetServiceName(), up) {
				logf(config.LvlErr, "FAIL %s: %s instance answered %d through %q while %s was stopping", step, up, code, saw, down)
				return false
			}
		}
		if !holds(step, up, pu, 30*time.Second) {
			return false
		}
		return start(down, pd.GetServiceName()) && start(down, app.GetServiceName()) &&
			throughOwnProxy("after restarting "+down+"'s pair", down, pd)
	}

	if !throughOwnProxy("after provisioning", a1, p1) || !throughOwnProxy("after provisioning", a2, p2) {
		return false
	}
	if !stopPair(a1, p1, a2, p2) || !stopPair(a2, p2, a1, p1) {
		return false
	}

	// one proxy only: its agent's instance stays up and fails (no fallback proxy), the other
	// holds; back through its proxy once the proxy is started again, the app not restarted
	step := "with " + p1.GetName() + " only stopped"
	a1IP := ipOf(app.GetServiceName(), a1)
	if !stop(a1, p1.GetServiceName()) {
		return false
	}
	failed := false
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if code, _ := ask(a1IP); code == http.StatusServiceUnavailable {
			failed = true
			break
		}
	}
	if !failed {
		logf(config.LvlErr, "FAIL %s: %s instance never answered 503 (its proxy is down, it has no other)", step, a1)
		return false
	}
	logf("TEST", "%s: %s instance answers 503, as expected without a fallback proxy", step, a1)
	if !holds(step, a2, p2, 20*time.Second) || !start(a1, p1.GetServiceName()) {
		return false
	}
	return throughOwnProxy("after restarting "+p1.GetName(), a1, p1)
}
