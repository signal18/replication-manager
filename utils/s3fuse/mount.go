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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
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

// mounted holds the mounts this process serves, by mount point.
var mounted sync.Map

// Serves tells whether path is on a mount this process serves: its files are S3 objects,
// their inodes and times are the mount's, their S3 ETags (the s3.etag attribute) the bucket's.
func Serves(path string) bool {
	path = filepath.Clean(path)
	return serving(path) != nil
}

func serving(path string) *Mount {
	path = filepath.Clean(path)
	var in *Mount
	mounted.Range(func(k, v any) bool {
		point := k.(string)
		if path == point || strings.HasPrefix(path, point+string(os.PathSeparator)) {
			in = v.(*Mount)
			return false
		}
		return true
	})
	return in
}

// ErrTooManyObjects: the listing holds more objects than asked for.
var ErrTooManyObjects = errors.New("more objects than asked for")

// ETags returns the S3 ETags (unquoted) of the objects under path, a file or a folder of a
// mount this process serves, by their path on the mount, as the bucket lists them: GeeseFS's
// own (the s3.etag attribute) is, after it uploaded a file in parts, the file's MD5 until the
// folder is listed again, not the bucket's. At most max objects (0: all of them).
func ETags(path string, max int) (map[string]string, error) {
	m := serving(path)
	if m == nil {
		return nil, fmt.Errorf("s3fuse: %s is on no mount", path)
	}
	rel, err := filepath.Rel(m.point, filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	key := m.prefix + filepath.ToSlash(rel)
	if rel == "." {
		key = m.prefix
	}
	etags := map[string]string{}
	add := func(items []core.BlobItemOutput, exact bool) error {
		for _, it := range items {
			if it.Key == nil || it.ETag == nil || strings.HasSuffix(*it.Key, "/") {
				continue
			}
			if exact != (*it.Key == key) {
				continue
			}
			if max > 0 && len(etags) >= max {
				return ErrTooManyObjects
			}
			etags[filepath.Join(m.point, filepath.FromSlash(strings.TrimPrefix(*it.Key, m.prefix)))] = strings.Trim(*it.ETag, `"`)
		}
		return nil
	}
	// the file itself
	out, err := m.lister.ListBlobs(&core.ListBlobsInput{Prefix: &key, MaxKeys: aws.Uint32(1)})
	if err != nil {
		return nil, fmt.Errorf("s3fuse: listing %s: %w", key, err)
	}
	if err := add(out.Items, true); err != nil || len(etags) == 1 {
		return etags, err
	}
	// a folder: every object under it
	dir := strings.TrimSuffix(key, "/") + "/"
	if key == "" {
		dir = ""
	}
	in := &core.ListBlobsInput{Prefix: &dir}
	for {
		out, err := m.lister.ListBlobs(in)
		if err != nil {
			return nil, fmt.Errorf("s3fuse: listing %s: %w", dir, err)
		}
		if err := add(out.Items, false); err != nil {
			return nil, err
		}
		if !out.IsTruncated || out.NextContinuationToken == nil {
			return etags, nil
		}
		in.ContinuationToken = out.NextContinuationToken
	}
}

// Mount is a mounted folder.
type Mount struct {
	point  string
	fs     *core.Goofys
	mfs    *fuse.MountedFileSystem
	lister *core.S3Backend // the bucket's listings, for the ETags
	prefix string          // the folder's key prefix in the bucket, "" or ending in /
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

	bucket, prefix, _ := strings.Cut(o.Bucket, ":")
	if prefix = strings.Trim(prefix, "/"); prefix != "" {
		prefix += "/"
	}
	lister, err := core.NewS3(bucket, flags, s3)
	if err != nil {
		return nil, fmt.Errorf("s3fuse: %s: %w", o.Bucket, err)
	}

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
	m := &Mount{point: filepath.Clean(o.MountPoint), fs: fs, mfs: mfs, lister: lister, prefix: prefix}
	// before anything else opens a file of the mount: without it the process can freeze
	if err := disablePoll(o.MountPoint); err != nil {
		m.Unmount()
		return nil, err
	}
	mounted.Store(m.point, m)
	return m, nil
}

// Join waits until the mount ends.
func (m *Mount) Join(ctx context.Context) error {
	err := m.mfs.Join(ctx)
	if ctx.Err() == nil { // the mount ended, not the wait
		mounted.Delete(m.point)
	}
	return err
}

// Unmount flushes what is buffered, unmounts and stops the file system.
func (m *Mount) Unmount() error {
	mounted.Delete(m.point)
	err := core.TryUnmount(m.point)
	m.fs.Shutdown()
	return err
}
