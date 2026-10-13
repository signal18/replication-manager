// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"fmt"
	"os"
)

// A ColumnStore child of an OLTP parent (#2015): ColumnStore takes no row UPDATE or DELETE
// from replication (its row-level update and delete do nothing; preprod belair-mcs lost an
// UPDATE silently with columnstore_replication_slave ON, and stopped on 1032 with it OFF).
// Its ColumnStore tables are carried by the replplugin-columnstore plugin, and its native
// channel from the parent ignores them: <channel>.replicate_ignore_table, set live at the
// seed and rendered here into the bundle for the next start. The line comes from the
// plugin's TOML, through the server's template environment (ColumnstoreReplicateIgnoreEnv).
const (
	columnstoreReplicateIgnoreFile = "03_with_mcs_replicate_ignore.cnf"
	// ColumnstoreReplicateIgnoreEnv is "<channel>.replicate_ignore_table=<tables>" in a UM's
	// template environment, "" when its channel ignores nothing
	ColumnstoreReplicateIgnoreEnv = "%%ENV:COLUMNSTORE_REPLICATE_IGNORE%%"
)

// writeColumnstoreReplicateIgnore renders the channel's ignore list in conf.d (included by
// my.cnf).
func (configurator *Configurator) writeColumnstoreReplicateIgnore(Datadir, line string) error {
	dir := Datadir + "/init/etc/mysql/conf.d"
	if err := os.MkdirAll(dir, os.FileMode(0775)); err != nil {
		return fmt.Errorf("Compliance create directory %q: %s", dir, err)
	}
	content := "# replication-manager: the ColumnStore tables are carried by replplugin-columnstore, not by this channel (see columnstore_replication.go)\n[mariadb]\n" + line + "\n"
	return os.WriteFile(dir+"/"+columnstoreReplicateIgnoreFile, []byte(content), 0644)
}
