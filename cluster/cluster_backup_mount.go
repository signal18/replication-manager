// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/signal18/replication-manager/cluster/s3mount"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s3helper"
	"github.com/signal18/replication-manager/utils/state"
)

// The backups folder mount (#2091): with backup-streaming, the cluster's backups folder
// <working-dir>/backups/<cluster> is a mount of backup-streaming-bucket, its folder <cluster>,
// under the instance's URI on Cloud18 (backupMountPrefix). The endpoint,
// region and keys are those of an S3 provider of the cluster (backup-streaming-provider);
// with none set, one is added from the backup-streaming-* values and set. The mount is served
// by repman-s3mount, a process of its own (cluster/s3mount), never by replication-manager: a
// stalled S3 or a stuck FUSE request can then never freeze the monitor.

// ConstBackupMountProvider is the name of the S3 provider added from the backup-streaming-*
// values.
const ConstBackupMountProvider = "backup-mount"

// BackupMountState is what the backups folder mount is, shown in the Backup settings.
type BackupMountState struct {
	Enabled    bool      `json:"enabled"`
	Provider   string    `json:"provider"`
	Bucket     string    `json:"bucket"`
	MountPoint string    `json:"mountPoint"`
	Mounted    bool      `json:"mounted"`
	Error      string    `json:"error,omitempty"`
	At         time.Time `json:"at"`
	// the local backups moving into the bucket before the first mount
	Moving      bool   `json:"moving,omitempty"`
	MovedFiles  int    `json:"movedFiles,omitempty"`
	TotalFiles  int    `json:"totalFiles,omitempty"`
	MovedBytes  int64  `json:"movedBytes,omitempty"`
	TotalBytes  int64  `json:"totalBytes,omitempty"`
	LocalKeptAt string `json:"localKeptAt,omitempty"` // where the local copy was kept, never deleted
}

type backupMount struct {
	mu        sync.Mutex
	starting  bool         // the helper is starting
	moving    bool         // the local backups are being uploaded into the bucket
	signature string       // what is mounted, "" for nothing
	helper    *mountHelper // the running repman-s3mount
	state     BackupMountState
}

// backupMountPoint is the cluster's backups folder.
func (cluster *Cluster) backupMountPoint() string {
	return filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, cluster.Name)
}

// backupMountPrefix is the folder of the bucket mounted: the cluster name, under the
// instance's URI on Cloud18 (<cloud18-domain>/<cloud18-sub-domain>-<cloud18-sub-domain-zone>,
// as its secrets and billing name it), so instances sharing a bucket never mix.
func (cluster *Cluster) backupMountPrefix() string {
	c := cluster.Conf
	if c.Cloud18 && c.Cloud18Domain != "" && c.Cloud18SubDomain != "" {
		return c.Cloud18Domain + "/" + c.Cloud18SubDomain + "-" + c.Cloud18SubDomainZone + "/" + cluster.Name
	}
	return cluster.Name
}

// ConstCloud18Provider is the S3 provider a replication-manager registered to Cloud18 gets:
// the Cloud18 storage of backup-streaming-endpoint, reached with the Cloud18 SSO.
const ConstCloud18Provider = "cloud18"

// cloud18Registered: this replication-manager is registered to Cloud18 with its own identity.
func (cluster *Cluster) cloud18Registered() bool {
	return cluster.Conf.Cloud18 && cluster.Conf.Cloud18GitUser != ""
}

// ensureCloud18Provider: registered to Cloud18, the cluster's S3 providers hold the Cloud18
// storage, reached with the Cloud18 SSO (no keys, its role computed), added once.
func (cluster *Cluster) ensureCloud18Provider() {
	if !cluster.cloud18Registered() || strings.TrimSpace(cluster.Conf.BackupStreamingEndpoint) == "" {
		return
	}
	for _, p := range cluster.GetS3ProvidersSnapshot() {
		if p.Name == ConstCloud18Provider {
			return
		}
	}
	p := config.S3Provider{
		Name:           ConstCloud18Provider,
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       strings.TrimRight(strings.TrimSpace(cluster.Conf.BackupStreamingEndpoint), "/"),
		Region:         cluster.Conf.BackupStreamingRegion,
		Cloud18SSO:     true,
	}
	err := cluster.WithS3ProviderCRUDLock(func() error {
		if err := cluster.AddS3Provider(p); err != nil {
			return err
		}
		if err := cluster.SaveS3Providers(); err != nil {
			cluster.RemoveS3Provider(p.Name)
			return err
		}
		return nil
	})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cloud18 S3 provider: not added: %s", err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18 S3 provider %s added: %s, reached with the Cloud18 SSO", p.Name, p.Endpoint)
}

// ensureBackupMountProvider: backup-streaming on and no provider set, the mount uses the
// Cloud18 storage when registered; else a provider is added from the backup-streaming-* keys
// when the cluster has none of that name, and set (an on-premise instance, its own bucket).
func (cluster *Cluster) ensureBackupMountProvider() {
	cluster.ensureCloud18Provider()
	if !cluster.Conf.BackupStreaming || cluster.Conf.BackupStreamingProvider != "" {
		return
	}
	if cluster.cloud18Registered() {
		for _, p := range cluster.GetS3ProvidersSnapshot() {
			if p.Name == ConstCloud18Provider {
				cluster.Conf.BackupStreamingProvider = ConstCloud18Provider
				cluster.Save()
				return
			}
		}
	}
	exists := false
	for _, p := range cluster.GetS3ProvidersSnapshot() {
		if p.Name == ConstBackupMountProvider {
			exists = true
		}
	}
	if !exists {
		p := config.S3Provider{
			Name:           ConstBackupMountProvider,
			ProviderSource: config.S3ProviderSourceCustom,
			Endpoint:       cluster.Conf.BackupStreamingEndpoint,
			Region:         cluster.Conf.BackupStreamingRegion,
			AccessKey:      cluster.Conf.BackupStreamingAwsAccessKeyId,
			SecretKey:      cluster.Conf.GetDecryptedValue("backup-streaming-aws-access-secret"),
		}
		err := cluster.WithS3ProviderCRUDLock(func() error {
			if err := cluster.AddS3Provider(p); err != nil {
				return err
			}
			if err := cluster.SaveS3Providers(); err != nil {
				cluster.RemoveS3Provider(p.Name)
				return err
			}
			return nil
		})
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: cannot add the S3 provider %s: %s", p.Name, err)
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: S3 provider %s added from the backup-streaming settings", p.Name)
	}
	cluster.Conf.BackupStreamingProvider = ConstBackupMountProvider
	cluster.Save()
}

// mountTarget is what the backups folder mount should be, without its keys: an SSO provider's
// are fetched only when a mount or a move needs them.
type mountTarget struct {
	sig, provider, bucket, prefix, endpoint, region string
	accessKey, secretKey                            string // a provider with keys
	sso                                             bool
	roleArn                                         string
}

// spec is the goofys bucket spec of the target, its folder under the SSO user's when ssoUser.
func (t *mountTarget) spec(ssoUser string) (string, string) {
	prefix := t.prefix
	if t.sso {
		prefix = ssoUser + "/" + prefix
	}
	return t.bucket + ":" + prefix, prefix
}

// backupMountTarget is what the mount should be, nil when it is off.
func (cluster *Cluster) backupMountTarget() (*mountTarget, error) {
	if !cluster.Conf.BackupStreaming {
		return nil, nil
	}
	name := cluster.Conf.BackupStreamingProvider
	var provider *config.S3Provider
	for _, p := range cluster.GetS3ProvidersSnapshot() {
		if p.Name == name {
			p := p
			provider = &p
		}
	}
	if provider == nil {
		return nil, fmt.Errorf("no S3 provider %q", name)
	}
	if err := cluster.s3ProviderUsable(*provider); err != nil {
		return nil, err
	}
	if cluster.Conf.BackupStreamingBucket == "" {
		return nil, errors.New("no bucket (backup-streaming-bucket)")
	}
	endpoint, region, accessKey, secretKey := cluster.resolveProviderEffectiveValues(provider)
	if endpoint == "" {
		return nil, fmt.Errorf("S3 provider %q has no endpoint", name)
	}
	t := &mountTarget{provider: name, bucket: cluster.Conf.BackupStreamingBucket, prefix: cluster.backupMountPrefix(), endpoint: endpoint, region: region, accessKey: accessKey, secretKey: secretKey, sso: provider.Cloud18SSO, roleArn: provider.RoleArn}
	who := accessKey
	if t.sso {
		who = "cloud18-sso:" + cluster.Conf.Cloud18GitUser + ":" + provider.RoleArn
	}
	t.sig = fmt.Sprintf("%s|%s|%s|%s|%s|%s|%t|%x", name, t.bucket, t.prefix, endpoint, region, who, cluster.Conf.BackupStreamingDebug, sha256.Sum256([]byte(secretKey)))
	return t, nil
}

// mountKeys are the S3 keys of a target and the SSO user its folder starts with; for the
// SSO, keys renews them (nil for static keys).
type mountKeys struct {
	value   s3mount.Keys
	ssoUser string
	renew   *credentials.Credentials
	src     *cloud18SSOCredentials
}

func (cluster *Cluster) backupMountKeys(t *mountTarget) (*mountKeys, error) {
	if !t.sso {
		return &mountKeys{value: s3mount.Keys{AccessKey: t.accessKey, SecretKey: t.secretKey}}, nil
	}
	creds, src, err := cluster.newCloud18SSOCredentials(t.endpoint, t.region, t.roleArn)
	if err != nil {
		return nil, err
	}
	k := &mountKeys{ssoUser: src.User(), renew: creds, src: src}
	return k, k.refresh()
}

// refresh reads the current SSO keys, a new exchange when they near their end.
func (k *mountKeys) refresh() error {
	if k.renew == nil {
		return nil
	}
	v, err := k.renew.Get()
	if err != nil {
		return err
	}
	exp, _ := k.renew.ExpiresAt()
	k.value = s3mount.Keys{AccessKey: v.AccessKeyID, SecretKey: v.SecretAccessKey, SessionToken: v.SessionToken, Expiration: exp}
	return nil
}

// holdsFiles: dir has a regular file somewhere below it. A mount over it would hide them.
func holdsFiles(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

const (
	backupMountStartTimeout = time.Minute
	backupMountStopGrace    = 15 * time.Second
	backupMountProbeTimeout = 30 * time.Second
	backupMountRenewBefore  = 5 * time.Minute
)

// RefreshBackupMount makes the backups folder mount what the settings say: mounted (local
// backups moved into the bucket first), remounted after a change, unmounted when turned off;
// mounted, it checks the mount still answers and kills a stalled helper. A failure is retried
// at the next refresh. Never blocks: the helper starts, and the move runs, in the background.
func (cluster *Cluster) RefreshBackupMount() {
	cluster.ensureBackupMountProvider()
	bm := &cluster.backupMount
	bm.mu.Lock()
	defer bm.mu.Unlock()
	defer cluster.publishBackupMountLocked() // runs before the unlock
	if bm.starting || bm.moving {
		return
	}
	t, err := cluster.backupMountTarget()
	sig := ""
	if t != nil {
		sig = t.sig
	}
	if err == nil && sig != "" && sig == bm.signature && bm.state.Mounted {
		go cluster.probeBackupMount(bm.helper)
		return
	}
	if err == nil && sig == "" && bm.signature == "" && bm.helper == nil {
		bm.state = BackupMountState{}
		return
	}
	mountPoint := cluster.backupMountPoint()
	if bm.helper != nil {
		h := bm.helper
		bm.helper, bm.signature = nil, ""
		bm.mu.Unlock()
		cluster.stopMountHelper(h)
		bm.mu.Lock()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: %s unmounted", mountPoint)
	}
	st := BackupMountState{Enabled: cluster.Conf.BackupStreaming, Provider: cluster.Conf.BackupStreamingProvider, MountPoint: mountPoint, At: time.Now()}
	var keys *mountKeys
	if err == nil && t != nil {
		keys, err = cluster.backupMountKeys(t)
	}
	if err == nil && t != nil {
		st.Bucket, _ = t.spec(keys.ssoUser)
		if _, ferr := cluster.mountHelperBinary(); ferr != nil {
			err = ferr
		}
	}
	if err != nil {
		st.Error = err.Error()
		if bm.state.Error != st.Error {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s", err)
		}
	}
	st.LocalKeptAt = bm.state.LocalKeptAt
	bm.state = st
	if err != nil || t == nil {
		return
	}
	// a mount left by a previous run (replication-manager killed before it stopped its helper):
	// detached, never mistaken for local backups
	if bm.helper == nil && isMountPoint(mountPoint) {
		lazyUnmount(mountPoint)
		if isMountPoint(mountPoint) {
			bm.state.Error = mountPoint + " is still mounted by a previous run and could not be detached"
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Backups folder mount: a mount left on %s by a previous run detached", mountPoint)
	}
	os.MkdirAll(mountPoint, os.ModePerm)
	// local backups: uploaded into the bucket first, their records with them, so the catalogue
	// finds them at the same paths once mounted; never during a backup
	if holdsFiles(mountPoint) {
		if cluster.IsInBackup() {
			bm.state.Error = "a backup is running: the local backups move into the bucket after it"
			return
		}
		bm.moving = true
		bm.state.Moving = true
		go cluster.moveLocalBackupsIntoBucket(t, keys)
		return
	}
	// the empty folders replication-manager made while unmounted (one per server): FUSE 2
	// refuses a mount point that is not empty; holdsFiles said there is no file below it
	removeEmptyDirs(mountPoint)
	bm.starting = true
	go cluster.startMountHelper(t, keys)
}

// removeEmptyDirs removes the empty folders under dir, deepest first, never dir itself nor a
// folder holding anything.
func removeEmptyDirs(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != dir {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // fails, and keeps it, when the folder is not empty
	}
}

// mountHelperBinary finds repman-s3mount next to the plugins, signed when the key is there.
func (cluster *Cluster) mountHelperBinary() (string, error) {
	for _, dir := range cluster.archivePluginDirs() {
		p := filepath.Join(dir, s3mount.BinaryName)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			if err := cluster.archivePluginSigned(p); err != nil {
				return "", fmt.Errorf("%s: %w", s3mount.BinaryName, err)
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", s3mount.BinaryName, strings.Join(cluster.archivePluginDirs(), ", "))
}

// mountHelper is a running repman-s3mount.
type mountHelper struct {
	cmd       *exec.Cmd
	done      chan struct{}
	stopRenew chan struct{}
	point     string
}

// backupMountKeysFile is the helper's keys file, readable by repman's user only.
func (cluster *Cluster) backupMountKeysFile() string {
	return filepath.Join(cluster.WorkingDir, ".s3mount", "keys.json")
}

// startMountHelper starts repman-s3mount and waits for its status line; mounted, it watches
// its exit and, for the SSO, renews its keys file before the keys end.
func (cluster *Cluster) startMountHelper(t *mountTarget, keys *mountKeys) {
	bm := &cluster.backupMount
	fail := func(err error) {
		bm.mu.Lock()
		bm.starting = false
		bm.state.Mounted = false
		bm.state.Error = err.Error()
		bm.state.At = time.Now()
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s", err)
	}
	bin, err := cluster.mountHelperBinary()
	if err != nil {
		fail(err)
		return
	}
	keysFile := cluster.backupMountKeysFile()
	if err := os.MkdirAll(filepath.Dir(keysFile), 0o700); err != nil {
		fail(err)
		return
	}
	if err := s3mount.WriteKeys(keysFile, keys.value); err != nil {
		fail(err)
		return
	}
	spec, _ := t.spec(keys.ssoUser)
	point := cluster.backupMountPoint()
	var in bytes.Buffer
	json.NewEncoder(&in).Encode(s3mount.Config{MountPoint: point, Bucket: spec, Endpoint: t.endpoint, Region: t.region, KeysFile: keysFile, Debug: cluster.Conf.BackupStreamingDebug})
	cmd := exec.Command(bin)
	helperDiesWithParent(cmd)
	cmd.Env = archivePluginBaseEnv()
	cmd.Stdin = &in
	cmd.Stderr = io.Discard
	out, err := cmd.StdoutPipe()
	if err != nil {
		fail(err)
		return
	}
	if err := cmd.Start(); err != nil {
		fail(err)
		return
	}
	h := &mountHelper{cmd: cmd, done: make(chan struct{}), stopRenew: make(chan struct{}), point: point}
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		if sc.Scan() {
			line <- sc.Text()
		}
		close(line)
		io.Copy(io.Discard, out)
	}()
	go func() {
		cmd.Wait()
		close(h.done)
	}()
	var status s3mount.Status
	select {
	case l, ok := <-line:
		if !ok || json.Unmarshal([]byte(l), &status) != nil {
			status.Error = "the helper exited without a status"
		}
	case <-time.After(backupMountStartTimeout):
		status.Error = "the helper did not mount within " + backupMountStartTimeout.String()
	}
	if !status.Mounted {
		cluster.killMountHelper(h)
		fail(fmt.Errorf("mount of %s on %s: %s", spec, point, status.Error))
		return
	}
	bm.mu.Lock()
	bm.starting = false
	bm.helper = h
	bm.signature = t.sig
	bm.state.Mounted = true
	bm.state.Error = ""
	bm.state.Bucket = spec
	bm.state.At = time.Now()
	cluster.publishBackupMountLocked()
	bm.mu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: %s mounted on %s (repman-s3mount pid %d)", spec, point, cmd.Process.Pid)

	if keys.renew != nil {
		go cluster.renewMountKeys(h, keys, keysFile)
	}
	<-h.done
	close(h.stopRenew)
	bm.mu.Lock()
	if bm.helper == h {
		// it ended on its own: the mount is gone, retried at the next refresh
		bm.helper, bm.signature = nil, ""
		bm.state.Mounted = false
		bm.state.Error = "repman-s3mount exited: " + exitText(cmd)
		bm.state.At = time.Now()
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		lazyUnmount(point)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: repman-s3mount exited (%s), unmounted %s", exitText(cmd), point)
		return
	}
	bm.mu.Unlock()
}

func exitText(cmd *exec.Cmd) string {
	if cmd.ProcessState == nil {
		return "unknown"
	}
	return cmd.ProcessState.String()
}

// renewMountKeys rewrites the keys file before the SSO keys end; the helper reads it again.
func (cluster *Cluster) renewMountKeys(h *mountHelper, keys *mountKeys, keysFile string) {
	for {
		wait := time.Until(keys.value.Expiration.Add(-backupMountRenewBefore))
		if wait < time.Minute {
			wait = time.Minute
		}
		select {
		case <-h.stopRenew:
			return
		case <-time.After(wait):
		}
		if err := keys.refresh(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: the SSO keys were not renewed: %s", err)
			continue
		}
		if err := s3mount.WriteKeys(keysFile, keys.value); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: keys file: %s", err)
		}
	}
}

// probeBackupMount checks the mount answers; a stat that does not return within
// backupMountProbeTimeout means a stalled helper: it is killed, which makes every pending
// request on the mount fail instead of hang, and the next refresh mounts again.
func (cluster *Cluster) probeBackupMount(h *mountHelper) {
	if h == nil {
		return
	}
	answered := make(chan struct{})
	go func() {
		os.Stat(h.point)
		close(answered)
	}()
	select {
	case <-answered:
	case <-h.done:
	case <-time.After(backupMountProbeTimeout):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s did not answer within %s, repman-s3mount killed", h.point, backupMountProbeTimeout)
		cluster.killMountHelper(h)
	}
}

// stopMountHelper asks the helper to unmount and exit, kills it when it does not in time.
func (cluster *Cluster) stopMountHelper(h *mountHelper) {
	if h == nil || h.cmd.Process == nil {
		return
	}
	h.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-h.done:
	case <-time.After(backupMountStopGrace):
		cluster.killMountHelper(h)
	}
	lazyUnmount(h.point)
}

// killMountHelper kills the helper: the kernel then fails every request on its mount.
func (cluster *Cluster) killMountHelper(h *mountHelper) {
	if h == nil || h.cmd.Process == nil {
		return
	}
	h.cmd.Process.Kill()
	select {
	case <-h.done:
	case <-time.After(backupMountStopGrace):
	}
	lazyUnmount(h.point)
}

// lazyUnmount detaches a mount left behind by a helper that died, when there is one.
func lazyUnmount(point string) {
	if !isMountPoint(point) {
		return
	}
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if p, err := exec.LookPath(bin); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), backupMountStopGrace)
			exec.CommandContext(ctx, p, "-uz", point).Run()
			cancel()
			return
		}
	}
}

// isMountPoint: point is mounted (from /proc/self/mounts, never by touching point itself,
// which a dead FUSE mount would hang).
func isMountPoint(point string) bool {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return false
	}
	clean := filepath.Clean(point)
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 1 && f[1] == clean {
			return true
		}
	}
	return false
}

// checkBackupMountStates, every tick, from memory only: CINF0019 while the local backups
// move into the bucket, WARN0251 while a mount turned on is not mounted.
func (cluster *Cluster) checkBackupMountStates() {
	bm := &cluster.backupMount
	bm.mu.Lock()
	st, moving := bm.state, bm.moving
	bm.mu.Unlock()
	switch {
	case moving:
		cluster.SetState("CINF0019", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0019"], cluster.Name, st.Bucket, st.MovedFiles, st.TotalFiles, st.MovedBytes, st.TotalBytes), ErrFrom: "BACKUP"})
	case cluster.Conf.BackupStreaming && !st.Mounted && st.Error != "":
		cluster.SetState("WARN0251", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0251"], cluster.Name, st.Error), ErrFrom: "BACKUP"})
	}
}

// backupFolderMoving refuses a backup while the local backups move into the bucket: the
// CINF0019 state, or the move just started (before the tick raised it).
func (cluster *Cluster) backupFolderMoving() error {
	bm := &cluster.backupMount
	bm.mu.Lock()
	moving := bm.moving
	bm.mu.Unlock()
	if moving || (cluster.StateMachine != nil && cluster.StateMachine.IsInState("CINF0019")) {
		return errors.New("backup refused: the cluster's backups are moving into the S3 bucket of its backups folder mount (CINF0019)")
	}
	return nil
}

// moveLocalBackupsIntoBucket uploads the cluster's local backups into the bucket's folder,
// each file checked by its size in the bucket, straight through the S3 API (no FUSE mount, so
// nothing can hang the monitor), then keeps the local folder aside as <cluster>.local-<time>
// (never deleted) and leaves the mount to the next refresh. On any failure the local folder
// stays as it was; files already uploaded are uploaded again next time.
func (cluster *Cluster) moveLocalBackupsIntoBucket(t *mountTarget, keys *mountKeys) {
	bm := &cluster.backupMount
	local := cluster.backupMountPoint()
	spec, prefix := t.spec(keys.ssoUser)
	finish := func(err error, keptAt string) {
		bm.mu.Lock()
		bm.moving = false
		bm.state.Moving = false
		bm.state.At = time.Now()
		if err != nil {
			bm.state.Error = err.Error()
		} else {
			bm.state.Error = ""
			bm.state.LocalKeptAt = keptAt
		}
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: the local backups did not move into %s: %s", spec, err)
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: the local backups moved into %s, the local copy kept as %s", spec, keptAt)
		go cluster.RefreshBackupMount()
	}

	// what moves
	var files []string
	var total int64
	walkErr := filepath.WalkDir(local, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				files = append(files, path)
				total += info.Size()
			}
		}
		return nil
	})
	if walkErr != nil {
		finish(fmt.Errorf("reading %s: %w", local, walkErr), "")
		return
	}
	bm.mu.Lock()
	bm.state.Bucket = spec
	bm.state.TotalFiles, bm.state.TotalBytes, bm.state.MovedFiles, bm.state.MovedBytes = len(files), total, 0, 0
	cluster.publishBackupMountLocked()
	bm.mu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: uploading %d local files (%d bytes) of %s into %s", len(files), total, local, spec)

	ctx := context.Background()
	for _, src := range files {
		if cluster.IsInBackup() {
			finish(errors.New("a backup started while the local backups moved: moved again at the next refresh"), "")
			return
		}
		if err := keys.refresh(); err != nil {
			finish(err, "")
			return
		}
		client, err := s3helper.NewUploadClient(keys.value.AccessKey, keys.value.SecretKey, keys.value.SessionToken, t.region, t.endpoint)
		if err != nil {
			finish(err, "")
			return
		}
		rel, _ := filepath.Rel(local, src)
		key := strings.TrimSuffix(prefix, "/") + "/" + filepath.ToSlash(rel)
		n, err := s3helper.UploadFile(ctx, client, t.bucket, key, src)
		if err != nil {
			finish(fmt.Errorf("uploading %s: %w", rel, err), "")
			return
		}
		got, err := s3helper.ObjectSize(ctx, client, t.bucket, key)
		if err != nil || got != n {
			finish(fmt.Errorf("checking %s in the bucket: %d bytes there, %d local (%v)", rel, got, n, err), "")
			return
		}
		bm.mu.Lock()
		bm.state.MovedFiles++
		bm.state.MovedBytes += n
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
	}

	// the local copy aside, an empty folder for the mount
	keptAt := local + ".local-" + time.Now().UTC().Format("20060102-150405")
	if err := os.Rename(local, keptAt); err != nil {
		finish(fmt.Errorf("keeping the local copy as %s: %w", keptAt, err), "")
		return
	}
	if err := os.MkdirAll(local, os.ModePerm); err != nil {
		finish(err, keptAt)
		return
	}
	finish(nil, keptAt)
}

// UnmountBackupFolder stops the backups folder mount's helper, at the cluster's stop.
func (cluster *Cluster) UnmountBackupFolder() {
	bm := &cluster.backupMount
	bm.mu.Lock()
	h := bm.helper
	bm.helper, bm.signature = nil, ""
	bm.state.Mounted = false
	cluster.publishBackupMountLocked()
	bm.mu.Unlock()
	cluster.stopMountHelper(h)
}

// publishBackupMountLocked copies the state where the cluster's JSON shows it; bm.mu held.
func (cluster *Cluster) publishBackupMountLocked() {
	st := cluster.backupMount.state
	st.Enabled = cluster.Conf.BackupStreaming
	st.Provider = cluster.Conf.BackupStreamingProvider
	if st.MountPoint == "" {
		st.MountPoint = cluster.backupMountPoint()
	}
	cluster.BackupMount = st
}

// BackupMountStatus is the backups folder mount's state.
func (cluster *Cluster) BackupMountStatus() BackupMountState {
	bm := &cluster.backupMount
	bm.mu.Lock()
	defer bm.mu.Unlock()
	cluster.publishBackupMountLocked()
	return cluster.BackupMount
}
