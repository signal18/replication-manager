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
	"io/ioutil"
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
	pem, err := ioutil.ReadFile(cacertfile)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM.")
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
	pem, err := ioutil.ReadFile(cacertfile)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM.")
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

func (cluster *Cluster) createKeys() error {

	// start generate key
	var notBefore time.Time
	notBefore = time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour * 2)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "failed to generate serial number: %s", err)
	}

	//	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		panic(err)
	}

	cluster.keyToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/ca-key.pem", rootKey)

	rootTemplate := x509.Certificate{
		SerialNumber: serialNumber,
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
	}

	rootTemplate.IsCA = true
	rootTemplate.KeyUsage |= x509.KeyUsageCertSign

	derBytes, err := x509.CreateCertificate(rand.Reader, &rootTemplate, &rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Failed to create certificate: %s", err)
	}
	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/ca-cert.pem", derBytes)

	leafKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		panic(err)
	}
	cluster.keyToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-key.pem", leafKey)
	serialNumber, err = rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "failed to generate serial number: %s", err)
	}
	leafTemplate := x509.Certificate{
		SerialNumber: serialNumber,
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
	}
	if len(cluster.Servers) == 0 {
		return errors.New("No servers list")
	}
	for _, h := range cluster.Servers {
		if h == nil {
			return errors.New("No servers list")
		}
		leafTemplate.DNSNames = append(leafTemplate.DNSNames, h.Host)
	}
	derBytes, err = x509.CreateCertificate(rand.Reader, &leafTemplate, &rootTemplate, &leafKey.PublicKey, rootKey)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "failed to generate cert: %s", err)
	}
	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-cert.pem", derBytes)

	clientKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "failed to generate client key: %s", err)
	}
	cluster.keyToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/client-key.pem", clientKey)

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

	derBytes, err = x509.CreateCertificate(rand.Reader, &clientTemplate, &rootTemplate, &clientKey.PublicKey, rootKey)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "failed to generate client cert: %s", err)
	}

	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/client-cert.pem", derBytes)

	return nil
}

func (cluster *Cluster) keyToFile(filename string, key *rsa.PrivateKey) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		file, err := os.Create(filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlInfo, "Failed to generate file: %s", err)
		}
		defer file.Close()
		b := x509.MarshalPKCS1PrivateKey(key)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Unable to marshal ECDSA private key: %v", err)

		}
		if err := pem.Encode(file, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: b}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Failed pem.Encode  %s", err)
		}
	}
}

func (cluster *Cluster) certToFile(filename string, derBytes []byte) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		certOut, err := os.Create(filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Failed to open cert.pem for writing: %s", err)
		}
		if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Failed to write data to cert.pem: %s", err)
		}
		if err := certOut.Close(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlErr, "Error closing cert.pem: %s", err)
		}
		return
	}
}

func (cluster *Cluster) KeyRotation() {
	//os.RemoveAll(cluster.WorkingDir + "/old_certs")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, LvlInfo, "Cluster rotate certificats")
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
	cluster.createKeys()
	cluster.tlsoldconf = cluster.tlsconf
	cluster.HaveDBTLSOldCert = true
	for _, srv := range cluster.Servers {
		srv.SetDSN()
	}
}

func (cluster *Cluster) GeneratePassword() (string, error) {
	const (
		digits = "0123456789"
		lowers = "abcdefghijklmnopqrstuvwxyz"
		uppers = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		//symbols = "!\"#$%&'()*+,-./0123456789:;<=>?@[\\]^_`{|}~"
		symbols = "!#$%&()*+-;<=>?[]^_{|}~"
	)
	var length = 8
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
