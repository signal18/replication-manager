package cert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"
)

var (
	Organization string        = "Acme Co"            // Organization for the certificate
	Host         string                               // Comma-separated hostnames and IPs to generate a certificate for
	ValidFrom    string                               // Creation date formatted as Jan 1 15:04:05 2011
	ValidFor     time.Duration = 365 * 24 * time.Hour // Duration that certificate is valid for
	IsCA         bool                                 // whether this cert should be its own Certificate Authority
	RsaBits      int           = 2048                 // Size of RSA key to generate. Ignored if EcdsaCurve is set
	EcdsaCurve   string        = "P256"               // ECDSA curve to use to generate a key. Valid values are P224, P256 (recommended), P384, P521
	Ed25519Key   bool                                 // Generate an Ed25519 key
)

// adapted from https://go.dev/src/crypto/tls/generate_cert.go

func publicKey(priv interface{}) interface{} {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	case ed25519.PrivateKey:
		return k.Public().(ed25519.PublicKey)
	default:
		return nil
	}
}

func GenerateKeyAndCert() (key []byte, cert []byte, err error) {
	var priv interface{}

	switch EcdsaCurve {
	case "":
		if Ed25519Key {
			_, priv, err = ed25519.GenerateKey(rand.Reader)
		} else {
			priv, err = rsa.GenerateKey(rand.Reader, RsaBits)
		}
	case "P224":
		priv, err = ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	case "P256":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "P384":
		priv, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "P521":
		priv, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	default:
		return nil, nil, fmt.Errorf("unrecognized elliptic curve: %q", EcdsaCurve)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	// ECDSA, ED25519 and RSA subject keys should have the DigitalSignature
	// KeyUsage bits set in the x509.Certificate template
	keyUsage := x509.KeyUsageDigitalSignature
	// Only RSA subject keys should have the KeyEncipherment KeyUsage bits set. In
	// the context of TLS this KeyUsage is particular to RSA key exchange and
	// authentication.
	if _, isRSA := priv.(*rsa.PrivateKey); isRSA {
		keyUsage |= x509.KeyUsageKeyEncipherment
	}

	var notBefore time.Time
	if len(ValidFrom) == 0 {
		notBefore = time.Now()
	} else {
		notBefore, err = time.Parse("Jan 2 15:04:05 2006", ValidFrom)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse creation date: %v", err)
		}
	}

	notAfter := notBefore.Add(ValidFor)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{Organization},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	hosts := strings.Split(Host, ",")
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	if IsCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, publicKey(priv), priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %v", err)
	}

	certOut := bytes.NewBuffer([]byte{})
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, nil, fmt.Errorf("failed to write data to cert.pem: %v", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to marshal private key: %v", err)
	}
	keyOut := bytes.NewBuffer([]byte{})
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return nil, nil, fmt.Errorf("failed to write data to key.pem: %v", err)
	}

	return keyOut.Bytes(), certOut.Bytes(), nil
}

func GenerateTempKeyAndCert() (key string, cert string, err error) {
	k, c, err := GenerateKeyAndCert()

	certOut, err := os.CreateTemp("", "cert.pem")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temporary cert.pem for writing: %v", err)
	}

	if _, err := certOut.Write(c); err != nil {
		return "", "", fmt.Errorf("Failed to write data to cert.pem: %v", err)
	}

	if err := certOut.Close(); err != nil {
		return "", "", fmt.Errorf("Error closing cert.pem: %v", err)
	}

	keyOut, err := os.CreateTemp("", "key.pem")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temporary key.pem for writing: %v", err)
	}

	if _, err := keyOut.Write(k); err != nil {
		return "", "", fmt.Errorf("Failed to write data to temporary key.pem: %v", err)
	}

	if err := keyOut.Close(); err != nil {
		return "", "", fmt.Errorf("Error closing temporary key.pem: %v", err)
	}

	return keyOut.Name(), certOut.Name(), nil
}
