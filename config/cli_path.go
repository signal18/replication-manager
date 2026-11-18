// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepManAbsolutePath returns the absolute path of the running replication-manager executable,
// symlinks resolved.
func RepManAbsolutePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	finfo, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if finfo.Mode()&os.ModeSymlink == 0 {
		return filepath.Abs(path)
	}
	truepath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(truepath)
}

// GetReplicationManagerCliPath returns the replication-manager-cli binary: the configured
// replication-manager-cli-path, else the one beside the running executable, else the one in
// PATH, else the bare name (the caller's exec then fails with a clear "not found").
func (conf *Config) GetReplicationManagerCliPath() string {
	if p := strings.TrimSpace(conf.ReplicationManagerCliPath); p != "" {
		return conf.ReplicationManagerCliPath
	}
	if self, err := RepManAbsolutePath(); err == nil {
		clipath := filepath.Join(filepath.Dir(self), "replication-manager-cli")
		if _, err := os.Stat(clipath); err == nil {
			return clipath
		}
	}
	if path, err := exec.LookPath("replication-manager-cli"); err == nil {
		return path
	}
	return "replication-manager-cli"
}
