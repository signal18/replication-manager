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
	if !found || strings.TrimSpace(user) == "" {
		return errors.New("onpremise-ssh-credential must be user:password, the password empty to use the private key")
	}
	if err := cluster.CredentialChain().Set("onpremise-ssh-credential", cred); err != nil {
		return err
	}
	cluster.Conf.OnPremiseSSHCredential = cred
	cluster.MarkSecretVersionStoreDirty()
	return nil
}

// checkOnPremiseSSHKey refuses what is not a private key the SSH dial can use: not a key, or a
// key protected by a passphrase (nothing could unlock it). The parser's errors never quote the
// key.
func checkOnPremiseSSHKey(data []byte) error {
	if _, err := ssh.ParseRawPrivateKey(data); err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return errors.New("onpremise-ssh-private-key: a key protected by a passphrase is not supported")
		}
		return fmt.Errorf("onpremise-ssh-private-key is not a private key: %v", err)
	}
	return nil
}

// SetOnPremiseSSHPrivateKey sets onpremise-ssh-private-key. The setting is the path of the key
// on the replication-manager host, as before; value is either that path (absolute, an existing
// file; empty: the user's ~/.ssh/id_rsa) or the key itself (PEM / OpenSSH, beginning with
// -----BEGIN). A key is checked, written to GetOnPremiseSSHKeyFile 0600 and the setting set to that
// path: the key content is never in the config, in git or in a log. A key file in a path is
// checked the same way. When the setting leaves the file written here, the file is removed: no
// key stays on disk that no setting names.
func (cluster *Cluster) SetOnPremiseSSHPrivateKey(value string) error {
	trimmed := strings.TrimSpace(value)
	managed := cluster.GetOnPremiseSSHKeyFile()
	switch {
	case trimmed == "":
		cluster.Conf.OnPremiseSSHPrivateKey = ""
	case strings.HasPrefix(trimmed, "-----BEGIN"):
		if err := checkOnPremiseSSHKey([]byte(trimmed + "\n")); err != nil {
			return err
		}
		// an existing file keeps its mode through WriteFileDurable: restricted before the new key
		// is written, so the key is never readable by another user, even for a moment
		if err := os.Chmod(managed, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("onpremise-ssh-private-key: cannot restrict the key file: %v", err)
		}
		// the directory is created 0700 by WriteFileDurable, the file 0600
		if err := misc.WriteFileDurable(managed, []byte(trimmed+"\n"), 0o600, ".onpremise_ssh_key-*.tmp"); err != nil {
			return fmt.Errorf("onpremise-ssh-private-key: cannot write the key file: %v", err)
		}
		cluster.Conf.OnPremiseSSHPrivateKey = managed
	default:
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf("onpremise-ssh-private-key must be an absolute path or the key itself, got %q", trimmed)
		}
		fi, err := os.Stat(trimmed)
		if err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("onpremise-ssh-private-key: no key file at %s", trimmed)
		}
		data, err := os.ReadFile(trimmed)
		if err != nil {
			return fmt.Errorf("onpremise-ssh-private-key: cannot read %s", trimmed)
		}
		if err := checkOnPremiseSSHKey(data); err != nil {
			return err
		}
		cluster.Conf.OnPremiseSSHPrivateKey = trimmed
	}
	if cluster.Conf.OnPremiseSSHPrivateKey != managed {
		// the key received earlier is no longer named by the setting
		os.Remove(managed)
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
