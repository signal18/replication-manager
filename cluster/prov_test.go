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

// TestStartReappliesProxyConfig pins the fix for the NeedProxyReprov badge
// being cleared (or left set) incorrectly depending on which orchestrator's
// "start" action actually reapplies the proxy's config:
//   - OnPrem/SlapOS starts never reapply config on their own (plain
//     "systemctl start ..." / a no-op) -- a successful start there must
//     NOT clear reprov, regardless of prov-proxy-start-fetch-config.
//   - Localhost always regenerates+applies unconditionally on start.
//   - OpenSVC/Kubernetes only reapply via their own container-side fetch,
//     gated on prov-proxy-start-fetch-config (HasNoConfigFetchCookie).
func TestStartReappliesProxyConfig(t *testing.T) {
	newProxy := func() *Proxy {
		return &Proxy{Datadir: t.TempDir()}
	}

	t.Run("OnPremise never reapplies", func(t *testing.T) {
		proxy := newProxy()
		// Even with fetch-on-start effectively enabled (cookie absent),
		// OnPremise's start is just "systemctl start ..." -- no fetch step
		// exists to gate at all.
		if startReappliesProxyConfig(proxy, config.ConstOrchestratorOnPremise) {
			t.Errorf("startReappliesProxyConfig(OnPremise) = true, want false")
		}
	})

	t.Run("SlapOS never reapplies", func(t *testing.T) {
		proxy := newProxy()
		if startReappliesProxyConfig(proxy, config.ConstOrchestratorSlapOS) {
			t.Errorf("startReappliesProxyConfig(SlapOS) = true, want false")
		}
	})

	t.Run("Localhost always reapplies", func(t *testing.T) {
		proxy := newProxy()
		// Fetch-on-start effectively disabled (cookie present) -- Localhost
		// doesn't consult that cookie at all, it always re-renders.
		proxy.SetNoConfigFetchCookie()
		if !startReappliesProxyConfig(proxy, config.ConstOrchestratorLocalhost) {
			t.Errorf("startReappliesProxyConfig(Localhost) = false, want true")
		}
	})

	t.Run("OpenSVC reapplies only when fetch-on-start is enabled", func(t *testing.T) {
		proxy := newProxy()
		proxy.SetNoConfigFetchCookie() // fetch-on-start effectively off
		if startReappliesProxyConfig(proxy, config.ConstOrchestratorOpenSVC) {
			t.Errorf("startReappliesProxyConfig(OpenSVC, fetch off) = true, want false")
		}

		proxy.DelNoConfigFetchCookie() // fetch-on-start effectively on
		if !startReappliesProxyConfig(proxy, config.ConstOrchestratorOpenSVC) {
			t.Errorf("startReappliesProxyConfig(OpenSVC, fetch on) = false, want true")
		}
	})

	t.Run("Kubernetes mirrors OpenSVC's fetch-on-start gating", func(t *testing.T) {
		proxy := newProxy()
		proxy.SetNoConfigFetchCookie()
		if startReappliesProxyConfig(proxy, config.ConstOrchestratorKubernetes) {
			t.Errorf("startReappliesProxyConfig(Kubernetes, fetch off) = true, want false")
		}
		proxy.DelNoConfigFetchCookie()
		if !startReappliesProxyConfig(proxy, config.ConstOrchestratorKubernetes) {
			t.Errorf("startReappliesProxyConfig(Kubernetes, fetch on) = false, want true")
		}
	})
}
