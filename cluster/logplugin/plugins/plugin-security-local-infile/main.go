// plugin-security-local-infile raises SEC0102 when the MySQL/MariaDB server
// variable local_infile is ON.
//
// With local_infile enabled a malicious or compromised server can instruct a
// client to read arbitrary local files and send their contents to the server
// ("LOAD DATA LOCAL INFILE" attack).  It should be OFF in all production
// deployments unless a specific, controlled use-case requires it.
//
// Reference: CVE-2016-3440, CIS MySQL Benchmark control 4.5
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

	val, ok := req.ServerVariables["local_infile"]
	if !ok {
		// Variable not present — cannot determine, emit nothing.
		json.NewEncoder(os.Stdout).Encode(wire.Response{})
		return
	}

	if strings.ToUpper(strings.TrimSpace(val)) == "ON" {
		json.NewEncoder(os.Stdout).Encode(wire.Response{
			Findings: []wire.Finding{
				{
					ErrKey:   "SEC0102",
					Severity: "SECURITY",
					Description: fmt.Sprintf(
						"Server %s: local_infile=ON — LOAD DATA LOCAL INFILE is enabled;"+
							" a rogue server could read arbitrary client-side files."+
							" Set local_infile=OFF unless explicitly required.",
						req.ServerURL),
				},
			},
		})
		return
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{})
}
