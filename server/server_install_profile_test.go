package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/spf13/pflag"
)

func TestResolveInstallProfile(t *testing.T) {
	cases := []struct {
		argv0, envProfile, envLayout string
		profile, layout              string
	}{
		// Packages and dev trees: the program name says the flavour it replaces.
		{"/usr/bin/replication-manager-osc", "", "", InstallProfileOsc, InstallLayoutPackage},
		{"/usr/bin/replication-manager-pro", "", "", InstallProfilePro, InstallLayoutPackage},
		{"./replication-manager-tst", "", "", InstallProfileTst, InstallLayoutPackage},
		{"build/binaries/replication-manager-osc-cgo", "", "", InstallProfileOsc, InstallLayoutPackage},
		{"/usr/local/replication-manager/bin/replication-manager-osc-basedir", "", "", InstallProfileOsc, InstallLayoutTarball},
		{"replication-manager-pro-basedir", "", "", InstallProfilePro, InstallLayoutTarball},
		{"build/binaries/replication-manager-emb", "", "", InstallProfilePro, InstallLayoutEmbed},
		// The CLI and the arbitrator embed the server package: their old link defaults.
		{"/usr/bin/replication-manager-cli", "", "", InstallProfileTst, InstallLayoutEmbed},
		{"/usr/bin/replication-manager-arb", "", "", InstallProfileTst, InstallLayoutPackage},
		// Go test binary: the unflagged defaults.
		{"/tmp/go-build/server.test", "", "", InstallProfileTst, InstallLayoutPackage},
		// Any other name: the old emb binary and the release download.
		{"replication-manager", "", "", InstallProfilePro, InstallLayoutEmbed},
		{"replication-manager-linux-amd64", "", "", InstallProfilePro, InstallLayoutEmbed},
		{"REPLICATION-MANAGER-OSC.exe", "", "", InstallProfileOsc, InstallLayoutPackage},
		// Only the word after "replication-manager-" names a flavour.
		{"replication-manager-pro-osc", "", "", InstallProfilePro, InstallLayoutPackage},
		{"repman-osc", "", "", InstallProfilePro, InstallLayoutEmbed},
		{"replication-manager-host1-arb", "", "", InstallProfilePro, InstallLayoutEmbed},
		{"replication-manager-basedir", "", "", InstallProfilePro, InstallLayoutEmbed},
		// Docker images: the environment wins, each variable on its own.
		{"/usr/bin/replication-manager", "osc", "package", InstallProfileOsc, InstallLayoutPackage},
		{"/usr/bin/replication-manager", "PRO", "package", InstallProfilePro, InstallLayoutPackage},
		{"/usr/bin/replication-manager-osc", "pro", "", InstallProfilePro, InstallLayoutPackage},
		{"/usr/bin/replication-manager-osc", "", "tarball", InstallProfileOsc, InstallLayoutTarball},
		// Unknown values are ignored, the next source applies.
		{"/usr/bin/replication-manager-osc", "enterprise", "home", InstallProfileOsc, InstallLayoutPackage},
	}
	for _, c := range cases {
		env := map[string]string{EnvInstallProfile: c.envProfile, EnvInstallLayout: c.envLayout}
		warned := 0
		p := resolveInstallProfile(c.argv0, func(k string) string { return env[k] }, func(string) { warned++ }, nil)
		if p.Profile != c.profile || p.Layout != c.layout {
			t.Errorf("%s env(%q,%q): got %s/%s, want %s/%s", c.argv0, c.envProfile, c.envLayout, p.Profile, p.Layout, c.profile, c.layout)
		}
		if c.envProfile == "enterprise" && warned != 2 {
			t.Errorf("unknown environment values: %d warnings, want 2", warned)
		}
	}
}

// The plain name beside exactly one flavour symlink (a package install) takes that flavour.
func TestResolveInstallProfileSibling(t *testing.T) {
	has := func(set ...string) func(string) bool {
		return func(f string) bool {
			for _, s := range set {
				if s == f {
					return true
				}
			}
			return false
		}
	}
	none := func(string) string { return "" }
	for _, c := range []struct {
		argv0   string
		sibling func(string) bool
		env     map[string]string
		want    string
	}{
		{"/usr/bin/replication-manager", has("osc"), nil, "osc/package"},
		{"/usr/bin/replication-manager", has("pro"), nil, "pro/package"},
		{"build/binaries/replication-manager", has("osc", "tst", "pro"), nil, "pro/embed"}, // a dev tree: no rule
		{"/usr/bin/replication-manager", has(), nil, "pro/embed"},
		{"/usr/bin/replication-manager-osc", has("pro"), nil, "osc/package"}, // the name wins
		{"/usr/bin/replication-manager", has("osc"), map[string]string{EnvInstallProfile: "pro"}, "pro/package"},
	} {
		getenv := none
		if c.env != nil {
			getenv = func(k string) string { return c.env[k] }
		}
		p := resolveInstallProfile(c.argv0, getenv, nil, c.sibling)
		if got := p.Profile + "/" + p.Layout; got != c.want {
			t.Errorf("%s: got %s, want %s", c.argv0, got, c.want)
		}
	}
}

// A process started through a symlink resolves the profile from the symlink's name, as the
// packages and the Makefile install the flavour names (/usr/bin/replication-manager-osc ->
// replication-manager). The test binary re-executes itself through such symlinks.
func TestInstallProfileThroughSymlink(t *testing.T) {
	if os.Getenv("ONEBUILD_PRINT_PROFILE") == "1" {
		p := resolveProcessInstallProfile()
		fmt.Printf("PROFILE=%s/%s\n", p.Profile, p.Layout)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "replication-manager")
	if err := os.Symlink(self, target); err != nil {
		t.Skip(err)
	}
	for name, want := range map[string]string{
		"replication-manager-osc":         "osc/package",
		"replication-manager-pro":         "pro/package",
		"replication-manager-pro-basedir": "pro/tarball",
	} {
		link := filepath.Join(dir, name)
		if err := os.Symlink("replication-manager", link); err != nil { // relative, symlink to a symlink
			t.Fatal(err)
		}
		cmd := exec.Command(link, "-test.run=^TestInstallProfileThroughSymlink$")
		cmd.Env = append(os.Environ(), "ONEBUILD_PRINT_PROFILE=1", EnvInstallProfile+"=", EnvInstallLayout+"=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if !strings.Contains(string(out), "PROFILE="+want+"\n") {
			t.Errorf("%s: want PROFILE=%s, got:\n%s", name, want, out)
		}
	}
}

// addFlagsForProfile runs AddFlags the way the monitor does, for one profile.
func addFlagsForProfile(t *testing.T, profile string) (*pflag.FlagSet, *config.Config) {
	t.Helper()
	p := InstallProfile{Profile: profile, Layout: InstallLayoutPackage}
	savedProv, savedOsvc := WithProvisioning, WithOpenSVC
	WithProvisioning, WithOpenSVC = onOff(p.Provisioning()), onOff(p.OpenSVC())
	defer func() { WithProvisioning, WithOpenSVC = savedProv, savedOsvc }()
	ensureRepMan()
	flags := pflag.NewFlagSet("monitor", pflag.ContinueOnError)
	c := new(config.Config)
	RepMan.AddFlags(flags, c, false)
	return flags, c
}

func flagNames(fs *pflag.FlagSet) []string {
	var names []string
	fs.VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	return names
}

// Every profile registers the same keys: a profile is a set of defaults, never a capability.
func TestInstallProfileRegistersEveryKey(t *testing.T) {
	osc, _ := addFlagsForProfile(t, InstallProfileOsc)
	tst, _ := addFlagsForProfile(t, InstallProfileTst)
	pro, _ := addFlagsForProfile(t, InstallProfilePro)
	o, s, p := flagNames(osc), flagNames(tst), flagNames(pro)
	if len(o) != len(p) || len(s) != len(p) {
		t.Fatalf("flag count osc=%d tst=%d pro=%d, want equal", len(o), len(s), len(p))
	}
	for i := range p {
		if o[i] != p[i] || s[i] != p[i] {
			t.Fatalf("flag %d: osc=%s tst=%s pro=%s", i, o[i], s[i], p[i])
		}
	}
	for _, k := range []string{"prov-db-docker-img", "opensvc", "opensvc-host", "kube-config", "prov-timeout", "spider"} {
		if osc.Lookup(k) == nil {
			t.Errorf("osc profile: %s not registered", k)
		}
	}
}

// The defaults of each profile are the ones its old flavour had.
func TestInstallProfileDefaults(t *testing.T) {
	_, osc := addFlagsForProfile(t, InstallProfileOsc)
	_, tst := addFlagsForProfile(t, InstallProfileTst)
	_, pro := addFlagsForProfile(t, InstallProfilePro)

	// osc: the provisioning and OpenSVC keys at their zero value, as when they were not registered.
	if osc.ProvDbImg != "" || osc.ProvType != "" || osc.ProvAutoUpdateCompliance || osc.ProvDBCreateReplicationUser || osc.ProvObjectAllowOverwrite || osc.ProvDiskSnapshotKeep != 0 {
		t.Errorf("osc: provisioning defaults not zero: img=%q type=%q compliance=%v repluser=%v overwrite=%v keep=%d",
			osc.ProvDbImg, osc.ProvType, osc.ProvAutoUpdateCompliance, osc.ProvDBCreateReplicationUser, osc.ProvObjectAllowOverwrite, osc.ProvDiskSnapshotKeep)
	}
	if osc.Enterprise || osc.ProvHost != "" || osc.ProvEventTimeout != 0 {
		t.Errorf("osc: OpenSVC defaults not zero: opensvc=%v host=%q timeout=%d", osc.Enterprise, osc.ProvHost, osc.ProvEventTimeout)
	}
	if osc.Measurement || osc.ProvDBConfig || osc.BinlogCopyMode != "ssh" || osc.ProvOrchestrator != "onpremise" || osc.ProvOrchestratorEnable != "onpremise,local" {
		t.Errorf("osc: measurement=%v prov-db-config=%v binlog-copy-mode=%s orchestrator=%s enable=%s",
			osc.Measurement, osc.ProvDBConfig, osc.BinlogCopyMode, osc.ProvOrchestrator, osc.ProvOrchestratorEnable)
	}

	// tst: provisioning defaults, no OpenSVC.
	if tst.ProvDbImg != "mariadb:latest" || !tst.ProvAutoUpdateCompliance || !tst.Measurement || !tst.ProvDBConfig || tst.BinlogCopyMode != "mysqlbinlog" {
		t.Errorf("tst: img=%q compliance=%v measurement=%v prov-db-config=%v binlog-copy-mode=%s",
			tst.ProvDbImg, tst.ProvAutoUpdateCompliance, tst.Measurement, tst.ProvDBConfig, tst.BinlogCopyMode)
	}
	if tst.Enterprise || tst.ProvHost != "" || tst.ProvOrchestrator != "onpremise" {
		t.Errorf("tst: opensvc=%v host=%q orchestrator=%s", tst.Enterprise, tst.ProvHost, tst.ProvOrchestrator)
	}

	// pro: provisioning and OpenSVC defaults.
	if pro.ProvDbImg != "mariadb:latest" || !pro.Enterprise || pro.ProvHost == "" || pro.ProvEventTimeout != 600 ||
		pro.ProvOrchestrator != "opensvc" || pro.ProvOrchestratorEnable != "opensvc,kube,onpremise,local" {
		t.Errorf("pro: img=%q opensvc=%v host=%q timeout=%d orchestrator=%s enable=%s",
			pro.ProvDbImg, pro.Enterprise, pro.ProvHost, pro.ProvEventTimeout, pro.ProvOrchestrator, pro.ProvOrchestratorEnable)
	}
}

// Every flag of the two profile groups has a type addFlagsWithDefaults can zero.
func TestInstallProfileGroupsZeroable(t *testing.T) {
	unzeroableProfileFlags = map[string]string{}
	osc, _ := addFlagsForProfile(t, InstallProfileOsc)
	if len(unzeroableProfileFlags) > 0 {
		t.Errorf("profile group flags without a zero value (they keep their default on osc): %v", unzeroableProfileFlags)
	}
	pro, _ := addFlagsForProfile(t, InstallProfilePro)
	pro.VisitAll(func(f *pflag.Flag) {
		o := osc.Lookup(f.Name)
		if o == nil {
			t.Errorf("%s: registered with pro, not with osc", f.Name)
			return
		}
		if o.DefValue == f.DefValue {
			return
		}
		if _, ok := zeroFlagValue(o.Value.Type()); !ok {
			t.Errorf("%s: type %s has no zero value in zeroFlagValue", f.Name, o.Value.Type())
		}
	})
}
