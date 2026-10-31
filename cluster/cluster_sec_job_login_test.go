// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integrity of this file.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/signal18/replication-manager/utils/state"
)

// scriptJobBody builds a job body with the jobs script's own functions (job_body and the
// helpers it calls), so the test covers what a database server really posts.
func scriptJobBody(t *testing.T, payload, password string) []byte {
	t.Helper()
	return scriptJobBodyKey(t, payload, password, "")
}

// scriptJobBodyKey is scriptJobBody with JOBS_API_KEY set as the ssh environment sets it.
func scriptJobBodyKey(t *testing.T, payload, password, jobsKey string) []byte {
	t.Helper()
	for _, bin := range []string{"bash", "openssl", "sha256sum"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	fns := `src=../share/scripts/dbjobs_new.sh
eval "$(sed -n '/^pad_pkcs7() {/,/^}/p; /^derive_key() {/,/^}/p; /^jobs_key() {/,/^}/p; /^job_body() {/,/^}/p' "$src")"
job_body "$1" "$2"`
	cmd := exec.Command("bash", "-c", fns, "job_body", payload, password)
	cmd.Env = append(cmd.Environ(), "JOBS_API_KEY="+jobsKey)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("job_body: %v", err)
	}
	return bytes.TrimSpace(out)
}

// legacyJobBody is the body of a jobs script older than the one-time format: the IV derived
// from the password, no timestamp, no nonce.
func legacyJobBody(t *testing.T, payload, password string) []byte {
	t.Helper()
	cmd := exec.Command("openssl", "aes-256-cbc", "-a", "-A", "-nosalt", "-K", crypto.GetSHA256Hash(password), "-iv", crypto.GetMD5Hash(password))
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("openssl: %v", err)
	}
	b, _ := json.Marshal(map[string]string{"data": strings.TrimSpace(string(out))})
	return b
}

func jobLoginServer(legacy bool) *ServerMonitor {
	c := &Cluster{Name: "c1", Conf: &config.Config{APISecretLoginLegacy: legacy, APISecretLoginWindow: 300}}
	c.SecurityStateMachine = new(state.StateMachine)
	c.SecurityStateMachine.Init()
	s := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", Pass: "s3cr3t", ClusterGroup: c}
	c.Servers = []*ServerMonitor{s}
	return s
}

// A one-time body of the script is accepted once, its payload intact; the same body again is
// a replay; a body outside the window is refused.
func TestOpenJobBodyOneTime(t *testing.T) {
	s := jobLoginServer(false)
	body := scriptJobBody(t, `{"server":"db1:3306", "secret":"pw"}`, s.Pass)
	now := time.Now()

	data, mode, code, err := s.OpenJobBody(body, now)
	if err != nil || code != 200 || mode != JobKeyDB {
		t.Fatalf("first use: code %d, mode %s, err %v", code, mode, err)
	}
	secret, payload, err := s.DecodeSecret(data)
	if err != nil || secret != "pw" || !secretPayloadHas(payload, "nonce") {
		t.Fatalf("payload: secret %q, %s, err %v", secret, payload, err)
	}
	if _, _, code, err = s.OpenJobBody(body, now); code != 401 || err == nil {
		t.Fatalf("replay must be refused 401, got %d %v", code, err)
	}

	late := scriptJobBody(t, `{"server":"db1:3306", "secret":"pw"}`, s.Pass)
	if _, _, code, _ = s.OpenJobBody(late, now.Add(10*time.Minute)); code != 401 {
		t.Fatalf("a body 10 min old must be refused with a 5 min window, got %d", code)
	}
	empty := scriptJobBody(t, `{}`, s.Pass)
	if _, _, code, err = s.OpenJobBody(empty, now); code != 200 {
		t.Fatalf("an empty object stamped by the script must be valid JSON, got %d %v", code, err)
	}
}

// A legacy body is accepted while api-secret-login-legacy is on and noted for WARN0274;
// refused when it is off.
func TestOpenJobBodyLegacy(t *testing.T) {
	s := jobLoginServer(true)
	body := legacyJobBody(t, `{"server":"db1:3306","secret":"pw"}`, s.Pass)
	for i := 0; i < 2; i++ { // replayable by nature: accepted each time while legacy is on
		if _, mode, code, err := s.OpenJobBody(body, time.Now()); code != 200 || mode != JobKeyLegacy || err != nil {
			t.Fatalf("legacy on: code %d, mode %s, err %v", code, mode, err)
		}
	}
	if atomic.LoadInt64(&s.legacyJobLoginAt) == 0 {
		t.Fatal("a legacy body must be noted")
	}
	s.ClusterGroup.CheckSecretLoginLegacy()
	open := s.ClusterGroup.SecurityStateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0274" || !strings.Contains(open[0].ErrDesc, s.URL) {
		t.Fatalf("WARN0274 on the server: %+v", open)
	}

	s.ClusterGroup.Conf.APISecretLoginLegacy = false
	if _, _, code, _ := s.OpenJobBody(body, time.Now()); code != 401 {
		t.Fatalf("legacy off must refuse 401, got %d", code)
	}
}

// A malformed IV is a bad request; a full nonce store refuses (bounded, F4) until the oldest
// expire.
func TestOpenJobBodyBadIVAndBound(t *testing.T) {
	s := jobLoginServer(false)
	if _, _, code, _ := s.OpenJobBody([]byte(`{"data":"x","iv":"zz"}`), time.Now()); code != 400 {
		t.Fatalf("bad iv must be 400, got %d", code)
	}

	now := time.Now()
	jobNonces.Lock()
	saved := jobNonces.seen
	jobNonces.seen = make(map[string]time.Time, jobNonceMax)
	for i := 0; i < jobNonceMax; i++ {
		jobNonces.seen[fmt.Sprintf("n%d", i)] = now.Add(time.Minute)
	}
	jobNonces.Unlock()
	defer func() { jobNonces.Lock(); jobNonces.seen = saved; jobNonces.Unlock() }()

	body := scriptJobBody(t, `{"server":"db1:3306","secret":"pw"}`, s.Pass)
	if _, _, code, _ := s.OpenJobBody(body, now); code != 503 {
		t.Fatalf("a full store must refuse 503, got %d", code)
	}
	if _, _, code, err := s.OpenJobBody(body, now.Add(2*time.Minute)); code != 200 {
		t.Fatalf("expired nonces are purged when full, got %d %v", code, err)
	}
}

func secretPayloadHas(payload, field string) bool {
	var m map[string]interface{}
	if json.Unmarshal([]byte(payload), &m) != nil {
		return false
	}
	_, ok := m[field]
	return ok
}

// With the cluster's jobs key, the body carries no database password: accepted for the server
// it names, refused for another server of the cluster, refused when the key cannot be derived;
// SecretLoginCheck lets it in without "secret".
func TestOpenJobBodyJobsKey(t *testing.T) {
	s := jobLoginServer(false)
	c := s.ClusterGroup
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	other := &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", Pass: "other", ClusterGroup: c}
	c.Servers = append(c.Servers, other)
	jk := c.GetJobsKey()
	if jk == "" || jk == c.GetSystemAPIKey() {
		t.Fatalf("the jobs key is derived per cluster, apart from the system key: %q", jk)
	}

	body := scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk)
	if !strings.Contains(string(body), `"key":"jobs"`) {
		t.Fatalf("the script must name the jobs key: %s", body)
	}
	data, mode, code, err := s.OpenJobBody(body, time.Now())
	if code != 200 || mode != JobKeyJobs || err != nil || strings.Contains(string(data), "secret") {
		t.Fatalf("jobs key: code %d, mode %s, err %v, data %s", code, mode, err, data)
	}

	stolen := scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk)
	if _, _, code, _ := other.OpenJobBody(stolen, time.Now()); code != 401 {
		t.Fatalf("a body naming db1 posted for db2 must be refused, got %d", code)
	}

	login := scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk)
	if node, _, code, err := c.SecretLoginCheck(map[string]string{"serverName": "db1", "serverPort": "3306"}, io.NopCloser(bytes.NewReader(login))); code != 200 || node != s {
		t.Fatalf("secret-login with the jobs key and no secret: code %d, err %v", code, err)
	}

	c.Conf.SecretKey = nil
	late := scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk)
	if _, _, code, _ := s.OpenJobBody(late, time.Now()); code != 401 {
		t.Fatalf("no persistent key: a jobs-key body must be refused, got %d", code)
	}
}

// The jobs volume maps the jobs key next to the database password when it can be derived, and
// the database password only without a persistent key; the ssh environment carries it.
func TestJobsKeyDelivery(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{}}
	if got := c.OpenSVCGetJobsVolumeSecret()["secrets"]; got != "env/MYSQL_ROOT_PASSWORD:/" {
		t.Fatalf("without a persistent key: %q", got)
	}
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	if got := c.OpenSVCGetJobsVolumeSecret()["secrets"]; got != "env/MYSQL_ROOT_PASSWORD:/ env/JOBS_API_KEY:/" {
		t.Fatalf("with a persistent key: %q", got)
	}
	s := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: c}
	c.Servers = []*ServerMonitor{s}
	if env := s.GetSshEnv(); !strings.Contains(env, "JOBS_API_KEY="+c.GetJobsKey()) && !strings.Contains(env, "JOBS_API_KEY='"+c.GetJobsKey()+"'") {
		t.Fatalf("the ssh environment must export the jobs key")
	}
}

// A jobs token (secret-login) reaches the jobs script's endpoints of its own cluster and
// nothing else, whatever the `system` account holds.
func TestJobsPrincipalGrants(t *testing.T) {
	c := newTokenTestCluster("c1")
	sys := APIUser{User: "system", Password: "x", Roles: map[string]bool{}}
	c.SetUserGrants(&sys, "db proxy "+config.GrantClusterResourceSensor)
	c.APIUsers["system"] = sys
	c.EnsureJobsPrincipal()
	p := c.JobsPrincipalName()
	srv := "/api/clusters/c1/servers/db1/3306"

	for _, url := range []string{
		srv + "/needs/backup",
		srv + "/write-log/main",
		srv + "/actions/receive-task/backup",
		srv + "/actions/job-state/backup/done",
		srv + "/actions/receive-jobs-check",
		srv + "/actions/send-jobs-upgrade",
		srv + "/config-receiver",
		srv + "/config-dummy-sender",
	} {
		if !c.IsValidACL(p, "", url, "token") {
			t.Errorf("a jobs token must reach %s", url)
		}
	}
	for _, url := range []string{
		srv + "/actions/backup-physical",
		srv + "/actions/reseed/mariabackup",
		srv + "/actions/maintenance",
		srv + "/actions/stop",
		srv + "/variables",
		"/api/clusters/c1/actions/switchover",
		"/api/clusters/c1/settings/actions/switch/failover-mode",
	} {
		if c.IsValidACL(p, "", url, "token") {
			t.Errorf("a jobs token must not reach %s", url)
		}
	}
	other := newTokenTestCluster("c2")
	other.APIUsers["system"] = sys
	if other.IsValidACL(p, "", "/api/clusters/c2/servers/db1/3306/needs/backup", "token") {
		t.Error("a jobs token of c1 has no principal on c2")
	}
	if c.IsValidACL(p, "", srv+"/needs/backup", "password") {
		t.Error("the jobs principal is no user: a password check refuses it")
	}
}

// scriptStream runs the jobs script's stream functions as the jobs of db1:3306 do: fn is
// job_stream_open (a stream to the jobs listener) or job_stream_signed (a stream to
// replication-manager). It returns what goes through and why it was refused (job_stream_open).
func scriptStream(t *testing.T, fn, logDir string, stream []byte, password, jobsKey string) (string, string) {
	t.Helper()
	fns := `src=../share/scripts/dbjobs_new.sh
eval "$(sed -n '/^derive_key() {/,/^}/p; /^jobs_key() {/,/^}/p; /^readonly JOB_STREAM_WINDOW=/p; /^job_stream_open() {/,/^}/p; /^job_stream_mac() {/,/^}/p; /^job_stream_signed() {/,/^}/p; /^job_stream_refused() {/,/^}/p' "$src")"
` + fn + `
job_stream_refused >&2`
	cmd := exec.Command("bash", "-c", fns)
	cmd.Env = append(cmd.Environ(), "JOBS_API_KEY="+jobsKey, "MYSQL_ROOT_PASSWORD="+password, "MYSQL_SERVER=db1", "MYSQL_PORT=3306", "LOG_DIR="+logDir)
	cmd.Stdin = bytes.NewReader(stream)
	var out, refused bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &refused
	cmd.Run()
	return out.String(), strings.TrimSpace(refused.String())
}

// A stream to the jobs listener is taken only with the header replication-manager signs with
// the key of the server's job bodies: once, within the window; a stream without it (whoever
// connects to the port first), replayed, forged or signed for the other direction is refused
// with nothing written. A server whose script posts legacy bodies gets no header; once its
// script signs, a legacy body is a replay.
func TestJobStreamHeader(t *testing.T) {
	s := jobLoginServer(true)
	c := s.ClusterGroup
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	jk := c.GetJobsKey()
	payload := "#!/bin/bash\necho new script\n# EOF\n"
	open := func(dir string, stream []byte, password, key string) (string, string) {
		return scriptStream(t, "job_stream_open", dir, stream, password, key)
	}

	if _, _, code, _ := s.OpenJobBody(legacyJobBody(t, `{"server":"db1:3306"}`, s.Pass), time.Now()); code != 200 {
		t.Fatalf("legacy body: %d", code)
	}
	if h, err := s.JobStreamHeader(time.Now()); h != nil || err != nil {
		t.Fatalf("an older script reads no header: got %q %v", h, err)
	}
	if _, _, code, err := s.OpenJobBody(scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk), time.Now()); code != 200 {
		t.Fatalf("jobs key body: %d %v", code, err)
	}
	if _, _, code, _ := s.OpenJobBody(legacyJobBody(t, `{"server":"db1:3306"}`, s.Pass), time.Now()); code != 401 {
		t.Fatalf("a legacy body once the script signs is a replay, got %d", code)
	}
	h, err := s.JobStreamHeader(time.Now())
	if err != nil || h == nil {
		t.Fatalf("header: %q %v", h, err)
	}
	dir := t.TempDir()
	if got, why := open(dir, append(append([]byte{}, h...), payload...), s.Pass, jk); got != payload || why != "" {
		t.Fatalf("signed stream: got %q, refused %q", got, why)
	}
	if got, why := open(dir, append(append([]byte{}, h...), payload...), s.Pass, jk); got != "" || !strings.Contains(why, "replay") {
		t.Fatalf("replayed header: got %q, refused %q", got, why)
	}
	if got, why := open(dir, []byte(payload), s.Pass, jk); got != "" || why == "" {
		t.Fatalf("no header (first to connect): got %q, refused %q", got, why)
	}
	now := time.Now().Unix()
	nonce := strings.Repeat("ab", 16)
	forged := []byte(fmt.Sprintf("MRMSTREAM1 %d %s %s\n", now, nonce, jobapi.StreamMAC("guess", jobapi.StreamToDB, "db1:3306", now, nonce)))
	if got, why := open(dir, append(forged, payload...), s.Pass, jk); got != "" || !strings.Contains(why, "not signed") {
		t.Fatalf("forged header: got %q, refused %q", got, why)
	}
	otherWay, _ := jobapi.StreamHeader(jk, jobapi.StreamToMonitor, "db1:3306", time.Now())
	if got, why := open(dir, append(otherWay, payload...), s.Pass, jk); got != "" || !strings.Contains(why, "not signed") {
		t.Fatalf("a header of the other direction: got %q, refused %q", got, why)
	}
	old, _ := s.JobStreamHeader(time.Now().Add(-time.Hour))
	if got, why := open(dir, append(old, payload...), s.Pass, jk); got != "" || !strings.Contains(why, "window") {
		t.Fatalf("old header: got %q, refused %q", got, why)
	}

	// without a jobs key the script signs with the database password, and so does the header
	if _, _, code, err := s.OpenJobBody(scriptJobBody(t, `{"server":"db1:3306"}`, s.Pass), time.Now()); code != 200 {
		t.Fatalf("db key body: %d %v", code, err)
	}
	h, _ = s.JobStreamHeader(time.Now())
	if got, why := open(dir, append(h, payload...), s.Pass, ""); got != payload || why != "" {
		t.Fatalf("db key stream: got %q, refused %q", got, why)
	}
}

// A replication-manager receiver takes the stream the script signs (job_stream_signed) and
// refuses one without the header, closing it and waiting for the next on the same listener:
// whoever connects first neither writes the backup nor blocks it; WARN0275 says so.
func TestJobStreamReceiver(t *testing.T) {
	s := jobLoginServer(false)
	c := s.ClusterGroup
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	jk := c.GetJobsKey()
	if _, _, code, err := s.OpenJobBody(scriptJobBodyKey(t, `{"server":"db1:3306"}`, s.Pass, jk), time.Now()); code != 200 {
		t.Fatalf("jobs key body: %d %v", code, err)
	}
	payload := "backup bytes\x00\x01 and more\n"
	signed, _ := scriptStream(t, "job_stream_signed", t.TempDir(), []byte(payload), s.Pass, jk)
	if !strings.HasPrefix(signed, jobapi.StreamTag+" ") || !strings.HasSuffix(signed, payload) {
		t.Fatalf("job_stream_signed: %q", signed)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sst := &SST{cluster: c, listener: ln}
	got := make(chan string, 1)
	go func() {
		conn, err := sst.acceptJobStream(s, "mariabackup")
		if err != nil {
			got <- "accept: " + err.Error()
			return
		}
		b, _ := io.ReadAll(conn)
		conn.Close()
		got <- string(b)
	}()
	send := func(b string) {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte(b))
		conn.Close()
	}
	send("injected backup\n")
	send(signed)
	select {
	case r := <-got:
		if r != payload {
			t.Fatalf("the receiver must take the signed stream only, got %q", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the receiver did not take the signed stream after refusing the first")
	}
	if err := s.CheckJobStream(pipeWith(signed)); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("the same signed stream again is a replay: %v", err)
	}
	c.CheckJobStreamRefused()
	if !c.SecurityStateMachine.IsInState("WARN0275") {
		t.Fatal("a refused stream opens WARN0275")
	}
}

// pipeWith is a connection that sends b.
func pipeWith(b string) net.Conn {
	a, z := net.Pipe()
	go func() { z.Write([]byte(b)); z.Close() }()
	return a
}

// The PostgreSQL sidecar (replication-manager-cli job, utils/jobapi) posts one-time bodies
// too, with the jobs key or the password, and its stream listener (stream --listen --job)
// takes replication-manager's signed stream only, once.
func TestJobAPIClientOneTime(t *testing.T) {
	s := jobLoginServer(false)
	c := s.ClusterGroup
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	for _, key := range []string{"", c.GetJobsKey()} {
		client := &jobapi.Client{Server: "db1", Port: "3306", Secret: s.Pass, JobsKey: key}
		body, err := client.Body(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		data, mode, code, err := s.OpenJobBody(body, time.Now())
		if code != 200 || (key != "") != (mode == JobKeyJobs) {
			t.Fatalf("jobapi body (jobs key %v): code %d, mode %s, err %v", key != "", code, mode, err)
		}
		if secret, _, _ := s.DecodeSecret(data); (secret == s.Pass) == (key != "") {
			t.Fatalf("the password travels without a jobs key only (jobs key %v)", key != "")
		}
		if _, _, code, _ := s.OpenJobBody(body, time.Now()); code != 401 {
			t.Fatalf("jobapi body replayed: %d", code)
		}

		h, _ := s.JobStreamHeader(time.Now())
		secret, _ := client.StreamSecret()
		nonces := t.TempDir() + "/nonces"
		check := func(r io.Reader) error {
			line, err := jobapi.ReadStreamHeader(r)
			if err != nil {
				return err
			}
			nonce, err := jobapi.CheckStreamHeader(line, secret, jobapi.StreamToDB, client.Name(), time.Now(), 300*time.Second)
			if err != nil {
				return err
			}
			return jobapi.TakeStreamNonce(nonces, nonce, time.Now(), 600*time.Second)
		}
		if err := check(bytes.NewReader(append(append([]byte{}, h...), "data"...))); err != nil {
			t.Fatalf("sidecar check of replication-manager's header: %v", err)
		}
		if err := check(bytes.NewReader(append(append([]byte{}, h...), "data"...))); err == nil {
			t.Fatal("the sidecar must refuse a header twice")
		}
	}
}
