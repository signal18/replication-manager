// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func (cluster *Cluster) ResticGetEnv() []string {
	newEnv := append(os.Environ(), "RESTIC_PASSWORD="+cluster.Conf.GetDecryptedValue("backup-restic-password"))
	newEnv = append(newEnv, "RESTIC_CACHE_DIR="+cluster.Conf.WorkingDir+"/"+cluster.Name+"/.cache/restic")

	if cluster.Conf.BackupResticAws {
		newEnv = append(newEnv, "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository+"/"+cluster.Name)
	} else {
		if _, err := os.Stat(cluster.GetResticLocalDir()); os.IsNotExist(err) {
			err := os.MkdirAll(cluster.GetResticLocalDir(), os.ModePerm)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Create archive directory failed: %s,%s", cluster.GetResticLocalDir(), err)
			}
		}
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.GetResticLocalDir())
	}
	return newEnv
}

func (cluster *Cluster) ReloadResticEnv() {
	if cluster.ResticManager != nil {
		cluster.ResticManager.SetEnv(cluster.ResticGetEnv())
	}
}

func (cluster *Cluster) CheckResticInstallation() {
	if cluster.Conf.BackupRestic && cluster.VersionsMap.Get("restic") == nil {
		if err := cluster.RefreshResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic version: %s", cluster.VersionsMap.Get("restic").ToString())
		}
	}
}

func (cluster *Cluster) CheckResticErrors() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// If repo cannot be initialized, all other errors are not relevant. So we just fetch the init repo errors
	if !cluster.ResticManager.CanInitRepo && cluster.ResticManager.HasAnyError() {
		err := cluster.ResticManager.FetchAndClearError(backupmgr.InitTask)
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return
	}

	for task, err := range cluster.ResticManager.FetchAndClearErrors() {
		switch task {
		case backupmgr.FetchTask:
			cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
		case backupmgr.PurgeTask:
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
		case backupmgr.UnlockTask:
			cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		default:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unknown restic task error: %s", err)
		}
	}

}

func (cluster *Cluster) CheckResticConfigBackup() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if err := cluster.BackupResticConfig(); err != nil {
		cluster.SetState("WARN0145", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0145"], err), ErrFrom: "BACKUP"})
	}
}

func (cluster *Cluster) StartResticManager() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	resticManager := backupmgr.NewResticRepo(cluster.Conf.BackupResticBinaryPath, cluster.MessageChan, config.ConstLogModRestic)
	if err := cluster.Conf.ValidateResticPermissions(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Invalid restic permission config: %s", err)
	}
	resticManager.OnPurgeComplete = cluster.handleResticPurgeComplete
	resticManager.SetPermissions(cluster.Conf.GetResticDirMode(), cluster.Conf.GetResticFileMode())
	resticManager.SetOperationTimeout(cluster.Conf.GetResticTimeout())
	resticManager.SetDumpTimeout(cluster.Conf.GetResticDumpTimeout())
	resticManager.AllowUnsafeMount = cluster.Conf.BackupResticAllowUnsafeMount
	resticManager.MountRecoveryEnabled = cluster.Conf.BackupResticMountRecoveryEnabled
	resticManager.AutoDetectAndDisableMount()
	cluster.ResticManager = resticManager
	cluster.ReloadResticEnv()
	if cluster.ResticManager.RecoverMountStateOnStartup() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Recovered restic mount state on startup")
	}
	go cluster.ResticFetchRepo()
	return nil
}

func (cluster *Cluster) ResticInitRepo(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	err := cluster.ResticManager.InitRepo(force)
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) AddPurgeTask(snapshotID string) error {
	return cluster.ResticPurgeSnapshotWithOptions(snapshotID, true, false)
}

func (cluster *Cluster) ResticPurgeSnapshot(snapshotID string, now bool) error {
	return cluster.ResticPurgeSnapshotWithOptions(snapshotID, now, false)
}

func (cluster *Cluster) ResticPurgeSnapshotWithOptions(snapshotID string, now bool, dryRun bool) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	trimmed := strings.TrimSpace(snapshotID)
	if trimmed == "" {
		return fmt.Errorf("Unable to purge single snapshot: snapshot ID is empty")
	}

	cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
		SnapshotID: trimmed,
		Compact:    cluster.Conf.BackupResticPurgePruneCompact,
		Prune:      cluster.Conf.BackupResticPurgePrune,
		PruneOption: backupmgr.ResticPruneOption{
			MaxUnused:           cluster.Conf.BackupResticPurgePruneMaxUnused,
			MaxRepackSize:       cluster.Conf.BackupResticPurgePruneMaxRepackSize,
			RepackCacheableOnly: cluster.Conf.BackupResticPurgePruneRepackCacheableOnly,
			RepackSmall:         cluster.Conf.BackupResticPurgePruneRepackSmall,
			RepackUncompressed:  cluster.Conf.BackupResticPurgePruneRepackUncompressed,
		},
		DryRun: dryRun,
	}, now)
	return nil
}

func (cluster *Cluster) ResticPurgeRepo(now bool) error {
	return cluster.ResticPurgeRepoWithOptions(now, false)
}

func (cluster *Cluster) ResticPurgeRepoWithOptions(now bool, dryRun bool) error {
	if cluster.Conf.BackupRestic {
		err := cluster.Conf.CheckKeepWithin() // Check if backup-keep-within is valid
		if err != nil {
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		if cluster.ResticManager == nil {
			cluster.StartResticManager()
		}

		hasKeepN := cluster.Conf.BackupKeepLast > 0 ||
			cluster.Conf.BackupKeepHourly > 0 ||
			cluster.Conf.BackupKeepDaily > 0 ||
			cluster.Conf.BackupKeepWeekly > 0 ||
			cluster.Conf.BackupKeepMonthly > 0 ||
			cluster.Conf.BackupKeepYearly > 0
		hasKeepWithin := strings.TrimSpace(cluster.Conf.BackupKeepWithin) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinHourly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinDaily) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinWeekly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinMonthly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinYearly) != ""
		if !hasKeepN && !hasKeepWithin {
			err := fmt.Errorf("restic purge skipped: no keep-last/keep-within policy configured")
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		groupBy := strings.TrimSpace(cluster.Conf.BackupResticPurgeGroupBy)
		if groupBy == "" || strings.EqualFold(groupBy, "default") {
			groupBy = ""
		} else if strings.EqualFold(groupBy, "none") {
			groupBy = "none"
		}

		keepTemplates := parseResticKeepTagTemplates(cluster.Conf.BackupResticPurgeKeepTag, cluster)
		keepValues := map[string]string{
			"tenant":  cluster.Conf.Cloud18GitUser,
			"cluster": cluster.Name,
		}
		keepTags := make([]string, 0, len(keepTemplates))
		for _, template := range keepTemplates {
			rendered, ok := renderResticKeepTagTemplate(template, keepValues, cluster)
			if !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to render restic keep-tag template %q", template)
				continue
			}
			if strings.TrimSpace(rendered) == "" {
				continue
			}
			keepTags = append(keepTags, rendered)
		}

		purgeHosts := splitResticPurgeFilterValues(cluster.Conf.BackupResticPurgeHost)
		purgeTags := parseResticTagFilterValues(cluster.Conf.BackupResticPurgeTag, cluster, "purge")
		purgePaths := filterResticAbsolutePaths(splitResticPurgeFilterValues(cluster.Conf.BackupResticPurgePath), cluster)
		cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
			KeepLast:          cluster.Conf.BackupKeepLast,
			KeepHourly:        cluster.Conf.BackupKeepHourly,
			KeepDaily:         cluster.Conf.BackupKeepDaily,
			KeepWeekly:        cluster.Conf.BackupKeepWeekly,
			KeepMonthly:       cluster.Conf.BackupKeepMonthly,
			KeepYearly:        cluster.Conf.BackupKeepYearly,
			KeepWithin:        cluster.Conf.BackupKeepWithin,
			KeepWithinHourly:  cluster.Conf.BackupKeepWithinHourly,
			KeepWithinDaily:   cluster.Conf.BackupKeepWithinDaily,
			KeepWithinWeekly:  cluster.Conf.BackupKeepWithinWeekly,
			KeepWithinMonthly: cluster.Conf.BackupKeepWithinMonthly,
			KeepWithinYearly:  cluster.Conf.BackupKeepWithinYearly,
			GroupBy:           groupBy,
			KeepTag:           keepTags,
			Host:              purgeHosts,
			Tag:               purgeTags,
			Path:              purgePaths,
			Compact:           cluster.Conf.BackupResticPurgePruneCompact,
			Prune:             cluster.Conf.BackupResticPurgePrune,
			PruneOption: backupmgr.ResticPruneOption{
				MaxUnused:           cluster.Conf.BackupResticPurgePruneMaxUnused,
				MaxRepackSize:       cluster.Conf.BackupResticPurgePruneMaxRepackSize,
				RepackCacheableOnly: cluster.Conf.BackupResticPurgePruneRepackCacheableOnly,
				RepackSmall:         cluster.Conf.BackupResticPurgePruneRepackSmall,
				RepackUncompressed:  cluster.Conf.BackupResticPurgePruneRepackUncompressed,
			},
			DryRun: dryRun,
		}, now)
	}
	return nil
}

func (cluster *Cluster) ResticFetchRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// Check if no other fetch task queued
	if !cluster.ResticManager.HasFetchQueue() {
		cluster.ResticManager.AddFetchTask()
	}
}

func (cluster *Cluster) BackupResticConfig() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if _, err := os.Stat(filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")); err == nil {
		// Backup already exists
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	dest := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")
	src := filepath.Join(repopath, "config")

	err := misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file backed up to %s", dest)
	return nil
}

func (cluster *Cluster) RestoreResticConfig(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	_, err := os.Stat(filepath.Join(repopath, "config"))
	if !os.IsNotExist(err) && !force {
		return fmt.Errorf("restic config file already exists in repo path %s", repopath)
	}

	dest := filepath.Join(repopath, "config")
	src := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")

	err = misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file restored from %s", src)
	return nil
}

func (cluster *Cluster) ResticUnlockRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.ResticManager.AddUnlockTask()

}

func (cluster *Cluster) ResticGetQueue() ([]*backupmgr.ResticTask, error) {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil, nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.TaskQueue, nil
}

var resticTagTemplateKeySet = map[string]struct{}{
	"tenant":      {},
	"cluster":     {},
	"engine":      {},
	"version":     {},
	"backup-type": {},
	"backup-tool": {},
	"line":        {},
	"method":      {},
}

var resticKeepTagTemplateKeySet = map[string]struct{}{
	"tenant":  {},
	"cluster": {},
}

var resticTagTemplatePattern = regexp.MustCompile(`\{([^}]+)\}`)

func normalizeResticTagCategory(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	return normalized
}

func parseResticTagTemplates(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := splitResticTagTemplates(value)
	templates := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		template := strings.TrimSpace(part)
		if template == "" {
			continue
		}
		if _, ok := seen[template]; ok {
			continue
		}
		seen[template] = struct{}{}
		templates = append(templates, template)
	}
	return templates
}

func parseResticKeepTagTemplates(value string, cluster *Cluster) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts, hadUnmatched := splitResticKeepTagTemplates(value)
	if hadUnmatched && cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring restic keep-tag with unmatched quotes in %q", value)
	}
	templates := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		template := strings.TrimSpace(part)
		if template == "" {
			continue
		}
		if _, ok := seen[template]; ok {
			continue
		}
		seen[template] = struct{}{}
		templates = append(templates, template)
	}
	return templates
}

func parseResticTagFilterValues(value string, cluster *Cluster, scope string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts, hadUnmatched := splitResticTagFilterValues(value)
	if hadUnmatched && cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Ignoring restic %s tag filter with unmatched quotes in %q", scope, value)
	}
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if literal, ok := unquoteResticTagLiteral(candidate); ok {
			candidate = strings.TrimSpace(literal)
		}
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func splitResticTagTemplates(value string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false

	for _, r := range value {
		if quote != 0 {
			if quote == '"' && !escaped && r == '\\' {
				escaped = true
				current.WriteRune(r)
				continue
			}
			if quote == '"' && escaped {
				current.WriteRune(r)
				escaped = false
				continue
			}
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '"', '\'':
			quote = r
			current.WriteRune(r)
		case ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 || strings.HasSuffix(value, ",") {
		parts = append(parts, current.String())
	}

	return parts
}

func splitResticKeepTagTemplates(value string) ([]string, bool) {
	return splitResticSpaceSeparatedValues(value)
}

func splitResticTagFilterValues(value string) ([]string, bool) {
	return splitResticSpaceSeparatedValues(value)
}

func splitResticSpaceSeparatedValues(value string) ([]string, bool) {
	parts := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false
	hadUnmatched := false

	for _, r := range value {
		if quote != 0 {
			if quote == '"' && !escaped && r == '\\' {
				escaped = true
				current.WriteRune(r)
				continue
			}
			if quote == '"' && escaped {
				current.WriteRune(r)
				escaped = false
				continue
			}
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '"', '\'':
			quote = r
			current.WriteRune(r)
		case ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if quote != 0 {
		hadUnmatched = true
	} else if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts, hadUnmatched
}

// splitResticPurgeFilterValues splits host/path filters on commas and whitespace.
func splitResticPurgeFilterValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func filterResticAbsolutePaths(values []string, cluster *Cluster) []string {
	if len(values) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if !filepath.IsAbs(trimmed) {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring restic purge path (not absolute): %s", trimmed)
			}
			continue
		}
		filtered = append(filtered, trimmed)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func validateResticKeepTagTemplatesStrict(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	_, hadUnmatched := splitResticKeepTagTemplates(value)
	if hadUnmatched {
		return fmt.Errorf("restic keep-tag has unmatched quotes")
	}
	return nil
}

func isQuotedResticTagLiteral(value string) bool {
	if len(value) < 2 {
		return false
	}
	first := value[0]
	last := value[len(value)-1]
	return (first == '"' && last == '"') || (first == '\'' && last == '\'')
}

func unquoteResticTagLiteral(value string) (string, bool) {
	if !isQuotedResticTagLiteral(value) {
		return value, false
	}

	quote := value[0]
	raw := value[1 : len(value)-1]
	if quote == '\'' {
		// Single quotes preserve content literally (no escape processing).
		return raw, true
	}

	var b strings.Builder
	b.Grow(len(raw))
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	// Double quotes allow \\ and \" escapes (backslash only affects the next rune).
	return b.String(), true
}

func renderResticTagTemplate(template string, values map[string]string, cluster *Cluster) (string, bool) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", false
	}

	if literal, ok := unquoteResticTagLiteral(trimmed); ok {
		literal = strings.TrimSpace(literal)
		if literal == "" {
			return "", false
		}
		return literal, true
	}

	matches := resticTagTemplatePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		if strings.Contains(trimmed, ":") {
			return trimmed, true
		}
		key := normalizeResticTagCategory(trimmed)
		if _, ok := resticTagTemplateKeySet[key]; ok {
			value := strings.TrimSpace(values[key])
			if value == "" {
				return "", false
			}
			return fmt.Sprintf("%s:%s", key, value), true
		}
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unknown restic tag template %q", trimmed)
		}
		return trimmed, true
	}

	rendered := trimmed
	for _, match := range matches {
		raw := match[1]
		key := normalizeResticTagCategory(raw)
		if key == "" {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid restic tag template %q", template)
			}
			return "", false
		}
		if _, ok := resticTagTemplateKeySet[key]; !ok {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unknown restic tag template key %q in %q", raw, template)
			}
			return "", false
		}
		value := strings.TrimSpace(values[key])
		if value == "" {
			return "", false
		}
		rendered = strings.ReplaceAll(rendered, "{"+raw+"}", value)
	}

	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return "", false
	}
	return rendered, true
}

func renderResticKeepTagTemplate(template string, values map[string]string, cluster *Cluster) (string, bool) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", false
	}

	if literal, ok := unquoteResticTagLiteral(trimmed); ok {
		literal = strings.TrimSpace(literal)
		if literal == "" {
			return "", false
		}
		return literal, true
	}

	matches := resticTagTemplatePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return trimmed, true
	}

	rendered := trimmed
	for _, match := range matches {
		raw := match[1]
		key := normalizeResticTagCategory(raw)
		if key == "" {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid restic keep-tag template %q", template)
			}
			return "", false
		}
		if _, ok := resticKeepTagTemplateKeySet[key]; !ok {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unsupported restic keep-tag template key %q in %q", raw, template)
			}
			return "", false
		}
		value := strings.TrimSpace(values[key])
		if value == "" {
			return "", false
		}
		rendered = strings.ReplaceAll(rendered, "{"+raw+"}", value)
	}

	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return "", false
	}
	return rendered, true
}

func (server *ServerMonitor) BuildResticTags(backupType, backupTool, backupLine string, meta *backupmgr.BackupMetadata) []string {
	cluster := server.ClusterGroup
	lineValue := normalizeBackupLine(backupLine)
	if lineValue == "" {
		lineValue = backupmgr.BackupLineDefault
	}
	tagValues := map[string]string{
		"tenant":      cluster.Conf.Cloud18GitUser,
		"cluster":     cluster.Name,
		"engine":      server.DBVersion.Flavor,
		"version":     server.DBVersion.ToString(),
		"backup-type": backupType,
		"backup-tool": backupTool,
		"line":        lineValue,
		"method":      strings.TrimSpace(backupType),
	}

	templates := parseResticTagTemplates(cluster.Conf.BackupResticTags)
	tagSet := make(map[string]struct{})
	tags := make([]string, 0, len(templates)+3)
	for _, template := range templates {
		rendered, ok := renderResticTagTemplate(template, tagValues, cluster)
		if !ok || strings.TrimSpace(rendered) == "" {
			continue
		}
		if _, exists := tagSet[rendered]; exists {
			continue
		}
		tagSet[rendered] = struct{}{}
		tags = append(tags, rendered)
	}
	required := []string{}
	for _, tag := range required {
		if strings.HasSuffix(tag, ":") {
			continue
		}
		if _, exists := tagSet[tag]; exists {
			continue
		}
		tagSet[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func (cluster *Cluster) ResticModifyQueue(moveType string, taskID, cmpID int) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.MoveTask(moveType, taskID, cmpID)
}

func (cluster *Cluster) ResticCancelTask(taskId int) error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancelling restic task ID %d", taskId)

	cluster.ResticManager.CancelTask(taskId)

	return nil
}

func (cluster *Cluster) ResticClearQueue() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Clearing pending restic tasks from queue. Total tasks: %d", len(cluster.ResticManager.TaskQueue))

	cluster.ResticManager.ClearQueue()

	return nil
}

// ResticRunQueue starts processing the restic task queue
func (cluster *Cluster) ResticRunQueue() {

	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting restic task queue processing. Total tasks: %d", len(cluster.ResticManager.TaskQueue))
	cluster.ResticManager.ResumeWorker()
	cluster.IsResticQueuePaused = false
}

// ResticPauseQueue pauses the next restic task queue processing
func (cluster *Cluster) ResticPauseQueue() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pausing restic task queue processing")
	cluster.ResticManager.PauseWorker()
	cluster.IsResticQueuePaused = true
}

func (cluster *Cluster) UpdateDiskStat(dirpath string) error {
	diskstat, err := disk.Usage(dirpath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	if diskstat == nil {
		err := fmt.Errorf("disk usage is nil for %s", dirpath)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	cluster.DiskStatManager.UpdateStat(dirpath, diskstat)

	return nil
}

// TODO: Restic password change
func (cluster *Cluster) ChangeResticRepoPassword(newpass string) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if newpass == "" {
		return fmt.Errorf("New password is empty")
	}

	if newpass == cluster.Conf.GetDecryptedValue("backup-restic-password") {
		return fmt.Errorf("New password is the same as the current one")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Changing restic password for cluster %s", cluster.Name)

	cluster.ReloadResticEnv()

	keylist, err := cluster.ResticManager.GetRepoKeyList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to list restic keys: %s", err)
		return err
	}

	keylen := len(keylist)
	if keylen == 0 {
		return fmt.Errorf("No keys found in the restic repository")
	}

	oldkeyid := ""
	for _, key := range keylist {
		if key.Current {
			oldkeyid = key.Id
			break
		}
	}

	if _, err := os.Stat(cluster.ResticManager.GetCacheDirPath()); os.IsNotExist(err) {
		err := os.MkdirAll(cluster.ResticManager.GetCacheDirPath(), os.ModePerm)
		if err != nil {
			return fmt.Errorf("Error creating restic cache directory: %s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic cache directory created: %s", cluster.ResticManager.GetCacheDirPath())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Adding new key to restic repository")

	newpassfile := filepath.Join(cluster.ResticManager.GetCacheDirPath(), "newpass.txt")
	err = os.WriteFile(newpassfile, []byte(newpass), 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to write new password file: %s", err)
		return fmt.Errorf("failed to write new password file: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Temporary password file created: %s", newpassfile)

	defer func() {
		if _, err := os.Stat(newpassfile); err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Removing temporary password file")
			err := os.Remove(newpassfile)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove temporary password file: %s", err)
			}
		}
	}()

	err = cluster.ResticManager.AddRepoKey(newpassfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to add new key to restic repository: %s", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New key added to restic repository successfully. Saving new password.")

	// Save new password in configuration
	cluster.SetResticPassword(newpass)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New restic password saved in configuration successfully. Removing old key from repository using new password.")

	// Reload env with new password
	cluster.ReloadResticEnv()

	// Remove old key using new password
	err = cluster.ResticManager.RemoveRepoKey(oldkeyid)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove old key from restic repository: %s", err)
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic password changed successfully. New key added and old key removed.")

	return nil
}

func (cluster *Cluster) CheckBackupToolVersions() {
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.GetMaster()
		if bcksrv == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "No backup server or master server found for cluster %s", cluster.Name)
			return
		}
	}

	cluster.CheckLogicalBackupToolVersion(bcksrv)
	cluster.CheckPhysicalBackupToolVersion(bcksrv)
}

func (cluster *Cluster) CheckLogicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, logical := server.GetLatestMeta("logical")
	if logical != nil {
		v, _ := cluster.GetToolsVersion(logical.BackupTool)
		if v != nil && logical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(logical.BackupTool, logical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0156", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0156"], v.ToString(), logical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not compatible with restore version", server.URL)
			} else if cluster.IsInErrorState("WARN0156", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0156@%s", server.URL))
			}
		}
	}
	return nil
}

func (cluster *Cluster) CheckPhysicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, physical := server.GetLatestMeta("physical")
	if physical != nil {
		v, _ := cluster.GetToolsVersion(physical.BackupTool)
		if v != nil && physical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(physical.BackupTool, physical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0157", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0157"], v.ToString(), physical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not same with restore version", server.URL)
			} else if cluster.IsInErrorState("WARN0157", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0157@%s", server.URL))
			}
		}
	}
	return nil
}

// getSanitizedCompressionLevel validates and returns a safe compression level (1-9).
// If the configured value is out of range, it logs a warning and returns the default (6).
func (cluster *Cluster) getSanitizedCompressionLevel(logModule int) int {
	level := cluster.Conf.CompressBackupsCompressionLevel
	if level < 1 || level > 9 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlWarn,
			"compress-backups-compression-level value %d is out of range (1-9), using default 6", level)
		return 6 // Default to standard compression
	}
	return level
}

// getSanitizedParallelBlocks validates and returns safe parallel blocks (1-32).
// If the configured value is <= 0, it returns the default (16) for performance.
// If the configured value is > 32, it logs a warning and caps to 32.
func (cluster *Cluster) getSanitizedParallelBlocks(logModule int) int {
	blocks := cluster.Conf.CompressBackupsParallelBlocks
	if blocks <= 0 {
		return 16 // Default for SST/restore performance
	}
	if blocks > 32 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlWarn,
			"compress-backups-parallel-blocks value %d exceeds maximum 32, capping to 32", blocks)
		return 32 // Cap at maximum safe value
	}
	return blocks
}
