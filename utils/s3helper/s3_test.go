package s3helper

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeS3Client struct {
	listOutputs []*s3.ListObjectsV2Output
	listErr     error
	deleteErr   error
	headErr     error
	putErr      error
	existing    map[string]bool
	listCalls   int
	deleteCalls int
	headCalls   int
	putCalls    int
}

func (f *fakeS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.listOutputs) == 0 {
		out := &s3.ListObjectsV2Output{}
		if f.existing != nil {
			for key := range f.existing {
				if params.Prefix != nil && !strings.HasPrefix(key, aws.ToString(params.Prefix)) {
					continue
				}
				k := key
				out.Contents = append(out.Contents, types.Object{Key: &k})
			}
		}
		f.listCalls++
		return out, nil
	}
	if f.listCalls >= len(f.listOutputs) {
		f.listCalls++
		return &s3.ListObjectsV2Output{}, nil
	}
	out := f.listOutputs[f.listCalls]
	f.listCalls++
	return out, nil
}

func (f *fakeS3Client) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.deleteCalls++
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (f *fakeS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.headCalls++
	if f.headErr != nil {
		return nil, f.headErr
	}
	if f.existing != nil && f.existing[aws.ToString(params.Key)] {
		return &s3.HeadObjectOutput{}, nil
	}
	return nil, &smithy.GenericAPIError{Code: "NotFound", Message: "not found"}
}

func (f *fakeS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putCalls++
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.existing == nil {
		f.existing = map[string]bool{}
	}
	f.existing[aws.ToString(params.Key)] = true
	return &s3.PutObjectOutput{}, nil
}

func TestDeletePrefixWithOptionsRequireNonEmptyPrefix(t *testing.T) {
	client := &fakeS3Client{}
	err := deletePrefixWithOptions(client, "bucket", "", DeletePrefixOptions{RequireNonEmptyPrefix: true})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "requires a non-empty prefix") {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.listCalls != 0 {
		t.Fatalf("expected no list calls, got %d", client.listCalls)
	}
}

func TestDeletePrefixWithOptionsMaxObjectsExceeded(t *testing.T) {
	client := &fakeS3Client{
		listOutputs: []*s3.ListObjectsV2Output{
			{
				Contents: []types.Object{
					{Key: aws.String("a")},
					{Key: aws.String("b")},
				},
			},
		},
	}
	options := DeletePrefixOptions{MaxObjects: 1}
	err := deletePrefixWithOptions(client, "bucket", "prefix", options)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds max objects") {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.deleteCalls != 0 {
		t.Fatalf("expected no delete calls, got %d", client.deleteCalls)
	}
}

func TestDeletePrefixWithOptionsDryRunDoesNotDelete(t *testing.T) {
	client := &fakeS3Client{
		listOutputs: []*s3.ListObjectsV2Output{
			{
				Contents: []types.Object{
					{Key: aws.String("a")},
				},
			},
		},
	}
	options := DeletePrefixOptions{DryRun: true}
	err := deletePrefixWithOptions(client, "bucket", "prefix", options)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.deleteCalls != 0 {
		t.Fatalf("expected no delete calls, got %d", client.deleteCalls)
	}
}

func TestListPrefixHasRealObjectsIgnoresMarker(t *testing.T) {
	client := &fakeS3Client{existing: map[string]bool{
		"prefix/.restic-marker": true,
	}}
	hasReal, err := listPrefixHasRealObjects(client, "bucket", "prefix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasReal {
		t.Fatalf("expected no real objects when only marker exists")
	}
}

func TestListPrefixHasRealObjectsWithRealObject(t *testing.T) {
	client := &fakeS3Client{existing: map[string]bool{
		"prefix/.restic-marker": true,
		"prefix/data/file":      true,
	}}
	hasReal, err := listPrefixHasRealObjects(client, "bucket", "prefix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasReal {
		t.Fatalf("expected real objects to be detected")
	}
}

func TestEnsurePrefixMarkerSkipsWhenRealObjectsExist(t *testing.T) {
	client := &fakeS3Client{existing: map[string]bool{
		"prefix/data/file": true,
	}}
	if err := ensurePrefixMarker(client, "bucket", "prefix"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.putCalls != 0 {
		t.Fatalf("expected no marker put, got %d", client.putCalls)
	}
}

func TestEnsurePrefixMarkerCreatesWhenEmpty(t *testing.T) {
	client := &fakeS3Client{}
	if err := ensurePrefixMarker(client, "bucket", "prefix"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.putCalls != 1 {
		t.Fatalf("expected marker put once, got %d", client.putCalls)
	}
	if !client.existing["prefix/.restic-marker"] {
		t.Fatalf("expected marker to be created")
	}
}

// fakeBucketClient implements bucketAPI for EnsureBucket tests.
type fakeBucketClient struct {
	// headErrs is consumed in order across successive HeadBucket calls; the
	// last entry is reused once exhausted. A nil entry means success.
	headErrs      []error
	headCalls     int
	createErr     error
	createCalls   int
	createdConfig *types.CreateBucketConfiguration
}

func (f *fakeBucketClient) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	idx := f.headCalls
	if idx >= len(f.headErrs) {
		idx = len(f.headErrs) - 1
	}
	f.headCalls++
	if idx >= 0 && f.headErrs[idx] != nil {
		return nil, f.headErrs[idx]
	}
	return &s3.HeadBucketOutput{}, nil
}

func (f *fakeBucketClient) CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	f.createCalls++
	f.createdConfig = params.CreateBucketConfiguration
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &s3.CreateBucketOutput{}, nil
}

func TestEnsureBucketEmptyName(t *testing.T) {
	client := &fakeBucketClient{}
	if _, err := ensureBucket(client, "  ", ""); err == nil {
		t.Fatal("expected error for empty bucket name")
	}
	if client.headCalls != 0 || client.createCalls != 0 {
		t.Fatalf("expected no S3 calls for empty bucket name, got head=%d create=%d", client.headCalls, client.createCalls)
	}
}

func TestEnsureBucketExistingAccessible(t *testing.T) {
	client := &fakeBucketClient{headErrs: []error{nil}}
	created, err := ensureBucket(client, "mybucket", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Fatal("expected created=false for already-accessible bucket")
	}
	if client.createCalls != 0 {
		t.Fatalf("expected no CreateBucket call, got %d", client.createCalls)
	}
}

func TestEnsureBucketMissingCreates(t *testing.T) {
	client := &fakeBucketClient{headErrs: []error{&smithy.GenericAPIError{Code: "NotFound"}}}
	created, err := ensureBucket(client, "mybucket", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for missing bucket")
	}
	if client.createCalls != 1 {
		t.Fatalf("expected one CreateBucket call, got %d", client.createCalls)
	}
}

func TestEnsureBucketAccessDenied(t *testing.T) {
	client := &fakeBucketClient{headErrs: []error{&smithy.GenericAPIError{Code: "Forbidden"}}}
	if _, err := ensureBucket(client, "mybucket", ""); err == nil {
		t.Fatal("expected access-denied error")
	}
	if client.createCalls != 0 {
		t.Fatalf("expected no CreateBucket call on access denied, got %d", client.createCalls)
	}
}

func TestEnsureBucketCreateBucketAlreadyOwnedByYou(t *testing.T) {
	client := &fakeBucketClient{
		headErrs:  []error{&smithy.GenericAPIError{Code: "NotFound"}},
		createErr: &smithy.GenericAPIError{Code: "BucketAlreadyOwnedByYou"},
	}
	created, err := ensureBucket(client, "mybucket", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Fatal("expected created=false when BucketAlreadyOwnedByYou")
	}
}

func TestEnsureBucketCreateBucketAlreadyExistsButAccessible(t *testing.T) {
	// First HeadBucket (pre-create probe) reports missing; the race-recovery
	// probe after BucketAlreadyExists reports accessible.
	client := &fakeBucketClient{
		headErrs:  []error{&smithy.GenericAPIError{Code: "NotFound"}, nil},
		createErr: &smithy.GenericAPIError{Code: "BucketAlreadyExists"},
	}
	created, err := ensureBucket(client, "mybucket", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Fatal("expected created=false when bucket already exists and is accessible")
	}
}

func TestEnsureBucketCreateBucketAlreadyExistsNotAccessible(t *testing.T) {
	client := &fakeBucketClient{
		headErrs:  []error{&smithy.GenericAPIError{Code: "NotFound"}, &smithy.GenericAPIError{Code: "Forbidden"}},
		createErr: &smithy.GenericAPIError{Code: "BucketAlreadyExists"},
	}
	if _, err := ensureBucket(client, "mybucket", ""); err == nil {
		t.Fatal("expected error when bucket already exists and is not accessible")
	}
}

func TestEnsureBucketCreateBucketGenericError(t *testing.T) {
	client := &fakeBucketClient{
		headErrs:  []error{&smithy.GenericAPIError{Code: "NotFound"}},
		createErr: &smithy.GenericAPIError{Code: "InternalError"},
	}
	if _, err := ensureBucket(client, "mybucket", ""); err == nil {
		t.Fatal("expected error for generic CreateBucket failure")
	}
}

func TestBucketLocationConfigurationUsEast1(t *testing.T) {
	if cfg := bucketLocationConfiguration("us-east-1"); cfg != nil {
		t.Fatalf("expected nil LocationConstraint for us-east-1, got %+v", cfg)
	}
	if cfg := bucketLocationConfiguration(""); cfg != nil {
		t.Fatalf("expected nil LocationConstraint for empty region, got %+v", cfg)
	}
}

func TestBucketLocationConfigurationOtherRegion(t *testing.T) {
	cfg := bucketLocationConfiguration("eu-west-1")
	if cfg == nil {
		t.Fatal("expected non-nil LocationConstraint for eu-west-1")
	}
	if cfg.LocationConstraint != types.BucketLocationConstraint("eu-west-1") {
		t.Fatalf("unexpected LocationConstraint: %v", cfg.LocationConstraint)
	}
}

func TestEnsureBucketPassesRegionToCreateBucketConfiguration(t *testing.T) {
	client := &fakeBucketClient{headErrs: []error{&smithy.GenericAPIError{Code: "NotFound"}}}
	if _, err := ensureBucket(client, "mybucket", "eu-west-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.createdConfig == nil || client.createdConfig.LocationConstraint != types.BucketLocationConstraint("eu-west-1") {
		t.Fatalf("expected region-specific CreateBucketConfiguration, got %+v", client.createdConfig)
	}
}
