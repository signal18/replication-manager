package licensebuilder

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func newMaster(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// The happy path: derive a seat from the master, sign its licence, verify the
// whole chain with only the master public key, and gate on the entitlement.
func TestChainRoundTrip(t *testing.T) {
	mpub, mpriv := newMaster(t)
	seatPriv, cert, err := GenerateSeat(mpriv, "tnsi", 2, "20261228")
	if err != nil {
		t.Fatal(err)
	}
	licJSON, sig, err := SignLicense(seatPriv, "tnsi", "20261228", 2, "support")
	if err != nil {
		t.Fatal(err)
	}
	lic, err := VerifyLicense(mpub, cert, licJSON, sig)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if lic.Domain != "tnsi" || lic.SubDomain != "20261228" || lic.SubDomainZone != "2" || lic.Plan != "support" {
		t.Fatalf("licence fields: %+v", lic)
	}
	// a plugin blob signed by the seat key verifies under the master-trusted seat key
	blob := []byte("fake plugin binary")
	bsig, _ := SignBlob(seatPriv, blob)
	seatPub, _ := VerifySeatCert(mpub, cert)
	if !ed25519.Verify(seatPub, blob, bsig) {
		t.Fatal("plugin blob signature should verify under the seat key")
	}
	// entitlement: valid before expiry, case-insensitive company
	if err := CheckEntitlement(lic, "TNSI", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("entitlement (valid): %v", err)
	}
	// still valid on the expiry day itself
	if err := CheckEntitlement(lic, "tnsi", time.Date(2026, 12, 28, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("entitlement (expiry day should still be valid): %v", err)
	}
	// expired the day after
	if err := CheckEntitlement(lic, "tnsi", time.Date(2026, 12, 29, 0, 0, 1, 0, time.UTC)); err == nil {
		t.Fatal("expected expired after the expiry day")
	}
	// wrong company
	if err := CheckEntitlement(lic, "other", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected company mismatch")
	}
}

// Seat keys are DERIVED from the master private key: same inputs → same key.
func TestSeatKeyDeterministic(t *testing.T) {
	_, mpriv := newMaster(t)
	_, c1, _ := GenerateSeat(mpriv, "tnsi", 1, "20261228")
	_, c2, _ := GenerateSeat(mpriv, "tnsi", 1, "20261228")
	if c1.SeatPub != c2.SeatPub || c1.MasterSig != c2.MasterSig {
		t.Fatal("seat derivation must be deterministic (idempotent re-issue)")
	}
	// a different seat, company or expiry yields a different key
	_, c3, _ := GenerateSeat(mpriv, "tnsi", 2, "20261228")
	if c3.SeatPub == c1.SeatPub {
		t.Fatal("different seat must yield a different key")
	}
}

// A licence verified under the wrong master key must be rejected.
func TestWrongMasterRejected(t *testing.T) {
	_, mpriv := newMaster(t)
	otherPub, _ := newMaster(t)
	seatPriv, cert, _ := GenerateSeat(mpriv, "tnsi", 1, "20261228")
	licJSON, sig, _ := SignLicense(seatPriv, "tnsi", "20261228", 1, "support")
	if _, err := VerifyLicense(otherPub, cert, licJSON, sig); err == nil {
		t.Fatal("expected rejection under wrong master key")
	}
}

// Tampering with the licence bytes must break the seat signature.
func TestTamperedLicenceRejected(t *testing.T) {
	mpub, mpriv := newMaster(t)
	seatPriv, cert, _ := GenerateSeat(mpriv, "tnsi", 1, "20261228")
	licJSON, sig, _ := SignLicense(seatPriv, "tnsi", "20261228", 1, "support")
	bad := make([]byte, len(licJSON))
	copy(bad, licJSON)
	bad[len(bad)/2] ^= 0xff
	if _, err := VerifyLicense(mpub, cert, bad, sig); err == nil {
		t.Fatal("expected tampered licence rejected")
	}
}

// A licence whose seat# disagrees with its cert must be rejected.
func TestSeatCertMismatchRejected(t *testing.T) {
	mpub, mpriv := newMaster(t)
	seatPriv1, cert1, _ := GenerateSeat(mpriv, "tnsi", 1, "20261228")
	// sign a licence claiming seat 3 with seat-1's key, present it under cert 1
	licJSON, sig, _ := SignLicense(seatPriv1, "tnsi", "20261228", 3, "support")
	if _, err := VerifyLicense(mpub, cert1, licJSON, sig); err == nil {
		t.Fatal("expected seat mismatch rejected")
	}
}

// An attacker's self-made seat key with a bogus master signature must be rejected.
func TestForgedSeatCertRejected(t *testing.T) {
	mpub, _ := newMaster(t)
	attackerPub, attackerPriv, _ := ed25519.GenerateKey(rand.Reader)
	cert := SeatCert{
		Company: "tnsi", Seat: 1, Expiry: "20261228",
		SeatPub:   base64.StdEncoding.EncodeToString(attackerPub),
		MasterSig: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	}
	licJSON, sig, _ := SignLicense(attackerPriv, "tnsi", "20261228", 1, "support")
	if _, err := VerifyLicense(mpub, cert, licJSON, sig); err == nil {
		t.Fatal("expected forged seat cert (no valid master sig) rejected")
	}
}
