// Package licensebuilder is the Cloud18 licence-builder: a small HTTP queue (the
// `license-builder` subcommand, entrypoint start_builder on the regular dev image)
// that, per request, drives the dev monitor to build + self-test + deliver a licence
// package. See dbaas-portal/doc/LICENSE_PACKAGE_DESIGN.md.
//
// This file writes the package tar.gz that repman's cluster/logplugin.ExtractPackage
// reads. The two must agree on the format string + the Kind values + manifest shape.
package licensebuilder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const packageFormat = "cloud18-license-package/v1"

// Manifest + ManifestEntry mirror cluster/logplugin.PackageManifest (the reader).
type Manifest struct {
	Format  string          `json:"format"`
	Company string          `json:"company"`
	Entries []ManifestEntry `json:"entries"`
}

type ManifestEntry struct {
	Path string `json:"path"` // name inside the tar.gz
	Dest string `json:"dest"` // filename relative to the reader's per-kind base dir
	Kind string `json:"kind"` // plugin | plugin-sig | license | license-sig
}

// Plugin is a built, master-signed plugin (from the Makefile / signer flow).
type Plugin struct {
	Name      string
	Data      []byte // compiled binary
	MasterSig []byte // detached signature made with the master key
}

// offlineLicense mirrors repman's server.OfflineLicense (the signature is over these
// exact JSON bytes; repman verifies with the embedded/injected master public key).
type offlineLicense struct {
	Domain        string `json:"domain"`        // company
	SubDomain     string `json:"subDomain"`     // registered sub-domain (identity)
	SubDomainZone string `json:"subDomainZone"` // registered zone (identity)
	Plan          string `json:"plan"`
	Expires       string `json:"expires,omitempty"` // date limit YYYYMMDD
	Seat          int    `json:"seat,omitempty"`
	IssuedAt      string `json:"issuedAt"`
	Nonce         string `json:"nonce"`
}

// WritePackage builds the company licence package tar.gz: the master-signed plugins
// (binary + .sig) shipped once, plus for each of N seats a licence signed with the
// master key (the plan + the expiry date limit). The licence payload identity is the
// instance's registered domain/subDomain/zone; the seat and expiry are carried too.
// (The exact identity encoding is per the design doc; the master key signs it either way.)
func WritePackage(masterPriv ed25519.PrivateKey, company, subDomain, zone, plan, expiry string, seats int, repmanVersion string, plugins []Plugin) ([]byte, error) {
	if len(masterPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("master key size %d, want %d", len(masterPriv), ed25519.PrivateKeySize)
	}
	if seats < 1 {
		return nil, fmt.Errorf("seats must be >= 1, got %d", seats)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	man := Manifest{Format: packageFormat, Company: company}

	add := func(path, dest, kind string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		man.Entries = append(man.Entries, ManifestEntry{Path: path, Dest: dest, Kind: kind})
		return nil
	}

	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	for _, p := range plugins {
		if err := add("plugins/"+p.Name, p.Name, "plugin", p.Data); err != nil {
			return nil, err
		}
		if len(p.MasterSig) > 0 {
			if err := add("sigs/"+p.Name+".sig", p.Name+".sig", "plugin-sig", p.MasterSig); err != nil {
				return nil, err
			}
		}
	}

	for seat := 1; seat <= seats; seat++ {
		licJSON, sig, err := signLicense(masterPriv, company, subDomain, zone, plan, expiry, seat)
		if err != nil {
			return nil, err
		}
		base := fmt.Sprintf("seats/%d/", seat)
		if err := add(base+"license.json", "license.json", "license", licJSON); err != nil {
			return nil, err
		}
		if err := add(base+"license.sig", "license.sig", "license-sig", sig); err != nil {
			return nil, err
		}
	}

	manJSON, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := add("manifest.json", "", "manifest", manJSON); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// signLicense builds the offline licence for one seat and signs it with the MASTER
// key (the same key that signs the plugins). repman verifies with the master public key.
func signLicense(masterPriv ed25519.PrivateKey, company, subDomain, zone, plan, expiry string, seat int) (licJSON, sig []byte, err error) {
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	lic := offlineLicense{
		Domain:        company,
		SubDomain:     subDomain,
		SubDomainZone: zone,
		Plan:          plan,
		Expires:       expiry,
		Seat:          seat,
		IssuedAt:      time.Now().UTC().Format(time.RFC3339),
		Nonce:         base64.StdEncoding.EncodeToString(nonce),
	}
	if licJSON, err = json.Marshal(lic); err != nil {
		return nil, nil, err
	}
	return licJSON, ed25519.Sign(masterPriv, licJSON), nil
}

// DeriveMasterPub returns the master public key from the signer private key — the
// "public key derived from our private key" the instance verifies with (injected
// via inject.toml). It never leaves the builder as the private key.
func DeriveMasterPub(masterPriv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(masterPriv.Public().(ed25519.PublicKey))
}

