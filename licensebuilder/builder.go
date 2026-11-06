package licensebuilder

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request is one seat to build, queued by the CRM after the HTTP layer verified the caller's
// signal18-admin token.
type Request struct {
	Company     string `json:"company"`   // the Cloud18 company code, the URI's domain
	URI         string `json:"uri"`       // <company>.<expiry YYYYMM>.<seat>
	Seat        int    `json:"seat"`      // 1..N
	Expiry      string `json:"expiry"`    // YYYYMM
	AirGapped   bool   `json:"airGapped"` // delivered as a download (license-pull), not a -pull push
	Version     string `json:"version"`   // the replication-manager release to build the plugins of
	Platform    string `json:"platform"`  // GOOS/GOARCH, default linux/amd64
	RequestedBy string `json:"requestedBy"`
}

// Result is the outcome of one request, kept for GET /queue.
type Result struct {
	Request
	State    string    `json:"state"` // queued, building, testing, done, failed
	Error    string    `json:"error,omitempty"`
	Package  string    `json:"package,omitempty"` // file name under OutDir
	Loaded   []string  `json:"loaded,omitempty"`  // plugins the monitor loaded from the package
	Started  time.Time `json:"started,omitempty"`
	Finished time.Time `json:"finished,omitempty"`
}

// Config is the builder's setup, read from the environment by the license-builder command.
type Config struct {
	RepoURL         string   // replication-manager source to clone per version (public)
	SourceDir       string   // the builder's own source checkout: builds plugin-licence
	WorkDir         string   // version clones
	OutDir          string   // packages kept for download
	DataDir         string   // the monitor's datadir, where license-pull reads the package
	MonitorURL      string   // the monitor's API, e.g. https://127.0.0.1:10005
	MonitorUser     string   // an API user of the monitor with the global settings grant
	MonitorPassword string   //
	SignerUser      string   // signer repo credentials: make plugin-keys fetches the Signal18 key
	SignerToken     string   //
	GitBaseURL      string   // Cloud18 GitLab base (e.g. https://gitlab.signal18.io) for -pull delivery
	GitUser         string   // Cloud18 git identity (defaults to oauth2) used to push the -pull repos
	GitToken        string   // its token; carried in an Authorization header, never in the remote URL
	IssuerURL       string   // the Cloud18 realm that signs the callers' tokens
	AllowedClients  []string // the clients a caller's token may come from (azp), e.g. cloud18-crm
	Addr            string   // HTTP listen address
}

// Builder runs the queued requests one at a time.
type Builder struct {
	cfg   Config
	queue chan Request
	log   func(format string, a ...interface{})
	run   func(dir string, env []string, name string, args ...string) error // exec seam (tests)
	test  func(pkg string) ([]string, error)                                // license-pull seam (tests)

	mu      sync.Mutex
	current *Result
	results []Result // the last resultsMax, newest last
}

const (
	queueMax   = 64 // bounded queue (F4)
	resultsMax = 50 // bounded history (F4)
)

var uriPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}\.[0-9]{6}\.[0-9]+$`)

// fullURIPattern is a registered instance's identity URI, domain.subdomain.zone (any number of
// lowercase dotted labels); its expiry is carried separately, not in the URI.
var fullURIPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)

// New builds a Builder. logf may be nil.
func New(cfg Config, logf func(string, ...interface{})) *Builder {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	b := &Builder{cfg: cfg, queue: make(chan Request, queueMax), log: logf}
	b.run = b.exec
	b.test = b.licensePull
	return b
}

// Validate checks a request before it is queued.
func (r *Request) Validate() error {
	if r.Platform == "" {
		r.Platform = "linux/amd64"
	}
	switch {
	case r.Company == "" || !strings.HasPrefix(r.URI, r.Company+"."):
		return fmt.Errorf("uri %q is not of company %q", r.URI, r.Company)
	case r.Version == "" || strings.ContainsAny(r.Version, " /\\"):
		return fmt.Errorf("version %q", r.Version)
	case strings.Count(r.Platform, "/") != 1:
		return fmt.Errorf("platform %q is not GOOS/GOARCH", r.Platform)
	case r.AirGapped && !uriPattern.MatchString(r.URI):
		return fmt.Errorf("air-gapped uri %q is not <company>.<YYYYMM>.<seat>", r.URI)
	case !r.AirGapped && !fullURIPattern.MatchString(r.URI):
		return fmt.Errorf("online uri %q is not a dotted domain.subdomain.zone", r.URI)
	case !r.AirGapped && r.Expiry == "":
		return fmt.Errorf("online licence for %q needs an expiry (the contract end date)", r.URI)
	}
	// Never build a born-expired licence: a mis-created contract (a past or too-short term) must
	// fail HERE, on our side, not after it ships and the client is the one who hits ERR00122. The
	// expiry is the URI's YYYYMM for an air-gapped seat, the contract end for a registered instance.
	if end, err := r.expiryEnd(); err == nil && !time.Now().Before(end) {
		return fmt.Errorf("licence %q expires %s, already in the past: fix the contract term before building — a born-expired licence must never ship", r.URI, end.AddDate(0, 0, -1).Format("2006-01-02"))
	}
	return nil
}

// expiryEnd is the first instant after this request's licence is last valid: the contract end
// (Expiry) for a registered instance, the URI's YYYYMM for an air-gapped seat.
func (r *Request) expiryEnd() (time.Time, error) {
	if !r.AirGapped {
		return parseExpiry(r.Expiry)
	}
	return licenceExpiryEnd(r.URI)
}

// parseExpiry accepts YYYY-MM-DD (valid through that day) or YYYYMM (valid through that month),
// mirroring the plugin-licence's own parse of a separately-carried expiry.
func parseExpiry(s string) (time.Time, error) {
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return d.AddDate(0, 0, 1), nil
	}
	if m, err := time.Parse("200601", s); err == nil {
		return m.AddDate(0, 1, 0), nil
	}
	return time.Time{}, fmt.Errorf("licence expiry %q is not YYYY-MM-DD or YYYYMM", s)
}

// licenceExpiryEnd parses <company>.<YYYYMM>.<seat> and returns the first instant after the expiry
// month. It mirrors plugin-licence's own parse (kept local for the build-time guardrail; folding
// both onto one shared helper is the T2 follow-up).
func licenceExpiryEnd(uri string) (time.Time, error) {
	p := strings.Split(uri, ".")
	if len(p) != 3 {
		return time.Time{}, fmt.Errorf("uri %q is not <company>.<YYYYMM>.<seat>", uri)
	}
	m, err := time.Parse("200601", p[1])
	if err != nil {
		return time.Time{}, err
	}
	return m.AddDate(0, 1, 0), nil
}

// Enqueue adds a verified request; refused when the queue is full (bounded, F4).
func (b *Builder) Enqueue(r Request) error {
	if err := r.Validate(); err != nil {
		return err
	}
	select {
	case b.queue <- r:
		b.record(Result{Request: r, State: "queued"})
		return nil
	default:
		return fmt.Errorf("license-builder queue is full (%d)", queueMax)
	}
}

// Run processes the queue forever, one request at a time. Call in a goroutine.
func (b *Builder) Run() {
	for r := range b.queue {
		res := Result{Request: r, State: "building", Started: time.Now()}
		b.setCurrent(&res)
		if err := b.process(&res); err != nil {
			res.State, res.Error = "failed", err.Error()
			b.log("license-builder: %s failed: %v", r.URI, err)
		} else {
			res.State = "done"
			b.log("license-builder: %s done, package %s, plugins loaded %v", r.URI, res.Package, res.Loaded)
		}
		res.Finished = time.Now()
		b.setCurrent(nil)
		b.record(res)
	}
}

// Status is GET /queue: the requests waiting, the one building, and the last results.
func (b *Builder) Status() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	results := append([]Result(nil), b.results...)
	var current *Result
	if b.current != nil {
		c := *b.current
		current = &c
	}
	return map[string]interface{}{"pending": len(b.queue), "current": current, "results": results}
}

func (b *Builder) setCurrent(r *Result) {
	b.mu.Lock()
	b.current = r
	b.mu.Unlock()
}

// record keeps a result, the queued entry of the same URI replaced by its outcome.
func (b *Builder) record(r Result) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.results) - 1; i >= 0; i-- {
		if b.results[i].URI == r.URI && b.results[i].State == "queued" && r.State != "queued" {
			b.results = append(b.results[:i], b.results[i+1:]...)
			break
		}
	}
	b.results = append(b.results, r)
	if n := len(b.results); n > resultsMax {
		b.results = append([]Result(nil), b.results[n-resultsMax:]...) // fresh array: frees the old
	}
}

// process builds one seat/instance's licence plugin, then either packages it for an air-gapped
// download (with the release's other plugins, tested via license-pull) or stores the signed
// plugin-licence alone, for delivery into a registered instance's -pull repo (#2309).
func (b *Builder) process(res *Result) error {
	r := res.Request
	goos, goarch, _ := strings.Cut(r.Platform, "/")
	env := []string{"OS=" + goos, "ARCH=" + goarch, "PLUGIN_SIGNER_USER=" + b.cfg.SignerUser, "PLUGIN_SIGNER_TOKEN=" + b.cfg.SignerToken}

	// 1. the release clone (once per version), for the wire the seat's licence plugin must speak
	src, err := b.versionSource(r.Version)
	if err != nil {
		return err
	}
	wire, err := wireVersion(src)
	if err != nil {
		return err
	}
	if own, err := wireVersion(b.cfg.SourceDir); err != nil || own != wire {
		return fmt.Errorf("plugin-licence source is wire %d, release %s is wire %d (%v)", own, r.Version, wire, err)
	}

	// 2. the licence plugin, from the builder's own source. An air-gapped seat carries its expiry
	//    in the URI (<company>.<YYYYMM>.<seat>); a registered instance carries the full identity URI
	//    and the expiry separately (LICENCE_EXPIRY = the contract end).
	if err := os.RemoveAll(filepath.Join(b.cfg.SourceDir, "build", "plugins")); err != nil {
		return err
	}
	licenceEnv := append(env, "LICENCE_URI="+r.URI)
	if !r.AirGapped {
		licenceEnv = append(licenceEnv, "LICENCE_EXPIRY="+r.Expiry)
	}
	if err := b.run(b.cfg.SourceDir, licenceEnv, "make", "plugin-licence"); err != nil {
		return fmt.Errorf("make plugin-licence: %w", err)
	}
	licence, err := collectPlugins(b.cfg.SourceDir)
	if err != nil {
		return err
	}

	// 3a. Registered (online) instance: the licence plugin rides the instance's -pull repo with the
	//     other plugins, rebuilt and re-delivered on every version change — no package, no
	//     license-pull self-test (that exercises the air-gapped install path). Store the signed
	//     plugin-licence (+ .sig) keyed by uri/wire/platform; the -pull delivery reads it from here.
	if !r.AirGapped {
		name, err := writeLicencePlugin(b.cfg.OutDir, r, wire, licence)
		if err != nil {
			return err
		}
		res.Package = name
		res.Loaded = pluginNames(licence)
		if err := b.deliverToPull(r, licence); err != nil {
			return fmt.Errorf("deliver to -pull: %w", err)
		}
		return nil
	}

	// 3b. Air-gapped seat: the full package (the release's plugins + the licence plugin) the client
	//     extracts into its datadir, tested here with license-pull as the client would.
	if err := os.RemoveAll(filepath.Join(src, "build", "plugins")); err != nil {
		return err
	}
	if err := b.run(src, env, "make", "plugins"); err != nil {
		return fmt.Errorf("make plugins (%s): %w", r.Version, err)
	}
	plugins, err := collectPlugins(src)
	if err != nil {
		return err
	}
	plugins = append(plugins, licence...)
	pkg, err := WritePackage(r.Company, r.URI, wire, r.Platform, time.Now(), plugins)
	if err != nil {
		return err
	}
	name := r.URI + ".tar.gz"
	if err := os.MkdirAll(b.cfg.OutDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(b.cfg.OutDir, name), pkg, 0o644); err != nil {
		return err
	}
	res.Package = name

	// 4. remove every plugin built, and what a previous test left on the monitor: the test
	//    then loads the package's plugins only
	for _, dir := range []string{filepath.Join(src, "build", "plugins"), filepath.Join(b.cfg.SourceDir, "build", "plugins"),
		filepath.Join(b.cfg.DataDir, ".pull", "plugins"), filepath.Join(b.cfg.DataDir, "plugins")} {
		if err := removeContents(dir); err != nil {
			return fmt.Errorf("clean %s: %w", dir, err)
		}
	}

	// 5. test: the monitor loads the package as a client would, with license-pull
	if r.Platform != "linux/amd64" && r.Platform != hostPlatform() {
		return fmt.Errorf("package built, not tested: the monitor runs %s, the package is %s", hostPlatform(), r.Platform)
	}
	if err := os.WriteFile(filepath.Join(b.cfg.DataDir, name), pkg, 0o644); err != nil {
		return err
	}
	res.State = "testing"
	b.setCurrent(res)
	loaded, err := b.test(name)
	res.Loaded = loaded
	if err != nil {
		return fmt.Errorf("license-pull on the monitor: %w", err)
	}
	if len(loaded) != len(plugins) {
		return fmt.Errorf("license-pull loaded %d of the %d plugins", len(loaded), len(plugins))
	}
	return nil
}

// writeLicencePlugin stores a registered instance's signed plugin-licence (binary + .sig) under
// OutDir, keyed by uri/wire/platform (two releases of one wire share a plugin; a version upgrade
// that changes the wire gets a fresh build). The -pull delivery reads it from here. Returns the
// stored directory name.
func writeLicencePlugin(outDir string, r Request, wire int, licence []Plugin) (string, error) {
	plat := strings.ReplaceAll(r.Platform, "/", "-")
	name := fmt.Sprintf("%s.wire%d.%s", r.URI, wire, plat)
	dir := filepath.Join(outDir, name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, p := range licence {
		if err := os.WriteFile(filepath.Join(dir, p.Name), p.Data, 0o644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, p.Name+".sig"), p.Sig, 0o644); err != nil {
			return "", err
		}
	}
	return name, nil
}

func pluginNames(plugins []Plugin) []string {
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	return names
}

// deliverToPull pushes a registered instance's licence plugin into its -pull repo, into the same
// plugins/ directory the enterprise plugins live in (inject_plugins' layout: binary + .sig). On its
// next git pull repman syncs and verifies it against the embedded public key, exactly as for the
// other plugins. The repo is <GitBaseURL>/<domain>/<subdomain>-<zone>-pull, derived from the full
// URI domain.subdomain.zone. The git token is sent in an Authorization header, never in the remote
// URL or any error output (F10). Unchanged content (same wire/expiry already there) is a no-op.
func (b *Builder) deliverToPull(r Request, licence []Plugin) error {
	parts := strings.Split(r.URI, ".")
	if len(parts) != 3 {
		return fmt.Errorf("uri %q is not domain.subdomain.zone", r.URI)
	}
	if b.cfg.GitBaseURL == "" || b.cfg.GitToken == "" {
		return fmt.Errorf("-pull delivery not configured (git-base-url / git-token)")
	}
	repoPath := fmt.Sprintf("%s/%s-%s-pull", parts[0], parts[1], parts[2])
	cloneURL := strings.TrimSuffix(b.cfg.GitBaseURL, "/") + "/" + repoPath + ".git"
	user := b.cfg.GitUser
	if user == "" {
		user = "oauth2"
	}
	authHdr := "http.extraheader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+b.cfg.GitToken))
	ident := []string{
		"GIT_AUTHOR_NAME=license-builder", "GIT_AUTHOR_EMAIL=license-builder@signal18.io",
		"GIT_COMMITTER_NAME=license-builder", "GIT_COMMITTER_EMAIL=license-builder@signal18.io",
	}

	dir, err := os.MkdirTemp("", "pull-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := b.run("", nil, "git", "-c", authHdr, "clone", "--depth", "1", cloneURL, dir); err != nil {
		return fmt.Errorf("clone %s: %w", repoPath, err)
	}
	plugins := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		return err
	}
	for _, p := range licence {
		if err := os.WriteFile(filepath.Join(plugins, p.Name), p.Data, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(plugins, p.Name+".sig"), p.Sig, 0o644); err != nil {
			return err
		}
	}
	if err := b.run(dir, nil, "git", "add", "plugins"); err != nil {
		return err
	}
	// git diff --cached --quiet exits 0 when nothing is staged: the same plugin is already there
	// (same wire/expiry), so there is nothing to deliver — a no-op, not an error.
	if b.run(dir, nil, "git", "diff", "--cached", "--quiet") == nil {
		return nil
	}
	if err := b.run(dir, ident, "git", "commit", "-m", "licence plugin for "+r.URI); err != nil {
		return err
	}
	if err := b.run(dir, nil, "git", "-c", authHdr, "push"); err != nil {
		return fmt.Errorf("push %s: %w", repoPath, err)
	}
	return nil
}

// versionSource returns a clone of the release, made once per version.
func (b *Builder) versionSource(version string) (string, error) {
	dir := filepath.Join(b.cfg.WorkDir, "replication-manager-"+version)
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(b.cfg.WorkDir, 0o755); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := b.run(b.cfg.WorkDir, nil, "git", "clone", "--depth", "1", "--branch", version, b.cfg.RepoURL, dir); err != nil {
		return "", fmt.Errorf("clone %s: %w", version, err)
	}
	return dir, nil
}

func (b *Builder) exec(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...) // #nosec G204 — fixed commands, validated arguments
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("%w: %s", err, tail)
	}
	return nil
}

// collectPlugins reads build/plugins/* of a source tree and each one's signature
// (share/plugins/<name>.sig, written by the Makefile's signing).
func collectPlugins(src string) ([]Plugin, error) {
	entries, err := os.ReadDir(filepath.Join(src, "build", "plugins"))
	if err != nil {
		return nil, err
	}
	var plugins []Plugin
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, "build", "plugins", e.Name()))
		if err != nil {
			return nil, err
		}
		sig, err := os.ReadFile(filepath.Join(src, "share", "plugins", e.Name()+".sig"))
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", e.Name(), err)
		}
		plugins = append(plugins, Plugin{Name: e.Name(), Data: data, Sig: sig})
	}
	return plugins, nil
}

var wirePattern = regexp.MustCompile(`WireVersion = (\d+)`)

// wireVersion reads the log plugin wire of a source tree (the Makefile reads it the same way).
func wireVersion(src string) (int, error) {
	data, err := os.ReadFile(filepath.Join(src, "cluster", "logplugin", "plugins", "wire", "wire.go"))
	if err != nil {
		return 0, err
	}
	m := wirePattern.FindSubmatch(data)
	if m == nil {
		return 0, fmt.Errorf("no WireVersion in %s", src)
	}
	return strconv.Atoi(string(m[1]))
}

func removeContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// licensePull loads the package on the monitor through its API, as the GUI button does, and
// returns the plugins it loaded; an error when the monitor refused the package or a plugin.
func (b *Builder) licensePull(pkg string) ([]string, error) {
	if !loopbackURL(b.cfg.MonitorURL) {
		return nil, fmt.Errorf("monitor URL %s is not on this host", b.cfg.MonitorURL)
	}
	client := monitorClient()
	login, _ := json.Marshal(map[string]string{"username": b.cfg.MonitorUser, "password": b.cfg.MonitorPassword})
	resp, err := client.Post(b.cfg.MonitorURL+"/api/login", "application/json", bytes.NewReader(login))
	if err != nil {
		return nil, err
	}
	var tok struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil || tok.Token == "" {
		return nil, fmt.Errorf("monitor login: HTTP %d", resp.StatusCode)
	}
	body, _ := json.Marshal(map[string]string{"path": pkg})
	req, _ := http.NewRequest(http.MethodPost, b.cfg.MonitorURL+"/api/clusters/settings/actions/license-pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var msg bytes.Buffer
		msg.ReadFrom(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(msg.String()))
	}
	var out struct {
		Loaded    []string `json:"loaded"`
		Refused   []string `json:"refused"`
		Unchanged bool     `json:"unchanged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Unchanged {
		return nil, fmt.Errorf("the monitor answered unchanged: this exact package was already loaded")
	}
	if len(out.Refused) > 0 {
		return out.Loaded, fmt.Errorf("plugins refused: %s", strings.Join(out.Refused, ", "))
	}
	return out.Loaded, nil
}
