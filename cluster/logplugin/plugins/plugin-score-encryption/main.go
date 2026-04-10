// plugin-score-encryption evaluates at-rest encryption compliance checks:
//
//	HasTableEncryption  — InnoDB/Aria table encryption enabled
//	HasBinlogEncryption — binary log encryption enabled
//	HasTmpEncryption    — temporary file/table encryption enabled
//	HasBackupEncryption — backups are configured with encryption (from cluster context)
//
// Security findings (require adding with_sec_keyfileencrypt compliance tag + restart):
//
//	SEC0109  innodb_encrypt_tables/aria_encrypt_tables both OFF — table data unencrypted at rest
//	SEC0110  encrypt_binlog/binlog_encryption both OFF — binary logs unencrypted at rest
//	SEC0111  encrypt_tmp_files/encrypt_tmp_disk_tables both OFF — temp files unencrypted at rest
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

	var findings []wire.Finding

	// SEC0109 — table encryption disabled
	if !hasTableEnc {
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0109",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: table encryption is disabled (innodb_encrypt_tables=%s, aria_encrypt_tables=%s)"+
					" — InnoDB/Aria data files are stored unencrypted on disk."+
					" Enable the with_sec_keyfileencrypt compliance tag to activate file-key encryption (requires restart).",
				req.ServerURL, get("innodb_encrypt_tables"), get("aria_encrypt_tables")),
		})
	}

	// SEC0110 — binlog encryption disabled
	if !hasBinlogEnc {
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0110",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: binary log encryption is disabled (encrypt_binlog=%s, binlog_encryption=%s)"+
					" — binary logs contain row-level data changes in plaintext."+
					" Enable the with_sec_keyfileencrypt compliance tag to activate binlog encryption (requires restart).",
				req.ServerURL, get("encrypt_binlog"), get("binlog_encryption")),
		})
	}

	// SEC0111 — tmp file encryption disabled
	if !hasTmpEnc {
		findings = append(findings, wire.Finding{
			ErrKey:   "SEC0111",
			Severity: "SECURITY",
			Description: fmt.Sprintf(
				"Server %s: temporary file encryption is disabled (encrypt_tmp_files=%s, encrypt_tmp_disk_tables=%s)"+
					" — temporary tables and sort buffers spilled to disk are stored in plaintext."+
					" Enable the with_sec_keyfileencrypt compliance tag to activate tmp encryption (requires restart).",
				req.ServerURL, get("encrypt_tmp_files"), get("encrypt_tmp_disk_tables")),
		})
	}

	json.NewEncoder(os.Stdout).Encode(wire.Response{ScoreChecks: checks, Findings: findings})
}
