package licensebuilder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// Manifest describes a licence package: what it is and where each file goes when
// the instance "Loads" it. Deterministic (sorted) so re-issues are byte-stable
// except for the per-licence nonce/issuedAt.
type Manifest struct {
	Format        string         `json:"format"`        // "cloud18-license-package/v1"
	Company       string         `json:"company"`       // the domain the licences are scoped to
	Expiry        string         `json:"expiry"`        // YYYYMMDD
	Plan          string         `json:"plan"`
	Seats         int            `json:"seats"`
	RepmanVersion string         `json:"repmanVersion"`
	BuiltAt       string         `json:"builtAt"`
	Entries       []ManifestItem `json:"entries"`
}

// ManifestItem maps a file in the package to where it belongs on the instance.
type ManifestItem struct {
	Path string `json:"path"` // path inside the tar.gz
	Dest string `json:"dest"` // logical destination key the loader resolves
	Kind string `json:"kind"` // license | license-sig | seat-cert | pubkey | plugin | plugin-sig
}

// Plugin is a built plugin binary to be signed per seat and shipped in the package.
type Plugin struct {
	Name string
	Data []byte
}

// BuildPackage produces the company licence package tar.gz: for each of the N
// seats it derives the seat key from the master (chain), signs the seat licence,
// and signs every plugin with that seat key; plugin binaries are shipped once and
// each seat carries its own detached signatures. The master public key is included
// so the instance can verify master -> seat -> licence/plugin offline.
func BuildPackage(masterPriv ed25519.PrivateKey, company, expiry, plan string, seats int, repmanVersion string, plugins []Plugin) ([]byte, error) {
	if len(masterPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("master key size %d, want %d", len(masterPriv), ed25519.PrivateKeySize)
	}
	if seats < 1 {
		return nil, fmt.Errorf("seats must be >= 1, got %d", seats)
	}
	if _, err := ParseExpiry(expiry); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	man := Manifest{
		Format:        "cloud18-license-package/v1",
		Company:       company,
		Expiry:        expiry,
		Plan:          plan,
		Seats:         seats,
		RepmanVersion: repmanVersion,
		BuiltAt:       time.Now().UTC().Format(time.RFC3339),
	}

	add := func(path string, data []byte, dest, kind string) error {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		man.Entries = append(man.Entries, ManifestItem{Path: path, Dest: dest, Kind: kind})
		return nil
	}

	// plugin binaries: shipped once, shared by all seats (same version)
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	for _, p := range plugins {
		if err := add("plugins/"+p.Name, p.Data, "plugins/"+p.Name, "plugin"); err != nil {
			return nil, err
		}
	}

	// the master public key, so the instance can verify the chain offline
	masterPub := masterPriv.Public().(ed25519.PublicKey)
	if err := add("master.pub", []byte(base64.StdEncoding.EncodeToString(masterPub)+"\n"), "plugin-signing-public-key", "pubkey"); err != nil {
		return nil, err
	}

	for seat := 1; seat <= seats; seat++ {
		seatPriv, cert, err := GenerateSeat(masterPriv, company, seat, expiry)
		if err != nil {
			return nil, err
		}
		licJSON, sig, err := SignLicense(seatPriv, company, expiry, seat, plan)
		if err != nil {
			return nil, err
		}
		certJSON, err := json.Marshal(cert)
		if err != nil {
			return nil, err
		}
		base := fmt.Sprintf("seats/%d/", seat)
		if err := add(base+"license.json", licJSON, "license.json", "license"); err != nil {
			return nil, err
		}
		if err := add(base+"license.sig", sig, "license.sig", "license-sig"); err != nil {
			return nil, err
		}
		if err := add(base+"seat-cert.json", certJSON, "seat-cert.json", "seat-cert"); err != nil {
			return nil, err
		}
		// per-seat plugin signatures
		for _, p := range plugins {
			psig, err := SignBlob(seatPriv, p.Data)
			if err != nil {
				return nil, err
			}
			if err := add(base+"sigs/"+p.Name+".sig", psig, "plugins/"+p.Name+".sig", "plugin-sig"); err != nil {
				return nil, err
			}
		}
	}

	manJSON, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := add("manifest.json", manJSON, "", "manifest"); err != nil {
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

// ReadPackageFile extracts one file from a package tar.gz (helper for the loader/tests).
func ReadPackageFile(pkg []byte, path string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(pkg))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("file %q not found in package", path)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == path {
			return io.ReadAll(tr)
		}
	}
}
