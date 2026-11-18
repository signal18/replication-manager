// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	sshclient "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/config"
)

// The on-premise jobs run over SSH: replication-manager pipes the jobs script into a shell on the
// database host (JobRunViaSSH). The script reaches replication-manager only through
// replication-manager-cli (#2405), so before each run the CLI is put on the host, in the server's
// jobs directory (GetJobDatadir, the script's TMP_DIR), where the script looks after its own
// directory and before the PATH. See doc/implementation/share/scripts/DBJOBS_CLI.md.
//
//   - Marker: <jobs dir>/replication-manager-cli.sha256 holds "<sha256> <CLI version>" of the copy.
//     Each run reads it (and `uname -sm`) in one command: the CLI is never hashed on the host
//     at each run, only after a copy.
//   - Copy: when the marker is missing or names another hash. Written to a temporary name,
//     checked (sha256, then run once: a noexec mount fails there), made 755, renamed, then the
//     marker is written: a running job never sees a half-written CLI.
//   - Only to a host of the monitor's own OS and architecture: the copy is the monitor's binary.
//     A mismatch copies nothing and is a job error naming both; the CLI can then be installed by
//     hand on the host's PATH.
//   - A failed copy is retried at most every jobsCliCopyRetry per server.

const (
	jobsCliName      = "replication-manager-cli"
	jobsCliCopyRetry = 10 * time.Minute
)

// jobsCliLocal is the CLI on the monitor and what the hosts compare their copy with, computed
// once per file (path, size, modification time): hashing 200 MB at each job run would cost the
// monitor what it saves the hosts.
type jobsCliLocal struct {
	path    string
	size    int64
	mtime   time.Time
	hash    string
	version string
}

var (
	jobsCliLocalMu    sync.Mutex
	jobsCliLocalCache jobsCliLocal
)

// localJobsCli returns the monitor's CLI with its hash and version, from the cache when the
// file did not change.
func (cluster *Cluster) localJobsCli() (jobsCliLocal, error) {
	path := cluster.Conf.GetReplicationManagerCliPath()
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return jobsCliLocal{}, fmt.Errorf("no %s on the monitor (looked at %s)", jobsCliName, path)
	}
	jobsCliLocalMu.Lock()
	defer jobsCliLocalMu.Unlock()
	c := jobsCliLocalCache
	if c.path == path && c.size == st.Size() && c.mtime.Equal(st.ModTime()) {
		return c, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return jobsCliLocal{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return jobsCliLocal{}, err
	}
	c = jobsCliLocal{path: path, size: st.Size(), mtime: st.ModTime(), hash: hex.EncodeToString(h.Sum(nil))}
	c.version = jobsCliVersion(path)
	jobsCliLocalCache = c
	return c, nil
}

// jobsCliVersion is the "Full Version" the CLI prints, "unknown" when it does not run.
func jobsCliVersion(path string) string {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return "unknown"
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v := strings.TrimSpace(strings.TrimPrefix(l, "Full Version:")); v != l && v != "" {
			return v
		}
	}
	return "unknown"
}

// unameToGo maps `uname -sm` to GOOS/GOARCH, "" for a system the release does not build.
func unameToGo(uname string) string {
	f := strings.Fields(uname)
	if len(f) != 2 {
		return ""
	}
	goos := map[string]string{"Linux": "linux", "Darwin": "darwin", "FreeBSD": "freebsd"}[f[0]]
	goarch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64", "ppc64le": "ppc64le", "s390x": "s390x"}[f[1]]
	if goos == "" || goarch == "" {
		return ""
	}
	return goos + "/" + goarch
}

// jobsCliPlan decides from the host's `uname -sm` and its marker whether to copy: "" when the
// copy on the host is the monitor's, else the reason to copy, or an error when it cannot be the
// monitor's binary.
func jobsCliPlan(uname, marker string, local jobsCliLocal, monitorPlatform string) (string, error) {
	host := unameToGo(uname)
	if host != monitorPlatform {
		if host == "" {
			host = strings.TrimSpace(uname)
		}
		return "", fmt.Errorf("the host is %s and the monitor's %s is %s: nothing copied. Multi-architecture CLIs are not shipped yet; install %s for %s by hand on the PATH of the jobs user",
			host, jobsCliName, monitorPlatform, jobsCliName, host)
	}
	if f := strings.Fields(marker); len(f) > 0 && f[0] == local.hash {
		return "", nil
	}
	if strings.TrimSpace(marker) == "" {
		return "absent", nil
	}
	return "another version", nil
}

// ensureJobsCliViaSSH puts the monitor's CLI in the server's jobs directory on the host when it
// is absent or another version. It returns why the jobs cannot run there, nil when they can.
func (server *ServerMonitor) ensureJobsCliViaSSH(client *sshclient.Client) error {
	cluster := server.ClusterGroup
	server.jobsCliMu.Lock()
	defer server.jobsCliMu.Unlock()
	if server.jobsCliErr != nil && time.Since(server.jobsCliErrAt) < jobsCliCopyRetry {
		return server.jobsCliErr
	}
	fail := func(err error) error {
		server.jobsCliErr, server.jobsCliErrAt = err, time.Now()
		return err
	}
	local, err := cluster.localJobsCli()
	if err != nil {
		return fail(err)
	}
	dir := server.GetJobDatadir()
	target := dir + "/" + jobsCliName
	out, err := client.Cmd("uname -sm; echo; cat " + shellSingleQuote(target+".sha256") + " 2>/dev/null").Output()
	if err != nil {
		return fail(fmt.Errorf("cannot read the host system and the %s marker: %v", jobsCliName, err))
	}
	uname, marker, _ := strings.Cut(string(out), "\n\n")
	why, err := jobsCliPlan(uname, marker, local, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		return fail(err)
	}
	if why == "" {
		server.jobsCliErr = nil
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Copying %s %s to %s:%s (%s on the host)", jobsCliName, local.version, server.Host, target, why)
	if err := copyJobsCli(client, local, dir); err != nil {
		return fail(fmt.Errorf("copy of %s to %s failed: %v", jobsCliName, dir, err))
	}
	server.jobsCliErr = nil
	return nil
}

// copyJobsCli streams the CLI into dir on the host over one SSH session: temporary name, sha256
// check, one run (a noexec mount fails here), 755, rename, then the marker.
func copyJobsCli(client *sshclient.Client, local jobsCliLocal, dir string) error {
	f, err := os.Open(local.path)
	if err != nil {
		return err
	}
	defer f.Close()
	session, err := client.UnderlyingClient().NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var stderr strings.Builder
	session.Stdin, session.Stderr = f, &stderr
	d := shellSingleQuote(dir)
	script := `set -e; d=` + d + `; mkdir -p "$d"; t="$d/.` + jobsCliName + `.tmp.$$"; trap 'rm -f "$t"' EXIT; cat >"$t"
h=$( (sha256sum "$t" 2>/dev/null || shasum -a 256 "$t") | cut -d' ' -f1)
[ "$h" = ` + shellSingleQuote(local.hash) + ` ] || { echo "checksum mismatch after the copy (no space left?)" >&2; exit 3; }
chmod 755 "$t"
"$t" version >/dev/null 2>&1 || { echo "the copy does not run (noexec mount?)" >&2; exit 4; }
mv -f "$t" "$d/` + jobsCliName + `"; trap - EXIT
printf '%s %s\n' "$h" ` + shellSingleQuote(local.version) + ` >"$d/` + jobsCliName + `.sha256"`
	if err := session.Run(script); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}
