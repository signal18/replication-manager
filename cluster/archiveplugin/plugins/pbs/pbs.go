// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alphadose/haxmap"
	"github.com/signal18/replication-manager/cluster/archiveplugin/plugins/pbs/internal/pbscommon"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// archive is one PBS archive: the datastore's namespace, reached with an API token. The
// backup and restore sessions go through pbscommon (the PBS protocol in Go, copied from
// RDEM Systems' NimbusBackupClient, see internal/pbscommon/NOTICE); the datastore
// administration (snapshot list, notes, forget, prune, namespaces) through the REST API.
type archive struct {
	baseURL     string
	datastore   string
	namespace   string
	authID      string
	secret      string
	fingerprint string
	chunkCache  int
	noPurge     bool // the token has no Datastore.Prune: purge and forget are the PBS's job
	noModify    bool // the token has no Datastore.Modify: namespaces are the PBS admin's
	crypt       *pbscommon.CryptConfig
	http        *http.Client
}

// newArchive builds the archive of a store from its settings and the plugin's environment.
func newArchive(st wire.Store) (*archive, error) {
	s := func(name string) string { return strings.TrimSpace(st.Settings[name]) }
	a := &archive{
		baseURL:     strings.TrimRight(s(setBaseURL), "/"),
		datastore:   s(setDatastore),
		namespace:   strings.Trim(s(setNamespace), "/"),
		authID:      s(setAuthID),
		fingerprint: s(setFingerprint),
		secret:      os.Getenv(wire.SecretEnv(setSecret)),
		chunkCache:  16,
	}
	if a.baseURL == "" || a.datastore == "" || a.authID == "" {
		return nil, errors.New("the archive needs its base-url, datastore and auth-id")
	}
	if u, err := url.Parse(a.baseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("base-url %q is not https://<host>[:port]", a.baseURL)
	}
	if !strings.Contains(a.authID, "!") {
		return nil, fmt.Errorf("auth-id %q is not an API token (user@realm!token)", a.authID)
	}
	if a.secret == "" {
		return nil, errors.New("the archive's API token secret is not set")
	}
	var err error
	if a.noPurge, err = boolSetting(s(setNoPurge)); err != nil {
		return nil, fmt.Errorf("%s: %w", setNoPurge, err)
	}
	if a.noModify, err = boolSetting(s(setNoModify)); err != nil {
		return nil, fmt.Errorf("%s: %w", setNoModify, err)
	}
	if c := s(setChunkCache); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > 1024 {
			return nil, fmt.Errorf("chunk-cache %q is not 1-1024", c)
		}
		a.chunkCache = n
	}
	if key := os.Getenv(wire.SecretEnv(setKey)); strings.TrimSpace(key) != "" {
		kc, err := pbscommon.ParseKeyConfig([]byte(key))
		if err != nil {
			return nil, fmt.Errorf("encryption-key: %w", err)
		}
		crypt, err := kc.CryptConfig([]byte(os.Getenv(wire.SecretEnv(setKeyPassword))))
		if err != nil {
			return nil, fmt.Errorf("encryption-key: %w", err)
		}
		a.crypt = crypt
	}
	a.http = &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{TLSClientConfig: a.tlsConfig()}}
	return a, nil
}

// boolSetting reads a bool setting, false when empty.
func boolSetting(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	return strconv.ParseBool(v)
}

// tlsConfig pins the server certificate on its fingerprint when one is set, as pbscommon
// does; otherwise the certificate is verified as usual.
func (a *archive) tlsConfig() *tls.Config {
	if a.fingerprint == "" {
		return &tls.Config{}
	}
	want := strings.ToLower(strings.ReplaceAll(a.fingerprint, ":", ""))
	return &tls.Config{
		InsecureSkipVerify: true, // replaced by the fingerprint check below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("the server presented no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if got := hex.EncodeToString(sum[:]); got != want {
				return fmt.Errorf("server certificate fingerprint %s, the archive expects %s", got, want)
			}
			return nil
		},
	}
}

// location is where the archive is, without secret.
func (a *archive) location() string {
	loc := a.baseURL + "/" + a.datastore
	if a.namespace != "" {
		loc += "/" + a.namespace
	}
	return loc
}

// nsValues adds the archive's namespace to q, none for the datastore's root.
func (a *archive) nsValues(q url.Values) url.Values {
	if q == nil {
		q = url.Values{}
	}
	if a.namespace != "" {
		q.Set("ns", a.namespace)
	}
	return q
}

// api calls the datastore API: the query in the URL for GET and DELETE, a form body for
// POST and PUT; out receives the response's "data".
func (a *archive) api(method, path string, q url.Values, out any) error {
	u := a.baseURL + "/api2/json/admin/datastore/" + url.PathEscape(a.datastore) + path
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
	} else {
		body = strings.NewReader(q.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PBSAPIToken="+a.authID+":"+a.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &apiError{status: resp.StatusCode, msg: fmt.Sprintf("%s %s: HTTP %d %s", method, path, resp.StatusCode, msg)}
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// apiError is a refusal of the PBS API, with its HTTP status.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// session is the pbscommon client of one backup or restore session of the group
// type/backupID.
func (a *archive) session(backupID string, backupTime int64) *pbscommon.PBSClient {
	return &pbscommon.PBSClient{
		BaseURL:         a.baseURL,
		CertFingerPrint: a.fingerprint,
		AuthID:          a.authID,
		Secret:          a.secret,
		Datastore:       a.datastore,
		Namespace:       a.namespace,
		Crypt:           a.crypt,
		Manifest:        pbscommon.BackupManifest{BackupID: backupID, BackupTime: backupTime, BackupType: backupType},
	}
}

// chunkStream cuts one archive stream into content-defined chunks, uploads those the
// previous snapshot of the group does not hold, and closes the dynamic index. Adapted from
// NimbusBackupClient's directorybackup (GPL-3.0, see internal/pbscommon/NOTICE).
type chunkStream struct {
	client  *pbscommon.PBSClient
	wrid    uint64
	known   *haxmap.Map[string, bool]
	chunker pbscommon.Chunker
	digests hash.Hash
	current []byte
	pos     uint64
	count   uint64
	ids     []string
	offsets []uint64
	added   int
	reused  int
}

func newChunkStream(client *pbscommon.PBSClient, wrid uint64, known *haxmap.Map[string, bool]) *chunkStream {
	c := &chunkStream{client: client, wrid: wrid, known: known, digests: sha256.New()}
	c.chunker = pbscommon.Chunker{}
	c.chunker.New(4 << 20)
	return c
}

// write takes the next bytes of the stream.
func (c *chunkStream) write(b []byte) error {
	for {
		cut := c.chunker.Scan(b)
		if cut == 0 {
			c.current = append(c.current, b...)
			return nil
		}
		c.current = append(c.current, b[:cut]...)
		if err := c.emit(); err != nil {
			return err
		}
		b = b[cut:]
	}
}

// emit uploads the current chunk unless known, and records it in the index.
func (c *chunkStream) emit() error {
	digest := c.client.ChunkDigest(c.current)
	id := hex.EncodeToString(digest[:])
	if _, found := c.known.GetOrSet(id, true); !found {
		if err := c.client.UploadDynamicCompressedChunk(c.wrid, id, c.current); err != nil {
			return fmt.Errorf("upload chunk %s: %w", id, err)
		}
		c.added++
	} else {
		c.reused++
	}
	end := c.pos + uint64(len(c.current))
	if err := binary.Write(c.digests, binary.LittleEndian, end); err != nil {
		return err
	}
	c.digests.Write(digest[:])
	c.offsets = append(c.offsets, c.pos)
	c.ids = append(c.ids, id)
	c.pos = end
	c.count++
	c.current = c.current[:0:0]
	return nil
}

// close emits the last chunk, assigns the chunks to the index (128 per request, as PBS
// refuses larger bodies) and closes it.
func (c *chunkStream) close() error {
	if len(c.current) > 0 {
		if err := c.emit(); err != nil {
			return err
		}
	}
	for k := 0; k < len(c.ids); k += 128 {
		e := min(k+128, len(c.ids))
		if err := c.client.AssignDynamicChunks(c.wrid, c.ids[k:e], c.offsets[k:e]); err != nil {
			return fmt.Errorf("assign chunks %d-%d: %w", k, e, err)
		}
	}
	return c.client.CloseDynamicIndex(c.wrid, hex.EncodeToString(c.digests.Sum(nil)), c.pos, c.count)
}
