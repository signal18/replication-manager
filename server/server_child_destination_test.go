// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// CDC_CORE.md: a destination's image follows the parent's release when its manifest says
// "{version}", refused for a release its list does not have.
func TestChildDestinationImage(t *testing.T) {
	m := wire.PluginManifest{Name: "colstore", Image: "example.org/db:{version}", ImageVersions: []string{"11.4.14", "11.8.10"}}
	if img, err := destinationImageFor(m, "shop", "11.8.10"); err != nil || img != "example.org/db:11.8.10" {
		t.Fatalf("%s %v", img, err)
	}
	if _, err := destinationImageFor(m, "shop", "11.8.9"); err == nil || !strings.Contains(err.Error(), "11.4.14, 11.8.10") {
		t.Fatalf("a release without image must be refused with the list: %v", err)
	}
	m.ImageVersions = nil
	if img, _ := destinationImageFor(m, "shop", "12.3.4"); img != "example.org/db:12.3.4" {
		t.Fatalf("no list, any release: %s", img)
	}
	if img, err := childDestinationImage(wire.PluginManifest{Image: "example.org/db:1"}, &cluster.Cluster{}); err != nil || img != "example.org/db:1" {
		t.Fatalf("a fixed image: %s %v", img, err)
	}
	if _, err := childDestinationImage(m, &cluster.Cluster{}); err == nil {
		t.Fatal("a release image without a parent master must be refused")
	}
}
