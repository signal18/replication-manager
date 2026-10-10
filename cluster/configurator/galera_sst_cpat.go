// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"fmt"
	"os"
)

// Galera SST and the .system tree. Before restoring, the joiner's SST script empties the
// datadir and the InnoDB directories: every first-level entry of each root that does not
// match its "cpat" regex is removed (wsrep_sst_mariabackup: find ib_home_dir ib_undo_dir
// ib_log_dir "$DATA" -mindepth 1 -prune -regex "$cpat" -o -exec rm -rf). Under .system sit
// MOUNT POINTS, the volumes per role (tmp, innodb/redo, repl): rm answers "Device or
// resource busy", the script exits 1, Galera receives an empty position and aborts the
// joiner (signal 11 after "Application state transfer failed"), with or without the
// nosplitpath tag: Galera never joined a node. cpat is read from the [sst] group (parse_cnf
// sst cpat): the default plus .system (the datadir root) and .system/innodb/redo (the
// InnoDB root on the split layout) keeps the mount points; their files are still emptied,
// innodb_log_group_home_dir being a root the script cleans itself (-mindepth 1).
// dev3 galera-sst 2026-10-10.
const (
	galeraSSTCpatFile = "03_with_rep_wsrep_sst_keep_system.cnf"
	// the stock default of wsrep_sst_mariabackup (MariaDB 11.8) + the .system mount points
	galeraSSTCpat = `.*\.pem$\|.*galera\.cache$\|.*sst_in_progress$\|.*\.sst$\|.*gvwstate\.dat$\|.*grastate\.dat$\|.*\.err$\|.*\.log$\|.*RPM_UPGRADE_MARKER$\|.*RPM_UPGRADE_HISTORY$\|.*/\.system$\|.*/\.system/innodb/redo$`
)

// galeraSplitPathSST: a Galera cluster needs the [sst] cpat, split path or not: the
// .system mount points exist in both layouts.
func (configurator *Configurator) galeraSplitPathSST() bool {
	return configurator.IsFilterInDBTags("wsrep")
}

// writeGaleraSSTCpat renders the [sst] cpat in conf.d (included by my.cnf, read by the SST
// script through my_print_defaults, which keeps the backslashes as written).
func (configurator *Configurator) writeGaleraSSTCpat(Datadir string) error {
	dir := Datadir + "/init/etc/mysql/conf.d"
	if err := os.MkdirAll(dir, os.FileMode(0775)); err != nil {
		return fmt.Errorf("Compliance create directory %q: %s", dir, err)
	}
	content := "# replication-manager: the Galera SST keeps the .system mount points (see galera_sst_cpat.go)\n[sst]\ncpat=" + galeraSSTCpat + "\n"
	return os.WriteFile(dir+"/"+galeraSSTCpatFile, []byte(content), 0644)
}
