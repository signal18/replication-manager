// plugin-score-encryption evaluates at-rest encryption compliance checks:
//
//	HasTableEncryption  — InnoDB/Aria table encryption enabled
//	HasBinlogEncryption — binary log encryption enabled
//	HasTmpEncryption    — temporary file/table encryption enabled
//	HasBackupEncryption — backups are configured with encryption (from cluster context)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
)

func main() {
	var req wire.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}

	v := req.ServerVariables
	get := func(key string) string { return strings.TrimSpace(v[key]) }
	on := func(key string) bool { return strings.ToUpper(get(key)) == "ON" }

	// Table encryption: InnoDB (MySQL/MariaDB) or Aria (MariaDB)
	hasTableEnc := on("innodb_encrypt_tables") || on("aria_encrypt_tables")

	// Binlog encryption
	hasBinlogEnc := on("encrypt_binlog") || on("binlog_encryption")

	// Tmp encryption
	hasTmpEnc := on("encrypt_tmp_files") || on("encrypt_tmp_disk_tables")

	checks := []wire.ScoreCheck{
		{Tag: "HasTableEncryption", Pass: hasTableEnc,
			Detail: fmt.Sprintf("innodb_encrypt_tables=%s aria_encrypt_tables=%s",
				get("innodb_encrypt_tables"), get("aria_encrypt_tables"))},
		{Tag: "HasBinlogEncryption", Pass: hasBinlogEnc,
			Detail: fmt.Sprintf("encrypt_binlog=%s binlog_encryption=%s",
				get("encrypt_binlog"), get("binlog_encryption"))},
		{Tag: "HasTmpEncryption", Pass: hasTmpEnc,
			Detail: fmt.Sprintf("encrypt_tmp_files=%s encrypt_tmp_disk_tables=%s",
				get("encrypt_tmp_files"), get("encrypt_tmp_disk_tables"))},
		{Tag: "HasBackupEncryption", Pass: req.ClusterContext.BackupEncrypted,
			Detail: "restic backup with password configured"},
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks})
}
