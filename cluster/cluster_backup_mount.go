// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/s3fuse"
	"github.com/signal18/replication-manager/utils/s3helper"
	"github.com/signal18/replication-manager/utils/state"
)

// The backups folder mount (#2091): with backup-streaming, the cluster's backups folder
// <working-dir>/backups/<cluster> is a mount of backup-streaming-bucket, its folder
// <cluster>/backups, under the instance's URI on Cloud18 (backupMountPrefix). The endpoint,
// region and keys are those of an S3 provider of the cluster (backup-streaming-provider);
// with none set, one is added from the backup-streaming-* values and set. The mount is served in
// this process by utils/s3fuse (GeeseFS), every FUSE request answered within its deadline, so a
// stalled S3 fails a write instead of freezing the monitor.

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
	// requests answered EIO past their deadline (S3 too slow or unreachable), and the last one
	LateOps  int    `json:"lateOps,omitempty"`
	LastLate string `json:"lastLate,omitempty"`
}

type backupMount struct {
	mu        sync.Mutex
	starting  bool          // the mount is being made
	moving    bool          // the local backups are being uploaded into the bucket
	signature string        // what is mounted, "" for nothing
	mount     *s3fuse.Mount // the mount, served in this process
	lastLate  time.Time     // the last request answered past its deadline
	state     BackupMountState
	// state.Mounted, published for readers that never wait for mu, held across a mount's
	// network calls: the restore catalogue on the monitor tick
	mounted atomic.Bool
}

// backupMountPoint is the cluster's backups folder.
func (cluster *Cluster) backupMountPoint() string {
	return filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, cluster.Name)
}

// bucketClusterFolder is the cluster's folder in a bucket: the cluster name, under the
// instance's URI on Cloud18 (<cloud18-domain>/<cloud18-sub-domain>-<cloud18-sub-domain-zone>,
// as its secrets and billing name it), so instances sharing a bucket never mix. On the Cloud18
// SSO it is itself under the SSO user's folder (mountTarget.spec, archiveBucketFolder).
func (cluster *Cluster) bucketClusterFolder() string {
	c := cluster.Conf
	if c.Cloud18 && c.Cloud18Domain != "" && c.Cloud18SubDomain != "" {
		return c.Cloud18Domain + "/" + c.Cloud18SubDomain + "-" + c.Cloud18SubDomainZone + "/" + cluster.Name
	}
	return cluster.Name
}

// The cluster's folder holds its backups folder and its archives side by side, so the mounted
// backups folder never shows an archive's files.
const (
	bucketBackupsFolder = "backups"
	bucketArchiveFolder = "archive"
)

// backupMountPrefix is the folder of the bucket mounted: backups in the cluster's folder.
func (cluster *Cluster) backupMountPrefix() string {
	return cluster.bucketClusterFolder() + "/" + bucketBackupsFolder
}

// archiveBucketPrefix is the default folder of the cluster's archive name in a bucket:
// archive/<name> in the cluster's folder, next to its backups folder.
func (cluster *Cluster) archiveBucketPrefix(name string) string {
	return cluster.bucketClusterFolder() + "/" + bucketArchiveFolder + "/" + name
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

// mountKeys are the S3 keys of a target, renewed by themselves for the SSO, and the SSO user
// its folder starts with.
type mountKeys struct {
	creds   *credentials.Credentials
	ssoUser string
}

func (cluster *Cluster) backupMountKeys(t *mountTarget) (*mountKeys, error) {
	if !t.sso {
		return &mountKeys{creds: credentials.NewStaticCredentials(t.accessKey, t.secretKey, "")}, nil
	}
	creds, src, err := cluster.newCloud18SSOCredentials(t.endpoint, t.region, t.roleArn)
	if err != nil {
		return nil, err
	}
	return &mountKeys{creds: creds, ssoUser: src.User()}, nil
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
	backupMountHTTPTimeout  = 30 * time.Second // each S3 request
	backupMountOpTimeout    = 2 * time.Minute  // each FUSE request (DeadlineFS)
	backupMountProbeTimeout = backupMountOpTimeout + 30*time.Second
	backupMountStopTimeout  = time.Minute
	backupMountLateWindow   = 10 * time.Minute // WARN0262 stays open that long after a late request
)

// RefreshBackupMount makes the backups folder mount what the settings say: mounted (local
// backups moved into the bucket first), remounted after a change, unmounted when turned off;
// mounted, it checks the mount still answers. A failure is retried at the next refresh.
// Never blocks: the mount and the move run in the background.
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
		go cluster.probeBackupMount()
		return
	}
	if err == nil && sig == "" && bm.signature == "" && bm.mount == nil {
		bm.state = BackupMountState{}
		return
	}
	mountPoint := cluster.backupMountPoint()
	if bm.mount != nil {
		m := bm.mount
		bm.mount, bm.signature = nil, ""
		bm.mu.Unlock()
		cluster.unmountBackupFolder(m)
		go cluster.resyncBackupCatalogue()
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
	// a mount left by a previous run: detached, never mistaken for local backups
	if err := cluster.detachDeadBackupMount(); err != nil {
		bm.state.Error = err.Error()
		return
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
	go cluster.startBackupMount(t, keys)
}

// detachDeadBackupMount detaches a mount left on the backups folder by a previous run
// (replication-manager killed, the mount it served in its process gone): the kernel keeps the
// mount point, every request on it answered "transport endpoint is not connected", so a
// backup into it fails. A mount this process serves is never touched (a config reload).
func (cluster *Cluster) detachDeadBackupMount() error {
	point := cluster.backupMountPoint()
	if !isMountPoint(point) || s3fuse.Serves(point) {
		return nil
	}
	lazyUnmount(point)
	if isMountPoint(point) {
		return fmt.Errorf("%s is still mounted by a previous run and could not be detached", point)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Backups folder mount: a mount left on %s by a previous run detached", point)
	return nil
}

// detachDeadBackupMountAtStart, at the cluster's start before its servers read their backups:
// the backups folder is a folder again at once, never read through a mount left by the
// previous run; its mount is made by startBackupFolder.
func (cluster *Cluster) detachDeadBackupMountAtStart() {
	if err := cluster.detachDeadBackupMount(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s", err)
	}
}

// startBackupFolder, at the cluster's start: its mount is made now, not at the first mount
// check a minute or two later; a backup in between would fail or land on the local disk.
func (cluster *Cluster) startBackupFolder() {
	go cluster.RefreshBackupMount()
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

// startBackupMount mounts the bucket's folder in this process (utils/s3fuse: GeeseFS behind a
// deadline on every request), then waits for the mount to end.
func (cluster *Cluster) startBackupMount(t *mountTarget, keys *mountKeys) {
	bm := &cluster.backupMount
	spec, _ := t.spec(keys.ssoUser)
	point := cluster.backupMountPoint()
	m, err := s3fuse.New(context.Background(), s3fuse.Options{
		MountPoint:  point,
		Bucket:      spec,
		Endpoint:    t.endpoint,
		Region:      t.region,
		Credentials: keys.creds,
		HTTPTimeout: backupMountHTTPTimeout,
		OpTimeout:   backupMountOpTimeout,
		Debug:       cluster.Conf.BackupStreamingDebug,
		OnLate: func(op string) {
			bm.mu.Lock()
			bm.lastLate = time.Now()
			bm.state.LateOps++
			bm.state.LastLate = op + " at " + bm.lastLate.UTC().Format(time.RFC3339)
			cluster.publishBackupMountLocked()
			bm.mu.Unlock()
		},
		OnPanic: func(op string, r interface{}, stack []byte) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s panicked, answered EIO: %v %s", op, r, stack)
		},
	})
	bm.mu.Lock()
	bm.starting = false
	bm.state.At = time.Now()
	if err != nil {
		bm.state.Mounted = false
		bm.state.Error = err.Error()
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s", err)
		return
	}
	bm.mount = m
	bm.signature = t.sig
	bm.state.Mounted = true
	bm.state.Error = ""
	bm.state.Bucket = spec
	cluster.publishBackupMountLocked()
	bm.mu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: %s mounted on %s", spec, point)
	go cluster.resyncBackupCatalogue()

	m.Join(context.Background())
	bm.mu.Lock()
	if bm.mount == m {
		// it ended on its own (unmounted from outside): retried at the next refresh
		bm.mount, bm.signature = nil, ""
		bm.state.Mounted = false
		bm.state.Error = "the mount ended"
		bm.state.At = time.Now()
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		lazyUnmount(point)
		go cluster.resyncBackupCatalogue()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s ended, mounted again at the next check", point)
		return
	}
	bm.mu.Unlock()
}

// resyncBackupCatalogue makes the catalogue the backups folder now in use, as a restart would
// load it, once the folder switched between local and the bucket (the mount came up, was
// turned off or ended): the folder holds each server's current backups, their history is in
// the archives. Run off the monitor tick.
func (cluster *Cluster) resyncBackupCatalogue() {
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		server.forgetFolderBackups()
		server.FetchLastBackupMetadata() // the folder's records
		server.lastBackupsFromCatalogue()
	}
}

// forgetFolderBackups drops from memory the backups of the server whose record is in its
// backups folder, for the folder now in use to load its own: the records of the previous
// folder stay where they are (the bucket, or the local folder kept aside) and come back with
// it, and a loaded record never replaces one in memory (LoadRegisteredBackupMetadata), so a record
// the move rewrote for the bucket would not be read. Kept: a backup being verified, and one
// that did not complete while a backup runs (the running one writes its record at its end); a
// failed one goes, as a restart forgets it.
func (server *ServerMonitor) forgetFolderBackups() {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return
	}
	dir := filepath.Clean(server.GetMyBackupDirectoryPath()) + string(os.PathSeparator)
	running := cluster.IsInBackup()
	cluster.BackupMetaMap.Callback(func(id int64, m *backupmgr.BackupMetadata) bool {
		// a replaced backup is in no folder, only in the archive: the global catalogue keeps it
		if m == nil || m.Source != server.URL || (!m.Completed && running) || m.Replaced {
			return true
		}
		if _, verifying := cluster.backupVerifying.Load(id); verifying {
			return true
		}
		rec := m.MetaFile
		if rec == "" {
			rec = server.backupMetaFilePath(m)
		}
		if rec == "" || !strings.HasPrefix(filepath.Clean(rec), dir) {
			return true
		}
		cluster.BackupMetaMap.Delete(id)
		cluster.backupChanges.Delete(id)
		return true
	})
}

// lastBackupsFromCatalogue moves the server's last logical and physical backups, when they
// left the catalogue, to its newest ones left.
func (server *ServerMonitor) lastBackupsFromCatalogue() {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	for _, last := range []struct {
		method string
		meta   **backupmgr.BackupMetadata
	}{{"logical", &server.LastBackupMeta.Logical}, {"physical", &server.LastBackupMeta.Physical}} {
		if m := *last.meta; m == nil || m.Id == 0 || cluster.BackupMetaMap.Get(m.Id) != nil {
			continue
		}
		if _, newest := server.GetLatestBackupMeta(last.method, true); newest != nil {
			*last.meta = newest
		} else {
			*last.meta = new(backupmgr.BackupMetadata)
		}
	}
}

// probeBackupMount checks the mount answers: with every request bounded, a stat that does not
// return in time means the mount is broken beyond its deadline, said in the mount state.
func (cluster *Cluster) probeBackupMount() {
	point := cluster.backupMountPoint()
	answered := make(chan struct{})
	go func() {
		os.Stat(point)
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(backupMountProbeTimeout):
		bm := &cluster.backupMount
		bm.mu.Lock()
		bm.state.Error = fmt.Sprintf("%s did not answer within %s", point, backupMountProbeTimeout)
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s did not answer within %s", point, backupMountProbeTimeout)
	}
}

// unmountBackupFolder flushes and unmounts m, bounded: a mount that does not let go in time is
// detached lazily.
func (cluster *Cluster) unmountBackupFolder(m *s3fuse.Mount) {
	if m == nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- m.Unmount() }()
	select {
	case err := <-done:
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Backups folder mount: unmount: %s", err)
			lazyUnmount(cluster.backupMountPoint())
		}
	case <-time.After(backupMountStopTimeout):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: unmount did not end within %s, detached", backupMountStopTimeout)
		lazyUnmount(cluster.backupMountPoint())
	}
}

// lazyUnmount detaches a mount left behind by a helper that died, when there is one.
func lazyUnmount(point string) {
	if !isMountPoint(point) {
		return
	}
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if p, err := exec.LookPath(bin); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), backupMountStopTimeout)
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
// move into the bucket, WARN0261 while a mount turned on is not mounted.
func (cluster *Cluster) checkBackupMountStates() {
	bm := &cluster.backupMount
	bm.mu.Lock()
	st, moving, lastLate := bm.state, bm.moving, bm.lastLate
	bm.mu.Unlock()
	if st.Mounted && !lastLate.IsZero() && time.Since(lastLate) < backupMountLateWindow {
		cluster.SetState("WARN0262", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0262"], cluster.Name, st.LateOps, backupMountOpTimeout, st.LastLate), ErrFrom: "BACKUP"})
	}
	switch {
	case moving:
		cluster.SetState("CINF0019", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0019"], cluster.Name, st.Bucket, st.MovedFiles, st.TotalFiles, st.MovedBytes, st.TotalBytes), ErrFrom: "BACKUP"})
	case cluster.Conf.BackupStreaming && !st.Mounted && st.Error != "":
		cluster.SetState("WARN0261", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0261"], cluster.Name, st.Error), ErrFrom: "BACKUP"})
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

	// the records of the backups whose fingerprints hold go up last, with the fingerprint of
	// their files in the bucket: the mount gives other inodes and times, the ETags hold
	proven := cluster.backupsProvenForMove(local)
	sort.SliceStable(files, func(i, j int) bool {
		_, ri := proven[files[i]]
		_, rj := proven[files[j]]
		return !ri && rj
	})
	etags := map[string]string{}

	ctx := context.Background()
	for _, src := range files {
		if cluster.IsInBackup() {
			finish(errors.New("a backup started while the local backups moved: moved again at the next refresh"), "")
			return
		}
		v, err := keys.creds.Get() // renewed when near their end (the SSO)
		if err != nil {
			finish(err, "")
			return
		}
		client, err := s3helper.NewUploadClient(v.AccessKeyID, v.SecretAccessKey, v.SessionToken, t.region, t.endpoint)
		if err != nil {
			finish(err, "")
			return
		}
		rel, _ := filepath.Rel(local, src)
		key := strings.TrimSuffix(prefix, "/") + "/" + filepath.ToSlash(rel)
		var n int64
		if id, ok := proven[src]; ok {
			if data := cluster.recordWithBucketFingerprint(src, id, etags); data != nil {
				n, err = s3helper.UploadBytes(ctx, client, t.bucket, key, data)
			} else {
				n, err = s3helper.UploadFile(ctx, client, t.bucket, key, src)
			}
		} else {
			n, err = s3helper.UploadFile(ctx, client, t.bucket, key, src)
		}
		if err != nil {
			finish(fmt.Errorf("uploading %s: %w", rel, err), "")
			return
		}
		got, etag, err := s3helper.ObjectSizeETag(ctx, client, t.bucket, key)
		if err != nil || got != n {
			finish(fmt.Errorf("checking %s in the bucket: %d bytes there, %d local (%v)", rel, got, n, err), "")
			return
		}
		etags[src] = etag
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

// backupsProvenForMove returns, by their record's path, the completed backups of the cluster
// whose file and record are in the local folder and whose recorded fingerprints (registration,
// passed verification) still hold: the move proves their files went up by their size and
// records their bucket fingerprint. A backup changed already, or without a fingerprint, goes
// up as it is (WARN0249, or adopted on the mount).
func (cluster *Cluster) backupsProvenForMove(local string) map[string]int64 {
	proven := map[string]int64{}
	if cluster.BackupMetaMap == nil {
		return proven
	}
	dir := filepath.Clean(local) + string(os.PathSeparator)
	under := func(p string) bool { return p != "" && strings.HasPrefix(filepath.Clean(p), dir) }
	cluster.BackupMetaMap.Callback(func(id int64, m *backupmgr.BackupMetadata) bool {
		server := cluster.GetServerFromURL(m.Source)
		if server == nil || !under(m.Dest) {
			return true
		}
		server.backupMetaMutex.Lock()
		completed, removed := m.Completed, m.OriginalRemoved
		var recs []backupmgr.ArtifactFingerprint
		if m.Registration != nil {
			recs = append(recs, m.Registration.Artifact)
		}
		if v := m.Verification; v != nil && v.Status == backupmgr.BackupVerificationPassed && v.Artifact != nil {
			recs = append(recs, *v.Artifact)
		}
		rec := server.backupMetaFilePath(m)
		server.backupMetaMutex.Unlock()
		if !completed || removed || len(recs) == 0 || !under(rec) {
			return true
		}
		for i := range recs {
			if !artifactUnchangedBounded(m.Dest, &recs[i], backupProofWalkMax) {
				return true
			}
		}
		// a record rewritten by a newer backup of the same file is that one's
		if prev, ok := proven[filepath.Clean(rec)]; !ok || id > prev {
			proven[filepath.Clean(rec)] = id
		}
		return true
	})
	return proven
}

// recordWithBucketFingerprint returns the record file rec of backup id with its registration
// and passed verification fingerprinted as its files are in the bucket, from the ETags their
// upload got; nil when the record is not that backup's or its files did not all go up (it then
// goes up as it is).
func (cluster *Cluster) recordWithBucketFingerprint(rec string, id int64, etags map[string]string) []byte {
	raw, err := os.ReadFile(rec)
	if err != nil {
		return nil
	}
	meta := new(backupmgr.BackupMetadata)
	if err := json.Unmarshal(raw, meta); err != nil || meta.Id != id {
		return nil
	}
	fp, err := artifactFingerprint(meta.Dest, backupProofWalkMax, func(file string) (string, error) {
		if etag := etags[filepath.Clean(file)]; etag != "" {
			return etag, nil
		}
		return "", os.ErrNotExist
	})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Backups folder mount: backup %d goes into the bucket with its disk fingerprint: %s", id, err)
		return nil
	}
	if meta.Registration != nil {
		meta.Registration.Artifact = *fp
	}
	if v := meta.Verification; v != nil && v.Status == backupmgr.BackupVerificationPassed && v.Artifact != nil {
		bucket := *fp
		v.Artifact = &bucket
	}
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return nil
	}
	return data
}

// UnmountBackupFolder unmounts the backups folder, at the cluster's stop.
func (cluster *Cluster) UnmountBackupFolder() {
	bm := &cluster.backupMount
	bm.mu.Lock()
	m := bm.mount
	bm.mount, bm.signature = nil, ""
	bm.state.Mounted = false
	cluster.publishBackupMountLocked()
	bm.mu.Unlock()
	cluster.unmountBackupFolder(m)
}

// publishBackupMountLocked copies the state where the cluster's JSON shows it; bm.mu held.
func (cluster *Cluster) publishBackupMountLocked() {
	st := cluster.backupMount.state
	cluster.backupMount.mounted.Store(st.Mounted)
	st.Enabled = cluster.Conf.BackupStreaming
	st.Provider = cluster.Conf.BackupStreamingProvider
	if st.MountPoint == "" {
		st.MountPoint = cluster.backupMountPoint()
	}
	cluster.BackupMount = st
}

// mountedBackupsFolder is the backups folder when the bucket is mounted on it, else "". It
// takes no lock.
func (cluster *Cluster) mountedBackupsFolder() string {
	if !cluster.backupMount.mounted.Load() {
		return ""
	}
	return cluster.backupMountPoint()
}

// BackupMountStatus is the backups folder mount's state.
func (cluster *Cluster) BackupMountStatus() BackupMountState {
	bm := &cluster.backupMount
	bm.mu.Lock()
	defer bm.mu.Unlock()
	cluster.publishBackupMountLocked()
	return cluster.BackupMount
}
