package s3helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// UploadPartSize is the size of each part of a multipart upload: S3 takes at most 10000 parts,
// so 64 MiB parts carry files up to 625 GiB; a smaller file goes in one PutObject.
const UploadPartSize = 64 << 20

// UploadPartTimeout bounds each request of an upload: a stalled S3 fails the upload instead of
// holding the caller forever.
const UploadPartTimeout = 5 * time.Minute

// UploadFile puts the local file path at bucket/key, in parts when it is larger than one part,
// each request bounded by UploadPartTimeout, and returns the bytes sent. A failed multipart
// upload is aborted so no orphan parts stay in the bucket. ctx cancels the whole upload.
func UploadFile(ctx context.Context, client *s3.Client, bucket, key, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size <= UploadPartSize {
		buf, err := io.ReadAll(f)
		if err != nil {
			return 0, err
		}
		rctx, cancel := context.WithTimeout(ctx, UploadPartTimeout)
		defer cancel()
		_, err = client.PutObject(rctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(buf), ContentLength: aws.Int64(int64(len(buf)))})
		if err != nil {
			return 0, fmt.Errorf("put %s: %w", key, err)
		}
		return int64(len(buf)), nil
	}

	rctx, cancel := context.WithTimeout(ctx, UploadPartTimeout)
	created, err := client.CreateMultipartUpload(rctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	cancel()
	if err != nil {
		return 0, fmt.Errorf("start upload of %s: %w", key, err)
	}
	abort := func(cause error) (int64, error) {
		actx, acancel := context.WithTimeout(context.Background(), UploadPartTimeout)
		defer acancel()
		client.AbortMultipartUpload(actx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: created.UploadId})
		return 0, cause
	}
	var parts []types.CompletedPart
	var sent int64
	buf := make([]byte, UploadPartSize)
	for n := int32(1); ; n++ {
		if err := ctx.Err(); err != nil {
			return abort(err)
		}
		read, rerr := io.ReadFull(f, buf)
		if read > 0 {
			pctx, pcancel := context.WithTimeout(ctx, UploadPartTimeout)
			out, err := client.UploadPart(pctx, &s3.UploadPartInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: created.UploadId, PartNumber: aws.Int32(n), Body: bytes.NewReader(buf[:read]), ContentLength: aws.Int64(int64(read))})
			pcancel()
			if err != nil {
				return abort(fmt.Errorf("upload part %d of %s: %w", n, key, err))
			}
			parts = append(parts, types.CompletedPart{ETag: out.ETag, PartNumber: aws.Int32(n)})
			sent += int64(read)
		}
		if rerr == io.EOF || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return abort(rerr)
		}
	}
	cctx, ccancel := context.WithTimeout(ctx, UploadPartTimeout)
	defer ccancel()
	if _, err := client.CompleteMultipartUpload(cctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: created.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}}); err != nil {
		return abort(fmt.Errorf("complete upload of %s: %w", key, err))
	}
	if sent != size {
		return sent, fmt.Errorf("upload of %s: %d bytes sent, %d local", key, sent, size)
	}
	return sent, nil
}

// ObjectSize is the size of bucket/key, for checking an upload.
func ObjectSize(ctx context.Context, client *s3.Client, bucket, key string) (int64, error) {
	rctx, cancel := context.WithTimeout(ctx, UploadPartTimeout)
	defer cancel()
	out, err := client.HeadObject(rctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return 0, err
	}
	return aws.ToInt64(out.ContentLength), nil
}
