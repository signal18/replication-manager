// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml"
)

// The helpers every list of plugin entries of a cluster shares: its archives (#2077), its S3
// providers (#2155) and its credential stores (#2227). Each entry is a TOML table of an array,
// [[<section>.<table>]], with its name, its plugin and its settings.

// writeEntryToml renders one entry as a [[<section>.<table>]] table: the parts in order, the
// keys of each part sorted, an empty part left out.
func writeEntryToml(b *strings.Builder, section, table string, parts ...map[string]interface{}) error {
	b.WriteString("\n[[" + section + "." + table + "]]\n")
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		keys := make([]string, 0, len(part))
		for k := range part {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t, err := toml.TreeFromMap(map[string]interface{}{k: part[k]})
			if err != nil {
				return err
			}
			line, err := t.ToTomlString()
			if err != nil {
				return err
			}
			b.WriteString(line)
		}
	}
	return nil
}

// cloneSettings is a copy of an entry's settings: a Config is copied by value, its entries
// must never share their maps.
func cloneSettings(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// splitSecretSettings splits an entry's settings for the API and the GUI: the settings, and
// the sorted names of the secrets it holds. A secret is never sent, not even encrypted.
func splitSecretSettings(in map[string]interface{}, isSecret func(string) bool) (map[string]interface{}, []string) {
	settings := make(map[string]interface{}, len(in))
	secrets := []string{}
	for k, v := range in {
		if isSecret(k) {
			secrets = append(secrets, k)
			continue
		}
		settings[k] = v
	}
	sort.Strings(secrets)
	return settings, secrets
}

var entryNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// validateEntryName: lowercase letters, digits, - and _, starting with a letter or a digit,
// 64 characters at most. The name goes into the config file and the event keys
// ("<table>.<name>.<setting>").
func validateEntryName(kind, name string) error {
	if !entryNameRe.MatchString(name) {
		return fmt.Errorf("%s name %q: lowercase letters, digits, - and _ only, 64 characters at most", kind, name)
	}
	return nil
}

// ClusterSpace is the cluster's space in a store shared by instances (a bucket, a vault): the
// cluster name, under the instance's URI on Cloud18 (<cloud18-domain>/<cloud18-sub-domain>-
// <cloud18-sub-domain-zone>, as its secrets and billing name it), so instances sharing the
// store never mix.
func (conf *Config) ClusterSpace(cluster string) string {
	if conf.Cloud18 && conf.Cloud18Domain != "" && conf.Cloud18SubDomain != "" {
		return conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + "/" + cluster
	}
	return cluster
}
