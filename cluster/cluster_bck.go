// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

var splitDumpTimestampRegex = regexp.MustCompile(`^\d+$`)

// resticSftpRepoRegex matches the restic sftp backend repository syntax,
// sftp:[user@]host:path, e.g. sftp:backup@10.0.0.1:/srv/restic-repo
var resticSftpRepoRegex = regexp.MustCompile(`^sftp:[^:@\s]+(@[^:@\s]+)?:.+$`)

var (
	resolveDiskFilesystem = misc.ResolveFilesystem
	getDiskUsage          = disk.Usage
)

// ValidateResticSftpRepository checks that repoPath matches the
// sftp:[user@]host:/path syntax expected by restic's sftp backend, so a
// malformed value is rejected with a clear error instead of an opaque
// restic failure.
func ValidateResticSftpRepository(repoPath string) error {
	repoPath = strings.TrimSpace(repoPath)
	if !resticSftpRepoRegex.MatchString(repoPath) {
		return config.NewValidationError("backup-restic-local-repository", repoPath, "expected sftp:[user@]host:/path")
	}
	return nil
}

func buildResticS3RepoSpec(endpoint, bucket, prefix, clusterName string, appendCluster bool) (string, string) {
	bucket = strings.TrimSpace(bucket)
	prefix = strings.Trim(prefix, "/")
	if appendCluster && shouldAppendClusterNameS3(bucket, prefix, clusterName) {
		if prefix == "" {
			prefix = clusterName
		} else {
			prefix = prefix + "/" + clusterName
		}
	}

	repoPath := ""
	if endpoint != "" {
		endpoint = strings.TrimRight(endpoint, "/")
		repoPath = "s3:" + endpoint + "/" + bucket
	} else {
		repoPath = "s3:" + bucket
	}
	if prefix != "" {
		repoPath += "/" + prefix
	}

	return repoPath, prefix
}

func shouldAppendClusterNameS3(bucket, prefix, clusterName string) bool {
	if clusterName == "" {
		return false
	}
	if strings.TrimSpace(bucket) == clusterName {
		return false
	}
	prefix = strings.Trim(prefix, "/")
	if prefix != "" && path.Base(prefix) == clusterName {
		return false
	}
	return true
}

func shouldAppendClusterNameLocal(localRepoPath, clusterName string) bool {
	if clusterName == "" {
		return false
	}
	localRepoPath = strings.TrimSpace(localRepoPath)
	if localRepoPath == "" {
		return true
	}
	return filepath.Base(filepath.Clean(localRepoPath)) != clusterName
}

func (cluster *Cluster) ResticS3EffectivePrefixForInit() (string, bool) {
	if !cluster.Conf.BackupResticAws {
		return "", false
	}
	bucket := strings.TrimSpace(cluster.Conf.BackupResticAwsBucket)
	if bucket == "" {
		return "", false
	}
	_, appendCluster := resolveResticRepoPolicy(cluster.Conf, cluster.Conf.BackupResticLocalRepository, cluster)
	if !appendCluster {
		return "", false
	}
	currentPrefix := strings.Trim(cluster.Conf.BackupResticAwsPrefix, "/")
	if !shouldAppendClusterNameS3(bucket, currentPrefix, cluster.Name) {
		return "", false
	}
	_, effectivePrefix := buildResticS3RepoSpec(
		cluster.Conf.BackupResticAwsEndpoint,
		bucket,
		currentPrefix,
		cluster.Name,
		appendCluster,
	)
	if effectivePrefix == "" || effectivePrefix == currentPrefix {
		return "", false
	}
	return effectivePrefix, true
}

func isWithinParentPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == "" || child == "" {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err == nil {
		parent = parentAbs
	}
	childAbs, err := filepath.Abs(child)
	if err == nil {
		child = childAbs
	}
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}

func resolveResticRepoPolicy(conf *config.Config, localRepoPath string, cluster *Cluster) (string, bool) {
	appendCluster := conf.BackupResticRepoAppendCluster
	localRepoPath = strings.TrimSpace(localRepoPath)
	defaultParent := filepath.Clean(filepath.Join(conf.WorkingDir, config.ConstStreamingSubDir, "archive"))
	if localRepoPath != "" && isWithinParentPath(defaultParent, localRepoPath) {
		localRepoPath = ""
	}
	if !appendCluster && localRepoPath == "" {
		if conf.BackupResticAws {
			return localRepoPath, appendCluster
		}
		if cluster != nil {
			cluster.LogModulePrintf(conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"backup-restic-repo-append-cluster=false ignored: backup-restic-local-repository is empty or invalid")
		}
		appendCluster = true
	}
	return localRepoPath, appendCluster
}

func splitResticAdditionalEnvTokens(value string) ([]string, error) {
	parts := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false
	hadUnmatched := false
	hadInvalid := false
	seenEquals := false
	justClosedQuote := false

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
				justClosedQuote = true
			}
			current.WriteRune(r)
			continue
		}

		if justClosedQuote {
			switch r {
			case ',', ' ', '\t', '\n', '\r':
				// ok: delimiter after quoted token
			case '=':
				if seenEquals {
					hadInvalid = true
				}
			default:
				hadInvalid = true
			}
			justClosedQuote = false
			if hadInvalid {
				break
			}
		}

		switch r {
		case '"', '\'':
			quote = r
			current.WriteRune(r)
		case ',', ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
				seenEquals = false
			}
		case '=':
			seenEquals = true
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}

	if hadInvalid {
		return nil, errors.New("restic additional env has invalid characters after quoted value")
	}
	if quote != 0 {
		hadUnmatched = true
	}
	if hadUnmatched {
		return nil, errors.New("restic additional env has unmatched quotes")
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts, nil
}

func parseResticAdditionalEnvOverrides(raw string) (map[string]string, map[string]struct{}, error) {
	overrides := make(map[string]string)
	allowlist := make(map[string]struct{})

	parts, err := splitResticAdditionalEnvTokens(raw)
	if err != nil {
		return overrides, allowlist, err
	}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		key, value, hasValue := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if unquotedKey, ok := unquoteResticTagLiteral(key); ok {
			key = strings.TrimSpace(unquotedKey)
		}
		if key == "" {
			continue
		}
		allowlist[key] = struct{}{}
		if hasValue {
			value = strings.TrimSpace(value)
			if unquotedValue, ok := unquoteResticTagLiteral(value); ok {
				value = unquotedValue
			}
			overrides[key] = value
		}
	}

	return overrides, allowlist, nil
}

func ValidateResticAdditionalEnvOverrides(raw string) error {
	_, _, err := parseResticAdditionalEnvOverrides(raw)
	return err
}

func filterResticEnv(cluster *Cluster, baseEnv []string, repoPath, password, cacheDir, awsAccessKey, awsSecretKey, awsRegion, additionalEnv string) []string {
	filtered := make([]string, 0, len(baseEnv)+6)
	overrides, allowlist, err := parseResticAdditionalEnvOverrides(additionalEnv)
	if err != nil {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring restic additional env override: %s", err)
		}
		overrides = make(map[string]string)
		allowlist = make(map[string]struct{})
	}
	isS3 := config.IsS3ResticRepository(repoPath)
	defaultRegion := ""
	optionalAwsEnv := make(map[string]string)
	// Reserved env vars cannot be overridden via backup-restic-additional-env.
	// Add any new sensitive env vars here to prevent unintended injection.
	reserved := map[string]struct{}{
		"RESTIC_REPOSITORY":     {},
		"RESTIC_PASSWORD":       {},
		"RESTIC_CACHE_DIR":      {},
		"AWS_ACCESS_KEY_ID":     {},
		"AWS_SECRET_ACCESS_KEY": {},
	}
	for _, env := range baseEnv {
		key, val, hasValue := strings.Cut(env, "=")
		if !hasValue {
			continue
		}
		if key == "AWS_DEFAULT_REGION" {
			defaultRegion = val
		}
		if _, ok := allowlist[key]; ok {
			if _, blocked := reserved[key]; blocked {
				continue
			}
			if strings.HasPrefix(key, "AWS_") && !isS3 {
				continue
			}
			// AWS_DEFAULT_REGION from additional env is ignored for S3 repos; base env is used as fallback.
			if key == "AWS_DEFAULT_REGION" && isS3 {
				continue
			}
			if override, exists := overrides[key]; exists {
				optionalAwsEnv[key] = override
			} else {
				optionalAwsEnv[key] = val
			}
			if key == "AWS_DEFAULT_REGION" && !isS3 {
				defaultRegion = optionalAwsEnv[key]
			}
			continue
		}
		if strings.HasPrefix(key, "RESTIC_") || strings.HasPrefix(key, "AWS_") {
			continue
		}
		filtered = append(filtered, env)
	}

	for key, override := range overrides {
		if _, blocked := reserved[key]; blocked {
			continue
		}
		if strings.HasPrefix(key, "AWS_") && !isS3 {
			continue
		}
		if key == "AWS_DEFAULT_REGION" && isS3 {
			continue
		}
		if _, exists := optionalAwsEnv[key]; exists {
			continue
		}
		optionalAwsEnv[key] = override
		if key == "AWS_DEFAULT_REGION" && !isS3 {
			defaultRegion = override
		}
	}

	filtered = append(filtered, "RESTIC_PASSWORD="+password)
	filtered = append(filtered, "RESTIC_CACHE_DIR="+cacheDir)
	filtered = append(filtered, "RESTIC_REPOSITORY="+repoPath)

	if isS3 {
		filtered = append(filtered, "AWS_ACCESS_KEY_ID="+awsAccessKey)
		filtered = append(filtered, "AWS_SECRET_ACCESS_KEY="+awsSecretKey)
		region := strings.TrimSpace(awsRegion)
		if region == "" {
			region = strings.TrimSpace(defaultRegion)
		}
		if region != "" {
			filtered = append(filtered, "AWS_DEFAULT_REGION="+region)
		}
	}

	for key, value := range optionalAwsEnv {
		if _, blocked := reserved[key]; blocked {
			continue
		}
		if key == "AWS_DEFAULT_REGION" && isS3 {
			continue
		}
		filtered = append(filtered, key+"="+value)
	}

	return filtered
}

func (cluster *Cluster) ResticGetEnv() []string {
	cacheDir := cluster.Conf.WorkingDir + "/" + cluster.Name + "/.cache/restic"
	password := cluster.Conf.GetDecryptedValue("backup-restic-password")
	repoPath := ""
	// backup-restic-aws controls whether the repo path is remote; otherwise local repo is used.
	localRepoPath, appendCluster := resolveResticRepoPolicy(cluster.Conf, cluster.Conf.BackupResticLocalRepository, cluster)
	if cluster.Conf.BackupResticAws {
		if strings.TrimSpace(cluster.Conf.BackupResticAwsBucket) != "" {
			repoPath, _ = buildResticS3RepoSpec(
				cluster.Conf.BackupResticAwsEndpoint,
				cluster.Conf.BackupResticAwsBucket,
				cluster.Conf.BackupResticAwsPrefix,
				cluster.Name,
				appendCluster,
			)
		} else {
			repoPath = cluster.Conf.BackupResticRepository
			if appendCluster {
				repoPath = repoPath + "/" + cluster.Name
			}
		}
	} else {
		if localRepoPath != "" {
			repoPath = localRepoPath
			if appendCluster && shouldAppendClusterNameLocal(repoPath, cluster.Name) {
				repoPath = filepath.Join(repoPath, cluster.Name)
			}
		} else {
			repoPath = filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, "archive", cluster.Name)
		}
		if !config.IsSftpResticRepository(repoPath) {
			if _, err := os.Stat(repoPath); os.IsNotExist(err) {
				err := os.MkdirAll(repoPath, os.ModePerm)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Create archive directory failed: %s,%s", repoPath, err)
				}
			}
		}
	}

	return filterResticEnv(
		cluster,
		os.Environ(),
		repoPath,
		password,
		cacheDir,
		cluster.Conf.BackupResticAwsAccessKeyId,
		cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"),
		cluster.Conf.BackupResticAwsRegion,
		cluster.Conf.BackupResticAdditionalEnv,
	)
}

func (cluster *Cluster) ReloadResticEnv() {
	if cluster.ResticManager != nil {
		cluster.ResticManager.SetEnv(cluster.ResticGetEnv())
		bucket := ""
		prefix := ""
		endpoint := ""
		if cluster.Conf.BackupResticAws && strings.TrimSpace(cluster.Conf.BackupResticAwsBucket) != "" {
			_, appendCluster := resolveResticRepoPolicy(cluster.Conf, cluster.Conf.BackupResticLocalRepository, cluster)
			_, prefix = buildResticS3RepoSpec(
				cluster.Conf.BackupResticAwsEndpoint,
				cluster.Conf.BackupResticAwsBucket,
				cluster.Conf.BackupResticAwsPrefix,
				cluster.Name,
				appendCluster,
			)
			bucket = strings.TrimSpace(cluster.Conf.BackupResticAwsBucket)
			endpoint = strings.TrimSpace(cluster.Conf.BackupResticAwsEndpoint)
		}

		cluster.ResticManager.SetAwsConfig(
			cluster.Conf.BackupResticAwsAccessKeyId,
			cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"),
			cluster.Conf.BackupResticAwsRegion,
			endpoint,
			bucket,
			prefix,
		)
		// Clear init error backoff when environment changes (credentials/config may be fixed)
		cluster.ResticManager.ClearInitErrorBackoffManual()
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
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic init error: %s", err)
		}
		return
	}

	for task, err := range cluster.ResticManager.FetchAndClearErrors() {
		switch task {
		case backupmgr.FetchTask:
			cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic fetch error: %s", err)
		case backupmgr.PurgeTask:
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic purge error: %s", err)
		case backupmgr.UnlockTask:
			cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic unlock error: %s", err)
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
	resticManager.AddUnlockTask() // Queue unlock first to avoid fetch-before-unlock race on startup.
	go cluster.ResticFetchRepo()
	return nil
}

func (cluster *Cluster) ResticInitRepo(force bool) error {
	return cluster.ResticInitRepoWithOptions(backupmgr.ResticInitOption{Force: force})
}

func (cluster *Cluster) ResticInitRepoWithOptions(options backupmgr.ResticInitOption) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	err := cluster.ResticManager.InitRepoWithOptions(options)
	if err != nil {
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
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
	if !cluster.IsProvisioned() {
		return fmt.Errorf("cluster is not provisioned")
	}

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

func parseResticMountPathTemplates(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	templates := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		templates = append(templates, candidate)
	}
	if len(templates) == 0 {
		return nil
	}
	return templates
}

type resticMountDirResolveOptions struct {
	requireAbs         bool
	rejectDotDot       bool
	enforceDefaultBase bool
	logSanitize        bool
}

const resticDefaultMountSubdir = "mount"

type resticMountOptionMeta struct {
	mountDirSource  string
	targetDirSource string
}

func hasDotDotComponent(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}

func (cluster *Cluster) sanitizeResticMountDir(label, raw string, opts resticMountDirResolveOptions) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s mount dir is empty", label)
	}
	if opts.rejectDotDot && hasDotDotComponent(trimmed) {
		return "", fmt.Errorf("%s mount dir contains '..' component: %s", label, trimmed)
	}
	cleaned := filepath.Clean(trimmed)
	if opts.requireAbs && !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%s mount dir must be absolute: %s", label, cleaned)
	}
	if cleaned != trimmed && opts.logSanitize && cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Sanitized restic mount dir (%s): %s -> %s", label, trimmed, cleaned)
	}
	return cleaned, nil
}

func (cluster *Cluster) resolveResticMountDirFromConfig(opts resticMountDirResolveOptions) (string, string, error) {
	if cluster == nil || cluster.Conf == nil {
		return "", "", fmt.Errorf("cluster config is nil")
	}

	baseDir := filepath.Join(cluster.WorkingDir, resticDefaultMountSubdir) // cluster.WorkingDir already has cluster name
	mountDir := baseDir
	mountDirSource := "default"
	if trimmed := strings.TrimSpace(cluster.Conf.BackupResticMountDir); trimmed != "" {
		mountDirSource = "config"
		if opts.rejectDotDot && hasDotDotComponent(trimmed) {
			return "", mountDirSource, fmt.Errorf("%s mount dir contains '..' component: %s", mountDirSource, trimmed)
		}
		if filepath.IsAbs(trimmed) {
			mountDir = trimmed
		} else {
			mountDir = filepath.Join(baseDir, trimmed)
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Resolved relative restic mount dir from config: %s -> %s (base %s)", trimmed, mountDir, baseDir)
		}
	}

	var err error
	mountDir, err = cluster.sanitizeResticMountDir(mountDirSource, mountDir, opts)
	if err != nil {
		return "", mountDirSource, err
	}
	if opts.enforceDefaultBase && mountDirSource == "default" {
		base := filepath.Clean(filepath.Join(cluster.WorkingDir, resticDefaultMountSubdir))
		// filepath.Rel does not resolve symlinks; callers should avoid untrusted bases.
		rel, relErr := filepath.Rel(base, mountDir)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			mountErr := fmt.Errorf("default mount dir %s escapes base %s", mountDir, base)
			if relErr != nil {
				mountErr = fmt.Errorf("failed to resolve default mount dir %s under base %s: %w", mountDir, base, relErr)
			}
			return "", mountDirSource, mountErr
		}
	}

	return mountDir, mountDirSource, nil
}

// ResolveResticMountDirFromConfig returns the configured restic mount directory and source.
func (cluster *Cluster) ResolveResticMountDirFromConfig() (string, string, error) {
	return cluster.resolveResticMountDirFromConfig(resticMountDirResolveOptions{
		requireAbs:         false,
		rejectDotDot:       false,
		enforceDefaultBase: false,
		logSanitize:        false,
	})
}

// ResolveResticMountDirFromConfigStrict enforces extra safety for API usage.
func (cluster *Cluster) ResolveResticMountDirFromConfigStrict() (string, string, error) {
	return cluster.resolveResticMountDirFromConfig(resticMountDirResolveOptions{
		requireAbs:         false,
		rejectDotDot:       true,
		enforceDefaultBase: true,
		logSanitize:        true,
	})
}

func resticLogSnapshotID(cluster *Cluster, snapshotID string) string {
	trimmed := strings.TrimSpace(snapshotID)
	if trimmed == "" {
		return ""
	}
	if trimmed == "latest" {
		return trimmed
	}
	if cluster != nil && cluster.ResticManager != nil {
		snap := cluster.ResticManager.GetSnapshot(trimmed)
		if snap != nil {
			shortID := strings.TrimSpace(snap.ShortId)
			if shortID != "" {
				return shortID
			}
		}
	}
	if len(trimmed) > 8 {
		return trimmed[:8]
	}
	return trimmed
}

// getSnapshotSizeBytes returns the snapshot size (in bytes) when backup metadata is available.
func getSnapshotSizeBytes(cluster *Cluster, snapshotID string) (uint64, bool) {
	if cluster == nil || cluster.BackupMetaMap == nil || strings.TrimSpace(snapshotID) == "" {
		return 0, false
	}
	var selected *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(_, value any) bool {
		meta, ok := value.(*backupmgr.BackupMetadata)
		if !ok || meta == nil {
			return true
		}
		if strings.TrimSpace(meta.ResticSnapshotID) != snapshotID {
			return true
		}
		if selected == nil {
			selected = meta
			return true
		}
		if selected.EndTime.IsZero() && !meta.EndTime.IsZero() {
			selected = meta
			return true
		}
		if meta.EndTime.After(selected.EndTime) {
			selected = meta
			return true
		}
		if meta.EndTime.Equal(selected.EndTime) && meta.StartTime.After(selected.StartTime) {
			selected = meta
		}
		return true
	})
	if selected == nil || selected.Size <= 0 {
		return 0, false
	}
	return uint64(selected.Size), true
}

func (cluster *Cluster) parseResticMountOptionsFromConfig() (backupmgr.ResticMountOption, resticMountOptionMeta, error) {
	var mountOpt backupmgr.ResticMountOption
	var meta resticMountOptionMeta
	if cluster == nil || cluster.Conf == nil {
		return mountOpt, meta, fmt.Errorf("cluster config is nil")
	}

	mountDir, mountDirSource, err := cluster.resolveResticMountDirFromConfig(resticMountDirResolveOptions{
		requireAbs:         false,
		rejectDotDot:       false,
		enforceDefaultBase: false,
		logSanitize:        false,
	})
	if err != nil {
		return mountOpt, meta, err
	}
	meta.mountDirSource = mountDirSource

	targetDir := strings.TrimSpace(cluster.Conf.BackupResticMountTargetDir)
	targetDirSource := "default"
	if targetDir == "" {
		targetDir = mountDir
	} else {
		targetDirSource = "config"
	}
	meta.targetDirSource = targetDirSource

	mountOpt = backupmgr.NewResticMountOption(targetDir)
	mountOpt.AllowOther = cluster.Conf.BackupResticMountAllowOther
	mountOpt.NoDefaultPermissions = cluster.Conf.BackupResticMountNoDefaultPermissions
	mountOpt.OwnerRoot = cluster.Conf.BackupResticMountOwnerRoot
	mountOpt.NoLock = cluster.Conf.BackupResticMountNoLock
	mountOpt.Verbose = cluster.Conf.BackupResticMountVerbose
	mountOpt.Quiet = cluster.Conf.BackupResticMountQuiet
	mountOpt.Host = splitResticPurgeFilterValues(cluster.Conf.BackupResticMountHost)
	mountOpt.Tag = parseResticTagFilterValues(cluster.Conf.BackupResticMountTag, cluster, "mount")
	mountOpt.Path = filterResticAbsolutePaths(splitResticPurgeFilterValues(cluster.Conf.BackupResticMountPath), cluster)
	if templates := parseResticMountPathTemplates(cluster.Conf.BackupResticMountPathTemplate); len(templates) > 0 {
		mountOpt.PathTemplate = templates
	}
	if timeTemplate := strings.TrimSpace(cluster.Conf.BackupResticMountTimeTemplate); timeTemplate != "" {
		mountOpt.TimeTemplate = timeTemplate
	}

	return mountOpt, meta, nil
}

func (cluster *Cluster) sanitizeAndValidateResticMountOptions(mountOpt *backupmgr.ResticMountOption, meta resticMountOptionMeta) error {
	if cluster == nil || mountOpt == nil {
		return fmt.Errorf("restic mount options are nil")
	}
	if mountOpt.TargetDir == "" {
		return fmt.Errorf("restic mount target dir is empty")
	}

	cleaned := filepath.Clean(mountOpt.TargetDir)
	if cleaned != mountOpt.TargetDir {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Sanitized restic mount target dir (%s): %s -> %s", meta.targetDirSource, mountOpt.TargetDir, cleaned)
		mountOpt.TargetDir = cleaned
	}
	if !filepath.IsAbs(mountOpt.TargetDir) {
		return fmt.Errorf("restic mount target dir must be absolute: %s", mountOpt.TargetDir)
	}
	if meta.targetDirSource == "default" && meta.mountDirSource == "default" {
		base := filepath.Clean(filepath.Join(cluster.WorkingDir, resticDefaultMountSubdir))
		rel, relErr := filepath.Rel(base, mountOpt.TargetDir)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			mountErr := fmt.Errorf("default restic mount dir %s escapes base %s", mountOpt.TargetDir, base)
			if relErr != nil {
				mountErr = fmt.Errorf("failed to resolve default restic mount dir %s under base %s: %w", mountOpt.TargetDir, base, relErr)
			}
			return mountErr
		}
	}

	if err := mountOpt.Validate(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Invalid restic mount options: %s", err)
		return err
	}

	return nil
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

	if prefix, suffix, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(suffix) == "" {
		key := normalizeResticTagCategory(prefix)
		if key != "" {
			if _, ok := resticTagTemplateKeySet[key]; ok {
				value := strings.TrimSpace(values[key])
				if value == "" {
					return "", false
				}
				return key + ":" + value, true
			}
		}
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
			return value, true
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

	if prefix, suffix, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(suffix) == "" {
		key := normalizeResticTagCategory(prefix)
		if key != "" {
			if _, ok := resticKeepTagTemplateKeySet[key]; ok {
				value := strings.TrimSpace(values[key])
				if value == "" {
					return "", false
				}
				return key + ":" + value, true
			}
		}
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
	fsPath, err := resolveDiskFilesystem(dirpath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error resolving disk filesystem for dir %s: %s", dirpath, err)
		return err
	}

	diskstat, err := getDiskUsage(fsPath.Mountpoint)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	if diskstat == nil {
		err := fmt.Errorf("disk usage is nil for %s", dirpath)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	diskstat.Path = fsPath.Mountpoint
	cluster.DiskStatManager.UpdateStat(fsPath.Key, diskstat)

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

func (cluster *Cluster) normalizeSplitDumpOutputDir(destination *ServerMonitor, outputDir string) (string, error) {
	if destination == nil {
		return "", fmt.Errorf("splitdump destination is nil")
	}

	trimmedOutputDir := strings.TrimSpace(outputDir)
	if trimmedOutputDir == "" {
		trimmedOutputDir = filepath.Join(destination.GetMyBackupDirectory(), "splitdump")
	}

	baseDir := filepath.Clean(destination.GetMyBackupDirectory())
	baseDirAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve backup directory %s: %w", baseDir, err)
	}

	cleanOutputDir := filepath.Clean(trimmedOutputDir)
	if cleanOutputDir == "." || cleanOutputDir == string(filepath.Separator) {
		return "", fmt.Errorf("invalid splitdump output dir: %s", outputDir)
	}
	if !filepath.IsAbs(cleanOutputDir) {
		cleanOutputDir = filepath.Join(baseDirAbs, cleanOutputDir)
	}
	outputDirAbs, err := filepath.Abs(cleanOutputDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve splitdump output dir %s: %w", cleanOutputDir, err)
	}
	if outputDirAbs == baseDirAbs {
		return "", fmt.Errorf("splitdump output dir must be a subdirectory of %s", baseDirAbs)
	}

	defaultOutputDir := filepath.Join(baseDirAbs, "splitdump")
	outputBase := filepath.Base(outputDirAbs)
	if outputDirAbs != defaultOutputDir {
		if !strings.HasPrefix(outputBase, "splitdump.") {
			return "", fmt.Errorf("splitdump output dir must be %s or %s.<timestamp>", defaultOutputDir, defaultOutputDir)
		}
		suffix := strings.TrimPrefix(outputBase, "splitdump.")
		if suffix == "" || !splitDumpTimestampRegex.MatchString(suffix) {
			return "", fmt.Errorf("splitdump output dir must be %s or %s.<timestamp>", defaultOutputDir, defaultOutputDir)
		}
	}

	resolvedBaseDir, err := filepath.EvalSymlinks(baseDirAbs)
	if err != nil {
		if os.IsNotExist(err) {
			// Allow missing base dir; it may be created later during splitdump.
			parentDir := filepath.Dir(baseDirAbs)
			resolvedParentDir, parentErr := filepath.EvalSymlinks(parentDir)
			if parentErr != nil {
				return "", fmt.Errorf("failed to resolve backup directory parent %s: %w", parentDir, parentErr)
			}
			resolvedBaseDir = filepath.Join(resolvedParentDir, filepath.Base(baseDirAbs))
		} else {
			return "", fmt.Errorf("failed to resolve backup directory %s: %w", baseDirAbs, err)
		}
	}
	resolvedOutputDir, err := filepath.EvalSymlinks(outputDirAbs)
	if err != nil {
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(outputDirAbs)
			resolvedParentDir, parentErr := filepath.EvalSymlinks(parentDir)
			if parentErr != nil {
				return "", fmt.Errorf("failed to resolve splitdump output dir parent %s: %w", parentDir, parentErr)
			}
			resolvedOutputDir = filepath.Join(resolvedParentDir, filepath.Base(outputDirAbs))
		} else {
			return "", fmt.Errorf("failed to resolve splitdump output dir %s: %w", outputDirAbs, err)
		}
	}
	rel, err := filepath.Rel(resolvedBaseDir, resolvedOutputDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve splitdump output dir %s relative to %s: %w", outputDirAbs, baseDirAbs, err)
	}
	if rel == "." {
		return "", fmt.Errorf("splitdump output dir must be a subdirectory of %s", baseDirAbs)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("splitdump output dir %s is outside backup directory %s", outputDirAbs, baseDirAbs)
	}

	return outputDirAbs, nil
}

func (cluster *Cluster) SplitDumpWithCli(ctx context.Context, destination *ServerMonitor, outputDir string, allowRotate bool, stdin io.Reader, stdout, stderr io.Writer) error {
	if cluster == nil || cluster.Conf == nil {
		return fmt.Errorf("cluster config is nil")
	}
	if destination == nil {
		return fmt.Errorf("splitdump destination is nil")
	}
	if stdin == nil {
		return fmt.Errorf("splitdump stdin is nil")
	}
	if stdout == nil {
		return fmt.Errorf("splitdump stdout is nil")
	}
	if stderr == nil {
		return fmt.Errorf("splitdump stderr is nil")
	}
	resolvedOutputDir, err := cluster.normalizeSplitDumpOutputDir(destination, outputDir)
	if err != nil {
		return err
	}
	outputDir = resolvedOutputDir

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Starting splitdump for server %s to %s", destination.URL, outputDir)

	// Get CLI path and verify it's executable
	cliPath := cluster.GetReplicationManagerCliPath()
	if resolvedPath, err := exec.LookPath(cliPath); err == nil {
		cliPath = resolvedPath
	}
	if info, err := os.Stat(cliPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"replication-manager-cli not found at %s: %v", cliPath, err)
		return fmt.Errorf("replication-manager-cli not found at %s: %w", cliPath, err)
	} else if info.Mode().Perm()&0111 == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"replication-manager-cli is not executable: %s", cliPath)
		return fmt.Errorf("replication-manager-cli is not executable: %s", cliPath)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
		"Using replication-manager-cli at %s", cliPath)

	info, err := os.Stat(outputDir)
	switch {
	case os.IsNotExist(err):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Creating splitdump output directory %s", outputDir)
		if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create splitdump output dir %s: %w", outputDir, err)
		}
	case err != nil:
		return fmt.Errorf("failed to stat splitdump output dir %s: %w", outputDir, err)
	case !info.IsDir():
		return fmt.Errorf("splitdump output path is not a directory: %s", outputDir)
	default:
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			return fmt.Errorf("failed to read splitdump output dir %s: %w", outputDir, err)
		}
		if len(entries) > 0 {
			if allowRotate {
				rotatedDir := fmt.Sprintf("%s.old.%d", outputDir, time.Now().UnixNano())
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
					"Rotating existing splitdump directory to %s", rotatedDir)
				if err := os.Rename(outputDir, rotatedDir); err != nil {
					return fmt.Errorf("failed to rotate splitdump output dir %s to %s: %w", outputDir, rotatedDir, err)
				}
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
					"Removing existing splitdump directory %s before backup", outputDir)
				if err := os.RemoveAll(outputDir); err != nil {
					return fmt.Errorf("failed to remove splitdump output dir %s: %w", outputDir, err)
				}
			}
			if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
				return fmt.Errorf("failed to recreate splitdump output dir %s: %w", outputDir, err)
			}
		}
	}

	// Use parent context for cancellation control
	// If caller wants a timeout, they should pass context.WithTimeout
	args := []string{"splitdump", "--outputdir", outputDir}
	trimmedFileSize := strings.TrimSpace(cluster.Conf.BackupSplitdumpFileSize)
	if trimmedFileSize != "" {
		if sizeBytes, err := splitdump.ParseSizeBytes(trimmedFileSize); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Invalid backup-splitdump-file-size %q, using default sharding: %v", trimmedFileSize, err)
		} else {
			args = append(args, "--stream-size-max", trimmedFileSize)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Splitdump file size set to %q (%d bytes)", trimmedFileSize, sizeBytes)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
		"Executing splitdump command: %s %s", cliPath, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"Splitdump cancelled due to timeout or cancellation")
			return fmt.Errorf("splitdump cancelled: %w", err)
		} else if errors.Is(err, context.Canceled) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump cancelled by parent context")
			return fmt.Errorf("splitdump cancelled: %w", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Splitdump command failed: %v", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Splitdump completed successfully for server %s", destination.URL)
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

// getSanitizedDecompressBufferSize returns a safe pgzip decompression block size.
// If the configured value is <= 0, it falls back to SSTSendBuffer, then 250000.
func (cluster *Cluster) getSanitizedDecompressBufferSize(logModule int) int {
	blockSize := cluster.Conf.CompressBackupsDecompressBufferSize
	if blockSize > 0 {
		return blockSize
	}
	if cluster.Conf.SSTSendBuffer > 0 {
		return cluster.Conf.SSTSendBuffer
	}
	return 250000
}
