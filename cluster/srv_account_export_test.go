package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// #2436: the recovery of an account export that stops on an account skip_name_resolve makes the server ignore.

func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("exit %d returned no error", code)
	}
	return err
}

const (
	lineCreate = "mariadb-dump: Couldn't execute 'SHOW CREATE USER 'dnsacct'@'repman.example.test'': Can't find any matching row in the user table (1133)"
	lineGrants = "mariadb-dump: Couldn't execute 'SHOW GRANTS FOR 'dnsacct'@'repman.example.test'': There is no such grant defined for user 'dnsacct' on host 'repman.example.test' (1141)"
)

func TestValidateAccountRecoveryRun(t *testing.T) {
	good := accountDumpRun{exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants}}
	accts, err := validateAccountRecoveryRun(good)
	if err != nil || len(accts) != 1 || accts[0].String() != "'dnsacct'@'repman.example.test'" {
		t.Fatalf("the expected pair is accepted: %v %v", accts, err)
	}
	withWarning := good
	withWarning.stderr = []string{"mariadb-dump: Warning: something harmless", lineCreate, lineGrants}
	if _, err := validateAccountRecoveryRun(withWarning); err != nil {
		t.Fatalf("a client warning is not an error: %v", err)
	}
	for name, run := range map[string]accountDumpRun{
		"another exit status":      {exitErr: exitError(t, 1), stderr: []string{lineCreate, lineGrants}},
		"no error at all":          {exitErr: nil, stderr: []string{lineCreate, lineGrants}},
		"an unexpected error line": {exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants, "mariadb-dump: Couldn't execute 'SHOW FIELDS FROM `vbad`': View 'app.vbad' references invalid table(s) (1356)"}},
		"an unknown line":          {exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants, "something else"}},
		"grants error missing":     {exitErr: exitError(t, 2), stderr: []string{lineCreate}},
		"create error missing":     {exitErr: exitError(t, 2), stderr: []string{lineGrants}},
		"the pair twice":           {exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants, lineCreate}},
		"stderr overflow":          {exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants}, overflow: true},
		"stderr not read to end":   {exitErr: exitError(t, 2), stderr: []string{lineCreate, lineGrants}, readErr: errors.New("bufio.Scanner: token too long")},
		"no account named":         {exitErr: exitError(t, 2), stderr: nil},
		"a quote in the name": {exitErr: exitError(t, 2), stderr: []string{
			"mariadb-dump: Couldn't execute 'SHOW CREATE USER 'a\\'b'@'h.example'': x (1133)", "mariadb-dump: Couldn't execute 'SHOW GRANTS FOR 'a\\'b'@'h.example'': x (1141)"}},
	} {
		if _, err := validateAccountRecoveryRun(run); err == nil {
			t.Errorf("%s: must fail closed", name)
		}
	}
	two := accountDumpRun{exitErr: exitError(t, 2), stderr: []string{
		lineCreate, lineGrants,
		"mariadb-dump: Couldn't execute 'SHOW CREATE USER 'other'@'b.example.test'': Can't find any matching row in the user table (1133)",
		"mariadb-dump: Couldn't execute 'SHOW GRANTS FOR 'other'@'b.example.test'': There is no such grant (1141)"}}
	if accts, err := validateAccountRecoveryRun(two); err != nil || len(accts) != 2 {
		t.Fatalf("two accounts, each with its pair: %v %v", accts, err)
	}
}

// the real output of a forced accounts-only dump (E2), shortened
const forcedAccountDump = `-- MariaDB dump 10.19-11.4.13-MariaDB, for debian-linux-gnu (x86_64)
/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
CREATE USER ` + "`appuser`@`%`" + ` IDENTIFIED BY PASSWORD '*0000000000000000000000000000000000000000';
CREATE USER ` + "`roleuser`@`%`" + ` IDENTIFIED BY PASSWORD '*0000000000000000000000000000000000000000';
CREATE ROLE IF NOT EXISTS mariadb_dump_import_role;
GRANT mariadb_dump_import_role TO CURRENT_USER();
SET ROLE mariadb_dump_import_role;
/*M!100005 SET DEFAULT ROLE NONE FOR 'appuser'@'%' */;
/*!80001 ALTER USER 'appuser'@'%' DEFAULT ROLE NONE */;
/*M!100005 SET DEFAULT ROLE 'app_role' FOR 'dnsacct'@'repman.example.test' */;
/*!80001 ALTER USER 'dnsacct'@'repman.example.test' DEFAULT ROLE 'app_role' */;
GRANT USAGE ON *.* TO ` + "`appuser`@`%`" + `;
GRANT PROXY ON ` + "`dnsacct`@`repman.example.test`" + ` TO ` + "`proxyuser`@`%`" + `;
DROP ROLE mariadb_dump_import_role;
-- Dump completed on 2026-11-26  2:06:37
`

func TestGuardAccountDump(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.sql")
	dst := filepath.Join(dir, "out.sql")
	omitted := []accountRef{{"dnsacct", "repman.example.test"}}
	if err := os.WriteFile(src, []byte(forcedAccountDump), 0o600); err != nil {
		t.Fatal(err)
	}
	dropped, kept, err := guardAccountDump(src, dst, omitted)
	if err != nil || dropped != 2 || kept != 1 {
		t.Fatalf("dropped=%d kept=%d err=%v", dropped, kept, err)
	}
	out, _ := os.ReadFile(dst)
	got := string(out)
	if strings.Contains(got, "SET DEFAULT ROLE 'app_role' FOR 'dnsacct'") || strings.Contains(got, "ALTER USER 'dnsacct'") {
		t.Error("the default-role statements of the omitted account are dropped")
	}
	for _, must := range []string{"SET DEFAULT ROLE NONE FOR 'appuser'@'%'", "GRANT PROXY ON `dnsacct`@`repman.example.test` TO `proxyuser`@`%`",
		"CREATE USER `roleuser`@`%`", "DROP ROLE mariadb_dump_import_role;", "-- Dump completed"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing from the guarded output: %s", must)
		}
	}
	if strings.Count(got, "\n") != strings.Count(forcedAccountDump, "\n")-2 {
		t.Error("only the two statements are removed")
	}

	for name, bad := range map[string]string{
		"a grant to the omitted account":      "GRANT SELECT ON `app`.* TO `dnsacct`@`repman.example.test`;\n",
		"a role administered by it":           "/*M!100005 CREATE ROLE 'r' WITH ADMIN 'dnsacct'@'repman.example.test' */;\n",
		"a creation of it":                    "CREATE USER `dnsacct`@`repman.example.test` IDENTIFIED BY PASSWORD '*X';\n",
		"a default role statement of another": "/*M!100005 SET DEFAULT ROLE 'r' FOR 'dnsacct'@'repman.example.test' extra */;\n",
	} {
		if err := os.WriteFile(src, []byte(forcedAccountDump+bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := guardAccountDump(src, dst, omitted); err == nil {
			t.Errorf("%s: must fail the export", name)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Errorf("%s: no guarded output is left", name)
		}
	}
	// outside the account section nothing is looked at: a row naming the account is data
	row := "INSERT INTO `t` VALUES (1,'created by dnsacct@repman.example.test');\n"
	if err := os.WriteFile(src, []byte(forcedAccountDump+row), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := guardAccountDump(src, dst, omitted); err != nil {
		t.Errorf("a data row is not an account statement: %v", err)
	}
}

func TestAccountDumpComplete(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.sql")
	_ = os.WriteFile(p, []byte(forcedAccountDump), 0o600)
	if err := accountDumpComplete(p); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(p, []byte(strings.TrimSuffix(forcedAccountDump, "-- Dump completed on 2026-11-26  2:06:37\n")), 0o600)
	if accountDumpComplete(p) == nil {
		t.Fatal("a truncated output is refused")
	}
}

func TestAccountRecoveryDumpArgs(t *testing.T) {
	m1011, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	m114, _ := version.NewVersionFromString("MariaDB", "11.4.13-MariaDB")
	m118, _ := version.NewVersionFromString("MariaDB", "11.8.9-MariaDB")
	my84, _ := version.NewVersionFromString("MySQL", "8.4.13")
	args := []string{"--hex-blob", "--single-transaction", "--all-databases", "--system=all", "--master-data=2", "--host=db1", "--ignore-table=mysql.user", "--custom-flag"}

	for _, c := range []*version.Version{m114, m118} {
		out, err := accountRecoveryDumpArgs(args, c)
		if err != nil {
			t.Fatalf("client %s: %v", c.ToString(), err)
		}
		if slices.Contains(out, "--system=all") || !slices.Contains(out, "--system=plugins,udfs,servers,stats,timezones") {
			t.Errorf("the accounts category leaves --system: %v", out)
		}
		for _, tbl := range []string{"db", "global_priv", "tables_priv", "columns_priv", "procs_priv", "user", "host", "proxies_priv", "roles_mapping", "role_edges", "default_roles"} {
			if !slices.Contains(out, "--ignore-table=mysql."+tbl) {
				t.Errorf("mysql.%s is not left out of the data", tbl)
			}
		}
		if n := strings.Count(strings.Join(out, " "), "--ignore-table=mysql.user"); n != 1 {
			t.Errorf("mysql.user ignored %d times", n)
		}
		for _, keep := range []string{"--hex-blob", "--single-transaction", "--all-databases", "--master-data=2", "--host=db1", "--custom-flag"} {
			if !slices.Contains(out, keep) {
				t.Errorf("an unrelated option is dropped: %s", keep)
			}
		}
		if slices.Contains(out, "--force") || slices.Contains(out, "-f") {
			t.Error("the data dump is never forced")
		}
	}
	if !slices.Contains(args, "--system=all") {
		t.Error("the input is not modified")
	}
	for name, c := range map[string]*version.Version{"older MariaDB client": m1011, "MySQL client": my84, "unknown client": nil} {
		if _, err := accountRecoveryDumpArgs(args, c); err == nil {
			t.Errorf("%s: semantics not established, must refuse", name)
		}
	}
	if _, err := accountRecoveryDumpArgs([]string{"--system=users,plugins"}, m114); err == nil {
		t.Error("another --system selection is not rewritten")
	}
	if _, err := accountRecoveryDumpArgs([]string{"--all-databases"}, m114); err == nil {
		t.Error("no --system=all: nothing to rewrite")
	}
	if _, err := accountRecoveryDumpArgs([]string{"--system=all", "--system=users"}, m114); err == nil {
		t.Error("several --system selections are not rewritten")
	}
}

func TestAccountOmissionResult(t *testing.T) {
	if got := accountOmissionResult("Backup completed", nil); got != "Backup completed" {
		t.Fatalf("no omission: the base text unchanged, got %q", got)
	}
	got := accountOmissionResult("Backup completed", []string{"'a'@'x.example'", "'b'@'y.example'"})
	if accountsOmittedFromResult(got) != "'a'@'x.example', 'b'@'y.example'" {
		t.Fatalf("round trip: %q", got)
	}
	if accountsOmittedFromResult("Backup completed") != "" {
		t.Fatal("no marker, no accounts")
	}
	var many []string
	for i := 0; i < 50; i++ {
		many = append(many, fmt.Sprintf("'u%d'@'h.example'", i))
	}
	if r := accountOmissionResult("Backup completed", many); !strings.Contains(r, "(and 30 more)") || strings.Contains(r, "u49") {
		t.Fatalf("the list is bounded: %q", r)
	}
	if !slices.Equal(mergeOmittedAccounts([]string{"b", "a"}, []string{"a", "c"}), []string{"a", "b", "c"}) {
		t.Fatal("merge sorts and de-duplicates")
	}
}

func TestIsAccountExportFailureError(t *testing.T) {
	if !isAccountExportFailureError(fmt.Errorf("mysqldump: exit status 2 | stderr: x / %s", lineCreate)) {
		t.Fatal("the dump stopped on SHOW CREATE USER 1133")
	}
	if isAccountExportFailureError(errors.New("mysqldump: exit status 2 | stderr: mariadb-dump: Couldn't execute 'SHOW FIELDS FROM `v`': (1356)")) || isAccountExportFailureError(nil) {
		t.Fatal("another failure is not the trigger")
	}
}

// the tracked warning follows the latest finished backup or reseed job only
func TestLastAccountsOmittedJob(t *testing.T) {
	s := &ServerMonitor{URL: "db1:3306", JobResults: config.NewTasksMap()}
	if task, _ := s.LastAccountsOmittedJob(); task != "" {
		t.Fatal("no job: no warning")
	}
	s.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 1, State: 3, End: 100, Result: accountOmissionResult("Backup completed", []string{"'a'@'x.example'"})})
	if task, accts := s.LastAccountsOmittedJob(); task != "mysqldump" || accts != "'a'@'x.example'" {
		t.Fatalf("task=%q accounts=%q", task, accts)
	}
	s.JobResults.Set("reseedmydumper", &config.Task{Task: "reseedmydumper", Done: 1, State: 3, End: 200, Result: "Reseed completed"})
	if task, _ := s.LastAccountsOmittedJob(); task != "" {
		t.Fatal("a later job that left nothing out closes it")
	}
	s.JobResults.Set("reseedmydumper", &config.Task{Task: "reseedmydumper", Done: 1, State: 3, End: 300, Result: accountOmissionResult("Reseed completed", []string{"'b'@'y.example'"})})
	if task, accts := s.LastAccountsOmittedJob(); task != "reseedmydumper" || accts != "'b'@'y.example'" {
		t.Fatalf("a reseed that replayed without accounts: task=%q accounts=%q", task, accts)
	}
	s.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 1, State: 5, End: 400, Result: accountOmissionResult("x", []string{"'z'@'z.example'"})})
	if task, _ := s.LastAccountsOmittedJob(); task != "reseedmydumper" {
		t.Fatalf("a failed job never counts: %q", task)
	}
}

// fake dump client: an ordinary run stops on the hidden account; a forced run writes what E2 showed and exits 2.
const fakeDumpClient = `#!/bin/sh
force=0
for a in "$@"; do [ "$a" = "--force" ] && force=1; done
if [ "$MODE" = "unrelated" ]; then echo "mariadb-dump: Couldn't execute 'SHOW FIELDS FROM v': broken (1356)" >&2; exit 2; fi
if [ "$MODE" = "ok" ]; then cat "$FIXTURE"; exit 0; fi
if [ $force = 0 ]; then
  printf '%s\n' "-- MariaDB dump" "CREATE USER ` + "`appuser`@`%`" + `;" ; echo "` + lineCreate + `" >&2; exit 2
fi
cat "$FIXTURE"
echo "` + lineCreate + `" >&2
echo "` + lineGrants + `" >&2
exit 2
`

func accountExportTestServer(t *testing.T, mode string) (*ServerMonitor, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "mysqldump")
	if err := os.WriteFile(script, []byte(fakeDumpClient), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.sql")
	if err := os.WriteFile(fixture, []byte(forcedAccountDump), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODE", mode)
	t.Setenv("FIXTURE", fixture)
	cl := newEventLogTestCluster(t, 1)
	cl.Conf.BackupMysqldumpPath = script
	cl.VersionsMap = config.NewVersionsMap()
	srv := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: cl, LastTLSConfig: ConstTLSNoConfig}
	return srv, dir
}

func TestExportAccountsOrdinaryRunUnchanged(t *testing.T) {
	srv, dir := accountExportTestServer(t, "ok")
	acc, err := srv.ClusterGroup.exportAccounts(context.Background(), srv, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer acc.Close()
	if len(acc.Omitted) != 0 {
		t.Fatalf("nothing omitted on the ordinary path: %v", acc.Omitted)
	}
	b, _ := os.ReadFile(acc.Path)
	if string(b) != forcedAccountDump {
		t.Fatal("the ordinary output is passed through as it is")
	}
}

func TestExportAccountsUnrelatedFailureStaysFatal(t *testing.T) {
	srv, dir := accountExportTestServer(t, "unrelated")
	_, err := srv.ClusterGroup.exportAccounts(context.Background(), srv, dir, true)
	if err == nil || !strings.Contains(err.Error(), "1356") {
		t.Fatalf("an unrelated failure is returned with the tool's words: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "accounts-*")); len(left) != 0 {
		t.Fatalf("no temporary file is left: %v", left)
	}
}

// recovery needs the live confirmation: with no server to ask, the export fails closed and leaves nothing behind
func TestExportAccountsNotConfirmedFailsClosed(t *testing.T) {
	srv, dir := accountExportTestServer(t, "hidden")
	_, err := srv.ClusterGroup.exportAccounts(context.Background(), srv, dir, true)
	if err == nil {
		t.Fatal("an account that is not confirmed on the live server must not be omitted")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "accounts-*")); len(left) != 0 {
		t.Fatalf("no temporary file is left: %v", left)
	}
}

func TestExportAccountsCancelled(t *testing.T) {
	srv, dir := accountExportTestServer(t, "hidden")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := srv.ClusterGroup.exportAccounts(ctx, srv, dir, true); err == nil {
		t.Fatal("a cancelled export fails")
	}
}

// ---- #2436 validation round: unsupported paths fail safely, I/O failures never give a completed artifact ----

type failingWriter struct{ after, written int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.after {
		return 0, errors.New("injected write failure (disk full)")
	}
	w.written += len(p)
	return len(p), nil
}

func TestAccountRecoveryApplicable(t *testing.T) {
	m114, _ := version.NewVersionFromString("MariaDB", "11.4.13-MariaDB")
	m1011, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	args := []string{"--all-databases", "--system=all", "--master-data=2"}
	if err := accountRecoveryApplicable(false, args, m114); err != nil {
		t.Fatalf("a local dump with --system=all and a 11.4 client is recoverable: %v", err)
	}
	for name, c := range map[string]struct {
		remote bool
		args   []string
		client *version.Version
	}{
		"dump run on the DB node":      {true, args, m114},
		"older dump client":            {false, args, m1011},
		"custom system categories":     {false, []string{"--all-databases", "--system=users,plugins"}, m114},
		"no --system at all":           {false, []string{"--all-databases"}, m114},
		"unknown client":               {false, args, nil},
		"--system=all and another one": {false, []string{"--system=all", "--system=users"}, m114},
	} {
		if err := accountRecoveryApplicable(c.remote, c.args, c.client); err == nil {
			t.Errorf("%s: the recovery must not apply, the first failure stays", name)
		}
	}
}

// a write failure while the account dump is copied is a failed export, never a short but accepted one
func TestRunAccountDumpWriteFailureIsFailure(t *testing.T) {
	srv, _ := accountExportTestServer(t, "ok")
	run := srv.ClusterGroup.runAccountDump(context.Background(), srv, true, false, &failingWriter{after: 10})
	if run.readErr == nil {
		t.Fatalf("a failed write must be reported: %+v", run)
	}
	if !strings.Contains(run.readErr.Error(), "write of the account dump") {
		t.Fatalf("the failure names the write: %v", run.readErr)
	}
}

func TestAppendAccountExportWriteFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.sql")
	_ = os.WriteFile(p, []byte(forcedAccountDump), 0o600)
	if err := appendAccountExport(&failingWriter{after: 10}, &accountExport{Path: p}); err == nil {
		t.Fatal("a failed append must be an error: the artifact would end without its accounts")
	}
	var sb strings.Builder
	if err := appendAccountExport(&sb, &accountExport{Path: p}); err != nil || sb.String() != forcedAccountDump {
		t.Fatalf("a good append copies the export as it is: %v", err)
	}
	if err := appendAccountExport(&sb, &accountExport{Path: filepath.Join(dir, "missing.sql")}); err == nil {
		t.Fatal("a missing export is an error")
	}
}

func TestGuardAccountDumpWriteFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.sql")
	_ = os.WriteFile(src, []byte(forcedAccountDump), 0o600)
	dst := filepath.Join(dir, "no-such-dir", "out.sql")
	if _, _, err := guardAccountDump(src, dst, []accountRef{{"dnsacct", "repman.example.test"}}); err == nil {
		t.Fatal("an unwritable destination is an error")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("no guarded output is left")
	}
}

// the sidecar is published only if its unpublished file is there: a publication that can not happen is an error
func TestPublishUsersSidecar(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.Name = "c1"
	srv := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: cl}
	if err := srv.publishUsersSidecar(); err == nil {
		t.Fatal("nothing staged: publishing is an error, not a silent success")
	}
	dir := srv.GetMyBackupDirectory()
	final := filepath.Join(dir, "mysql.users.sql.gz")
	_ = os.WriteFile(final, []byte("previous"), 0o600)
	_ = os.WriteFile(final+usersSidecarPartialSuffix, []byte("new"), 0o600)
	if err := srv.publishUsersSidecar(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(final); string(b) != "new" {
		t.Fatalf("the staged sidecar replaces the previous one only when published: %q", b)
	}
	if _, err := os.Stat(final + usersSidecarPartialSuffix); err == nil {
		t.Fatal("the unpublished file is gone once published")
	}
	_ = os.WriteFile(final+usersSidecarPartialSuffix, []byte("staged"), 0o600)
	srv.discardUsersSidecar()
	if b, _ := os.ReadFile(final); string(b) != "new" {
		t.Fatalf("discarding keeps the previous sidecar: %q", b)
	}
	if _, err := os.Stat(final + usersSidecarPartialSuffix); err == nil {
		t.Fatal("discard removes the unpublished sidecar")
	}
}
