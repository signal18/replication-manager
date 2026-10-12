package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// A dropped tag still deployed until the restart is not drift: its variables stay out of
// the delta, an operator's own variable is still preserved (#1976).
func TestDeltaSkipsDroppedTagVariables(t *testing.T) {
	dir := t.TempDir()
	c := &Cluster{Name: "c1", Conf: &config.Config{}}
	b, err := os.ReadFile("../share/opensvc/moduleset_mariadb.svc.mrm.db.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &c.Configurator.DBModule); err != nil {
		t.Fatal(err)
	}
	s := &ServerMonitor{URL: "db3:3306", Datadir: dir, ClusterGroup: c, VariablesMap: config.NewVariablesMap(), State: stateSlave}

	// generated config without the ssl tag; the deployed one still has it, plus an
	// operator variable repman never renders
	os.WriteFile(filepath.Join(dir, "dummy.cnf"), []byte("[mysqld]\nmax_connections=200\n"), 0644)
	os.WriteFile(filepath.Join(dir, "current.cnf"), []byte("[mysqld]\nmax_connections=200\nssl\nssl_ca=/etc/mysql/ssl/ca-cert.pem\nssl_cert=/etc/mysql/ssl/server-cert.pem\nssl_key=/etc/mysql/ssl/server-key.pem\nmy_custom_operator_var=1\n"), 0644)
	if err := s.VariablesMap.LoadFromConfigFile(filepath.Join(dir, "dummy.cnf"), "config"); err != nil {
		t.Fatal(err)
	}
	if err := s.VariablesMap.LoadFromConfigFile(filepath.Join(dir, "current.cnf"), "deployed"); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteDeltaVariables(); err != nil {
		t.Fatal(err)
	}
	delta, _ := os.ReadFile(filepath.Join(dir, "02_delta.cnf"))
	if strings.Contains(string(delta), "ssl") {
		t.Fatalf("a dropped tag's variables must not be preserved:\n%s", delta)
	}
	if !strings.Contains(string(delta), "my_custom_operator_var") {
		t.Fatalf("an operator variable is still preserved:\n%s", delta)
	}
}

// writeTestCertificate writes a self-signed certificate and its key.
func writeTestCertificate(t *testing.T, certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600)
}

// When the database TLS certificates appear or disappear, every server DSN follows: a
// stale "&tls=tlsconfig" kept repman away from healthy servers after the ssl tag was
// dropped (#1974).
func TestReloadDBTLSCertificatesRebuildsDSN(t *testing.T) {
	dir := t.TempDir()
	writeTestCertificate(t, filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem"))
	c := &Cluster{Name: "c1", WorkingDir: dir, Conf: &config.Config{Timeout: 2, ReadTimeout: 2}}
	s := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", User: "u", Pass: "p", ClusterGroup: c, TLSConfigUsed: ConstTLSCurrentConfig}
	c.Servers = []*ServerMonitor{s}
	s.SetDSN()
	if strings.Contains(s.DSN, "tls=") {
		t.Fatalf("no certificate, no TLS: %s", s.DSN)
	}

	c.Conf.HostsTLSCA, c.Conf.HostsTlsCliCert, c.Conf.HostsTlsCliKey = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	c.reloadDBTLSCertificates()
	if !c.HaveDBTLSCert || !strings.Contains(s.DSN, ConstTLSCurrentConfig) {
		t.Fatalf("certificates loaded: the DSN uses them: %v %s", c.HaveDBTLSCert, s.DSN)
	}

	c.Conf.HostsTLSCA, c.Conf.HostsTlsCliCert, c.Conf.HostsTlsCliKey = "", "", ""
	c.reloadDBTLSCertificates()
	if c.HaveDBTLSCert || strings.Contains(s.DSN, "tls=") {
		t.Fatalf("certificates gone: the DSN must drop TLS: %v %s", c.HaveDBTLSCert, s.DSN)
	}
}
