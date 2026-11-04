package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestResetAppFromTemplate_EncryptsTemplateSecretLiterals: a secret value written as a
// literal in the template is stored encrypted after a reset from that template (#2248).
func TestResetAppFromTemplate_EncryptsTemplateSecretLiterals(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir(), SecretKey: []byte("0123456789abcdef")}}
	node := &cluster.App{
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "secret-literals", `
prov-app-docker-img = "templated/image:2"

[deployment]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.storages.git-clones]]
name = "site"
repo = "https://git.example.invalid/site.git"
volumename = "data-volume"
volumedir = "site"
pass = "gitpass-literal"

[[deployment.storages.s3-mounts]]
name = "assets"
bucket = "assets"
secretkey = "s3secret-literal"
volumename = "data-volume"
volumedir = "assets"

[[deployment.variables]]
name = "APP_SECRET"
type = "secret"
value = "secret-literal"
[[deployment.variables.conditional]]
agent = "agent1"
value = "agent-literal"

[[deployment.variables]]
name = "APP_ENV"
type = "env"
value = "plain-env"
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	if err := resetAppFromTemplateWithProjection(cl, node, "secret-literals", false); err != nil {
		t.Fatalf("resetAppFromTemplateWithProjection: %v", err)
	}

	d := node.AppConfig.Deployment
	if d == nil || len(d.Variables) != 2 || len(d.Variables[0].Conditional) != 1 || len(d.Storages.GitClones) != 1 || len(d.Storages.S3Mounts) != 1 {
		t.Fatalf("deployment not projected from template: %+v", d)
	}
	for field, c := range map[string][2]string{
		"APP_SECRET":        {d.Variables[0].Value, "secret-literal"},
		"APP_SECRET@agent1": {d.Variables[0].Conditional[0].Value, "agent-literal"},
		"git pass":          {d.Storages.GitClones[0].GitPass, "gitpass-literal"},
		"s3 secretkey":      {d.Storages.S3Mounts[0].SecretKey, "s3secret-literal"},
	} {
		if !strings.HasPrefix(c[0], "hash_") {
			t.Fatalf("%s stored in clear: %q", field, c[0])
		}
		if got := cl.Conf.GetDecryptedPassword(field, c[0]); got != c[1] {
			t.Fatalf("%s decrypts to %q, want %q", field, got, c[1])
		}
	}
	if d.Variables[1].Value != "plain-env" {
		t.Fatalf("env variable changed: %q", d.Variables[1].Value)
	}
}
