// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// archiveplugin-pbs is replication-manager's Proxmox Backup Server archive plugin
// (archive-pbs): a cluster's backups kept in a PBS datastore namespace, through the
// official proxmox-backup-client, a prerequisite of the node (not embedded: PBS speaks its
// own protocol, chunk store and indexes, and has no Go library). replication-manager runs
// it once per operation, as the wire package describes: the Request on stdin, the Response
// on stdout, progress on stderr, the secrets in the environment.
package main

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && os.Args[1] == wire.ManifestFlag {
		if err := wire.WriteManifests(os.Stdout, manifests()); err != nil {
			return 2
		}
		return 0
	}

	// stdout carries the response only: the client's output goes to stderr
	out := os.Stdout
	os.Stdout = os.Stderr

	req, err := wire.ReadRequest(os.Stdin)
	var resp wire.Response
	if err == nil {
		resp, err = handle(req)
	}
	if werr := wire.WriteResponse(out, resp, err); werr != nil {
		logf("write response: %s", werr)
		return 2
	}
	if err != nil {
		return 1
	}
	return 0
}

// logf writes a progress or diagnostic line on stderr.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "archiveplugin-pbs: "+format+"\n", args...)
}
