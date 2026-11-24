package cluster

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The backups of this tool carry "myloader_exec_change_source = 1" (taken with --source-data=2):
// myloader then sends CHANGE REPLICATION SOURCE TO, which MariaDB refuses (1064): myloader 1.0.1
// ends with exit 1 after the data is loaded, myloader 0.18.1 logs the error and completes. Run on
// MariaDB 10.6, 10.11, 11.8 and 12.2 with myloader 1.0.1 (1.0.11 on MariaDB 12.2): --source-data=0
// on myloader sends nothing, the data and the metadata position are intact, and the restore exits 0.
//
// myloader 0.17.1 rejects the option (option parsing failure) and 0.18.1 accepts it.
func TestMyLoaderNoSourceChangeArgsVersions(t *testing.T) {
	defaults := strings.Split("--overwrite-tables --verbose=3 --innodb-optimize-keys=skip --max-threads-for-schema-creation=1 --max-threads-for-index-creation=1", " ")
	for _, c := range []struct {
		name    string
		version string // "" is an unknown version (nil)
		added   bool
	}{
		{"unknown version", "", false},
		{"0.16.7", "0.16.7", false},
		{"0.17.1 rejects the option", "0.17.1", false},
		{"0.18.1 first supported", "0.18.1", true},
		{"0.19.3", "0.19.3", true},
		{"0.20.1", "0.20.1", true},
		{"0.21.3", "0.21.3", true},
		{"1.0.1", "1.0.1", true},
		{"1.0.11", "1.0.11", true},
	} {
		for _, args := range [][]string{{}, {"--verbose=3"}, defaults} {
			in := slices.Clone(args)
			var got []string
			if c.version == "" {
				got = myLoaderNoSourceChangeArgs(in, nil)
			} else {
				got = myLoaderNoSourceChangeArgs(in, mydumperVersion(c.version))
			}
			want := slices.Clone(args)
			if c.added {
				want = append(want, "--source-data=0")
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s, %d args: got %v, want %v", c.name, len(args), got, want)
			}
			// nothing else is removed or reordered: the configured arguments are a prefix, untouched
			if len(got) < len(args) || !slices.Equal(got[:len(args)], args) {
				t.Errorf("%s, %d args: configured arguments changed: %v", c.name, len(args), got)
			}
		}
	}
}

// An option the user configured is kept as it is, whatever its value or form: no second
// --source-data is added next to it.
func TestMyLoaderNoSourceChangeArgsConfigured(t *testing.T) {
	for _, configured := range [][]string{
		{"--source-data=0"}, {"--source-data=1"}, {"--source-data=2"}, {"--source-data=7"},
		{"--source-data"}, {"--source-data", "0"}, {"--source-data="},
	} {
		for _, v := range []string{"0.18.1", "1.0.1", "1.0.11"} {
			args := append([]string{"--overwrite-tables", "--verbose=3"}, configured...)
			args = append(args, "--max-threads-for-schema-creation=1")
			got := myLoaderNoSourceChangeArgs(slices.Clone(args), mydumperVersion(v))
			if !slices.Equal(got, args) {
				t.Errorf("%v on %s: got %v, want the options unchanged", configured, v, got)
			}
			n := 0
			for _, a := range got {
				if a == "--source-data" || strings.HasPrefix(a, "--source-data=") {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%v on %s: %d --source-data options in %v", configured, v, n, got)
			}
		}
	}
	// an option that only looks alike is not --source-data
	args := []string{"--source-database=x", "--source-db=app"}
	if got := myLoaderNoSourceChangeArgs(slices.Clone(args), mydumperVersion("1.0.1")); !slices.Equal(got, append(slices.Clone(args), "--source-data=0")) {
		t.Errorf("similar options: got %v", got)
	}
}

// The helper never changes the slice it is given when it adds nothing, and the added option is
// the last one (the restore appends its own --host/--directory after it).
func TestMyLoaderNoSourceChangeArgsOrderAndInput(t *testing.T) {
	in := []string{"--drop-table=DROP", "--verbose=3"}
	got := myLoaderNoSourceChangeArgs(slices.Clone(in), mydumperVersion("1.0.1"))
	if got[len(got)-1] != "--source-data=0" || len(got) != len(in)+1 {
		t.Fatalf("got %v", got)
	}
	if again := myLoaderNoSourceChangeArgs(slices.Clone(got), mydumperVersion("1.0.1")); !slices.Equal(again, got) {
		t.Fatalf("applying it twice changed the options: %v", again)
	}
}

// The version is the one of the myloader the restore runs (backup-myloader-path), not the detected
// mydumper's: run with a 0.17.1 myloader next to a 0.18.1 mydumper (MariaDB 12.2), the option was
// refused ("Unknown option --source-data=0") and the reseed failed.
func TestMyLoaderVersionOfTheBinaryThatRuns(t *testing.T) {
	_, srv := newStagingTestServer(t)
	cl := srv.ClusterGroup
	write := func(out string) string {
		p := t.TempDir() + "/myloader"
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct {
		out   string
		added bool
	}{
		{"myloader v0.17.1-1, built against MySQL 8.0.39-30 with SSL support", false},
		{"myloader v0.18.1-1, built against MySQL 8.0.40-31 with SSL support", true},
		{"myloader v1.0.1-1, built against MySQL 8.4.9 with SSL support", true},
		{"myloader v1.0.11-1, built against MySQL 8.4.11 with SSL support", true},
	} {
		cl.Conf.BackupMyLoaderPath = write(c.out)
		got := myLoaderNoSourceChangeArgs([]string{"--verbose=3"}, cl.myLoaderVersion())
		if added := slices.Contains(got, "--source-data=0"); added != c.added {
			t.Errorf("%q: --source-data=0 added=%v, want %v (%v)", c.out, added, c.added, got)
		}
	}
	// a myloader that can not be run, or whose output is no version: nothing added
	cl.Conf.BackupMyLoaderPath = t.TempDir() + "/missing"
	if v := cl.myLoaderVersion(); v != nil {
		t.Errorf("missing binary: version %v, want nil", v)
	}
	if got := myLoaderNoSourceChangeArgs([]string{"--verbose=3"}, cl.myLoaderVersion()); slices.Contains(got, "--source-data=0") {
		t.Errorf("missing binary: option added: %v", got)
	}
	cl.Conf.BackupMyLoaderPath = write("not a version")
	if got := myLoaderNoSourceChangeArgs([]string{"--verbose=3"}, cl.myLoaderVersion()); slices.Contains(got, "--source-data=0") {
		t.Errorf("no version in the output: option added: %v", got)
	}
}

// gone waits for a process to be killed and reaped (signal 0 fails with ESRCH).
func gone(pid int) bool {
	for i := 0; i < 40; i++ {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func readPid(t *testing.T, file string) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", file)
	return 0
}

// A myloader that never answers is cut at the deadline: no version (nothing is added to the
// arguments), the probe returns in bounded time and the process does not outlive it.
func TestReadMyLoaderVersionTimesOut(t *testing.T) {
	dir := t.TempDir()
	pidFile := dir + "/pid"
	bin := dir + "/myloader"
	// exec: the shell becomes the sleeper, so the kill at the deadline reaches it
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	v, err := readMyLoaderVersion(bin, 300*time.Millisecond)
	if v != nil || err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("hung binary: version %v, error %v", v, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("probe took %s, want about the 300ms deadline", d)
	}
	pid := readPid(t, pidFile)
	if !gone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("process %d still running after the probe returned", pid)
	}
	if got := myLoaderNoSourceChangeArgs([]string{"--verbose=3"}, v); slices.Contains(got, "--source-data=0") {
		t.Errorf("no version: option added: %v", got)
	}
}

// A child that inherited the output pipes must not hold the probe past the deadline plus
// myLoaderVersionWaitDelay.
func TestReadMyLoaderVersionChildHoldsThePipe(t *testing.T) {
	dir := t.TempDir()
	childFile := dir + "/child"
	bin := dir + "/myloader"
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 60 &\necho $! > "+childFile+"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // no orphan left by the test
		if b, err := os.ReadFile(childFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	v, err := readMyLoaderVersion(bin, 300*time.Millisecond)
	if v != nil || err == nil {
		t.Fatalf("version %v, error %v", v, err)
	}
	if d := time.Since(start); d > 300*time.Millisecond+myLoaderVersionWaitDelay+2*time.Second {
		t.Errorf("probe took %s, want at most the deadline plus the wait delay", d)
	}
}

// The reasons a version is not read, and the fast path.
func TestReadMyLoaderVersionFailuresAndSuccess(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if v, err := readMyLoaderVersion(dir+"/missing", time.Second); v != nil || err == nil {
		t.Errorf("missing binary: %v %v", v, err)
	}
	if v, err := readMyLoaderVersion(write("exit1", "#!/bin/sh\nexit 1\n"), time.Second); v != nil || err == nil {
		t.Errorf("exit 1: %v %v", v, err)
	}
	if v, err := readMyLoaderVersion(write("noversion", "#!/bin/sh\necho 'not a version'\n"), time.Second); v != nil || err == nil || !strings.Contains(err.Error(), "no version") {
		t.Errorf("no version in the output: %v %v", v, err)
	}
	v, err := readMyLoaderVersion(write("ok", "#!/bin/sh\necho 'myloader v1.0.1-1, built against MySQL 8.4.9 with SSL support'\n"), time.Second)
	if err != nil || v == nil || !v.GreaterEqual("0.18.1") {
		t.Errorf("1.0.1: %v %v", v, err)
	}
}
