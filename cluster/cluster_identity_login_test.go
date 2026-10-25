// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const testCloud18Realm = "https://identity.example/realms/cloud18"

// connectCloud18ForTest writes the Cloud18 login file of the cluster's instance (#2159), as a
// person's device login does: the offline token encrypted, in a working directory of its own.
func connectCloud18ForTest(t *testing.T, cl *Cluster) {
	t.Helper()
	cl.Conf.WorkingDir = t.TempDir()
	cl.Conf.SecretKey = []byte("0123456789abcdef")
	cl.Conf.Cloud18IdentityURL = testCloud18Realm
	data, _ := json.Marshal(map[string]string{"issuer": testCloud18Realm, "refresh_token": cl.Conf.GetEncryptedString("offline-test"), "user": "acme", "sub": "2"})
	if err := os.WriteFile(filepath.Join(cl.Conf.WorkingDir, "cloud18-identity.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// #2159: the login is read from the instance's file; another instance (working directory) has none
func TestCloud18LoginReadFromFile(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	connectCloud18ForTest(t, cl)
	if !cl.Conf.HasCloud18Login() || cl.Conf.Cloud18IdentityStatus().User != "acme" {
		t.Fatalf("status %+v", cl.Conf.Cloud18IdentityStatus())
	}
	other, _ := newTestClusterServer(t)
	other.Conf.WorkingDir, other.Conf.Cloud18IdentityURL = t.TempDir(), testCloud18Realm
	if other.Conf.HasCloud18Login() {
		t.Fatal("another instance has no login")
	}
}
