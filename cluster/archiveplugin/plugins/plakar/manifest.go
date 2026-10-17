// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"strings"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// The archive plugins this binary serves: one plakar engine, two storages.
const (
	pluginLocal = "archive-plakar-local"
	pluginS3    = "archive-plakar-s3"
)

// The settings of the manifests. Secrets (password) come from the environment, by
// wire.SecretEnv(name); the S3 endpoint and region from Request.Store, its keys from
// wire.EnvS3AccessKey/SecretKey/SessionToken (capability wire.CapS3).
const (
	setLocation    = "location"    // local: the folder of the archive
	setBucket      = "bucket"      // s3: the bucket
	setPrefix      = "prefix"      // s3: the folder in the bucket
	setUseTLS      = "use-tls"     // s3: https to the endpoint (default on)
	setPassword    = "password"    // the archive's passphrase (secret)
	setCompression = "compression" // compress the archive at creation (default on)
	setCacheDir    = "cache-dir"   // the engine's local state cache (default under the user cache dir)
)

// The secret of a sync target: wire.SecretEnv("target-password"), that is
// wire.EnvSecretPrefix + "TARGET_PASSWORD"; the S3 keys of an S3 target likewise
// (target-s3-access-key, target-s3-secret-key, target-s3-session-token), the source's
// S3 keys when unset (same provider).
const (
	secTargetPassword     = "target-password"
	secTargetS3AccessKey  = "target-s3-access-key"
	secTargetS3SecretKey  = "target-s3-secret-key"
	secTargetS3SessionTok = "target-s3-session-token"
)

var capabilities = []string{"edit", "remove", "probe", "size", "purge"}

func manifests() []wire.Manifest {
	common := []wire.Setting{
		{Name: setPassword, Type: "secret"},
		{Name: setCompression, Type: "bool", Group: "advanced"},
		{Name: setCacheDir, Type: "string", Group: "advanced"},
	}
	local := wire.Manifest{
		WireVersion:  wire.WireVersion,
		Name:         pluginLocal,
		Description:  "Local folder",
		Tier:         "enterprise",
		Settings:     append([]wire.Setting{{Name: setLocation, Type: "string"}}, common...),
		Capabilities: append([]string(nil), capabilities...),
	}
	s3 := wire.Manifest{
		WireVersion: wire.WireVersion,
		Name:        pluginS3,
		Description: "S3 bucket",
		Tier:        "enterprise",
		Settings: append([]wire.Setting{
			{Name: setBucket, Type: "string"},
			{Name: setPrefix, Type: "string"},
			{Name: setUseTLS, Type: "bool", Group: "advanced"},
		}, common...),
		Capabilities: append(append([]string(nil), capabilities...), wire.CapS3),
	}
	return []wire.Manifest{local, s3}
}

// storeKind is "local" or "s3" for a Store.Plugin: the manifest name, or its short form.
func storeKind(plugin string) string {
	switch {
	case plugin == pluginLocal || strings.HasSuffix(plugin, "plakar-local") || plugin == "local":
		return "local"
	case plugin == pluginS3 || strings.HasSuffix(plugin, "plakar-s3") || plugin == "s3":
		return "s3"
	}
	return ""
}
