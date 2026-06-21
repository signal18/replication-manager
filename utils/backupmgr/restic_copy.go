package backupmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// copySftpRepoRegex matches the restic sftp backend syntax: sftp:[user@]host:/path
// Mirrors cluster.resticSftpRepoRegex — keep both in sync if the pattern changes.
var copySftpRepoRegex = regexp.MustCompile(`^sftp:[^:@\s]+(@[^:@\s]+)?:.+$`)

// copyProgressLineRegex matches restic copy text-mode progress lines, e.g.:
// "[0:00] 100.00%  2 / 2 packs copied"
var copyProgressLineRegex = regexp.MustCompile(`\[[\d:]+\]\s+([\d.]+)%\s+(\d+)\s*/\s*(\d+)\s+packs`)

// copySnapshotSavedRegex matches "snapshot X saved, copied from source snapshot Y"
var copySnapshotSavedRegex = regexp.MustCompile(`(?i)snapshot\s+\S+\s+saved`)

// copySnapshotSkippedRegex matches restic's skip-snapshot lines.
// Restic emits "skipping source snapshot X, was already copied to snapshot Y"
// so the pattern must cover both "skipping snapshot" and "skipping source snapshot".
var copySnapshotSkippedRegex = regexp.MustCompile(`(?i)skipping(?:\s+source)?\s+snapshot`)

// ResticCopySourceAWSOption holds S3 credentials for the copy source repository.
type ResticCopySourceAWSOption struct {
	Endpoint     string `json:"endpoint,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	AccessKeyID  string `json:"access_key_id,omitempty"`
	AccessSecret string `json:"access_secret,omitempty"`
	Region       string `json:"region,omitempty"`
}

// ResticCopySourceOption describes the source repository for a copy operation.
// Password is intentionally serializable so the HTTP handler can decode it from the
// request body, but the entire ResticCopyOption is tagged json:"-" on ResticTask to
// prevent credential leakage through queue/current-task API responses.
type ResticCopySourceOption struct {
	Mode            string                     `json:"mode,omitempty"`
	Repository      string                     `json:"repository,omitempty"`
	Password        string                     `json:"password,omitempty"`
	KeyHint         string                     `json:"key_hint,omitempty"`
	AWS             *ResticCopySourceAWSOption `json:"aws,omitempty"`
	UseSavedConfig  bool                       `json:"use_saved_config,omitempty"`
}

// ResticCopyOption holds all parameters for a repository copy task.
// When attached to ResticTask, this field is tagged json:"-" to prevent
// secrets from appearing in task-queue API responses.
type ResticCopyOption struct {
	Source            ResticCopySourceOption `json:"source"`
	InitDestination   bool                   `json:"init_destination,omitempty"`
	CopyChunkerParams bool                   `json:"copy_chunker_params,omitempty"`
	SnapshotIDs       []string               `json:"snapshot_ids,omitempty"`
	Host              []string               `json:"host,omitempty"`
	Path              []string               `json:"path,omitempty"`
	Tag               []string               `json:"tag,omitempty"`
}

// validateCopySourceMode rejects unsupported source mode values.
func validateCopySourceMode(mode string) error {
	switch mode {
	case config.ConstBackupArchiveModeResticLocal,
		config.ConstBackupArchiveModeResticAws,
		config.ConstBackupArchiveModeResticSftp:
		return nil
	case "":
		return fmt.Errorf("source mode is required")
	default:
		return fmt.Errorf("unsupported source mode %q: must be restic-local, restic-aws, or restic-sftp", mode)
	}
}

// buildCopySourceRepoString constructs the restic repository URL for the source.
func buildCopySourceRepoString(src ResticCopySourceOption) (string, error) {
	switch src.Mode {
	case config.ConstBackupArchiveModeResticLocal:
		repo := strings.TrimSpace(src.Repository)
		if repo == "" {
			return "", fmt.Errorf("source repository path is required for mode %q", src.Mode)
		}
		return repo, nil
	case config.ConstBackupArchiveModeResticSftp:
		repo := strings.TrimSpace(src.Repository)
		if repo == "" {
			return "", fmt.Errorf("source repository path is required for mode %q", src.Mode)
		}
		if !copySftpRepoRegex.MatchString(repo) {
			return "", fmt.Errorf("source SFTP repository %q does not match expected format sftp:[user@]host:/path", repo)
		}
		return repo, nil
	case config.ConstBackupArchiveModeResticAws:
		if src.AWS == nil {
			return "", fmt.Errorf("source AWS configuration is required for mode restic-aws")
		}
		bucket := strings.TrimSpace(src.AWS.Bucket)
		if bucket == "" {
			return "", fmt.Errorf("source AWS bucket is required")
		}
		prefix := strings.Trim(src.AWS.Prefix, "/")
		endpoint := strings.TrimRight(strings.TrimSpace(src.AWS.Endpoint), "/")
		var repoStr string
		if endpoint != "" {
			repoStr = "s3:" + endpoint + "/" + bucket
		} else {
			repoStr = "s3:" + bucket
		}
		if prefix != "" {
			repoStr += "/" + prefix
		}
		return repoStr, nil
	default:
		return "", fmt.Errorf("unsupported source mode: %q", src.Mode)
	}
}

// buildCopySourceEnvOverlay builds the command-scoped environment entries needed
// to authenticate the source repository during copy or init.
// Never logs passwords or secrets.
//
// AWS credential handling: for restic-aws source mode, AWS credentials
// (access_key_id, access_secret, region) are optional only when the destination
// is non-S3 (local or SFTP). In that case omitted fields fall through to
// ambient AWS auth (IAM instance profile, ECS task role, AWS_* env vars).
// When the destination is also S3, validateS3SourceVsDest enforces that source
// and destination credentials match explicitly — ambient credentials that differ
// from the destination config will be rejected before the command runs.
func buildCopySourceEnvOverlay(src ResticCopySourceOption, fromRepo string) ([]string, error) {
	if src.Password == "" {
		return nil, fmt.Errorf("source password is required")
	}
	env := []string{
		"RESTIC_FROM_REPOSITORY=" + fromRepo,
		"RESTIC_FROM_PASSWORD=" + src.Password,
	}
	if hint := strings.TrimSpace(src.KeyHint); hint != "" {
		env = append(env, "RESTIC_FROM_KEY_HINT="+hint)
	}
	if src.Mode == config.ConstBackupArchiveModeResticAws && src.AWS != nil {
		// S3 source needs AWS credentials so restic can access the source repository.
		// Per G1, these credentials match destination creds (or destination is non-S3).
		if src.AWS.AccessKeyID != "" {
			env = append(env, "AWS_ACCESS_KEY_ID="+src.AWS.AccessKeyID)
		}
		if src.AWS.AccessSecret != "" {
			env = append(env, "AWS_SECRET_ACCESS_KEY="+src.AWS.AccessSecret)
		}
		if src.AWS.Region != "" {
			env = append(env, "AWS_DEFAULT_REGION="+src.AWS.Region)
		}
	}
	return env, nil
}

// validateS3SourceVsDest enforces G1: reject S3→S3 copy when effective AWS
// credentials or connection settings differ between source and destination.
func (repo *ResticManager) validateS3SourceVsDest(src ResticCopySourceOption) error {
	if src.Mode != config.ConstBackupArchiveModeResticAws {
		return nil
	}
	// Check whether destination uses S3.
	destPath := repo.GetRepoPath()
	if !config.IsS3ResticRepository(destPath) && strings.TrimSpace(repo.AwsBucket) == "" {
		return nil // destination is not S3 — no credential conflict
	}
	// Destination is S3: enforce matching credentials.
	srcAWS := src.AWS
	if srcAWS == nil {
		return fmt.Errorf("source AWS configuration is required for mode restic-aws")
	}
	if srcAWS.AccessKeyID != repo.AwsAccessKeyID {
		return fmt.Errorf("S3 source and destination have different AWS access key IDs; mismatched credentials are not supported for S3-to-S3 copy")
	}
	if srcAWS.AccessSecret != repo.AwsSecretAccessKey {
		return fmt.Errorf("S3 source and destination have different AWS access secrets; mismatched credentials are not supported for S3-to-S3 copy")
	}
	srcRegion := strings.TrimSpace(srcAWS.Region)
	destRegion := strings.TrimSpace(repo.AwsRegion)
	if srcRegion != destRegion {
		return fmt.Errorf("S3 source region %q differs from destination region %q", srcRegion, destRegion)
	}
	srcEndpoint := strings.TrimRight(strings.TrimSpace(srcAWS.Endpoint), "/")
	destEndpoint := strings.TrimRight(strings.TrimSpace(repo.AwsEndpoint), "/")
	if srcEndpoint != destEndpoint {
		return fmt.Errorf("S3 source endpoint %q differs from destination endpoint %q", srcEndpoint, destEndpoint)
	}
	return nil
}

// ValidateCopyOption performs all early-rejection checks that do not require
// running a restic command. Call before adding a copy task to the queue.
//
// AWS credential handling: omitting source.aws fields for restic-aws mode is
// only permitted when the destination is non-S3. For S3-to-S3 copy,
// validateS3SourceVsDest requires source and destination credentials to match
// explicitly; ambient credentials cannot be relied upon in that path.
func (repo *ResticManager) ValidateCopyOption(opt ResticCopyOption) error {
	if err := validateCopySourceMode(opt.Source.Mode); err != nil {
		return err
	}
	if _, err := buildCopySourceRepoString(opt.Source); err != nil {
		return err
	}
	if opt.Source.Password == "" {
		return fmt.Errorf("source password is required")
	}
	if opt.CopyChunkerParams && !opt.InitDestination {
		return fmt.Errorf("copy_chunker_params requires init_destination to be true")
	}
	if err := repo.validateS3SourceVsDest(opt.Source); err != nil {
		return err
	}
	return nil
}

// envKey returns the key portion of a KEY=value environment string.
func envKey(kv string) string {
	idx := strings.IndexByte(kv, '=')
	if idx < 0 {
		return kv
	}
	return kv[:idx]
}

// mergeEnvByKey merges two env slices with deterministic precedence.
// Entries in override replace same-key entries from base. Order is preserved;
// override entries that introduce a new key are appended.
func mergeEnvByKey(base []string, override []string) []string {
	// Track the index of each key in the result slice.
	indexByKey := make(map[string]int, len(base))
	result := make([]string, 0, len(base)+len(override))

	for _, kv := range base {
		k := envKey(kv)
		if idx, exists := indexByKey[k]; exists {
			result[idx] = kv
		} else {
			indexByKey[k] = len(result)
			result = append(result, kv)
		}
	}
	for _, kv := range override {
		k := envKey(kv)
		if idx, exists := indexByKey[k]; exists {
			result[idx] = kv
		} else {
			indexByKey[k] = len(result)
			result = append(result, kv)
		}
	}
	return result
}

// redactArgs returns a copy of args with the value following --from-repo replaced
// by [redacted] to avoid logging source repository URIs (host/bucket/path metadata).
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		if a == "--from-repo" && i+1 < len(out) {
			out[i+1] = "[redacted]"
		}
	}
	return out
}

// runCommandWithExtraEnv executes a restic command with a command-scoped env overlay.
// The effective env is: os.Environ() + repo.Env + extraEnv, with later entries
// overriding earlier ones when keys collide (G8 deterministic precedence).
// Source credentials in extraEnv are never written to repo.Env (G3 per-command scope).
func (repo *ResticManager) runCommandWithExtraEnv(ctx context.Context, args []string, extraEnv []string, loglevel logrus.Level, captureOutput bool) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, repo.BinaryPath, args...)
	cmd.Env = mergeEnvByKey(repo.getEnvCopy(), extraEnv)

	var stdoutBuf, stderrBuf bytes.Buffer

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(loglevel, "Starting command: %s %v", repo.BinaryPath, redactArgs(args))

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("error starting command: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	streamOutput := func(pipe io.ReadCloser, prefix string, buffer *bytes.Buffer, capture bool) {
		defer wg.Done()
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, prefix+line)
			if capture {
				buffer.WriteString(line + "\n")
			}
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, prefix+"Error reading output: %v", err)
		}
	}

	go streamOutput(stdoutPipe, "[OUT] ", &stdoutBuf, captureOutput)
	go streamOutput(stderrPipe, "[ERR] ", &stderrBuf, true)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdoutBuf.Bytes(), stderrBuf.Bytes(), fmt.Errorf("command timeout: %w", ctx.Err())
		}
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), fmt.Errorf("command execution failed: %w", err)
	}

	repo.Printf(loglevel, "Command completed successfully: %s %v", repo.BinaryPath, redactArgs(args))

	if captureOutput {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), nil
	}
	return nil, stderrBuf.Bytes(), nil
}

// buildSourcePrimaryEnv constructs an env overlay that sets RESTIC_REPOSITORY and
// RESTIC_PASSWORD to the source repository values so that regular restic commands
// (e.g. "snapshots") target the source repo directly.
// This is distinct from buildCopySourceEnvOverlay which sets RESTIC_FROM_* variables
// used by "restic copy" and "restic init --from-repo".
func buildSourcePrimaryEnv(src ResticCopySourceOption, srcRepo string) ([]string, error) {
	if src.Password == "" {
		return nil, fmt.Errorf("source password is required")
	}
	env := []string{
		"RESTIC_REPOSITORY=" + srcRepo,
		"RESTIC_PASSWORD=" + src.Password,
	}
	if hint := strings.TrimSpace(src.KeyHint); hint != "" {
		env = append(env, "RESTIC_KEY_HINT="+hint)
	}
	if src.Mode == config.ConstBackupArchiveModeResticAws && src.AWS != nil {
		if src.AWS.AccessKeyID != "" {
			env = append(env, "AWS_ACCESS_KEY_ID="+src.AWS.AccessKeyID)
		}
		if src.AWS.AccessSecret != "" {
			env = append(env, "AWS_SECRET_ACCESS_KEY="+src.AWS.AccessSecret)
		}
		if src.AWS.Region != "" {
			env = append(env, "AWS_DEFAULT_REGION="+src.AWS.Region)
		}
	}
	return env, nil
}

// countSourceSnapshots returns the number of snapshots that will be processed
// by a copy operation, without running restic copy itself.
//   - For explicit snapshot IDs: returns len(snapshotIDs) immediately.
//   - For filter-based selection: runs restic snapshots on the source repo and counts matches.
//     Returns 0 without error when the count cannot be determined (so copy still proceeds).
func (repo *ResticManager) countSourceSnapshots(ctx context.Context, opt ResticCopyOption, srcRepo string) int {
	snapshotIDs := trimResticValues(opt.SnapshotIDs)
	if len(snapshotIDs) > 0 {
		return len(snapshotIDs)
	}

	// Build env that targets the source repo directly (RESTIC_REPOSITORY/RESTIC_PASSWORD).
	srcPrimaryEnv, err := buildSourcePrimaryEnv(opt.Source, srcRepo)
	if err != nil {
		return 0
	}

	args := []string{"snapshots", "--json"}
	args = appendResticArgs(args, "--host", opt.Host)
	args = appendResticArgs(args, "--path", opt.Path)
	args = appendResticArgs(args, "--tag", opt.Tag)

	stdout, _, err := repo.runCommandWithExtraEnv(ctx, args, srcPrimaryEnv, logrus.DebugLevel, true)
	if err != nil || len(stdout) == 0 {
		return 0
	}

	var snapshots []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(stdout, &snapshots); err != nil {
		return 0
	}
	return len(snapshots)
}

// runCopyWithProgress runs "restic copy" with the given args and env, streaming
// stdout line-by-line through UpdateCurrentTaskCopyLine so the current task
// state reflects real-time copy progress.
func (repo *ResticManager) runCopyWithProgress(ctx context.Context, args []string, extraEnv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, repo.BinaryPath, args...)
	cmd.Env = mergeEnvByKey(repo.getEnvCopy(), extraEnv)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Starting command: %s %v", repo.BinaryPath, redactArgs(args))

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error starting command: %w", err)
	}

	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, "[OUT] "+line)
			repo.UpdateCurrentTaskCopyLine(line)
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, "[OUT] Error reading copy output: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			repo.Print(logrus.DebugLevel, "[ERR] "+line)
			stderrBuf.WriteString(line + "\n")
		}
		if err := scanner.Err(); err != nil {
			repo.Printf(logrus.ErrorLevel, "[ERR] Error reading copy stderr: %v", err)
		}
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stderrBuf.Bytes(), fmt.Errorf("command timeout: %w", ctx.Err())
		}
		return stderrBuf.Bytes(), fmt.Errorf("command execution failed: %w", err)
	}

	repo.Printf(logrus.InfoLevel, "Command completed successfully: %s %v", repo.BinaryPath, redactArgs(args))
	return stderrBuf.Bytes(), nil
}

// CopyRepoWithOptions copies snapshots from a source repository into the destination
// repository configured on this ResticManager. All guardrails are enforced here:
//   - G1: S3→S3 with mismatched credentials is rejected before execution.
//   - G3: source credentials are command-scoped and never mutate repo.Env.
//   - G4: destination state is classified before init is attempted.
//   - G5: init_destination must be true when copy_chunker_params is true.
//   - G6: only restic copy (and optional init) are called — never forget/prune/rewrite.
func (repo *ResticManager) CopyRepoWithOptions(opt ResticCopyOption) error {
	// Validate source and guardrail combinations.
	if err := validateCopySourceMode(opt.Source.Mode); err != nil {
		return err
	}
	if opt.CopyChunkerParams && !opt.InitDestination {
		return fmt.Errorf("copy_chunker_params requires init_destination to be true")
	}
	srcRepo, err := buildCopySourceRepoString(opt.Source)
	if err != nil {
		return err
	}
	if err := repo.validateS3SourceVsDest(opt.Source); err != nil {
		return err
	}

	// Classify destination state before any init attempt (G4).
	if opt.InitDestination {
		repo.UpdateCurrentTaskPhase("init_destination")

		result := repo.ValidateRepoConfigManual()
		switch result.Status {
		case ManualCheckStatusOK:
			if opt.CopyChunkerParams {
				return fmt.Errorf("destination repository is already initialized; copy_chunker_params cannot be applied to an existing repository")
			}
			repo.Printf(logrus.InfoLevel, "Destination repository already initialized; skipping init")
		case ManualCheckStatusInitRequired:
			// --from-repo and --copy-chunker-params together (restic >= 0.14) let the
			// destination inherit the source's chunker parameters for better dedup.
			// Without CopyChunkerParams a plain init is sufficient and works on all
			// restic versions.
			var initArgs []string
			var srcEnvForInit []string
			if opt.CopyChunkerParams {
				initArgs = []string{"init", "--from-repo", srcRepo, "--copy-chunker-params"}
				var envErr error
				srcEnvForInit, envErr = buildCopySourceEnvOverlay(opt.Source, srcRepo)
				if envErr != nil {
					return fmt.Errorf("failed to build source env for init: %w", envErr)
				}
			} else {
				initArgs = []string{"init"}
			}
			initCtx, initCancel := context.WithTimeout(context.Background(), repo.GetOperationTimeout())
			defer initCancel()
			_, stderr, err := repo.runCommandWithExtraEnv(initCtx, initArgs, srcEnvForInit, logrus.InfoLevel, false)
			if err != nil {
				return fmt.Errorf("failed to initialize destination repository: %v, stderr: %s", err, stderr)
			}
			repo.Printf(logrus.InfoLevel, "Destination repository initialized from source")
		default: // ManualCheckStatusError
			return fmt.Errorf("destination repository is inaccessible or corrupt: %s", result.Message)
		}
	}

	// Build copy command arguments.
	// --verbose ensures restic prints "skipping snapshot" lines for already-copied
	// snapshots; without it those lines are suppressed and completed_snapshots
	// would never advance for them on reruns.
	args := []string{"copy", "--verbose"}
	snapshotIDs := trimResticValues(opt.SnapshotIDs)
	if len(snapshotIDs) > 0 {
		// Explicit snapshot IDs take precedence; host/path/tag filters are ignored.
		args = append(args, snapshotIDs...)
	} else {
		args = appendResticArgs(args, "--host", opt.Host)
		args = appendResticArgs(args, "--path", opt.Path)
		args = appendResticArgs(args, "--tag", opt.Tag)
	}

	// Build command-scoped source env overlay (G3: never mutates repo.Env).
	srcEnv, err := buildCopySourceEnvOverlay(opt.Source, srcRepo)
	if err != nil {
		return fmt.Errorf("failed to build source env: %w", err)
	}

	// Pre-flight: count source snapshots so the UI can show X/Y progress.
	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer preflightCancel()
	total := repo.countSourceSnapshots(preflightCtx, opt, srcRepo)
	if total > 0 {
		repo.currentTaskMutex.Lock()
		if repo.currentTask != nil {
			repo.currentTask.TotalSnapshots = total
		}
		repo.currentTaskMutex.Unlock()
	}

	repo.UpdateCurrentTaskPhase("copy")

	timeout := repo.GetOperationTimeout()
	copyCtx, copyCancel := context.WithTimeout(context.Background(), timeout)
	defer copyCancel()

	stderr, err := repo.runCopyWithProgress(copyCtx, args, srcEnv)
	if err != nil {
		if copyCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("restic copy timeout after %v", timeout)
		}
		return fmt.Errorf("restic copy failed: %v, stderr: %s", err, stderr)
	}

	// Refresh destination snapshots and stats after successful copy.
	if err := repo.FetchRepo(); err != nil {
		repo.Printf(logrus.WarnLevel, "post-copy snapshot refresh failed: %v", err)
	}
	return nil
}

// AddCopyTask enqueues a copy task with the given options.
// Returns an error if a wipe is active; the check and enqueue are atomic.
func (repo *ResticManager) AddCopyTask(opt ResticCopyOption) error {
	return repo.appendTaskChecked(&ResticTask{
		ID:      repo.GenerateTaskID(),
		Type:    CopyTask,
		CopyOpt: &opt,
	})
}
