// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
	"golang.org/x/crypto/ssh"
)

// GetOnPremiseSSHKeyFile is where a private key received as content through the settings API is
// kept: in the cluster's working directory, 0600, a file the config git never stages (it stages
// <cluster>/*.toml and a few named json files), so the key never reaches the config repository
// (F9). onpremise-ssh-private-key then holds this path.
func (cluster *Cluster) GetOnPremiseSSHKeyFile() string {
	return filepath.Join(cluster.WorkingDir, ".ssh", "onpremise_ssh_key")
}

// SetOnPremiseSSHCredential sets onpremise-ssh-credential, the user:password the on-premise
// orchestrator connects over SSH with (an empty password: the private key). Through the
// credential chain like every credential: a version in every active store, encrypted in the
// saved config (F9), never logged (F10); refused when the chain refuses it.
func (cluster *Cluster) SetOnPremiseSSHCredential(cred string) error {
	user, _, found := strings.Cut(cred, ":")
	if !found || user == "" {
		return errors.New("onpremise-ssh-credential must be user:password, the password empty to use the private key")
	}
	if err := cluster.CredentialChain().Set("onpremise-ssh-credential", cred); err != nil {
		return err
	}
	cluster.Conf.OnPremiseSSHCredential = cred
	cluster.MarkSecretVersionStoreDirty()
	return nil
}

// SetOnPremiseSSHPrivateKey sets onpremise-ssh-private-key. The setting is the path of the key
// on the replication-manager host, as before; value is either that path (absolute, an existing
// file; empty: the user's ~/.ssh/id_rsa) or the key itself (PEM / OpenSSH, beginning with
// -----BEGIN). A key is checked, written to GetOnPremiseSSHKeyFile 0600 and the setting set to that
// path: the key content is never in the config, in git or in a log.
func (cluster *Cluster) SetOnPremiseSSHPrivateKey(value string) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		cluster.Conf.OnPremiseSSHPrivateKey = ""
	case strings.HasPrefix(trimmed, "-----BEGIN"):
		if _, err := ssh.ParseRawPrivateKey([]byte(trimmed + "\n")); err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return errors.New("onpremise-ssh-private-key: a key protected by a passphrase is not supported")
			}
			// the parser's error never quotes the key
			return fmt.Errorf("onpremise-ssh-private-key is not a private key: %v", err)
		}
		path := cluster.GetOnPremiseSSHKeyFile()
		if err := misc.WriteFileDurable(path, []byte(trimmed+"\n"), 0o600, ".onpremise_ssh_key-*.tmp"); err != nil {
			return fmt.Errorf("onpremise-ssh-private-key: cannot write the key file: %v", err)
		}
		// an existing file keeps its mode through WriteFileDurable: the key is the owner's only
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("onpremise-ssh-private-key: cannot restrict the key file: %v", err)
		}
		cluster.Conf.OnPremiseSSHPrivateKey = path
	default:
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf("onpremise-ssh-private-key must be an absolute path or the key itself, got %q", trimmed)
		}
		if fi, err := os.Stat(trimmed); err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("onpremise-ssh-private-key: no key file at %s", trimmed)
		}
		cluster.Conf.OnPremiseSSHPrivateKey = trimmed
	}
	// the key check (WARN0169) runs again with the new key
	cluster.HaveSSHKeyChecked = false
	return nil
}

// SetOnPremiseSSHSetting sets one of the plain settings of the on-premise orchestrator:
// onpremise-ssh-port (1-65535) and the scripts, each a local file of the replication-manager
// host sent over SSH to the server (absolute path of an existing file; empty: the built-in
// command).
func (cluster *Cluster) SetOnPremiseSSHSetting(name string, value string) error {
	value = strings.TrimSpace(value)
	if name == "onpremise-ssh-port" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("onpremise-ssh-port must be a port number, got %q", value)
		}
		cluster.Conf.OnPremiseSSHPort = port
		return nil
	}
	var field *string
	switch name {
	case "onpremise-ssh-start-db-script":
		field = &cluster.Conf.OnPremiseSSHStartDbScript
	case "onpremise-ssh-start-proxy-script":
		field = &cluster.Conf.OnPremiseSSHStartProxyScript
	case "onpremise-ssh-stop-proxy-script":
		field = &cluster.Conf.OnPremiseSSHStopProxyScript
	case "onpremise-ssh-db-job-script":
		field = &cluster.Conf.OnPremiseSSHDbJobScript
	case "onpremise-ssh-upgrade-db-script":
		field = &cluster.Conf.OnPremiseSSHUpgradeDbScript
	default:
		return fmt.Errorf("%s is not an on-premise SSH setting", name)
	}
	if value != "" {
		if !filepath.IsAbs(value) {
			return fmt.Errorf("%s must be an absolute path, got %q", name, value)
		}
		if fi, err := os.Stat(value); err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: no script at %s", name, value)
		}
	}
	*field = value
	return nil
}
