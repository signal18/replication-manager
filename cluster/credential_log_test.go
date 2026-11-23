// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// a credential stored without its colon is all user: the database's access denied and the
// replication's IO error name it, and neither may reach a log or a state (F10)
func TestRedactCredentialUsers(t *testing.T) {
	malformed := base64.StdEncoding.EncodeToString([]byte("root:s3cr3t"))
	c := &Cluster{Conf: &config.Config{}}
	c.Conf.Secrets = config.NewSecrets(map[string]config.Secret{
		"db-servers-credential":  {Value: malformed},
		"replication-credential": {Value: "repl:pw"},
	})

	denied := errors.New("Error 1045 (28000): Access denied for user '" + malformed + "'@'10.0.0.5' (using password: NO)")
	got := c.redactCredentialError(denied)
	if strings.Contains(got, malformed) || !strings.Contains(got, "'<db-servers-credential user>'@'10.0.0.5'") {
		t.Fatalf("access denied not redacted: %s", got)
	}

	io := "error connecting to master 'repl@db1:3306' - retry-time: 10 message: Access denied for user 'repl'@'db2'"
	got = c.redactCredentialUsers(io)
	if strings.Contains(got, "'repl@") || strings.Contains(got, "'repl'@") || !strings.Contains(got, "'<replication-credential user>@db1:3306'") {
		t.Fatalf("IO error not redacted: %s", got)
	}

	// a server's own copy of the monitoring user
	got = c.redactCredentialUsers("Access denied for user 'other'@'h'", "other")
	if !strings.Contains(got, "'<db-servers-credential user>'@'h'") {
		t.Fatalf("the server's user not redacted: %s", got)
	}

	// only the quoted forms: a short user elsewhere in the text is left
	if got := c.redactCredentialUsers("table repl.t1 is read only"); got != "table repl.t1 is read only" {
		t.Fatalf("unquoted text changed: %s", got)
	}
	if c.redactCredentialError(nil) != "" {
		t.Fatal("a nil error is not empty")
	}
}
