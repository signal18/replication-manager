// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	goofys "github.com/signal18/replication-manager/goofys/api"
	common "github.com/signal18/replication-manager/goofys/api/common"
)

// The backups folder mount (#2091): with backup-streaming, the cluster's backups folder
// <working-dir>/backups/<cluster> is a mount of backup-streaming-bucket, its folder <cluster>,
// under the instance's URI on Cloud18 (backupMountPrefix). The endpoint,
// region and keys are those of an S3 provider of the cluster (backup-streaming-provider);
// with none set, one is added from the backup-streaming-* values and set.

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
}

type backupMount struct {
	mu        sync.Mutex
	mounting  bool
	signature string // what is mounted, "" for nothing
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

// ensureBackupMountProvider: backup-streaming on and no provider set, the provider is
// added from the backup-streaming-* values when the cluster has none of that name, and set.
func (cluster *Cluster) ensureBackupMountProvider() {
	if !cluster.Conf.BackupStreaming || cluster.Conf.BackupStreamingProvider != "" {
		return
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

// backupMountSSO is what an SSO provider's mount needs to get its keys, at the mount only.
type backupMountSSO struct {
	endpoint, roleArn, bucket, prefix string
	backend                           *common.S3Config
}

// backupMountTarget is what the mount should be: its signature ("" for no mount), the
// goofys bucket spec and storage flags; for an SSO provider, what gets the keys at the
// mount (the bucket's folder then starts with the SSO user).
func (cluster *Cluster) backupMountTarget() (string, string, *common.FlagStorage, *backupMountSSO, error) {
	if !cluster.Conf.BackupStreaming {
		return "", "", nil, nil, nil
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
		return "", "", nil, nil, fmt.Errorf("no S3 provider %q", name)
	}
	if err := cluster.s3ProviderUsable(*provider); err != nil {
		return "", "", nil, nil, err
	}
	if cluster.Conf.BackupStreamingBucket == "" {
		return "", "", nil, nil, errors.New("no bucket (backup-streaming-bucket)")
	}
	endpoint, region, accessKey, secretKey := cluster.resolveProviderEffectiveValues(provider)
	if endpoint == "" {
		return "", "", nil, nil, fmt.Errorf("S3 provider %q has no endpoint", name)
	}
	bucket := cluster.Conf.BackupStreamingBucket + ":" + cluster.backupMountPrefix()
	backend := (&common.S3Config{AccessKey: accessKey, SecretKey: secretKey, Region: region}).Init()
	flags := &common.FlagStorage{
		MountPoint: cluster.backupMountPoint(),
		DirMode:    os.ModePerm,
		FileMode:   os.ModePerm,
		Endpoint:   endpoint,
		DebugS3:    cluster.Conf.BackupStreamingDebug,
		Backend:    backend,
	}
	var sso *backupMountSSO
	if provider.Cloud18SSO {
		sso = &backupMountSSO{endpoint: endpoint, roleArn: provider.RoleArn, bucket: cluster.Conf.BackupStreamingBucket, prefix: cluster.backupMountPrefix(), backend: backend}
		accessKey, secretKey = "cloud18-sso:"+cluster.Conf.Cloud18GitUser, provider.RoleArn
	}
	sig := fmt.Sprintf("%s|%s|%s|%s|%s|%x", name, bucket, endpoint, region, accessKey, sha256.Sum256([]byte(secretKey)))
	return sig, bucket, flags, sso, nil
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

// RefreshBackupMount makes the backups folder mount what the settings say: mounted,
// remounted after a change, unmounted when turned off. A failed mount is retried at the
// next refresh. Never blocks: the mount runs in the background.
func (cluster *Cluster) RefreshBackupMount() {
	cluster.ensureBackupMountProvider()
	bm := &cluster.backupMount
	bm.mu.Lock()
	defer bm.mu.Unlock()
	defer cluster.publishBackupMountLocked() // runs before the unlock
	if bm.mounting {
		return
	}
	sig, bucket, flags, sso, err := cluster.backupMountTarget()
	if err == nil && sig == bm.signature && (sig == "" || bm.state.Mounted) {
		return
	}
	mountPoint := cluster.backupMountPoint()
	if bm.signature != "" {
		if uerr := goofys.TryUnmount(mountPoint); uerr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: unmount %s: %s", mountPoint, uerr)
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: %s unmounted", mountPoint)
		bm.signature = ""
	}
	state := BackupMountState{Enabled: cluster.Conf.BackupStreaming, Provider: cluster.Conf.BackupStreamingProvider, Bucket: bucket, MountPoint: mountPoint, At: time.Now()}
	if err == nil && sig != "" {
		os.MkdirAll(mountPoint, os.ModePerm)
		if holdsFiles(mountPoint) {
			err = fmt.Errorf("%s holds local files: a mount would hide them, move them first", mountPoint)
		}
	}
	// an SSO provider: the keys, renewed inside the mount, and the SSO user's folder
	if err == nil && sso != nil {
		creds, src, cerr := cluster.newCloud18SSOCredentials(sso.endpoint, sso.roleArn)
		if cerr != nil {
			err = cerr
		} else {
			sso.backend.Credentials = creds
			bucket = sso.bucket + ":" + src.User() + "/" + sso.prefix
			state.Bucket = bucket
		}
	}
	if err != nil {
		state.Error = err.Error()
		if bm.state.Error != state.Error {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s", err)
		}
	}
	bm.state = state
	if err != nil || sig == "" {
		return
	}
	bm.mounting = true
	go func() {
		_, mfs, merr := goofys.Mount(context.Background(), bucket, flags)
		bm.mu.Lock()
		bm.mounting = false
		bm.state.At = time.Now()
		if merr != nil {
			bm.state.Error = merr.Error()
			cluster.publishBackupMountLocked()
			bm.mu.Unlock()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: %s on %s: %s", bucket, mountPoint, merr)
			return
		}
		bm.signature = sig
		bm.state.Mounted = true
		bm.state.Error = ""
		cluster.publishBackupMountLocked()
		bm.mu.Unlock()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backups folder mount: %s mounted on %s", bucket, mountPoint)
		mfs.Join(context.Background())
	}()
}

// UnmountBackupFolder unmounts the backups folder, at the cluster's stop.
func (cluster *Cluster) UnmountBackupFolder() {
	bm := &cluster.backupMount
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.signature == "" {
		return
	}
	if err := goofys.TryUnmount(cluster.backupMountPoint()); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Backups folder mount: unmount: %s", err)
		return
	}
	bm.signature = ""
	bm.state.Mounted = false
	cluster.publishBackupMountLocked()
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
