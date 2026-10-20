package pbscommon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphadose/haxmap"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/http2"
)

// CompressionLevel defines the Zstd compression level
type CompressionLevel string

const (
	// CompressionFastest - Fastest compression, lower CPU usage (~30-40% faster)
	CompressionFastest CompressionLevel = "fastest"
	// CompressionDefault - Balanced compression/speed (zstd default level 3)
	CompressionDefault CompressionLevel = "default"
	// CompressionBetter - Better compression ratio, higher CPU usage
	CompressionBetter CompressionLevel = "better"
	// CompressionBest - Best compression ratio, highest CPU usage
	CompressionBest CompressionLevel = "best"
)

// ParseCompressionLevel converts a string to CompressionLevel
// Returns CompressionFastest if invalid or empty
func ParseCompressionLevel(level string) CompressionLevel {
	switch strings.ToLower(level) {
	case "fastest", "fast":
		return CompressionFastest
	case "default", "":
		return CompressionDefault
	case "better":
		return CompressionBetter
	case "best", "max":
		return CompressionBest
	default:
		return CompressionFastest // Default to fastest
	}
}

type IndexCreateResp struct {
	WriterID int `json:"data"`
}

type IndexPutReq struct {
	DigestList []string `json:"digest-list"`
	OffsetList []uint64 `json:"offset-list"`
	WriterID   uint64   `json:"wid"`
}

type IndexCloseReq struct {
	ChunkCount uint64 `json:"chunk-count"`
	CheckSum   string `json:"csum"`
	Size       uint64 `json:"size"`
	WriterID   uint64 `json:"wid"`
}

type File struct {
	CryptMode string `json:"crypt-mode"`
	Csum      string `json:"csum"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
}

type ChunkUploadStats struct {
	CompressedSize int64 `json:"compressed_size"`
	Count          int   `json:"count"`
	Duplicates     int   `json:"duplicates"`
	Size           int64 `json:"size"`
}

type FixedIndexCreateReq struct {
	ArchiveName string `json:"archive-name"`
	Size        int64  `json:"size"`
}

type Unprotected struct {
	ChunkUploadStats ChunkUploadStats `json:"chunk_upload_stats"`

	// KeyFingerprint carries the colon-hex fingerprint of the encryption key
	// that signed this snapshot, mirroring proxmox-backup's
	// `unprotected["key-fingerprint"]`. It is deliberately *outside* the
	// signature (proxmox-backup removes `unprotected` before computing the
	// auth tag), so a restore can pick the right key before verifying.
	KeyFingerprint *string `json:"key-fingerprint,omitempty"`
}

// BackupManifest mirrors proxmox-backup's `BackupManifest` (src/backup/
// manifest.rs) field for field.
//
// The field set must match exactly. PBS does not store the index.json.blob we
// upload byte for byte: at /finish it parses the blob into its own struct and
// re-serializes it, which drops every key that struct does not declare and
// reformats the document (the stored blob is always serde_json pretty-printed).
// Since the official restore client verifies the manifest signature against that
// re-serialized shape, any extra key we add here would be signed but never
// stored, and every encrypted restore would fail with "wrong signature in
// manifest". Comment is therefore kept in memory but never serialized - PBS
// does not persist it anyway, so writing it out only ever broke signing.
type BackupManifest struct {
	BackupID    string      `json:"backup-id"`
	BackupTime  int64       `json:"backup-time"`
	BackupType  string      `json:"backup-type"`
	Comment     string      `json:"-"`
	Files       []File      `json:"files"`
	Signature   interface{} `json:"signature"`
	Unprotected Unprotected `json:"unprotected"`
}

// AuthErr is returned when PBS refuses to upgrade the connection to the
// backup or reader protocol. Despite its name, that is not always an
// authentication problem: PBS also refuses with 400 ("backup owner check
// failed"), 403 (missing privilege) or 404 ("namespace not found"), with the
// reason as plain text in the body.
type AuthErr struct {
	StatusCode   int    // HTTP status code
	Status       string // reason phrase of the status line, e.g. "Bad Request"
	ResponseBody string // response body only, whitespace-collapsed
}

func (e *AuthErr) Error() string {
	reason := e.ResponseBody
	if reason == "" {
		reason = e.Status
	}
	if reason == "" {
		reason = http.StatusText(e.StatusCode)
	}
	what := "PBS refused the backup"
	switch e.StatusCode {
	case http.StatusUnauthorized:
		what = "PBS authentication failed"
	case http.StatusForbidden:
		what = "PBS access denied"
	}
	return fmt.Sprintf("%s (HTTP %d): %s", what, e.StatusCode, reason)
}

// upgradeRejection builds the error for a refused protocol upgrade from the
// response status line ("HTTP/1.1 400 Bad Request") and body. The response
// headers are left out: they only go to the debug log.
func upgradeRejection(statusLine string, body []byte) *AuthErr {
	e := &AuthErr{}
	toks := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(toks) > 1 {
		e.StatusCode, _ = strconv.Atoi(toks[1])
	}
	if len(toks) > 2 {
		e.Status = toks[2]
	}
	text := strings.TrimSpace(string(body))
	// PBS answers API errors in JSON ({"message": ...}) and the upgrade
	// refusal in plain text; accept both.
	var j struct {
		Message string `json:"message"`
	}
	if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &j) == nil && j.Message != "" {
		text = j.Message
	}
	e.ResponseBody = strings.Join(strings.Fields(text), " ")
	return e
}

// NamespaceNotFoundErr is returned when the server rejects a request because
// the requested namespace does not exist yet.
//
// Namespaces are a server-side object and have to be created before they can be
// used. Every other PBS client behaves the same way, but the raw server answer
// ("404 namespace not found") arriving as an *AuthErr* on the HTTP/2 upgrade
// handshake is genuinely misleading: it reads like a credential problem. This
// type exists purely to turn that into an actionable message.
type NamespaceNotFoundErr struct {
	Namespace string
	Datastore string
}

func (e *NamespaceNotFoundErr) Error() string {
	return fmt.Sprintf("PBS namespace %q does not exist on datastore %q: create it first, "+
		"e.g. POST /api2/json/admin/datastore/%s/namespace with name=%s "+
		"(note: the parameter is \"name\", not \"ns\"; proxmox-backup-manager has no "+
		"\"namespace\" command, use the API)", e.Namespace, e.Datastore, e.Datastore, e.Namespace)
}

// namespaceNotFound reports whether a PBS error body means "that namespace
// does not exist" rather than a genuine authentication failure.
func namespaceNotFound(body string) bool {
	return strings.Contains(strings.ToLower(body), "namespace not found")
}

type PBSClient struct {
	BaseURL         string
	CertFingerPrint string
	APIToken        string
	Secret          string
	AuthID          string

	// Ticket login (username+password → POST /access/ticket). When Ticket is
	// non-empty, requests authenticate with "Cookie: PBSAuthCookie=<ticket>"
	// (+ CSRFPreventionToken) instead of the PBSAPIToken Authorization header.
	// This mirrors how PBS's own web UI / client authenticate.
	Username  string
	Password  string
	Ticket    string
	CSRFToken string

	Datastore string
	Namespace string
	Manifest  BackupManifest

	Insecure bool

	Client    http.Client
	TLSConfig tls.Config

	WritersManifest  map[uint64]int
	SkippedFiles     []string         // ALL skips (errors + system auto-excludes + junctions) — for display
	ExcludedFiles    []string         // Track files/dirs excluded by user policy (H-04)
	ReadErrors       []string         // Outcome-affecting read failures + content instability (v2-H-02)
	CompressionLevel CompressionLevel // Zstd compression level (default: fastest)

	// Crypt, when non-nil, puts every chunk and blob this client uploads
	// into AES-256-GCM DataBlobs and switches chunk digests to
	// sha256(plaintext || id_key) — the exact scheme proxmox-backup-server
	// and the official proxmox-backup-client expect for `--crypt-mode
	// encrypt`. It is also the decryption key when reading (GetChunkData /
	// DownloadBlob), so a restore of an encrypted snapshot needs nothing
	// else. Nil (the default) keeps the pre-existing unencrypted framing.
	Crypt *CryptConfig

	// activeConn is the raw TLS socket underlying the HTTP/2 transport for
	// this backup session. Close() uses it to force-terminate the connection
	// so PBS releases the writer / snapshot lock immediately, without waiting
	// on OS TCP keepalive (10+ min) — http2.Transport.CloseIdleConnections()
	// alone is a no-op while streams are open.
	activeConn   net.Conn
	activeConnMu sync.Mutex
}

// activeClients tracks every PBSClient currently holding an open HTTP/2
// session. A signal handler or Wails shutdown hook calls CloseAllActive
// to release writers before the process exits; otherwise the PBS-side
// snapshot lock lingers and blocks the next verify run on that group.
var (
	activeClientsMu sync.Mutex
	activeClients   = map[*PBSClient]struct{}{}
)

func registerActive(c *PBSClient) {
	activeClientsMu.Lock()
	activeClients[c] = struct{}{}
	activeClientsMu.Unlock()
}

func unregisterActive(c *PBSClient) {
	activeClientsMu.Lock()
	delete(activeClients, c)
	activeClientsMu.Unlock()
}

// CloseAllActive force-closes every live PBS backup session. Safe to
// call from a signal handler.
func CloseAllActive() {
	activeClientsMu.Lock()
	clients := make([]*PBSClient, 0, len(activeClients))
	for c := range activeClients {
		clients = append(clients, c)
	}
	activeClientsMu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

const PBS_FIXED_CHUNK_SIZE = 4 * 1024 * 1024

var blobCompressedMagic = []byte{49, 185, 88, 66, 111, 182, 163, 127}
var blobUncompressedMagic = []byte{66, 171, 56, 7, 190, 131, 112, 161}

// ManifestBlobName is the well-known name PBS and the official client use
// for the snapshot manifest. It is never encrypted — see UploadManifest.
const ManifestBlobName = "index.json.blob"

type SnapshotsResp struct {
	Data []BackupManifest `json:"data"`
}

// buildTLSConfig returns the TLS config used by ALL PBS HTTP clients. When a
// fingerprint is configured (pbs.CertFingerPrint != ""), CA validation is skipped but the peer
// certificate's SHA-256 is pinned to CertFingerPrint; otherwise normal CA
// validation applies. Factored so every client that sends the auth token pins
// identically (audit H-02) — ListSnapshots/TestConnection previously skipped the
// pin (InsecureSkipVerify with no fingerprint check), accepting any certificate.
func (pbs *PBSClient) buildTLSConfig() *tls.Config {
	// Pinning is keyed on whether a fingerprint is configured, NOT on pbs.Insecure:
	// some callers (e.g. nbd) set Insecure with no fingerprint and must keep working
	// (skip verification), and a fingerprint-less pin would compare against "" and
	// fail every connection.
	if pbs.CertFingerPrint == "" {
		// No fingerprint: trust the system CA, unless explicitly insecure.
		return &tls.Config{InsecureSkipVerify: pbs.Insecure}
	}
	// Fingerprint configured: skip CA validation but pin the peer cert's SHA-256.
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificates presented by the peer")
			}
			peerCert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("failed to parse certificate: %v", err)
			}
			expectedFingerprint := strings.ReplaceAll(pbs.CertFingerPrint, ":", "")
			sum := sha256.Sum256(peerCert.Raw)
			calculatedFingerprint := hex.EncodeToString(sum[:])
			if !strings.EqualFold(calculatedFingerprint, expectedFingerprint) {
				return fmt.Errorf("certificate fingerprint does not match (expected %s, got %s)", expectedFingerprint, calculatedFingerprint)
			}
			return nil
		},
	}
}

// FetchServerFingerprint dials baseURL over TLS without verifying the chain and
// returns the leaf certificate's SHA-256 fingerprint as colon-separated uppercase
// hex pairs (AA:BB:...), the format PBS displays and ValidateFingerprint accepts.
// Used for trust-on-first-use pinning when a server presents a self-signed
// certificate and no fingerprint is configured yet (audit H-02 made CA validation
// strict, so such servers now fail TestConnection with x509 unknown-authority).
// It performs no authentication and sends no token — it only inspects the
// certificate the server presents.
func FetchServerFingerprint(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "8007")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	// #nosec G402 -- intentionally unverified: we only read the presented cert to
	// compute its fingerprint for the user to pin (TOFU); no data is exchanged.
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return "", fmt.Errorf("failed to reach server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("server presented no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(pairs, ":"), nil
}

// percentEncodeCookie percent-encodes a value for use in the PBSAuthCookie
// cookie value, matching PBS's own Rust client (percent-encoding crate,
// DEFAULT_ENCODE_SET): only RFC 3986 unreserved chars (A-Za-z0-9 - . _ ~)
// are left as-is; everything else (":" "@" "/" "+" "=" ...) is %XX-encoded.
func percentEncodeCookie(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// setAuth applies the active PBS credentials to req. When a ticket is present
// it sends "Cookie: PBSAuthCookie=<ticket>" (+ CSRFPreventionToken when set);
// otherwise it falls back to the classic "Authorization: PBSAPIToken=..." header.
func (pbs *PBSClient) setAuth(req *http.Request) {
	if pbs.Ticket != "" {
		req.Header.Set("Cookie", "PBSAuthCookie="+percentEncodeCookie(pbs.Ticket))
		if pbs.CSRFToken != "" {
			req.Header.Set("CSRFPreventionToken", pbs.CSRFToken)
		}
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("PBSAPIToken=%s:%s", pbs.AuthID, pbs.Secret))
}

// authHeaderLines returns the raw "Key: Value" credential header lines for the
// manual (pre-hijacked) HTTP/1.1 upgrade request built in Connect.
func (pbs *PBSClient) authHeaderLines() []string {
	if pbs.Ticket != "" {
		lines := []string{"Cookie: PBSAuthCookie=" + percentEncodeCookie(pbs.Ticket)}
		if pbs.CSRFToken != "" {
			lines = append(lines, "CSRFPreventionToken: "+pbs.CSRFToken)
		}
		return lines
	}
	return []string{fmt.Sprintf("Authorization: PBSAPIToken=%s:%s", pbs.AuthID, pbs.Secret)}
}

// redactRequest masks the credential from a fully-built raw request string so
// it can be logged without leaking the ticket / CSRF token / API secret.
func (pbs *PBSClient) redactRequest(s string) string {
	if pbs.Ticket != "" {
		s = strings.ReplaceAll(s, "PBSAuthCookie="+percentEncodeCookie(pbs.Ticket), "PBSAuthCookie=<redacted>")
		if pbs.CSRFToken != "" {
			s = strings.ReplaceAll(s, "CSRFPreventionToken: "+pbs.CSRFToken, "CSRFPreventionToken: <redacted>")
		}
		return s
	}
	return strings.Replace(s,
		fmt.Sprintf("PBSAPIToken=%s:%s", pbs.AuthID, pbs.Secret),
		fmt.Sprintf("PBSAPIToken=%s:<redacted>", pbs.AuthID), 1)
}

// ticketResp mirrors the JSON returned by POST /api2/json/access/ticket.
type ticketResp struct {
	Data struct {
		Ticket              string `json:"ticket"`
		CSRFPreventionToken string `json:"CSRFPreventionToken"`
		Username            string `json:"username"`
	} `json:"data"`
}

// ObtainTicket exchanges pbs.Username + pbs.Password for a PBS ticket via
// POST /api2/json/access/ticket and stores it (plus the CSRF token) so that
// subsequent requests authenticate with the PBSAuthCookie cookie. Call this
// once after construction when using username/password instead of an API token.
func (pbs *PBSClient) ObtainTicket() error {
	if pbs.Username == "" || pbs.Password == "" {
		return fmt.Errorf("username and password are required to obtain a ticket")
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: pbs.buildTLSConfig()},
	}
	form := url.Values{}
	form.Set("username", pbs.Username)
	form.Set("password", pbs.Password)
	req, err := http.NewRequest("POST", pbs.BaseURL+"/api2/json/access/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create ticket request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ticket request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ticket login failed: HTTP %d - %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr ticketResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("failed to parse ticket response: %w", err)
	}
	if tr.Data.Ticket == "" {
		return fmt.Errorf("ticket login returned no ticket: %s", strings.TrimSpace(string(body)))
	}
	pbs.Ticket = tr.Data.Ticket
	pbs.CSRFToken = tr.Data.CSRFPreventionToken
	return nil
}

// serverSupportsNamespace is gone on purpose.
//
// It used to gate the `ns` parameter on the server major version being "2",
// on the assumption that PBS 3.x dropped namespaces. That assumption was wrong
// and the gate silently disabled namespaces on exactly the servers that do
// support them (verified against PBS 4.2.0, which serves
// /api2/json/admin/datastore/{store}/namespace and accepts `ns` on the backup
// upgrade). Namespaces must simply exist on the server; see
// NamespaceHint in missingNamespaceErr.

func (pbs *PBSClient) ListSnapshots() ([]BackupManifest, error) {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: pbs.buildTLSConfig()},
	}

	ret := make([]BackupManifest, 0)
	var r SnapshotsResp
	params := url.Values{}
	if pbs.Namespace != "" {
		params.Add("ns", pbs.Namespace)
	}
	fullURL := fmt.Sprintf("%s/api2/json/admin/datastore/%s/snapshots?%s", pbs.BaseURL, pbs.Datastore, params.Encode())

	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return ret, err
	}
	req.Header.Set("Accept", "application/json")
	pbs.setAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return ret, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return ret, fmt.Errorf("HTTP error: %d - %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return ret, err
	}
	// sort snapshots by date, newest first (then backup id/type) so -list is
	// deterministic
	slices.SortFunc(r.Data, func(a, b BackupManifest) int {
		if a.BackupTime != b.BackupTime {
			if a.BackupTime > b.BackupTime {
				return -1
			}
			return 1
		}
		if a.BackupID != b.BackupID {
			if a.BackupID < b.BackupID {
				return -1
			}
			return 1
		}
		if a.BackupType != b.BackupType {
			if a.BackupType < b.BackupType {
				return -1
			}
			return 1
		}
		return 0
	})
	return r.Data, nil

}

func (pbs *PBSClient) CreateFixedIndex(fic FixedIndexCreateReq) (uint64, error) {
	jd, err := json.Marshal(fic)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", pbs.BaseURL+"/fixed_index", bytes.NewBuffer(jd))
	if err != nil {
		return 0, err
	}
	pbs.setAuth(req)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return 0, err
	}

	if resp2.StatusCode != http.StatusOK {
		resp1, _ := io.ReadAll(resp2.Body)
		fmt.Println("Error making request:", string(resp1), string(resp2.Proto))
		return 0, fmt.Errorf("request failed: %s %s", string(resp1), resp2.Proto)
	}

	resp1, err := io.ReadAll(resp2.Body)
	if err != nil {
		return 0, err
	}
	var R IndexCreateResp
	err = json.Unmarshal(resp1, &R)
	if err != nil {
		fmt.Println("Error parsing JSON:", err)
		return 0, err
	}
	fmt.Println("Writer id: ", R.WriterID)
	defer func() { _ = resp2.Body.Close() }()
	f := File{
		CryptMode: pbs.defaultCryptMode(),
		Csum:      "",
		Filename:  fic.ArchiveName,
		Size:      0,
	}
	pbs.Manifest.Files = append(pbs.Manifest.Files, f)
	pbs.WritersManifest[uint64(R.WriterID)] = len(pbs.Manifest.Files) - 1
	return uint64(R.WriterID), nil

}

func (pbs *PBSClient) AssignFixedChunks(writerid uint64, digests []string, offsets []uint64) error {
	indexput := &IndexPutReq{
		WriterID:   writerid,
		DigestList: digests,
		OffsetList: offsets,
	}

	jsondata, err := json.Marshal(indexput)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", pbs.BaseURL+"/fixed_index", bytes.NewBuffer(jsondata))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS assign fixed chunks failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	return nil
}

func (pbs *PBSClient) CloseFixedIndex(writerid uint64, checksum string, totalsize uint64, chunkcount uint64) error {
	finishreq := &IndexCloseReq{
		WriterID:   writerid,
		CheckSum:   checksum,
		Size:       totalsize,
		ChunkCount: chunkcount,
	}
	jsonpayload, err := json.Marshal(finishreq)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", pbs.BaseURL+"/fixed_close", bytes.NewBuffer(jsonpayload))
	if err != nil {
		return err
	}
	pbs.setAuth(req)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS close fixed index failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	f := &pbs.Manifest.Files[pbs.WritersManifest[writerid]]
	f.Csum = checksum
	f.Size = int64(totalsize)

	return nil
}

// redactedHeaders returns a copy of h with credential values masked, so
// request headers can be logged without leaking the PBS API token, ticket
// cookie, or CSRF token (audit L-01).
func redactedHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		// Header.Set canonicalizes keys ("CSRFPreventionToken" is stored as
		// "Csrfpreventiontoken"), so compare canonical forms or the CSRF token
		// would be printed in clear.
		switch http.CanonicalHeaderKey(k) {
		case "Authorization":
			out[k] = []string{"PBSAPIToken=<redacted>"}
		case "Cookie":
			out[k] = []string{"PBSAuthCookie=<redacted>"}
		case http.CanonicalHeaderKey("CSRFPreventionToken"):
			out[k] = []string{"<redacted>"}
		default:
			out[k] = v
		}
	}
	return out
}

func (pbs *PBSClient) CreateDynamicIndex(name string) (uint64, error) {
	fmt.Printf("=== CreateDynamicIndex START ===\n")
	fmt.Printf("Archive name: %s\n", name)
	fmt.Printf("BaseURL: %s\n", pbs.BaseURL)

	// Use json.Marshal to properly escape special characters and ensure valid JSON
	payload := map[string]string{"archive-name": name}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("ERROR: Failed to marshal JSON: %v\n", err)
		return 0, fmt.Errorf("failed to marshal JSON payload: %w", err)
	}

	req, err := http.NewRequest("POST", pbs.BaseURL+"/dynamic_index", bytes.NewBuffer(jsonPayload))
	if err != nil {
		fmt.Printf("ERROR: Failed to create request: %v\n", err)
		return 0, err
	}

	pbs.setAuth(req)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	fmt.Printf("Sending POST request to: %s\n", req.URL.String())
	fmt.Printf("Headers: %+v\n", redactedHeaders(req.Header))

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Printf("ERROR: HTTP request failed: %v\n", err)
		fmt.Printf("ERROR: Error type: %T\n", err)
		return 0, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	fmt.Printf("Response status: %d %s\n", resp2.StatusCode, resp2.Status)
	fmt.Printf("Response proto: %s\n", resp2.Proto)

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		fmt.Printf("ERROR: PBS returned non-200 status\n")
		fmt.Printf("Status: %d\n", resp2.StatusCode)
		fmt.Printf("Body: %s\n", string(bodyBytes))
		return 0, fmt.Errorf("PBS returned HTTP %d: %s", resp2.StatusCode, string(bodyBytes))
	}

	resp1, err := io.ReadAll(resp2.Body)
	if err != nil {
		return 0, err
	}
	var R IndexCreateResp
	err = json.Unmarshal(resp1, &R)
	if err != nil {
		fmt.Println("Error parsing JSON:", err)
		return 0, err
	}
	fmt.Println("Writer id: ", R.WriterID)
	defer func() { _ = resp2.Body.Close() }()
	f := File{
		CryptMode: pbs.defaultCryptMode(),
		Csum:      "",
		Filename:  name,
		Size:      0,
	}
	pbs.Manifest.Files = append(pbs.Manifest.Files, f)
	pbs.WritersManifest[uint64(R.WriterID)] = len(pbs.Manifest.Files) - 1
	return uint64(R.WriterID), nil
}

func (pbs *PBSClient) UploadDynamicUncompressedChunk(writerid uint64, digest string, chunkdata []byte) error {
	return pbs.UploadChunk(writerid, digest, chunkdata, true, false)
}
func (pbs *PBSClient) UploadFixedUncompressedChunk(writerid uint64, digest string, chunkdata []byte) error {
	return pbs.UploadChunk(writerid, digest, chunkdata, false, false)
}
func (pbs *PBSClient) UploadDynamicCompressedChunk(writerid uint64, digest string, chunkdata []byte) error {
	return pbs.UploadChunk(writerid, digest, chunkdata, true, true)
}
func (pbs *PBSClient) UploadFixedCompressedChunk(writerid uint64, digest string, chunkdata []byte) error {
	return pbs.UploadChunk(writerid, digest, chunkdata, false, true)
}

// ChunkDigest returns the digest a chunk's *plaintext* must be published
// under. With encryption enabled that is sha256(plaintext || id_key), not a
// bare sha256 — the chunk store and both index formats key chunks by this
// value, so every producer of a digest has to go through here. Without
// encryption it is a plain sha256, i.e. exactly the pre-encryption scheme.
func (pbs *PBSClient) ChunkDigest(chunkdata []byte) [32]byte {
	if pbs.Crypt != nil {
		return pbs.Crypt.ComputeDigest(chunkdata)
	}
	return sha256.Sum256(chunkdata)
}

// ChunkDigestHex is ChunkDigest in the hex form the PBS API and our index
// files expect.
func (pbs *PBSClient) ChunkDigestHex(chunkdata []byte) string {
	if pbs.Crypt != nil {
		return pbs.Crypt.ComputeDigestHex(chunkdata)
	}
	sum := sha256.Sum256(chunkdata)
	return hex.EncodeToString(sum[:])
}

// defaultCryptMode is the crypt-mode a freshly created index archive entry
// gets. UploadManifest re-derives it for every archive before signing, so
// this is only about keeping the in-memory manifest honest between creation
// and upload.
func (pbs *PBSClient) defaultCryptMode() string {
	if pbs.Crypt != nil {
		return CryptModeEncrypt
	}
	return CryptModeNone
}

// zstdLevel maps the configured compression level onto the zstd encoder.
func (pbs *PBSClient) zstdLevel() zstd.EncoderLevel {
	switch pbs.CompressionLevel {
	case CompressionFastest:
		return zstd.SpeedFastest
	case CompressionBetter:
		return zstd.SpeedBetterCompression
	case CompressionBest:
		return zstd.SpeedBestCompression
	default: // CompressionDefault or empty
		return zstd.SpeedDefault
	}
}

func (pbs *PBSClient) UploadChunk(writerid uint64, digest string, chunkdata []byte, dynamic bool, compressed bool) error {
	var outBuffer []byte
	var err error

	if pbs.Crypt != nil {
		// Encrypted chunk: MAGIC || crc32 || IV || TAG || AES-256-GCM(data),
		// where data is the zstd-compressed plaintext (ENCR_COMPR, what
		// proxmox-backup-client writes by default) when compression is on
		// and helps, the plaintext otherwise.
		//
		// `size` stays the *plaintext* length and `encoded-size` the framed
		// length, matching backup_writer.rs's "size": chunk_len.
		if compressed {
			outBuffer, err = pbs.Crypt.EncodeEncryptedCompressed(chunkdata, pbs.zstdLevel())
		} else {
			outBuffer, err = pbs.Crypt.EncodeEncrypted(chunkdata)
		}
		if err != nil {
			return err
		}
	} else if compressed {
		outBuffer = append(outBuffer, blobCompressedMagic...)
		compressedData := make([]byte, 0)

		w, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(pbs.zstdLevel()))
		compressedData = w.EncodeAll(chunkdata, compressedData)
		checksum := crc32.Checksum(compressedData, crc32.IEEETable)
		//binary.Write(outBuffer, binary.LittleEndian, checksum)
		outBuffer = binary.LittleEndian.AppendUint32(outBuffer, checksum)

		//fmt.Printf("Appended checksum %08x , len: %d\n", checksum, len(outBuffer))

		outBuffer = append(outBuffer, compressedData...)

		if len(compressedData) > len(chunkdata) {
			return pbs.UploadChunk(writerid, digest, chunkdata, dynamic, false)
		}
	} else {
		outBuffer = append(outBuffer, blobUncompressedMagic...)
		checksum := crc32.Checksum(chunkdata, crc32.IEEETable)
		outBuffer = binary.LittleEndian.AppendUint32(outBuffer, checksum)
		outBuffer = append(outBuffer, chunkdata...)
	}

	//fmt.Printf("Compressed: %d , Orig: %d\n", len(compressedData), len(chunkdata))

	q := &url.Values{}
	q.Add("digest", digest)
	q.Add("encoded-size", fmt.Sprintf("%d", len(outBuffer)))
	q.Add("size", fmt.Sprintf("%d", len(chunkdata)))
	q.Add("wid", fmt.Sprintf("%d", writerid))
	suburl := "/dynamic_chunk?"
	if !dynamic {
		suburl = "/fixed_chunk?"
	}
	req, err2 := http.NewRequest("POST", pbs.BaseURL+suburl+q.Encode(), bytes.NewBuffer(outBuffer))
	if err2 != nil {
		fmt.Println("Error making request:", err2)
		return err2
	}
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}

	if resp2.StatusCode != http.StatusOK {
		resp1, _ := io.ReadAll(resp2.Body)
		fmt.Println("Error making request:", string(resp1), string(resp2.Proto))
		return fmt.Errorf("Error making request: %s %s", string(resp1), string(resp2.Proto))
	}

	return nil
}

func (pbs *PBSClient) AssignDynamicChunks(writerid uint64, digests []string, offsets []uint64) error {
	indexput := &IndexPutReq{
		WriterID:   writerid,
		DigestList: digests,
		OffsetList: offsets,
	}

	jsondata, err := json.Marshal(indexput)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", pbs.BaseURL+"/dynamic_index", bytes.NewBuffer(jsondata))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS assign chunks failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	return nil
}

func (pbs *PBSClient) CloseDynamicIndex(writerid uint64, checksum string, totalsize uint64, chunkcount uint64) error {
	finishreq := &IndexCloseReq{
		WriterID:   writerid,
		CheckSum:   checksum,
		Size:       totalsize,
		ChunkCount: chunkcount,
	}
	jsonpayload, err := json.Marshal(finishreq)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", pbs.BaseURL+"/dynamic_close", bytes.NewBuffer(jsonpayload))
	if err != nil {
		return err
	}
	pbs.setAuth(req)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS close dynamic index failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	f := &pbs.Manifest.Files[pbs.WritersManifest[writerid]]
	f.Csum = checksum
	f.Size = int64(totalsize)

	return nil
}

// UploadBlob uploads a top-level blob, encrypting it when the client has a
// CryptConfig (index.json.blob excepted — see UploadManifest).
func (pbs *PBSClient) UploadBlob(name string, data []byte) error {
	return pbs.uploadBlob(name, data, pbs.Crypt != nil)
}

func (pbs *PBSClient) uploadBlob(name string, data []byte, encrypt bool) error {
	var out []byte
	cryptMode := CryptModeNone

	if encrypt && pbs.Crypt != nil {
		var err error
		// Compressed when it helps, as proxmox-backup-client uploads blobs.
		out, err = pbs.Crypt.EncodeEncryptedCompressed(data, pbs.zstdLevel())
		if err != nil {
			return fmt.Errorf("failed to encrypt blob %s: %w", name, err)
		}
		cryptMode = CryptModeEncrypt
	} else {
		out = make([]byte, 0, 12+len(data))
		out = append(out, blobUncompressedMagic...)

		checksum := crc32.ChecksumIEEE(data)
		out = binary.LittleEndian.AppendUint32(out, checksum)
		out = append(out, data...)
	}

	q := &url.Values{}
	q.Add("encoded-size", fmt.Sprintf("%d", len(out)))
	q.Add("file-name", name)

	req, _ := http.NewRequest("POST", pbs.BaseURL+"/blob?"+q.Encode(), bytes.NewBuffer(out))

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return err
	}

	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS upload blob failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	// Manifest csum must match the SHA-256 of the blob file as stored
	// on the datastore — i.e. the encoded buffer (magic + crc32 +
	// payload) we just sent. PBS validates this field on /finish as a
	// strict 64-char hex string; leaving it empty triggers
	// "unable to update manifest blob - Invalid string length".
	//
	// Size must match DataBlob::raw_size() used by PBS verify, which
	// returns raw_data.len() — i.e. the full on-disk file size with
	// the 12-byte DataBlobHeader, not just the inner payload. Using
	// len(data) produces "wrong size (len(data) != len(data)+12)" at
	// verify time even though /finish accepts it.
	//
	// Historically this never blew up because the only UploadBlob
	// caller was UploadManifest, which appends the index.json.blob
	// entry AFTER the manifest JSON has already been serialized — so
	// the stored manifest never referenced itself with a broken csum.
	// As soon as another blob (the NTFS ACL side-car) is uploaded
	// before the manifest is serialized, its broken entry lands in
	// the persisted JSON and /finish rejects it.
	sum := sha256.Sum256(out)
	pbs.Manifest.Files = append(pbs.Manifest.Files, File{
		CryptMode: cryptMode,
		Csum:      hex.EncodeToString(sum[:]),
		Filename:  name,
		Size:      int64(len(out)),
	})

	return nil
}

// DownloadBlob fetches a top-level blob previously written by UploadBlob
// (e.g. the NTFS ACL/attributes side-car) from the current snapshot and
// decodes it back to its raw payload — the inverse of UploadBlob's framing.
// Deliberately a standalone call site (it does not share FetchChunk's code)
// so this newer, less-proven path can't put that already-proven one at risk.
//
// Encrypted blobs are decrypted with pbs.Crypt, which must be populated for
// snapshots taken with `--crypt-mode encrypt`. The digest argument is nil
// here because PBS does not publish a per-blob digest; integrity still rests
// on the DataBlob CRC32 that DecodeEncryptedBlob verifies.
func (pbs *PBSClient) DownloadBlob(name string) ([]byte, error) {
	raw, err := pbs.DownloadToBytes(name)
	if err != nil {
		return nil, err
	}
	if len(raw) < DataBlobHeaderSize {
		return nil, fmt.Errorf("short blob response for %s: %d bytes", name, len(raw))
	}
	if IsEncryptedBlob(raw) && pbs.Crypt == nil {
		return nil, fmt.Errorf("blob %s is encrypted but no encryption key was provided", name)
	}
	return DecodeEncryptedBlob(raw, pbs.Crypt, nil)
}

// UploadManifest serializes, signs and stores index.json.blob.
//
// Following proxmox-backup ("manifests are never encrypted, but include a
// signature"), the manifest blob itself is always uploaded unencrypted —
// otherwise nobody could read the key fingerprint and file list needed to
// bootstrap decryption. With pbs.Crypt set we additionally
//
//   - mark every archive we uploaded as crypt-mode "encrypt" so the server
//     and the official client know its chunks need the key, and
//   - sign the manifest, storing the hex HMAC in "signature" and the key's
//     colon-hex fingerprint in "unprotected"."key-fingerprint".
//
// Both are computed over the canonical JSON of the manifest with
// "signature" and "unprotected" removed — byte-for-byte what
// proxmox-backup's json_signature() feeds to its auth tag.
func (pbs *PBSClient) UploadManifest() error {
	if pbs.Crypt != nil {
		for i := range pbs.Manifest.Files {
			// index.json.blob's own entry is appended by uploadBlob below,
			// i.e. after this loop and after serialization — matching
			// proxmox-backup, which never lists the manifest in files[].
			if pbs.Manifest.Files[i].Filename == ManifestBlobName {
				continue
			}
			pbs.Manifest.Files[i].CryptMode = CryptModeEncrypt
		}
	}

	manifestBin, err := json.Marshal(pbs.Manifest)
	if err != nil {
		return err
	}

	if pbs.Crypt != nil {
		signed, err := canonicalJSONBytes(manifestBin)
		if err != nil {
			return fmt.Errorf("failed to canonicalize manifest for signing: %w", err)
		}
		pbs.Manifest.Signature = hex.EncodeToString(pbs.Crypt.ComputeAuthTag(signed))
		fingerprint := pbs.Crypt.Fingerprint()
		pbs.Manifest.Unprotected.KeyFingerprint = &fingerprint

		// Re-marshal: the signature and the fingerprint only become visible
		// to the official client through the stored bytes.
		manifestBin, err = json.Marshal(pbs.Manifest)
		if err != nil {
			return err
		}
	}

	return pbs.uploadBlob(ManifestBlobName, manifestBin, false)
}

func (pbs *PBSClient) Finish() error {
	req, err := http.NewRequest("POST", pbs.BaseURL+"/finish", nil)
	if err != nil {
		return fmt.Errorf("failed to create finish request: %w", err)
	}
	pbs.setAuth(req)

	resp2, err := pbs.Client.Do(req)
	if err != nil {
		return fmt.Errorf("finish request failed: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	// CRITICAL: Check HTTP status code
	if resp2.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("PBS finish failed: HTTP %d - %s", resp2.StatusCode, string(bodyBytes))
	}

	// Session committed. Tear the H2 connection down right away so PBS
	// drops the worker task and releases the snapshot lock — otherwise
	// the task UI keeps it active until the process exits or TCP
	// keepalive reaps the socket (~16 min), which blocks the next
	// scheduled run with "retry lock error".
	pbs.Close()

	return nil
}

// TestConnection performs a real HTTP request to verify PBS connectivity
// Returns error if hostname unreachable, credentials invalid, or datastore inaccessible
func (pbs *PBSClient) TestConnection() error {
	// Same TLS policy as the backup session: pin the fingerprint when configured,
	// otherwise CA-validate. The old `|| CertFingerPrint == ""` skipped verification
	// entirely when no fingerprint was set, accepting any certificate (audit H-02).
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: pbs.buildTLSConfig(),
		},
	}

	// Test endpoint: GET datastore status (requires auth + datastore access)
	testURL := fmt.Sprintf("%s/api2/json/admin/datastore/%s/status", pbs.BaseURL, pbs.Datastore)

	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add PBS authentication (ticket cookie or API token, see setAuth)
	pbs.setAuth(req)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check HTTP status
	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed: invalid credentials")
	}
	if resp.StatusCode == 403 {
		return fmt.Errorf("access denied: check datastore permissions")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server error: HTTP %d", resp.StatusCode)
	}

	return nil
}

func (pbs *PBSClient) Connect(reader bool, backuptype string) {
	pbs.WritersManifest = make(map[uint64]int)
	pbs.SkippedFiles = []string{}  // Reset skipped files for new backup
	pbs.ExcludedFiles = []string{} // Reset excluded files for new backup
	pbs.ReadErrors = []string{}    // Reset read errors for new backup
	// CRITICAL: Reset Manifest.Files for each new session. Otherwise files from
	// previous Connect() calls leak into the next session's manifest, causing PBS
	// to reject the manifest (files reference UUIDs from abandoned sessions).
	pbs.Manifest.Files = nil
	// Same pinning policy as ListSnapshots/TestConnection (audit H-02), factored
	// into buildTLSConfig so all PBS clients verify the certificate identically.
	pbs.TLSConfig = *pbs.buildTLSConfig()
	if !reader {
		pbs.Manifest.BackupTime = time.Now().Unix()
	}
	pbs.Manifest.BackupType = backuptype
	if pbs.Manifest.BackupID == "" {
		hostname, _ := os.Hostname()
		pbs.Manifest.BackupID = hostname
	}

	// Close any existing HTTP/2 connections from previous backups (successful or failed)
	// This prevents reusing stale/broken connections that might cause 400 errors
	if pbs.Client.Transport != nil {
		// Close all idle connections first
		pbs.Client.CloseIdleConnections()

		// If it's an http2.Transport, force close it
		if h2Transport, ok := pbs.Client.Transport.(*http2.Transport); ok {
			h2Transport.CloseIdleConnections()
		}

		// Nil out the transport to ensure it's garbage collected
		pbs.Client.Transport = nil
	}

	// Track whether DialTLSContext has already been called for this session.
	// If HTTP/2 tries to reconnect (connection dropped mid-backup), we must
	// fail rather than silently create a new PBS session with stale writer IDs.
	var dialCalled atomic.Bool

	// Create completely new client with fresh HTTP/2 transport.
	// ReadIdleTimeout + PingTimeout cause the transport to send PING frames on idle
	// connections and detect dead connections quickly. Without this, a silently
	// dropped connection (firewall, NAT timeout, etc.) only manifests when the
	// next chunk upload fails, wasting minutes of backup time.
	pbs.Client = http.Client{
		Transport: &http2.Transport{
			ReadIdleTimeout: 30 * time.Second,
			PingTimeout:     15 * time.Second,

			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {

				// Prevent HTTP/2 auto-reconnect from creating orphaned PBS sessions.
				// If the connection drops mid-backup, a reconnect would start a new PBS
				// session with the same backup-time but fresh writer IDs on the server,
				// while the client still holds stale writer IDs → "writer not registered".
				if !dialCalled.CompareAndSwap(false, true) {
					return nil, fmt.Errorf("connection lost: PBS session cannot be resumed (reconnect blocked to prevent orphaned sessions)")
				}

				//This is one of the trickiest parts, GO http2 library does not support starting with http1 and upgrading to 2 after
				//So to achieve that the function to create SSL socket has been hijacked here
				//Here an http 1.1 request to authenticate, start the backup and require upgrade to HTTP2 is done then the socket is passed to
				// http2.Transport handler
				conn, err := tls.Dial(network, addr, &pbs.TLSConfig)
				if err != nil {
					return nil, err
				}
				// Stash the raw socket so Close() can force-terminate the
				// session on shutdown even while an H2 stream is in flight.
				pbs.activeConnMu.Lock()
				pbs.activeConn = conn
				pbs.activeConnMu.Unlock()
				q := &url.Values{}
				q.Add("backup-time", fmt.Sprintf("%d", pbs.Manifest.BackupTime))
				q.Add("backup-type", pbs.Manifest.BackupType)
				q.Add("store", pbs.Datastore)
				if pbs.Namespace != "" {
					q.Add("ns", pbs.Namespace)
				}

				q.Add("backup-id", pbs.Manifest.BackupID)
				fmt.Println(q.Encode())
				//q.Add("debug", "1")

				// Build and log the full HTTP request
				var requestLines []string
				if !reader {
					requestLines = append(requestLines, "GET /api2/json/backup?"+q.Encode()+" HTTP/1.1")
				} else {
					requestLines = append(requestLines, "GET /api2/json/reader?"+q.Encode()+" HTTP/1.1")
				}
				requestLines = append(requestLines, "Host: "+addr)
				requestLines = append(requestLines, pbs.authHeaderLines()...)
				if !reader {
					requestLines = append(requestLines, "Upgrade: proxmox-backup-protocol-v1")
				} else {
					requestLines = append(requestLines, "Upgrade: proxmox-backup-reader-protocol-v1")
				}
				requestLines = append(requestLines, "Connection: Upgrade")

				fullRequest := strings.Join(requestLines, "\r\n") + "\r\n\r\n"
				// Redact the credential before logging the raw request —
				// fullRequest carries the API token or the PBSAuthCookie ticket.
				redactedRequest := pbs.redactRequest(fullRequest)
				fmt.Printf("=== SENDING HTTP REQUEST TO PBS ===\n%s=== END REQUEST ===\n", redactedRequest)

				// Send the request
				if _, err := conn.Write([]byte(fullRequest)); err != nil {
					return nil, fmt.Errorf("send upgrade request: %w", err)
				}
				fmt.Print("Reading response to upgrade...\n")
				buf := make([]byte, 0)
				for !strings.HasSuffix(string(buf), "\r\n\r\n") && !strings.HasSuffix(string(buf), "\n\n") {
					//fmt.Println(buf)
					b2 := make([]byte, 1)
					nbytes, err := conn.Read(b2)
					if err != nil || nbytes == 0 {
						fmt.Println("Connection unexpectedly closed")
						return nil, err
					}
					buf = append(buf, b2[:nbytes]...)

					//fmt.Println(string(b2))
				}
				fmt.Printf("=== RECEIVED HTTP RESPONSE FROM PBS ===\n%s\n=== END RESPONSE ===\n", string(buf))

				lines := strings.Split(string(buf), "\n")

				if len(lines) > 0 {
					toks := strings.Split(lines[0], " ")
					if len(toks) > 1 && toks[1] != "101" {
						statusCode := strings.Join(toks[1:], " ")
						fmt.Printf("ERROR: PBS rejected upgrade with status: %s\n", statusCode)
						fmt.Printf("Response headers:\n%s\n", string(buf))

						// Read the response body (JSON error details) based on Content-Length
						var contentLength int
						for _, line := range lines {
							line = strings.TrimSpace(line)
							if strings.HasPrefix(strings.ToLower(line), "content-length:") {
								_, _ = fmt.Sscanf(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), "%d", &contentLength)
							}
						}
						var responseBody string
						if contentLength > 0 && contentLength < 4096 {
							bodyBuf := make([]byte, contentLength)
							totalRead := 0
							for totalRead < contentLength {
								n, readErr := conn.Read(bodyBuf[totalRead:])
								if readErr != nil || n == 0 {
									break
								}
								totalRead += n
							}
							responseBody = string(bodyBuf[:totalRead])
							fmt.Printf("PBS error body: %s\n", responseBody)
						}

						// A missing namespace is reported as HTTP 404 on
						// the upgrade handshake; give it its own error.
						if pbs.Namespace != "" && namespaceNotFound(responseBody) {
							return nil, &NamespaceNotFoundErr{
								Namespace: pbs.Namespace,
								Datastore: pbs.Datastore,
							}
						}
						return nil, upgradeRejection(lines[0], []byte(responseBody))
					}
				}

				fmt.Printf("Upgraderesp: %s\n", string(buf))
				fmt.Println("Successfully upgraded to HTTP/2.")
				return conn, nil
			},
		},
	}

	registerActive(pbs)
}

// Close force-terminates the HTTP/2 session so PBS releases the backup
// writer and its shared snapshot lock without waiting on TCP keepalive.
// Safe to call multiple times and before Connect.
func (pbs *PBSClient) Close() {
	unregisterActive(pbs)

	pbs.activeConnMu.Lock()
	conn := pbs.activeConn
	pbs.activeConn = nil
	pbs.activeConnMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}

	if pbs.Client.Transport != nil {
		if h2, ok := pbs.Client.Transport.(*http2.Transport); ok {
			h2.CloseIdleConnections()
		} else {
			pbs.Client.CloseIdleConnections()
		}
	}
}

type FIDXHeader struct {
	Magic        [8]byte
	UUID         [16]byte
	CreationTime uint64
	IndexCsum    [32]byte
	Size         uint64
	ChunkSize    uint64
	Padding      [4016]byte
}

func (pbs *PBSClient) DownloadPreviousToBytes(archivename string) ([]byte, error) { //In the future also download to tmp if index is extremely big...
	q := &url.Values{}

	q.Add("archive-name", archivename)

	req, err := http.NewRequest("GET", pbs.BaseURL+"/previous?"+q.Encode(), nil)
	pbs.setAuth(req)
	if err != nil {
		return nil, err
	}
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return nil, err
	}
	defer func() { _ = resp2.Body.Close() }()

	ret, err := io.ReadAll(resp2.Body)

	if err != nil {
		return nil, err
	}

	return ret, nil

}

func (pbs *PBSClient) DownloadToBytes(archivename string) ([]byte, error) { //In the future also download to tmp if index is extremely big...
	q := &url.Values{}

	q.Add("file-name", archivename)

	req, err := http.NewRequest("GET", pbs.BaseURL+"/download?"+q.Encode(), nil)
	pbs.setAuth(req)
	if err != nil {
		return nil, err
	}
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return nil, err
	}
	defer func() { _ = resp2.Body.Close() }()

	ret, err := io.ReadAll(resp2.Body)

	if err != nil {
		return nil, err
	}

	return ret, nil

}

func (pbs *PBSClient) GetKnownSha265FromFIDX(archivename string) (*haxmap.Map[string, bool], error) {
	data, err := pbs.DownloadPreviousToBytes(archivename)
	if err != nil {
		return nil, err
	}
	rdr := bytes.NewReader(data)
	var hdr FIDXHeader
	err = binary.Read(rdr, binary.LittleEndian, &hdr)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(hdr.Magic[:], []byte{47, 127, 65, 237, 145, 253, 15, 205}) {
		return nil, fmt.Errorf("FIDX: Invalid magic %+v", hdr.Magic)
	}
	ret := haxmap.New[string, bool]()
	for i := uint64(0); i < hdr.Size/hdr.ChunkSize; i++ {
		H := make([]byte, 32)
		nbytes, err := rdr.Read(H)
		if err != nil {
			return nil, err
		}
		if nbytes != len(H) {
			return nil, fmt.Errorf("FIDX: Short read")
		}
		ret.Set(hex.EncodeToString(H), true)
	}
	return ret, nil

}

func (pbs *PBSClient) GetChunkData(digest string) ([]byte, error) {
	q := &url.Values{}

	q.Add("digest", digest)

	req, err := http.NewRequest("GET", pbs.BaseURL+"/chunk?"+q.Encode(), nil)
	pbs.setAuth(req)
	if err != nil {
		return nil, err
	}
	resp2, err := pbs.Client.Do(req)
	if err != nil {
		fmt.Println("Error making request:", err)
		return nil, err
	}
	defer func() { _ = resp2.Body.Close() }()

	ret, err := io.ReadAll(resp2.Body)

	if err != nil {
		return nil, err
	}

	// Guard against a truncated/error body (proxy 502, reset mid-restore): the
	// blob magic + CRC header is 12 bytes, so ret[:8]/ret[12:] would panic.
	if len(ret) < 12 {
		return nil, fmt.Errorf("short chunk response for %s: %d bytes", digest, len(ret))
	}

	if slices.Equal(ret[:8], blobUncompressedMagic) {
		return ret[12:], nil
	} else if slices.Equal(ret[:8], blobCompressedMagic) {
		// zstd.NewReader(nil), not NewReader(bytes.NewReader(...)): decoding
		// below is done entirely via DecodeAll on the byte slice, so the
		// decoder is never Read() from. Passing a real io.Reader here (as
		// this used to) starts klauspost/compress/zstd's background
		// streaming-decode goroutines (startStreamDecoder), which then sit
		// parked forever on a channel receive waiting for a Read() call that
		// never comes — confirmed 2026-09-24 via a SIGQUIT goroutine dump of
		// a real hung restore: two goroutines blocked in exactly
		// zstd.(*Decoder).startStreamDecoder for 8+ minutes. A restore fetches
		// thousands of chunks, each constructing (and Close()ing) one of
		// these, so this leaked/wedged a small fraction of the time — the
		// exact "stalls at a different point every time, no error, nothing
		// in the retry/reconnect logs" symptom chased most of this session.
		// The fetch/retry/reconnect machinery in nbd/fidxserver.go and
		// DIDXReaderAt never had a chance against this: by the time it hangs,
		// the network read is long done, so no context timeout on the HTTP
		// request can touch it.
		dec, err := zstd.NewReader(nil)

		if err != nil {
			return nil, err
		}
		defer dec.Close()
		ret2 := make([]byte, 0)
		ret2, err = dec.DecodeAll(ret[12:], ret2)
		if err != nil {
			return nil, err
		}
		return ret2, nil
	} else {
		// Encrypted chunk (or a magic we don't know). DecodeEncryptedBlob
		// owns the IV/TAG/CRC framing and the sha256(plaintext || id_key)
		// digest check; it needs pbs.Crypt to be populated, which the
		// restore-side callers arrange from the snapshot key file.
		if IsEncryptedBlob(ret) && pbs.Crypt == nil {
			return nil, fmt.Errorf("chunk %s is encrypted but no encryption key was provided", digest)
		}
		return DecodeEncryptedBlob(ret, pbs.Crypt, nil)
	}

}
