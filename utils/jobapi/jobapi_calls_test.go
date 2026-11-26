package jobapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// openBody decrypts a job body the way the server does (OpenJobBody): AES-256-CBC, key
// SHA-256 of the secret, the body's IV, PKCS#7.
func openBody(t *testing.T, raw []byte, secret string) map[string]interface{} {
	t.Helper()
	var b struct{ Data, IV, Key string }
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("job body %s: %v", raw, err)
	}
	enc, _ := base64.StdEncoding.DecodeString(b.Data)
	iv, _ := hex.DecodeString(b.IV)
	key := sha256.Sum256([]byte(secret))
	block, _ := aes.NewCipher(key[:])
	plain := make([]byte, len(enc))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, enc)
	plain = plain[:len(plain)-int(plain[len(plain)-1])]
	var clear map[string]interface{}
	if err := json.Unmarshal(plain, &clear); err != nil {
		t.Fatalf("decrypted %q: %v", plain, err)
	}
	return clear
}

func TestJobsScriptCalls(t *testing.T) {
	bodies := map[string][]byte{}
	auth := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		bodies[key], auth[key] = b, r.Header.Get("Authorization")
		if v := VersionFromUserAgent(r.UserAgent()); v != "v3.1.51-2-gabc" {
			t.Errorf("%s: the call names the CLI version, got %q (%q)", key, v, r.UserAgent())
		}
		p := r.URL.Path
		if !strings.Contains(p, "/clusters/c1/") {
			http.Error(w, "No cluster", 500)
			return
		}
		switch {
		case strings.HasSuffix(p, "/jobs-log-level/mysqldump/INFO"):
			w.Write([]byte("true"))
		case strings.HasSuffix(p, "/jobs-log-level/mysqldump/DEBUG"):
			w.WriteHeader(500)
			w.Write([]byte("false"))
		case strings.HasSuffix(p, "/secret-login"):
			w.Write([]byte(`{"token":"tok"}`))
		case strings.HasSuffix(p, "/write-log/mysqldump"), strings.HasSuffix(p, "/actions/job-state/reseedmariabackup/done"),
			strings.HasSuffix(p, "/actions/send-jobs-upgrade"), strings.HasSuffix(p, "/need-config-refresh"):
			w.Write([]byte("ok"))
		case strings.HasSuffix(p, "/actions/receive-jobs-check"):
			w.Write([]byte("RECEIVER_PORT=40555"))
		case strings.HasSuffix(p, "/actions/receive-task/mysqldump"):
			w.Write([]byte("RECEIVER_PORT=40556 DUMP_ARGS=LS1hbGw= LOCK_DDL=1"))
		case strings.HasSuffix(p, "/config-receiver") && r.Header.Get("Authorization") == "Bearer tok":
			w.Write([]byte(`{"monitor_address":"10.0.0.1","dummy_config_port":"4444"}`))
		case strings.HasSuffix(p, "/config-dummy-sender") && r.Header.Get("Authorization") == "Bearer tok":
			w.Write([]byte(`{"status":"queued","sst_port":"4444","sst_host":"db1"}`))
		default:
			http.Error(w, "no", 403)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Cluster: "c1", Server: "db1", Port: "3306", JobsKey: "jk", Secret: "dbpass", Version: "v3.1.51-2-gabc", HTTP: srv.Client()}
	base := "/api/clusters/c1/servers/db1/3306"

	if ok, err := c.LogLevel("mysqldump", "INFO"); !ok || err != nil {
		t.Fatalf("INFO printed: %v %v", ok, err)
	}
	if ok, err := c.LogLevel("mysqldump", "DEBUG"); ok || err != nil {
		t.Fatalf("DEBUG not printed is false without error: %v %v", ok, err)
	}
	if _, ok := bodies["GET /api/clusters/c1/jobs-log-level/mysqldump/INFO"]; !ok {
		t.Fatalf("log level is the cluster route: %v", bodies)
	}

	text := "line \"one\"\\nline two\n"
	if err := c.Log("mysqldump", "INFO", text); err != nil {
		t.Fatal(err)
	}
	clear := openBody(t, bodies["POST "+base+"/write-log/mysqldump"], "jk")
	if clear["log"] != text || clear["level"] != "INFO" || clear["server"] != "db1:3306" || clear["nonce"] == nil || clear["secret"] != nil {
		t.Fatalf("write-log body: %v", clear)
	}
	if err := c.Log("mysqldump", "INFO", strings.Repeat("x", MaxLogSize+1)); err == nil {
		t.Fatal("a log larger than MaxLogSize is refused")
	}

	restore := json.RawMessage(`{"gtid":"0-1-10","binlogFile":"b.000001"}`)
	if err := c.StateWith("reseedmariabackup", "done", restore); err != nil {
		t.Fatal(err)
	}
	clear = openBody(t, bodies["POST "+base+"/actions/job-state/reseedmariabackup/done"], "jk")
	if r, _ := clear["restore"].(map[string]interface{}); r["gtid"] != "0-1-10" {
		t.Fatalf("job-state carries the restore metadata: %v", clear)
	}
	if err := c.StateWith("reseedmariabackup", "done", json.RawMessage(`{broken`)); err == nil {
		t.Fatal("restore metadata that is not JSON is refused before any call")
	}

	if addr, err := c.JobsCheckReceiver(); err != nil || addr != "127.0.0.1:40555" {
		t.Fatalf("jobs check receiver: %q %v", addr, err)
	}
	if addr, err := c.Receiver("mysqldump"); err != nil || addr != "127.0.0.1:40556 DUMP_ARGS=LS1hbGw= LOCK_DDL=1" {
		t.Fatalf("mysqldump receiver keeps the dump arguments: %q %v", addr, err)
	}
	if err := c.RequestJobsUpgrade(); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.NeedConfigRefresh(); !ok || err != nil {
		t.Fatalf("config refresh: %v %v", ok, err)
	}
	if js, err := c.ConfigReceiver(); err != nil || !strings.Contains(string(js), `"dummy_config_port":"4444"`) {
		t.Fatalf("config receiver: %s %v", js, err)
	}
	if port, err := c.ConfigDummySend(); err != nil || port != "4444" {
		t.Fatalf("config send: %q %v", port, err)
	}
	if auth["POST "+base+"/config-dummy-sender"] != "Bearer tok" {
		t.Fatal("the config send carries the jobs login token")
	}

	c.Cluster = "other"
	if _, err := c.JobsCheckReceiver(); err == nil {
		t.Fatal("a refused call is an error")
	}
}

func TestVersionFromUserAgent(t *testing.T) {
	if v := VersionFromUserAgent("replication-manager-cli/v3.1.51"); v != "v3.1.51" {
		t.Fatal(v)
	}
	if v := VersionFromUserAgent("Wget/1.21"); v != "" {
		t.Fatalf("another client names no CLI version: %q", v)
	}
	if v := VersionFromUserAgent(UserAgentPrefix + strings.Repeat("x", 500)); len(v) != 128 {
		t.Fatalf("bounded: %d", len(v))
	}
}

// Config asks need-config-fetch and stops on its 500 (ErrNoConfigFetch); with force it does not ask
// and downloads the archive whatever the route would answer.
func TestConfigForceSkipsNeedConfigFetch(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		calls[p[strings.LastIndex(p, "/"):]]++
		switch {
		case strings.HasSuffix(p, "/need-config-fetch"):
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No config fetch needed!"))
		case strings.HasSuffix(p, "/secret-login"):
			w.Write([]byte(`{"token":"t0k"}`))
		case strings.HasSuffix(p, "/config"):
			if r.Header.Get("Authorization") != "Bearer t0k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte("archive"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Cluster: "c1", Server: "db1", Port: "3306", JobsKey: "jk", HTTP: srv.Client()}

	var out strings.Builder
	if err := c.Config(&out, false); err != ErrNoConfigFetch || out.Len() != 0 {
		t.Fatalf("not wanted: err=%v written=%q", err, out.String())
	}
	if calls["/need-config-fetch"] != 1 || calls["/config"] != 0 {
		t.Fatalf("the route is asked and the download not made: %v", calls)
	}
	if err := c.Config(&out, true); err != nil || out.String() != "archive" {
		t.Fatalf("forced: err=%v written=%q", err, out.String())
	}
	if calls["/need-config-fetch"] != 1 || calls["/secret-login"] != 1 || calls["/config"] != 1 {
		t.Fatalf("forced: the route is not asked again, the login and the download are made once: %v", calls)
	}
}

// need-config-refresh says "not needed" with a 204 (with a 500 and its text up to v3.1.52):
// no error; any other answer is one.
func TestNeedConfigRefreshAnswers(t *testing.T) {
	answer := http.StatusNoContent
	text := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(answer)
		w.Write([]byte(text))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Cluster: "c1", Server: "db1", Port: "3306", JobsKey: "jk", HTTP: srv.Client()}
	if ok, err := c.NeedConfigRefresh(); ok || err != nil {
		t.Fatalf("not needed (204): %v %v", ok, err)
	}
	answer, text = http.StatusInternalServerError, "500 -No config refresh needed!"
	if ok, err := c.NeedConfigRefresh(); ok || err != nil {
		t.Fatalf("not needed (older replication-manager, 500): %v %v", ok, err)
	}
	answer, text = http.StatusInternalServerError, "500 -No valid server!"
	if _, err := c.NeedConfigRefresh(); err == nil {
		t.Fatal("no valid server is an error, not a no")
	}
	answer, text = http.StatusOK, "200 -Need config refresh!"
	if ok, err := c.NeedConfigRefresh(); !ok || err != nil {
		t.Fatalf("needed: %v %v", ok, err)
	}
	answer, text = http.StatusUnauthorized, "no"
	if _, err := c.NeedConfigRefresh(); err == nil {
		t.Fatal("a 401 is an error, not a no")
	}
}
