// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"strings"
)

// The retention policy is the user's, one per cluster (backup-keep-*): how many backups to
// keep and for how long. The attached archive's plugin is asked to apply it (capability
// purge), after each backup it receives or on a schedule; how it purges is its own config
// (the restic purge-* settings: grouping, kept tag, prune) (#2091).

// When the attached archive is asked to purge.
const (
	ConstArchivePurgeAfterBackup = "after-backup"
	ConstArchivePurgeSchedule    = "schedule"
)

// ArchivePurgePolicy is what the plugin is asked to keep: a zero count or an empty
// duration is not part of the policy.
type ArchivePurgePolicy struct {
	KeepLast          int    `json:"keepLast"`
	KeepHourly        int    `json:"keepHourly"`
	KeepDaily         int    `json:"keepDaily"`
	KeepWeekly        int    `json:"keepWeekly"`
	KeepMonthly       int    `json:"keepMonthly"`
	KeepYearly        int    `json:"keepYearly"`
	KeepWithin        string `json:"keepWithin"`
	KeepWithinHourly  string `json:"keepWithinHourly"`
	KeepWithinDaily   string `json:"keepWithinDaily"`
	KeepWithinWeekly  string `json:"keepWithinWeekly"`
	KeepWithinMonthly string `json:"keepWithinMonthly"`
	KeepWithinYearly  string `json:"keepWithinYearly"`
}

// ArchivePurgePolicy is the cluster's retention policy.
func (conf *Config) ArchivePurgePolicy() ArchivePurgePolicy {
	return ArchivePurgePolicy{
		KeepLast:          conf.BackupKeepLast,
		KeepHourly:        conf.BackupKeepHourly,
		KeepDaily:         conf.BackupKeepDaily,
		KeepWeekly:        conf.BackupKeepWeekly,
		KeepMonthly:       conf.BackupKeepMonthly,
		KeepYearly:        conf.BackupKeepYearly,
		KeepWithin:        strings.TrimSpace(conf.BackupKeepWithin),
		KeepWithinHourly:  strings.TrimSpace(conf.BackupKeepWithinHourly),
		KeepWithinDaily:   strings.TrimSpace(conf.BackupKeepWithinDaily),
		KeepWithinWeekly:  strings.TrimSpace(conf.BackupKeepWithinWeekly),
		KeepWithinMonthly: strings.TrimSpace(conf.BackupKeepWithinMonthly),
		KeepWithinYearly:  strings.TrimSpace(conf.BackupKeepWithinYearly),
	}
}

// IsEmpty: the policy keeps nothing in particular, so a purge would delete everything; the
// plugin is never asked.
func (p ArchivePurgePolicy) IsEmpty() bool {
	return p.KeepLast <= 0 && p.KeepHourly <= 0 && p.KeepDaily <= 0 && p.KeepWeekly <= 0 &&
		p.KeepMonthly <= 0 && p.KeepYearly <= 0 &&
		p.KeepWithin == "" && p.KeepWithinHourly == "" && p.KeepWithinDaily == "" &&
		p.KeepWithinWeekly == "" && p.KeepWithinMonthly == "" && p.KeepWithinYearly == ""
}

// Validate checks the durations (2y5m7d3h).
func (p ArchivePurgePolicy) Validate() error {
	for name, d := range map[string]string{
		"backup-keep-within": p.KeepWithin, "backup-keep-within-hourly": p.KeepWithinHourly,
		"backup-keep-within-daily": p.KeepWithinDaily, "backup-keep-within-weekly": p.KeepWithinWeekly,
		"backup-keep-within-monthly": p.KeepWithinMonthly, "backup-keep-within-yearly": p.KeepWithinYearly,
	} {
		if !ResticDurationChecker(d) {
			return fmt.Errorf("invalid duration format '%s': %s", name, d)
		}
	}
	return nil
}

// ValidateArchivePurgeTrigger checks a backup-archive-purge-trigger value.
func ValidateArchivePurgeTrigger(v string) error {
	switch v {
	case ConstArchivePurgeAfterBackup, ConstArchivePurgeSchedule:
		return nil
	}
	return fmt.Errorf("backup-archive-purge-trigger must be %s or %s", ConstArchivePurgeAfterBackup, ConstArchivePurgeSchedule)
}

// ArchivePurgeOnSchedule: the purge runs from scheduler-archive-purge-cron.
func (conf *Config) ArchivePurgeOnSchedule() bool {
	return conf.BackupArchivePurgeTrigger == ConstArchivePurgeSchedule
}

// ArchivePurgeAfterBackup: the purge runs after each backup the archive receives (the
// default, also for an unknown value).
func (conf *Config) ArchivePurgeAfterBackup() bool {
	return !conf.ArchivePurgeOnSchedule()
}
