// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/version"
)

// CDC_CORE.md: a destination's image follows the parent's release, as the monitor parsed it,
// when its manifest says "{version}": the exact X.Y.Z, never a suffix.
func TestChildDestinationImage(t *testing.T) {
	m := wire.PluginManifest{Name: "duckdb", Image: "ghcr.io/mariadb/duckdb:{version}", Flavor: "MariaDB"}
	for in, want := range map[string]string{
		"11.8.9-MariaDB-ubu2404-log": "ghcr.io/mariadb/duckdb:11.8.9",
		"11.4.14-MariaDB":            "ghcr.io/mariadb/duckdb:11.4.14",
	} {
		v, tokens := version.NewVersionFromString("MariaDB", in)
		if v == nil || tokens != 3 {
			t.Fatalf("%s: not parsed as X.Y.Z (%v, %d tokens)", in, v, tokens)
		}
		if img, err := destinationImageFor(m, "shop", v); err != nil || img != want {
			t.Fatalf("%s: %s %v, want %s", in, img, err, want)
		}
	}
	// a MariaDB engine: a MySQL parent refused at the plan, not at the pull
	mysql, _ := version.NewVersionFromString("MySQL", "8.4.3")
	if _, err := destinationImageFor(m, "shop", mysql); err == nil || !strings.Contains(err.Error(), "exists for MariaDB") {
		t.Fatalf("a MySQL parent of a MariaDB destination must be refused: %v", err)
	}
	m.Flavor = ""
	if img, err := destinationImageFor(m, "shop", mysql); err != nil || img != "ghcr.io/mariadb/duckdb:8.4.3" {
		t.Fatalf("no flavor, any parent: %s %v", img, err)
	}
	m.Flavor = "MariaDB"
	if img, err := childDestinationImage(wire.PluginManifest{Image: "example.org/db:1"}, &cluster.Cluster{}); err != nil || img != "example.org/db:1" {
		t.Fatalf("a fixed image: %s %v", img, err)
	}
	if _, err := childDestinationImage(m, &cluster.Cluster{}); err == nil {
		t.Fatal("a release image without a parent master must be refused")
	}
}
