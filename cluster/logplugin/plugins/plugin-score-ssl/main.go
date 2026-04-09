// plugin-score-ssl evaluates two SSL/TLS compliance checks:
//
//	HasSSL     — server has SSL/TLS configured
//	HasZeroSSL — require_secure_transport=ON (all connections must use TLS)
//
// HasSSL detection strategy (first match wins):
//
//  1. have_ssl=YES           — traditional MySQL/MariaDB flag
//  2. have_openssl=YES       — MariaDB alias (same underlying flag)
//  3. tls_version != ""      — MariaDB 10.4+ / 11.x zero-config SSL sets this
//     even when cert paths are auto-generated and not exposed in ssl_ca/cert/key
//  4. ssl_cert != ""         — explicit cert path set (covers manual config
//     where ssl_ca may be omitted)
//  5. ssl_ca + ssl_cert + ssl_key all non-empty — full explicit cert triplet
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

	// HasSSL: accept any indicator that SSL/TLS is active on this server.
	// MariaDB 11.x zero-config SSL auto-generates certs; ssl_ca/cert/key may be
	// empty in SHOW VARIABLES even though TLS is fully operational.
	hasSSL := upper(get("have_ssl")) == "YES" ||
		upper(get("have_openssl")) == "YES" ||
		get("tls_version") != "" ||
		get("ssl_cert") != "" ||
		(get("ssl_ca") != "" && get("ssl_cert") != "" && get("ssl_key") != "")

	// HasZeroSSL: require_secure_transport enforces TLS for every connection.
	hasZeroSSL := upper(get("require_secure_transport")) == "ON"

	detail := fmt.Sprintf("have_ssl=%s have_openssl=%s tls_version=%q ssl_cert=%q",
		get("have_ssl"), get("have_openssl"), get("tls_version"), get("ssl_cert"))

	checks := []wire.ScoreCheck{
		{Tag: "HasSSL", Pass: hasSSL, Detail: detail},
		{Tag: "HasZeroSSL", Pass: hasZeroSSL,
			Detail: fmt.Sprintf("require_secure_transport=%s", get("require_secure_transport"))},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
