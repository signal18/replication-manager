package cluster

import "testing"

func newTestMaxscaleProxyProxy(cluster *Cluster) *MaxscaleProxy {
	prx := &MaxscaleProxy{}
	prx.ClusterGroup = cluster
	prx.Host = "maxscale1.example.com"
	prx.Port = "6603"
	prx.User = "admin"
	prx.Pass = "mariadb"
	return prx
}

func TestNewMaxscaleClient_UsesRestPortAndProtocolByDefault(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = true
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)

	m := prx.newMaxscaleClient()
	if !m.UseRest {
		t.Fatalf("expected UseRest true by default")
	}
	if m.Port != "8989" {
		t.Fatalf("expected port 8989, got %s", m.Port)
	}
}

func TestNewMaxscaleClient_UsesMaxAdminPortWhenRestDisabled(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = false
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)

	m := prx.newMaxscaleClient()
	if m.UseRest {
		t.Fatalf("expected UseRest false when maxscale-rest-api is disabled")
	}
	if m.Port != prx.Port {
		t.Fatalf("expected port %s (proxy.Port), got %s", prx.Port, m.Port)
	}
}

// TunnelPort is only ever wired to the MaxAdmin listener (nothing in the
// codebase re-points it at MxsRestPort), so a tunneled connection must
// always speak MaxAdmin regardless of maxscale-rest-api -- otherwise an
// existing tunneled setup would silently break under the maxscale-rest-api
// default.
func TestNewMaxscaleClient_TunnelAlwaysUsesMaxAdminRegardlessOfRestApi(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = true
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)
	prx.Tunnel = true
	prx.TunnelPort = 16603

	m := prx.newMaxscaleClient()
	if m.UseRest {
		t.Fatalf("expected UseRest false on the tunnel path even with maxscale-rest-api=true")
	}
	if m.Host != "localhost" {
		t.Fatalf("expected tunnel host localhost, got %s", m.Host)
	}
	if m.Port != "16603" {
		t.Fatalf("expected tunnel port 16603, got %s", m.Port)
	}
}
