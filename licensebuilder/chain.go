// Package licensebuilder builds Cloud18 per-seat offline licence packages.
//
// Trust chain (doc: dbaas-portal/doc/LICENSE_PACKAGE_DESIGN.md):
//
//	MASTER key (the embedded plugin-signing Ed25519 key) ──signs──▶ per-SEAT public key
//	per-SEAT key ──signs──▶ that seat's licence + plugins
//
// The seat key is DERIVED from the master private key + the seat identity, so the
// seat public key is "generated from our private key" and the same seat always
// yields the same key (idempotent re-issue, no per-seat key storage). The master
// signs the seat public key into a SeatCert, so repman — which holds only the
// embedded master PUBLIC key — verifies master → seat → licence/plugin OFFLINE.
//
// Identity carried in the licence: Domain = company, SubDomain = expiry date
// (YYYYMMDD), SubDomainZone = seat number. Valid while now < expiry and the
// company matches the instance.
package licensebuilder

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const expiryLayout = "20060102" // YYYYMMDD

// License is the signed offline licence. It is kept byte-identical in shape to
// repman's server.OfflineLicense (the signature is over these exact JSON bytes).
type License struct {
	Domain        string `json:"domain"`        // company (the registered Signal18 client name)
	SubDomain     string `json:"subDomain"`     // expiry date, YYYYMMDD (the ERP contract end date)
	SubDomainZone string `json:"subDomainZone"` // seat number
	Plan          string `json:"plan"`
	IssuedAt      string `json:"issuedAt"`
	Nonce         string `json:"nonce"`
}

// SeatCert binds a per-seat public key to its scope (company, seat, expiry),
// signed by the master key. Delivered next to the licence so repman can verify
// the chain offline with only the embedded master public key.
type SeatCert struct {
	Company   string `json:"company"`
	Seat      int    `json:"seat"`
	Expiry    string `json:"expiry"`    // YYYYMMDD
	SeatPub   string `json:"seatPub"`   // base64 Ed25519 public key (32 bytes)
	MasterSig string `json:"masterSig"` // base64 Ed25519 signature over the canonical cert bytes
}

// seatIdentity is the canonical identity string for a seat, used both to derive
// the seat key and inside the cert-signing bytes. One definition, no drift.
func seatIdentity(company string, seat int, expiry string) string {
	return "cloud18-seat\x00" + company + "\x00" + strconv.Itoa(seat) + "\x00" + expiry
}

// seatCertBytes is the exact bytes the master signs: the seat identity + the seat
// public key, in a fixed order. Builder and verifier compute it identically.
func seatCertBytes(company string, seat int, expiry, seatPubB64 string) []byte {
	return []byte("cloud18-seat-cert\x00" + seatIdentity(company, seat, expiry) + "\x00" + seatPubB64)
}

// deriveSeatKey derives the seat Ed25519 keypair deterministically from the master
// private key + the seat identity (HMAC-SHA256 seed). So the seat public key is
// generated from our private key, and the same seat always yields the same key.
func deriveSeatKey(masterPriv ed25519.PrivateKey, company string, seat int, expiry string) ed25519.PrivateKey {
	mac := hmac.New(sha256.New, masterPriv)
	mac.Write([]byte(seatIdentity(company, seat, expiry)))
	return ed25519.NewKeyFromSeed(mac.Sum(nil)) // 32-byte seed → Ed25519 private key
}

// GenerateSeat derives the seat's key and the master-signed SeatCert for it.
// Returns the seat PRIVATE key (to sign that seat's licence + plugins) and the cert.
func GenerateSeat(masterPriv ed25519.PrivateKey, company string, seat int, expiry string) (ed25519.PrivateKey, SeatCert, error) {
	if len(masterPriv) != ed25519.PrivateKeySize {
		return nil, SeatCert{}, fmt.Errorf("master key size %d, want %d", len(masterPriv), ed25519.PrivateKeySize)
	}
	if company == "" {
		return nil, SeatCert{}, errors.New("company required")
	}
	if seat < 1 {
		return nil, SeatCert{}, fmt.Errorf("seat must be >= 1, got %d", seat)
	}
	if _, err := ParseExpiry(expiry); err != nil {
		return nil, SeatCert{}, err
	}
	seatPriv := deriveSeatKey(masterPriv, company, seat, expiry)
	seatPubB64 := base64.StdEncoding.EncodeToString(seatPriv.Public().(ed25519.PublicKey))
	cert := SeatCert{
		Company:   company,
		Seat:      seat,
		Expiry:    expiry,
		SeatPub:   seatPubB64,
		MasterSig: base64.StdEncoding.EncodeToString(ed25519.Sign(masterPriv, seatCertBytes(company, seat, expiry, seatPubB64))),
	}
	return seatPriv, cert, nil
}

// VerifySeatCert checks the master's signature on the cert and returns the trusted
// seat public key. The verifier needs only the master PUBLIC key.
func VerifySeatCert(masterPub ed25519.PublicKey, cert SeatCert) (ed25519.PublicKey, error) {
	if len(masterPub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("master pub size %d, want %d", len(masterPub), ed25519.PublicKeySize)
	}
	seatPub, err := base64.StdEncoding.DecodeString(cert.SeatPub)
	if err != nil || len(seatPub) != ed25519.PublicKeySize {
		return nil, errors.New("seat cert: bad seat public key")
	}
	sig, err := base64.StdEncoding.DecodeString(cert.MasterSig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("seat cert: bad master signature")
	}
	if !ed25519.Verify(masterPub, seatCertBytes(cert.Company, cert.Seat, cert.Expiry, cert.SeatPub), sig) {
		return nil, errors.New("seat cert: master signature mismatch (not the master key, or tampered)")
	}
	return ed25519.PublicKey(seatPub), nil
}

// SignLicense builds the licence for one seat and signs it with the seat key.
// Returns the exact license.json bytes and the detached signature (license.sig),
// the signature being over the license.json bytes verbatim (what repman reads).
func SignLicense(seatPriv ed25519.PrivateKey, company, expiry string, seat int, plan string) (licenseJSON, sig []byte, err error) {
	if len(seatPriv) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("seat key size %d, want %d", len(seatPriv), ed25519.PrivateKeySize)
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	lic := License{
		Domain:        company,
		SubDomain:     expiry,
		SubDomainZone: strconv.Itoa(seat),
		Plan:          plan,
		IssuedAt:      time.Now().UTC().Format(time.RFC3339),
		Nonce:         base64.StdEncoding.EncodeToString(nonce),
	}
	if licenseJSON, err = json.Marshal(lic); err != nil {
		return nil, nil, err
	}
	return licenseJSON, ed25519.Sign(seatPriv, licenseJSON), nil
}

// SignBlob signs an arbitrary blob (e.g. a plugin binary) with the seat key.
// repman verifies it with the seat public key from the (master-verified) cert.
func SignBlob(seatPriv ed25519.PrivateKey, data []byte) ([]byte, error) {
	if len(seatPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("seat key size %d, want %d", len(seatPriv), ed25519.PrivateKeySize)
	}
	return ed25519.Sign(seatPriv, data), nil
}

// VerifyLicense verifies the licence under the (master-trusted) seat key and that
// its identity agrees with the cert. Returns the parsed licence.
func VerifyLicense(masterPub ed25519.PublicKey, cert SeatCert, licenseJSON, sig []byte) (License, error) {
	seatPub, err := VerifySeatCert(masterPub, cert)
	if err != nil {
		return License{}, err
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(seatPub, licenseJSON, sig) {
		return License{}, errors.New("licence signature mismatch (not the seat key, or tampered)")
	}
	var lic License
	if err := json.Unmarshal(licenseJSON, &lic); err != nil {
		return License{}, fmt.Errorf("licence json: %w", err)
	}
	if lic.Domain != cert.Company || lic.SubDomain != cert.Expiry || lic.SubDomainZone != strconv.Itoa(cert.Seat) {
		return License{}, errors.New("licence does not match its seat cert (company/expiry/seat)")
	}
	return lic, nil
}

// CheckEntitlement is the instance-side gate: the licence company must match this
// instance's company (case-insensitive) and it must not have expired. now is injected.
func CheckEntitlement(lic License, company string, now time.Time) error {
	if !strings.EqualFold(lic.Domain, company) {
		return fmt.Errorf("licence is for %q, this instance is %q", lic.Domain, company)
	}
	exp, err := ParseExpiry(lic.SubDomain)
	if err != nil {
		return err
	}
	if !now.Before(exp) {
		return fmt.Errorf("licence expired %s", lic.SubDomain)
	}
	return nil
}

// ParseExpiry reads a YYYYMMDD expiry as valid through the end of that day (UTC).
func ParseExpiry(s string) (time.Time, error) {
	t, err := time.Parse(expiryLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expiry %q must be YYYYMMDD: %w", s, err)
	}
	return t.AddDate(0, 0, 1), nil // valid through the end of that day
}
