package jobapi

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// The server decrypts with the openssl command line (ServerMonitor.DecryptAES256): what
// Encrypt produces must come back through that exact command.
func TestEncryptIsReadByOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not installed")
	}
	secret := "p@ss word'\"1"
	plain := `{"server":"pg1.pgtest.svc.cloud18:5432","secret":"p@ss word'\"1"}`
	enc, err := Encrypt([]byte(plain), secret)
	if err != nil {
		t.Fatal(err)
	}
	key, iv := sha256.Sum256([]byte(secret)), md5.Sum([]byte(secret))
	cmd := exec.Command("openssl", "aes-256-cbc", "-d", "-a", "-A", "-nosalt", "-K", hex.EncodeToString(key[:]), "-iv", hex.EncodeToString(iv[:]))
	cmd.Stdin = strings.NewReader(enc + "\n")
	out, err := cmd.Output()
	if err != nil || string(out) != plain {
		t.Fatalf("openssl read %q (%v), want %q", out, err, plain)
	}
}

func TestClientCalls(t *testing.T) {
	var paths []string
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		paths, bodies = append(paths, r.Method+" "+r.URL.Path), append(bodies, string(b))
		switch {
		case strings.HasSuffix(r.URL.Path, "/needs/pgdump"):
			w.Write([]byte("true"))
		case strings.HasSuffix(r.URL.Path, "/needs/pgbasebackup"):
			w.WriteHeader(500)
			w.Write([]byte("false"))
		case strings.HasSuffix(r.URL.Path, "/actions/receive-task/pgdump"):
			w.Write([]byte("RECEIVER_PORT=40123\n"))
		case strings.HasSuffix(r.URL.Path, "/actions/receive-task/optimize"):
			w.Write([]byte("NO_RECEIVER_NEEDED"))
		case strings.HasSuffix(r.URL.Path, "/actions/job-state/pgdump/done"):
			w.Write([]byte("ok"))
		case strings.HasSuffix(r.URL.Path, "/secret-login"):
			w.Write([]byte(`{"token":"tok123"}`))
		case strings.HasSuffix(r.URL.Path, "/dbu"):
			if r.Header.Get("Authorization") != "Bearer tok123" {
				http.Error(w, "no token", 401)
				return
			}
			w.Write([]byte("ok"))
		default:
			http.Error(w, "Invalid secret", 401)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Cluster: "pgtest", Server: "pg1.pgtest.svc.cloud18", Port: "5432", Secret: "s3cret", HTTP: srv.Client()}

	if ok, err := c.Needs("pgdump"); err != nil || !ok {
		t.Fatalf("needs pgdump: %v %v", ok, err)
	}
	if ok, err := c.Needs("pgbasebackup"); err != nil || ok {
		t.Fatalf("a task not wanted is false without error: %v %v", ok, err)
	}
	addr, err := c.Receiver("pgdump")
	if err != nil || addr != "127.0.0.1:40123" {
		t.Fatalf("receiver: %q %v", addr, err)
	}
	if addr, err := c.Receiver("optimize"); err != nil || addr != "" {
		t.Fatalf("no receiver needed: %q %v", addr, err)
	}
	if err := c.State("pgdump", "done"); err != nil {
		t.Fatal(err)
	}
	if err := c.State("pgdump", "bogus"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a refused call is an error: %v", err)
	}
	if err := c.ReportUsage([]byte(`{"memMaxBytes":1}`)); err != nil {
		t.Fatalf("usage report with the login token: %v", err)
	}
	if err := c.ReportUsage([]byte(`not json`)); err == nil {
		t.Fatal("a report that is not JSON is refused before any call")
	}
	if last := bodies[len(bodies)-1]; last != `{"memMaxBytes":1}` {
		t.Fatalf("the report is posted as is: %s", last)
	}
	if paths[0] != "POST /api/clusters/pgtest/servers/pg1.pgtest.svc.cloud18/5432/needs/pgdump" {
		t.Fatalf("path: %s", paths[0])
	}
	var body struct{ Data string }
	if err := json.Unmarshal([]byte(bodies[2]), &body); err != nil || body.Data == "" || strings.Contains(bodies[2], "s3cret") {
		t.Fatalf("the body carries only the encrypted data: %s", bodies[2])
	}
}
