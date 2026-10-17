// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// archiveplugin-plakar is replication-manager's plakar archive plugin (#2091): the plakar
// engine (github.com/PlakarKorp/kloset) embedded, on a local folder (archive-plakar-local)
// or an S3 bucket (archive-plakar-s3). replication-manager runs it once per operation, as
// the wire package describes: the Request on stdin, the Response on stdout, progress on
// stderr, the secrets in the environment.
package main

import (
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

	// stdout carries the response only: anything the engine prints goes to stderr
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
