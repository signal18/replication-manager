// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestLocalhostCheckScriptContentEscapesClusterName guards against
// cluster.Name being interpolated verbatim into localhostCheckScriptTemplate:
// it lands inside a double-quoted shell string (the wget URL, and the
// printf-built HTTP request line in the /dev/tcp fallback), so a name
// containing '"', '`', '$', or '\' could otherwise break out of that
// quoting and inject shell syntax into a script HAProxy's external-check
// then executes. url.PathEscape must neutralize that while still producing
// a working URL path.
func TestLocalhostCheckScriptContentEscapesClusterName(t *testing.T) {
	cluster := &Cluster{
		Name: `injected"; touch /tmp/pwned; echo "`,
		Conf: &config.Config{
			MonitorAddress: "127.0.0.1",
			HttpPort:       "10001",
		},
	}

	content := cluster.localhostCheckScriptContent("master-status")

	// The script's own fixed shell syntax legitimately contains '"' and '$'
	// (e.g. HOST="$1", printf "GET %s ..."), so check that the cluster name
	// specifically was neutralized, not that those characters are absent
	// from the whole script.
	if strings.Contains(content, cluster.Name) {
		t.Fatalf("rendered script contains cluster.Name verbatim, unescaped:\n%s", content)
	}
	for _, dangerous := range []string{`"`, "`", "$("} {
		if strings.Contains(content, "injected"+dangerous) {
			t.Fatalf("rendered script contains cluster.Name with unescaped %q immediately after it:\n%s", dangerous, content)
		}
	}
	if !strings.Contains(content, "injected%22%3B") {
		t.Fatalf("rendered script does not contain the percent-encoded cluster name:\n%s", content)
	}
}

// TestLocalhostCheckScriptContentPreservesNormalClusterName guards against
// url.PathEscape over-escaping a typical cluster name (letters, digits,
// hyphens, underscores) into something that no longer matches the
// "/clusters/{name}/..." route it's meant to address.
func TestLocalhostCheckScriptContentPreservesNormalClusterName(t *testing.T) {
	cluster := &Cluster{
		Name: "prod-cluster_01",
		Conf: &config.Config{
			MonitorAddress: "127.0.0.1",
			HttpPort:       "10001",
		},
	}

	content := cluster.localhostCheckScriptContent("slave-status")

	if !strings.Contains(content, "/clusters/prod-cluster_01/servers/$3/$4/slave-status") {
		t.Fatalf("rendered script does not contain the expected unescaped route for a normal cluster name:\n%s", content)
	}
}
