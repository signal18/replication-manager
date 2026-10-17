// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// repman-s3mount serves the backups folder mount of one cluster (#2091): it mounts an S3
// bucket's folder with FUSE (goofys) and stays in the foreground until SIGTERM, when it
// unmounts. Its S3 keys come from the keys file, read again whenever they near their end, so
// replication-manager renews short-lived keys without a remount. See package s3mount.
package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/signal18/replication-manager/cluster/s3mount"
	goofys "github.com/signal18/replication-manager/goofys/api"
	common "github.com/signal18/replication-manager/goofys/api/common"
)

// fileKeys is the credentials source of the mount: the keys file, read again a minute before
// the keys it holds end (static keys: read once).
type fileKeys struct {
	path string
	mu   sync.Mutex
	exp  time.Time
}

func (f *fileKeys) Retrieve() (credentials.Value, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := s3mount.ReadKeys(f.path)
	if err != nil {
		return credentials.Value{}, err
	}
	f.exp = k.Expiration
	return credentials.Value{AccessKeyID: k.AccessKey, SecretAccessKey: k.SecretKey, SessionToken: k.SessionToken, ProviderName: "repman-s3mount"}, nil
}

func (f *fileKeys) IsExpired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.exp.IsZero() && time.Now().After(f.exp.Add(-time.Minute))
}

func status(s s3mount.Status) {
	s3mount.WriteStatus(os.Stdout, s)
}

func main() {
	cfg, err := s3mount.ReadConfig(os.Stdin)
	if err != nil {
		status(s3mount.Status{Error: err.Error()})
		os.Exit(1)
	}
	keys := &fileKeys{path: cfg.KeysFile}
	if _, err := keys.Retrieve(); err != nil {
		status(s3mount.Status{Error: err.Error()})
		os.Exit(1)
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 2 * time.Minute
	}
	region := cfg.Region
	backend := (&common.S3Config{Region: region, RegionSet: region != "", Credentials: credentials.NewCredentials(keys)}).Init()
	flags := &common.FlagStorage{
		MountPoint:  cfg.MountPoint,
		DirMode:     0o755,
		FileMode:    0o644,
		Endpoint:    cfg.Endpoint,
		DebugS3:     cfg.Debug,
		HTTPTimeout: cfg.HTTPTimeout,
		Backend:     backend,
	}
	if err := os.MkdirAll(cfg.MountPoint, 0o755); err != nil {
		status(s3mount.Status{Error: err.Error()})
		os.Exit(1)
	}
	_, mfs, err := goofys.Mount(context.Background(), cfg.Bucket, flags)
	if err != nil {
		status(s3mount.Status{Error: err.Error()})
		os.Exit(1)
	}
	status(s3mount.Status{Mounted: true})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		mfs.Join(context.Background())
		close(done)
	}()
	select {
	case <-sig:
		goofys.TryUnmount(cfg.MountPoint)
		<-done
	case <-done:
	}
}
