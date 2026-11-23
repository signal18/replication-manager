package licensebuilder

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// The builder's HTTP API, every call gated by a Cloud18 realm token of a signal18-admin:
//
//	POST /queue                 queue one seat (Request), 202
//	GET  /queue                 the requests waiting, the one building, the last results
//	GET  /packages/<uri>.tar.gz download a built package (air-gapped delivery)

// adminGroups and adminRoles are what make a realm token a Signal18 admin's, as the CRM reads
// them (groups claim or realm role).
var (
	adminGroups = []string{"/signal18-admins", "signal18-admins"}
	adminRoles  = []string{"signal18-admin"}
)

// Handler returns the builder's HTTP API.
func (b *Builder) Handler() http.Handler {
	verifier := oidc.NewVerifier(strings.TrimRight(b.cfg.IssuerURL, "/"),
		oidc.NewRemoteKeySet(context.Background(), strings.TrimRight(b.cfg.IssuerURL, "/")+"/protocol/openid-connect/certs"),
		&oidc.Config{SkipClientIDCheck: true}) // the audience is the CRM's client: checked as azp below
	mux := http.NewServeMux()
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		who, ok := b.admin(w, r, verifier)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, b.Status())
		case http.MethodPost:
			var req Request
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
				b.log("license-builder: POST /queue by %s refused: decode body: %v", who, err)
				http.Error(w, "decode body: "+err.Error(), http.StatusBadRequest)
				return
			}
			req.RequestedBy = who
			if err := b.Enqueue(req); err != nil {
				b.log("license-builder: POST /queue by %s refused: %s (uri %s, version %s, airGapped %v)", who, err, req.URI, req.Version, req.AirGapped)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			b.log("license-builder: %s queued by %s (version %s, platform %s, airGapped %v)", req.URI, who, req.Version, req.Platform, req.AirGapped)
			writeJSON(w, http.StatusAccepted, map[string]string{"queued": req.URI})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/packages/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := b.admin(w, r, verifier); !ok {
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/packages/")
		if !strings.HasSuffix(name, ".tar.gz") || !uriPattern.MatchString(strings.TrimSuffix(name, ".tar.gz")) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		http.ServeFile(w, r, filepath.Join(b.cfg.OutDir, name))
	})
	return mux
}

// admin verifies the bearer: a realm token (signature, issuer, expiry), from an allowed client,
// of a signal18-admin. It answers 401/403 itself and returns the caller's email. Every request
// leaves one log line: accepted, or refused with the reason (the token itself is never logged).
func (b *Builder) admin(w http.ResponseWriter, r *http.Request, verifier *oidc.IDTokenVerifier) (string, bool) {
	who := "unknown caller"
	refuse := func(code int, reason string) (string, bool) {
		b.log("license-builder: %s %s by %s refused (%d): %s", r.Method, r.URL.Path, who, code, reason)
		http.Error(w, reason, code)
		return "", false
	}
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || raw == "" {
		return refuse(http.StatusUnauthorized, "Authorization: Bearer <Cloud18 token> required")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tok, err := verifier.Verify(ctx, raw)
	if err != nil {
		return refuse(http.StatusUnauthorized, "token refused: "+strings.ReplaceAll(err.Error(), raw, "[token]"))
	}
	var c struct {
		Email       string   `json:"email"`
		Azp         string   `json:"azp"`
		Groups      []string `json:"groups"`
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := tok.Claims(&c); err != nil {
		return refuse(http.StatusUnauthorized, "token claims: "+err.Error())
	}
	if c.Email != "" {
		who = c.Email
	}
	if !slices.Contains(b.cfg.AllowedClients, c.Azp) {
		return refuse(http.StatusForbidden, fmt.Sprintf("token of client %q not allowed", c.Azp))
	}
	isAdmin := slices.ContainsFunc(c.Groups, func(g string) bool { return slices.Contains(adminGroups, g) }) ||
		slices.ContainsFunc(c.RealmAccess.Roles, func(r string) bool { return slices.Contains(adminRoles, r) })
	if !isAdmin {
		b.log("license-builder: %s %s by %s: groups %v, roles %v", r.Method, r.URL.Path, who, c.Groups, c.RealmAccess.Roles)
		return refuse(http.StatusForbidden, "signal18-admin required")
	}
	b.log("license-builder: %s %s by %s accepted", r.Method, r.URL.Path, who)
	return c.Email, true
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func hostPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// monitorClient talks to the monitor the builder runs beside. Its API listens on loopback with
// its own self-signed certificate: the certificate is not checked for a loopback URL only.
func monitorClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — loopback only, see loopbackURL
	}}
}

// loopbackURL says whether the monitor URL points at this host.
func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
