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
	base  string
	token string
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
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: peerCallTimeout}
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
	apps := []string{}
	for _, a := range spec.Apps {
		a = strings.TrimSpace(a)
		if a != "" {
			apps = append(apps, a)
		}
	}
	spec.Apps = apps
	return spec, nil
}

// plannedHosts lists the services the tool will add, in order.
func plannedHosts(spec Cloud18ClusterSpec) []map[string]string {
	out := []map[string]string{}
	suffix := "." + spec.ClusterName + ".svc.cloud18"
	for i := 1; i <= spec.DBCount; i++ {
		out = append(out, map[string]string{"type": "database", "host": fmt.Sprintf("db%d%s", i, suffix), "port": "3306", "image": spec.DBImage})
	}
	if spec.Proxy != "none" {
		out = append(out, map[string]string{"type": spec.Proxy, "host": spec.Proxy + "1" + suffix, "port": "3306"})
	}
	for i, app := range spec.Apps {
		out = append(out, map[string]string{"type": "app", "host": fmt.Sprintf("%s%d%s", app, i+1, suffix), "port": "80", "template": app})
	}
	return out
}

// Cloud18CreateCluster plans (confirm=false) or creates (confirm=true) a cluster
// on an infrastructure, as this instance's Cloud18 identity.
func (repman *ReplicationManager) Cloud18CreateCluster(spec Cloud18ClusterSpec, confirm bool) (map[string]any, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	sess, err := repman.peerLogin(spec.Infrastructure)
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
	plan := map[string]any{
		"infrastructure": sess.base,
		"identity":       repman.Conf.Cloud18GitUser,
		"cluster":        spec.ClusterName,
		"services":       plannedHosts(spec),
		"selfService":    ss,
		"unitPlan":       "the infrastructure's default DBU / APU / BKU (no service plan)",
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
	// 2. Database image.
	if _, err := sess.mustOK(http.MethodGet, cpath+"/settings/actions/set/prov-db-docker-img/"+url.PathEscape(spec.DBImage), nil); err != nil {
		return fail("set database image", err)
	}
	steps = append(steps, "database image "+spec.DBImage)
	// 3. Servers, proxy, apps.
	for _, h := range plannedHosts(spec) {
		var err error
		switch h["type"] {
		case "database":
			_, err = sess.mustOK(http.MethodGet, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"], nil)
		case "app":
			_, err = sess.mustOK(http.MethodPost, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"]+"/app/"+url.PathEscape(h["template"]), map[string]any{})
		default:
			_, err = sess.mustOK(http.MethodGet, cpath+"/actions/addserver/"+h["host"]+"/"+h["port"]+"/"+h["type"], nil)
		}
		if err != nil {
			return fail("add "+h["type"]+" "+h["host"], err)
		}
		steps = append(steps, "added "+h["type"]+" "+h["host"])
	}
	// 4. Provision everything.
	if _, err := sess.mustOK(http.MethodGet, cpath+"/services/actions/provision", nil); err != nil {
		return fail("provision", err)
	}
	steps = append(steps, "provisioning started")
	plan["steps"] = steps
	plan["next"] = "follow with get-cloud18-cluster; provisioning takes a few minutes"
	if topo, err := sess.mustOK(http.MethodGet, cpath+"/topology/servers", nil); err == nil {
		plan["servers"] = json.RawMessage(topo)
	}
	return plan, nil
}

// Cloud18GetCluster reads a cluster on an infrastructure: state, servers, proxies, apps.
func (repman *ReplicationManager) Cloud18GetCluster(infra, clusterName string) (map[string]any, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return nil, errors.New("cluster_name is required")
	}
	sess, err := repman.peerLogin(infra)
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
	for _, part := range []string{"servers", "proxies"} {
		if body, err := sess.mustOK(http.MethodGet, cpath+"/topology/"+part, nil); err == nil {
			out[part] = json.RawMessage(body)
		}
	}
	if body, err := sess.mustOK(http.MethodGet, cpath+"/apps", nil); err == nil {
		out["apps"] = json.RawMessage(body)
	}
	return out, nil
}
