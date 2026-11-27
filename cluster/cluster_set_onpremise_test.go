// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml"
	"golang.org/x/crypto/ssh"
)

func testOnPremiseKeyPEM(t *testing.T, passphrase string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func savedClusterSection(t *testing.T, cl *Cluster) *toml.Tree {
	t.Helper()
	if _, err := cl.SaveConfigFile(); err != nil {
		t.Fatal(err)
	}
	tree, err := toml.LoadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := tree.Get("saved-" + cl.Name).(*toml.Tree)
	if !ok {
		t.Fatalf("no [saved-%s] section", cl.Name)
	}
	return saved
}

func maskedConfJSON(t *testing.T, cl *Cluster) string {
	t.Helper()
	doc, err := json.Marshal(cl.Conf)
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = cl.Conf.MaskSecretsJSON(doc, ""); err != nil {
		t.Fatal(err)
	}
	return string(doc)
}

// The on-premise SSH credential set through the API's setter is masked in the API JSON, saved
// encrypted with the instance key (F9) and read back as it was set.
func TestOnPremiseSSHCredentialMaskedAndSavedEncrypted(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	cl.Conf.MonitoringSecretVersioning = true
	const cred = "sshuser:MARKERSSHPASS"
	if err := cl.SetOnPremiseSSHCredential(cred); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cl.GetOnPremiseSSHUser() != "sshuser" || cl.GetOnPremiseSSHPass() != "MARKERSSHPASS" {
		t.Fatalf("credential in memory: %q / %q", cl.GetOnPremiseSSHUser(), cl.GetOnPremiseSSHPass())
	}
	if doc := maskedConfJSON(t, cl); strings.Contains(doc, "MARKER") {
		t.Fatalf("the SSH password is in the API JSON")
	}

	saved := savedClusterSection(t, cl)
	raw, _ := saved.Get("onpremise-ssh-credential").(string)
	if raw == "" || strings.Contains(raw, "MARKER") {
		t.Fatalf("onpremise-ssh-credential saved as %q, want it encrypted", raw)
	}
	if got := cl.Conf.DecryptSecretValue("onpremise-ssh-credential", raw); got != cred {
		t.Fatalf("saved value decrypts to %q, want %q", got, cred)
	}

	// a start reads it back from the saved config
	again := newSecretURLTestCluster(t, map[string]interface{}{"onpremise-ssh-credential": raw})
	again.Conf.SecretKey = cl.Conf.SecretKey
	again.Conf.DecryptSecretsFromConfig()
	if again.GetOnPremiseSSHUser() != "sshuser" || again.GetOnPremiseSSHPass() != "MARKERSSHPASS" {
		t.Fatalf("credential after a reread: %q / %q", again.GetOnPremiseSSHUser(), again.GetOnPremiseSSHPass())
	}

	// key authentication: a user and no password
	if err := cl.SetOnPremiseSSHCredential("root:"); err != nil {
		t.Fatalf("user with no password refused: %v", err)
	}
	for _, bad := range []string{"", "nouser", ":pass"} {
		if err := cl.SetOnPremiseSSHCredential(bad); err == nil {
			t.Errorf("credential %q accepted", bad)
		}
	}
}

// A private key received as content is written 0600 in the cluster's working directory and the
// setting holds its path: the key is never in the API JSON nor in the saved config.
func TestOnPremiseSSHPrivateKeyContent(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	// a running repman has the flag's default: the save writes a setting that differs from it
	cl.Conf.DefaultFlagMap["onpremise-ssh-private-key"] = ""
	cl.HaveSSHKeyChecked = true
	key := testOnPremiseKeyPEM(t, "")
	if err := cl.SetOnPremiseSSHPrivateKey(key); err != nil {
		t.Fatalf("set key content: %v", err)
	}
	path := cl.OnPremiseSSHKeyFile()
	if cl.Conf.OnPremiseSSHPrivateKey != path {
		t.Fatalf("setting is %q, want %q", cl.Conf.OnPremiseSSHPrivateKey, path)
	}
	if cl.HaveSSHKeyChecked {
		t.Fatalf("the key check does not run again for the new key")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", fi.Mode().Perm())
	}
	written, _ := os.ReadFile(path)
	if _, err := ssh.ParsePrivateKey(written); err != nil {
		t.Fatalf("written key does not parse: %v", err)
	}
	// the config git stages <cluster>/*.toml and named json files: never this file
	if filepath.Ext(path) == ".toml" || filepath.Ext(path) == ".json" || filepath.Dir(path) == cl.WorkingDir {
		t.Fatalf("key file %s is where the config git stages files", path)
	}

	if doc := maskedConfJSON(t, cl); strings.Contains(doc, "PRIVATE KEY") {
		t.Fatalf("the key content is in the API JSON")
	}
	saved := savedClusterSection(t, cl)
	if raw, _ := saved.Get("onpremise-ssh-private-key").(string); raw != path {
		t.Fatalf("onpremise-ssh-private-key saved as %q, want the path", raw)
	}
	dump, _ := os.ReadFile(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
	if strings.Contains(string(dump), "PRIVATE KEY") {
		t.Fatalf("the key content is in the saved config")
	}

	// a second key replaces the first, the file stays 0600 even if it was loosened
	os.Chmod(path, 0o644)
	if err := cl.SetOnPremiseSSHPrivateKey(testOnPremiseKeyPEM(t, "")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v after a second key, want 0600", fi.Mode().Perm())
	}

	// refusals leave the setting as it was
	for name, bad := range map[string]string{
		"passphrase":    testOnPremiseKeyPEM(t, "secret"),
		"not a key":     "-----BEGIN OPENSSH PRIVATE KEY-----\nnot a key\n-----END OPENSSH PRIVATE KEY-----\n",
		"relative path": "keys/id_rsa",
		"missing file":  filepath.Join(t.TempDir(), "absent"),
	} {
		if err := cl.SetOnPremiseSSHPrivateKey(bad); err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Errorf("%s: the refusal quotes the key: %v", name, err)
		}
		if cl.Conf.OnPremiseSSHPrivateKey != path {
			t.Fatalf("%s changed the setting to %q", name, cl.Conf.OnPremiseSSHPrivateKey)
		}
	}

	// a path of an existing file, and empty for the default
	other := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(other, []byte(key), 0o600)
	if err := cl.SetOnPremiseSSHPrivateKey(other); err != nil || cl.Conf.OnPremiseSSHPrivateKey != other {
		t.Fatalf("path %s: %v, setting %q", other, err, cl.Conf.OnPremiseSSHPrivateKey)
	}
	if err := cl.SetOnPremiseSSHPrivateKey(""); err != nil || cl.Conf.OnPremiseSSHPrivateKey != "" {
		t.Fatalf("clear: %v, setting %q", err, cl.Conf.OnPremiseSSHPrivateKey)
	}
}

func TestOnPremiseSSHSettings(t *testing.T) {
	cl := newSecretURLTestCluster(t, map[string]interface{}{})
	if err := cl.SetOnPremiseSSHSetting("onpremise-ssh-port", "2222"); err != nil || cl.Conf.OnPremiseSSHPort != 2222 {
		t.Fatalf("port: %v, %d", err, cl.Conf.OnPremiseSSHPort)
	}
	for _, bad := range []string{"0", "70000", "ssh"} {
		if err := cl.SetOnPremiseSSHSetting("onpremise-ssh-port", bad); err == nil {
			t.Errorf("port %q accepted", bad)
		}
	}
	script := filepath.Join(t.TempDir(), "start.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	fields := map[string]*string{
		"onpremise-ssh-start-db-script":    &cl.Conf.OnPremiseSSHStartDbScript,
		"onpremise-ssh-start-proxy-script": &cl.Conf.OnPremiseSSHStartProxyScript,
		"onpremise-ssh-stop-proxy-script":  &cl.Conf.OnPremiseSSHStopProxyScript,
		"onpremise-ssh-db-job-script":      &cl.Conf.OnPremiseSSHDbJobScript,
		"onpremise-ssh-upgrade-db-script":  &cl.Conf.OnPremiseSSHUpgradeDbScript,
	}
	for name, field := range fields {
		if err := cl.SetOnPremiseSSHSetting(name, script); err != nil || *field != script {
			t.Fatalf("%s: %v, %q", name, err, *field)
		}
		if err := cl.SetOnPremiseSSHSetting(name, "relative.sh"); err == nil || *field != script {
			t.Fatalf("%s: a relative path accepted", name)
		}
		if err := cl.SetOnPremiseSSHSetting(name, ""); err != nil || *field != "" {
			t.Fatalf("%s: clear: %v", name, err)
		}
	}
	if err := cl.SetOnPremiseSSHSetting("onpremise-ssh-credential", "x:y"); err == nil {
		t.Fatalf("the credential is set as a plain setting")
	}
}
