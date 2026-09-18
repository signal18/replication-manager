// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// See LICENSE in this directory for the integral text.

package server

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// OfflineLicense is the signed payload an air-gapped / PCI instance uses to obtain
// its Cloud18 subscription plan without reaching the CRM. It is generated and
// signed back-office side (per client, from their plan), the client downloads it
// from their GitLab account and drops it on the instance. repman verifies it
// OFFLINE against the embedded plugin-signing public key and only accepts a
// license whose identity matches this instance's configured domain/sub-domain/zone.
//
// No expiry: validity is tied to the signing key — rotating the key invalidates
// every license signed with the old one (revocation = key rotation). Only the
// PUBLIC key is embedded in repman; the license + its .sig are delivered (they are
// per-client, generated after the binary is built, so they cannot be embedded).
type OfflineLicense struct {
	Domain        string `json:"domain"`
	SubDomain     string `json:"subDomain"`
	SubDomainZone string `json:"subDomainZone"`
	Plan          string `json:"plan"`
	IssuedAt      string `json:"issuedAt"`
	Nonce         string `json:"nonce"`
}

const licenseStateKey = "GWARN015@license"

// loadOfflineLicensePlan sources the Cloud18 subscription plan from the signed
// offline license (cloud18-license-file) instead of the CRM. It is the offline
// courier of the plan that syncSubscriptionPlanFromCRM would fetch online, and is
// used only when cloud18-license-file is set (air-gapped / PCI instances).
//
// Soft by design (never gates monitoring): any failure — missing file, bad
// signature, identity mismatch, wrong/rotated key — leaves the plan at its
// configured default (free) and raises a WARN state; it never blocks. The plan is
// self-declared/soft, and the license is a cryptographically-vouched courier, not
// a DRM lock.
func (repman *ReplicationManager) loadOfflineLicensePlan() {
	licPath := repman.Conf.Cloud18LicenseFile
	uri := repman.registeredInstanceURI()

	fail := func(format string, args ...interface{}) {
		desc := fmt.Sprintf(format, args...)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"offline license: %s", desc)
		repman.SetState(licenseStateKey, state.State{ErrType: "WARNING", ErrKey: "GWARN015",
			ErrDesc: fmt.Sprintf(config.GlobalError["GWARN015"], desc), ErrFrom: "REPMAN"})
	}

	// Verify with the SAME embedded public key used for plugin signatures.
	pubKeyPath := repman.Conf.PluginSigningPublicKey
	pubBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		fail("cannot read signing public key %s: %s", pubKeyPath, err)
		return
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		fail("invalid public key length %d (expected %d)", len(pubBytes), ed25519.PublicKeySize)
		return
	}

	data, err := os.ReadFile(licPath)
	if err != nil {
		fail("cannot read license file %s: %s", licPath, err)
		return
	}

	// Detached signature: <name>.sig next to the license (license.json -> license.sig),
	// also accepting the <name>.json.sig form.
	sigPath := strings.TrimSuffix(licPath, filepath.Ext(licPath)) + ".sig"
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		if alt, altErr := os.ReadFile(licPath + ".sig"); altErr == nil {
			sig = alt
		} else {
			fail("cannot read license signature %s: %s", sigPath, err)
			return
		}
	}

	if !ed25519.Verify(ed25519.PublicKey(pubBytes), data, sig) {
		fail("signature mismatch — not signed by the current signing key (or tampered)")
		return
	}

	var lic OfflineLicense
	if err := json.Unmarshal(data, &lic); err != nil {
		fail("cannot parse license JSON: %s", err)
		return
	}

	// Identity binding: the license only applies to the instance it was issued for.
	if lic.Domain != repman.Conf.Cloud18Domain ||
		lic.SubDomain != repman.Conf.Cloud18SubDomain ||
		lic.SubDomainZone != repman.Conf.Cloud18SubDomainZone {
		fail("identity mismatch — license is for %s.%s.%s but this instance is %q",
			lic.Domain, lic.SubDomain, lic.SubDomainZone, uri)
		return
	}

	if lic.Plan == "" {
		fail("license carries no plan")
		return
	}

	// Success: apply the plan just like the CRM path.
	repman.persistInstanceSubscriptionPlan(lic.Plan, uri)
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"offline license: applied plan %q for %s (signed, identity-verified)", lic.Plan, uri)
}
