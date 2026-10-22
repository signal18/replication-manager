package cluster

import (
	"slices"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

// The default backup-mydumper-options, as GetMyDumperCompatibleOptions returns them.
var mydumperDefaultArgs = []string{"--chunk-filesize=1000", "--compress", "--less-locking", "--verbose=3", "--triggers", "--routines", "--events", "--trx-consistency-only", "--kill-long-queries", "--source-data=2"}

func mydumperVersion(v string) *version.Version {
	dumper, _ := version.NewVersionFromString("mydumper", v)
	return dumper
}

func trxOptions(args []string) []string {
	var opts []string
	for _, a := range args {
		if a == "--trx-tables" || strings.HasPrefix(a, "--trx-tables=") || a == "--no-trx-tables" {
			opts = append(opts, a)
		}
	}
	return opts
}

// With the default options, every mydumper >= 0.18.1 gets the deprecated flags removed. MariaDB
// gets --trx-tables=0: in trx-tables mode (the default since 0.20.1) mydumper refuses its Aria
// system tables. MySQL / Percona get no trx-tables option: mydumper's default applies.
func TestMydumperTrxTablesArgsDefaultOptions(t *testing.T) {
	for _, v := range []string{"0.18.1", "0.19.3", "0.20.1", "0.21.1", "1.0.11"} {
		for _, mariadb := range []bool{true, false} {
			got, changes, warning := mydumperTrxTablesArgs(slices.Clone(mydumperDefaultArgs), mydumperVersion(v), mariadb)
			if slices.Contains(got, "--less-locking") || slices.Contains(got, "--trx-consistency-only") {
				t.Errorf("%s mariadb=%v: deprecated flag kept: %v", v, mariadb, got)
			}
			if !slices.Contains(got, "--source-data=2") || !slices.Contains(got, "--triggers") {
				t.Errorf("%s mariadb=%v: other options lost: %v", v, mariadb, got)
			}
			if warning != "" {
				t.Errorf("%s mariadb=%v: unexpected warning %q", v, mariadb, warning)
			}
			if len(changes) == 0 || !strings.Contains(changes[0], "--less-locking, --trx-consistency-only removed") {
				t.Errorf("%s mariadb=%v: removal not reported: %v", v, mariadb, changes)
			}
			want := []string(nil)
			if mariadb {
				want = []string{"--trx-tables=0"}
			}
			if opts := trxOptions(got); !slices.Equal(opts, want) {
				t.Errorf("%s mariadb=%v: trx-tables options %v, want %v", v, mariadb, opts, want)
			}
			if mariadb != (len(changes) == 2 && strings.HasPrefix(changes[1], "--trx-tables=0 added")) {
				t.Errorf("%s mariadb=%v: changes %v", v, mariadb, changes)
			}
		}
	}
}

// Nothing to adapt, nothing reported: the log line is written only when an option changed.
func TestMydumperTrxTablesArgsNothingChanged(t *testing.T) {
	args := []string{"--chunk-filesize=1000", "--source-data=2"}
	got, changes, warning := mydumperTrxTablesArgs(slices.Clone(args), mydumperVersion("1.0.11"), false)
	if !slices.Equal(got, args) || len(changes) != 0 || warning != "" {
		t.Fatalf("got %v, changes %v, warning %q; want the options unchanged, nothing reported", got, changes, warning)
	}
	got, changes, _ = mydumperTrxTablesArgs(slices.Clone(args), mydumperVersion("1.0.11"), true)
	if !slices.Equal(changes, []string{"--trx-tables=0 added (since mydumper 0.20.1, the Aria system tables of MariaDB are dumped only with --trx-tables=0)"}) || trxOptions(got)[0] != "--trx-tables=0" {
		t.Fatalf("mariadb without deprecated flags: got %v, changes %v", got, changes)
	}
}

// A trx-tables option configured is kept as it is and nothing is added; the warning names the
// cases that fail the backup.
func TestMydumperTrxTablesArgsConfiguredOption(t *testing.T) {
	cases := []struct {
		opt      string
		dumper   string
		mariadb  bool
		warnPart string // empty: no warning
	}{
		// MariaDB: refused in trx-tables mode since 0.20.1, only warned before
		{"--trx-tables", "0.19.3", true, ""},
		{"--trx-tables", "0.20.1", true, "the backup will fail"},
		{"--trx-tables", "1.0.11", true, "the backup will fail"},
		{"--trx-tables=1", "1.0.11", true, "the backup will fail"},
		{"--trx-tables=0", "1.0.11", true, ""},
		{"--trx-tables=0", "0.18.1", true, ""},
		// --no-trx-tables: unknown before 0.21.1, on every engine
		{"--no-trx-tables", "0.20.1", true, "knows only since 0.21.1"},
		{"--no-trx-tables", "0.19.3", false, "knows only since 0.21.1"},
		{"--no-trx-tables", "0.21.1", true, ""},
		{"--no-trx-tables", "1.0.11", false, ""},
		// MySQL / Percona: their system tables are InnoDB, the option is theirs to choose
		{"--trx-tables", "1.0.11", false, ""},
		{"--trx-tables=0", "1.0.11", false, ""},
	}
	for _, c := range cases {
		args := append(slices.Clone(mydumperDefaultArgs), c.opt)
		got, changes, warning := mydumperTrxTablesArgs(args, mydumperVersion(c.dumper), c.mariadb)
		if opts := trxOptions(got); !slices.Equal(opts, []string{c.opt}) {
			t.Errorf("%s %s mariadb=%v: trx-tables options %v, want only %s", c.opt, c.dumper, c.mariadb, opts, c.opt)
		}
		if len(changes) != 1 {
			t.Errorf("%s %s mariadb=%v: changes %v, want only the removal", c.opt, c.dumper, c.mariadb, changes)
		}
		if (c.warnPart == "") != (warning == "") || !strings.Contains(warning, c.warnPart) || (warning != "" && !strings.Contains(warning, c.opt)) {
			t.Errorf("%s %s mariadb=%v: warning %q, want one containing %q", c.opt, c.dumper, c.mariadb, warning, c.warnPart)
		}
	}
}
