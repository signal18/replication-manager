// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2091: an S3 provider reached with the Cloud18 SSO holds no keys, needs a registered
// replication-manager, and its folder is named after the identity token's user
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
	if err := cl.s3ProviderUsable(config.S3Provider{}); err != nil {
		t.Fatalf("a provider with keys: %v", err)
	}

	tok := func(payload string) string {
		return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
	}
	if u, err := identityTokenUser(tok(`{"preferred_username":"acme"}`)); err != nil || u != "acme" {
		t.Fatalf("user %q %v", u, err)
	}
	if u, _ := identityTokenUser(tok(`{"nickname":"bob"}`)); u != "bob" {
		t.Fatalf("nickname fallback %q", u)
	}
	for _, bad := range []string{"x.y", tok(`{}`), tok(`{"preferred_username":"a/b"}`)} {
		if _, err := identityTokenUser(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
