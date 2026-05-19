package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	gitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// privateNets is the set of IP ranges that the server must never contact on
// behalf of a user-supplied URL. Parsed once at startup.
var privateNets = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local / cloud metadata (169.254.169.254)
		"100.64.0.0/10",  // shared address space (RFC 6598)
		"0.0.0.0/8",      // "this" network
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 ULA
		"fe80::/10",      // IPv6 link-local
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		if n != nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

func isPrivateIP(ip net.IP) bool {
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// isSSRFTarget returns true when repoURL should be blocked.
// Enforces scheme allowlist, IP-literal blocking, and DNS resolution checking.
// DNS resolution failure is treated as a block (fail-closed).
func isSSRFTarget(repoURL string) bool {
	raw := repoURL
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "git@") {
		raw = "https://" + raw
	}

	var host string
	if strings.HasPrefix(repoURL, "git@") {
		part := strings.TrimPrefix(repoURL, "git@")
		if idx := strings.Index(part, ":"); idx != -1 {
			host = part[:idx]
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return true
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return true
		}
		host = u.Hostname()
	}

	if host == "" {
		return true
	}

	switch strings.ToLower(host) {
	case "localhost", "ip6-localhost", "ip6-loopback":
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if isPrivateIP(a.IP) {
			return true
		}
	}
	return false
}

// safeDialContext is a DialContext function for http.Transport that validates
// the connection target at TCP-connect time. This catches threats that
// isSSRFTarget cannot: DNS rebinding between URL validation and connection,
// and redirects that resolve to private addresses.
//
// http.Transport resolves the hostname before calling DialContext, so addr is
// always "ip:port" by the time this function is called — but we handle the
// hostname case too for safety.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	if ip := net.ParseIP(host); ip != nil {
		// Already an IP (normal case after http.Transport DNS resolution).
		if isPrivateIP(ip) {
			return nil, fmt.Errorf("connection to private/internal address %s blocked", ip)
		}
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, network, addr)
	}

	// Hostname (fallback — should not happen with http.Transport but handled
	// defensively). Resolve and check before connecting.
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
	}
	for _, a := range ips {
		if isPrivateIP(a.IP) {
			return nil, fmt.Errorf("connection to private/internal address blocked")
		}
	}
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(host, port))
}

// safeGitHTTPClient is an http.Client used for all go-git HTTP/HTTPS operations.
// It enforces SSRF protection at connection time and on every redirect.
var safeGitHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	},
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if isSSRFTarget(req.URL.String()) {
			return errors.New("redirect to private/internal address blocked")
		}
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

func init() {
	// Replace go-git's default HTTP/HTTPS transports globally so every outbound
	// git operation in this process uses the safe client. This applies to
	// GetRepositoryTree, CheckRepo, DownloadFileFromRepo, and any future go-git
	// calls added to the codebase.
	gitclient.InstallProtocol("https", githttp.NewClient(safeGitHTTPClient))
	gitclient.InstallProtocol("http", githttp.NewClient(safeGitHTTPClient))
}
