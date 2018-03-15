// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/gob"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"time"
)

//Deprecate tentative to generate cluster keys
func (cluster *Cluster) createKeys() error {

	//	local := r.PKI.Store.(*store.Local)

	//	r := router{PKI: &easypki.EasyPKI{Store: &store.Local{}}}
	//	var signer *certificate.Bundle
	if false {
		reader := rand.Reader
		bitSize := 2048

		key, err := rsa.GenerateKey(reader, bitSize)
		if err != nil {
			return err
		}
		priv := key
		// start generate key
		var notBefore time.Time
		notBefore = time.Now()
		notAfter := notBefore.Add(365 * 24 * time.Hour * 2)
		serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
		serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
		if err != nil {
			log.Fatalf("failed to generate serial number: %s", err)
		}

		template := x509.Certificate{
			SerialNumber: serialNumber,
			Subject: pkix.Name{
				Organization: []string{"Signa18"},
			},
			NotBefore: notBefore,
			NotAfter:  notAfter,

			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}

		for _, h := range cluster.Servers {
			if ip := net.ParseIP(h.Host); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			}
		}
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, publicKey(priv), priv)
		if err != nil {
			log.Fatalf("Failed to create certificate: %s", err)
		}

		certOut, err := os.Create(cluster.Conf.WorkingDir + "/" + cluster.Name + "/cert.pem")
		if err != nil {
			log.Fatalf("failed to open cert.pem for writing: %s", err)
		}
		pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
		certOut.Close()
		keyOut, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/key.pem", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			log.Print("failed to open key.pem for writing:", err)
			return err
		}
		pem.Encode(keyOut, pemBlockForKey(priv))
		keyOut.Close()

		// End certificate

		publicKey := key.PublicKey

		err = cluster.saveGobKey(cluster.Conf.WorkingDir+"/"+cluster.Name+"/private.key", key)
		if err != nil {
			return err
		}
		err = cluster.savePEMKey(cluster.Conf.WorkingDir+"/"+cluster.Name+"/private.pem", key)
		if err != nil {
			return err
		}
		err = cluster.saveGobKey(cluster.Conf.WorkingDir+"/"+cluster.Name+"/public.key", publicKey)
		if err != nil {
			return err
		}
		err = cluster.savePublicPEMKey(cluster.Conf.WorkingDir+"/"+cluster.Name+"/public.pem", publicKey)
		return err
	}
	return nil
}

func (cluster *Cluster) saveCaCert(fileName string, key interface{}) error {
	outFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer outFile.Close()

	encoder := gob.NewEncoder(outFile)
	err = encoder.Encode(key)
	return err
}

func (cluster *Cluster) saveGobKey(fileName string, key interface{}) error {
	outFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer outFile.Close()

	encoder := gob.NewEncoder(outFile)
	err = encoder.Encode(key)
	return err
}

func (cluster *Cluster) savePEMKey(fileName string, key *rsa.PrivateKey) error {
	outFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var privateKey = &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}

	err = pem.Encode(outFile, privateKey)
	return err
}

func (cluster *Cluster) savePublicPEMKey(fileName string, pubkey rsa.PublicKey) error {
	asn1Bytes, err := asn1.Marshal(pubkey)
	if err != nil {
		return err
	}

	var pemkey = &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: asn1Bytes,
	}

	pemfile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer pemfile.Close()

	err = pem.Encode(pemfile, pemkey)
	return err
}

func publicKey(priv interface{}) interface{} {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

func pemBlockForKey(priv interface{}) *pem.Block {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to marshal ECDSA private key: %v", err)
			os.Exit(2)
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	default:
		return nil
	}
}
