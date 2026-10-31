// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package s3fuse mounts an S3 bucket's folder as a folder (#2091), served by a child process of
// replication-manager's own binary (#2246): GeeseFS (github.com/yandex-cloud/geesefs, the maintained successor of
// goofys: bounded S3 requests, retries, writes buffered and flushed in the background, real
// modifications, appends and renames) behind DeadlineFS, which answers every FUSE request in
// time so a stalled S3 can never hold replication-manager. In the child, a fatal error of the
// file system (a stack overflow, which no recover catches) ends the child, never the monitor.
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
	// OnLog gets each line the mount's server prints (GeeseFS's log, a crash), bounded (may be nil)
	OnLog func(line string)
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

// Mount is a mounted folder, served by a child process of this binary (#2246).
type Mount struct {
	point  string
	server *server         // the child serving the mount
	lister *core.S3Backend // the bucket's listings, for the ETags: this process's own S3 client
	prefix string          // the folder's key prefix in the bucket, "" or ending in /
}

// s3Flags are GeeseFS's settings for o, and its S3 configuration.
func s3Flags(o Options) (*cfg.FlagStorage, *cfg.S3Config) {
	flags := cfg.DefaultFlags()
	flags.MountPoint = o.MountPoint
	flags.Endpoint = o.Endpoint
	flags.HTTPTimeout = o.HTTPTimeout
	flags.MemoryLimit = o.MemoryLimit
	flags.DebugS3 = o.Debug
	flags.Uid, flags.Gid = uint32(os.Getuid()), uint32(os.Getgid())
	s3 := (&cfg.S3Config{Region: o.Region, RegionSet: o.Region != "", Credentials: o.Credentials}).Init()
	flags.Backend = s3
	return flags, s3
}

func (o *Options) defaults() error {
	if o.MountPoint == "" || o.Bucket == "" || o.Credentials == nil {
		return errors.New("s3fuse: mount point, bucket and credentials are required")
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
	return nil
}

// New mounts o.Bucket on o.MountPoint, owned by this process's user, and returns once mounted.
// The file system runs in a child process of this binary (the backup-folder-mount command): a
// fatal error in it (a stack overflow, which no recover catches) ends the child, never this
// process; the kernel then fails what waits on the mount (ENOTCONN) and Join returns why.
func New(ctx context.Context, o Options) (*Mount, error) {
	if err := o.defaults(); err != nil {
		return nil, err
	}
	flags, s3 := s3Flags(o)
	bucket, prefix, _ := strings.Cut(o.Bucket, ":")
	if prefix = strings.Trim(prefix, "/"); prefix != "" {
		prefix += "/"
	}
	lister, err := core.NewS3(bucket, flags, s3)
	if err != nil {
		return nil, fmt.Errorf("s3fuse: %s: %w", o.Bucket, err)
	}
	srv, err := startServer(ctx, o)
	if err != nil {
		return nil, err
	}
	m := &Mount{point: filepath.Clean(o.MountPoint), server: srv, lister: lister, prefix: prefix}
	mounted.Store(m.point, m)
	return m, nil
}

// Join waits until the mount ends: its server exited, said in the error when it did not end
// as asked (a crash, a kill).
func (m *Mount) Join(ctx context.Context) error {
	select {
	case <-m.server.exited:
		mounted.CompareAndDelete(m.point, m) // never a newer mount of the same point
		return m.server.exitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unmount asks the server to flush what is buffered, unmount and stop. A refused unmount
// (busy) is returned: the server keeps serving, so a request still in the mount gets its
// answer, until the mount, detached by the caller, is released or the server's bound ends
// (#2142).
func (m *Mount) Unmount() error {
	mounted.CompareAndDelete(m.point, m)
	return m.server.unmount()
}

// served is a mount served in this process: the backup-folder-mount child's.
type served struct {
	fs  *core.Goofys
	mfs *fuse.MountedFileSystem
}

// serve mounts o.Bucket on o.MountPoint in this process and returns once mounted.
func serve(ctx context.Context, o Options) (*served, error) {
	if err := o.defaults(); err != nil {
		return nil, err
	}
	flags, _ := s3Flags(o)
	fs, err := core.NewGoofys(ctx, o.Bucket, flags)
	if err != nil || fs == nil {
		if err == nil {
			err = errors.New("initialization failed")
		}
		return nil, fmt.Errorf("s3fuse: %s: %w", o.Bucket, err)
	}
	d := DeadlineFS{Fs: core.NewGoofysFuse(fs), Timeout: o.OpTimeout, OnLate: o.OnLate, OnPanic: o.OnPanic}
	mountCfg := &fuse.MountConfig{
		FSName:                  o.Bucket,
		Subtype:                 "geesefs",
		DisableWritebackCaching: true,
		UseVectoredRead:         true,
		UseReadDirPlus:          true,
	}
	mfs, err := fuse.Mount(o.MountPoint, fuseutil.NewFileSystemServer(d), mountCfg)
	if err != nil {
		fs.Shutdown()
		return nil, fmt.Errorf("s3fuse: mount %s on %s: %w", o.Bucket, o.MountPoint, err)
	}
	s := &served{fs: fs, mfs: mfs}
	// before anything opens a file of the mount: a poll request is then never sent to it
	if err := disablePoll(o.MountPoint); err != nil {
		s.unmount(o.MountPoint)
		return nil, err
	}
	return s, nil
}

// unmount flushes, unmounts and stops the file system; a refused unmount (busy) leaves it
// serving.
func (s *served) unmount(point string) error {
	if err := core.TryUnmount(point); err != nil {
		return err
	}
	s.fs.Shutdown()
	return nil
}
