package s3helper

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakeS3Client struct {
	listOutputs []*s3.ListObjectsV2Output
	listErr     error
	deleteErr   error
	listCalls   int
	deleteCalls int
}

func (f *fakeS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
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
