package cluster

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The backups of this tool carry "myloader_exec_change_source = 1" (taken with --source-data=2):
// myloader then sends CHANGE REPLICATION SOURCE TO, which MariaDB refuses (1064) and, from
// mydumper 0.20.1, ends with exit 1 after the data is loaded. Run against MariaDB 10.5 to 12.2
// with myloader 1.0.1 and 1.0.11: --source-data=0 on myloader sends nothing, the data and the
// metadata position are intact, and the restore exits 0.
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
