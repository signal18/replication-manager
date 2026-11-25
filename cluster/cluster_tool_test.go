package cluster

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/config"
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

// ---- managed default adaptation (issue #2433) ----

var shippedMyLoaderDefault = strings.Fields(config.DefaultBackupMyLoaderOptions)

func TestShippedMyLoaderDefaultIsTheFlagDefault(t *testing.T) {
	want := "--overwrite-tables --verbose=3 --innodb-optimize-keys=skip --max-threads-for-schema-creation=1 --max-threads-for-index-creation=1"
	if config.DefaultBackupMyLoaderOptions != want {
		t.Fatalf("shipped default changed: %q", config.DefaultBackupMyLoaderOptions)
	}
}

// The boundaries: --overwrite-tables -> --drop-table=DROP from 0.20.1, --innodb-optimize-keys=skip ->
// --optimize-keys=SKIP from 0.19.1; older releases keep the shipped list as it is; the input is not modified.
func TestMyLoaderManagedDefaultArgsBoundaries(t *testing.T) {
	const ow, dt, ok, nk = "--overwrite-tables", "--drop-table=DROP", "--innodb-optimize-keys=skip", "--optimize-keys=SKIP"
	for _, c := range []struct {
		version      string
		drop, optkey bool // adapted?
	}{
		{"0.16.7", false, false}, {"0.17.1", false, false}, {"0.18.1", false, false},
		{"0.19.1", false, true}, {"0.19.3", false, true},
		{"0.20.1", true, true}, {"0.20.2", true, true}, {"0.21.4", true, true},
		{"1.0.0", true, true}, {"1.0.1", true, true}, {"1.0.19", true, true},
	} {
		in := slices.Clone(shippedMyLoaderDefault)
		got, changes := myLoaderManagedDefaultArgs(in, mydumperVersion(c.version))
		if !slices.Equal(in, shippedMyLoaderDefault) {
			t.Errorf("%s: the input was modified: %v", c.version, in)
		}
		want := slices.Clone(shippedMyLoaderDefault)
		for i, a := range want {
			if a == ow && c.drop {
				want[i] = dt
			}
			if a == ok && c.optkey {
				want[i] = nk
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", c.version, got, want)
		}
		if n := len(changes); (c.drop && c.optkey && n != 2) || (c.drop != c.optkey && n != 1) || (!c.drop && !c.optkey && n != 0) {
			t.Errorf("%s: %d changes reported (%v)", c.version, n, changes)
		}
		// everything else of the list is untouched and in place
		for i := range want {
			if want[i] == shippedMyLoaderDefault[i] && got[i] != shippedMyLoaderDefault[i] {
				t.Errorf("%s: position %d changed", c.version, i)
			}
		}
	}
	// unknown version: nothing adapted
	if got, changes := myLoaderManagedDefaultArgs(slices.Clone(shippedMyLoaderDefault), nil); !slices.Equal(got, shippedMyLoaderDefault) || len(changes) != 0 {
		t.Errorf("nil version: %v %v", got, changes)
	}
}

func TestMyLoaderRejectedOptionsForExplicitConfiguration(t *testing.T) {
	cases := []struct {
		args    string
		version string
		n       int
	}{
		{"--overwrite-tables --verbose=3", "1.0.1", 1},
		{"--overwrite-tables --verbose=3", "0.19.3", 0},
		{"--innodb-optimize-keys=skip", "0.19.1", 1},
		{"--innodb-optimize-keys", "0.20.1", 1},
		{"--innodb-optimize-keys=skip", "0.18.1", 0},
		{"--drop-table=DROP --optimize-keys=SKIP", "1.0.1", 0},
		{"--overwrite-tables --innodb-optimize-keys=skip", "0.21.4", 2},
	}
	for _, c := range cases {
		if got := myLoaderRejectedOptions(strings.Fields(c.args), mydumperVersion(c.version)); len(got) != c.n {
			t.Errorf("%q on %s: %d messages (%v), want %d", c.args, c.version, len(got), got, c.n)
		}
	}
	if got := myLoaderRejectedOptions(strings.Fields("--overwrite-tables"), nil); got != nil {
		t.Errorf("unknown version: %v", got)
	}
}

// fakeMyLoader writes an executable that answers --version and counts its invocations.
func fakeMyLoader(t *testing.T, ver string) (path, counter string) {
	t.Helper()
	dir := t.TempDir()
	path, counter = dir+"/myloader", dir+"/count"
	body := "#!/bin/sh\necho x >> " + counter + "\necho 'myloader v" + ver + "-1, built against MySQL 8.4.9 with SSL support'\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, counter
}

func probes(t *testing.T, counter string) int {
	b, err := os.ReadFile(counter)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "x")
}

// The restore arguments: version read once, adaptation of the exact shipped default only, replica reseed only.
func TestMyLoaderRestoreArgs(t *testing.T) {
	_, srv := newStagingTestServer(t)
	cl := srv.ClusterGroup
	cl.VersionsMap = config.NewVersionsMap()
	const dt, nk = "--drop-table=DROP", "--optimize-keys=SKIP"
	setup := func(loader, dumper, opts string) string {
		path, counter := fakeMyLoader(t, loader)
		cl.Conf.BackupMyLoaderPath = path
		cl.Conf.BackupMyLoaderOptions = opts
		cl.Conf.DeleteImmuableFlag("backup-myloader-options")
		cl.Conf.DeleteDynamicFlag("backup-myloader-options")
		if dumper != "" {
			cl.VersionsMap.Set("mydumper", mydumperVersion(dumper))
		}
		return counter
	}
	def := config.DefaultBackupMyLoaderOptions
	defOld := slices.Clone(shippedMyLoaderDefault)

	// untouched shipped default, replica reseed, one probe
	counter := setup("1.0.1", "1.0.1", def)
	got := cl.myLoaderRestoreArgs(true)
	want := []string{dt, "--verbose=3", nk, "--max-threads-for-schema-creation=1", "--max-threads-for-index-creation=1", "--source-data=0"}
	if !slices.Equal(got, want) {
		t.Errorf("1.0.1 replica: got %v, want %v", got, want)
	}
	if n := probes(t, counter); n != 1 {
		t.Errorf("the myloader version was probed %d times for one restore, want 1", n)
	}

	// master restore and parent seeding (adaptDefaults=false): exactly what #2426 builds
	counter = setup("1.0.1", "1.0.1", def)
	got = cl.myLoaderRestoreArgs(false)
	if want := append(slices.Clone(defOld), "--source-data=0"); !slices.Equal(got, want) {
		t.Errorf("master/parent: got %v, want %v", got, want)
	}
	if n := probes(t, counter); n != 1 {
		t.Errorf("master/parent: %d probes, want 1", n)
	}

	// mixed tool versions: the loader decides, not the dumper
	setup("1.0.19", "0.18.1", def)
	if got = cl.myLoaderRestoreArgs(true); !slices.Contains(got, dt) || !slices.Contains(got, nk) {
		t.Errorf("dumper 0.18.1 + loader 1.0.19 must adapt: %v", got)
	}
	setup("0.18.1", "1.0.19", def)
	if got = cl.myLoaderRestoreArgs(true); !slices.Equal(got, append(slices.Clone(defOld), "--source-data=0")) {
		t.Errorf("dumper 1.0.19 + loader 0.18.1 must not adapt: %v", got)
	}
	// dumper below 0.16.7: the existing removal of --innodb-optimize-keys=skip stays; the loader's own rules still apply
	setup("1.0.1", "0.16.5", def)
	if got = cl.myLoaderRestoreArgs(true); slices.Contains(got, nk) || slices.Contains(got, "--innodb-optimize-keys=skip") || !slices.Contains(got, dt) {
		t.Errorf("dumper 0.16.5 + loader 1.0.1: %v", got)
	}

	// older loaders and the boundaries
	for _, c := range []struct {
		loader   string
		d, k     bool
		hasOldOW bool
	}{{"0.18.1", false, false, true}, {"0.19.3", false, true, true}, {"0.20.1", true, true, false}} {
		setup(c.loader, "1.0.1", def)
		got = cl.myLoaderRestoreArgs(true)
		if slices.Contains(got, dt) != c.d || slices.Contains(got, nk) != c.k || slices.Contains(got, "--overwrite-tables") != c.hasOldOW {
			t.Errorf("loader %s: %v", c.loader, got)
		}
	}

	// unknown version (the probe fails): the arguments are what they were
	cl.Conf.BackupMyLoaderPath = t.TempDir() + "/missing"
	cl.Conf.BackupMyLoaderOptions = def
	if got = cl.myLoaderRestoreArgs(true); !slices.Equal(got, defOld) {
		t.Errorf("unknown version: %v", got)
	}

	// custom value: kept as it is
	setup("1.0.1", "1.0.1", "--overwrite-tables --verbose=3 --max-threads-for-schema-creation=2")
	if got = cl.myLoaderRestoreArgs(true); !slices.Equal(got, []string{"--overwrite-tables", "--verbose=3", "--max-threads-for-schema-creation=2", "--source-data=0"}) {
		t.Errorf("custom value rewritten: %v", got)
	}
	// the shipped default set explicitly (config file / environment / flag, or the dynamic map): kept as it is
	for _, kind := range []string{"immutable", "dynamic"} {
		setup("1.0.1", "1.0.1", def)
		if kind == "immutable" {
			cl.Conf.SetImmuableFlag("backup-myloader-options", def)
		} else {
			cl.Conf.SetDynamicFlag("backup-myloader-options", def)
		}
		if got = cl.myLoaderRestoreArgs(true); !slices.Equal(got, append(slices.Clone(defOld), "--source-data=0")) {
			t.Errorf("explicit (%s) value rewritten: %v", kind, got)
		}
	}
	// an explicit option of the user, which is not in the shipped list, is kept
	setup("1.0.1", "1.0.1", def+" --enable-binlog-x")
	if got = cl.myLoaderRestoreArgs(true); slices.Contains(got, dt) {
		t.Errorf("a custom value was adapted: %v", got)
	}
	cl.Conf.DeleteImmuableFlag("backup-myloader-options")
	cl.Conf.DeleteDynamicFlag("backup-myloader-options")
}

// Without the adaptation (master restore, parent seeding, out-of-scope callers) the refactoring builds exactly what
// myLoaderNoSourceChangeArgs(GetMyLoaderCompatibleOptions(), version) built before.
func TestMyLoaderRestoreArgsWithoutAdaptationIsUnchanged(t *testing.T) {
	_, srv := newStagingTestServer(t)
	cl := srv.ClusterGroup
	cl.VersionsMap = config.NewVersionsMap()
	for _, loader := range []string{"0.16.7", "0.17.1", "0.18.1", "0.19.1", "0.20.1", "0.21.4", "1.0.0", "1.0.19"} {
		for _, opts := range []string{config.DefaultBackupMyLoaderOptions, "--verbose=3", "--drop-table=DROP --optimize-keys=SKIP", ""} {
			path, _ := fakeMyLoader(t, loader)
			cl.Conf.BackupMyLoaderPath, cl.Conf.BackupMyLoaderOptions = path, opts
			cl.VersionsMap.Set("mydumper", mydumperVersion(loader))
			before := myLoaderNoSourceChangeArgs(cl.GetMyLoaderCompatibleOptions(), cl.myLoaderVersion())
			if got := cl.myLoaderRestoreArgs(false); !slices.Equal(got, before) {
				t.Errorf("loader %s opts %q: got %v, before the change %v", loader, opts, got, before)
			}
		}
	}
}

func TestAdaptsMyLoaderDefaultsOnlyForTheOrdinaryReplicaReseed(t *testing.T) {
	for _, c := range []struct{ replica, master, fromParent, want bool }{
		{true, false, false, true},   // ordinary replica reseed
		{false, false, false, false}, // rejoin by logical backup (JobReseedMyLoader) on a non-master node
		{true, true, false, false},   // restore on the master (--enable-binlog)
		{true, false, true, false},   // seeding from a parent cluster
		{false, true, false, false},
		{false, false, true, false},
		{true, true, true, false},
	} {
		if got := adaptsMyLoaderDefaults(c.replica, c.master, c.fromParent); got != c.want {
			t.Errorf("replica=%v master=%v fromParent=%v: %v, want %v", c.replica, c.master, c.fromParent, got, c.want)
		}
	}
}

func TestIsOrdinaryMyLoaderReseed(t *testing.T) {
	for _, c := range []struct {
		name                         string
		pitr, refreshing, stagingSrv bool
		want                         bool
	}{
		{"ordinary replica reseed", false, false, false, true},
		{"point-in-time restore", true, false, false, false},
		{"staging refresh", false, true, false, false},
		{"configured staging server", false, false, true, false},
		{"all of them", true, true, true, false},
	} {
		if got := isOrdinaryMyLoaderReseed(c.pitr, c.refreshing, c.stagingSrv); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The arguments myloader really receives from the callers of a restore, with the shipped default untouched and a
// myloader 0.20.1 (both options renamed): only the ordinary replica reseed gets them adapted. JobReseedMyLoader
// is what the rejoin by logical backup (JobFlashbackLogicalBackup) calls; the staging refresh reaches
// ProcessReseedLogical too.
func TestMyLoaderRestoreCallersAdaptOnlyTheOrdinaryReplicaReseed(t *testing.T) {
	const adapted, legacy = "--drop-table=DROP --verbose=3 --optimize-keys=SKIP", "--overwrite-tables --verbose=3 --innodb-optimize-keys=skip"
	run := func(t *testing.T, call func(env *reseedPositioningEnv)) string {
		t.Helper()
		env := newReseedPositioningEnv(t, "MariaDB", 11, 8)
		defer env.closeDB()
		argsFile := filepath.Join(filepath.Dir(env.marker), "args")
		script := "#!/bin/sh\ncase \"$1\" in --version) echo 'myloader v0.20.1-1, built against MySQL 8.4.9 with SSL support'; exit 0;; esac\necho \"$@\" > " + argsFile + "\nexit 1\n"
		if err := os.WriteFile(env.cl.Conf.BackupMyLoaderPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		env.cl.VersionsMap = config.NewVersionsMap()
		env.cl.VersionsMap.Set("mydumper", mydumperVersion("0.20.1"))
		env.cl.Conf.BackupMyLoaderOptions = config.DefaultBackupMyLoaderOptions
		call(env)
		b, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("myloader did not run: %v", err)
		}
		return string(b)
	}
	positioning := func(env *reseedPositioningEnv) {
		env.mock.ExpectExec("STOP (SLAVE|REPLICA)").WillReturnResult(sqlmock.NewResult(0, 0))
		env.mock.ExpectExec("CHANGE ").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	cases := []struct {
		name string
		want string // adapted or legacy
		call func(env *reseedPositioningEnv)
	}{
		{"rejoin by logical backup (JobReseedMyLoader)", legacy, func(env *reseedPositioningEnv) { _ = env.server.JobReseedMyLoader(env.backup, false) }},
		{"ordinary replica reseed (JobReseedMyLoaderReplica)", adapted, func(env *reseedPositioningEnv) { _ = env.server.JobReseedMyLoaderReplica(env.backup, false) }},
		{"ordinary replica reseed (ProcessReseedLogical)", adapted, func(env *reseedPositioningEnv) {
			positioning(env)
			_ = env.server.ProcessReseedLogical(env.task)
		}},
		{"staging refresh (ProcessReseedLogical)", legacy, func(env *reseedPositioningEnv) {
			env.cl.IsRefreshStaging = true
			positioning(env)
			_ = env.server.ProcessReseedLogical(env.task)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := run(t, c.call)
			if !strings.Contains(got, c.want) {
				t.Errorf("myloader args %q, want them to contain %q", got, c.want)
			}
			other := adapted
			if c.want == adapted {
				other = legacy
			}
			if strings.Contains(got, strings.Fields(other)[0]) {
				t.Errorf("myloader args %q carry %q of the other form", got, strings.Fields(other)[0])
			}
		})
	}
}
