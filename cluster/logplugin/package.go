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

// A licence package is a tar.gz of plugins signed by the Signal18 key, with settings
// files (cloud18.toml, a cluster's inject.toml), loaded by license-pull (#2309) on a
// replication-manager without internet access: what a git pull of its -pull repo would
// bring. The manifest says where each file goes. Signature verification + loading stay
// the pull's job (syncPluginsFromPull), against plugin-signing-public-key.
type PackageManifest struct {
	Format  string `json:"format"`
	Company string `json:"company"`
	// Wire is the log plugin protocol the plugins were built for (wire.WireVersion, the
	// wire-v<N> of the plugin publishing) and Platform their GOOS/GOARCH: license-pull
	// refuses a package built for another replication-manager.
	Wire     int             `json:"wire"`
	Platform string          `json:"platform"`
	Entries  []ManifestEntry `json:"entries"`
}

// ManifestEntry maps one file in the tar.gz to its destination under the base dir.
type ManifestEntry struct {
	Path string `json:"path"` // name inside the tar.gz
	Dest string `json:"dest"` // relative destination under the base dir (no abs, no ..)
	Kind string `json:"kind"` // plugin | plugin-sig | config | pubkey
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
// lists to its destination. baseByKind maps a manifest Kind (plugin, plugin-sig,
// config, pubkey) to the base directory for that kind; a file whose kind has no base
// is skipped. This lets repman place the plugin binaries and the settings files in
// the pull dir (so the pull's steps run unchanged), and their .sig where the verify
// already looks (ShareDir/plugins). Plugin binaries are made executable. Returns the manifest. It does NOT verify or load; the caller runs
// the usual sync/verify afterwards.
func ExtractPackage(tarGzPath string, baseByKind map[string]string) (*PackageManifest, error) {
	man, err := ReadPackageManifest(tarGzPath)
	if err != nil {
		return nil, err
	}
	want := make(map[string]ManifestEntry, len(man.Entries))
	for _, e := range man.Entries {
		if e.Dest != "" && baseByKind[e.Kind] != "" {
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
		full, err := safeJoin(baseByKind[e.Kind], e.Dest)
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

// ReadPackageManifest reads the manifest.json of a package, without extracting anything.
func ReadPackageManifest(tarGzPath string) (*PackageManifest, error) {
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
