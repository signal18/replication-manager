// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2159: the Cloud18 identity client secret is saved encrypted; the save wrote it empty and a
// restart lost it (found on dev3)
func TestIdentityClientSecretSaved(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"cloud18-identity-client-secret": {Value: "s3cr3t"}})
	v := cl.GetEncryptedValueFromMemory("cloud18-identity-client-secret")
	if !strings.HasPrefix(v, "hash_") || cl.Conf.GetDecryptedPassword("t", v) != "s3cr3t" {
		t.Fatalf("saved value %q", v)
	}
}
