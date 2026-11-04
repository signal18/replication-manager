package cluster

import (
	"hash/crc64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// templateSecretsTOML holds a literal in every secret field a template can carry (#2248).
const templateSecretsTOML = `app-port = "8080"
prov-app-docker-img = "nginx:latest"

[deployment.storages]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.storages.git-clones]]
name = "site"
repo = "https://git.example.invalid/site.git"
branch = "main"
volumename = "data-volume"
volumedir = "site"
user = "deploy"
pass = "gitpass-literal"

[[deployment.storages.s3-mounts]]
name = "assets"
endpoint = "https://s3.example.invalid"
bucket = "assets"
accesskey = "AKIATEST"
secretkey = "s3secret-literal"
mountdir = "/assets"
volumename = "data-volume"
volumedir = "assets"

[[deployment.variables]]
name = "APP_SECRET"
type = "secret"
value = "secret-literal"

[[deployment.variables]]
name = "APP_COND_SECRET"
type = "secret"
value = "cond-default-literal"
[[deployment.variables.conditional]]
agent = "agent1"
value = "cond-agent-literal"

[[deployment.variables]]
name = "APP_ENV"
type = "env"
value = "plain-env"
`

var templateSecretsKey = []byte("0123456789abcdef")

// checkDeploymentSecretsEncrypted fails unless every secret literal of templateSecretsTOML
// is stored hash_ and decrypts back to the literal, and the env variable is left as it is.
func checkDeploymentSecretsEncrypted(t *testing.T, conf *config.Config, d *config.Deployment) {
	t.Helper()
	if d == nil {
		t.Fatalf("no deployment")
	}
	check := func(field, got, want string) {
		t.Helper()
		if !strings.HasPrefix(got, "hash_") {
			t.Fatalf("%s stored in clear: %q", field, got)
		}
		if dec := conf.GetDecryptedPassword(field, got); dec != want {
			t.Fatalf("%s decrypts to %q, want %q", field, dec, want)
		}
	}
	vars := map[string]config.VariableMapping{}
	for _, v := range d.Variables {
		vars[v.Name] = v
	}
	check("APP_SECRET", vars["APP_SECRET"].Value, "secret-literal")
	check("APP_COND_SECRET", vars["APP_COND_SECRET"].Value, "cond-default-literal")
	if len(vars["APP_COND_SECRET"].Conditional) != 1 {
		t.Fatalf("conditional values lost: %+v", vars["APP_COND_SECRET"].Conditional)
	}
	check("APP_COND_SECRET@agent1", vars["APP_COND_SECRET"].Conditional[0].Value, "cond-agent-literal")
	if got := vars["APP_ENV"].Value; got != "plain-env" {
		t.Fatalf("env variable changed: %q", got)
	}
	if len(d.Storages.GitClones) != 1 || len(d.Storages.S3Mounts) != 1 {
		t.Fatalf("storages lost: git=%d s3=%d", len(d.Storages.GitClones), len(d.Storages.S3Mounts))
	}
	check("git pass", d.Storages.GitClones[0].GitPass, "gitpass-literal")
	check("s3 secretkey", d.Storages.S3Mounts[0].SecretKey, "s3secret-literal")
	if got := d.Storages.S3Mounts[0].AccessKey; got != "AKIATEST" {
		t.Fatalf("s3 accesskey changed: %q", got)
	}
}

func TestEncryptDeploymentSecrets(t *testing.T) {
	conf := &config.Config{SecretKey: templateSecretsKey}
	cl := &Cluster{Conf: conf}
	whole := conf.GetEncryptedString("already-encrypted")
	dsn := "postgres://app:" + conf.GetEncryptedString("dbpass") + "@proxy:5432/app"
	d := config.NewDeploymentConfig()
	d.Variables = config.VariableMaps{
		{Name: "LITERAL", Type: config.VariableTypeSecret, Value: "literal",
			Conditional: config.AVSlice{{Agent: "a1", Value: "agent-literal"}, {Agent: "a2", Value: ""}}},
		{Name: "WHOLE", Type: config.VariableTypeSecret, Value: whole},
		{Name: "DSN", Type: config.VariableTypeSecret, Value: dsn},
		{Name: "EMPTY", Type: config.VariableTypeSecret, Value: ""},
		{Name: "ENV", Type: config.VariableTypeEnv, Value: "env-literal"},
	}
	d.Storages.GitClones = config.GitClones{{Name: "g", GitPass: "gitpass"}, {Name: "nopass"}}
	d.Storages.S3Mounts = config.S3Mounts{{Name: "s", SecretKey: "s3secret"}}

	if !cl.EncryptDeploymentSecrets(d) {
		t.Fatalf("expected literals to be reported as encrypted")
	}
	lit := d.Variables[0]
	for name, v := range map[string]string{"LITERAL": lit.Value, "LITERAL@a1": lit.Conditional[0].Value,
		"git": d.Storages.GitClones[0].GitPass, "s3": d.Storages.S3Mounts[0].SecretKey} {
		if !strings.HasPrefix(v, "hash_") {
			t.Fatalf("%s not encrypted: %q", name, v)
		}
	}
	if got := conf.GetDecryptedPassword("", lit.Conditional[0].Value); got != "agent-literal" {
		t.Fatalf("per-agent value decrypts to %q", got)
	}
	if lit.Conditional[1].Value != "" || d.Variables[3].Value != "" || d.Storages.GitClones[1].GitPass != "" {
		t.Fatalf("empty values must stay empty")
	}
	if d.Variables[1].Value != whole {
		t.Fatalf("an encrypted value was encrypted again: %q", d.Variables[1].Value)
	}
	if d.Variables[2].Value != dsn {
		t.Fatalf("a DSN holding a ciphertext was wrapped: %q", d.Variables[2].Value)
	}
	if got := conf.GetDecryptedEmbedded("DSN", d.Variables[2].Value); got != "postgres://app:dbpass@proxy:5432/app" {
		t.Fatalf("DSN no longer decrypts in place: %q", got)
	}
	if d.Variables[4].Value != "env-literal" {
		t.Fatalf("env variable changed: %q", d.Variables[4].Value)
	}

	before := lit.Value
	if cl.EncryptDeploymentSecrets(d) {
		t.Fatalf("second pass reported a change")
	}
	if d.Variables[0].Value != before {
		t.Fatalf("second pass changed an encrypted value")
	}
	if cl.EncryptDeploymentSecrets(nil) {
		t.Fatalf("nil deployment reported a change")
	}
}

func TestAddSeededApp_EncryptsTemplateSecretLiterals(t *testing.T) {
	workingDir := t.TempDir()
	localPath := filepath.Join(workingDir, ".templates", "apps", "secret-seed.toml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte(templateSecretsTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	cl := &Cluster{
		Name:       "test-cluster",
		WorkingDir: workingDir,
		crcTable:   crc64.MakeTable(crc64.ECMA),
		Conf: &config.Config{
			WorkingDir: workingDir,
			SecretKey:  templateSecretsKey,
			Apps:       make([]*config.AppConfig, 0),
		},
	}

	if err := cl.AddSeededApp("seed-host", "8080", "nginx:latest", "secret-seed", false); err != nil {
		t.Fatalf("AddSeededApp failed: %v", err)
	}
	seeded := cl.GetAppConfig("seed-host", "8080")
	if seeded == nil {
		t.Fatalf("seeded app not found")
	}
	checkDeploymentSecretsEncrypted(t, cl.Conf, seeded.Deployment)
}

func TestImportAppConfig_EncryptsPeerClearSecrets(t *testing.T) {
	cl := newTestClusterForAppImport(t, "c1")
	cl.Conf.SecretKey = templateSecretsKey
	// the peer's export, holding the clear literals of an app it created from a template
	tree, err := toml.Load(exportTOMLForImport(t, cl, "peerapp1", "8080"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := toml.Load(templateSecretsTOML)
	if err != nil {
		t.Fatal(err)
	}
	tree.Set("deployment", secrets.Get("deployment"))

	if err := cl.ImportAppConfig("peerapp1", "8080", tree.String()); err != nil {
		t.Fatalf("ImportAppConfig failed: %v", err)
	}
	loaded := cl.GetAppConfig("peerapp1", "8080")
	if loaded == nil {
		t.Fatalf("imported app not found")
	}
	checkDeploymentSecretsEncrypted(t, cl.Conf, loaded.Deployment)

	saved, err := os.ReadFile(filepath.Join(cl.WorkingDir, "apps", "peerapp1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"secret-literal", "cond-default-literal", "cond-agent-literal", "gitpass-literal", "s3secret-literal"} {
		if strings.Contains(string(saved), literal) {
			t.Fatalf("saved config still holds %q in clear:\n%s", literal, saved)
		}
	}
}
