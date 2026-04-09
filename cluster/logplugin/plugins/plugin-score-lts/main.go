// plugin-score-lts evaluates:
//
//	HasLastLTS — the server version is a known active LTS release
//
// The plugin has a built-in LTS list compiled in via go:embed (lts-versions.json
// next to this file). When PluginDataDir contains a newer lts-versions.json —
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
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

//go:embed lts-versions.json
var defaultLTSData []byte

type ltsData struct {
	Updated string              `json:"updated"`
	LTS     map[string][]string `json:"lts"`
}

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	// Prefer the on-disk file (periodically refreshed) over the compiled-in default.
	raw := defaultLTSData
	source := "built-in"
	if req.PluginDataDir != "" {
		dataFile := filepath.Join(req.PluginDataDir, "lts-versions.json")
		if disk, err := os.ReadFile(dataFile); err == nil {
			raw = disk
			source = dataFile
		}
	}

	var data ltsData
	if err := json.Unmarshal(raw, &data); err != nil {
		json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
			{Tag: "HasLastLTS", Pass: false,
				Detail: fmt.Sprintf("bad lts-versions.json (%s): %v", source, err)},
		}})
		return
	}

	// Detect flavor from version_comment server variable
	versionComment := strings.ToLower(req.ServerVariables["version_comment"])
	version := strings.TrimSpace(req.ServerVariables["version"])

	var flavor string
	switch {
	case strings.Contains(versionComment, "mariadb") || strings.Contains(version, "mariadb"):
		flavor = "mariadb"
	case strings.Contains(versionComment, "percona"):
		flavor = "percona"
	default:
		flavor = "mysql"
	}

	ltsList := data.LTS[flavor]
	pass := false
	for _, lts := range ltsList {
		if strings.HasPrefix(version, lts+".") || version == lts {
			pass = true
			break
		}
	}

	detail := fmt.Sprintf("flavor=%s version=%s lts=%v (data: %s, updated %s)",
		flavor, version, ltsList, source, data.Updated)

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
		{Tag: "HasLastLTS", Pass: pass, Detail: detail},
	}})
}
