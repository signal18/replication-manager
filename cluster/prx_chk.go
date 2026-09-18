// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

// CheckNeedConfigFetch mirrors ServerMonitor.CheckNeedConfigFetch
// (cluster/srv_chk.go) for proxies: prov-proxy-start-fetch-config is read
// live from cluster config and toggled on/off via the same
// Set/DelNoConfigFetchCookie cookie pair the orchestrator-side bootstrap
// gate consults on every start (e.g. k8sProxyBootstrapCommand's
// need-config-fetch check, prov_k8s_prx.go), not baked into a Deployment
// spec at provision time.
func (proxy *Proxy) CheckNeedConfigFetch() {
	if proxy.IsIgnored() {
		return
	}
	cluster := proxy.ClusterGroup
	if cluster.Conf.ProvProxyStartFetchConfig && proxy.HasNoConfigFetchCookie() {
		proxy.DelNoConfigFetchCookie()
	} else if !cluster.Conf.ProvProxyStartFetchConfig && !proxy.HasNoConfigFetchCookie() {
		proxy.SetNoConfigFetchCookie()
	}
}
