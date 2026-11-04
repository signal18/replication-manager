package licensebuilder

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Request is one queued licence build. The HTTP handler fills it AFTER verifying the
// caller's signal18-admin Keycloak token, so process() trusts it.
type Request struct {
	Company       string // the client / domain
	SubDomain     string // registered sub-domain (licence identity)
	Zone          string // registered zone (licence identity)
	GitLabPullURI string // the -pull repo to create / commit / push
	RepmanVersion string // "" = latest
	Seats         int
	Expiry        string // date limit YYYYMMDD
	Plan          string
	RequestedBy   string // audit: the signal18-admin who triggered
}

// Config is the builder's env-driven setup (read from hardcoded env on the dev image).
type Config struct {
	RepmanBin   string // repman binary (the monitor this builder drives)
	RepmanDir   string // repman source checkout, for `make`
	SignerRepo  string // private signer repo URL (GitHub) — master key + plugin source
	GitHubToken string // standing credential to clone the signer repo
	DataDir     string // the monitor's datadir, where the package is placed for self-test
	WorkDir     string // scratch
	Addr        string // HTTP listen address
}

// Builder processes requests one at a time, driving the dev monitor so each build is
// self-tested (regtest) before it ships. The nine orchestration steps are the methods
// below; see dbaas-portal/doc/LICENSE_PACKAGE_DESIGN.md.
type Builder struct {
	cfg   Config
	queue chan Request
	mon   *monitor
	log   func(format string, a ...interface{})
	mu    sync.Mutex
}

// New builds a Builder. logf may be nil.
func New(cfg Config, logf func(string, ...interface{})) *Builder {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Builder{cfg: cfg, queue: make(chan Request, 64), mon: &monitor{bin: cfg.RepmanBin}, log: logf}
}

// Enqueue adds a verified request; non-blocking up to the queue cap (bounded, F4).
func (b *Builder) Enqueue(r Request) error {
	select {
	case b.queue <- r:
		return nil
	default:
		return fmt.Errorf("license-builder queue is full")
	}
}

// QueueLen reports the pending count (for /queue + tests).
func (b *Builder) QueueLen() int { return len(b.queue) }

// Run processes the queue forever, one request at a time. Call in a goroutine.
func (b *Builder) Run() {
	for r := range b.queue { // step 9 (dequeue) = the range consuming the item
		if err := b.process(r); err != nil {
			b.log("license-builder: request for %s failed: %v", r.Company, err)
		}
	}
}

// process runs the orchestration for one request.
func (b *Builder) process(r Request) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.log("license-builder: building for %s (seats %d, expiry %s, version %q)", r.Company, r.Seats, r.Expiry, r.RepmanVersion)

	// 1. stop the monitor so the build + self-test run cleanly
	if err = b.mon.stop(); err != nil {
		return fmt.Errorf("stop monitor: %w", err)
	}
	// whatever happens, leave the monitor running again (never leave HA down — F2)
	defer func() {
		if sErr := b.mon.start(); sErr != nil && err == nil {
			err = fmt.Errorf("restart monitor: %w", sErr)
		}
	}()

	// 2. pull the signer (GitHub key) -> master private key; make + master-sign the
	//    plugins (reuse the Makefile); derive the master public key from the private key
	masterPriv, err := b.pullSignerKey()
	if err != nil {
		return fmt.Errorf("pull signer: %w", err)
	}
	plugins, err := b.makePlugins(r.RepmanVersion)
	if err != nil {
		return fmt.Errorf("make plugins: %w", err)
	}
	masterPub := DeriveMasterPub(masterPriv) // injected via inject.toml by delivery
	_ = masterPub

	// 3. sign + package the licence into the tar.gz
	pkg, err := WritePackage(masterPriv, r.Company, r.SubDomain, r.Zone, r.Plan, r.Expiry, r.Seats, r.RepmanVersion, plugins)
	if err != nil {
		return fmt.Errorf("package: %w", err)
	}
	if err = b.placeInDatadir(pkg); err != nil {
		return fmt.Errorf("place package: %w", err)
	}

	// 4. start the monitor (loads what we built) — handled by the defer above, but we
	//    start it now so the regtest runs against it, then the defer is a no-op/ensure.
	if err = b.mon.start(); err != nil {
		return fmt.Errorf("start monitor: %w", err)
	}

	// 5. run regtests, if any, against the live monitor
	if err = b.runRegtests(); err != nil {
		return fmt.Errorf("regtest: %w", err)
	}

	// 6. gitlab: create the -pull project if it does not exist
	if err = b.gitlabEnsureProject(r.GitLabPullURI); err != nil {
		return fmt.Errorf("gitlab ensure project: %w", err)
	}

	// 7 + 8. git commit + push the package to the -pull repo
	if err = b.gitCommitPush(r.GitLabPullURI, pkg); err != nil {
		return fmt.Errorf("git commit/push: %w", err)
	}

	b.log("license-builder: delivered package for %s to %s", r.Company, r.GitLabPullURI)
	return nil
}

// ---- external steps: exec the existing tooling, never reimplement (T2) -------------
// These are the seams that wrap the Makefile / git / the monitor process. Each is a
// thin exec wrapper; the exact commands live here so the orchestration reads top-down.

// pullSignerKey clones the private signer repo with the GitHub token and reads the
// master private key (plugin-signing.key). TODO: wire the clone + key read.
func (b *Builder) pullSignerKey() (ed25519.PrivateKey, error) {
	// git clone https://<token>@<SignerRepo> ; read plugin-signing.key (64B ed25519)
	return nil, fmt.Errorf("pullSignerKey: not wired yet")
}

// makePlugins runs the Makefile / build-sign-enterprise.sh for the version and returns
// the built, master-signed plugins. TODO: exec `make plugins` + build-sign-enterprise.sh.
func (b *Builder) makePlugins(version string) ([]Plugin, error) {
	_ = exec.Command // placeholder until wired
	return nil, fmt.Errorf("makePlugins: not wired yet")
}

// placeInDatadir writes the package tar.gz where the monitor will pick it up.
func (b *Builder) placeInDatadir(pkg []byte) error {
	if b.cfg.DataDir == "" {
		return fmt.Errorf("DataDir not configured")
	}
	if err := os.MkdirAll(b.cfg.DataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.cfg.DataDir, "license-package.tar.gz"), pkg, 0o644)
}

// runRegtests runs the regression suite against the live monitor, if any. TODO.
func (b *Builder) runRegtests() error { return nil }

// gitlabEnsureProject creates the -pull project if absent (GitLab API). TODO.
func (b *Builder) gitlabEnsureProject(pullURI string) error { return nil }

// gitCommitPush commits the package into a clone of the -pull repo and pushes. TODO.
func (b *Builder) gitCommitPush(pullURI string, pkg []byte) error { return nil }

// ---- the managed monitor process ---------------------------------------------------

type monitor struct {
	bin string
	cmd *exec.Cmd
}

func (m *monitor) start() error {
	if m.bin == "" {
		return nil // nothing to drive (e.g. in tests)
	}
	if m.cmd != nil && m.cmd.Process != nil {
		return nil // already running
	}
	cmd := exec.Command(m.bin, "monitor") // #nosec G204 — bin is operator config
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	return nil
}

func (m *monitor) stop() error {
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	_ = m.cmd.Process.Kill()
	_, _ = m.cmd.Process.Wait()
	m.cmd = nil
	return nil
}
