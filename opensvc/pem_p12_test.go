package opensvc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

func testP12(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	p12, err := pkcs12.Modern.Encode(key, cert, nil, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return p12
}

// The om3 client's files are written once, outside the cluster's directory, and a new
// p12 gets new files.
func TestGeneratePemFromP12WritesOnce(t *testing.T) {
	clusterDir := t.TempDir()
	c := &Collector{PemDir: filepath.Join(t.TempDir(), ".opensvc-pem")}
	p12 := testP12(t, "a")

	key, cert, _, err := c.GeneratePemFromP12(p12, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatalf("unusable pair: %v", err)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0600 {
		t.Fatalf("key mode %v", st.Mode().Perm())
	}
	st1, _ := os.Stat(key)
	time.Sleep(20 * time.Millisecond)
	key2, cert2, _, err := c.GeneratePemFromP12(p12, "secret")
	if err != nil || key2 != key || cert2 != cert {
		t.Fatalf("second call %q %q %v", key2, cert2, err)
	}
	if st2, _ := os.Stat(key); !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("the key was rewritten")
	}
	if left, _ := os.ReadDir(clusterDir); len(left) != 0 {
		t.Fatalf("files in the cluster's directory: %v", left)
	}

	key3, _, _, err := c.GeneratePemFromP12(testP12(t, "b"), "secret")
	if err != nil || key3 == key {
		t.Fatalf("a new p12 must get new files: %q %v", key3, err)
	}
}

// Concurrent om3 calls of several clusters: every caller gets a complete pair.
func TestGeneratePemFromP12Concurrent(t *testing.T) {
	c := &Collector{PemDir: filepath.Join(t.TempDir(), ".opensvc-pem")}
	p12 := testP12(t, "a")
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, cert, _, err := c.GeneratePemFromP12(p12, "secret")
			if err == nil {
				_, err = tls.LoadX509KeyPair(cert, key)
			}
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if tmp, _ := filepath.Glob(filepath.Join(c.PemDir, ".tmp-*")); len(tmp) != 0 {
		t.Fatalf("temporary files left: %v", tmp)
	}
}

func TestRemoveLegacyP12Pem(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{".s18.p12-key.pem", ".s18.p12-cert.pem", ".s18.p12-chain.pem", "ca-cert.pem", "blab.toml"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0600)
	}
	RemoveLegacyP12Pem(dir)
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "blab.toml" || names[1] != "ca-cert.pem" {
		t.Fatalf("left %v, want blab.toml and ca-cert.pem (the cluster's own TLS files stay)", names)
	}
}
