// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package githelper

import (
	"strings"
	"testing"
)

// a GitLab answer in the debug log shows its fields, never its tokens
func TestRedactTokens(t *testing.T) {
	in := `{"access_token":"a1b2","token_type":"Bearer","refresh_token": "r3","id_token":"x.y.z","token":"glpat-1","scope":"api"}`
	out := redactTokens([]byte(in))
	for _, secret := range []string{"a1b2", "r3", "x.y.z", "glpat-1"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q left in %s", secret, out)
		}
	}
	if !strings.Contains(out, `"token_type":"Bearer"`) || !strings.Contains(out, `"scope":"api"`) {
		t.Fatalf("the other fields must stay: %s", out)
	}
}
