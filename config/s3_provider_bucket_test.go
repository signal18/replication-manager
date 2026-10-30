// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2219: an [[s3-provider]] block carries the provider's bucket both ways.
func TestS3ProviderSettingsBucket(t *testing.T) {
	p := S3ProviderFromSettings(map[string]interface{}{"name": "ovh-gra", "endpoint": "https://s3.gra.example", "bucket": "c18-x"})
	if p.Bucket != "c18-x" || S3ProviderSettings(p)["bucket"] != "c18-x" {
		t.Fatalf("bucket: %+v %v", p, S3ProviderSettings(p))
	}
	if _, ok := S3ProviderSettings(S3Provider{Name: "n"})["bucket"]; ok {
		t.Fatal("an empty bucket is left out")
	}
}
