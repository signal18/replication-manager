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

// The settings of the manifest. Secrets (secret, encryption-key, encryption-key-password)
// come from the environment, by wire.SecretEnv(name).
const (
	setBaseURL     = "base-url"                // https://<host>:8007
	setDatastore   = "datastore"               // the datastore, created on the PBS by its admin
	setNamespace   = "namespace"               // the cluster's namespace in it (a/b/c); required to remove
	setAuthID      = "auth-id"                 // the API token: user@realm!token
	setFingerprint = "fingerprint"             // the server certificate's SHA-256 fingerprint
	setChunkCache  = "chunk-cache"             // restore: chunks kept in memory (default 16, about 64 MB)
	setSecret      = "secret"                  // the API token's secret (secret)
	setKey         = "encryption-key"          // the client encryption key, its JSON (secret); none: not encrypted
	setKeyPassword = "encryption-key-password" // the key's passphrase when it has one (secret)
)

// No sync: copying between PBS datastores is a sync job of the PBS itself.
var capabilities = []string{"edit", "remove", "probe", "size", "purge"}

func manifests() []wire.Manifest {
	return []wire.Manifest{{
		WireVersion: wire.WireVersion,
		Name:        pluginPBS,
		Description: "Proxmox Backup Server",
		Tier:        "enterprise",
		Settings: []wire.Setting{
			{Name: setBaseURL, Type: "string"},
			{Name: setDatastore, Type: "string"},
			{Name: setNamespace, Type: "string"},
			{Name: setAuthID, Type: "string"},
			{Name: setFingerprint, Type: "string"},
			{Name: setSecret, Type: "secret"},
			{Name: setKey, Type: "secret"},
			{Name: setKeyPassword, Type: "secret"},
			{Name: setChunkCache, Type: "int", Group: "advanced"},
		},
		Capabilities: append([]string(nil), capabilities...),
	}}
}

// isPBS reports whether a Store.Plugin is this one: the manifest name, or its short form.
func isPBS(plugin string) bool {
	return plugin == pluginPBS || plugin == "pbs" || strings.HasSuffix(plugin, "-pbs")
}
