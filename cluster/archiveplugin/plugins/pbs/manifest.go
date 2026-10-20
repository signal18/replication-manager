// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"strings"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// The archive plugin this binary serves.
const pluginPBS = "archive-pbs"

// The settings of the manifest. Secrets (password, encryption-key,
// encryption-key-password) come from the environment, by wire.SecretEnv(name).
const (
	setServer      = "server"                  // the PBS host
	setPort        = "port"                    // its API port (default 8007)
	setDatastore   = "datastore"               // the datastore, created on the PBS by its admin
	setNamespace   = "namespace"               // the cluster's namespace in it (a/b/c); required to remove
	setAuthID      = "auth-id"                 // user@realm or user@realm!token
	setFingerprint = "fingerprint"             // the server certificate's SHA-256 fingerprint
	setClientPath  = "client-path"             // proxmox-backup-client (default: from PATH)
	setChangeMode  = "change-mode"             // the client's change detection mode
	setPassword    = "password"                // the auth id's password or API token secret (secret)
	setKey         = "encryption-key"          // the client encryption key, its JSON (secret); none: not encrypted
	setKeyPassword = "encryption-key-password" // the key's passphrase when it has one (secret)
)

// No sync: copying between PBS datastores is a sync job of the PBS itself, not a copy the
// client runs.
var capabilities = []string{"edit", "remove", "probe", "size", "purge"}

func manifests() []wire.Manifest {
	return []wire.Manifest{{
		WireVersion: wire.WireVersion,
		Name:        pluginPBS,
		Description: "Proxmox Backup Server",
		Tier:        "enterprise",
		Settings: []wire.Setting{
			{Name: setServer, Type: "string"},
			{Name: setDatastore, Type: "string"},
			{Name: setNamespace, Type: "string"},
			{Name: setAuthID, Type: "string"},
			{Name: setFingerprint, Type: "string"},
			{Name: setPassword, Type: "secret"},
			{Name: setKey, Type: "secret"},
			{Name: setKeyPassword, Type: "secret"},
			{Name: setPort, Type: "int", Group: "advanced"},
			{Name: setClientPath, Type: "string", Group: "advanced"},
			{Name: setChangeMode, Type: "enum", Values: []string{"legacy", "data", "metadata"}, Group: "advanced"},
		},
		Capabilities: append([]string(nil), capabilities...),
	}}
}

// isPBS reports whether a Store.Plugin is this one: the manifest name, or its short form.
func isPBS(plugin string) bool {
	return plugin == pluginPBS || plugin == "pbs" || strings.HasSuffix(plugin, "-pbs")
}
