// plugin-score-audit evaluates:
//
//	HasAuditPlugins — a recognized audit plugin is active on the server
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

// Known audit plugin variable names (value "ON" or non-empty means active)
var auditVars = []string{
	"audit_log",           // MySQL Enterprise Audit / Percona Audit Log
	"server_audit",        // MariaDB Audit Plugin (via server_audit_logging)
	"server_audit_logging",
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	v := req.ServerVariables
	hasAudit := false
	var detail string
	for _, key := range auditVars {
		val := strings.TrimSpace(v[key])
		if strings.ToUpper(val) == "ON" || (val != "" && strings.ToUpper(val) != "OFF") {
			hasAudit = true
			detail = fmt.Sprintf("%s=%s", key, val)
			break
		}
	}
	if !hasAudit {
		detail = "no audit plugin variable found active"
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
		{Tag: "HasAuditPlugins", Pass: hasAudit, Detail: detail},
	}})
}
