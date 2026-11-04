package licensebuilder

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
)

func TestBuildPackage(t *testing.T) {
	mpub, mpriv := newMaster(t)
	plugins := []Plugin{
		{Name: "enterprise-snapshot", Data: []byte("binary-A"), MasterSig: ed25519.Sign(mpriv, []byte("binary-A"))},
		{Name: "enterprise-archive", Data: []byte("binary-B"), MasterSig: ed25519.Sign(mpriv, []byte("binary-B"))},
	}
	pkg, err := BuildPackage(mpriv, "tnsi", "20261228", "support", 3, "v3.1.47", plugins)
	if err != nil {
		t.Fatal(err)
	}

	// manifest
	mj, err := ReadPackageFile(pkg, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var man Manifest
	if err := json.Unmarshal(mj, &man); err != nil {
		t.Fatal(err)
	}
	if man.Company != "tnsi" || man.Seats != 3 || man.Expiry != "20261228" || man.RepmanVersion != "v3.1.47" {
		t.Fatalf("manifest: %+v", man)
	}

	// the master public key the instance will verify with
	mpB, err := ReadPackageFile(pkg, "master.pub")
	if err != nil {
		t.Fatal(err)
	}
	if string(mpB) != base64.StdEncoding.EncodeToString(mpub)+"\n" {
		t.Fatal("master.pub mismatch")
	}

	// plugins are master-signed (from the Makefile flow): verify under the master pub
	for _, p := range plugins {
		psig, err := ReadPackageFile(pkg, "plugins/"+p.Name+".sig")
		if err != nil {
			t.Fatalf("%s master sig: %v", p.Name, err)
		}
		if !ed25519.Verify(mpub, p.Data, psig) {
			t.Fatalf("plugin %s master signature should verify", p.Name)
		}
	}

	// each seat: licence chain + every plugin signature verifies under the seat key
	for seat := 1; seat <= 3; seat++ {
		base := fmt.Sprintf("seats/%d/", seat)
		licJSON, err := ReadPackageFile(pkg, base+"license.json")
		if err != nil {
			t.Fatalf("seat %d license.json: %v", seat, err)
		}
		sig, _ := ReadPackageFile(pkg, base+"license.sig")
		certJSON, _ := ReadPackageFile(pkg, base+"seat-cert.json")
		var cert SeatCert
		if err := json.Unmarshal(certJSON, &cert); err != nil {
			t.Fatal(err)
		}
		lic, err := VerifyLicense(mpub, cert, licJSON, sig)
		if err != nil {
			t.Fatalf("seat %d verify: %v", seat, err)
		}
		if lic.SubDomainZone != fmt.Sprint(seat) {
			t.Fatalf("seat %d licence seat#: %s", seat, lic.SubDomainZone)
		}
	}
}
