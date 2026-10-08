// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// Consumer side of self-service clusters (issue #1838): from this instance, act
// on a Cloud18 infrastructure (a partner's replication-manager, one of the
// api-public-url of the marketplace) as this instance's Cloud18 identity, the
// same way the dashboard's peer proxy does: log into the partner's API with the
// GitLab credentials, then call it. The partner applies its self-service rules
// (cloud18-self-service-clusters, per-user limit) and its ACL; nothing here
// grants anything.

const peerCallTimeout = 45 * time.Second

// peerSession is an authenticated session on a partner infrastructure.
type peerSession struct {
	base     string // the infrastructure's public API URL, as shown in answers
	callBase string // where requests go: base, or the loopback API when the infrastructure is this instance
	token    string
}

// endpoint is where a request to the session goes.
func (s *peerSession) endpoint() string {
	if s.callBase != "" {
		return s.callBase
	}
	return s.base
}

// peerLogin authenticates on the infrastructure as the Cloud18 GitLab identity.
func (repman *ReplicationManager) peerLogin(infra string) (*peerSession, error) {
	base := strings.TrimRight(strings.TrimSpace(infra), "/")
	if base == "" {
		return nil, errors.New("infrastructure (api-public-url) is required")
	}
	if repman.PeerManager == nil || !repman.PeerManager.HasPeerURL(base) {
		return nil, fmt.Errorf("%s is not a known Cloud18 infrastructure: pick one from list-cloud18-infrastructures", base)
	}
	if repman.Conf.Cloud18GitUser == "" {
		return nil, errors.New("this instance has no Cloud18 identity: register first (cloud18-register)")
	}
	loginURL, err := url.Parse(base + "/api/login")
	if err != nil {
		return nil, err
	}
	creds := userCredentials{
		Username: repman.Conf.Cloud18GitUser,
		Password: repman.Conf.GetDecryptedPassword("git-password", repman.Conf.Secrets["cloud18-gitlab-password"].Value),
	}
	status, body := repman.PeerLogin(loginURL, creds)
	if status != http.StatusOK {
		return nil, fmt.Errorf("login on %s refused (HTTP %d): %s", base, status, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Token == "" {
		return nil, fmt.Errorf("login on %s returned no token", base)
	}
	return &peerSession{base: base, token: resp.Token}, nil
}

// call performs one API call on the infrastructure.
func (s *peerSession) call(method, path string, payload any) (int, []byte, error) {
	return s.callWithTimeout(method, path, payload, peerCallTimeout)
}

func (s *peerSession) callWithTimeout(method, path string, payload any, timeout time.Duration) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.endpoint()+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, nil
}

func (s *peerSession) mustOK(method, path string, payload any) ([]byte, error) {
	status, body, err := s.call(method, path, payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return body, fmt.Errorf("%s %s answered HTTP %d: %s", method, path, status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// Cloud18ClusterSpec is what an assistant asks for.
type Cloud18ClusterSpec = repmanmcp.Cloud18ClusterSpec

func normalizeSpec(spec Cloud18ClusterSpec) (Cloud18ClusterSpec, error) {
	spec.ClusterName = strings.TrimSpace(strings.ToLower(spec.ClusterName))
	if spec.ClusterName == "" {
		return spec, errors.New("cluster_name is required")
	}
	if strings.ContainsAny(spec.ClusterName, " ./:") {
		return spec, errors.New("cluster_name must be a plain name (letters, digits, dashes)")
	}
	if spec.DBImage == "" {
		spec.DBImage = "mariadb:lts"
	}
	if spec.DBCount <= 0 {
		spec.DBCount = 2
	}
	if spec.DBCount > 5 {
		return spec, errors.New("db_count above 5 is not a self-service size")
	}
	spec.Proxy = strings.ToLower(strings.TrimSpace(spec.Proxy))
	switch spec.Proxy {
	case "", "haproxy", "proxysql", "none":
	default:
		return spec, fmt.Errorf("proxy must be haproxy, proxysql or none, got %q", spec.Proxy)
	}
	if spec.Proxy == "" {
		spec.Proxy = "haproxy"
	}
	// phpMyAdmin is deployed by default; apps=none opts out.
	apps := []string{}
	optOut := false
	for _, a := range spec.Apps {
		a = strings.TrimSpace(a)
		if strings.EqualFold(a, "none") {
			optOut = true
			continue
		}
		if a != "" {
			apps = append(apps, a)
		}
	}
	if len(apps) == 0 && !optOut {
		apps = []string{"phpmyadmin"}
	}
	spec.Apps = apps
	return spec, nil
}

// plannedHosts lists the services the tool will add, in order. Hosts are short
// names: the infrastructure appends its own service domain
// (.<cluster>.svc.<orchestrator cluster>) to every server, proxy and app.
func plannedHosts(spec Cloud18ClusterSpec) []map[string]string {
	out := []map[string]string{}
	for i := 1; i <= spec.DBCount; i++ {
		out = append(out, map[string]string{"type": "database", "host": fmt.Sprintf("db%d", i), "port": "3306", "image": spec.DBImage})
	}
	if spec.Proxy != "none" {
		out = append(out, map[string]string{"type": spec.Proxy, "host": spec.Proxy + "1", "port": "3306"})
	}
	for i, app := range spec.Apps {
		short := app
		if k := strings.LastIndex(short, "/"); k >= 0 {
			short = short[k+1:]
		}
		out = append(out, map[string]string{"type": "app", "host": fmt.Sprintf("%s%d", short, i+1), "port": "80", "template": app})
	}
	return out
}

// plannedAppURLs renders, per planned app, the URL its template's primary route
// gives once provisioned: https://<app>.<cluster>.<subDomain>-<zone>.<domain>.cloud18.io/
// ("" when the infrastructure declares no identity).
func plannedAppURLs(spec Cloud18ClusterSpec, ss map[string]any) []map[string]string {
	domain, _ := ss["domain"].(string)
	sub, _ := ss["subDomain"].(string)
	zone, _ := ss["zone"].(string)
	out := []map[string]string{}
	for _, h := range plannedHosts(spec) {
		if h["type"] != "app" {
			continue
		}
		u := ""
		if domain != "" && sub != "" {
			u = "https://" + h["host"] + "." + spec.ClusterName + "." + sub + "-" + zone + "." + domain + ".cloud18.io/"
		}
		out = append(out, map[string]string{"name": h["host"], "template": h["template"], "url": u, "note": "answers once the app is provisioned (cloud18-get-cluster lists the apps with their url)"})
	}
	return out
}

// provisionTimeout bounds the infrastructure's synchronous provision call,
// which waits for the databases to come up and bootstraps replication.
const provisionTimeout = 20 * time.Minute

// Cloud18CreateCluster plans (confirm=false) or creates (confirm=true) a cluster
// on an infrastructure, as this instance's Cloud18 identity.
func (repman *ReplicationManager) Cloud18CreateCluster(p *repmanmcp.Principal, spec Cloud18ClusterSpec, confirm bool) (map[string]any, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	sess, _, err := repman.peerLoginAs(p, spec.Infrastructure)
	if err != nil {
		return nil, err
	}
	// Self-service status on the infrastructure, for this identity.
	statusBody, err := sess.mustOK(http.MethodGet, "/api/cloud18/self-service", nil)
	if err != nil {
		return nil, fmt.Errorf("the infrastructure does not expose self-service (older release?): %w", err)
	}
	var ss map[string]any
	_ = json.Unmarshal(statusBody, &ss)
	// The apps are resolved against the templates the infrastructure can deploy: a
	// name with no template is refused here, never silently turned into a docker image.
	templates := []string{}
	if raw, ok := ss["appTemplates"].([]any); ok {
		for _, t := range raw {
			if n, ok := t.(string); ok {
				templates = append(templates, n)
			}
		}
	}
	unresolved := []string{}
	for i, a := range spec.Apps {
		if t := ResolveAppTemplate(a, templates); t != "" {
			spec.Apps[i] = t
		} else {
			unresolved = append(unresolved, a)
		}
	}
	plan := map[string]any{
		"infrastructure": sess.base,
		"identity":       repman.Conf.Cloud18GitUser,
		"cluster":        spec.ClusterName,
		"services":       plannedHosts(spec),
		"apps":           plannedAppURLs(spec, ss),
		"selfService":    ss,
		"unitPlan":       "the infrastructure's default DBU / APU / BKU (no service plan)",
	}
	if len(unresolved) > 0 {
		plan["refused"] = fmt.Sprintf("app template not available on the infrastructure: %s (templates: %s)", strings.Join(unresolved, ", "), strings.Join(templates, ", "))
		if !confirm {
			return plan, nil
		}
		return plan, fmt.Errorf("%s", plan["refused"])
	}
	if enabled, _ := ss["enabled"].(bool); !enabled {
		plan["refused"] = ss["reason"]
		if !confirm {
			return plan, nil
		}
		return plan, fmt.Errorf("self-service refused by %s: %v", sess.base, ss["reason"])
	}
	if rem, _ := ss["remaining"].(float64); rem <= 0 {
		plan["refused"] = fmt.Sprintf("limit reached: %v clusters already sponsored there", ss["used"])
		if !confirm {
			return plan, nil
		}
		return plan, fmt.Errorf("self-service limit reached on %s", sess.base)
	}
	// Templates for the apps must exist on the infrastructure; checked once the
	// cluster exists (the listing is per cluster), so only announced here.
	if !confirm {
		plan["next"] = "call again with confirm=true to create it; this is billable consumption on the infrastructure"
		return plan, nil
	}
	steps := []string{}
	fail := func(step string, err error) (map[string]any, error) {
		plan["steps"] = steps
		plan["failedStep"] = step
		return plan, err
	}
	// 1. Create the cluster (no plan: default units on the infrastructure).
	if _, err := sess.mustOK(http.MethodPost, "/api/clusters/actions/add/"+spec.ClusterName, map[string]string{"clusterName": spec.ClusterName}); err != nil {
		return fail("create cluster", err)
	}
	steps = append(steps, "cluster created")
	cpath := "/api/clusters/" + spec.ClusterName
	// 2. Database image, best effort: the infrastructure may pin it (immutable
	// setting), in which case the cluster runs the infrastructure's image.
	if _, err := sess.mustOK(http.MethodGet, cpath+"/settings/actions/set/prov-db-image/"+url.PathEscape(spec.DBImage), nil); err != nil {
		plan["dbImageNote"] = fmt.Sprintf("the infrastructure kept its own database image (%v)", err)
		steps = append(steps, "database image left to the infrastructure")
	} else {
		steps = append(steps, "database image "+spec.DBImage)
	}
	// 3. Servers, proxy, apps.
	for _, h := range plannedHosts(spec) {
		var err error
		switch h["type"] {
		case "database":
			_, err = sess.mustOK(http.MethodGet, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"], nil)
		case "app":
			// the route takes a docker IMAGE in the path and the template in the body: sent in
			// the path, "phpmyadmin/phpmyadmin" became the image of a template-less app
			// (preprod 2026-10-07, #1907)
			_, err = sess.mustOK(http.MethodPost, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"]+"/app", map[string]any{"template": h["template"]})
		default:
			_, err = sess.mustOK(http.MethodGet, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"]+"/"+h["type"], nil)
		}
		if err != nil {
			return fail("add "+h["type"]+" "+h["host"], err)
		}
		steps = append(steps, "added "+h["type"]+" "+h["host"])
	}
	// 4. Provision: databases and proxies through the cluster provision (the
	// infrastructure's call is synchronous: it waits for the databases and
	// bootstraps replication, minutes), then each app through its own route.
	// Runs in the background here, the outcome goes to the log.
	apps := []string{}
	for _, h := range plannedHosts(spec) {
		if h["type"] == "app" {
			apps = append(apps, h["host"])
		}
	}
	go func() {
		slow := *sess
		status, body, err := slow.callWithTimeout(http.MethodGet, cpath+"/services/actions/provision", nil, provisionTimeout)
		switch {
		case err != nil:
			repman.Logrus.Warnf("cloud18-create-cluster %s on %s: provision call failed: %v", spec.ClusterName, sess.base, err)
			return
		case status < 200 || status > 299:
			repman.Logrus.Warnf("cloud18-create-cluster %s on %s: provision answered HTTP %d: %s", spec.ClusterName, sess.base, status, strings.TrimSpace(string(body)))
			return
		default:
			repman.Logrus.Infof("cloud18-create-cluster %s on %s: databases and proxies provisioned", spec.ClusterName, sess.base)
		}
		// The app routes take the app id, not its name: resolve through the topology.
		ids := map[string]string{}
		if body, err := slow.mustOK(http.MethodGet, cpath+"/topology/apps", nil); err == nil {
			var list []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(body, &list) == nil {
				for _, a := range list {
					ids[a.Name] = a.ID
				}
			}
		}
		for _, app := range apps {
			id, ok := ids[app]
			if !ok {
				repman.Logrus.Warnf("cloud18-create-cluster %s on %s: app %s not found in the topology, not provisioned", spec.ClusterName, sess.base, app)
				continue
			}
			status, body, err := slow.callWithTimeout(http.MethodPost, cpath+"/apps/"+id+"/actions/provision", map[string]any{}, provisionTimeout)
			if err != nil || status < 200 || status > 299 {
				repman.Logrus.Warnf("cloud18-create-cluster %s on %s: app %s provision failed (HTTP %d, %v): %s", spec.ClusterName, sess.base, app, status, err, strings.TrimSpace(string(body)))
				continue
			}
			repman.Logrus.Infof("cloud18-create-cluster %s on %s: app %s provisioned", spec.ClusterName, sess.base, app)
		}
	}()
	steps = append(steps, "provisioning started")
	plan["steps"] = steps
	plan["next"] = "follow with get-cloud18-cluster; provisioning takes a few minutes"
	if topo, err := sess.mustOK(http.MethodGet, cpath+"/topology/servers", nil); err == nil {
		plan["servers"] = json.RawMessage(topo)
	}
	return plan, nil
}

// Cloud18CreateClusterToken mints, on the infrastructure and as this instance's
// Cloud18 identity (the sponsor), an API token scoped to one cluster there, so
// an assistant can be pointed at the infrastructure's own MCP endpoint for that
// cluster. The token is the sponsor's grants narrowed to what the form asks;
// it is returned once and never stored here.
func (repman *ReplicationManager) Cloud18CreateClusterToken(p *repmanmcp.Principal, infra, clusterName, label, grants string, expireDays int) (map[string]any, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return nil, errors.New("cluster_name is required")
	}
	if strings.TrimSpace(label) == "" {
		label = "assistant-" + clusterName
	}
	sess, _, err := repman.peerLoginAs(p, infra)
	if err != nil {
		return nil, err
	}
	form := map[string]any{"label": label, "grants": strings.TrimSpace(grants), "clusters": []string{clusterName}, "expireDays": expireDays}
	body, err := sess.mustOK(http.MethodPost, "/api/tokens", form)
	if err != nil {
		return nil, fmt.Errorf("the infrastructure refused the token (the sponsor needs token-create there): %w", err)
	}
	var tok map[string]any
	if err := json.Unmarshal(body, &tok); err != nil || tok["token"] == nil {
		return nil, fmt.Errorf("the infrastructure returned no token: %s", strings.TrimSpace(string(body)))
	}
	return map[string]any{
		"infrastructure": sess.base,
		"cluster":        clusterName,
		"identity":       repman.Conf.Cloud18GitUser,
		"id":             tok["id"],
		"label":          tok["label"],
		"grants":         tok["grants"],
		"expiresAt":      tok["expiresAt"],
		"token":          tok["token"],
		"mcpUrl":         sess.base + "/api/mcp/sse",
		"mcpServerConfig": map[string]any{
			"type":    "sse",
			"url":     sess.base + "/api/mcp/sse",
			"headers": map[string]string{"Authorization": "Bearer " + fmt.Sprint(tok["token"])},
		},
		"note": "the token is shown once; add mcpServerConfig to the assistant's MCP servers under a name such as the infrastructure's host, then that server exposes the cluster's tools",
	}, nil
}

// peerIdentity is who an infrastructure is asked to act for.
type peerIdentity struct {
	user     string
	password string
	mode     string // "caller" (the SSO user), "instance" (the registered identity), "refused"
	reason   string
}

// peerIdentityFor decides whose credentials reach an infrastructure, the same door as the
// dashboard's "Enter" on a peer (DynamicPeerHandler): a user logged in with the Cloud18
// (GitLab) account is themselves everywhere, so the infrastructure sees them and they
// sponsor what they create; a local admin without that identity acts as the instance's
// registered identity; anyone else (a local user, an API token: no Cloud18 identity to
// carry) is refused with the way in. Pure, for tests.
func peerIdentityFor(p *repmanmcp.Principal, decrypt func(string) string, isAdmin bool, instanceUser, instancePassword string) peerIdentity {
	if p == nil {
		return peerIdentity{mode: "refused", reason: "unauthenticated"}
	}
	switch p.AuthMethod {
	case "oidc":
		pwd, _ := p.Auth.(string)
		if pwd = decrypt(pwd); pwd == "" {
			return peerIdentity{mode: "refused", reason: "your Cloud18 session carries no credential: log in again with your Cloud18 (GitLab) account"}
		}
		return peerIdentity{user: p.User, password: pwd, mode: "caller"}
	case "token":
		return peerIdentity{mode: "refused", reason: "an API token carries no Cloud18 identity: log in with your Cloud18 (GitLab) account to act on an infrastructure"}
	}
	if isAdmin && instanceUser != "" {
		return peerIdentity{user: instanceUser, password: instancePassword, mode: "instance"}
	}
	return peerIdentity{mode: "refused", reason: "log in with your Cloud18 (GitLab) account to act on an infrastructure: a local user has no identity there"}
}

// peerSessionTTL bounds the reuse of a login on an infrastructure: one login per user and
// infrastructure per interval, not one per tool call.
const peerSessionTTL = 10 * time.Minute

type peerSessionEntry struct {
	sess *peerSession
	at   time.Time
}

// loopbackAPI is this instance's own API over the loopback: the plain-HTTP monitor port
// (http-port, 10001 by default), always served, whatever the TLS API port is bound to.
func (repman *ReplicationManager) loopbackAPI() string {
	port := strings.TrimSpace(repman.Conf.HttpPort)
	if port == "" {
		port = "10001"
	}
	return "http://127.0.0.1:" + port
}

// forgetPeerSession drops a cached login on an infrastructure (after a 401: password
// changed, session revoked), so the next call logs in again.
func (repman *ReplicationManager) forgetPeerSession(user, base string) {
	repman.peerSessionMu.Lock()
	defer repman.peerSessionMu.Unlock()
	delete(repman.peerSessions, user+"|"+base)
}

// peerLoginAs authenticates on the infrastructure as the principal (peerIdentityFor) and
// reuses the session within peerSessionTTL.
func (repman *ReplicationManager) peerLoginAs(p *repmanmcp.Principal, infra string) (*peerSession, string, error) {
	base := strings.TrimRight(strings.TrimSpace(infra), "/")
	self := strings.TrimRight(strings.TrimSpace(repman.Conf.APIPublicURL), "/")
	if base == "" || (self != "" && strings.EqualFold(base, self)) {
		// This instance, as the caller: the tool runs on the infrastructure's own MCP
		// (the assistant connected here with the caller's session or token) and its REST
		// is called over the loopback with that very credential, so every self-service
		// rule applies to the caller exactly as over REST.
		if p == nil || p.Bearer == "" {
			return nil, "", errors.New("no credential on this request to act as you on this instance")
		}
		return &peerSession{base: self, callBase: repman.loopbackAPI(), token: p.Bearer}, p.User, nil
	}
	if repman.PeerManager == nil || !repman.PeerManager.HasPeerURL(base) {
		return nil, "", fmt.Errorf("%s is not a known Cloud18 infrastructure: pick one from list-cloud18-infrastructures", base)
	}
	id := peerIdentityFor(p, func(v string) string { return repman.Conf.GetDecryptedPassword("peer-login", v) },
		p != nil && repman.isAdminUser(p.User), repman.Conf.Cloud18GitUser,
		repman.Conf.GetDecryptedPassword("git-password", repman.Conf.Secrets["cloud18-gitlab-password"].Value))
	if id.mode == "refused" {
		return nil, "", errors.New(id.reason)
	}
	key := id.user + "|" + base
	repman.peerSessionMu.Lock()
	if e, ok := repman.peerSessions[key]; ok && time.Since(e.at) < peerSessionTTL {
		repman.peerSessionMu.Unlock()
		return e.sess, id.user, nil
	}
	repman.peerSessionMu.Unlock()
	loginURL, err := url.Parse(base + "/api/login")
	if err != nil {
		return nil, "", err
	}
	status, body := repman.PeerLogin(loginURL, userCredentials{Username: id.user, Password: id.password})
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("login on %s as %s refused (HTTP %d): %s", base, id.user, status, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Token == "" {
		return nil, "", fmt.Errorf("login on %s returned no token", base)
	}
	sess := &peerSession{base: base, token: resp.Token}
	repman.peerSessionMu.Lock()
	if repman.peerSessions == nil {
		repman.peerSessions = map[string]peerSessionEntry{}
	}
	if len(repman.peerSessions) >= 1024 {
		repman.peerSessions = map[string]peerSessionEntry{}
	}
	repman.peerSessions[key] = peerSessionEntry{sess: sess, at: time.Now()}
	repman.peerSessionMu.Unlock()
	return sess, id.user, nil
}

// selfServiceStatusOf reads the caller's self-service status on the session's
// infrastructure (served there from its snapshot) and keeps the fields an assistant
// decides on.
func selfServiceStatusOf(sess *peerSession) (map[string]any, error) {
	ss, _, err := selfServiceStatusOfTimeout(sess, peerCallTimeout)
	return ss, err
}

// selfServiceStatusOfTimeout is selfServiceStatusOf with its own deadline, returning the
// HTTP status so a 401 can be told from the rest.
func selfServiceStatusOfTimeout(sess *peerSession, timeout time.Duration) (map[string]any, int, error) {
	status, body, err := sess.callWithTimeout(http.MethodGet, "/api/cloud18/self-service", nil, timeout)
	if err != nil {
		return nil, 0, err
	}
	if status < 200 || status > 299 {
		return nil, status, fmt.Errorf("the infrastructure does not expose self-service (older release?): GET /api/cloud18/self-service answered HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	var ss map[string]any
	if err := json.Unmarshal(body, &ss); err != nil {
		return nil, status, fmt.Errorf("self-service status of %s: %w", sess.base, err)
	}
	out := map[string]any{}
	for _, k := range []string{"enabled", "reason", "orchestrator", "maxClustersPerUser", "used", "remaining", "defaultDbu", "defaultApu", "defaultBku", "neededDbu", "neededApu", "pool", "poolOk", "poolNote", "borrowed", "appTemplates"} {
		if v, ok := ss[k]; ok {
			out[k] = v
		}
	}
	return out, status, nil
}

// accessStatusTimeout bounds the self-service status read of one infrastructure in
// Cloud18InfrastructuresAccess; accessParallel bounds how many infrastructures are asked
// at once: a few dead peers must not make the tool wait minutes.
const (
	accessStatusTimeout = 10 * time.Second
	accessParallel      = 4
)

// Cloud18InfrastructuresAccess logs the caller in, as themselves, on every infrastructure
// of the marketplace and returns one MCP server entry per infrastructure carrying that
// session, with the caller's self-service status there. No account and no token are
// written on the infrastructures: the session is the login the dashboard would get, it
// expires with api-token-timeout. An infrastructure that refuses the caller is listed
// with the reason. Infrastructures are asked accessParallel at a time; a cached session
// an infrastructure answers 401 to is dropped and the login done again, once.
func (repman *ReplicationManager) Cloud18InfrastructuresAccess(p *repmanmcp.Principal) ([]repmanmcp.Cloud18InfrastructureAccess, error) {
	list, err := repman.Cloud18Infrastructures()
	if err != nil {
		return nil, err
	}
	out := make([]repmanmcp.Cloud18InfrastructureAccess, len(list))
	sem := make(chan struct{}, accessParallel)
	var wg sync.WaitGroup
	for i, infra := range list {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = repman.cloud18InfrastructureAccessOne(p, url)
		}(i, infra.ApiPublicUrl)
	}
	wg.Wait()
	return out, nil
}

// cloud18InfrastructureAccessOne is one infrastructure's entry of Cloud18InfrastructuresAccess.
func (repman *ReplicationManager) cloud18InfrastructureAccessOne(p *repmanmcp.Principal, url string) repmanmcp.Cloud18InfrastructureAccess {
	entry := repmanmcp.Cloud18InfrastructureAccess{ApiPublicUrl: url}
	sess, as, err := repman.peerLoginAs(p, url)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	ss, status, err := selfServiceStatusOfTimeout(sess, accessStatusTimeout)
	if status == http.StatusUnauthorized {
		// a cached session the infrastructure no longer accepts: log in again, once
		repman.forgetPeerSession(as, sess.base)
		if sess, as, err = repman.peerLoginAs(p, url); err == nil {
			ss, _, err = selfServiceStatusOfTimeout(sess, accessStatusTimeout)
		}
	}
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.Identity = as
	entry.MCPServerConfig = map[string]any{
		"type":    "sse",
		"url":     sess.base + "/api/mcp/sse",
		"headers": map[string]string{"Authorization": "Bearer " + sess.token},
		"note":    "your session on this infrastructure, as " + as + ": a secret like a token, it expires with the login; create your cluster there with cloud18-create-cluster, then mint a durable token with cloud18-create-cluster-token",
	}
	entry.SelfService = ss
	return entry
}

// Cloud18GetCluster reads a cluster on an infrastructure: state, servers, proxies, apps.
func (repman *ReplicationManager) Cloud18GetCluster(p *repmanmcp.Principal, infra, clusterName string) (map[string]any, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return nil, errors.New("cluster_name is required")
	}
	sess, _, err := repman.peerLoginAs(p, infra)
	if err != nil {
		return nil, err
	}
	cpath := "/api/clusters/" + clusterName
	out := map[string]any{"infrastructure": sess.base, "cluster": clusterName}
	if body, err := sess.mustOK(http.MethodGet, cpath, nil); err == nil {
		var cl map[string]any
		if json.Unmarshal(body, &cl) == nil {
			for _, k := range []string{"isProvisioned", "isDown", "isMasterDown", "isFailable", "isNeedDatabasesRestart", "isNeedProxiesRestart", "topology", "uptime"} {
				if v, ok := cl[k]; ok {
					out[k] = v
				}
			}
		}
	} else {
		return nil, err
	}
	for _, part := range []string{"servers", "proxies", "apps"} {
		if body, err := sess.mustOK(http.MethodGet, cpath+"/topology/"+part, nil); err == nil {
			out[part] = json.RawMessage(body)
		}
	}
	return out, nil
}

// ClusterPrice is the local cluster's rows of the running month statement (the MCP
// get-cluster-price tool; same answer as GET /api/clusters/{clusterName}/price).
func (repman *ReplicationManager) ClusterPrice(clusterName string) (map[string]any, error) {
	if repman.resourceManager == nil {
		return nil, errors.New("resource manager not ready")
	}
	cs, ok := repman.resourceManager.ClusterStatementOf(clusterName, time.Now())
	if !ok {
		return nil, fmt.Errorf("no statement yet for %s: the first monitoring tick has not pushed its usage", clusterName)
	}
	st, _ := repman.resourceManager.Statement("", time.Now())
	return map[string]any{"month": st.Month, "elapsedPct": st.ElapsedPct, "currency": st.Currency, "prices": st.Prices, "cluster": cs}, nil
}

// Cloud18GetClusterPrice reads a cluster's month statement on an infrastructure, as this
// instance's Cloud18 identity: what the infrastructure's resource manager integrated.
func (repman *ReplicationManager) Cloud18GetClusterPrice(p *repmanmcp.Principal, infra, clusterName string) (map[string]any, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return nil, errors.New("cluster_name is required")
	}
	sess, _, err := repman.peerLoginAs(p, infra)
	if err != nil {
		return nil, err
	}
	body, err := sess.mustOK(http.MethodGet, "/api/clusters/"+clusterName+"/price", nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("price of %s on %s: %w", clusterName, sess.base, err)
	}
	out["infrastructure"] = sess.base
	return out, nil
}
