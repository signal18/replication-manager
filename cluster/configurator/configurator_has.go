// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"encoding/json"
	"strings"

	"github.com/signal18/replication-manager/config"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (configurator *Configurator) HasInstallPlugin(Plugins map[string]*dbhelper.Plugin, name string) bool {
	val, ok := Plugins[name]
	if !ok {
		return false
	}
	if val.Status == "ACTIVE" {
		return true
	}
	return false
}

func (configurator *Configurator) HasWsrep(Variables map[string]string) bool {
	return Variables["WSREP_ON"] == "ON"
}

func (configurator *Configurator) HaveDBTag(tag string) bool {
	for _, t := range configurator.DBTags {
		if t == tag {
			return true
		}
	}
	return false
}

func (configurator *Configurator) HaveProxyTag(tag string) bool {
	for _, t := range configurator.ProxyTags {
		if t == tag {
			return true
		}
	}
	return false
}

// filterMatchesTag reports whether a ruleset filter (fset_name, e.g.
// "mariadb.security.ssl") or a bare tag belongs to tag: the tag must be the filter's
// whole last segment. A plain suffix match let "ssl" select "mariadb.security.strictssl",
// "bind" select "nobind" and "statement" select "nologslavestatement" (#1975).
func filterMatchesTag(filter, tag string) bool {
	return tag != "" && (filter == tag || strings.HasSuffix(filter, "."+tag))
}

func (configurator *Configurator) IsFilterInProxyTags(filter string) bool {
	tags := configurator.GetProxyTags()
	for _, tag := range tags {
		if filterMatchesTag(filter, tag) {
			//fmt.Println("test tag: " + tag + "  against " + filter)
			return true
		}
	}
	return false
}

func (configurator *Configurator) IsFilterInDBTags(filter string) bool {
	tags := configurator.GetDBTags()
	for _, tag := range tags {
		if filterMatchesTag(filter, tag) {
			//	fmt.Println(server.ClusterGroup.Conf.ProvTags + " vs tag: " + tag + "  against " + filter)
			return true
		}

	}
	return false
}

func (configurator *Configurator) HasProxyReadLeader() bool {
	if configurator.IsFilterInProxyTags("readonmaster") {
		return true
	}
	if configurator.ClusterConfig.PRXServersReadOnMaster {
		return true
	}
	return false
}

func (configurator *Configurator) HasProxyReadLeaderNoSlave() bool {
	if configurator.IsFilterInProxyTags("readonmasternoslave") {
		return true
	}
	if configurator.ClusterConfig.PRXServersReadOnMasterNoSlave {
		return true
	}
	return false
}

// formerlyRenderedVariables are variables a tag ruleset of an earlier release rendered and
// the module no longer does. A server deployed by that release still carries them in its
// config, so they stay in TagManagedVariables: dropping the tag must not leave them behind.
//   - ssl: the bare "ssl" line of the ssl tag, removed because MySQL/Percona 8.4 refuse the
//     option at start (#1993); ssl-ca/ssl-cert/ssl-key alone enable TLS.
var formerlyRenderedVariables = []string{"ssl"}

// TagManagedVariables returns every variable a database cnf ruleset of the module
// renders, whatever the cluster's tags: those variables are replication-manager's own.
// One of them deployed but absent from the generated config is a pending tag change
// (a tag dropped, its file still deployed until the restart), never operator drift, so
// the delta must not preserve it (#1976). Names are normalized like the variable maps.
// The map is a set: true means "managed by a tag ruleset", never a value of the variable
// (nothing is rendered or enabled from it).
func (configurator *Configurator) TagManagedVariables() map[string]bool {
	type fileVar struct {
		Fmt string `json:"fmt"`
	}
	managed := make(map[string]bool)
	for _, name := range formerlyRenderedVariables {
		managed[name] = true
	}
	for _, rule := range configurator.DBModule.Rulesets {
		if !strings.Contains(rule.Name, "mariadb.svc.mrm.db.cnf") {
			continue
		}
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			var fv fileVar
			if err := json.Unmarshal([]byte(variable.Value), &fv); err != nil || fv.Fmt == "" {
				continue
			}
			for _, line := range strings.Split(fv.Fmt, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "!") {
					continue
				}
				key, _, _ := strings.Cut(line, "=")
				if name := strings.ToLower(config.NormalizeConfigVarName(key)); name != "" {
					managed[name] = true
				}
			}
		}
	}
	return managed
}
