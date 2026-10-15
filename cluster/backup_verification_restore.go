package cluster

import (
	"bufio"
	"bytes"
	stdgzip "compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/sqlmask"
	"github.com/signal18/replication-manager/utils/version"
)

// The restore level of backup verification (#2023): the backup is loaded into
// backup-verification-host, a server outside the cluster, then each table's rows and each
// schema's objects are compared with what the dump holds. The whole dump is loaded, the
// mysql schema included: the verification credential must be an account the backups hold.
// The application schemas of the backup are dropped before the load and after the
// comparison; the system schemas are loaded, not compared (the server keeps its own rows
// there). Replication statements are not run: a backup made before the position was
// written as a comment has an active CHANGE MASTER and START SLAVE, which would make the
// verification server replicate from the cluster; the GTID state is replication state too,
// not data.

// backupVerificationProblemsMax bounds the differences a failed comparison reports.
const backupVerificationProblemsMax = 10

// backupVerificationSkippedSchemas are not loaded on an external server: replication-
// manager's own state of the source (its jobs), not data, which on a server replication-
// manager monitors would replace that server's own.
var backupVerificationSkippedSchemas = map[string]bool{"replication_manager_schema": true}

// backupVerificationSystemSchemas are loaded, not dropped nor compared.
var backupVerificationSystemSchemas = map[string]bool{"mysql": true, "sys": true, "information_schema": true, "performance_schema": true}

// mariadbSandboxLine is the first line of a MariaDB 11 dump, a client command: the server
// would answer "Query was empty".
const mariadbSandboxLine = `/*M!999999\- enable the sandbox mode */`

// dumpIdentifier is a quoted or a plain identifier.
const dumpIdentifier = "`(?:[^`]|``)+`|[A-Z0-9_$]+"

var (
	versionedCommentOpen = regexp.MustCompile(`/\*M?!\d*\s?`)
	stmtUse              = regexp.MustCompile("^USE\\s+`((?:[^`]|``)+)`")
	stmtCreateDatabase   = regexp.MustCompile("^CREATE\\s+(?:DATABASE|SCHEMA)\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`((?:[^`]|``)+)`")
	stmtCreateTable      = regexp.MustCompile("^CREATE\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?(?:`(?:[^`]|``)+`\\.)?`((?:[^`]|``)+)`")
	// a trigger keeps the text it was created with: its name can be plain and qualified
	// (TRIGGER app.trg), not only quoted as the client writes the other objects
	stmtCreateObject = regexp.MustCompile("^CREATE\\s+(?:\\S+\\s+)*?(VIEW|PROCEDURE|FUNCTION|TRIGGER|EVENT)\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?(?:(" + dumpIdentifier + ")\\.)?(" + dumpIdentifier + ")")
	stmtInsert       = regexp.MustCompile("^(?:INSERT|REPLACE)\\s+(?:IGNORE\\s+)?INTO\\s+(?:`(?:[^`]|``)+`\\.)?`((?:[^`]|``)+)`")
	stmtReplication  = regexp.MustCompile(`^(?:CHANGE\s+(?:MASTER|REPLICATION\s+SOURCE)\b|(?:START|STOP)\s+(?:ALL\s+)?(?:SLAVES?|REPLICAS?)\b|RESET\s+(?:SLAVE|REPLICA)\b|SET\s+(?:@@)?(?:GLOBAL[ .]\s*)?(?:GTID_PURGED|GTID_SLAVE_POS)\b)`)
)

// dumpInventory is what a dump creates: its schemas, and per schema the rows of each table
// and the names of its tables, views, procedures, functions, triggers and events.
type dumpInventory struct {
	schemas []string
	rows    map[string]map[string]int64           // schema -> table -> rows
	objects map[string]map[string]map[string]bool // schema -> kind -> name
}

func newDumpInventory() *dumpInventory {
	return &dumpInventory{rows: map[string]map[string]int64{}, objects: map[string]map[string]map[string]bool{}}
}

func (inv *dumpInventory) addSchema(schema string) {
	if _, ok := inv.objects[schema]; ok {
		return
	}
	inv.schemas = append(inv.schemas, schema)
	inv.objects[schema] = map[string]map[string]bool{}
	inv.rows[schema] = map[string]int64{}
}

func (inv *dumpInventory) addObject(schema, kind, name string) {
	inv.addSchema(schema)
	if inv.objects[schema][kind] == nil {
		inv.objects[schema][kind] = map[string]bool{}
	}
	inv.objects[schema][kind][name] = true
}

// normalizeDumpStatement opens the versioned comments of a statement's start
// (/*!50003 CREATE*/ /*!50017 DEFINER=...*/ /*!50003 TRIGGER ...*/) and collapses its
// whitespace, for the statement patterns: upper is head upper-cased, same offsets.
func normalizeDumpStatement(stmt string) (head, upper string) {
	head = stmt[:min(len(stmt), 1024)]
	head = versionedCommentOpen.ReplaceAllString(head, "")
	head = strings.Join(strings.Fields(strings.ReplaceAll(head, "*/", " ")), " ")
	return head, strings.ToUpper(head)
}

// nameAt returns the name group of a pattern matched in upper, read from head.
func nameAt(re *regexp.Regexp, head, upper string, group int) (string, bool) {
	loc := re.FindStringSubmatchIndex(upper)
	if loc == nil {
		return "", false
	}
	return strings.ReplaceAll(head[loc[2*group]:loc[2*group+1]], "``", "`"), true
}

// countInsertRows counts the rows of an INSERT ... VALUES (...),(...): the parentheses
// opened at depth 0 after VALUES, outside quoted strings.
func countInsertRows(stmt string) int64 {
	i := valuesKeyword(stmt)
	if i < 0 {
		return 0
	}
	var n int64
	depth := 0
	var quote byte
	for j := i + len(" VALUES"); j < len(stmt); j++ {
		c := stmt[j]
		if quote != 0 {
			switch {
			case c == '\\':
				j++
			case c == quote && j+1 < len(stmt) && stmt[j+1] == quote:
				j++
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			if depth == 0 {
				n++
			}
			depth++
		case ')':
			depth--
		}
	}
	return n
}

// valuesKeyword returns where the VALUES keyword of an INSERT starts, -1 without: the
// first " VALUES" in any case, wherever the column list of a complete insert ends (a
// backquoted column name is never preceded by a space).
func valuesKeyword(stmt string) int {
	const kw = " VALUES"
	for i := 0; i+len(kw) <= len(stmt); i++ {
		if strings.EqualFold(stmt[i:i+len(kw)], kw) {
			return i
		}
	}
	return -1
}

// dumpStatementSummary is the start of a statement, for an error or a log line, its
// password literals masked.
func dumpStatementSummary(stmt string) string {
	head, _ := normalizeDumpStatement(stmt)
	if len(head) > 120 {
		head = head[:120] + "..."
	}
	return sqlmask.Secrets(head) // a CHANGE MASTER or CREATE USER can carry a password (F10)
}

// backupVerificationLoad runs a dump's statements on the verification server: one pinned
// connection with the restore session settings, data statements in transactions.
type backupVerificationLoad struct {
	server  *ServerMonitor // the backup's server: its restore statement runners
	host    string         // backup-verification-host, for the SQL log
	path    string         // the file being loaded, for the runners' messages
	ctx     context.Context
	conn    *sqlx.Conn
	db      *sqlx.DB
	ver     *version.Version
	inv     *dumpInventory
	schema  string          // the current schema (USE)
	dropped map[string]bool // the application schemas dropped before their load
	skipped int             // replication statements not run
	// external server: the schemas not loaded (backupVerificationSkippedSchemas), and
	// whether the statements read are in one of them
	skipSchemas map[string]bool
	inSkipped   bool
}

// logSQL logs a statement repman sends to the verification server itself (F11), and its
// error.
func (l *backupVerificationLoad) logSQL(query string, err error, level, format string, args ...any) {
	l.server.ClusterGroup.LogSQL(query, err, l.host, "BackupVerification", level, format, args...)
}

// skip logs a statement of the dump the test restore does not run (F11): a replay by
// hand leaves it out too.
func (l *backupVerificationLoad) skip(why, stmt string) {
	cluster := l.server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification on %s skipped a statement of %s (%s): %s", l.host, filepath.Base(l.path), why, dumpStatementSummary(stmt))
}

// run loads the statements of r, schema being the current one when r starts. A splitdump
// file ends with the next schema's CREATE DATABASE and USE: a skipped schema there does not
// reach the next file, which starts in its own schema.
func (l *backupVerificationLoad) run(r io.Reader, schema string) error {
	l.schema = schema
	l.inSkipped = l.skipSchemas[schema]
	return planAndExecSplitdump(r, splitdumpBatchStatements, splitdumpExecutor{
		batch: func(stmts []string) error {
			if l.inSkipped {
				return nil
			}
			if err := l.server.execSplitdumpBatch(l.ctx, l.conn, stmts, l.path, nil); err != nil {
				return err
			}
			for _, s := range stmts {
				l.recordInsert(s)
			}
			return nil
		},
		single: l.single,
	})
}

// single runs one statement that is not a data statement, and records what it creates.
func (l *backupVerificationLoad) single(stmt string) error {
	if strings.TrimSpace(stmt) == mariadbSandboxLine {
		l.skip("sandbox mode of the dump client", stmt)
		return nil
	}
	head, upper := normalizeDumpStatement(stmt)
	if stmtReplication.MatchString(upper) {
		l.skipped++
		l.skip("replication state, not data", stmt)
		return nil
	}
	if name, ok := nameAt(stmtUse, head, upper, 1); ok {
		l.inSkipped = l.skipSchemas[name]
		if l.inSkipped {
			l.schema = name
			l.skip("replication-manager's own state, not data", stmt)
			return nil
		}
	}
	if name, ok := nameAt(stmtCreateDatabase, head, upper, 1); ok {
		// a dump creates the next schema before its USE: that ends a skipped one too
		l.inSkipped = l.skipSchemas[name]
		if l.inSkipped {
			l.skip("replication-manager's own state, not data", stmt)
			return nil
		}
	}
	if l.inSkipped {
		return nil
	}
	if name, ok := nameAt(stmtCreateDatabase, head, upper, 1); ok {
		if err := l.dropApplicationSchema(name); err != nil {
			return err
		}
		l.inv.addSchema(name)
	}
	if name, ok := nameAt(stmtUse, head, upper, 1); ok && !l.dropped[name] && !backupVerificationSystemSchemas[strings.ToLower(name)] {
		// a dump without CREATE DATABASE (--no-create-db): the schema is cleared and created
		// before its first USE, as at a CREATE DATABASE, so no object of an earlier load stays
		if err := l.dropApplicationSchema(name); err != nil {
			return err
		}
		query, err := dbhelper.CreateDatabaseIfNotExists(l.conn, name)
		l.logSQL(query, err, config.LvlInfo, "Backup verification could not create %s: %s", name, err)
		if err != nil {
			return fmt.Errorf("create %s on the verification server: %w", name, err)
		}
		l.inv.addSchema(name)
	}
	// the reseed's runner: an account that exists is altered, an active plugin skipped
	if err := l.server.execSplitdumpSingle(l.ctx, l.conn, stmt, l.path, nil); err != nil {
		return fmt.Errorf("%s: %w", dumpStatementSummary(stmt), err)
	}
	l.record(head, upper)
	return nil
}

// record notes what a statement run on the verification server switched to or created.
func (l *backupVerificationLoad) record(head, upper string) {
	if name, ok := nameAt(stmtUse, head, upper, 1); ok {
		l.schema = name
		return
	}
	if name, ok := nameAt(stmtCreateTable, head, upper, 1); ok {
		l.inv.addObject(l.schema, dbhelper.SchemaObjectTable, name)
		l.inv.rows[l.schema][name] += 0
		return
	}
	if loc := stmtCreateObject.FindStringSubmatchIndex(upper); loc != nil {
		schema := l.schema
		if loc[4] >= 0 {
			schema = unquoteDumpIdentifier(head[loc[4]:loc[5]])
		}
		l.inv.addObject(schema, strings.ToLower(upper[loc[2]:loc[3]]), unquoteDumpIdentifier(head[loc[6]:loc[7]]))
	}
}

// unquoteDumpIdentifier returns the name of a quoted or plain identifier.
func unquoteDumpIdentifier(id string) string {
	if len(id) >= 2 && id[0] == '`' && id[len(id)-1] == '`' {
		return strings.ReplaceAll(id[1:len(id)-1], "``", "`")
	}
	return id
}

// recordInsert adds the rows of a data statement to its table.
func (l *backupVerificationLoad) recordInsert(stmt string) {
	head, upper := normalizeDumpStatement(stmt)
	if table, ok := nameAt(stmtInsert, head, upper, 1); ok {
		l.inv.addSchema(l.schema)
		l.inv.rows[l.schema][table] += countInsertRows(stmt)
	}
}

// dropApplicationSchema drops an application schema of the backup on the verification
// server, once, before its load: a schema of an earlier verification never mixes in.
func (l *backupVerificationLoad) dropApplicationSchema(schema string) error {
	if backupVerificationSystemSchemas[strings.ToLower(schema)] || l.dropped[schema] {
		return nil
	}
	l.dropped[schema] = true
	// on the load's own session: on a replica it does not write the binary log
	query, err := dbhelper.DropDatabaseIfExists(l.conn, l.ver, schema)
	l.logSQL(query, err, config.LvlInfo, "Backup verification could not drop %s: %s", schema, err)
	if err != nil {
		return fmt.Errorf("drop %s on the verification server: %w", schema, err)
	}
	return nil
}

// openBackupVerificationServer connects to host, backup-verification-host read once by the
// caller with its credential (a setting changed during a run never splits it). A server of
// the cluster is returned as the replica the test restore reseeds (verificationReplica),
// with its own connection pool, which the caller must not close.
func (cluster *Cluster) openBackupVerificationServer(host, credential string) (*sqlx.DB, *version.Version, *ServerMonitor, error) {
	if host == "" {
		return nil, nil, nil, fmt.Errorf("backup-verification-host is not set")
	}
	name, port := misc.SplitHostPort(host)
	if port == "" {
		port = "3306"
	}
	replica, err := cluster.verificationReplica(name, port)
	if err != nil {
		return nil, nil, nil, err
	}
	if replica != nil {
		if replica.Conn == nil || replica.DBVersion == nil {
			return nil, nil, nil, fmt.Errorf("backup-verification-host %s: no connection to it", replica.URL)
		}
		return replica.Conn, replica.DBVersion, replica, nil
	}
	user, pass := misc.SplitPair(credential)
	db, err := dbhelper.MySQLConnect(user, pass, dbhelper.GetAddress(misc.Unbracket(name), port, ""), "timeout=10s")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to the verification server %s: %w", host, err)
	}
	ver, query, err := dbhelper.GetDBVersion(db)
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlDbg, "Backup verification could not read the version of %s: %s", host, err)
	if err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("version of the verification server %s: %w", host, err)
	}
	return db, ver, nil, nil
}

// verifyLogicalBackupRestore is the restore level of backup verification. It returns what
// it loaded and compared, and the kinds of objects the source has and the backup lacks.
func (server *ServerMonitor) verifyLogicalBackupRestore(meta *backupmgr.BackupMetadata) (notes []string, warnings []string, err error) {
	cluster := server.ClusterGroup
	// backup-verification-timeout: the load and the comparison are canceled with ctx
	ctx := context.Background()
	if timeout := time.Duration(cluster.Conf.BackupVerificationTimeout) * time.Second; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		defer func() {
			if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				err = fmt.Errorf("canceled after backup-verification-timeout (%s): %w", timeout, err)
			}
		}()
	}
	// the settings of this run, read once
	host := strings.TrimSpace(cluster.Conf.BackupVerificationHost)
	credential := cluster.Conf.GetDecryptedValue("backup-verification-credential")
	db, ver, replica, err := cluster.openBackupVerificationServer(host, credential)
	if err != nil {
		return nil, nil, err
	}
	if replica == nil {
		defer db.Close()
	}
	if ver.IsPostgreSQL() {
		return nil, nil, dbhelper.ErrBackupVerificationPostgreSQL
	}
	user, _ := misc.SplitPair(credential)
	var conn *sqlx.Conn
	if replica != nil {
		if replica.HasMySQLGTID() && meta.BinLogGtid == "" {
			// it would restart with auto-position from an empty executed set
			return nil, nil, fmt.Errorf("backup %d has no GTID set: %s replicates with GTID auto-position and could not resume from the backup", meta.Id, replica.URL)
		}
		// the replica's own pool, without binary logging: the load is not a transaction of
		// the replica, and its binary logs are reset before replication restarts
		// before the replica is touched: it must replicate from the backup's position after
		if err := replica.checkVerificationResumable(meta); err != nil {
			return nil, nil, err
		}
		setMaintenance, err := replica.beginVerificationReseed()
		if err != nil {
			return nil, nil, err
		}
		resumed, touched := false, false
		defer func() { replica.endVerificationReseed(setMaintenance, resumed, touched) }()
		resumeScheduler, err := server.pauseEventScheduler(db, ver, replica.URL)
		if err != nil {
			return nil, nil, err
		}
		defer resumeScheduler() // after the events are disabled
		nb, err := replica.GetConnNoBinlog(db)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to %s: %w", replica.URL, err)
		}
		defer nb.Close()
		conn = nb.Conn
		host, user = replica.URL, "the cluster's database account"
		if err := server.prepareRestoreConn(ctx, conn); err != nil {
			return nil, nil, fmt.Errorf("restore session on %s: %w", host, err)
		}
		touched = true // the load replaces the replica's data
		notes, warnings, err := server.loadAndCompareForVerification(ctx, meta, db, ver, conn, host, user, false)
		// whatever the result, before the scheduler comes back: the load created its events ENABLE
		if derr := replica.disableVerificationEvents(); derr != nil && err == nil {
			err = derr
		}
		if err != nil {
			return notes, warnings, err
		}
		if err := replica.resumeVerificationReplica(meta); err != nil {
			return notes, warnings, err
		}
		resumed = true
		return append(notes, "replica "+replica.URL+" reseeded from the backup, replicating from its position"), warnings, nil
	}
	if err := cluster.ensureVerificationMark(db, ver, host); err != nil {
		return nil, nil, err
	}
	resumeScheduler, err := server.pauseEventScheduler(db, ver, host)
	if err != nil {
		return nil, nil, err
	}
	defer resumeScheduler() // after loadAndCompareForVerification dropped the schemas and their events
	conn, err = db.Connx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the verification server: %w", err)
	}
	defer conn.Close()
	if err := server.prepareRestoreConn(ctx, conn); err != nil {
		return nil, nil, fmt.Errorf("restore session on the verification server: %w", err)
	}
	return server.loadAndCompareForVerification(ctx, meta, db, ver, conn, host, user, true)
}

// loadAndCompareForVerification loads the backup on conn and compares it with the dump and
// with its source. dropAfter drops the backup's schemas when it ends, whatever its result
// (a verification server); a replica keeps them, they are its data.
func (server *ServerMonitor) loadAndCompareForVerification(ctx context.Context, meta *backupmgr.BackupMetadata, db *sqlx.DB, ver *version.Version, conn *sqlx.Conn, host, user string, dropAfter bool) ([]string, []string, error) {
	var skipSchemas map[string]bool
	if dropAfter { // an external server
		skipSchemas = backupVerificationSkippedSchemas
	}
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification loading %s into %s (%s %s) as %s", meta.Dest, host, ver.Flavor, ver.ToString(), user)
	l := &backupVerificationLoad{server: server, host: host, path: meta.Dest, ctx: ctx, conn: conn, db: db, ver: ver, inv: newDumpInventory(), dropped: map[string]bool{}, skipSchemas: skipSchemas}
	defer func() {
		if !dropAfter {
			return
		}
		for _, schema := range sortedKeys(l.dropped) {
			query, err := dbhelper.DropDatabaseIfExists(db, ver, schema)
			l.logSQL(query, err, config.LvlInfo, "Backup verification could not drop %s: %s", schema, err)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification could not drop %s on the verification server: %s", schema, err)
			}
		}
	}()

	start := time.Now()
	var err error
	if meta.SplitDump {
		err = server.loadSplitdumpForVerification(l, meta.Dest)
	} else {
		err = server.loadMysqldumpForVerification(l, meta.Dest)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("test restore on %s: %w", host, err)
	}
	notes := []string{fmt.Sprintf("loaded into %s (%s %s) in %s", host, ver.Flavor, ver.ToString(), time.Since(start).Round(time.Second))}
	if l.skipped > 0 {
		notes = append(notes, fmt.Sprintf("%d replication statement(s) not run (replication state, not data)", l.skipped))
	}
	compared, err := l.compare()
	notes = append(notes, compared...)
	if err != nil {
		return notes, nil, err
	}
	warnings, note := l.compareSource()
	if note != "" {
		notes = append(notes, note)
	}
	return notes, warnings, nil
}

// loadMysqldumpForVerification loads a single-file dump, plain or encrypted.
func (server *ServerMonitor) loadMysqldumpForVerification(l *backupVerificationLoad, path string) error {
	rc, _, err := server.ClusterGroup.openRestoreArtifactStream(path)
	if err != nil {
		return err
	}
	defer rc.Close()
	r, closeGz, err := gunzipIfCompressed(rc)
	if err != nil {
		return err
	}
	defer closeGz()
	return l.run(r, "")
}

// gunzipIfCompressed returns the plaintext of r, gunzipped when it starts with the gzip
// magic.
func gunzipIfCompressed(r io.Reader) (io.Reader, func(), error) {
	plain := bufio.NewReader(r)
	if magic, _ := plain.Peek(2); !bytes.Equal(magic, []byte{0x1f, 0x8b}) {
		return plain, func() {}, nil
	}
	gz, err := stdgzip.NewReader(plain)
	if err != nil {
		return nil, nil, err
	}
	return gz, func() { gz.Close() }, nil
}

// loadSplitdumpForVerification loads a splitdump, plain or encrypted, in its restore order:
// its schemas are dropped and created first, each file loads in its own schema.
func (server *ServerMonitor) loadSplitdumpForVerification(l *backupVerificationLoad, path string) error {
	dir, cleanup, err := server.ClusterGroup.resolveRestoreArtifactPath(path)
	if err != nil {
		return err
	}
	defer cleanup()
	schemas, err := splitdump.ListSchemas(dir)
	if err != nil {
		return err
	}
	for _, schema := range schemas {
		if l.skipSchemas[schema] {
			continue
		}
		if err := l.dropApplicationSchema(schema); err != nil {
			return err
		}
		query, err := dbhelper.CreateDatabaseIfNotExists(l.conn, schema)
		l.logSQL(query, err, config.LvlInfo, "Backup verification could not create %s: %s", schema, err)
		if err != nil {
			return fmt.Errorf("create %s on the verification server: %w", schema, err)
		}
		l.inv.addSchema(schema)
	}
	plan, err := splitdump.BuildRestorePlan(dir, true, nil)
	if err != nil {
		return err
	}
	for _, file := range append(append(append([]string{}, plan.Schema...), plan.Data...), plan.Post...) {
		base := filepath.Base(file)
		if splitdump.IsGtidSlavePosDataFile(base) {
			cluster := server.ClusterGroup
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification on %s did not load %s (replication state, not data)", l.host, base)
			continue
		}
		schema := splitdump.SchemaFromFilename(file)
		if l.skipSchemas[schema] {
			continue // replication-manager's own state, not data
		}
		if schema != "" && !splitdump.IsMysqlSystemAll(base) {
			query, err := dbhelper.UseDatabaseConn(l.ctx, l.conn, l.ver, schema)
			l.logSQL(query, err, config.LvlDbg, "Backup verification could not use %s: %s", schema, err)
			if err != nil {
				return fmt.Errorf("%s: %w", base, err)
			}
		}
		if err := l.loadFile(file, schema); err != nil {
			return fmt.Errorf("%s: %w", base, err)
		}
	}
	return nil
}

func (l *backupVerificationLoad) loadFile(file, schema string) error {
	l.path = file
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	r, closeGz, err := gunzipIfCompressed(f)
	if err != nil {
		return err
	}
	defer closeGz()
	return l.run(r, schema)
}

// compare checks the verification server against the dump: each application schema's
// objects by name, each table's rows. It returns what it compared.
func (l *backupVerificationLoad) compare() ([]string, error) {
	var problems []string
	add := func(format string, args ...any) {
		if len(problems) < backupVerificationProblemsMax {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	tables, rows, others := 0, int64(0), 0
	var system []string
	for _, schema := range l.inv.schemas {
		if backupVerificationSystemSchemas[strings.ToLower(schema)] {
			system = append(system, schema)
			continue
		}
		got, query, err := dbhelper.GetSchemaObjectNames(l.db, l.ver, schema)
		l.logSQL(query, err, config.LvlDbg, "Backup verification could not list the objects of %s: %s", schema, err)
		if err != nil {
			return nil, fmt.Errorf("objects of %s on the verification server: %w", schema, err)
		}
		have := map[string]map[string]bool{}
		for kind, names := range got {
			have[kind] = map[string]bool{}
			for _, n := range names {
				have[kind][n] = true
			}
		}
		views := l.inv.objects[schema][dbhelper.SchemaObjectView]
		kinds := make([]string, 0, len(l.inv.objects[schema]))
		for kind := range l.inv.objects[schema] {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			for _, name := range sortedKeys(l.inv.objects[schema][kind]) {
				if kind == dbhelper.SchemaObjectTable && views[name] {
					continue // a view's placeholder table
				}
				if !have[kind][name] {
					add("%s %s.%s missing", kind, schema, name)
				} else if kind != dbhelper.SchemaObjectTable {
					others++
				}
			}
		}
		for _, table := range sortedKeys(l.inv.rows[schema]) {
			if views[table] || !have[dbhelper.SchemaObjectTable][table] {
				continue
			}
			want := l.inv.rows[schema][table]
			n, query, err := dbhelper.CountTableRows(l.ctx, l.db, l.ver, schema, table)
			l.logSQL(query, err, config.LvlDbg, "Backup verification could not count the rows of %s.%s: %s", schema, table, err)
			if err != nil {
				return nil, fmt.Errorf("rows of %s.%s on the verification server: %w", schema, table, err)
			}
			tables++
			rows += n
			if n != want {
				add("%s.%s: %d row(s) in the dump, %d restored", schema, table, want, n)
			}
		}
	}
	notes := []string{fmt.Sprintf("%d table(s), %d row(s), %d other object(s) compared", tables, rows, others)}
	if len(system) > 0 {
		notes = append(notes, "system schema(s) loaded, not compared: "+strings.Join(system, ", "))
	}
	if len(problems) > 0 {
		return notes, fmt.Errorf("the restored copy differs from the dump: %s", strings.Join(problems, "; "))
	}
	return notes, nil
}

// compareSource checks the backup against its source server: for each application schema
// of the backup, a kind of object (view, procedure, function, trigger, event) the source
// has and the backup has none of is a warning, e.g. events a dump left out
// (--events=false while the event scheduler is off). Only whole kinds count: an object
// created or dropped on the source since the backup is not a difference. It returns the
// warnings, or a note when the source could not be read.
func (l *backupVerificationLoad) compareSource() ([]string, string) {
	src := l.server
	if src.Conn == nil || src.IsDown() {
		return nil, fmt.Sprintf("not compared with its source %s: the server is down", src.URL)
	}
	var warnings []string
	for _, schema := range l.inv.schemas {
		if backupVerificationSystemSchemas[strings.ToLower(schema)] {
			continue
		}
		got, query, err := dbhelper.GetSchemaObjectNames(src.Conn, src.DBVersion, schema)
		src.ClusterGroup.LogSQL(query, err, src.URL, "BackupVerification", config.LvlDbg, "Backup verification could not list the objects of %s on its source: %s", schema, err)
		if err != nil {
			return warnings, fmt.Sprintf("not compared with its source %s: %s", src.URL, err)
		}
		for _, kind := range []string{dbhelper.SchemaObjectView, dbhelper.SchemaObjectProcedure, dbhelper.SchemaObjectFunction, dbhelper.SchemaObjectTrigger, dbhelper.SchemaObjectEvent} {
			if n := len(got[kind]); n > 0 && len(l.inv.objects[schema][kind]) == 0 && len(warnings) < backupVerificationProblemsMax {
				warnings = append(warnings, fmt.Sprintf("%s: the source has %d %s(s), the backup none", schema, n, kind))
			}
		}
	}
	return warnings, ""
}

// sortedKeys returns the keys of a map in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BackupVerificationServerState is what a test restore finds on backup-verification-host.
// An external server must be marked as a verification server
// (replication_manager_schema.backup_verification_server): the schemas a backup holds are
// dropped there. A VIP, a proxy or another cluster's server is never marked.
type BackupVerificationServerState struct {
	Host    string   `json:"host"`
	Replica bool     `json:"replica"` // a replica of the cluster: reseeded, never marked
	Marked  bool     `json:"marked"`
	Empty   bool     `json:"empty"`   // no schema of its own: marked on its first test restore
	Schemas []string `json:"schemas"` // its schemas, the system ones and an empty replication_manager_schema left out
}

// backupVerificationServerState reads the mark and the schemas of an external server.
func (cluster *Cluster) backupVerificationServerState(db *sqlx.DB, ver *version.Version, host string) (*BackupVerificationServerState, error) {
	state := &BackupVerificationServerState{Host: host, Schemas: []string{}}
	marked, query, err := dbhelper.HasVerificationMark(db, ver)
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlDbg, "Backup verification could not read the mark of %s: %s", host, err)
	if err != nil {
		return nil, fmt.Errorf("mark of the verification server %s: %w", host, err)
	}
	state.Marked = marked
	schemas, query, err := dbhelper.ListDatabases(db, ver)
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlDbg, "Backup verification could not list the schemas of %s: %s", host, err)
	if err != nil {
		return nil, fmt.Errorf("schemas of the verification server %s: %w", host, err)
	}
	for _, s := range schemas {
		if s == dbhelper.VerificationMarkSchema {
			// replication-manager's own schema: the server's only when it holds tables
			// (a server replication-manager monitors), the mark aside
			objects, query, err := dbhelper.GetSchemaObjectNames(db, ver, s)
			cluster.LogSQL(query, err, host, "BackupVerification", config.LvlDbg, "Backup verification could not list the objects of %s on %s: %s", s, host, err)
			if err != nil {
				return nil, fmt.Errorf("objects of %s on the verification server %s: %w", s, host, err)
			}
			own := 0
			for kind, names := range objects {
				for _, name := range names {
					if kind != dbhelper.SchemaObjectTable || name != dbhelper.VerificationMarkTable {
						own++
					}
				}
			}
			if own == 0 {
				continue
			}
		}
		state.Schemas = append(state.Schemas, s)
	}
	state.Empty = len(state.Schemas) == 0
	return state, nil
}

// ensureVerificationMark refuses an external server that is not marked as a verification
// server, unless it is empty: nothing of it can be lost, it is marked on this first use.
func (cluster *Cluster) ensureVerificationMark(db *sqlx.DB, ver *version.Version, host string) error {
	state, err := cluster.backupVerificationServerState(db, ver, host)
	if err != nil {
		return err
	}
	if state.Marked {
		return nil
	}
	if !state.Empty {
		return fmt.Errorf("backup-verification-host %s is not marked as a verification server and holds schemas (%s): mark it in Settings > Backups if they may be lost (a test restore drops there the schemas the backup holds)", host, strings.Join(state.Schemas, ", "))
	}
	query, err := dbhelper.MarkVerificationServer(db, ver, cluster.Name, "replication-manager (empty server)")
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlErr, "Backup verification could not mark %s as a verification server: %s", host, err)
	if err != nil {
		return fmt.Errorf("mark the verification server %s: %w", host, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backup verification server %s holds no schema: marked as a verification server", host)
	return nil
}

// GetBackupVerificationServerState returns the state of backup-verification-host.
func (cluster *Cluster) GetBackupVerificationServerState() (*BackupVerificationServerState, error) {
	host := strings.TrimSpace(cluster.Conf.BackupVerificationHost)
	db, ver, replica, err := cluster.openBackupVerificationServer(host, cluster.Conf.GetDecryptedValue("backup-verification-credential"))
	if err != nil {
		return nil, err
	}
	if replica != nil {
		return &BackupVerificationServerState{Host: replica.URL, Replica: true, Schemas: []string{}}, nil
	}
	defer db.Close()
	return cluster.backupVerificationServerState(db, ver, host)
}

// MarkBackupVerificationServer marks backup-verification-host as a verification server, on
// the request of user: the schemas a backup holds may then be dropped there.
func (cluster *Cluster) MarkBackupVerificationServer(user string) error {
	host := strings.TrimSpace(cluster.Conf.BackupVerificationHost)
	db, ver, replica, err := cluster.openBackupVerificationServer(host, cluster.Conf.GetDecryptedValue("backup-verification-credential"))
	if err != nil {
		return err
	}
	if replica != nil {
		return fmt.Errorf("backup-verification-host %s is a replica of the cluster: it is reseeded, not marked", replica.URL)
	}
	defer db.Close()
	query, err := dbhelper.MarkVerificationServer(db, ver, cluster.Name, user)
	cluster.LogSQL(query, err, host, "BackupVerification", config.LvlErr, "Backup verification could not mark %s as a verification server: %s", host, err)
	if err != nil {
		return fmt.Errorf("mark the verification server %s: %w", host, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Backup verification server %s marked as a verification server by %s", host, user)
	return nil
}
