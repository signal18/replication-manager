// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestS3ProviderMarshalJSON_SecretsOmitted verifies that MarshalJSON never emits
// AccessKey or SecretKey, satisfying the secret-masking requirement for cluster
// read responses (Story 6.2 AC: 2).
func TestS3ProviderMarshalJSON_SecretsOmitted(t *testing.T) {
	p := S3Provider{
		Name:           "myprovider",
		ProviderSource: S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal S3Provider: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("MarshalJSON exposed AccessKey in output: %s", out)
	}
	if strings.Contains(out, "wJalrXUtnFEMI") {
		t.Errorf("MarshalJSON exposed SecretKey in output: %s", out)
	}
	if strings.Contains(out, "accesskey") {
		t.Errorf("MarshalJSON included accesskey field in output: %s", out)
	}
	if strings.Contains(out, "secretkey") {
		t.Errorf("MarshalJSON included secretkey field in output: %s", out)
	}
}

// TestS3ProviderMarshalJSON_NonSecretFieldsPresent verifies that non-sensitive
// fields are present in the JSON output so the payload remains usable for UI
// list and selection behaviour (Story 6.2 AC: 2).
func TestS3ProviderMarshalJSON_NonSecretFieldsPresent(t *testing.T) {
	p := S3Provider{
		Name:           "myprovider",
		ProviderSource: S3ProviderSourceCustom,
		Endpoint:       "https://s3.example.com",
		Region:         "us-east-1",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal S3Provider: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	for _, field := range []string{"name", "providerSource", "endpoint", "region"} {
		if _, ok := out[field]; !ok {
			t.Errorf("expected field %q in JSON output, got: %v", field, out)
		}
	}
}

// TestS3ProviderMarshalJSON_AppMode verifies app-mode providers are serialised
// correctly (providerApp present, no endpoint/region/secrets).
func TestS3ProviderMarshalJSON_AppMode(t *testing.T) {
	p := S3Provider{
		Name:           "app-provider",
		ProviderSource: S3ProviderSourceApp,
		ProviderApp:    "minio:9000",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal S3Provider: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if out["providerApp"] != "minio:9000" {
		t.Errorf("expected providerApp=minio:9000, got %v", out["providerApp"])
	}
	if _, hasKey := out["accesskey"]; hasKey {
		t.Errorf("accesskey should not be present in output")
	}
	if _, hasKey := out["secretkey"]; hasKey {
		t.Errorf("secretkey should not be present in output")
	}
}

// TestS3ProviderMarshalJSON_SlicePreservesOrder verifies that a slice of providers
// marshals to an array preserving order and masking secrets in every element —
// this mirrors the ClusterS3Providers cluster field serialisation.
func TestS3ProviderMarshalJSON_SlicePreservesOrder(t *testing.T) {
	providers := []S3Provider{
		{
			Name:           "first",
			ProviderSource: S3ProviderSourceCustom,
			Endpoint:       "https://first.example.com",
			AccessKey:      "key1",
			SecretKey:      "secret1",
		},
		{
			Name:           "second",
			ProviderSource: S3ProviderSourceApp,
			ProviderApp:    "app:8080",
		},
	}

	data, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("marshal providers slice: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "key1") || strings.Contains(out, "secret1") {
		t.Errorf("secrets should not appear in serialised slice: %s", out)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal providers slice: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(decoded))
	}
	if decoded[0]["name"] != "first" || decoded[1]["name"] != "second" {
		t.Errorf("order not preserved: %v", decoded)
	}
}
