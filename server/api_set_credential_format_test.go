// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// a credential setting is user:password with a user and a password, in the encoding the GUI
// sends for its key; anything else is refused before any store or field changes
func TestCheckCredentialSetting(t *testing.T) {
	const secret = "s3cr3t-Value"
	clear := []string{"db-servers-credential", "replication-credential", "proxysql-servers-credential", "maxscale-servers-credential", "shardproxy-servers-credential"}
	for _, key := range clear {
		if err := checkCredentialSetting(key, "repman:"+secret); err != nil {
			t.Fatalf("%s user:password refused: %v", key, err)
		}
		if err := checkCredentialSetting(key, "repman:pa:ss"); err != nil {
			t.Fatalf("%s a colon in the password refused: %v", key, err)
		}
		for _, bad := range []string{b64("repman:" + secret), "repman", "repman:", ":" + secret, ""} {
			err := checkCredentialSetting(key, bad)
			if !errors.Is(err, ErrInvalidCredentialFormat) {
				t.Fatalf("%s %q accepted: %v", key, bad, err)
			}
			if strings.Contains(err.Error(), secret) || (bad != "" && strings.Contains(err.Error(), bad)) {
				t.Fatalf("%s: the refusal echoes the value: %s", key, err)
			}
		}
	}
	for _, key := range []string{"backup-verification-credential", "cloud18-dba-user-credentials", "cloud18-sponsor-user-credentials"} {
		if err := checkCredentialSetting(key, b64("dba:"+secret)); err != nil {
			t.Fatalf("%s base64 user:password refused: %v", key, err)
		}
		for _, bad := range []string{"dba:" + secret, "!!not-base64!!", b64("dba"), b64(":" + secret), b64("dba:")} {
			if err := checkCredentialSetting(key, bad); !errors.Is(err, ErrInvalidCredentialFormat) {
				t.Fatalf("%s %q accepted: %v", key, bad, err)
			}
		}
	}
	// an empty value clears the optional ones only
	for key, clearable := range map[string]bool{"backup-verification-credential": true, "cloud18-sponsor-user-credentials": true, "cloud18-dba-user-credentials": false, "db-servers-credential": false} {
		err := checkCredentialSetting(key, "")
		if clearable != (err == nil) {
			t.Fatalf("%s empty value: got %v, clearable %v", key, err, clearable)
		}
	}
	if err := checkCredentialSetting("mail-smtp-user", "anything"); err != nil {
		t.Fatalf("a non-credential setting is checked: %v", err)
	}
}

// the value the regtest sent: db-servers-credential base64-encoded is refused with 400, the
// credential unchanged, and the answer never carries the value
func TestSetSettingsRefusesMalformedCredential(t *testing.T) {
	const secret = "s3cr3t-Value"
	cl := &cluster.Cluster{Conf: &config.Config{User: "root:current", RplUser: "repl:current", ConfRewrite: true}}
	repman := &ReplicationManager{}
	for _, key := range []string{"db-servers-credential", "replication-credential"} {
		err := repman.setClusterSetting(cl, key, b64("root:"+secret))
		if !errors.Is(err, ErrInvalidCredentialFormat) {
			t.Fatalf("%s base64: got %v, want a format refusal", key, err)
		}
		if code := setSettingErrorCode(err); code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", key, code)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: the refusal echoes the value", key)
		}
		if cl.Conf.User != "root:current" || cl.Conf.RplUser != "repl:current" {
			t.Fatalf("%s: the credential changed", key)
		}
	}
	if code := setSettingErrorCode(cluster.ErrCredentialChangeRefused); code != http.StatusConflict {
		t.Fatalf("a refused change: status %d, want 409", code)
	}
}
