// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"time"
)

func (cluster *Cluster) createKeys() error {

	// start generate key
	var notBefore time.Time
	notBefore = time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour * 2)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		cluster.LogPrintf(LvlErr, "failed to generate serial number: %s", err)
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
			CommonName:    "Signal18CA",
			Country:       []string{"FR"},
			Province:      []string{""},
			Locality:      []string{"Paris"},
			StreetAddress: []string{"201 Rue Championnet"},
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
		cluster.LogPrintf(LvlErr, "Failed to create certificate: %s", err)
	}
	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/ca-cert.pem", derBytes)

	leafKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		panic(err)
	}
	cluster.keyToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-key.pem", leafKey)
	serialNumber, err = rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		cluster.LogPrintf(LvlErr, "failed to generate serial number: %s", err)
	}
	leafTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  []string{"Signal18"},
			CommonName:    "Signal18Admin",
			Country:       []string{"FR"},
			Province:      []string{""},
			Locality:      []string{"Paris"},
			StreetAddress: []string{"201 Rue Championnet"},
			PostalCode:    []string{"75018"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		SubjectKeyId:          []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA: false,
	}
	for _, h := range cluster.Servers {
		leafTemplate.DNSNames = append(leafTemplate.DNSNames, h.Host)
	}
	derBytes, err = x509.CreateCertificate(rand.Reader, &leafTemplate, &rootTemplate, &leafKey.PublicKey, rootKey)
	if err != nil {
		cluster.LogPrintf(LvlErr, "failed to generate cert: %s", err)
	}
	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/server-cert.pem", derBytes)

	clientKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		cluster.LogPrintf(LvlErr, "failed to generate client key: %s", err)
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
			StreetAddress: []string{"201 Rue Championnet"},
			PostalCode:    []string{"75018"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA: false,
	}

	derBytes, err = x509.CreateCertificate(rand.Reader, &clientTemplate, &rootTemplate, &clientKey.PublicKey, rootKey)
	if err != nil {
		cluster.LogPrintf(LvlErr, "failed to generate client cert: %s", err)
	}

	cluster.certToFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/client-cert.pem", derBytes)

	return nil
}

func (cluster *Cluster) keyToFile(filename string, key *rsa.PrivateKey) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		file, err := os.Create(filename)
		if err != nil {
			cluster.LogPrintf(LvlInfo, "Failed to generate file: %s", err)
		}
		defer file.Close()
		b := x509.MarshalPKCS1PrivateKey(key)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Unable to marshal ECDSA private key: %v", err)

		}
		if err := pem.Encode(file, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: b}); err != nil {
			cluster.LogPrintf(LvlErr, "Failed pem.Encode  %s", err)
		}
	}
}

func (cluster *Cluster) certToFile(filename string, derBytes []byte) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {

		certOut, err := os.Create(filename)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Failed to open cert.pem for writing: %s", err)
		}
		if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
			cluster.LogPrintf(LvlErr, "Failed to write data to cert.pem: %s", err)
		}
		if err := certOut.Close(); err != nil {
			cluster.LogPrintf(LvlErr, "Error closing cert.pem: %s", err)
		}
		return
	}
}
