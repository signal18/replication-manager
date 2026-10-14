// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package sqlmask

import (
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	for _, c := range []struct{ in, secret, keep string }{
		// the replica error of preprod curepipe (2026-10-14), password replaced
		{"Error 'Operation CREATE USER failed for 'forgejo1'@'%'' on query. Default database: ''. Query: 'CREATE USER `forgejo1`@`%` IDENTIFIED BY 'Xq7kIn9ZpL''", "Xq7kIn9ZpL", "CREATE USER `forgejo1`@`%` IDENTIFIED BY '*****'"},
		{"ALTER USER 'repman'@'%' IDENTIFIED BY 'it''s s3cret'", "s3cret", "IDENTIFIED BY '*****'"},
		{`ALTER USER 'a'@'%' IDENTIFIED BY "dbl\"quoted"`, "quoted", "IDENTIFIED BY '*****'"},
		{"GRANT ALL ON *.* TO 'x'@'%' IDENTIFIED BY PASSWORD '*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19'", "2470C0C0", "IDENTIFIED BY PASSWORD '*****'"},
		{"CREATE USER u IDENTIFIED WITH caching_sha2_password BY 'pw1234'", "pw1234", "IDENTIFIED WITH caching_sha2_password BY '*****'"},
		{"CREATE USER u IDENTIFIED VIA ed25519 USING PASSWORD('pw5678')", "pw5678", "USING PASSWORD('*****')"},
		{"SET PASSWORD FOR 'u'@'%' = 'pw9999'", "pw9999", "= '*****'"},
		{"SET PASSWORD = PASSWORD('pw0000')", "pw0000", "PASSWORD('*****')"},
		{"CHANGE MASTER TO MASTER_HOST='db2', MASTER_PASSWORD='rplpw', MASTER_USE_GTID=slave_pos", "rplpw", "MASTER_PASSWORD='*****', MASTER_USE_GTID"},
		{"CHANGE REPLICATION SOURCE TO SOURCE_PASSWORD = 'srcpw'", "srcpw", "SOURCE_PASSWORD = '*****'"},
		{"--host=db1 --user=root --password=topsecret --port=3306", "topsecret", "--password=*****"},
		// a statement cut short by the error message
		{"Query: 'CREATE USER `a`@`%` IDENTIFIED BY 'cutshortpw", "cutshortpw", "IDENTIFIED BY '*****'"},
	} {
		got := Secrets(c.in)
		if strings.Contains(got, c.secret) || !strings.Contains(got, c.keep) {
			t.Errorf("Secrets(%q) = %q: must hide %q and keep %q", c.in, got, c.secret, c.keep)
		}
	}
	for _, s := range []string{"", "SELECT 1", "Column count of mysql.proc is wrong. Expected 22, found 21", "UPDATE t SET password_hash = ? WHERE id = 1"} {
		if got := Secrets(s); got != s {
			t.Errorf("no secret: %q became %q", s, got)
		}
	}
}
