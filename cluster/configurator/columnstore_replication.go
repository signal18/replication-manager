// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"fmt"
	"os"
)

// ColumnStore replicating from an OLTP parent (#2014): ColumnStore drops the row changes a
// replica applies unless columnstore_replication_slave is ON (preprod belair-mcs: a parent
// INSERT never arrived, the next UPDATE stopped the channel on error 1032). The variable is
// read-only at runtime, so it is rendered into the server's configuration bundle. Only for
// the UM holding the parent's channel, a per-server role the caller decides
// (ColumnstoreReplicationEnv in the template environment): another UM of the same
// ColumnStore cluster replicates from that one and must keep it OFF, or it would apply the
// same changes a second time to the shared storage.
const (
	columnstoreReplicationFile = "03_with_mcs_replication_slave.cnf"
	// ColumnstoreReplicationEnv is ON in a server's template environment when that server
	// applies its cluster's parent replication into ColumnStore
	ColumnstoreReplicationEnv = "%%ENV:COLUMNSTORE_REPLICATION_SLAVE%%"
)

// writeColumnstoreReplicationSlave renders the fragment in conf.d (included by my.cnf).
// loose_: a server started without the ColumnStore plugin only warns.
func (configurator *Configurator) writeColumnstoreReplicationSlave(Datadir string) error {
	dir := Datadir + "/init/etc/mysql/conf.d"
	if err := os.MkdirAll(dir, os.FileMode(0775)); err != nil {
		return fmt.Errorf("Compliance create directory %q: %s", dir, err)
	}
	content := "# replication-manager: this UM applies its parent's replication into ColumnStore (see columnstore_replication.go)\n[mariadb]\nloose_columnstore_replication_slave=ON\n"
	return os.WriteFile(dir+"/"+columnstoreReplicationFile, []byte(content), 0644)
}
