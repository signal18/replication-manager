// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/githelper"
)

// #2159: the reconnect test. AES-CFB has no integrity check, so a value encrypted with another
// key "decrypts" without error to random bytes: only this instance's key gives readable values.
func TestOwnsEncryptedValues(t *testing.T) {
	mine := &Config{SecretKey: testEncryptionKey}
	other := &Config{SecretKey: []byte("fedcba9876543210fedcba9876543210")}
	var mineValues, otherValues []string
	for _, v := range []string{"s3cr3t-password", "glpat-0123456789abcdef", "root:Passw0rd!"} {
		mineValues = append(mineValues, mine.GetEncryptedString(v))
		otherValues = append(otherValues, other.GetEncryptedString(v))
	}
	if !mine.OwnsEncryptedValues(mineValues) {
		t.Error("the instance's own config must be recognized")
	}
	if mine.OwnsEncryptedValues(otherValues) {
		t.Error("another instance's config must not be taken for this one's")
	}
	if mine.OwnsEncryptedValues(append(mineValues, otherValues[0])) {
		t.Error("one value of another key is enough to refuse")
	}
	if mine.OwnsEncryptedValues(nil) || mine.OwnsEncryptedValues([]string{"hash_00"}) {
		t.Error("no usable value proves nothing")
	}
	// many draws: random bytes are never all printable
	for i := 0; i < 200; i++ {
		v := other.GetEncryptedString(fmt.Sprintf("token-%d-abcdefgh", i))
		if mine.OwnsEncryptedValues([]string{v}) {
			t.Fatalf("draw %d: another key's value read as printable", i)
		}
	}
}

// #2159: the project tokens are renewed 30 days before they end; an instance registered with the
// password has none and never rotates them.
func TestCloud18GitTokensDue(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	conf := &Config{Cloud18GitProjectID: 7, Cloud18GitTokenExpires: "2027-10-01"}
	if conf.Cloud18GitTokensDue(now) {
		t.Error("a token ending in a year is not due")
	}
	conf.Cloud18GitTokenExpires = "2026-11-10"
	if !conf.Cloud18GitTokensDue(now) {
		t.Error("a token ending in 16 days is due")
	}
	conf.Cloud18GitTokenExpires = ""
	if !conf.Cloud18GitTokensDue(now) {
		t.Error("an unknown end is due")
	}
	if (&Config{}).Cloud18GitTokensDue(now) {
		t.Error("an instance without project tokens has nothing to rotate")
	}
}

// #2159: the rotation keeps the new tokens and their end, saved encrypted (dynamic config); a
// token GitLab refuses marks the instance to register again, a success clears it.
func TestRotateCloud18GitTokens(t *testing.T) {
	valid := map[string]bool{"main-1": true, "pull-1": true}
	n := 1
	gitlab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		old := r.Header.Get("PRIVATE-TOKEN")
		if !valid[old] || !strings.HasSuffix(r.URL.Path, "/access_tokens/self/rotate") {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		delete(valid, old)
		n++
		tok := strings.Split(old, "-")[0] + fmt.Sprintf("-%d", n)
		valid[tok] = true
		fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, tok, r.URL.Query().Get("expires_at"))
	}))
	defer gitlab.Close()
	old := githelper.GitLabURL
	githelper.GitLabURL = gitlab.URL
	defer func() { githelper.GitLabURL = old }()

	conf := &Config{WorkingDir: t.TempDir(), SecretKey: testEncryptionKey, Cloud18GitProjectID: 10, Cloud18GitPullProjectID: 11}
	conf.Secrets = NewSecrets(nil)
	conf.SetCloud18GitTokens("main-1", "pull-1", "2026-11-01")

	now := time.Now()
	if err := conf.RotateCloud18GitTokens(now); err != nil {
		t.Fatal(err)
	}
	main, pull := conf.GetDecryptedValue("git-acces-token"), conf.GitPullToken()
	if !strings.HasPrefix(main, "main-") || main == "main-1" || !strings.HasPrefix(pull, "pull-") || pull == "pull-1" {
		t.Fatalf("tokens after rotation: main %q pull %q", main, pull)
	}
	if conf.Cloud18GitTokenExpires != now.Add(Cloud18GitTokenLifetime).Format("2006-01-02") || conf.Cloud18GitTokenLost() {
		t.Fatalf("end %q lost %v", conf.Cloud18GitTokenExpires, conf.Cloud18GitTokenLost())
	}
	if v, ok := conf.GetDynamicFlag("git-acces-token"); !ok || !strings.HasPrefix(fmt.Sprint(v), "hash_") {
		t.Fatalf("the new token must be saved encrypted: %v %v", v, ok)
	}

	conf.SetCloud18GitTokens("revoked", pull, conf.Cloud18GitTokenExpires)
	err := conf.RotateCloud18GitTokens(now)
	if !errors.Is(err, ErrCloud18GitTokenLost) || !conf.Cloud18GitTokenLost() {
		t.Fatalf("a refused token: %v, lost %v", err, conf.Cloud18GitTokenLost())
	}
	conf.SetCloud18GitTokens(fmt.Sprintf("main-%d", n-1), pull, conf.Cloud18GitTokenExpires)
	valid[fmt.Sprintf("main-%d", n-1)] = true
	if err := conf.RotateCloud18GitTokens(now); err != nil || conf.Cloud18GitTokenLost() {
		t.Fatalf("a rotation that works again clears the request: %v %v", err, conf.Cloud18GitTokenLost())
	}

	if err := (&Config{}).RotateCloud18GitTokens(now); err != nil {
		t.Fatalf("an instance registered with the password: %v", err)
	}
}
