// plugin-score-proxy evaluates:
//
//	HasProxies — at least one proxy (ProxySQL, HAProxy, MaxScale…) is configured
//	             for the cluster (from cluster context)
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	pass := req.ClusterContext.HasProxies
	detail := "no proxies configured"
	if pass {
		detail = "cluster has at least one proxy configured"
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
		{Tag: "HasProxies", Pass: pass, Detail: fmt.Sprintf("%s (server: %s)", detail, req.ServerURL)},
	}})
}
