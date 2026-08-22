// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package share

import (
	"strings"
	"testing"
)

// TestOpenSVCHaproxyModulesetUsesNonRootSafeSockets guards against the
// embedded OpenSVC HAProxy templates (opensvc/moduleset_mariadb.svc.mrm.proxy.json,
// var_name "proxy_cnf_haproxy" / "proxy_cnf_haproxy_runtime_api") regressing
// to a stats socket path under /run. A non-root HAProxy container (e.g.
// haproxy:3.4, uid 99) can't create a socket there -- /run is root:root 755
// in that image, so "stats socket /run/haproxy.sock ..."/"/run/admin.sock"
// fails HAProxy startup outright. /tmp is writable regardless of the
// container's uid, so both generated config files (haproxy.cfg,
// haproxy_check.cfg) must use a /tmp path for their local admin socket
// instead.
func TestOpenSVCHaproxyModulesetUsesNonRootSafeSockets(t *testing.T) {
	data, err := EmbededDbModuleFS.ReadFile("opensvc/moduleset_mariadb.svc.mrm.proxy.json")
	if err != nil {
		t.Fatalf("could not read embedded moduleset_mariadb.svc.mrm.proxy.json: %s", err)
	}
	content := string(data)

	for _, forbidden := range []string{"/run/haproxy.sock", "/run/admin.sock"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("moduleset_mariadb.svc.mrm.proxy.json contains %q -- a non-root HAProxy container can't create a socket under root-owned /run, use a /tmp path instead", forbidden)
		}
	}

	for _, want := range []string{"stats socket /tmp/haproxy.sock", "stats socket /tmp/admin.sock"} {
		if !strings.Contains(content, want) {
			t.Errorf("moduleset_mariadb.svc.mrm.proxy.json does not contain %q -- expected non-root-safe stats socket path", want)
		}
	}
}
