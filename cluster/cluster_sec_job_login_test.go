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
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/state"
)

// scriptJobBody builds a job body with the jobs script's own functions (job_body and the
// helpers it calls), so the test covers what a database server really posts.
func scriptJobBody(t *testing.T, payload, password string) []byte {
	t.Helper()
	for _, bin := range []string{"bash", "openssl", "sha256sum"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	fns := `src=../share/scripts/dbjobs_new.sh
eval "$(sed -n '/^pad_pkcs7() {/,/^}/p; /^derive_key() {/,/^}/p; /^job_body() {/,/^}/p' "$src")"
job_body "$1" "$2"`
	out, err := exec.Command("bash", "-c", fns, "job_body", payload, password).Output()
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
	s := &ServerMonitor{URL: "db1:3306", Pass: "s3cr3t", ClusterGroup: c}
	c.Servers = []*ServerMonitor{s}
	return s
}

// A one-time body of the script is accepted once, its payload intact; the same body again is
// a replay; a body outside the window is refused.
func TestOpenJobBodyOneTime(t *testing.T) {
	s := jobLoginServer(false)
	body := scriptJobBody(t, `{"server":"db1:3306", "secret":"pw"}`, s.Pass)
	now := time.Now()

	data, oneTime, code, err := s.OpenJobBody(body, now)
	if err != nil || code != 200 || !oneTime {
		t.Fatalf("first use: code %d, one-time %v, err %v", code, oneTime, err)
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

// A legacy body is accepted while api-secret-login-legacy is on and noted for WARN0272;
// refused when it is off.
func TestOpenJobBodyLegacy(t *testing.T) {
	s := jobLoginServer(true)
	body := legacyJobBody(t, `{"server":"db1:3306","secret":"pw"}`, s.Pass)
	for i := 0; i < 2; i++ { // replayable by nature: accepted each time while legacy is on
		if _, oneTime, code, err := s.OpenJobBody(body, time.Now()); code != 200 || oneTime || err != nil {
			t.Fatalf("legacy on: code %d, one-time %v, err %v", code, oneTime, err)
		}
	}
	if atomic.LoadInt64(&s.legacyJobLoginAt) == 0 {
		t.Fatal("a legacy body must be noted")
	}
	s.ClusterGroup.CheckSecretLoginLegacy()
	open := s.ClusterGroup.SecurityStateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0272" || !strings.Contains(open[0].ErrDesc, s.URL) {
		t.Fatalf("WARN0272 on the server: %+v", open)
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
