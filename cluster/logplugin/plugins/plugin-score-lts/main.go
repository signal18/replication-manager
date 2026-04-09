// plugin-score-lts evaluates:
//
//	HasLastLTS — the server version is a known active LTS release
//
// The plugin reads lts-versions.json from PluginDataDir. This file ships
// with the repman package and is periodically refreshed from the Signal18
// back-office so the LTS list stays current without a repman upgrade.
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
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

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

	dataFile := filepath.Join(req.PluginDataDir, "lts-versions.json")
	raw, err := os.ReadFile(dataFile)
	if err != nil {
		// Can't read data file — emit unknown rather than false negative
		json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
			{Tag: "HasLastLTS", Pass: false,
				Detail: fmt.Sprintf("cannot read %s: %v", dataFile, err)},
		}})
		return
	}

	var data ltsData
	if err := json.Unmarshal(raw, &data); err != nil {
		json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
			{Tag: "HasLastLTS", Pass: false,
				Detail: fmt.Sprintf("bad lts-versions.json: %v", err)},
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

	detail := fmt.Sprintf("flavor=%s version=%s lts=%v (data updated %s)",
		flavor, version, ltsList, data.Updated)

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: []wire.ScoreCheck{
		{Tag: "HasLastLTS", Pass: pass, Detail: detail},
	}})
}
