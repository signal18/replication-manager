// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2091, #2159: an S3 provider reached with the Cloud18 SSO holds no keys, needs a registered
// replication-manager with its Cloud18 identity client, and assumes the storages' role
func TestCloud18SSOProvider(t *testing.T) {
	p := config.S3Provider{Name: "cloud18", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example", Cloud18SSO: true}
	if err := p.Validate(); err != nil {
		t.Fatalf("an SSO provider without keys: %v", err)
	}
	p.AccessKey = "id"
	if p.Validate() == nil {
		t.Fatal("an SSO provider must refuse keys")
	}
	p.AccessKey, p.RoleArn = "", "arn:minio:iam:eu-west-1::role/x"
	if err := p.Validate(); err != nil {
		t.Fatalf("an SSO provider with its role: %v", err)
	}
	keyed := config.S3Provider{Name: "k", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example", RoleArn: "arn:x"}
	if keyed.Validate() == nil {
		t.Fatal("a role is for an SSO provider only")
	}
	app := config.S3Provider{Name: "app", ProviderSource: config.S3ProviderSourceApp, ProviderApp: "minio:9000", Cloud18SSO: true}
	if app.Validate() == nil {
		t.Fatal("an app provider never takes cloud18Sso")
	}

	cl := newEventLogTestCluster(t, 1)
	if err := cl.s3ProviderUsable(config.S3Provider{Cloud18SSO: true}); !errors.Is(err, ErrCloud18SSONotRegistered) {
		t.Fatalf("not registered: %v", err)
	}
	cl.Conf.Cloud18 = true
	if err := cl.s3ProviderUsable(config.S3Provider{Cloud18SSO: true}); !errors.Is(err, config.ErrCloud18IdentityNotSet) {
		t.Fatalf("registered, nobody connected it: %v", err)
	}
	connectCloud18ForTest(t, cl)
	if err := cl.s3ProviderUsable(config.S3Provider{Cloud18SSO: true}); err != nil {
		t.Fatalf("registered and connected: %v", err)
	}
	if err := cl.s3ProviderUsable(config.S3Provider{}); err != nil {
		t.Fatalf("a provider with keys: %v", err)
	}

	// the role MinIO printed for the storages' audience (fr-1, fr-2 and mixr fr-1, 2026-10-24)
	if r := minioRoleArn("eu-west-1", config.ConstCloud18StorageAudience); r != "arn:minio:iam:eu-west-1::role/uis-o2VW9GtZuoJDSshFCPUr0Rs" {
		t.Fatalf("computed role %s", r)
	}
}
