// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package s3fuse mounts an S3 bucket's folder as a folder, inside replication-manager's own
// process (#2091): GeeseFS (github.com/yandex-cloud/geesefs, the maintained successor of
// goofys: bounded S3 requests, retries, writes buffered and flushed in the background, real
// modifications, appends and renames) behind DeadlineFS, which answers every FUSE request in
// time so a stalled S3 can never hold replication-manager.
package s3fuse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseutil"
	"github.com/yandex-cloud/geesefs/core"
	"github.com/yandex-cloud/geesefs/core/cfg"
)

// Options is what a mount mounts.
type Options struct {
	MountPoint string
	Bucket     string // bucket or bucket:prefix
	Endpoint   string
	Region     string
	// Credentials: static or renewed (the Cloud18 SSO); asked again whenever they near their end
	Credentials *credentials.Credentials
	// HTTPTimeout bounds each S3 request; 0: 30 s
	HTTPTimeout time.Duration
	// OpTimeout bounds each FUSE request (DeadlineFS); 0: 2 minutes
	OpTimeout time.Duration
	// MemoryLimit for GeeseFS's buffers, in bytes; 0: 256 MiB
	MemoryLimit uint64
	Debug       bool
	OnLate      func(op string)
	OnPanic     func(op string, recovered interface{}, stack []byte)
}

// Mount is a mounted folder.
type Mount struct {
	point string
	fs    *core.Goofys
	mfs   *fuse.MountedFileSystem
}

// New mounts o.Bucket on o.MountPoint, owned by this process's user, and returns once mounted.
func New(ctx context.Context, o Options) (*Mount, error) {
	if o.MountPoint == "" || o.Bucket == "" || o.Credentials == nil {
		return nil, errors.New("s3fuse: mount point, bucket and credentials are required")
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 30 * time.Second
	}
	if o.OpTimeout <= 0 {
		o.OpTimeout = 2 * time.Minute
	}
	if o.MemoryLimit == 0 {
		o.MemoryLimit = 256 << 20
	}
	flags := cfg.DefaultFlags()
	flags.MountPoint = o.MountPoint
	flags.Endpoint = o.Endpoint
	flags.HTTPTimeout = o.HTTPTimeout
	flags.MemoryLimit = o.MemoryLimit
	flags.DebugS3 = o.Debug
	flags.Uid, flags.Gid = uint32(os.Getuid()), uint32(os.Getgid())
	s3 := (&cfg.S3Config{Region: o.Region, RegionSet: o.Region != "", Credentials: o.Credentials}).Init()
	flags.Backend = s3

	fs, err := core.NewGoofys(ctx, o.Bucket, flags)
	if err != nil || fs == nil {
		if err == nil {
			err = errors.New("initialization failed")
		}
		return nil, fmt.Errorf("s3fuse: %s: %w", o.Bucket, err)
	}
	served := DeadlineFS{Fs: core.NewGoofysFuse(fs), Timeout: o.OpTimeout, OnLate: o.OnLate, OnPanic: o.OnPanic}
	mountCfg := &fuse.MountConfig{
		FSName:                  o.Bucket,
		Subtype:                 "geesefs",
		DisableWritebackCaching: true,
		UseVectoredRead:         true,
		UseReadDirPlus:          true,
	}
	mfs, err := fuse.Mount(o.MountPoint, fuseutil.NewFileSystemServer(served), mountCfg)
	if err != nil {
		fs.Shutdown()
		return nil, fmt.Errorf("s3fuse: mount %s on %s: %w", o.Bucket, o.MountPoint, err)
	}
	return &Mount{point: o.MountPoint, fs: fs, mfs: mfs}, nil
}

// Join waits until the mount ends.
func (m *Mount) Join(ctx context.Context) error { return m.mfs.Join(ctx) }

// Unmount flushes what is buffered, unmounts and stops the file system.
func (m *Mount) Unmount() error {
	err := core.TryUnmount(m.point)
	m.fs.Shutdown()
	return err
}
