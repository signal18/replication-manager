// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// client runs proxmox-backup-client on one archive: the repository and the secrets go in
// the client's environment (PBS_REPOSITORY, PBS_PASSWORD, PBS_FINGERPRINT,
// PBS_ENCRYPTION_PASSWORD), the encryption key through a pipe (--keyfd 3): no secret on a
// command line, none in a file.
type client struct {
	path      string
	repo      string
	namespace string
	env       []string
	key       []byte // the encryption key's JSON, nil: not encrypted
	change    string
}

// newClient builds the client of a store from its settings and the plugin's environment.
func newClient(st wire.Store) (*client, error) {
	s := func(name string) string { return strings.TrimSpace(st.Settings[name]) }
	server, datastore := s(setServer), s(setDatastore)
	if server == "" || datastore == "" {
		return nil, errors.New("the archive needs its server and datastore")
	}
	authID := s(setAuthID)
	if authID == "" {
		authID = "root@pam"
	}
	repo := authID + "@" + server
	if p := s(setPort); p != "" && p != "0" {
		if _, err := strconv.Atoi(p); err != nil {
			return nil, fmt.Errorf("port %q is not a number", p)
		}
		repo += ":" + p
	}
	repo += ":" + datastore

	c := &client{path: s(setClientPath), repo: repo, namespace: strings.Trim(s(setNamespace), "/"), change: s(setChangeMode)}
	if c.path == "" {
		c.path = "proxmox-backup-client"
	}
	switch c.change {
	case "", "legacy", "data", "metadata":
	default:
		return nil, fmt.Errorf("change-mode %q is not legacy, data or metadata", c.change)
	}

	// the parent's environment without replication-manager's secrets, then the client's own
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "REPMAN_ARCHIVEPLUGIN_") && !strings.HasPrefix(kv, "PBS_") {
			c.env = append(c.env, kv)
		}
	}
	c.env = append(c.env, "PBS_REPOSITORY="+repo, "PROXMOX_OUTPUT_FORMAT=text")
	if pw := os.Getenv(wire.SecretEnv(setPassword)); pw != "" {
		c.env = append(c.env, "PBS_PASSWORD="+pw)
	}
	if fp := s(setFingerprint); fp != "" {
		c.env = append(c.env, "PBS_FINGERPRINT="+fp)
	}
	if key := os.Getenv(wire.SecretEnv(setKey)); strings.TrimSpace(key) != "" {
		c.key = []byte(key)
		if kp := os.Getenv(wire.SecretEnv(setKeyPassword)); kp != "" {
			c.env = append(c.env, "PBS_ENCRYPTION_PASSWORD="+kp)
		}
	}
	return c, nil
}

// nsArgs is the --ns of the archive's namespace, none for the datastore's root.
func (c *client) nsArgs() []string {
	if c.namespace == "" {
		return nil
	}
	return []string{"--ns", c.namespace}
}

// run runs the client with args; withKey adds the encryption key (--keyfd 3) when the
// archive has one. It returns the client's stdout; its stderr is the plugin's.
func (c *client) run(withKey bool, args ...string) ([]byte, error) {
	cmd := exec.Command(c.path, args...)
	cmd.Env = c.env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if withKey && c.key != nil {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		cmd.ExtraFiles = []*os.File{r} // fd 3 in the child
		cmd.Args = append(cmd.Args, "--keyfd", "3", "--crypt-mode", "encrypt")
		// the key is a few hundred bytes: it fits the pipe's buffer before the client reads
		if _, err := w.Write(c.key); err != nil {
			w.Close()
			return nil, err
		}
		w.Close()
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.Bytes(), fmt.Errorf("%s %s: exit %d", c.path, args[0], exit.ExitCode())
		}
		return out.Bytes(), fmt.Errorf("%s %s: %w", c.path, args[0], err)
	}
	return out.Bytes(), nil
}
