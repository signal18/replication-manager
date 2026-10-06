// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"github.com/signal18/replication-manager/config"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

// ReseedReadiness is what a rolling reprov across a major release needs before it may
// start (Stéphane 2026-10-02): binary logs monitored, a backup-based reseed method, and
// a backup newer than the binary log retention so the reseeded node can catch up.
// Each missing condition is one tracked state (WARN0222 backup, WARN0223/WARN0231 method,
// WARN0224 binlog), open while it holds, resolved when it no longer does.
type ReseedIssue struct {
	Code string // WARN0222 (no usable backup), WARN0223 / WARN0231 on PostgreSQL (reseed method), WARN0224 (binary logs), "" (no primary)
	Text string
}

type ReseedReadiness struct {
	Issues        []ReseedIssue
	LogicalFresh  bool // a completed logical backup of the primary newer than the retention
	PhysicalFresh bool // same for a physical backup
	Retention     time.Duration
}

// binlogRetention is the primary's binary log retention: binlog_expire_logs_seconds,
// else expire_logs_days; 0 when the server never purges on its own.
func binlogRetention(master *ServerMonitor) time.Duration {
	if master == nil || master.Variables == nil {
		return 0
	}
	if s := strings.TrimSpace(master.Variables.Get("BINLOG_EXPIRE_LOGS_SECONDS")); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	if s := strings.TrimSpace(master.Variables.Get("EXPIRE_LOGS_DAYS")); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
			return time.Duration(f * 24 * float64(time.Hour))
		}
	}
	return 0
}

// backupFresh says whether a completed backup is newer than the retention (any
// completed backup when the server never purges).
func backupFresh(meta *backupmgr.BackupMetadata, retention time.Duration) bool {
	if meta == nil || !meta.Completed || meta.EndTime.IsZero() {
		return false // an incomplete backup (the job failed, metadata written anyway) is no backup
	}
	return retention == 0 || time.Since(meta.EndTime) < retention
}

func describeBackup(kind string, meta *backupmgr.BackupMetadata) string {
	if meta == nil || !meta.Completed || meta.EndTime.IsZero() {
		return "no completed " + kind + " backup of the primary"
	}
	return "last " + kind + " backup of the primary " + meta.EndTime.UTC().Format("2006-01-02 15:04") + " UTC"
}

// GetReseedReadiness computes the readiness from the primary's state and the cluster
// settings, without side effects.
func (cluster *Cluster) GetReseedReadiness() ReseedReadiness {
	r := ReseedReadiness{}
	master := cluster.GetMaster()
	if master == nil {
		r.Issues = append(r.Issues, ReseedIssue{Text: "no primary"})
		return r
	}
	switch {
	case master.DBVersion != nil && master.DBVersion.IsPostgreSQL():
		// no binary logs on PostgreSQL: WARN0224 does not apply
	case !master.HaveBinlog:
		r.Issues = append(r.Issues, ReseedIssue{Code: "WARN0224", Text: fmt.Sprintf(clusterError["WARN0224"], "log_bin is off on "+master.URL)})
	case !cluster.Conf.BackupBinlogs:
		r.Issues = append(r.Issues, ReseedIssue{Code: "WARN0224", Text: fmt.Sprintf(clusterError["WARN0224"], "backup-binlogs is off")})
	}
	// The reseed method matters where a node can be reseeded: on PostgreSQL that is the
	// logical replication topology only (a single active-passive instance has no replica).
	switch {
	case master.DBVersion != nil && master.DBVersion.IsPostgreSQL():
		// PostgreSQL: its own words (no mysqldump there) and only the logical backup helps
		// across a major release
		if cluster.GetTopology() == config.TopoMasterSlavePgLog && !cluster.Conf.AutorejoinLogicalBackup {
			r.Issues = append(r.Issues, ReseedIssue{Code: "WARN0231", Text: clusterError["WARN0231"]})
		}
	case !cluster.Conf.AutorejoinLogicalBackup && !cluster.Conf.AutorejoinPhysicalBackup:
		r.Issues = append(r.Issues, ReseedIssue{Code: "WARN0223", Text: clusterError["WARN0223"]})
	}
	r.Retention = binlogRetention(master)
	master.backupMetaMutex.Lock()
	logical, physical := master.LastBackupMeta.Logical, master.LastBackupMeta.Physical
	master.backupMetaMutex.Unlock()
	if cluster.Conf.AutorejoinLogicalBackup {
		r.LogicalFresh = backupFresh(logical, r.Retention)
	}
	if cluster.Conf.AutorejoinPhysicalBackup {
		r.PhysicalFresh = backupFresh(physical, r.Retention)
	}
	if (cluster.Conf.AutorejoinLogicalBackup || cluster.Conf.AutorejoinPhysicalBackup) && !r.LogicalFresh && !r.PhysicalFresh {
		parts := []string{}
		if cluster.Conf.AutorejoinLogicalBackup {
			parts = append(parts, describeBackup("logical", logical))
		}
		if cluster.Conf.AutorejoinPhysicalBackup {
			parts = append(parts, describeBackup("physical", physical))
		}
		ret := "the primary never purges its binary logs"
		if r.Retention > 0 {
			ret = "binary log retention " + r.Retention.String()
		}
		r.Issues = append(r.Issues, ReseedIssue{Code: "WARN0222", Text: fmt.Sprintf(clusterError["WARN0222"], strings.Join(parts, ", ")+", "+ret)})
	}
	return r
}

// CheckReseedReadiness opens the readiness states every tick while their condition
// holds; a state not set on a tick resolves.
func (cluster *Cluster) CheckReseedReadiness() {
	for _, issue := range cluster.GetReseedReadiness().Issues {
		if issue.Code != "" {
			cluster.SetState(issue.Code, state.State{ErrType: "WARNING", ErrDesc: issue.Text, ErrFrom: "CHECK"})
		}
	}
}

// IssueTexts is the readiness issues as text, for a plan or an error.
func (r ReseedReadiness) IssueTexts() []string {
	out := make([]string, 0, len(r.Issues))
	for _, i := range r.Issues {
		out = append(out, i.Text)
	}
	return out
}
