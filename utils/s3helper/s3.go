package s3helper

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// NewClient creates an AWS S3 client with MinIO compatibility.
func NewClient(accessKey, secretKey, sessionToken, region, endpoint string) (*s3.Client, error) {
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("AWS credentials not found (set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY)")
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	awsRegion := strings.TrimSpace(region)
	if awsRegion == "" {
		awsRegion = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if awsRegion == "" {
		awsRegion = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}

	awsConfig := aws.Config{
		Region:      awsRegion,
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken),
		HTTPClient:  httpClient,
	}

	return s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = true
		if endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}

// CheckObjectExists checks if an object exists in an S3 bucket.
func CheckObjectExists(client *s3.Client, bucket, key string) (bool, error) {
	_, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		if isS3ErrorCode(err, "NotFound", "NoSuchKey") {
			return false, nil
		}
		if isS3ErrorCode(err, "Forbidden", "AccessDenied") {
			return false, fmt.Errorf("access denied to S3 object %s/%s (check credentials/permissions)", bucket, key)
		}
		return false, fmt.Errorf("S3 HeadObject failed: %w", err)
	}

	return true, nil
}

type DeletePrefixOptions struct {
	DryRun                bool
	MaxObjects            int
	RequireNonEmptyPrefix bool
}

type listDeleteAPI interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// DeletePrefix deletes all objects under the given prefix (or entire bucket if prefix is empty).
func DeletePrefix(client *s3.Client, bucket, prefix string) error {
	return deletePrefixWithOptions(client, bucket, prefix, DeletePrefixOptions{})
}

func DeletePrefixWithOptions(client *s3.Client, bucket, prefix string, options DeletePrefixOptions) error {
	return deletePrefixWithOptions(client, bucket, prefix, options)
}

func EnsurePrefixMarker(client *s3.Client, bucket, prefix string) error {
	return ensurePrefixMarker(client, bucket, prefix)
}

func ListPrefixHasRealObjects(client *s3.Client, bucket, prefix string) (bool, error) {
	return listPrefixHasRealObjects(client, bucket, prefix)
}

func ensurePrefixMarker(client listDeleteAPI, bucket, prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	hasReal, err := listPrefixHasRealObjects(client, bucket, prefix)
	if err != nil {
		return err
	}
	if hasReal {
		return nil
	}
	markerKey := strings.Trim(prefix, "/") + "/.restic-marker"
	_, err = client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(markerKey),
	})
	if err == nil {
		return nil
	}
	if !isS3ErrorCode(err, "NotFound", "NoSuchKey") {
		return fmt.Errorf("S3 HeadObject failed: %w", err)
	}

	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(markerKey),
		Body:   strings.NewReader(""),
	})
	if err != nil {
		return fmt.Errorf("S3 PutObject failed: %w", err)
	}
	return nil
}

func listPrefixHasRealObjects(client listDeleteAPI, bucket, prefix string) (bool, error) {
	markerKey := ""
	if strings.TrimSpace(prefix) != "" {
		markerKey = strings.Trim(prefix, "/") + "/.restic-marker"
	}
	var continuation *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket:  aws.String(bucket),
			Prefix:  aws.String(prefix),
			MaxKeys: aws.Int32(2),
		}
		if continuation != nil {
			input.ContinuationToken = continuation
		}

		result, err := client.ListObjectsV2(context.Background(), input)
		if err != nil {
			if isS3ErrorCode(err, "NoSuchBucket") {
				return false, fmt.Errorf("S3 bucket not found: %s (create it first or check name)", bucket)
			}
			if isS3ErrorCode(err, "Forbidden", "AccessDenied") {
				return false, fmt.Errorf("access denied to S3 bucket: %s (check credentials/permissions)", bucket)
			}
			return false, fmt.Errorf("S3 ListObjects failed: %w", err)
		}

		for _, obj := range result.Contents {
			if obj.Key == nil {
				continue
			}
			if markerKey != "" && aws.ToString(obj.Key) == markerKey {
				continue
			}
			return true, nil
		}

		if aws.ToBool(result.IsTruncated) {
			continuation = result.NextContinuationToken
			if continuation == nil {
				return false, fmt.Errorf("S3 ListObjects truncated without continuation token")
			}
			continue
		}

		break
	}

	return false, nil
}

func deletePrefixWithOptions(client listDeleteAPI, bucket, prefix string, options DeletePrefixOptions) error {
	if options.RequireNonEmptyPrefix && prefix == "" {
		return fmt.Errorf("S3 delete prefix requires a non-empty prefix")
	}

	if options.DryRun || options.MaxObjects > 0 {
		count, err := listPrefixCount(client, bucket, prefix)
		if err != nil {
			return err
		}
		if options.MaxObjects > 0 && count > options.MaxObjects {
			return fmt.Errorf("S3 delete prefix exceeds max objects (%d > %d)", count, options.MaxObjects)
		}
		if options.DryRun {
			return nil
		}
	}

	var continuation *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(prefix),
		}
		if continuation != nil {
			input.ContinuationToken = continuation
		}

		result, err := client.ListObjectsV2(context.Background(), input)
		if err != nil {
			if isS3ErrorCode(err, "NoSuchBucket") {
				return fmt.Errorf("S3 bucket not found: %s (create it first or check name)", bucket)
			}
			if isS3ErrorCode(err, "Forbidden", "AccessDenied") {
				return fmt.Errorf("access denied to S3 bucket: %s (check credentials/permissions)", bucket)
			}
			return fmt.Errorf("S3 ListObjects failed: %w", err)
		}

		if len(result.Contents) > 0 {
			objects := make([]types.ObjectIdentifier, 0, len(result.Contents))
			for _, obj := range result.Contents {
				if obj.Key == nil {
					continue
				}
				objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
			}

			for start := 0; start < len(objects); start += 1000 {
				end := start + 1000
				if end > len(objects) {
					end = len(objects)
				}

				deleteInput := &s3.DeleteObjectsInput{
					Bucket: aws.String(bucket),
					Delete: &types.Delete{
						Objects: objects[start:end],
						Quiet:   aws.Bool(true),
					},
				}
				deleteResult, delErr := client.DeleteObjects(context.Background(), deleteInput)
				if delErr != nil {
					return fmt.Errorf("S3 DeleteObjects failed: %w", delErr)
				}
				if deleteResult != nil && len(deleteResult.Errors) > 0 {
					firstErr := deleteResult.Errors[0]
					return fmt.Errorf("S3 DeleteObjects error for key %s: %s", aws.ToString(firstErr.Key), aws.ToString(firstErr.Message))
				}
			}
		}

		if aws.ToBool(result.IsTruncated) {
			continuation = result.NextContinuationToken
			if continuation == nil {
				return fmt.Errorf("S3 ListObjects truncated without continuation token")
			}
			continue
		}

		break
	}

	return nil
}

func listPrefixCount(client listDeleteAPI, bucket, prefix string) (int, error) {
	var continuation *string
	count := 0

	for {
		input := &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(prefix),
		}
		if continuation != nil {
			input.ContinuationToken = continuation
		}

		result, err := client.ListObjectsV2(context.Background(), input)
		if err != nil {
			if isS3ErrorCode(err, "NoSuchBucket") {
				return 0, fmt.Errorf("S3 bucket not found: %s (create it first or check name)", bucket)
			}
			if isS3ErrorCode(err, "Forbidden", "AccessDenied") {
				return 0, fmt.Errorf("access denied to S3 bucket: %s (check credentials/permissions)", bucket)
			}
			return 0, fmt.Errorf("S3 ListObjects failed: %w", err)
		}

		count += len(result.Contents)

		if aws.ToBool(result.IsTruncated) {
			continuation = result.NextContinuationToken
			if continuation == nil {
				return 0, fmt.Errorf("S3 ListObjects truncated without continuation token")
			}
			continue
		}

		break
	}

	return count, nil
}

func isS3ErrorCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		for _, code := range codes {
			if apiErr.ErrorCode() == code {
				return true
			}
		}
	}
	return false
}
