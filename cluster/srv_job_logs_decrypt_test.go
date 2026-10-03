package cluster

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

// opensslEncrypt mirrors encrypt_data() in share/scripts/dbjobs_new.sh: AES-256-CBC,
// no salt, key/iv derived from the password, base64 stripped to a single line.
func opensslEncrypt(t *testing.T, plain, password string, wrap bool) string {
	t.Helper()
	key := sha256.Sum256([]byte(password))
	iv := md5.Sum([]byte(password))
	args := []string{"aes-256-cbc", "-a", "-nosalt", "-K", hex.EncodeToString(key[:]), "-iv", hex.EncodeToString(iv[:])}
	cmd := exec.Command("openssl", args...)
	cmd.Stdin = strings.NewReader(plain)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("openssl encrypt: %v", err)
	}
	if wrap {
		return strings.TrimSpace(string(out))
	}
	return strings.ReplaceAll(string(out), "\n", "")
}

// DecryptAES256 must preserve complete plaintext: OpenSSL can exit successfully
// with truncated output when a long, single-line Base64 payload lacks -A.
func TestDecryptAES256AnyPayloadSize(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not available")
	}
	const password = "pw"
	key := sha256.Sum256([]byte(password))
	iv := md5.Sum([]byte(password))
	server := &ServerMonitor{}

	var sizes []int
	for n := 0; n <= 2200; n += 4 { // the dense range: every ciphertext length
		sizes = append(sizes, n)
	}
	for n := 2200; n <= 8000; n += 97 { // the range that failed for every payload
		sizes = append(sizes, n)
	}
	sizes = append(sizes, 16<<10, 64<<10, 256<<10, 1<<20, 3<<20)

	for _, form := range []string{"single line", "wrapped", "padded"} {
		for _, n := range sizes {
			plain := `{"log":"` + strings.Repeat("a", n) + `"}`
			sent := plain
			if form == "padded" {
				sent += strings.Repeat("\x01", 32-len(plain)%32)
			}
			enc := opensslEncrypt(t, sent, password, form == "wrapped")
			got, err := server.DecryptAES256(enc, hex.EncodeToString(key[:]), hex.EncodeToString(iv[:]))
			if err != nil {
				t.Fatalf("payload %d (%s, base64 %d chars): %v", n, form, len(enc), err)
			}
			if string(got) != sent {
				t.Fatalf("payload %d (%s, base64 %d chars): decrypted %d bytes, want %d", n, form, len(enc), len(got), len(sent))
			}
		}
	}
}
