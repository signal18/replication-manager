// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func parseBackupRunOptions(r *http.Request) (cluster.BackupRunOptions, error) {
	opts := cluster.BackupRunOptions{}
	query := r.URL.Query()

	retentionValue := strings.TrimSpace(query.Get("retention-days"))
	if retentionValue == "" {
		retentionValue = strings.TrimSpace(query.Get("retentionDays"))
	}
	if retentionValue != "" {
		value, err := strconv.Atoi(retentionValue)
		if err != nil || value < 0 {
			return opts, fmt.Errorf("invalid retention-days")
		}
		opts.RetentionDays = value
	}

	lineValue := strings.TrimSpace(query.Get("line"))
	if lineValue != "" {
		line, err := normalizeBackupLineParam(lineValue)
		if err != nil {
			return opts, err
		}
		opts.Line = line
	}

	resticValue := strings.TrimSpace(query.Get("restic"))
	if resticValue != "" {
		value, err := strconv.ParseBool(resticValue)
		if err != nil {
			return opts, fmt.Errorf("invalid restic")
		}
		opts.ResticEnabled = &value
	}

	backupIDValue := strings.TrimSpace(query.Get("backup-id"))
	if backupIDValue == "" {
		backupIDValue = strings.TrimSpace(query.Get("backupId"))
	}
	if backupIDValue != "" {
		value, err := strconv.ParseInt(backupIDValue, 10, 64)
		if err != nil || value <= 0 {
			return opts, fmt.Errorf("invalid backup-id")
		}
		opts.BackupID = value
	}

	return opts, nil
}

func normalizeBackupLineParam(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case backupmgr.BackupLineDefault:
		return backupmgr.BackupLineDefault, nil
	case backupmgr.BackupLineAdhoc:
		return backupmgr.BackupLineAdhoc, nil
	default:
		return "", fmt.Errorf("invalid line")
	}
}
