// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"github.com/iancoleman/strcase"
	"github.com/tidwall/sjson"
)

// SecretMask is the value every secret configuration key shows on the wire.
const SecretMask = "*:*"

// MaskSecretsJSON replaces, in a JSON document holding this configuration under prefix
// ("config." in a cluster or server object, "" for a bare configuration), the value of
// every secret key with SecretMask. Every API path that sends a configuration (REST, MCP)
// goes through it: api-credentials holds the local admin password in clear.
func (conf *Config) MaskSecretsJSON(doc []byte, prefix string) ([]byte, error) {
	var err error
	for key := range conf.Secrets.Snapshot() {
		doc, err = sjson.SetBytes(doc, prefix+strcase.ToLowerCamel(key), SecretMask)
		if err != nil {
			return nil, err
		}
	}
	return doc, nil
}
