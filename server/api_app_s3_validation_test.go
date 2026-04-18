package server

import (
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestValidateCustomEndpointCredentialPair_BothBlankAllowed(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("", ""); err != nil {
		t.Fatalf("expected blank pair to be allowed, got error: %v", err)
	}
}

func TestValidateCustomEndpointCredentialPair_BothSetAllowed(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("AKIA", "SECRET"); err != nil {
		t.Fatalf("expected complete pair to be allowed, got error: %v", err)
	}
}

func TestValidateCustomEndpointCredentialPair_RejectsPartialPair(t *testing.T) {
	if err := validateCustomEndpointCredentialPair("AKIA", ""); err == nil {
		t.Fatal("expected error for partial pair with missing secretkey")
	}
	if err := validateCustomEndpointCredentialPair("", "SECRET"); err == nil {
		t.Fatal("expected error for partial pair with missing accesskey")
	}
}

func TestValidateStandaloneCustomEndpointCredentials_RejectsBlankPair(t *testing.T) {
	if err := validateStandaloneCustomEndpointCredentials("", ""); err == nil {
		t.Fatal("expected error for standalone custom mount without credentials")
	}
}

func TestHydrateS3MountFromProvider_CustomProviderFillsBlankCreds(t *testing.T) {
	cl := &cluster.Cluster{}
	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "archive-s3",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://minio.example.com",
		Region:         "us-east-1",
		AccessKey:      "PROVIDER_ACCESS",
		SecretKey:      "PROVIDER_SECRET",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	mount := &config.S3Mount{
		Name:         "backup",
		ProviderName: "archive-s3",
		Bucket:       "db-backups",
		// AccessKey/SecretKey intentionally omitted in request payload.
	}

	if err := hydrateS3MountFromProvider(cl, mount); err != nil {
		t.Fatalf("hydrateS3MountFromProvider: %v", err)
	}

	if mount.Endpoint != "https://minio.example.com" {
		t.Fatalf("endpoint: got %q, want provider endpoint", mount.Endpoint)
	}
	if mount.Region != "us-east-1" {
		t.Fatalf("region: got %q, want provider region", mount.Region)
	}
	if mount.AccessKey != "PROVIDER_ACCESS" {
		t.Fatalf("accesskey: got %q, want provider access key", mount.AccessKey)
	}
	if mount.SecretKey != "PROVIDER_SECRET" {
		t.Fatalf("secretkey: got %q, want provider secret key", mount.SecretKey)
	}

	if err := validateStandaloneCustomEndpointCredentials(mount.AccessKey, mount.SecretKey); err != nil {
		t.Fatalf("hydrated credentials should satisfy pair validation: %v", err)
	}
}

func TestHydrateS3MountFromProvider_ModifyFlowOverridesProviderManagedFields(t *testing.T) {
	cl := &cluster.Cluster{}
	if err := cl.AddS3Provider(config.S3Provider{
		Name:           "archive-s3",
		ProviderSource: config.S3ProviderSourceCustom,
		Endpoint:       "https://provider.example.com",
		Region:         "eu-west-1",
		AccessKey:      "PROVIDER_ACCESS",
		SecretKey:      "PROVIDER_SECRET",
	}); err != nil {
		t.Fatalf("AddS3Provider: %v", err)
	}

	mount := &config.S3Mount{
		Name:         "backup",
		ProviderName: "archive-s3",
		Endpoint:     "https://user-override.example.com",
		Region:       "ap-south-1",
		AccessKey:    "USER_ACCESS",
		SecretKey:    "USER_SECRET",
	}

	if err := hydrateS3MountFromProvider(cl, mount); err != nil {
		t.Fatalf("hydrateS3MountFromProvider: %v", err)
	}

	if mount.Endpoint != "https://provider.example.com" || mount.Region != "eu-west-1" {
		t.Fatalf("provider-managed endpoint/region not authoritative after modify hydration: %+v", mount)
	}
	if mount.AccessKey != "PROVIDER_ACCESS" || mount.SecretKey != "PROVIDER_SECRET" {
		t.Fatalf("provider-managed credentials not authoritative after modify hydration: %+v", mount)
	}
}
