// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
	"github.com/signal18/replication-manager/utils/releases"
)

// cloud18OptionDimensions are the dimensions list-cloud18-cluster-options answers (#1963).
var cloud18OptionDimensions = []string{"db_flavor", "db_image", "topology", "proxy", "apps", "dbu", "apu"}

// cloud18Flavors: the database flavors and whether a self-service creation builds them.
// Every flavor is built: MariaDB, MySQL and Percona on the database path, PostgreSQL
// members from the infrastructure's postgres app templates (engine servers, monitored
// like any server).
var cloud18Flavors = []struct {
	name, repo string
	creatable  bool
	note       string
}{
	{"mariadb", "mariadb", true, ""},
	{"mysql", "mysql", true, ""},
	{"percona", "percona/percona-server", true, ""},
	{"postgres", "postgres", true, "members run the image of the infrastructure's postgres/postgres templates"},
}

// cloud18Topologies: per flavor, the topologies replication-manager runs, and whether a
// self-service creation builds them: cloud18-create-cluster adds database nodes and lets
// the default replication bootstrap run, which is master/replica only.
var cloud18Topologies = map[string][]map[string]any{
	"mariadb": {
		{"topology": config.TopoMasterSlave, "description": "a master and replicas (db_count 2 to 5)", "minNodes": 2, "creatable": true},
		{"topology": config.TopoMultiMasterWsrep, "description": "Galera multi-master", "minNodes": 3, "creatable": false, "note": "needs a topology parameter and the Galera bootstrap in cloud18-create-cluster"},
	},
	"mysql": {
		{"topology": config.TopoMasterSlave, "description": "a master and replicas (db_count 2 to 5)", "minNodes": 2, "creatable": true},
		{"topology": config.TopoMultiMasterGrouprep, "description": "group replication", "minNodes": 3, "creatable": false, "note": "needs a topology parameter in cloud18-create-cluster"},
	},
	"percona": {
		{"topology": config.TopoMasterSlave, "description": "a master and replicas (db_count 2 to 5)", "minNodes": 2, "creatable": true},
		{"topology": config.TopoMultiMasterGrouprep, "description": "group replication", "minNodes": 3, "creatable": false, "note": "needs a topology parameter in cloud18-create-cluster"},
	},
	"postgres": {
		{"topology": config.TopoMasterSlavePgStream, "description": "WAL streaming replication, a primary and standbys (default)", "minNodes": 2, "creatable": true},
		{"topology": config.TopoMasterSlavePgLog, "description": "logical replication", "minNodes": 2, "creatable": true},
		{"topology": config.TopoActivePassive, "description": "one server (db_count 1)", "minNodes": 1, "creatable": true},
	},
}

// Cloud18ClusterOptions lists the possible values of one dimension of a cluster request, or
// of every dimension when dimension is empty, optionally for one flavor. The release lines
// come from the release table (the one the rolling upgrade resolves with); the apps, DBU and
// APU from the infrastructure's own self-service status, read as the caller.
func (repman *ReplicationManager) Cloud18ClusterOptions(p *repmanmcp.Principal, dimension, flavor, infra string) (map[string]any, error) {
	dimension = strings.ToLower(strings.TrimSpace(dimension))
	flavor = strings.ToLower(strings.TrimSpace(flavor))
	if dimension != "" && !containsString(cloud18OptionDimensions, dimension) {
		return nil, fmt.Errorf("dimension must be one of %s (empty = all)", strings.Join(cloud18OptionDimensions, ", "))
	}
	flavors := []string{}
	for _, f := range cloud18Flavors {
		if flavor == "" || f.name == flavor {
			flavors = append(flavors, f.name)
		}
	}
	if len(flavors) == 0 {
		return nil, fmt.Errorf("db_flavor must be mariadb, mysql, percona or postgres")
	}
	want := func(d string) bool { return dimension == "" || dimension == d }
	out := map[string]any{}
	if dimension != "" {
		out["dimension"] = dimension
	}
	if flavor != "" {
		out["db_flavor_filter"] = flavor
	}
	if want("db_flavor") {
		list := []map[string]any{}
		for _, f := range cloud18Flavors {
			if containsString(flavors, f.name) {
				e := map[string]any{"db_flavor": f.name, "image_repository": f.repo, "creatable": f.creatable}
				if f.note != "" {
					e["note"] = f.note
				}
				list = append(list, e)
			}
		}
		out["db_flavor"] = list
	}
	if want("db_image") {
		table, source, _ := releases.Load(repman.Conf.ShareDir + "/plugins/data")
		images := map[string]any{}
		for _, f := range cloud18Flavors {
			if !containsString(flavors, f.name) {
				continue
			}
			lines := []map[string]any{}
			for _, s := range table.Lines[f.name] {
				l, err := releases.ParseLine(s)
				if err != nil {
					continue
				}
				lines = append(lines, map[string]any{"db_image": f.repo + ":" + s, "lts": table.IsLTS(f.name, l)})
			}
			images[f.name] = map[string]any{
				"floating":  []string{f.repo + ":lts", f.repo + ":latest"},
				"lines":     lines,
				"creatable": f.creatable,
				"note":      imageNote(f.repo, table.Lines[f.name]),
			}
		}
		out["db_image"] = images
		out["db_image_source"] = source
	}
	if want("topology") {
		topo := map[string]any{}
		for _, f := range flavors {
			topo[f] = cloud18Topologies[f]
		}
		out["topology"] = topo
	}
	if want("proxy") {
		out["proxy"] = []map[string]any{
			{"proxy": "haproxy", "default": true, "proxy_count": "1 to 3, default 1"},
			{"proxy": "proxysql", "proxy_count": "1 to 3, default 1"},
			{"proxy": "none"},
		}
	}
	if want("apps") || want("dbu") || want("apu") {
		sess, _, err := repman.peerLoginAs(p, infra)
		if err != nil {
			return nil, err
		}
		body, err := sess.mustOK(http.MethodGet, "/api/cloud18/self-service", nil)
		if err != nil {
			return nil, fmt.Errorf("the infrastructure does not expose self-service (older release?): %w", err)
		}
		var ss map[string]any
		_ = json.Unmarshal(body, &ss)
		out["infrastructure"] = sess.base
		num := func(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }
		pool, _ := ss["pool"].(map[string]any)
		if want("apps") {
			out["apps"] = map[string]any{"templates": ss["appTemplates"], "default": []string{"phpmyadmin"}, "none": "apps=none deploys no app"}
		}
		if want("dbu") {
			e := map[string]any{"unit": "DBU per database node", "min": 1, "max": 64, "default": num(ss, "defaultDbu")}
			if pool != nil {
				e["freeInPool"] = math.Round(num(pool, "freeDbu")*100) / 100
			}
			out["dbu"] = e
		}
		if want("apu") {
			e := map[string]any{"unit": "APU of the proxies and apps together (each app at least 1)", "min": 1, "max": 256, "default": num(ss, "defaultApu")}
			if pool != nil {
				e["freeInPool"] = math.Round(num(pool, "freeApu")*100) / 100
			}
			out["apu"] = e
		}
	}
	return out, nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// imageNote explains how an image of a flavor is taken, with an example of that flavor.
func imageNote(repo string, lines []string) string {
	if len(lines) == 0 {
		return "no release line is published for " + repo + ": " + repo + ":lts, :latest or an exact tag; PostgreSQL members run the image of the infrastructure's postgres/postgres templates"
	}
	return "a floating tag or a line (e.g. " + repo + ":" + lines[len(lines)-1] + ") resolves to its exact release at provisioning and is pinned there until a rolling upgrade; an exact release (" + repo + ":<line>.<patch>) is taken as is"
}
