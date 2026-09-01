// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestProxyServiceOrchestratorRoutesHaproxyStandbyToLocalhost guards the
// dispatch override added alongside the standby/externalcheck mode split:
// haproxy-mode=standby must always be provisioned/started/stopped as a
// repman-local process (Init(), cluster/prx_haproxy.go, has no remote
// equivalent -- it renders+reloads via a local PID) regardless of which
// orchestrator the cluster's databases are actually provisioned under. Every
// other combination (non-standby mode, or a non-HAProxy proxy type) must
// keep following the cluster's own orchestrator.
func TestProxyServiceOrchestratorRoutesHaproxyStandbyToLocalhost(t *testing.T) {
	tests := []struct {
		name        string
		proxyType   string
		haproxyMode string
		provOrch    string
		want        string
	}{
		{
			name:        "haproxy standby on a K8s database cluster routes to Localhost",
			proxyType:   config.ConstProxyHaproxy,
			haproxyMode: "standby",
			provOrch:    config.ConstOrchestratorKubernetes,
			want:        config.ConstOrchestratorLocalhost,
		},
		{
			name:        "haproxy standby on an OpenSVC database cluster routes to Localhost",
			proxyType:   config.ConstProxyHaproxy,
			haproxyMode: "standby",
			provOrch:    config.ConstOrchestratorOpenSVC,
			want:        config.ConstOrchestratorLocalhost,
		},
		{
			name:        "haproxy runtimeapi stays on the cluster's own orchestrator",
			proxyType:   config.ConstProxyHaproxy,
			haproxyMode: "runtimeapi",
			provOrch:    config.ConstOrchestratorKubernetes,
			want:        config.ConstOrchestratorKubernetes,
		},
		{
			name:        "haproxy externalcheck stays on the cluster's own orchestrator",
			proxyType:   config.ConstProxyHaproxy,
			haproxyMode: "externalcheck",
			provOrch:    config.ConstOrchestratorOpenSVC,
			want:        config.ConstOrchestratorOpenSVC,
		},
		{
			name:        "a non-haproxy proxy is unaffected by haproxy-mode=standby",
			proxyType:   config.ConstProxySqlproxy,
			haproxyMode: "standby",
			provOrch:    config.ConstOrchestratorKubernetes,
			want:        config.ConstOrchestratorKubernetes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newTestCluster("provtest")
			cluster.Conf.HaproxyMode = tt.haproxyMode
			cluster.Conf.ProvOrchestrator = tt.provOrch

			prx := &fakeProxy{proxyType: tt.proxyType, cluster: cluster}

			if got := cluster.proxyServiceOrchestrator(prx); got != tt.want {
				t.Fatalf("proxyServiceOrchestrator() = %q, want %q", got, tt.want)
			}
		})
	}
}
