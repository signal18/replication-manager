package cluster

// Account export recovery (#2436, follow-up of #2046).
//
// With skip_name_resolve=ON a server ignores the accounts whose host is a plain DNS name although they stay in the
// privilege catalogue: mariadb-dump --system=user (or the account section of --system=all) stops on the first of
// them, SHOW CREATE USER answering error 1133. A backup or a fresh-node account replay then used to fail, or to be
// refused (#2046). Now the failure is recovered when, and only when, it can be fully explained:
//
//   - the ordinary dump runs first, unchanged; nothing here runs on the ordinary path;
//   - when it stops on a SHOW CREATE USER 1133, the accounts-only dump is run again with --force, its only
//     use here (never on a data dump): its stderr must be observed completely and be exactly, for each account,
//     one SHOW CREATE USER error 1133 and one SHOW GRANTS error 1141, its exit status must be the expected 2,
//     and its output complete (the dump trailer);
//   - each account is confirmed on the live server, under the dump credentials (dbhelper.ConfirmAccountHiddenBySkipNameResolve);
//   - the output of the forced run is guarded: the only statements dropped are the two default-role statements
//     of the omitted account, a PROXY grant on it held by another account is kept, any other statement naming it
//     fails the export. Role memberships or other privileges of the omitted account are not in the output (the
//     server answers nothing for it), they go with the account.
//
// Anything else (an unexpected stderr line, a truncated or unobserved stream, a cancellation, a write failure,
// another exit status, an account that is not confirmed) fails the export with the tool's own words.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/hostmatch"
	"github.com/signal18/replication-manager/utils/version"
)

const (
	// accountDumpStderrMaxLines bounds the stderr lines kept of one accounts-only dump (T18): more is an unobserved stream.
	accountDumpStderrMaxLines = 200
	// accountDumpLineMax bounds one stderr or output line of the accounts-only dump.
	accountDumpLineMax = 4 << 20
	// accountsOmittedMarker introduces the omitted accounts in a job result: the tracked warning is derived from it.
	accountsOmittedMarker = " -- accounts omitted (skip_name_resolve): "
	// accountsOmittedListMax bounds the accounts named in a job result.
	accountsOmittedListMax = 20
)

var (
	reAccountShowCreateUser = regexp.MustCompile(`^(?:mariadb-dump|mysqldump): Couldn't execute 'SHOW CREATE USER '([^'\\]+)'@'([^'\\]+)'': .*\(1133\)$`)
	reAccountShowGrants     = regexp.MustCompile(`^(?:mariadb-dump|mysqldump): Couldn't execute 'SHOW GRANTS FOR '([^'\\]+)'@'([^'\\]+)'': .*\(1141\)$`)
	reDumpClientWarning     = regexp.MustCompile(`(?i)^(?:mariadb-dump|mysqldump): warning`)
)

// accountRef is an account as the dump names it.
type accountRef struct{ User, Host string }

func (a accountRef) String() string { return "'" + a.User + "'@'" + a.Host + "'" }

// accountExport is the result of exportAccounts: the account SQL in a temporary file the caller removes (Close),
// and the accounts left out of it.
type accountExport struct {
	Path    string
	Omitted []accountRef
}

// OmittedNames lists the omitted accounts as 'user'@'host'.
func (a *accountExport) OmittedNames() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.Omitted))
	for _, o := range a.Omitted {
		out = append(out, o.String())
	}
	return out
}

// Close removes the temporary file.
func (a *accountExport) Close() {
	if a != nil && a.Path != "" {
		_ = os.Remove(a.Path)
		a.Path = ""
	}
}

// accountDumpRun is what one run of the accounts-only dump showed.
type accountDumpRun struct {
	exitErr  error
	stderr   []string
	overflow bool // more stderr lines than accountDumpStderrMaxLines
	readErr  error
}

// accountDumpArgs are the arguments of the accounts-only dump of server. insertIgnore writes CREATE USER IF NOT EXISTS
// (the users sidecar and the live replay); without it the statements are the plain ones the account section of
// --system=all writes. force is only for the guarded recovery run.
func (server *ServerMonitor) accountDumpArgs(insertIgnore, force bool) []string {
	args := append(server.ClusterGroup.GetDumpCredentials(server), server.GetSSLClientParam("client-dump")...)
	if insertIgnore {
		args = append(args, "--insert-ignore")
	}
	args = append(args, "--system=user")
	if force {
		args = append(args, "--force")
	}
	return args
}

// runAccountDump runs the accounts-only dump of src into out and reports what it showed, stderr read to its end.
func (cluster *Cluster) runAccountDump(ctx context.Context, src *ServerMonitor, insertIgnore, force bool, out io.Writer) accountDumpRun {
	var run accountDumpRun
	cmd := exec.CommandContext(ctx, cluster.GetMysqlDumpPath(), src.accountDumpArgs(insertIgnore, force)...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Accounts of %s: %s", src.URL, strings.ReplaceAll(cmd.String(), "="+cluster.GetDbPass(), "=XXXX"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		run.readErr = err
		return run
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		run.readErr = err
		return run
	}
	if err := cmd.Start(); err != nil {
		run.exitErr = err
		return run
	}
	var wg sync.WaitGroup
	var copyErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		bw := bufio.NewWriterSize(out, 64*1024)
		_, copyErr = io.Copy(bw, stdout)
		if copyErr == nil {
			copyErr = bw.Flush()
		}
		if copyErr != nil {
			_, _ = io.Copy(io.Discard, stdout) // let the process end
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), accountDumpLineMax)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg, "[%s] %s", src.URL, line)
			if len(run.stderr) < accountDumpStderrMaxLines {
				run.stderr = append(run.stderr, line)
			} else {
				run.overflow = true
			}
		}
		if err := sc.Err(); err != nil {
			run.readErr = err
			_, _ = io.Copy(io.Discard, stderr)
		}
	}()
	wg.Wait()
	run.exitErr = cmd.Wait()
	if copyErr != nil {
		run.readErr = errors.Join(run.readErr, fmt.Errorf("write of the account dump: %w", copyErr))
	}
	return run
}

// accountDumpError is the failure of an ordinary accounts-only dump, with the tool's last stderr lines.
func accountDumpError(run accountDumpRun) error {
	err := run.exitErr
	if err == nil {
		err = run.readErr
	}
	if err == nil {
		err = errors.New("account dump failed")
	}
	tail := run.stderr
	if len(tail) > 5 {
		tail = tail[len(tail)-5:]
	}
	if len(tail) > 0 {
		return fmt.Errorf("mysqldump: %w | stderr: %s", err, strings.Join(tail, " / "))
	}
	return fmt.Errorf("mysqldump: %w", err)
}

// stopsOnShowCreateUser reports whether an accounts-only dump stopped on a SHOW CREATE USER error 1133: the failure the
// recovery is for. A trigger only: nothing is authorised by it.
func (run accountDumpRun) stopsOnShowCreateUser() bool {
	if run.exitErr == nil {
		return false
	}
	return slices.ContainsFunc(run.stderr, reAccountShowCreateUser.MatchString)
}

// isAccountExportFailureError is the same trigger for the error of a main dump, whose message carries the last stderr
// lines of the tool.
func isAccountExportFailureError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SHOW CREATE USER") && strings.Contains(msg, "(1133)")
}

// validateAccountRecoveryRun checks the forced accounts-only run: complete observation, the expected exit status and,
// for each account, exactly the pair of errors that an account the server ignores makes. It returns the accounts.
func validateAccountRecoveryRun(run accountDumpRun) ([]accountRef, error) {
	if run.readErr != nil {
		return nil, fmt.Errorf("the account dump was not observed completely: %w", run.readErr)
	}
	if run.overflow {
		return nil, fmt.Errorf("the account dump wrote more than %d stderr lines", accountDumpStderrMaxLines)
	}
	var ee *exec.ExitError
	if !errors.As(run.exitErr, &ee) || ee.ExitCode() != 2 {
		return nil, fmt.Errorf("the forced account dump did not end with the expected exit status 2: %v", run.exitErr)
	}
	type pair struct{ create, grants int }
	pairs := map[accountRef]*pair{}
	get := func(u, h string) *pair {
		k := accountRef{u, h}
		if pairs[k] == nil {
			pairs[k] = &pair{}
		}
		return pairs[k]
	}
	for _, line := range run.stderr {
		switch {
		case reAccountShowCreateUser.MatchString(line):
			m := reAccountShowCreateUser.FindStringSubmatch(line)
			get(m[1], m[2]).create++
		case reAccountShowGrants.MatchString(line):
			m := reAccountShowGrants.FindStringSubmatch(line)
			get(m[1], m[2]).grants++
		case reDumpClientWarning.MatchString(line):
			// a client warning is not an error, the ordinary dump runs with them as well
		default:
			return nil, fmt.Errorf("unexpected line from the account dump: %s", truncateForError(line))
		}
	}
	if len(pairs) == 0 {
		return nil, errors.New("the forced account dump reported no account it cannot export")
	}
	accts := make([]accountRef, 0, len(pairs))
	for a, p := range pairs {
		if p.create != 1 || p.grants != 1 {
			return nil, fmt.Errorf("account %s: expected one SHOW CREATE USER and one SHOW GRANTS error, got %d and %d", a, p.create, p.grants)
		}
		if !plainAccountRef(a) {
			return nil, fmt.Errorf("account %s has a name that needs escaping", a)
		}
		accts = append(accts, a)
	}
	sort.Slice(accts, func(i, j int) bool { return accts[i].String() < accts[j].String() })
	return accts, nil
}

func plainAccountRef(a accountRef) bool {
	for _, s := range []string{a.User, a.Host} {
		if s == "" || strings.ContainsAny(s, "'`\\\n\r") {
			return false
		}
	}
	return true
}

func truncateForError(s string) string {
	if len(s) > 240 {
		return s[:240] + "..."
	}
	return s
}

// accountDumpComplete checks the output file of a forced run ends with the dump trailer: a truncated output is never accepted.
func accountDumpComplete(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	const tail = 2048
	off := st.Size() - tail
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !strings.Contains(string(buf), "-- Dump completed") {
		return errors.New("the account dump has no completion trailer: output truncated")
	}
	return nil
}

// accountGuardGate are the beginnings of the statements of the account section of a dump: only these lines are looked at.
var accountGuardGate = []string{"CREATE USER", "CREATE ROLE", "DROP USER", "GRANT ", "REVOKE ", "ALTER USER", "SET DEFAULT ROLE", "SET PASSWORD",
	"RENAME USER", "/*M!100005", "/*!80001", "/*!80016", "/*M!100203"}

// guardAccountDump copies the forced account dump src to dst without the two default-role statements of each omitted
// account, keeps a PROXY grant on it held by another account (it replays as it stands, and is that account's own
// privilege), and fails on any other statement of the account section naming an omitted account.
func guardAccountDump(src, dst string, omitted []accountRef) (dropped, kept int, err error) {
	var forms []string
	for _, a := range omitted {
		if !plainAccountRef(a) {
			return 0, 0, fmt.Errorf("account %s has a name that needs escaping", a)
		}
		forms = append(forms, "'"+a.User+"'@'"+a.Host+"'", "`"+a.User+"`@`"+a.Host+"`")
	}
	quoted := make([]string, len(forms))
	for i, f := range forms {
		quoted[i] = regexp.QuoteMeta(f)
	}
	alt := strings.Join(quoted, "|")
	dropShapes := []*regexp.Regexp{
		regexp.MustCompile(`^/\*M!100005 SET DEFAULT ROLE .* FOR (` + alt + `) \*/;\s*$`),
		regexp.MustCompile(`^/\*!80001 ALTER USER (` + alt + `) DEFAULT ROLE .* \*/;\s*$`),
	}
	keepShape := regexp.MustCompile("^GRANT PROXY ON (" + alt + ") TO ")

	in, err := os.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, 0, err
	}
	bw := bufio.NewWriterSize(out, 64*1024)
	fail := func(e error) (int, int, error) {
		out.Close()
		_ = os.Remove(dst)
		return 0, 0, e
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), accountDumpLineMax)
	for sc.Scan() {
		line := sc.Text()
		if lineHasPrefix(line, accountGuardGate) && containsAny(line, forms) {
			switch {
			case dropShapes[0].MatchString(line) || dropShapes[1].MatchString(line):
				dropped++
				continue
			case keepShape.MatchString(line):
				kept++
			default:
				return fail(fmt.Errorf("unsupported statement naming an omitted account: %s", truncateForError(line)))
			}
		}
		if _, err := bw.WriteString(line + "\n"); err != nil {
			return fail(err)
		}
	}
	if err := sc.Err(); err != nil {
		return fail(err)
	}
	if err := bw.Flush(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return 0, 0, err
	}
	return dropped, kept, nil
}

func lineHasPrefix(line string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func containsAny(line string, subs []string) bool {
	for _, s := range subs {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// confirmOmittedAccounts confirms on the live server, under the dump credentials, that every account is one the server
// ignores because of skip_name_resolve (and not one dropped meanwhile or exportable now).
func (cluster *Cluster) confirmOmittedAccounts(src *ServerMonitor, accts []accountRef) error {
	db, err := src.GetNewDBConn()
	if err != nil {
		return fmt.Errorf("confirm the accounts on %s: %w", src.URL, err)
	}
	defer db.Close()
	for _, a := range accts {
		if !hostmatch.IsDNSHost(a.Host) {
			return fmt.Errorf("account %s: its host is not a DNS name", a)
		}
		hidden, why, err := dbhelper.ConfirmAccountHiddenBySkipNameResolve(db, src.DBVersion, a.User, a.Host)
		if err != nil {
			return fmt.Errorf("confirm account %s on %s: %w", a, src.URL, err)
		}
		if !hidden {
			return fmt.Errorf("account %s on %s is not confirmed as ignored by skip_name_resolve: %s", a, src.URL, why)
		}
	}
	return nil
}

// exportAccounts runs the accounts-only dump of src into a temporary file of dir. The ordinary run is the only one on
// the ordinary path. A run that stops on SHOW CREATE USER 1133 is recovered as described at the top of this file;
// every other failure is returned as it is.
func (cluster *Cluster) exportAccounts(ctx context.Context, src *ServerMonitor, dir string, insertIgnore bool) (*accountExport, error) {
	tmp, err := os.CreateTemp(dir, "accounts-*.sql.partial")
	if err != nil {
		return nil, fmt.Errorf("account dump of %s: %w", src.URL, err)
	}
	path := tmp.Name()
	fail := func(e error) (*accountExport, error) {
		tmp.Close()
		_ = os.Remove(path)
		_ = os.Remove(path + ".guarded")
		return nil, e
	}

	run := cluster.runAccountDump(ctx, src, insertIgnore, false, tmp)
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if run.exitErr == nil && run.readErr == nil {
		if err := tmp.Close(); err != nil {
			_ = os.Remove(path)
			return nil, fmt.Errorf("account dump of %s: %w", src.URL, err)
		}
		return &accountExport{Path: path}, nil
	}
	if !run.stopsOnShowCreateUser() {
		return fail(accountDumpError(run))
	}

	// the recovery: the same dump with --force, under full validation
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Accounts of %s: the dump stops on an account the server cannot show (SHOW CREATE USER, error 1133); checking whether skip_name_resolve explains it", src.URL)
	if err := tmp.Truncate(0); err != nil {
		return fail(err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	forced := cluster.runAccountDump(ctx, src, insertIgnore, true, tmp)
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	accts, err := validateAccountRecoveryRun(forced)
	if err != nil {
		return fail(fmt.Errorf("%w; the first dump failed: %v", err, accountDumpError(run)))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("account dump of %s: %w", src.URL, err)
	}
	if err := accountDumpComplete(path); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := cluster.confirmOmittedAccounts(src, accts); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("%w; the first dump failed: %v", err, accountDumpError(run))
	}
	guarded := path + ".guarded"
	dropped, kept, err := guardAccountDump(path, guarded, accts)
	_ = os.Remove(path)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(guarded, path); err != nil {
		_ = os.Remove(guarded)
		return nil, err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Accounts of %s: %d account(s) ignored by skip_name_resolve are left out of the export (%d default-role statement(s) dropped, %d PROXY grant(s) on them kept): %s", src.URL, len(accts), dropped, kept, strings.Join(accountNames(accts), ", "))
	return &accountExport{Path: path, Omitted: accts}, nil
}

func accountNames(accts []accountRef) []string {
	out := make([]string, 0, len(accts))
	for _, a := range accts {
		out = append(out, a.String())
	}
	return out
}

// accountOmissionResult is the job result of a job that completed with accounts left out: the base text, then the accounts.
func accountOmissionResult(base string, omitted []string) string {
	if len(omitted) == 0 {
		return base
	}
	list := omitted
	more := ""
	if len(list) > accountsOmittedListMax {
		more = fmt.Sprintf(" (and %d more)", len(list)-accountsOmittedListMax)
		list = list[:accountsOmittedListMax]
	}
	return base + accountsOmittedMarker + strings.Join(list, ", ") + more
}

// accountsOmittedFromResult returns the accounts text of a job result written by accountOmissionResult, "" when none.
func accountsOmittedFromResult(result string) string {
	_, after, found := strings.Cut(result, accountsOmittedMarker)
	if !found {
		return ""
	}
	return after
}

// mariadbDumpAccountTables are the privilege tables, views and role tables mariadb-dump leaves out of the data it dumps
// when it dumps the accounts itself (--system=all / users): the exclusion list of the client source, identical in
// 11.4, 11.8 and 12.2. Naming one a server does not have is harmless.
var mariadbDumpAccountTables = []string{"db", "global_priv", "tables_priv", "columns_priv", "procs_priv", "user", "host", "proxies_priv",
	"roles_mapping", "role_edges", "default_roles"}

// mariadbDumpSystemWithoutUsers are the categories of --system=all other than the accounts.
const mariadbDumpSystemWithoutUsers = "plugins,udfs,servers,stats,timezones"

// accountRecoveryDumpArgs rewrites the arguments of a main MariaDB dump for the recovery run: its accounts come from the
// guarded accounts-only export, appended to the artifact, so --system=all becomes the other categories and the account
// tables are left out of the data as the client leaves them out of --system=all. It never forces anything. It refuses,
// so the failure stays, when the semantics are not established: a client that is not MariaDB's from 11.4 (the first
// release whose source was checked), or an option string that does not carry exactly --system=all.
func accountRecoveryDumpArgs(args []string, client *version.Version) ([]string, error) {
	if client == nil || !client.IsMariaDB() || !client.GreaterEqual("11.4") {
		return nil, errors.New("the dump client is not a MariaDB client from 11.4: its account exclusions are not established")
	}
	i := slices.Index(args, "--system=all")
	if i < 0 {
		return nil, errors.New("the dump options do not carry --system=all: they are not rewritten")
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--system=") && a != "--system=all" {
			return nil, errors.New("the dump options carry several --system selections: they are not rewritten")
		}
	}
	out := slices.Clone(args)
	out[i] = "--system=" + mariadbDumpSystemWithoutUsers
	for _, t := range mariadbDumpAccountTables {
		if opt := "--ignore-table=mysql." + t; !slices.Contains(out, opt) {
			out = append(out, opt)
		}
	}
	return out, nil
}

// accountRecoveryApplicable tells whether the recovery of a main dump that stopped on an account can run at all; the error says
// why it can not, and the first failure then stays as it was. A dump run on the DB node is not recovered (its stderr does not
// reach this process, the validation would be blind); the rewrite needs a client and options whose semantics are established.
func accountRecoveryApplicable(remote bool, dumpArgs []string, client *version.Version) error {
	if remote {
		return errors.New("the dump runs on the DB node: its stderr does not reach replication-manager")
	}
	_, err := accountRecoveryDumpArgs(dumpArgs, client)
	return err
}

// appendAccountExport writes the accounts of acc to w, after the main dump: appended, never prepended (a dump header
// after a system section would send its saved session settings to the system artifact, its restores staying with the data).
func appendAccountExport(w io.Writer, acc *accountExport) error {
	f, err := os.Open(acc.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("append the accounts to the dump: %w", err)
	}
	return nil
}
