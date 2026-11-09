// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func certTestCluster(t *testing.T, hosts ...string) *Cluster {
	t.Helper()
	cl := &Cluster{Name: "c1", Conf: &config.Config{WorkingDir: t.TempDir(), DBServersTLSCompleteGeneratedCertificate: true}}
	for _, h := range hosts {
		cl.Servers = append(cl.Servers, &ServerMonitor{Host: h})
	}
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	return cl
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// verifies leaf against the CA of dir, for usage
func verifyLeaf(t *testing.T, dir, name string, usage x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	ca, err := readCertificate(filepath.Join(dir, "ca-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := readCertificate(filepath.Join(dir, name+"-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		t.Fatalf("%s certificate does not verify against the CA: %v", name, err)
	}
	key, err := readRSAKey(filepath.Join(dir, name+"-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		t.Fatalf("%s certificate is not for %s-key.pem", name, name)
	}
	return leaf
}

// A fresh cluster gets the whole set, a valid chain.
func TestCreateKeysFreshSet(t *testing.T) {
	cl := certTestCluster(t, "db1", "db2")
	if err := cl.createKeys(); err != nil {
		t.Fatal(err)
	}
	if m := cl.missingCertificateFiles(); len(m) != 0 {
		t.Fatalf("missing after creation: %v", m)
	}
	leaf := verifyLeaf(t, cl.certificateDir(), "server", x509.ExtKeyUsageServerAuth)
	if len(leaf.DNSNames) != 2 {
		t.Fatalf("server certificate names %v, want the 2 servers", leaf.DNSNames)
	}
	verifyLeaf(t, cl.certificateDir(), "client", x509.ExtKeyUsageClientAuth)
}

// The case seen on a dev instance and 6 preprod clusters: added before its servers, the cluster
// kept ca-cert, ca-key and server-key only. Completed later, the CA and the server key are
// unchanged and the new certificates verify against that CA, for that key.
func TestCreateKeysCompletesWithoutOverwriting(t *testing.T) {
	cl := certTestCluster(t, "db1")
	if err := cl.createKeys(); err != nil {
		t.Fatal(err)
	}
	dir := cl.certificateDir()
	for _, f := range []string{"server-cert.pem", "client-cert.pem", "client-key.pem"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	caBefore, caKeyBefore := readFile(t, filepath.Join(dir, "ca-cert.pem")), readFile(t, filepath.Join(dir, "ca-key.pem"))
	keyBefore := readFile(t, filepath.Join(dir, "server-key.pem"))

	if err := cl.createKeys(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(caBefore, readFile(t, filepath.Join(dir, "ca-cert.pem"))) || !bytes.Equal(caKeyBefore, readFile(t, filepath.Join(dir, "ca-key.pem"))) {
		t.Fatal("the CA was overwritten")
	}
	if !bytes.Equal(keyBefore, readFile(t, filepath.Join(dir, "server-key.pem"))) {
		t.Fatal("the existing server key was overwritten")
	}
	verifyLeaf(t, dir, "server", x509.ExtKeyUsageServerAuth)
	verifyLeaf(t, dir, "client", x509.ExtKeyUsageClientAuth)
}

// Without servers, the CA and the client pair are written, the server certificate waits.
func TestCreateKeysWithoutServers(t *testing.T) {
	cl := certTestCluster(t)
	if err := cl.createKeys(); !errors.Is(err, errCertificatesNoServers) {
		t.Fatalf("err = %v, want errCertificatesNoServers", err)
	}
	m := cl.missingCertificateFiles()
	if len(m) != 2 || m[0] != "server-cert.pem" || m[1] != "server-key.pem" {
		t.Fatalf("missing %v, want the server pair only (made once the servers are known)", m)
	}
	verifyLeaf(t, cl.certificateDir(), "client", x509.ExtKeyUsageClientAuth)
}

// Half a CA cannot be completed: nothing is written, the error says so.
func TestCreateKeysRefusesHalfCA(t *testing.T) {
	cl := certTestCluster(t, "db1")
	dir := cl.certificateDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca-cert.pem"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.createKeys(); err == nil {
		t.Fatal("half a CA was accepted")
	}
	if m := cl.missingCertificateFiles(); len(m) != 5 {
		t.Fatalf("files written beside half a CA: missing %v", m)
	}
	if string(readFile(t, filepath.Join(dir, "ca-cert.pem"))) != "x" {
		t.Fatal("the CA certificate was overwritten")
	}
}

// WARN0318 is open while a file is missing and not once the set is whole; the remediation
// completes it.
func TestCheckCertificatesState(t *testing.T) {
	cl := certTestCluster(t, "db1")
	cl.Conf.DBServersTLSCompleteGeneratedCertificate = false // the remediation below, not the background one
	cl.CheckCertificates()
	if !cl.SecurityStateMachine.IsInState("WARN0318") {
		t.Fatal("WARN0318 not open on an empty set")
	}
	if err := cl.FixSecState("WARN0318"); err != nil {
		t.Fatal(err)
	}
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	cl.CheckCertificates()
	if cl.SecurityStateMachine.IsInState("WARN0318") {
		t.Fatal("WARN0318 open on a whole set")
	}
}

// Concurrent completions (the start's and a tick's, seen on a dev instance) give one consistent
// set: each certificate verifies against the CA and is for its key.
func TestCompleteCertificatesConcurrent(t *testing.T) {
	cl := certTestCluster(t, "db1")
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- cl.completeCertificates() }()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	verifyLeaf(t, cl.certificateDir(), "server", x509.ExtKeyUsageServerAuth)
	verifyLeaf(t, cl.certificateDir(), "client", x509.ExtKeyUsageClientAuth)
}
