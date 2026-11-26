// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
)

// One build (#2444): there is one server binary. The old flavours (osc, tst, pro, emb,
// -basedir) only differed by default values, so an install profile and an install layout,
// resolved before any flag is registered, give each existing install the defaults its
// flavour had. A profile is a set of defaults, never a capability: every flag is
// registered whatever the profile. See doc/implementation/build/ONE_BUILD.md.

const (
	InstallProfileOsc = "osc" // provisioning and OpenSVC defaults off
	InstallProfileTst = "tst" // provisioning defaults on, OpenSVC off
	InstallProfilePro = "pro" // provisioning and OpenSVC defaults on

	InstallLayoutPackage = "package" // /etc, /var/lib, /usr/share
	InstallLayoutTarball = "tarball" // /usr/local/replication-manager
	InstallLayoutEmbed   = "embed"   // the user's home, the embedded share

	// EnvInstallProfile and EnvInstallLayout are set by the Docker images. They are not
	// config keys (they decide where the config is read from) and are excluded from the
	// environment cluster discovery.
	EnvInstallProfile = "REPLICATION_MANAGER_INSTALL_PROFILE"
	EnvInstallLayout  = "REPLICATION_MANAGER_INSTALL_LAYOUT"
)

// InstallProfile is the resolved profile and layout of this process, and where each came from.
type InstallProfile struct {
	Profile       string
	Layout        string
	ProfileSource string
	LayoutSource  string
}

// Provisioning reports whether the provisioning keys get their non-zero defaults.
func (p InstallProfile) Provisioning() bool { return p.Profile != InstallProfileOsc }

// OpenSVC reports whether the OpenSVC keys get their defaults (opensvc=true, orchestrator opensvc).
func (p InstallProfile) OpenSVC() bool { return p.Profile == InstallProfilePro }

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

func tarballOnOff(p InstallProfile) string { return onOff(p.Layout == InstallLayoutTarball) }

func embedOnOff(p InstallProfile) string { return onOff(p.Layout == InstallLayoutEmbed) }

func validInstallProfile(v string) bool {
	return v == InstallProfileOsc || v == InstallProfileTst || v == InstallProfilePro
}

func validInstallLayout(v string) bool {
	return v == InstallLayoutPackage || v == InstallLayoutTarball || v == InstallLayoutEmbed
}

// resolveInstallProfile resolves the profile and layout from, first match wins: the
// environment (each one on its own), the program name replication-manager-<token>[-...]
// (first token osc, tst, pro, cli or arb; basedir anywhere after it), a Go test binary
// (tst + package), anything else (pro + embed, the old emb binary and the release
// download). warn receives a line for an unknown environment value.
func resolveInstallProfile(argv0 string, getenv func(string) string, warn func(string)) InstallProfile {
	name := strings.ToLower(filepath.Base(argv0))
	name = strings.TrimSuffix(name, ".exe")

	// From the program name: only the word right after "replication-manager-" names a
	// flavour, so an unrelated suffix (a hostname, a wrapper) cannot select one.
	p := InstallProfile{Profile: InstallProfilePro, Layout: InstallLayoutEmbed, ProfileSource: "default", LayoutSource: "default"}
	if strings.HasSuffix(name, ".test") {
		p = InstallProfile{Profile: InstallProfileTst, Layout: InstallLayoutPackage, ProfileSource: "test binary", LayoutSource: "test binary"}
	} else if rest, ok := strings.CutPrefix(name, "replication-manager-"); ok {
		toks := strings.Split(rest, "-")
		switch toks[0] {
		case InstallProfileOsc, InstallProfileTst, InstallProfilePro:
			p.Profile, p.ProfileSource = toks[0], "program name"
			p.Layout, p.LayoutSource = InstallLayoutPackage, "program name"
		case "cli":
			p.Profile, p.ProfileSource = InstallProfileTst, "program name"
			p.Layout, p.LayoutSource = InstallLayoutEmbed, "program name"
		case "arb":
			p.Profile, p.ProfileSource = InstallProfileTst, "program name"
			p.Layout, p.LayoutSource = InstallLayoutPackage, "program name"
		}
		if p.ProfileSource == "program name" {
			for _, tok := range toks[1:] {
				if tok == "basedir" {
					p.Layout, p.LayoutSource = InstallLayoutTarball, "program name"
				}
			}
		}
	}

	// The environment wins.
	if v := strings.ToLower(strings.TrimSpace(getenv(EnvInstallProfile))); v != "" {
		if validInstallProfile(v) {
			p.Profile, p.ProfileSource = v, EnvInstallProfile
		} else if warn != nil {
			warn(fmt.Sprintf("%s=%q is not one of osc, tst, pro: ignored, profile %s from the %s", EnvInstallProfile, v, p.Profile, p.ProfileSource))
		}
	}
	if v := strings.ToLower(strings.TrimSpace(getenv(EnvInstallLayout))); v != "" {
		if validInstallLayout(v) {
			p.Layout, p.LayoutSource = v, EnvInstallLayout
		} else if warn != nil {
			warn(fmt.Sprintf("%s=%q is not one of package, tarball, embed: ignored, layout %s from the %s", EnvInstallLayout, v, p.Layout, p.LayoutSource))
		}
	}
	return p
}

func resolveProcessInstallProfile() InstallProfile {
	argv0 := ""
	if len(os.Args) > 0 {
		argv0 = os.Args[0]
	}
	return resolveInstallProfile(argv0, os.Getenv, func(s string) { fmt.Fprintln(os.Stderr, s) })
}

// isInstallProfileEnv reports whether an environment variable name is one of the two
// install variables, which the environment cluster discovery must not read as a cluster.
func isInstallProfileEnv(name string) bool {
	return name == EnvInstallProfile || name == EnvInstallLayout
}

// unzeroableProfileFlags lists the flags of a profile group that could not be set to a
// zero value (no zero for their type in zeroFlagValue, or Set refused it): they keep their
// default on every profile. It must stay empty (TestInstallProfileGroupsZeroable). Keyed
// by name, so registering the flags again (tests) does not grow it.
var unzeroableProfileFlags = map[string]string{}

// addFlagsWithDefaults registers the flags that register adds, with their defaults when
// withDefaults is true, else with the zero value of their type: the value an old flavour
// had when it did not register them at all. The flags exist either way, so the key can be
// set from the config, the API and the GUI on every profile.
func addFlagsWithDefaults(flags *pflag.FlagSet, withDefaults bool, register func(*pflag.FlagSet)) {
	group := pflag.NewFlagSet("profile-group", pflag.ContinueOnError)
	register(group)
	if !withDefaults {
		group.VisitAll(func(f *pflag.Flag) {
			zero, ok := zeroFlagValue(f.Value.Type())
			if !ok {
				unzeroableProfileFlags[f.Name] = "type " + f.Value.Type() + " has no zero value"
				return
			}
			if err := f.Value.Set(zero); err != nil {
				unzeroableProfileFlags[f.Name] = "set " + zero + ": " + err.Error()
				return
			}
			f.DefValue = zero
		})
	}
	flags.AddFlagSet(group)
}

func zeroFlagValue(typ string) (string, bool) {
	switch typ {
	case "string":
		return "", true
	case "bool":
		return "false", true
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return "0", true
	case "duration":
		return "0s", true
	}
	return "", false
}
