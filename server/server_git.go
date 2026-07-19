package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/peer"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/meethelper"
	"github.com/signal18/replication-manager/utils/misc"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// gitImportNetworkTimeout bounds the staging clone the same way
// config.gitNetworkTimeout bounds every other go-git network operation in
// this codebase: without it a hung remote holds runtimeClusterStartMu forever.
const gitImportNetworkTimeout = 120 * time.Second

// DynamicClusterImportResult is the structured, always-200 result of
// FetchDynamicClustersFromGit: some clusters may import while others fail
// or are skipped, and the caller reports all of it in one payload.
type DynamicClusterImportResult struct {
	Imported        []string          `json:"imported"`
	SkippedExisting []string          `json:"skipped_existing"`
	Invalid         []string          `json:"invalid"`
	Errors          map[string]string `json:"errors"`
}

// nonDynamicClusterDirs lists staged main-config-repo root directories that
// are never dynamic cluster directories, even though they live at repo root
// alongside real cluster directories.
var nonDynamicClusterDirs = map[string]bool{
	".git":     true,
	".pull":    true,
	".tmp":     true,
	"plugins":  true,
	"graphite": true,
	"backups":  true,
}

// FetchDynamicClustersFromGit clones the main config git repo
// (repman.Conf.GitUrl) into a temporary staging directory and imports, live
// and without a restart, any dynamic cluster directory found there that is
// not already known to this instance. Manual, admin-triggered, missing-only,
// never overwrites an existing local cluster. See
// doc/implementation/server/DYNAMIC_CLUSTER_GIT_IMPORT_PLAN.md.
func (repman *ReplicationManager) FetchDynamicClustersFromGit() (*DynamicClusterImportResult, error) {
	if repman.Conf.GitUrl == "" {
		return nil, fmt.Errorf("git URL is not configured")
	}
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	if tok == "" {
		return nil, fmt.Errorf("git access token is not configured")
	}

	repman.runtimeClusterStartMu.Lock()
	defer repman.runtimeClusterStartMu.Unlock()

	stagedDir, err := repman.stageMainConfigRepoForImport(tok)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stagedDir)

	return repman.importStagedDynamicClusters(stagedDir)
}

// importStagedDynamicClusters discovers and imports dynamic clusters from an
// already-staged main config repo checkout. Split out from
// FetchDynamicClustersFromGit purely so the import/rollback loop can be
// exercised in tests against a plain local directory fixture, without a real
// git remote. Callers must already hold runtimeClusterStartMu.
func (repman *ReplicationManager) importStagedDynamicClusters(stagedDir string) (*DynamicClusterImportResult, error) {
	importable, invalid, err := repman.discoverImportableDynamicClusterDirs(stagedDir)
	if err != nil {
		return nil, err
	}

	result := &DynamicClusterImportResult{
		Imported:        []string{},
		SkippedExisting: []string{},
		Invalid:         invalid,
		Errors:          map[string]string{},
	}

	for _, name := range importable {
		if repman.hasLocalCluster(name) {
			result.SkippedExisting = append(result.SkippedExisting, name)
			continue
		}

		if err := repman.importDynamicClusterDir(stagedDir, name); err != nil {
			result.Errors[name] = err.Error()
			continue
		}

		if err := repman.loadAndStartImportedCluster(stagedDir, name); err != nil {
			if rbErr := repman.rollbackFailedImport(name); rbErr != nil {
				result.Errors[name] = fmt.Sprintf("%v; %v", err, rbErr)
			} else {
				result.Errors[name] = err.Error()
			}
			continue
		}

		result.Imported = append(result.Imported, name)
	}

	return result, nil
}

// rollbackFailedImport undoes a partially-completed import so a retry is not
// permanently blocked. Without this, a failure inside
// loadAndStartImportedCluster (e.g. a malformed staged TOML file) would leave
// the copied directory on disk; the next call's hasLocalCluster would
// then see that directory and report the cluster as skipped_existing forever,
// even though it never actually started. Only called after
// importDynamicClusterDir has already copied files into the live working
// directory.
//
// Returns an error if removing the directory itself fails (e.g. a
// permission problem) — that case is still best-effort (nothing can force a
// filesystem to allow deletion), but the caller must surface it rather than
// silently swallow it: a leftover directory after a failed RemoveAll would
// otherwise poison retries exactly the way this function exists to prevent,
// except now with no error ever reported to the operator.
func (repman *ReplicationManager) rollbackFailedImport(name string) error {
	repman.Lock()
	delete(repman.Clusters, name)
	delete(repman.Confs, name)
	delete(repman.VersionConfs, name)
	delete(repman.ImmuableFlagMaps, name)
	delete(repman.DynamicFlagMaps, name)
	for i, n := range repman.ClusterList {
		if n == name {
			repman.ClusterList = append(repman.ClusterList[:i], repman.ClusterList[i+1:]...)
			break
		}
	}
	repman.Unlock()

	dst := filepath.Join(repman.Conf.WorkingDir, name)
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("in-memory registration rolled back, but cleanup of %s failed: %w — manual removal required before retry", dst, err)
	}
	return nil
}

// hasLocalCluster reports whether name is already running or already
// has a persisted directory in the live working directory — either makes it
// ineligible for import (see Skip policy in the import plan).
func (repman *ReplicationManager) hasLocalCluster(name string) bool {
	repman.Lock()
	_, running := repman.Clusters[name]
	repman.Unlock()
	if running {
		return true
	}
	_, err := os.Stat(filepath.Join(repman.Conf.WorkingDir, name))
	return err == nil
}

// stageMainConfigRepoForImport clones the main config repo into a fresh temp
// directory under working-dir/.tmp so it can be inspected without touching
// the live working directory. The caller must remove the returned directory.
func (repman *ReplicationManager) stageMainConfigRepoForImport(tok string) (string, error) {
	tmpRoot := filepath.Join(repman.Conf.WorkingDir, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return "", fmt.Errorf("cannot create staging root %s: %w", tmpRoot, err)
	}

	stagedDir, err := os.MkdirTemp(tmpRoot, "git-import-*")
	if err != nil {
		return "", fmt.Errorf("cannot create staging directory: %w", err)
	}

	cloneopt := &git.CloneOptions{
		URL:               repman.Conf.GitUrl,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth: &git_https.BasicAuth{
			Username: repman.Conf.GitUsername,
			Password: tok,
		},
		Depth: 1,
	}

	// Stage from the branch this instance actually tracks, not necessarily
	// the remote's default branch, so an instance running on a non-default
	// branch imports from the same source it pushes to. Mirrors
	// ConfigManager.RefreshGitMetadata's branch resolution (same fallback
	// chain: current local branch -> master -> remote default on retry).
	if refName, ok := manager.ResolveCurrentLocalBranch(repman.Conf.WorkingDir); ok {
		cloneopt.ReferenceName = refName
		cloneopt.SingleBranch = true
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Staging dynamic cluster import using current local branch reference %s", refName)
	} else {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Could not determine current local branch for dynamic cluster import; falling back to remote default branch")
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitImportNetworkTimeout)
	defer cancel()

	if _, cloneErr := git.PlainCloneContext(ctx, stagedDir, false, cloneopt); cloneErr != nil {
		if cloneopt.ReferenceName != "" && manager.IsReferenceNotFoundError(cloneErr) {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Staging clone with reference %s failed (%v); retrying with remote default branch", cloneopt.ReferenceName, cloneErr)
			if err := os.RemoveAll(stagedDir); err != nil {
				return "", fmt.Errorf("cannot reset staging directory before retry: %w", err)
			}
			if err := os.MkdirAll(stagedDir, 0755); err != nil {
				return "", fmt.Errorf("cannot recreate staging directory before retry: %w", err)
			}
			cloneopt.ReferenceName = ""
			cloneopt.SingleBranch = false
			if _, retryErr := git.PlainCloneContext(ctx, stagedDir, false, cloneopt); retryErr != nil {
				os.RemoveAll(stagedDir)
				return "", fmt.Errorf("cannot stage main config repo: %w", retryErr)
			}
		} else {
			os.RemoveAll(stagedDir)
			return "", fmt.Errorf("cannot stage main config repo: %w", cloneErr)
		}
	}

	return stagedDir, nil
}

// discoverImportableDynamicClusterDirs inspects the staged repo root and
// classifies each directory as importable (contains <name>/<name>.toml) or
// invalid (anything else that is not a known non-cluster directory).
func (repman *ReplicationManager) discoverImportableDynamicClusterDirs(stagedDir string) (importable []string, invalid []string, err error) {
	entries, err := os.ReadDir(stagedDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read staged repo %s: %w", stagedDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if nonDynamicClusterDirs[name] {
			continue
		}

		tomlPath := filepath.Join(stagedDir, name, name+".toml")
		if _, statErr := os.Stat(tomlPath); statErr == nil {
			importable = append(importable, name)
		} else {
			invalid = append(invalid, name)
		}
	}

	return importable, invalid, nil
}

// importDynamicClusterDir copies a staged cluster directory into the live
// working directory. Callers must have already confirmed via
// hasLocalCluster that the destination does not exist — this feature
// never overwrites an existing local cluster.
//
// The existence check is repeated here (rather than trusting the caller)
// because it draws the line that makes cleanup on failure safe: CopyDir only
// starts writing into dst after confirming dst does not exist, so once that
// is true here too, any later CopyDir failure necessarily means a partial
// copy that this call itself created — never a pre-existing directory — and
// is therefore always safe to remove. Without this, a failure partway
// through the copy (disk full, permission error on one file) would leave a
// half-written directory behind, and the next retry's hasLocalCluster
// check would see it and skip the cluster as "existing" forever.
func (repman *ReplicationManager) importDynamicClusterDir(stagedDir, name string) error {
	src := filepath.Join(stagedDir, name)
	dst := filepath.Join(repman.Conf.WorkingDir, name)

	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot stat destination %s: %w", dst, err)
	}

	if err := misc.CopyDir(src, dst); err != nil {
		// CopyDir only starts writing into dst after confirming it does not
		// exist (checked above), so dst here is necessarily a partial copy
		// this call itself created and it is always safe to remove.
		// RemoveAll itself failing (e.g. a permission problem) is best-effort
		// — nothing can force a filesystem to allow deletion — but that
		// failure must be surfaced rather than swallowed, or a leftover
		// partial directory would poison hasLocalCluster on every retry with
		// no error ever visible to the operator.
		if rmErr := os.RemoveAll(dst); rmErr != nil {
			return fmt.Errorf("copy failed (%v) and cleanup of partial directory also failed: %w — manual removal of %s required before retry", err, rmErr, dst)
		}
		return err
	}

	return nil
}

// reconstructImportedClusterConfig builds the config.Config for a newly
// imported cluster from its now-live <name>/<name>.toml, using an isolated
// Viper reader — never the shared repman.ViperConfig.
//
// Saved cluster files are delta overlays, not standalone configs, so the
// staged repo's own default.toml (not this instance's) provides the
// reconstruction context: that is the default baseline the exporting
// instance computed its per-cluster delta against. Locally immutable keys
// (CLI flags, /etc config, env) are still protected from being overwritten,
// mirroring InitConfig's saved-default merge, so this instance's own
// server-scoped settings remain authoritative.
//
// Split out from loadAndStartImportedCluster (pure, no repman mutation other
// than the brief lock to read the default flag maps) so the reconstruction
// logic itself — the part unique to this feature — can be tested directly
// against a local directory fixture without going through StartCluster()'s
// much heavier cluster.Init() machinery.
func (repman *ReplicationManager) reconstructImportedClusterConfig(stagedDir, name string) (config.Config, error) {
	isolated := viper.New()
	isolated.SetConfigType("toml")

	stagedDefaultToml := filepath.Join(stagedDir, "default.toml")
	if _, err := os.Stat(stagedDefaultToml); err == nil {
		isolated.SetConfigFile(stagedDefaultToml)
		if err := isolated.MergeInConfig(); err != nil {
			return config.Config{}, fmt.Errorf("cannot parse staged default.toml: %w", err)
		}
	}

	clusterTomlPath := filepath.Join(repman.Conf.WorkingDir, name, name+".toml")
	isolated.SetConfigName(name)
	isolated.SetConfigFile(clusterTomlPath)
	if err := isolated.MergeInConfig(); err != nil {
		return config.Config{}, fmt.Errorf("cannot parse %s: %w", clusterTomlPath, err)
	}

	baseConf := *repman.Conf
	if cf3 := isolated.Sub("saved-default"); cf3 != nil {
		for _, f := range cf3.AllKeys() {
			if v, ok := repman.Conf.ImmuableFlagMap[f]; ok {
				cf3.Set(f, v)
			}
		}
		repman.initAlias(cf3)
		cf3.Unmarshal(&baseConf)
	}

	repman.Lock()
	defaultImmuable := repman.ImmuableFlagMaps["default"]
	defaultDynamic := repman.DynamicFlagMaps["default"]
	repman.Unlock()

	return repman.GetClusterConfig(isolated, defaultImmuable, defaultDynamic, name, baseConf), nil
}

// registerImportedCluster makes clusterConf visible to the rest of the
// server — ClusterList, Confs, VersionConfs — the same bookkeeping every
// StartCluster() caller performs beforehand (see AddCluster,
// PullCloud18Configs). Split out so tests can verify this "runtime
// visibility" step directly, independent of StartCluster()'s own
// cluster.Init() machinery.
func (repman *ReplicationManager) registerImportedCluster(name string, clusterConf config.Config) {
	repman.Lock()
	repman.ClusterList = append(repman.ClusterList, name)
	repman.Confs[name] = clusterConf
	repman.VersionConfs[name] = new(config.ConfVersion)
	repman.VersionConfs[name].ConfInit = clusterConf
	repman.Unlock()
}

// loadAndStartImportedCluster reconstructs the imported cluster's config,
// registers it, and starts it.
func (repman *ReplicationManager) loadAndStartImportedCluster(stagedDir, name string) error {
	clusterConf, err := repman.reconstructImportedClusterConfig(stagedDir, name)
	if err != nil {
		return err
	}

	repman.registerImportedCluster(name, clusterConf)

	if _, err := repman.StartCluster(name); err != nil {
		return err
	}

	repman.refreshAllPeers()
	return nil
}

func (repman *ReplicationManager) GetIsGitPush() bool {
	repman.GitPushLock.Lock()
	defer repman.GitPushLock.Unlock()
	return repman.IsGitPush
}

func (repman *ReplicationManager) SetIsGitPush(val bool) {
	repman.GitPushLock.Lock()
	defer repman.GitPushLock.Unlock()
	repman.IsGitPush = val
	for _, cl := range repman.Clusters {
		cl.IsGitPush = val
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git push changed: %t", val)
}

func (repman *ReplicationManager) SetIsGitPull(val bool) {
	repman.IsGitPull = val
	for _, cl := range repman.Clusters {
		cl.IsGitPull = val
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Git pull changed: %t", val)
}

func (repman *ReplicationManager) InitGitConfig(conf *config.Config) error {
	if repman.GetIsGitPush() {
		return nil
	}

	repman.SetIsGitPush(true)
	defer repman.SetIsGitPush(false)

	if conf.GitUrl != "" && conf.GitAccesToken != "" && !conf.Cloud18 {
		var tok string

		if conf.IsVaultUsed() && conf.IsPath(conf.GitAccesToken) {
			conn, err := conf.GetVaultConnection()
			if err != nil {
				repman.Logrus.Printf("Error vault connection %v", err)
			}
			tok, err = conf.GetVaultCredentials(conn, conf.GitAccesToken, "git-acces-token")
			if err != nil {
				repman.Logrus.Printf("Error get vault git-acces-token value %v", err)
				tok = conf.GetDecryptedValue("git-acces-token")
			} else {
				var Secrets config.Secret
				Secrets.Value = tok
				conf.Secrets["git-acces-token"] = Secrets
			}

		} else {
			tok = conf.GetDecryptedValue("git-acces-token")
		}

		conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, tok, conf.WorkingDir)
	}

	if conf.Cloud18GitUser != "" && conf.Cloud18GitPassword != "" && conf.Cloud18 {
		gituser := conf.Cloud18GitUser
		gitpassword := conf.GetDecryptedValue("cloud18-gitlab-password")
		acces_tok, err := githelper.GetGitLabTokenBasicAuth(gituser, gitpassword, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("%s\n", err.Error()+conf.GetDecryptedValue("cloud18-gitlab-password"))
			}
			conf.Cloud18 = false
			return err
		}

		//to get meet token and create a client while login
		userID, err := meethelper.CreateMeetUserClient(gituser, gitpassword, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
		if err != nil {
			if repman.Conf.LogSupport {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn, "Error retrieving meet token: %s", err)
			}
		} else {
			repman.MeetUserID = userID
			for _, cluster := range repman.Clusters {
				cluster.MeetUserID = userID
			}
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "Meet token is retrieved")
		}

		if conf.Cloud18Domain == "" {
			return fmt.Errorf("Cloud18Domain is empty")
		}

		if conf.Cloud18SubDomain == "" {
			return fmt.Errorf("Cloud18SubDomain is empty")
		}

		if conf.Cloud18SubDomainZone == "" {
			return fmt.Errorf("Cloud18SubDomainZone is empty")
		}

		repman.SetCloudPartner()

		uid, err := githelper.GetGitLabUserId(acces_tok, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("%s\n", err.Error())
			}
			conf.Cloud18 = false
			return err
		} else if uid == 0 {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("Invalid user Id \n")
			}
			conf.Cloud18 = false
			return fmt.Errorf("Invalid user Id")
		}

		_, err = githelper.InitGroupAccessLevel(acces_tok, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if err != nil {
			if conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr) {
				repman.Logrus.Errorf("%s\n", err.Error())
			}
			conf.Cloud18 = false
			return err
		}

		tokenName := conf.GetInstancePATName()
		personal_access_token, _ := githelper.GetGitLabTokenOAuth(acces_tok, tokenName, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
		if personal_access_token == "" {
			personal_access_token, err = githelper.CreatePersonalAccessTokenCSRF(conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password"), tokenName)
			if err != nil && (conf.Verbose || conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlErr)) {
				repman.Logrus.Errorf("%v", err.Error())
			}
		}

		if personal_access_token != "" {
			var Secrets config.Secret
			Secrets.Value = personal_access_token
			conf.Secrets["git-acces-token"] = Secrets
			path := conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
			name := conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
			githelper.GitLabCreateProject(personal_access_token, name, path, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))
			githelper.GitLabCreatePullProject(personal_access_token, name, path, conf.Cloud18Domain, uid, conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg))

			conf.GitUrl = conf.OAuthProvider + "/" + conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + ".git"
			conf.GitUrlPull = conf.OAuthProvider + "/" + conf.Cloud18Domain + "/" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone + "-pull.git"
			conf.GitUsername = conf.Cloud18GitUser
			conf.GitAccesToken = personal_access_token
			conf.ImmuableFlagMap["git-url"] = conf.GitUrl
			conf.ImmuableFlagMap["git-url-pull"] = conf.GitUrlPull
			conf.ImmuableFlagMap["git-username"] = conf.GitUsername
			conf.ImmuableFlagMap["git-acces-token"] = personal_access_token

			if conf.ConfRestoreOnStart {
				conf.ConfRestoreOnStart = false
				conf.ImmuableFlagMap["monitoring-restore-config-on-start"] = false
				os.RemoveAll(conf.WorkingDir)
				conf.CloneConfigFromGit(conf.GitUrl, conf.GitUsername, conf.GitAccesToken, conf.WorkingDir)
				conf.CloneConfigFromGit(conf.GitUrlPull, conf.GitUsername, conf.GitAccesToken, conf.WorkingDir+"/.pull")
			}

		} else if conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlInfo) {
			err := fmt.Errorf("Could not get personal access token from gitlab")
			repman.Logrus.WithField("group", repman.ClusterList[cfgGroupIndex]).Infof("%s", err.Error())
			return err
		}

	}

	return nil
}

// DEAD CODE — no callers. Superseded by ConfigManager.PushAllConfigsToGit
// (config/manager/manager.go), the only live push path. Kept for reference only;
// safe to delete. Do not wire back in: it calls the dead repman.PushConfigToGit
// below, which stages agents.json unthrottled and bypasses the ConfigManager queue.
func (repman *ReplicationManager) PushAllConfigsToGit() error {
	defer func() {
		if r := recover(); r != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error pushing to git: %v", r)
		}
	}()

	if repman.Conf.GitUrl == "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "No Git URL provided, skipping push")
		return nil
	}

	repman.IsGitPush = true
	defer func() {
		repman.IsGitPush = false
	}()

	repman.AddPullToGitignore()
	repman.AddTempDirToGitignore()
	repman.AddPluginDirToGitignore()
	repman.AddDictTablesToGitignore()
	addLineToGitignore(repman.Conf.WorkingDir+"/.gitignore", "*/variable-diff.json")
	// Event log instance-local state and crash-safe temp files never travel.
	addLineToGitignore(repman.Conf.WorkingDir+"/.gitignore", "event-log-state.json")
	addLineToGitignore(repman.Conf.WorkingDir+"/.gitignore", "*.new")
	// The .config isolated clone is replaced by the config event log
	// (doc/implementation/config/CONFIG_EVENT_LOG.md); drop leftovers.
	os.RemoveAll(filepath.Join(repman.Conf.WorkingDir, ".config"))

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Pushing All Configs To Git")

	err := repman.PushConfigToGit()
	if err != nil && err == transport.ErrRepositoryNotFound {
		os.RemoveAll(repman.Conf.WorkingDir + "/.git")
		err := repman.PushConfigToGit()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error pushing to git: %v", err)
			return err
		}
	}

	// Count the commits
	commits, err := repman.CountAllCommits()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Error counting commits: %v", err)
		return err
	}

	if commits >= 10 {
		os.RemoveAll(repman.Conf.WorkingDir + "/.git")
		err := repman.ShallowClone()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error shallow cloning: %v", err)
			return err
		}
	}

	return nil
}

func (repman *ReplicationManager) PullCloud18Configs() {
	gm := repman.ConfigManager.GetGitManager()
	if gm == nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Git manager not initialized")
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Queue Pulling Cloud18 Configs")
	repman.ConfigManager.GitPullDir() // Queue the pull
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Waiting for Pulling Cloud18 Configs")

	// Wait for start pull signal
	<-gm.PullCh
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Pulling Cloud18 Configs")

	var pullErr error

	defer func() {
		// Inform the GitManager that the pull is done
		gm.SetPullResult(pullErr)
		gm.DonePullCh <- struct{}{}
	}()

	pullDir := repman.Conf.WorkingDir + "/.pull"
	filePath := pullDir + "/cloud18.toml"

	if repman.Conf.GitUrlPull != "" {
		err := repman.Conf.CloneConfigFromGit(repman.Conf.GitUrlPull, repman.Conf.GitUsername, repman.Conf.Secrets["git-acces-token"].Value, pullDir)
		if err != nil {
			os.RemoveAll(pullDir + "/.git")
			err = repman.Conf.CloneConfigFromGit(repman.Conf.GitUrlPull, repman.Conf.GitUsername, repman.Conf.Secrets["git-acces-token"].Value, pullDir)
			if err != nil {
				pullErr = err
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Error pulling cloud18 git config: %v", err)
				return
			}
		}

		//to check cloud18.toml for the first time
		if repman.Conf.Cloud18 {
			repman.CheckCloud18Config(filePath)
			repman.LoadPeerJson()
			repman.UpdateLocalPeer()
			repman.LoadPartnersJson()
		}

		// Copy plugin binaries from pull repo into each cluster's working dir
		// so ReloadLogPlugins can find them.
		repman.syncPluginsFromPull(pullDir)

		// Copy global plugin data files (e.g. enterprise-security-issues.json)
		// from pull repo root plugins/data/ → ShareDir/plugins/data/.
		repman.syncPluginDataFromPull(pullDir)
	}

	if repman.Conf.Cloud18 {
		//then to check new file pulled in working dir
		files, err := os.ReadDir(repman.Conf.WorkingDir)
		if err != nil {
			repman.Logrus.Infof("No working directory %s ", repman.Conf.WorkingDir)
		}
		//check all dir of the datadir to check if a new cluster has been pull by git
		for _, f := range files {
			new_cluster_discover := true
			if f.IsDir() && f.Name() != "graphite" && f.Name() != "backups" && f.Name() != ".git" && f.Name() != "cloud18.toml" && !strings.Contains(f.Name(), ".json") && !strings.Contains(f.Name(), ".csv") && f.Name() != ".pull" && f.Name() != "plugins" {
				repman.Lock()
				_, alreadyKnown := repman.Clusters[f.Name()]
				repman.Unlock()
				if alreadyKnown {
					new_cluster_discover = false
				}
			} else {
				new_cluster_discover = false
			}
			//find a dir that is not in the cluster list (and diff from backups and graphite)
			//so add the to new cluster to the repman
			if new_cluster_discover {
				//check if this there is a config file in the dir
				if _, err := os.Stat(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml"); !os.IsNotExist(err) {
					//init config, start the cluster and add it to the cluster list

					// Same runtime cluster-start lock as AddCluster() and
					// FetchDynamicClustersFromGit(): this auto-discovery path is a
					// third live StartCluster() entrypoint and must not race the
					// other two over the same cluster name. See
					// doc/implementation/server/DYNAMIC_CLUSTER_GIT_IMPORT_PLAN.md.
					//
					// new_cluster_discover above was only a fast, lock-protected
					// pre-filter — re-check membership now that the lock is held,
					// since another entrypoint may have registered this exact
					// cluster name while this goroutine was waiting for the lock.
					repman.runtimeClusterStartMu.Lock()
					repman.Lock()
					_, alreadyKnown := repman.Clusters[f.Name()]
					repman.Unlock()
					if alreadyKnown {
						repman.runtimeClusterStartMu.Unlock()
						continue
					}

					repman.ViperConfig.SetConfigName(f.Name())
					repman.ViperConfig.SetConfigFile(repman.Conf.WorkingDir + "/" + f.Name() + "/" + f.Name() + ".toml")
					err := repman.ViperConfig.MergeInConfig()
					if err != nil {
						repman.Logrus.Errorf("Config error in %s: %s", repman.Conf.WorkingDir+"/"+f.Name()+"/"+f.Name()+".toml", err.Error())
					}
					repman.Confs[f.Name()] = repman.GetClusterConfig(repman.ViperConfig, repman.Conf.ImmuableFlagMap, repman.Conf.DynamicFlagMap, f.Name(), *repman.Conf)
					repman.StartCluster(f.Name())
					repman.Lock()
					if c, ok := repman.Clusters[f.Name()]; ok {
						c.IsGitPull = true
					}
					repman.ClusterList = append(repman.ClusterList, f.Name())
					repman.Unlock()
					repman.runtimeClusterStartMu.Unlock()
					repman.refreshAllPeers()
				}
			}
		}
	}
}

// syncPluginsFromPull copies plugin binaries from the pull repo root
// (pullDir/plugins/) into a shared directory (WorkingDir/plugins/) and
// ensures each cluster's plugins/ is a symlink to that shared dir.
//
// On upgrade from per-cluster copies, existing real plugin directories are
// migrated: contents are moved to the shared dir and replaced by a symlink.
//
// .sig files are NOT distributed via the pull repo — they are CI artifacts
// shipped with the repman package in ShareDir/plugins/ to preserve the
// chain of trust.
func (repman *ReplicationManager) syncPluginsFromPull(pullDir string) {
	sharedDir := filepath.Join(repman.Conf.WorkingDir, logplugin.PluginDirName)
	if err := os.MkdirAll(sharedDir, 0755); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlErr,
			"[logplugin] cannot create shared plugin dir %s: %v", sharedDir, err)
		return
	}

	srcDir := filepath.Join(pullDir, logplugin.PluginDirName)
	changed := 0
	if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
				"[logplugin] cannot read pull plugin dir %s: %v", srcDir, err)
		} else {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				src := filepath.Join(srcDir, e.Name())
				dst := filepath.Join(sharedDir, e.Name())

				if filepath.Ext(e.Name()) == "" {
					if repman.Conf.PluginSigningPublicKey != "" {
						sigDir := filepath.Join(repman.Conf.ShareDir, "plugins")
						if err := logplugin.VerifyPluginSignature(src, sigDir, repman.Conf.PluginSigningPublicKey); err != nil {
							repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
								"[logplugin] skipping pull plugin %s: %v", e.Name(), err)
							continue
						}
					} else {
						repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
							"[logplugin] copying unsigned pull plugin %s (no public key configured)", e.Name())
					}
				}

				if gitPluginFilesEqual(src, dst) {
					continue
				}

				if err := gitPluginCopyFile(src, dst); err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
						"[logplugin] failed to copy plugin file %s: %v", e.Name(), err)
					continue
				}
				if filepath.Ext(e.Name()) == "" {
					os.Chmod(dst, 0755) // #nosec G302 — plugin binaries must be executable
				}
				changed++
			}
		}
	}

	reload := changed > 0
	for _, cluster := range repman.Clusters {
		clusterPluginDir := logplugin.PluginDir(cluster.WorkingDir)
		if repman.ensurePluginSymlink(clusterPluginDir, sharedDir) {
			reload = true
		}
	}

	if reload {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
			"[logplugin] synced %d changed plugin file(s) to shared dir %s", changed, sharedDir)
		for _, cluster := range repman.Clusters {
			cluster.ReloadLogPlugins()
		}
	}
}

// ensurePluginSymlink makes sure clusterPluginDir is a symlink to sharedDir.
// If it is a real directory (pre-upgrade), its contents are moved to sharedDir
// and the directory is replaced with a symlink. Returns true if a migration
// or symlink creation happened.
func (repman *ReplicationManager) ensurePluginSymlink(clusterPluginDir, sharedDir string) bool {
	rel, err := filepath.Rel(filepath.Dir(clusterPluginDir), sharedDir)
	if err != nil {
		rel = sharedDir
	}

	fi, err := os.Lstat(clusterPluginDir)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, rerr := os.Readlink(clusterPluginDir)
		if rerr == nil && target == rel {
			return false
		}
		os.Remove(clusterPluginDir)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
			"[logplugin] replacing stale symlink %s (was %s, want %s)", clusterPluginDir, target, rel)
	}

	if err == nil && fi.IsDir() {
		entries, readErr := os.ReadDir(clusterPluginDir)
		if readErr != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
				"[logplugin] cannot read plugin dir for migration %s: %v", clusterPluginDir, readErr)
		} else {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				src := filepath.Join(clusterPluginDir, e.Name())
				dst := filepath.Join(sharedDir, e.Name())
				if _, serr := os.Stat(dst); os.IsNotExist(serr) {
					if cerr := gitPluginCopyFile(src, dst); cerr != nil {
						repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
							"[logplugin] migration copy failed %s: %v", e.Name(), cerr)
					} else if filepath.Ext(e.Name()) == "" {
						os.Chmod(dst, 0755)
					}
				}
			}
		}
		if rerr := os.RemoveAll(clusterPluginDir); rerr != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlErr,
				"[logplugin] cannot remove old plugin dir %s: %v — skipping symlink", clusterPluginDir, rerr)
			return false
		}
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
			"[logplugin] migrated %s to symlink → %s", clusterPluginDir, rel)
	}

	if err := os.Symlink(rel, clusterPluginDir); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlErr,
			"[logplugin] cannot create symlink %s → %s: %v", clusterPluginDir, rel, err)
		return false
	}
	return true
}

// ensurePluginSymlinksAtStartup creates the shared plugin dir and migrates
// existing per-cluster real plugin dirs to symlinks. Called once at startup
// so that locally built plugins are available before the first pull sync.
func (repman *ReplicationManager) ensurePluginSymlinksAtStartup() {
	sharedDir := filepath.Join(repman.Conf.WorkingDir, logplugin.PluginDirName)
	fi, err := os.Lstat(sharedDir)
	if err != nil {
		if err := os.MkdirAll(sharedDir, 0755); err != nil {
			return
		}
	} else if fi.Mode()&os.ModeSymlink != 0 {
		// run.sh may have already created a symlink to build/plugins/
	} else if !fi.IsDir() {
		return
	}
	migrated := false
	for _, cluster := range repman.Clusters {
		clusterPluginDir := logplugin.PluginDir(cluster.WorkingDir)
		if repman.ensurePluginSymlink(clusterPluginDir, sharedDir) {
			migrated = true
		}
	}
	if migrated {
		for _, cluster := range repman.Clusters {
			cluster.ReloadLogPlugins()
		}
	}
}

// syncPluginDataFromPull copies global plugin data files from the pull repo
// root (pullDir/plugins/data/) into ShareDir/plugins/data/.  These files are
// instance-wide (not per-cluster) — e.g. the enterprise-security-issues.json
// pushed by the back office.  Only files that actually changed (MD5 comparison)
// are overwritten.
func (repman *ReplicationManager) syncPluginDataFromPull(pullDir string) {
	srcDir := filepath.Join(pullDir, "plugins", "data")
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return
	}
	dstDir := filepath.Join(repman.Conf.ShareDir, "plugins", "data")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlErr,
			"[logplugin] cannot create plugin data dir %s: %v", dstDir, err)
		return
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
			"[logplugin] cannot read pull plugin data dir %s: %v", srcDir, err)
		return
	}
	changed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		if gitPluginFilesEqual(src, dst) {
			continue
		}
		if err := gitPluginCopyFile(src, dst); err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
				"[logplugin] failed to copy plugin data file %s: %v", e.Name(), err)
			continue
		}
		changed++
	}
	if changed > 0 {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModPlugin, config.LvlInfo,
			"Synced %d global plugin data file(s) from pull repo", changed)

		for _, cluster := range repman.Clusters {
			// Reload DocHelp database (safe to auto-refresh)
			if cluster.Configurator.DocHelp != nil {
				cluster.Configurator.DocHelp.Reload()
			}
			// Reload db_distributions.json (safe to auto-refresh)
			cluster.Configurator.ReloadDBDistributions()
			// Reload repos.json (docker image tags)
			cluster.ReloadDockerRepos()
		}

		// Compliance modules are NOT auto-reloaded — the enterprise-compliance
		// plugin detects the change and raises a state; the user must explicitly
		// accept the new compliance via the API before it takes effect.
	}
}

// gitPluginFilesEqual returns true when dst exists and has the same MD5 as src.
func gitPluginFilesEqual(src, dst string) bool {
	srcHash, err := gitPluginFileHash(src)
	if err != nil {
		return false
	}
	dstHash, err := gitPluginFileHash(dst)
	if err != nil {
		return false
	}
	return bytes.Equal(srcHash, dstHash)
}

func gitPluginFileHash(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 — path is constructed from trusted dirs
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// gitPluginCopyFile copies a single file from src to dst, overwriting dst if it exists.
func gitPluginCopyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 — src is constructed from a trusted pull dir
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (repman *ReplicationManager) ReadCloud18Config() {
	filePath := conf.WorkingDir + "/.pull/cloud18.toml"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return
	}
	if repman.ViperConfig == nil {
		return
	}
	repman.Conf.ReadCloud18Config(repman.ViperConfig, filePath)
}

func (repman *ReplicationManager) ComputeFileChecksum(filePath string) (hash.Hash, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, fmt.Errorf("error computing file hash: %v", err)
	}
	return hasher, nil
}

func (repman *ReplicationManager) CheckCloud18Config(filePath string) {
	// Define the file path for cloud18.toml

	currentChecksum, err := repman.ComputeFileChecksum(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error checking file %s: %v", filePath, err)
		}
		return
	}

	// First-time initialization
	if repman.cloud18CheckSum == nil {
		repman.ReadCloud18Config()
		repman.cloud18CheckSum = currentChecksum
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Initialized cloud18.toml checksum")
	} else if !bytes.Equal(repman.cloud18CheckSum.Sum(nil), currentChecksum.Sum(nil)) {
		// File has changed, reload configuration
		repman.ReadCloud18Config()
		repman.cloud18CheckSum = currentChecksum
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "cloud18.toml has been updated")
	}
}

func (repman *ReplicationManager) LoadPeerJson() error {
	filePath := filepath.Join(repman.Conf.WorkingDir, ".pull", "peer.json")

	fstat, err := os.Stat(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading peer file: %v", err)
		}
		return err
	}

	modTime := fstat.ModTime()

	// Record every successful lookup of the remote peer catalog (surfaced in the peer
	// view), whether or not the content changed since last time.
	repman.LastPeerLookup = time.Now()

	if oldModTime, ok := repman.ModTimes["peer"]; ok && oldModTime.Equal(modTime) {
		repman.dispatchPeerHealthPoll()
		return nil // No changes in the file modification time
	}

	repman.ModTimes["peer"] = modTime

	content, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading peer file: %v", err)
		}
		return err
	}

	// Calculate the checksum
	newHash := md5.New()
	newHash.Write(content)

	// Compare with the existing checksum
	if oldHash, ok := repman.CheckSumConfig["peer"]; ok && bytes.Equal(oldHash.Sum(nil), newHash.Sum(nil)) {
		repman.dispatchPeerHealthPoll()
		return nil // No changes in the file content
	}

	// Decode JSON
	var PeerList []*peer.PeerCluster
	if err := json.Unmarshal(content, &PeerList); err != nil {
		repman.Logrus.Errorf("failed to decode peer JSON: %v", err)
		return err
	}

	if len(PeerList) > 0 {
		repman.PeerManager.BatchUpdateClusters(PeerList, true)
	}

	// peer.json content changed: refresh health immediately, but through the
	// server-driven, session-scoped dispatch (same call the two unchanged
	// branches above make). This keeps instant-on-pull refresh while ensuring
	// only PeerUserClusters for connected users are polled — never the for-sale
	// catalog. See doc/implementation/peer/MARKETPLACE.md §6.
	repman.dispatchPeerHealthPoll()

	// Update state
	repman.CheckSumConfig["peer"] = newHash

	return nil

}

func (repman *ReplicationManager) LoadPartnersJson() error {
	filePath := filepath.Join(repman.Conf.WorkingDir, ".pull", "partners.json")

	fstat, err := os.Stat(filePath)
	if err != nil {
		repman.Partners = make([]config.Partner, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading partners file: %v", err)
		}

		return err
	}

	modTime := fstat.ModTime()

	if oldModTime, ok := repman.ModTimes["partners"]; ok && oldModTime.Equal(modTime) {
		return nil // No changes in the file modification time
	}

	repman.ModTimes["partners"] = modTime

	content, err := os.ReadFile(filePath)
	if err != nil {
		repman.Partners = make([]config.Partner, 0)
		if !os.IsNotExist(err) {
			repman.Logrus.Errorf("failed reading partners file: %v", err)
		}
		return err
	}

	// Calculate the checksum
	newHash := md5.New()
	newHash.Write(content)

	// Compare with the existing checksum
	if oldHash, ok := repman.CheckSumConfig["partners"]; ok && bytes.Equal(oldHash.Sum(nil), newHash.Sum(nil)) {
		return nil // No changes in the file content
	}

	// Decode JSON
	var PartnerList []config.Partner
	if err := json.Unmarshal(content, &PartnerList); err != nil {
		repman.Logrus.Errorf("failed to decode partners JSON: %v", err)
		return err
	}

	// Update state
	repman.Partners = PartnerList
	repman.CheckSumConfig["partners"] = newHash
	repman.SetCloudPartner()
	return nil

}

// AddPullToGitignore ensures ".pull/" is in .gitignore.
func (repman *ReplicationManager) AddPullToGitignore() {
	gitignoreFile := repman.Conf.WorkingDir + "/.gitignore"
	lineToAdd := ".pull/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
		}
	}
}

// Ensures ".tmp/" is in .gitignore.
func (repman *ReplicationManager) AddTempDirToGitignore() {
	gitignoreFile := repman.Conf.WorkingDir + "/.gitignore"
	lineToAdd := ".tmp/"

	// Check if .gitignore exists
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		// If .gitignore doesn't exist, create it and write the line
		err := os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		if err != nil {
			fmt.Println("Error creating .gitignore:", err)
		}
		return
	}

	// Open .gitignore for reading and appending
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Error opening .gitignore:", err)
		return
	}
	defer file.Close()

	// Check if the line already exists
	scanner := bufio.NewScanner(file)
	lineExists := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			lineExists = true
			break
		}
	}

	if scanner.Err() != nil {
		fmt.Println("Error reading .gitignore:", scanner.Err())
		return
	}

	// Append the line if it doesn't already exist
	if !lineExists {
		_, err := file.WriteString(lineToAdd + "\n")
		if err != nil {
			fmt.Println("Error appending to .gitignore:", err)
		}
	}
}

// AddDictTablesToGitignore ensures "dicttables.json" is in .gitignore so
// table size changes do not generate git diffs on every monitoring tick.
func (repman *ReplicationManager) AddDictTablesToGitignore() {
	addLineToGitignore(repman.Conf.WorkingDir+"/.gitignore", "dicttables.json")
}

// addLineToGitignore ensures a given line is present in the .gitignore file
// at gitignoreFile, creating the file if it does not exist.
func addLineToGitignore(gitignoreFile, lineToAdd string) {
	if _, err := os.Stat(gitignoreFile); os.IsNotExist(err) {
		os.WriteFile(gitignoreFile, []byte(lineToAdd+"\n"), 0644)
		return
	}
	file, err := os.OpenFile(gitignoreFile, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == lineToAdd {
			return
		}
	}
	file.WriteString(lineToAdd + "\n")
}

// AddPluginDirToGitignore ensures "<clusterName>/plugins/" patterns are in
// .gitignore so that subscription-delivered plugin binaries are never
// accidentally committed back to the config repository.
func (repman *ReplicationManager) AddPluginDirToGitignore() {
	gitignoreFile := repman.Conf.WorkingDir + "/.gitignore"
	// Ignore all plugins/ subdirs regardless of cluster name.
	addLineToGitignore(gitignoreFile, "*/plugins/")
}

func (repman *ReplicationManager) MoveConfigsToTmpDir(path string) error {
	// Create the .tmp directory
	tmpDir := filepath.Join(path, ".tmp")
	err := os.MkdirAll(tmpDir, 0755)
	if err != nil {
		repman.Logrus.Errorf("Error creating .tmp directory: %s", err)
		return err
	}

	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the .tmp directory itself
		if info.IsDir() && filePath == tmpDir {
			return filepath.SkipDir
		}

		// Process only .toml and .json files
		if !info.IsDir() && (strings.HasSuffix(info.Name(), ".toml") || strings.HasSuffix(info.Name(), ".json")) {
			// Calculate relative path and target path in .tmp
			relPath, err := filepath.Rel(path, filePath)
			if err != nil {
				repman.Logrus.Errorf("Error calculating relative path for %s: %s", filePath, err)
				return err
			}
			newPath := filepath.Join(tmpDir, relPath)

			// Create directories in .tmp as needed
			err = os.MkdirAll(filepath.Dir(newPath), 0755)
			if err != nil {
				repman.Logrus.Errorf("Error creating directories for %s: %s", newPath, err)
				return err
			}

			// Move the file
			err = os.Rename(filePath, newPath)
			if err != nil {
				repman.Logrus.Errorf("Error moving file %s to %s: %s", filePath, newPath, err)
				return err
			}
		}
		return nil
	})

	if err != nil {
		repman.Logrus.Errorf("Error moving files to .tmp directory: %s", err)
		return err
	}

	return nil
}

func (repman *ReplicationManager) RestoreConfigsFromTmpDir(path string) error {
	// Define the .tmp directory
	tmpDir := filepath.Join(path, ".tmp")

	// Check if the .tmp directory exists
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		log.Printf(".tmp directory does not exist in %s\n", path)
		return nil
	}

	err := filepath.Walk(tmpDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process files
		if !info.IsDir() {
			// Calculate the original path of the file
			relPath, err := filepath.Rel(tmpDir, filePath)
			if err != nil {
				log.Printf("Error calculating relative path for %s: %s\n", filePath, err)
				return err
			}

			originalPath := filepath.Join(path, relPath)

			// Ensure the parent directory exists
			err = os.MkdirAll(filepath.Dir(originalPath), 0755)
			if err != nil {
				log.Printf("Error creating directories for %s: %s\n", originalPath, err)
				return err
			}

			// Move the file back to its original location
			err = os.Rename(filePath, originalPath)
			if err != nil {
				log.Printf("Error moving file %s back to %s: %s\n", filePath, originalPath, err)
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error restoring files from .tmp directory: %s\n", err)
		return err
	}

	// Remove the .tmp directory after restoration
	err = os.RemoveAll(tmpDir)
	if err != nil {
		log.Printf("Error removing .tmp directory: %s\n", err)
		return err
	}

	log.Println("Restoration completed successfully.")
	return nil
}

// DEAD CODE — only caller is the dead repman.PushAllConfigsToGit above.
// Superseded by ConfigManager.PushConfigToGit (config/manager/manager.go), the
// only live push path (with the shouldStageAgents throttle). This copy stages
// agents.json unthrottled; kept for reference only, safe to delete.
func (repman *ReplicationManager) PushConfigToGit() error {
	url := repman.Conf.GitUrl
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	user := repman.Conf.GitUsername
	path := repman.Conf.WorkingDir
	clusterList := repman.ClusterList

	// Log basic information
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Push to git: user=%s, dir=%s, clusters=%v", user, path, clusterList)

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	var r *git.Repository
	start := time.Now()

	// Check if .git directory exists
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		cloneopt := &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
			Auth:              auth,
			Depth:             1, // Shallow clone
		}

		if !repman.Conf.ConfRestoreOnStart {
			cloneopt.NoCheckout = true
		}

		// Perform shallow clone for better performance
		r, err = git.PlainClone(path, false, cloneopt)

		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
			"Clone took: %s", time.Since(start))

		// Handle repository not found
		if err != nil {
			if err == transport.ErrRepositoryNotFound {
				repman.Conf.CreateGitlabProjects()
				r, err = git.PlainClone(path, false, &git.CloneOptions{
					URL:               url,
					RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
					Auth:              auth,
					Depth:             1,
				})
			}
			if err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
					"Git error: cannot clone %s: %s", url, err)
				return err
			}
		}
	} else {
		// Open existing repository
		r, err = git.PlainOpen(path)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Git error: cannot open repo: %s", err)
			return err
		}
	}

	// Open the worktree
	w, err := r.Worktree()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot get worktree: %s", err)
		return err
	}

	var changedFiles []string

	// Add specific files without using AddGlob
	for _, name := range clusterList {
		dirPath := filepath.Join(path, name)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Error reading directory %s: %s", dirPath, err)
			continue
		}

		// Add .toml files
		for _, file := range files {
			if filepath.Ext(file.Name()) == ".toml" {
				filePath := filepath.Join(name, file.Name())
				if _, err := w.Add(filePath); err == nil {
					changedFiles = append(changedFiles, filePath)
				} else {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
						"Git error: cannot add %s: %s", filePath, err)
				}
			}
		}

		// Add agents.json and queryrules.json if they exist
		for _, jsonFile := range []string{"agents.json", "queryrules.json"} {
			jsonPath := filepath.Join(name, jsonFile)
			if _, err := os.Stat(filepath.Join(path, jsonPath)); !os.IsNotExist(err) {
				if _, err := w.Add(jsonPath); err == nil {
					changedFiles = append(changedFiles, jsonPath)
				} else {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
						"Git error: cannot add %s: %s", jsonPath, err)
				}
			}
		}
	}

	// Add default.toml if it exists
	defaultToml := "default.toml"
	if _, err := os.Stat(filepath.Join(path, defaultToml)); !os.IsNotExist(err) {
		if _, err := w.Add(defaultToml); err == nil {
			changedFiles = append(changedFiles, defaultToml)
		} else {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
				"Git error: cannot add %s: %s", defaultToml, err)
		}
	}

	// Add this instance's config event log: peers replicate config changes
	// by replaying it (doc/implementation/config/CONFIG_EVENT_LOG.md) — an
	// unstaged log means events never leave this node.
	if entries, err := os.ReadDir(path); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "event-changed.") || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			if _, err := w.Add(e.Name()); err == nil {
				changedFiles = append(changedFiles, e.Name())
			} else {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
					"Git error: cannot add %s: %s", e.Name(), err)
			}
		}
	}

	// Skip commit if no files were changed
	if len(changedFiles) == 0 {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
			"No changes detected, skipping commit.")
		return nil
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Files changed: %v", changedFiles)

	// Commit the changes
	commitStart := time.Now()
	_, err = w.Commit("Update configuration", &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication Manager",
			When: time.Now(),
		},
	})
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Commit took: %s", time.Since(commitStart))

	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot commit: %s", err)
		return err
	}

	// Push changes
	pushStart := time.Now()
	err = r.Push(&git.PushOptions{Auth: auth})
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlDbg,
		"Push took: %s", time.Since(pushStart))

	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr,
			"Git error: cannot push: %s", err)
	}
	// NOTE: do NOT stamp LastConfigGitPush here — this function is dead in the live loop
	// (see the "do not wire back in" note above). The real push runs in the ConfigManager
	// worker, which stamps it via SetSyncStampHook. Stamping here only produced a value on
	// a path that never runs, so the indicator showed "—" despite pushes succeeding.

	return err
}

func (repman *ReplicationManager) CountAllCommits() (int, error) {
	mainPath := repman.Conf.WorkingDir

	// Open the repository
	r, err := git.PlainOpen(mainPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open repository at %s: %w", mainPath, err)
	}

	r.Fetch(&git.FetchOptions{Force: true, Auth: &git_https.BasicAuth{Username: repman.Conf.GitUsername, Password: repman.Conf.GetDecryptedValue("git-acces-token")}})

	commitIter, err := r.Log(&git.LogOptions{All: true})
	if err != nil {
		return 0, fmt.Errorf("failed to get commit iterator: %w", err)
	}

	commitCount := 0
	// Count commits for this branch/tag
	_ = commitIter.ForEach(func(c *git_obj.Commit) error {
		commitCount++
		return nil
	})

	return commitCount, nil
}

func (repman *ReplicationManager) ShallowClone() error {
	url := repman.Conf.GitUrl
	tok := repman.Conf.GetDecryptedValue("git-acces-token")
	user := repman.Conf.GitUsername
	path := repman.Conf.WorkingDir

	auth := &git_https.BasicAuth{
		Username: user, // Can be any non-empty string
		Password: tok,
	}

	// Perform shallow clone for better performance
	_, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:               url,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth:              auth,
		Depth:             1, // Shallow clone
		NoCheckout:        true,
	})

	return err
}

