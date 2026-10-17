/*
 * Copyright (c) 2021 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

// The S3 storage of the plakar engine, adapted from github.com/PlakarKorp/integrations/s3
// v1.1.8 storage/storage.go (ISC, see LICENSE.plakar-integrations). Changes for
// replication-manager: it runs in-process (registered with kloset's storage registry, no
// gRPC plugin wrapper), its credentials take a session token for short-lived STS keys,
// a missing bucket or CONFIG is fs.ErrNotExist (probe creates the archive on it), Size
// sums the objects (the original answers -1), and the Glacier, SSE-C and virtual-host
// options are left out (replication-manager does not offer them).

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/PlakarKorp/kloset/connectors/storage"
	"github.com/PlakarKorp/kloset/location"
	"github.com/PlakarKorp/kloset/objects"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// The keys of an s3 storeConfig besides "location" (s3://host/bucket/prefix).
const (
	s3ConfAccessKey    = "access_key"
	s3ConfSecretKey    = "secret_access_key"
	s3ConfSessionToken = "session_token"
	s3ConfUseTLS       = "use_tls"
	s3ConfRegion       = "region"
)

type s3Store struct {
	minioClient *minio.Client
	host        string
	bucket      string
	prefixDir   string
	region      string
}

func init() {
	if err := storage.Register("s3", 0, newS3Store); err != nil {
		panic(err)
	}
}

func newS3Store(ctx context.Context, proto string, storeConfig map[string]string) (storage.Store, error) {
	useSsl := true
	if value, ok := storeConfig[s3ConfUseTLS]; ok && value != "" {
		tmp, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid use_tls value")
		}
		useSsl = tmp
	}

	u, err := url.Parse(storeConfig["location"])
	if err != nil {
		return nil, fmt.Errorf("parse location: %w", err)
	}
	host := u.Host
	bucket, prefixDir, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	if bucket == "" || host == "" {
		return nil, fmt.Errorf("failed to parse the location: bucket name or host name are empty")
	}
	prefixDir = "/" + strings.Trim(prefixDir, "/") + "/"
	if prefixDir == "//" {
		prefixDir = "/"
	}

	transport, err := minio.DefaultTransport(useSsl)
	if err != nil {
		return nil, fmt.Errorf("failed to create default transport: %w", err)
	}
	// All requests go to one host, so the per-host idle cap is the one that
	// bites: above it, each finished request drops its connection and the
	// next one redials.
	transport.MaxIdleConnsPerHost = transport.MaxIdleConns

	// Static keys, with the session token of short-lived STS keys; without keys, the
	// instance's IAM role, as the original falls back to.
	var creds *credentials.Credentials
	if storeConfig[s3ConfAccessKey] != "" {
		creds = credentials.NewStaticV4(storeConfig[s3ConfAccessKey], storeConfig[s3ConfSecretKey], storeConfig[s3ConfSessionToken])
	} else {
		creds = credentials.NewIAM("")
	}

	client, err := minio.New(host, &minio.Options{
		Creds:     creds,
		Secure:    useSsl,
		Transport: transport,
		Region:    storeConfig[s3ConfRegion],
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}
	client.SetAppInfo("replication-manager-archiveplugin-plakar", "1")

	return &s3Store{
		minioClient: client,
		host:        host,
		bucket:      bucket,
		prefixDir:   prefixDir,
		region:      storeConfig[s3ConfRegion],
	}, nil
}

func (s *s3Store) realpath(path string) string {
	return strings.TrimPrefix(s.prefixDir+path, "/")
}

func (s *s3Store) Create(ctx context.Context, config []byte) error {
	exists, err := s.minioClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check if bucket exists: %w", err)
	}
	if !exists {
		err = s.minioClient.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region})
		if err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}

	_, err = s.minioClient.StatObject(ctx, s.bucket, s.realpath("CONFIG"), minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code != "NoSuchKey" {
			return fmt.Errorf("stat object CONFIG: %w", err)
		}
	} else {
		return fmt.Errorf("bucket already initialized")
	}

	if err := s.checkObjectLockHistory(ctx); err != nil {
		return err
	}

	_, err = s.minioClient.PutObject(ctx, s.bucket, s.realpath("CONFIG"), bytes.NewReader(config), int64(len(config)), minio.PutObjectOptions{
		// Some providers (eg. BlackBlaze) return the error
		// "Unsupported header 'x-amz-checksum-algorithm'" if SendContentMd5
		// is not set.
		SendContentMd5: true,
	})
	if err != nil {
		return fmt.Errorf("put object CONFIG: %w", err)
	}
	return nil
}

// checkObjectLockHistory refuses creation if Object Lock is enabled and any
// version of CONFIG already exists, including delete markers: deleting every
// object from a locked bucket doesn't erase its history, so a second store
// created on top would make version recovery ambiguous between the two.
func (s *s3Store) checkObjectLockHistory(ctx context.Context) error {
	enabled, _, _, _, err := s.minioClient.GetObjectLockConfig(ctx, s.bucket)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "ObjectLockConfigurationNotFoundError" {
			return nil
		}
		return fmt.Errorf("get object lock config: %w", err)
	}
	if enabled != "Enabled" {
		return nil
	}

	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	object, ok := <-s.minioClient.ListObjects(listCtx, s.bucket, minio.ListObjectsOptions{
		Prefix:       s.realpath("CONFIG"),
		WithVersions: true,
		MaxKeys:      1,
	})
	if !ok {
		return nil
	}
	if object.Err != nil {
		return fmt.Errorf("list CONFIG versions: %w", object.Err)
	}
	return fmt.Errorf("bucket already initialized: object lock is enabled and still holds CONFIG's version history; clear it or use a different bucket or prefix")
}

// Open reads CONFIG; a missing bucket or CONFIG is fs.ErrNotExist.
func (s *s3Store) Open(ctx context.Context) ([]byte, error) {
	exists, err := s.minioClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("error checking if bucket exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s: %w", s.bucket, fs.ErrNotExist)
	}

	object := newRetryReader(ctx, -1, func(offset int64) (io.ReadCloser, error) {
		opts := minio.GetObjectOptions{}
		if offset > 0 {
			if err := opts.SetRange(offset, 0); err != nil {
				return nil, err
			}
		}
		return s.minioClient.GetObject(ctx, s.bucket, s.realpath("CONFIG"), opts)
	})
	defer object.Close()

	data, err := io.ReadAll(object)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, fmt.Errorf("CONFIG: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("error reading object: %w", err)
	}
	return data, nil
}

func (s *s3Store) Ping(ctx context.Context) error {
	ok, err := s.minioClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket does not exist")
	}
	return nil
}

func (s *s3Store) Origin() string        { return s.host }
func (s *s3Store) Root() string          { return path.Join("/", s.bucket, s.prefixDir) }
func (s *s3Store) Type() string          { return "s3" }
func (s *s3Store) Flags() location.Flags { return 0 }

func (s *s3Store) Mode(ctx context.Context) (storage.Mode, error) {
	return storage.ModeRead | storage.ModeWrite, nil
}

// Size sums the objects under the prefix: one listing of the archive.
func (s *s3Store) Size(ctx context.Context) (int64, error) {
	var size int64
	var listingErr error
	for object := range s.minioClient.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.realpath(""),
		Recursive: true,
	}) {
		if listingErr != nil {
			continue // drain the channel, per minio's documentation
		}
		if object.Err != nil {
			listingErr = object.Err
			continue
		}
		size += object.Size
	}
	if listingErr != nil {
		return 0, listingErr
	}
	return size, nil
}

func (s *s3Store) List(ctx context.Context, res storage.StorageResource) ([]objects.MAC, error) {
	var prefix string
	var prefixSize int

	switch res {
	case storage.StorageResourcePackfile:
		prefix = s.realpath("packfiles/")
		prefixSize = len(prefix) + 3 // prefix + len(%02x/) encoded
	case storage.StorageResourceState:
		prefix = s.realpath("states/")
		prefixSize = len(prefix) + 3 // prefix + len(%02x/) encoded
	case storage.StorageResourceLock:
		prefix = s.realpath("locks/")
		prefixSize = len(prefix)
	default:
		return nil, errors.ErrUnsupported
	}

	ret := make([]objects.MAC, 0)
	var listingErr error
	for object := range s.minioClient.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if listingErr != nil {
			continue // We have to drain the objects per documentation.
		}
		if object.Err != nil {
			listingErr = object.Err
			continue
		}
		if strings.HasPrefix(object.Key, prefix) && len(object.Key) >= prefixSize {
			t, err := hex.DecodeString(object.Key[prefixSize:])
			if err != nil {
				continue
			}
			if len(t) != 32 {
				continue
			}
			ret = append(ret, objects.MAC(t))
		}
	}
	if listingErr != nil {
		return nil, listingErr
	}
	return ret, nil
}

func (s *s3Store) objectName(res storage.StorageResource, mac objects.MAC) (string, error) {
	switch res {
	case storage.StorageResourcePackfile:
		return s.realpath(fmt.Sprintf("packfiles/%02x/%016x", mac[0], mac)), nil
	case storage.StorageResourceState:
		return s.realpath(fmt.Sprintf("states/%02x/%016x", mac[0], mac)), nil
	case storage.StorageResourceLock:
		return s.realpath(fmt.Sprintf("locks/%016x", mac)), nil
	}
	return "", errors.ErrUnsupported
}

func (s *s3Store) Put(ctx context.Context, res storage.StorageResource, mac objects.MAC, rd io.Reader) (int64, error) {
	name, err := s.objectName(res, mac)
	if err != nil {
		return -1, err
	}
	// Stream with an unknown length; without a part size, minio assumes a 5TiB
	// object and allocates a ~500MiB part buffer per concurrent Put, so cap
	// each Put at a 16MiB buffer.
	info, err := s.minioClient.PutObject(ctx, s.bucket, name, rd, -1, minio.PutObjectOptions{
		SendContentMd5: true,
		PartSize:       16 << 20,
	})
	if err != nil {
		return 0, fmt.Errorf("put %s object: %w", res, err)
	}
	return info.Size, nil
}

func (s *s3Store) Get(ctx context.Context, res storage.StorageResource, mac objects.MAC, rg *storage.Range) (io.ReadCloser, error) {
	name, err := s.objectName(res, mac)
	if err != nil {
		return nil, err
	}
	if rg != nil && rg.Length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	expected := int64(-1)
	if rg != nil {
		expected = int64(rg.Length)
	}
	return newRetryReader(ctx, expected, func(offset int64) (io.ReadCloser, error) {
		opts := minio.GetObjectOptions{}
		if rg != nil {
			start := int64(rg.Offset) + offset
			if err := opts.SetRange(start, int64(rg.Offset)+int64(rg.Length)-1); err != nil {
				return nil, err
			}
		} else if offset > 0 {
			if err := opts.SetRange(offset, 0); err != nil {
				return nil, err
			}
		}
		return s.minioClient.GetObject(ctx, s.bucket, name, opts)
	}), nil
}

func (s *s3Store) Delete(ctx context.Context, res storage.StorageResource, mac objects.MAC) error {
	name, err := s.objectName(res, mac)
	if err != nil {
		return err
	}
	if err := s.minioClient.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove %s object: %w", res, err)
	}
	return nil
}

func (s *s3Store) Close(ctx context.Context) error {
	return nil
}

// removeArchive deletes the archive's objects (not the bucket, nor anything else in it):
// the packfiles, states and locks, then CONFIG last so an interrupted remove can run again.
// The caller checked CONFIG is a plakar configuration.
func (s *s3Store) removeArchive(ctx context.Context) (int, error) {
	removed := 0
	for _, dir := range []string{"packfiles/", "states/", "locks/"} {
		n, err := s.removePrefix(ctx, s.realpath(dir))
		removed += n
		if err != nil {
			return removed, err
		}
	}
	if err := s.minioClient.RemoveObject(ctx, s.bucket, s.realpath("CONFIG"), minio.RemoveObjectOptions{}); err != nil {
		return removed, fmt.Errorf("remove CONFIG: %w", err)
	}
	return removed + 1, nil
}

// removePrefix deletes every object under prefix, in batches as minio's RemoveObjects does.
func (s *s3Store) removePrefix(ctx context.Context, prefix string) (int, error) {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	toRemove := make(chan minio.ObjectInfo)
	var listingErr error
	go func() {
		defer close(toRemove)
		for object := range s.minioClient.ListObjects(listCtx, s.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if object.Err != nil {
				if listingErr == nil {
					listingErr = object.Err
				}
				cancel()
				continue
			}
			select {
			case toRemove <- object:
			case <-listCtx.Done():
			}
		}
	}()
	removed := 0
	var removeErr error
	for res := range s.minioClient.RemoveObjectsWithResult(ctx, s.bucket, toRemove, minio.RemoveObjectsOptions{}) {
		if res.Err != nil {
			if removeErr == nil {
				removeErr = fmt.Errorf("remove %s: %w", res.ObjectName, res.Err)
			}
			continue
		}
		removed++
	}
	if listingErr != nil {
		return removed, fmt.Errorf("list objects: %w", listingErr)
	}
	return removed, removeErr
}
