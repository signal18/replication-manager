// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package jobapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The job stream header: the first line of every stream between replication-manager and a
// database's jobs (a backup, a log, a dump, a jobs script, a config), so each side takes a
// stream from the other only, not from whoever connects to the port first:
//
//	MRMSTREAM1 <ts> <nonce> <hex HMAC-SHA256(SHA256(secret), "<direction>:<server>:<ts>:<nonce>")>\n
//
// The secret is the one the jobs sign their bodies with: the cluster's jobs key, else the
// database password. The direction (StreamToDB, StreamToMonitor) keeps a header sent one way
// from being replayed the other way. The receiver refuses a header outside its window or
// with a nonce it already took. dbjobs_new.sh builds and checks the same line with openssl
// (job_stream_header, job_stream_open).

const (
	StreamTag       = "MRMSTREAM1"
	StreamToDB      = "to-db"     // replication-manager to the jobs listener
	StreamToMonitor = "to-repman" // the jobs to a replication-manager receiver
	// StreamHeaderMax bounds the header line a receiver reads before giving up.
	StreamHeaderMax = 200
)

var streamHeaderRe = regexp.MustCompile(`^` + StreamTag + ` ([0-9]+) ([0-9a-f]{32}) ([0-9a-f]{64})$`)

// StreamMAC is the header's MAC, keyed with SHA-256 of the secret (derive_key in the script).
func StreamMAC(secret, direction, server string, ts int64, nonce string) string {
	key := sha256.Sum256([]byte(secret))
	mac := hmac.New(sha256.New, key[:])
	fmt.Fprintf(mac, "%s:%s:%d:%s", direction, server, ts, nonce)
	return hex.EncodeToString(mac.Sum(nil))
}

// StreamHeader is a header line for one stream from now.
func StreamHeader(secret, direction, server string, now time.Time) ([]byte, error) {
	n := make([]byte, 16)
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(n)
	ts := now.Unix()
	return []byte(fmt.Sprintf("%s %d %s %s\n", StreamTag, ts, nonce, StreamMAC(secret, direction, server, ts, nonce))), nil
}

// ReadStreamHeader reads the header line from r a byte at a time, so what follows stays in
// r, StreamHeaderMax bytes at most.
func ReadStreamHeader(r io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for len(line) < StreamHeaderMax {
		if _, err := io.ReadFull(r, b); err != nil {
			return "", fmt.Errorf("no stream header: %w", err)
		}
		if b[0] == '\n' {
			return string(line), nil
		}
		line = append(line, b[0])
	}
	return "", fmt.Errorf("no stream header")
}

// CheckStreamHeader checks a header line: its MAC for the secret, direction and server, its
// timestamp within window of now. It returns the nonce, which the caller takes once.
func CheckStreamHeader(line, secret, direction, server string, now time.Time, window time.Duration) (string, error) {
	m := streamHeaderRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return "", fmt.Errorf("no stream header")
	}
	ts, _ := strconv.ParseInt(m[1], 10, 64)
	if !hmac.Equal([]byte(m[3]), []byte(StreamMAC(secret, direction, server, ts, m[2]))) {
		return "", fmt.Errorf("stream header not signed by the jobs key")
	}
	if t := time.Unix(ts, 0); t.Before(now.Add(-window)) || t.After(now.Add(window)) {
		return "", fmt.Errorf("stream header outside the %s window (clock skew or replay)", window)
	}
	return m[2], nil
}

// JobsKeyFromEnv is the cluster's jobs key as the jobs container gets it (jobs_key in the
// script): $JOBS_API_KEY, else /credentials/JOBS_API_KEY (the shm secret volume), else "".
func JobsKeyFromEnv() string {
	if k := os.Getenv("JOBS_API_KEY"); k != "" {
		return k
	}
	b, _ := os.ReadFile("/credentials/JOBS_API_KEY")
	return strings.TrimSpace(string(b))
}

// TakeStreamNonce records nonce in the file of the nonces a one-shot receiver took (the
// CLI's stream --listen), refusing one already there; entries older than keep are dropped,
// so the file holds the nonces of one window at most.
func TakeStreamNonce(path, nonce string, now time.Time, keep time.Duration) error {
	b, _ := os.ReadFile(path)
	var kept []string
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		if f[1] == nonce {
			return fmt.Errorf("stream header already used (replay)")
		}
		if ts, err := strconv.ParseInt(f[0], 10, 64); err == nil && now.Sub(time.Unix(ts, 0)) < keep {
			kept = append(kept, l)
		}
	}
	kept = append(kept, fmt.Sprintf("%d %s", now.Unix(), nonce))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
