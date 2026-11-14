// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) loadDBCertificates(path string) error {
	rootCertPool := x509.NewCertPool()
	var cacertfile, clicertfile, clikeyfile string

	if cluster.Conf.HostsTLSCA == "" || cluster.Conf.HostsTlsCliCert == "" || cluster.Conf.HostsTlsCliKey == "" {
		if cluster.Conf.DBServersTLSUseGeneratedCertificate || cluster.Configurator.HaveDBTag("ssl") {
			cacertfile = path + "/ca-cert.pem"
			clicertfile = path + "/client-cert.pem"
			clikeyfile = path + "/client-key.pem"
		} else {
			return errors.New("No given Key certificate")
		}

	} else {
		cacertfile = cluster.Conf.HostsTLSCA
		clicertfile = cluster.Conf.HostsTlsCliCert
		clikeyfile = cluster.Conf.HostsTlsCliKey
	}
	pem, err := os.ReadFile(cacertfile)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM")
	}
	clientCert := make([]tls.Certificate, 0, 1)
	certs, err := tls.LoadX509KeyPair(clicertfile, clikeyfile)
	if err != nil {
		return errors.New("Can not load database TLS X509 key pair")
	}

	clientCert = append(clientCert, certs)
	cluster.tlsconf = &tls.Config{
		RootCAs:            rootCertPool,
		Certificates:       clientCert,
		InsecureSkipVerify: true,
	}
	return nil
}

func (cluster *Cluster) loadDBOldCertificates(path string) error {
	rootCertPool := x509.NewCertPool()
	var cacertfile, clicertfile, clikeyfile string

	if cluster.Conf.HostsTLSCA == "" || cluster.Conf.HostsTlsCliCert == "" || cluster.Conf.HostsTlsCliKey == "" {
		if cluster.Conf.DBServersTLSUseGeneratedCertificate || cluster.Configurator.HaveDBTag("ssl") {
			cacertfile = path + "/ca-cert.pem"
			clicertfile = path + "/client-cert.pem"
			clikeyfile = path + "/client-key.pem"
		} else {
			return errors.New("No given Key certificate")
		}

	} else {
		misc.CopyFile(cluster.Conf.HostsTLSCA, path+"/ca-cert.pem")
		misc.CopyFile(cluster.Conf.HostsTlsCliCert, path+"/client-cert.pem")
		misc.CopyFile(cluster.Conf.HostsTlsCliKey, path+"/client-key.pem")
		misc.CopyFile(cluster.Conf.HostsTlsSrvCert, path+"/server-cert.pem")
		misc.CopyFile(cluster.Conf.HostsTlsSrvKey, path+"/server-key.pem")

		cacertfile = cluster.Conf.HostsTLSCA
		clicertfile = cluster.Conf.HostsTlsCliCert
		clikeyfile = cluster.Conf.HostsTlsCliKey
	}
	pem, err := os.ReadFile(cacertfile)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM")
	}
	clientCert := make([]tls.Certificate, 0, 1)
	certs, err := tls.LoadX509KeyPair(clicertfile, clikeyfile)
	if err != nil {
		return errors.New("Can not load database TLS X509 key pair")
	}

	clientCert = append(clientCert, certs)
	cluster.tlsoldconf = &tls.Config{
		RootCAs:            rootCertPool,
		Certificates:       clientCert,
		InsecureSkipVerify: true,
	}
	return nil
}

// clusterCertificateFiles is the cluster's TLS set, in its working directory: the CA, the server
// certificate the databases and proxies present, the client certificate repman and the
// replicas use. The configurator copies them to every database and proxy it provisions.
var clusterCertificateFiles = []string{"ca-cert.pem", "ca-key.pem", "server-cert.pem", "server-key.pem", "client-cert.pem", "client-key.pem"}

// errCertificatesNoServers: the server certificate names the servers; a cluster added before its
// servers (the add API) gets it once they are known.
var errCertificatesNoServers = errors.New("the server certificate waits for the cluster's servers")

func (cluster *Cluster) certificateDir() string {
	return cluster.Conf.WorkingDir + "/" + cluster.Name
}

// missingCertificateFiles lists the files of the cluster's TLS set that do not exist.
func (cluster *Cluster) missingCertificateFiles() []string {
	var missing []string
	for _, f := range clusterCertificateFiles {
		if _, err := os.Stat(cluster.certificateDir() + "/" + f); os.IsNotExist(err) {
			missing = append(missing, f)
		}
	}
	return missing
}

// createKeys completes the cluster's TLS set: it writes the files that are missing and never
// overwrites one that exists (WARN0318). The CA is loaded when both its files exist and created
// when neither does; a leaf key that exists signs its missing certificate. A cluster added before
// its servers used to stop after the CA and the server key, and the set stayed incomplete for
// good: the start only ran this when ca-key.pem was missing (6 of 15 clusters on a preprod
// instance, 2026-11-09; ProxySQL 2+ refuses to start on a partial set).
//
// What cannot be completed without overwriting (a certificate whose key is gone, half a CA) is
// returned as an error: KeyRotation regenerates the whole set.
func (cluster *Cluster) createKeys() error {
	dir := cluster.certificateDir()
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}
	if len(cluster.missingCertificateFiles()) == 0 {
		return nil
	}
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour * 100)
	rootCert, rootKey, err := cluster.loadOrCreateCA(dir, notBefore, notAfter)
	if err != nil {
		return err
	}
	// the client pair first: it names no server, so a cluster without servers yet gets it
	if cluster.certificateMissing(dir, "client") {
		clientTemplate := x509.Certificate{
			SerialNumber: new(big.Int).SetInt64(4),
			Subject: pkix.Name{
				Organization:  []string{"Signal18"},
				CommonName:    "Signal18Client",
				Country:       []string{"FR"},
				Province:      []string{""},
				Locality:      []string{"Paris"},
				StreetAddress: []string{"16 Villa Saint Michel"},
				PostalCode:    []string{"75018"},
			},
			NotBefore:             notBefore,
			NotAfter:              notAfter,
			KeyUsage:              x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
			IsCA:                  false,
		}
		if err := cluster.signLeaf(dir, "client", &clientTemplate, rootCert, rootKey); err != nil {
			return err
		}
	}
	if cluster.certificateMissing(dir, "server") {
		if len(cluster.Servers) == 0 {
			return errCertificatesNoServers
		}
		var dnsNames []string
		for _, h := range cluster.Servers {
			if h == nil {
				return errCertificatesNoServers
			}
			dnsNames = append(dnsNames, h.Host)
		}
		serial, err := certificateSerial()
		if err != nil {
			return err
		}
		leafTemplate := x509.Certificate{
			SerialNumber: serial,
			Subject: pkix.Name{
				Organization:  []string{"Signal18"},
				CommonName:    "Signal18Admin",
				Country:       []string{"FR"},
				Province:      []string{""},
				Locality:      []string{"Paris"},
				StreetAddress: []string{"16 Villa Saint Michel"},
				PostalCode:    []string{"75018"},
			},
			NotBefore:             notBefore,
			NotAfter:              notAfter,
			SubjectKeyId:          []byte{1, 2, 3, 4, 6},
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:              x509.KeyUsageDigitalSignature,
			BasicConstraintsValid: true,
			IsCA:                  false,
			DNSNames:              dnsNames,
		}
		if err := cluster.signLeaf(dir, "server", &leafTemplate, rootCert, rootKey); err != nil {
			return err
		}
	}
	return nil
}

func certificateSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

// certificateMissing tells whether <name>-cert.pem or <name>-key.pem is missing.
func (cluster *Cluster) certificateMissing(dir, name string) bool {
	_, certErr := os.Stat(dir + "/" + name + "-cert.pem")
	_, keyErr := os.Stat(dir + "/" + name + "-key.pem")
	return os.IsNotExist(certErr) || os.IsNotExist(keyErr)
}

// loadOrCreateCA loads the CA when both its files exist, creates it when neither does, and
// refuses half a CA: a new key would not match the certificate the servers already trust.
func (cluster *Cluster) loadOrCreateCA(dir string, notBefore, notAfter time.Time) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPath, keyPath := dir+"/ca-cert.pem", dir+"/ca-key.pem"
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		cert, err := readCertificate(certPath)
		if err != nil {
			return nil, nil, fmt.Errorf("CA certificate: %w", err)
		}
		key, err := readRSAKey(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("CA key: %w", err)
		}
		return cert, key, nil
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
	default:
		return nil, nil, errors.New("half of the CA is missing: only a key rotation can rebuild it")
	}
	rootKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	rootTemplate := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization:  []string{"Signal18"},
			CommonName:    cluster.Name + "." + cluster.GetCloudSubDomain() + ".cloud18.io",
			Country:       []string{"FR"},
			Province:      []string{""},
			Locality:      []string{"Paris"},
			StreetAddress: []string{"16 Villa Saint Michel"},
			PostalCode:    []string{"75018"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &rootTemplate, &rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, fmt.Errorf("CA certificate: %w", err)
	}
	cluster.keyToFile(keyPath, rootKey)
	cluster.certToFile(certPath, der)
	cert, err := x509.ParseCertificate(der)
	return cert, rootKey, err
}

// signLeaf writes <name>-cert.pem signed by the CA, with the key in <name>-key.pem, created when
// missing. A certificate whose key is gone cannot be completed without overwriting it.
func (cluster *Cluster) signLeaf(dir, name string, template *x509.Certificate, ca *x509.Certificate, caKey *rsa.PrivateKey) error {
	certPath, keyPath := dir+"/"+name+"-cert.pem", dir+"/"+name+"-key.pem"
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	var key *rsa.PrivateKey
	var err error
	switch {
	case certErr == nil && os.IsNotExist(keyErr):
		return fmt.Errorf("%s certificate without its key: only a key rotation can rebuild it", name)
	case keyErr == nil:
		if key, err = readRSAKey(keyPath); err != nil {
			return fmt.Errorf("%s key: %w", name, err)
		}
	default:
		if key, err = rsa.GenerateKey(rand.Reader, 4096); err != nil {
			return err
		}
		cluster.keyToFile(keyPath, key)
	}
	if certErr == nil {
		return nil
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("%s certificate: %w", name, err)
	}
	cluster.certToFile(certPath, der)
	return nil
}

func readCertificate(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func readRSAKey(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

func (cluster *Cluster) keyToFile(filename string, key *rsa.PrivateKey) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		file, err := os.Create(filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Failed to generate file %s: %s", filename, err)
		}
		defer file.Close()
		b := x509.MarshalPKCS1PrivateKey(key)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Unable to marshal ECDSA private key: %v", err)

		}
		if err := pem.Encode(file, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: b}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Failed pem.Encode  %s", err)
		}
	}
}

func (cluster *Cluster) certToFile(filename string, derBytes []byte) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		certOut, err := os.Create(filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Failed to open cert.pem for writing: %s", err)
		}
		if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Failed to write data to cert.pem: %s", err)
		}
		if err := certOut.Close(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error closing cert.pem: %s", err)
		}
		return
	}
}

func (cluster *Cluster) KeyRotation() {
	//os.RemoveAll(cluster.WorkingDir + "/old_certs")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Cluster rotate certificats")
	if _, err := os.Stat(cluster.WorkingDir + "/old_certs"); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs", os.ModePerm)
	}
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/ca-cert.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/ca-cert.pem")
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/ca-key.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/ca-key.pem")
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-cert.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/server-cert.pem")
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-key.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/server-key.pem")
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/client-cert.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/client-cert.pem")
	misc.CopyFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/client-key.pem", cluster.Conf.WorkingDir+"/"+cluster.Name+"/old_certs/client-key.pem")
	os.Remove(cluster.WorkingDir + "/ca-cert.pem")
	os.Remove(cluster.WorkingDir + "/ca-key.pem")
	os.Remove(cluster.WorkingDir + "/server-cert.pem")
	os.Remove(cluster.WorkingDir + "/server-key.pem")
	os.Remove(cluster.WorkingDir + "/client-cert.pem")
	os.Remove(cluster.WorkingDir + "/client-key.pem")
	// serialised with the background completion (#2360 review): a WARN0318 run while the files
	// are removed could otherwise write part of a set beside this one
	if err := cluster.completeCertificates(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Key rotation: the new TLS set is not complete: %s", err)
	}
	cluster.tlsoldconf = cluster.tlsconf
	cluster.HaveDBTLSOldCert = true
	for _, srv := range cluster.Servers {
		srv.SetDSN()
	}
}

// GeneratePassword is a random password of 8 characters.
func (cluster *Cluster) GeneratePassword() (string, error) {
	return cluster.GeneratePasswordLength(8)
}

// GeneratePasswordLength is a random password of length characters, from crypto/rand.
// The set has no ',' nor ':', which separate the entries of a user:password list.
func (cluster *Cluster) GeneratePasswordLength(length int) (string, error) {
	const (
		digits = "0123456789"
		lowers = "abcdefghijklmnopqrstuvwxyz"
		uppers = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		//symbols = "!\"#$%&'()*+,-./0123456789:;<=>?@[\\]^_`{|}~"
		symbols = "!#$%&()*+-;<=>?[]^_{|}~"
	)
	var charset = [](byte)(lowers)
	charset = append(charset, []byte(digits)...)
	charset = append(charset, []byte(lowers)...)
	charset = append(charset, []byte(uppers)...)
	charset = append(charset, []byte(symbols)...)
	max := big.NewInt(int64(len(charset)))
	password := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		password[i] = charset[n.Int64()]
	}
	return string(password), nil
}

func (cluster *Cluster) CBCEncrypter() {
	// Load your secret key from a safe place and reuse it across multiple
	// NewCipher calls. (Obviously don't use this example key for anything
	// real.) If you want to convert a passphrase to a key, use a suitable
	// package like bcrypt or scrypt.
	key, _ := hex.DecodeString("6368616e676520746869732070617373")
	plaintext := []byte("exampleplaintext")

	// CBC mode works on blocks so plaintexts may need to be padded to the
	// next whole block. For an example of such padding, see
	// https://tools.ietf.org/html/rfc5246#section-6.2.3.2. Here we'll
	// assume that the plaintext is already of the correct length.
	if len(plaintext)%aes.BlockSize != 0 {
		panic("plaintext is not a multiple of the block size")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	// The IV needs to be unique, but not secure. Therefore it's common to
	// include it at the beginning of the ciphertext.
	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		panic(err)
	}

	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext[aes.BlockSize:], plaintext)

	// It's important to remember that ciphertexts must be authenticated
	// (i.e. by using crypto/hmac) as well as being encrypted in order to
	// be secure.

	fmt.Printf("%x\n", ciphertext)
}
