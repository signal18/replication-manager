// plugin-score-lts evaluates:
//
//	HasLastLTS — the server version is a known active LTS release
//
// The LTS list is the shared release table utils/releases (lts-versions.json
// built in), also used by the MCP rolling-upgrade targets. When PluginDataDir contains a newer lts-versions.json —
// refreshed periodically from the Signal18 back-office — that file takes
// priority so the LTS list stays current without a repman upgrade.
//
// lts-versions.json format:
//
//	{
//	  "updated": "2026-04-09",
//	  "lts": {
//	    "mariadb": ["10.6", "10.11", "11.4"],
//	    "mysql":   ["8.0",  "8.4"],
//	    "percona": ["8.0",  "8.4"]
//	  }
//	}
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
	"github.com/signal18/replication-manager/utils/releases"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// Prefer the on-disk file (periodically refreshed) over the compiled-in default.
	data, source, err := releases.Load(req.PluginDataDir)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
			{Tag: "HasLastLTS", Pass: false, Detail: err.Error()},
		}})
		return
	}

	sv := req.ServerVersion
	flavor := strings.ToLower(sv.Flavor) // "mariadb", "mysql", "percona"
	cleanVersion := fmt.Sprintf("%d.%d.%d", sv.Major, sv.Minor, sv.Release)

	ltsList := data.LTS[flavor]
	pass := false
	for _, lts := range ltsList {
		if strings.HasPrefix(cleanVersion, lts+".") || cleanVersion == lts {
			pass = true
			break
		}
	}

	detail := fmt.Sprintf("flavor=%s version=%s lts=%v (data: %s, updated %s)",
		flavor, cleanVersion, ltsList, source, data.Updated)

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
		{Tag: "HasLastLTS", Pass: pass, Detail: detail},
	}})
}
