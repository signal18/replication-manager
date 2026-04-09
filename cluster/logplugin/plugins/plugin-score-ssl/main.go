// plugin-score-ssl evaluates two SSL/TLS compliance checks:
//
//	HasSSL     — server has SSL/TLS configured (have_ssl=YES or ssl_ca/cert/key set)
//	HasZeroSSL — require_secure_transport=ON (all connections must use TLS)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	v := req.ServerVariables
	get := func(key string) string { return strings.TrimSpace(v[key]) }
	upper := func(s string) string { return strings.ToUpper(s) }

	// HasSSL: MySQL 8.4+ uses ssl_ca/cert/key; older versions use have_ssl
	hasSSL := upper(get("have_ssl")) == "YES" ||
		(get("ssl_ca") != "" && get("ssl_cert") != "" && get("ssl_key") != "")

	// HasZeroSSL: require_secure_transport enforces TLS for every connection
	hasZeroSSL := upper(get("require_secure_transport")) == "ON"

	checks := []wire.ScoreCheck{
		{Tag: "HasSSL", Pass: hasSSL,
			Detail: fmt.Sprintf("have_ssl=%s ssl_ca=%q", get("have_ssl"), get("ssl_ca"))},
		{Tag: "HasZeroSSL", Pass: hasZeroSSL,
			Detail: fmt.Sprintf("require_secure_transport=%s", get("require_secure_transport"))},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
