package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// #2042: the API refuses before starting the rotation, with the reason
func TestStartPasswordRotationRefusedWithoutVersioning(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{
		SecretKey:       []byte("0123456789abcdef"),
		ConfRewrite:     true,
		ImmuableFlagMap: map[string]interface{}{},
	}}
	cl.Servers = append(cl.Servers, &cluster.ServerMonitor{State: "Master"})
	err := startPasswordRotation(cl)
	if err == nil || !strings.Contains(err.Error(), "monitoring-secret-versioning is off") {
		t.Fatalf("got %v, want the versioning refusal", err)
	}
}
