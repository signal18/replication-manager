// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// See LICENSE in this directory for the integral text.

package logplugin

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A Cloud18 licence package is a tar.gz built back-office (reusing the Makefile /
// signer flow to compile + master-sign the plugins) and delivered into the
// instance datadir on the existing git-pull / deposit. repman EXTRACTS it here —
// it no longer pulls the plugins from GitLab; the manifest says where each file
// goes. Signature verification + loading stays LoadPluginsFromDir's job, against
// the master public key repman already reads from config (PluginSigningPublicKey),
// injected via inject.toml — so the package need only ship the plugins + licence,
// not the key.
type PackageManifest struct {
	Format  string          `json:"format"`
	Company string          `json:"company"`
	Entries []ManifestEntry `json:"entries"`
}

// ManifestEntry maps one file in the tar.gz to its destination under the base dir.
type ManifestEntry struct {
	Path string `json:"path"` // name inside the tar.gz
	Dest string `json:"dest"` // relative destination under the base dir (no abs, no ..)
	Kind string `json:"kind"` // plugin | plugin-sig | license | license-sig | pubkey
}

// maxPackageFileBytes bounds a single extracted file (F3/F4: never extract to
// infinity). A plugin binary is tens of MB; 256 MB is a generous ceiling.
const maxPackageFileBytes = 256 << 20

// safeJoin resolves a manifest Dest under baseDir, refusing absolute paths and
// traversal so a crafted package can never write outside the datadir.
func safeJoin(baseDir, dest string) (string, error) {
	d := strings.ReplaceAll(dest, `\`, "/")
	if d == "" || d == "." || strings.HasPrefix(d, "/") {
		return "", fmt.Errorf("unsafe package destination %q", dest)
	}
	for _, part := range strings.Split(d, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe package destination %q (no '..')", dest)
		}
	}
	full := filepath.Join(baseDir, filepath.Clean(d))
	rel, err := filepath.Rel(baseDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("package destination escapes base dir: %q", dest)
	}
	return full, nil
}

// ExtractPackage opens the licence package tar.gz and writes each file its manifest
// lists to the destination under baseDir — plugins (made executable) and their
// .sig, license.json / license.sig, and the master public key. Returns the manifest.
// It does NOT verify signatures or load plugins; the caller runs LoadPluginsFromDir
// (which verifies against the shipped master public key) afterwards.
func ExtractPackage(tarGzPath, baseDir string) (*PackageManifest, error) {
	man, err := readManifest(tarGzPath)
	if err != nil {
		return nil, err
	}
	want := make(map[string]ManifestEntry, len(man.Entries))
	for _, e := range man.Entries {
		if e.Dest != "" {
			want[e.Path] = e
		}
	}

	f, err := os.Open(tarGzPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		e, ok := want[h.Name]
		if !ok {
			continue
		}
		full, err := safeJoin(baseDir, e.Dest)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		mode := os.FileMode(0o644)
		if e.Kind == "plugin" {
			mode = 0o755 // plugin binaries must be executable for LoadPluginsFromDir
		}
		out, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxPackageFileBytes+1))
		cerr := out.Close()
		if err != nil {
			return nil, err
		}
		if cerr != nil {
			return nil, cerr
		}
		if n > maxPackageFileBytes {
			_ = os.Remove(full)
			return nil, fmt.Errorf("package file %q exceeds %d bytes", h.Name, int64(maxPackageFileBytes))
		}
	}
	return man, nil
}

func readManifest(tarGzPath string) (*PackageManifest, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("manifest.json not found in package %s", tarGzPath)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "manifest.json" {
			var man PackageManifest
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&man); err != nil {
				return nil, fmt.Errorf("manifest.json: %w", err)
			}
			return &man, nil
		}
	}
}
