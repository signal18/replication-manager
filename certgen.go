//go:build ignore
// +build ignore

// adapted from https://go.dev/src/crypto/tls/generate_cert.go

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/signal18/replication-manager/cert"
)

var (
	host       = flag.String("host", "", "Comma-separated hostnames and IPs to generate a certificate for")
	validFrom  = flag.String("start-date", "", "Creation date formatted as Jan 1 15:04:05 2011")
	validFor   = flag.Duration("duration", 365*24*time.Hour, "Duration that certificate is valid for")
	isCA       = flag.Bool("ca", false, "whether this cert should be its own Certificate Authority")
	rsaBits    = flag.Int("rsa-bits", 2048, "Size of RSA key to generate. Ignored if --ecdsa-curve is set")
	ecdsaCurve = flag.String("ecdsa-curve", "", "ECDSA curve to use to generate a key. Valid values are P224, P256 (recommended), P384, P521")
	ed25519Key = flag.Bool("ed25519", false, "Generate an Ed25519 key")
)

func main() {
	fmt.Printf("COUCOU")
	flag.Parse()

	if len(*host) == 0 {
		log.Fatalf("Missing required --host parameter")
	}

	cert.Host = *host
	cert.ValidFrom = *validFrom
	cert.ValidFor = *validFor
	cert.IsCA = *isCA
	cert.RsaBits = *rsaBits
	cert.EcdsaCurve = *ecdsaCurve
	cert.Ed25519Key = *ed25519Key
	key, c, err := cert.GenerateKeyAndCert()
	if err != nil {
		log.Fatal(err)
	}

	certOut, err := os.Create("cert.pem")
	if err != nil {
		log.Fatalf("Failed to open cert.pem for writing: %v", err)
	}

	if _, err := certOut.Write(c); err != nil {
		log.Fatalf("Failed to write data to cert.pem: %v", err)
	}

	if err := certOut.Close(); err != nil {
		log.Fatalf("Error closing cert.pem: %v", err)
	}
	log.Print("wrote cert.pem\n")

	keyOut, err := os.OpenFile("key.pem", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Failed to open key.pem for writing: %v", err)
		return
	}

	if _, err := keyOut.Write(key); err != nil {
		log.Fatalf("Failed to write data to key.pem: %v", err)
	}

	if err := keyOut.Close(); err != nil {
		log.Fatalf("Error closing key.pem: %v", err)
	}
	log.Print("wrote key.pem\n")
}
