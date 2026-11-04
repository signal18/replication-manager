// Package licensebuilder is the Cloud18 licence-builder (#2309): it builds, for one seat, the
// licence package an instance loads with license-pull (or receives through its -pull repo), and
// tests it on the replication-manager monitor it runs beside. See
// dbaas-portal/doc/LICENSE_PACKAGE_DESIGN.md and doc/implementation/licensebuilder/LICENSE_BUILDER.md.
//
// This file writes the package tar.gz that cluster/logplugin.ExtractPackage reads: the plugins
// and their signatures (made by the Makefile with the Signal18 plugin key) and the manifest.
// The public key is not in the package: every replication-manager release ships it
// (share/plugins/plugin-signing.pub), so a package cannot bring a key of its own.
package licensebuilder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin"
)

const packageFormat = "cloud18-license-package/v1"

// Plugin is a built plugin binary and its detached signature.
type Plugin struct {
	Name string
	Data []byte
	Sig  []byte
}

// WritePackage builds the package of one seat: each plugin under plugins/ (kind plugin, placed in
// the pull dir) and its signature under sigs/ (kind plugin-sig, placed where the verify looks),
// with the manifest naming the seat's URI, the log plugin wire and the platform.
func WritePackage(company, uri string, wire int, platform string, built time.Time, plugins []Plugin) ([]byte, error) {
	if len(plugins) == 0 {
		return nil, fmt.Errorf("no plugin to package")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	man := logplugin.PackageManifest{Format: packageFormat, Company: company, URI: uri, Wire: wire,
		Platform: platform, Built: built.UTC().Format(time.RFC3339)}

	add := func(path string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data)), ModTime: built}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	for _, p := range plugins {
		if len(p.Sig) == 0 {
			return nil, fmt.Errorf("plugin %s has no signature", p.Name)
		}
		if err := add("plugins/"+p.Name, p.Data); err != nil {
			return nil, err
		}
		if err := add("sigs/"+p.Name+".sig", p.Sig); err != nil {
			return nil, err
		}
		man.Entries = append(man.Entries,
			logplugin.ManifestEntry{Path: "plugins/" + p.Name, Dest: p.Name, Kind: "plugin"},
			logplugin.ManifestEntry{Path: "sigs/" + p.Name + ".sig", Dest: p.Name + ".sig", Kind: "plugin-sig"})
	}

	manJSON, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := add("manifest.json", manJSON); err != nil {
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
