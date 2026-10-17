// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package s3mount is the contract between replication-manager and repman-s3mount (#2091), the
// program that serves the backups folder mount: an S3 bucket's folder mounted with FUSE in a
// process of its own, so a stalled S3 or a stuck FUSE request can never freeze the monitor.
// replication-manager starts it with its Config on stdin, waits for its Status line on stdout,
// stops it with SIGTERM and kills it when it does not answer: killing the process that serves a
// FUSE mount makes every pending request on it fail instead of hang. The S3 keys are in a file
// only replication-manager and the helper read (0600), rewritten before short-lived keys end,
// so a renewal never remounts. Standard library only.
package s3mount

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// BinaryName is the helper program, next to the plugins.
const BinaryName = "repman-s3mount"

// Config is what the helper mounts.
type Config struct {
	MountPoint  string        `json:"mountPoint"`
	Bucket      string        `json:"bucket"` // goofys spec: bucket or bucket:prefix
	Endpoint    string        `json:"endpoint"`
	Region      string        `json:"region"`
	KeysFile    string        `json:"keysFile"`
	Debug       bool          `json:"debug,omitempty"`
	HTTPTimeout time.Duration `json:"httpTimeout,omitempty"` // each S3 request; 0: 2 minutes
}

// Keys are the S3 keys of the KeysFile; Expiration zero for static keys.
type Keys struct {
	AccessKey    string    `json:"accessKey"`
	SecretKey    string    `json:"secretKey"`
	SessionToken string    `json:"sessionToken,omitempty"`
	Expiration   time.Time `json:"expiration,omitempty"`
}

// Status is the helper's one line on stdout once mounted, or with Error when it failed.
type Status struct {
	Mounted bool   `json:"mounted"`
	Error   string `json:"error,omitempty"`
}

// WriteKeys writes the keys file, readable by its owner only, through a rename so the helper
// never reads it half written.
func WriteKeys(path string, k Keys) error {
	b, err := json.Marshal(k)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadKeys reads the keys file.
func ReadKeys(path string) (Keys, error) {
	var k Keys
	b, err := os.ReadFile(path)
	if err != nil {
		return k, err
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return k, fmt.Errorf("keys file: %w", err)
	}
	if k.AccessKey == "" || k.SecretKey == "" {
		return k, fmt.Errorf("keys file: no keys")
	}
	return k, nil
}

// ReadConfig reads the helper's config on its stdin.
func ReadConfig(r io.Reader) (Config, error) {
	var c Config
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return c, fmt.Errorf("s3mount config: %w", err)
	}
	if c.MountPoint == "" || c.Bucket == "" || c.KeysFile == "" {
		return c, fmt.Errorf("s3mount config: mountPoint, bucket and keysFile are required")
	}
	return c, nil
}

// WriteStatus writes the helper's status line.
func WriteStatus(w io.Writer, s Status) error {
	return json.NewEncoder(w).Encode(s)
}
