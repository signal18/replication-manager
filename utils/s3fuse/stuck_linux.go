// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// fuseConnections is where the kernel shows each FUSE connection (fusectl), by its device minor.
const fuseConnections = "/sys/fs/fuse/connections"

// AbortStuckMounts, at the process start before it binds a port: a FUSE mount under root whose
// connection keeps requests waiting for an answer, at two reads interval apart, is the mount of
// a previous replication-manager that died while one of its threads waited on a request to its
// own mount (killed, or crashed): that thread can never get an answer, the process never ends
// and keeps the ports this start would bind (#2091). Its connection is aborted (the waiting
// request fails, the process ends) and the mount detached. A mount with nothing waiting is left
// alone: a live server, or one the cluster's start detaches. Returns the mount points aborted;
// the error says what could not be done, an abort needing write access to fusectl (root on a
// host, or CAP_SYS_ADMIN in a container to mount it).
func AbortStuckMounts(root string, interval time.Duration) ([]string, error) {
	// another live replication-manager may be serving them (a second start by mistake, a
	// restart overlapping): never abort a live server's busy mount (#2142); a dead one's main
	// thread reads as a zombie and does not count, nor does a mount's server (#2246)
	other := otherLiveProcess(processComm, os.Getpid())
	if other == 0 {
		// a mount's server still running lost its replication-manager (it did not end at its
		// parent's death signal, or not yet): killed, its mount then answers ENOTCONN and the
		// cluster's start detaches it
		stopOrphanServers(os.Getpid())
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	mounts := fuseMountsUnder(string(data), root)
	if len(mounts) == 0 {
		return nil, nil
	}
	if other != 0 {
		return nil, fmt.Errorf("another replication-manager runs (pid %d): the FUSE mounts under %s are left as they are", other, root)
	}
	// a container shows /sys read-only without fusectl: mounted in our own mount namespace for
	// the check (CAP_SYS_ADMIN, which the FUSE mount needs anyway), then unmounted
	if entries, err := os.ReadDir(fuseConnections); err == nil && len(entries) == 0 {
		if err := unix.Mount("fusectl", fuseConnections, "fusectl", 0, ""); err == nil {
			defer unix.Unmount(fuseConnections, 0)
		}
	}
	return abortStuck(mounts, interval, readWaiting, abortConnection)
}

type fuseMount struct {
	point string // the mount point
	conn  string // its connection: the device minor
}

// fuseMountsUnder lists the FUSE mounts of a mountinfo under root.
func fuseMountsUnder(mountinfo, root string) []fuseMount {
	root = filepath.Clean(root)
	var out []fuseMount
	for _, line := range strings.Split(mountinfo, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		f, g := strings.Fields(left), strings.Fields(right)
		if len(f) < 5 || len(g) < 1 || !strings.HasPrefix(g[0], "fuse") {
			continue
		}
		point := unescapeMountinfo(f[4])
		if point != root && !strings.HasPrefix(point, root+string(filepath.Separator)) {
			continue
		}
		_, minor, ok := strings.Cut(f[2], ":")
		if !ok {
			continue
		}
		out = append(out, fuseMount{point: point, conn: minor})
	}
	return out
}

// unescapeMountinfo decodes the octal escapes mountinfo writes for a space, a tab, a newline, a
// backslash.
func unescapeMountinfo(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// abortStuck aborts and detaches the mounts whose connection kept requests waiting at both reads.
func abortStuck(mounts []fuseMount, interval time.Duration, waiting func(conn string) (int, error), abort func(conn string) error) ([]string, error) {
	first := map[string]int{}
	var readErr error
	for _, m := range mounts {
		if n, err := waiting(m.conn); err == nil {
			first[m.conn] = n
		} else {
			readErr = err
		}
	}
	if len(first) == 0 && readErr != nil {
		// nothing read: a stuck mount would go unnoticed, said so (#2142)
		return nil, fmt.Errorf("FUSE mounts under the working directory not checked: %s unreadable (root, or CAP_SYS_ADMIN to mount fusectl): %s", fuseConnections, readErr)
	}
	stuck := false
	for _, n := range first {
		stuck = stuck || n > 0
	}
	if !stuck {
		return nil, nil
	}
	time.Sleep(interval)
	var aborted []string
	var errs []string
	for _, m := range mounts {
		if first[m.conn] == 0 {
			continue
		}
		if n, err := waiting(m.conn); err != nil || n == 0 {
			continue // answered meanwhile: a live server
		}
		if err := abort(m.conn); err != nil {
			errs = append(errs, fmt.Sprintf("%s (connection %s): %s", m.point, m.conn, err))
			continue
		}
		unix.Unmount(m.point, unix.MNT_DETACH)
		aborted = append(aborted, m.point)
	}
	if len(errs) > 0 {
		return aborted, fmt.Errorf("a FUSE mount left stuck by a previous run could not be aborted (write access to %s needed): %s", fuseConnections, strings.Join(errs, "; "))
	}
	return aborted, nil
}

// processComm is replication-manager's command name, as the kernel cuts it to 15 characters.
const processComm = "replication-man"

// otherLiveProcess is the pid of a live process (not a zombie) of that command name other than
// self and other than a mount's server, 0 when none: /proc/<pid>/stat, whose command name the
// kernel cuts to 15 characters.
func otherLiveProcess(comm string, self int) int {
	pid := 0
	liveProcesses(comm, self, func(p int, server bool) {
		if !server && pid == 0 {
			pid = p
		}
	})
	return pid
}

// liveProcesses calls f with each live process of that command name other than self, saying
// whether it is a mount's server.
func liveProcesses(comm string, self int, f func(pid int, server bool)) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		if name, state, ok := statCommState(string(b)); ok && name == comm && state != "Z" && state != "X" {
			cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
			f(pid, isServerCmdline(string(cmdline)))
		}
	}
}

// isServerCmdline: a /proc/<pid>/cmdline (arguments separated by NUL) is a mount's server's.
func isServerCmdline(cmdline string) bool {
	args := strings.Split(strings.TrimRight(cmdline, "\x00"), "\x00")
	return len(args) == 2 && args[1] == ServerCommand
}

// stopOrphanServers kills the mounts' servers left by a replication-manager gone (none other
// runs: checked first), and waits a little for them to end.
func stopOrphanServers(self int) {
	var orphans []int
	liveProcesses(processComm, self, func(pid int, server bool) {
		if server {
			orphans = append(orphans, pid)
		}
	})
	for _, pid := range orphans {
		unix.Kill(pid, unix.SIGKILL)
	}
	for i := 0; i < 20 && len(orphans) > 0; i++ {
		time.Sleep(100 * time.Millisecond)
		left := orphans[:0]
		for _, pid := range orphans {
			if unix.Kill(pid, 0) == nil {
				left = append(left, pid)
			}
		}
		orphans = left
	}
}

// statCommState reads the command name and the state of a /proc/<pid>/stat line: "pid (comm) S ...",
// comm may hold spaces and parentheses, so it ends at the last ')'.
func statCommState(stat string) (comm, state string, ok bool) {
	lp, rp := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if lp < 0 || rp < lp {
		return "", "", false
	}
	rest := strings.Fields(stat[rp+1:])
	if len(rest) == 0 {
		return "", "", false
	}
	return stat[lp+1 : rp], rest[0], true
}

func readWaiting(conn string) (int, error) {
	b, err := os.ReadFile(filepath.Join(fuseConnections, conn, "waiting"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func abortConnection(conn string) error {
	return os.WriteFile(filepath.Join(fuseConnections, conn, "abort"), []byte("1"), 0)
}
