// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	gmysql "github.com/go-mysql-org/go-mysql/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/splitdump"
)

// The init as one streamed SQL (CDC_CORE.md "Init"): rows flowing from a source the loader
// does not know, a SELECT on a consistent snapshot of the source (range by range in table
// mode, the index's query in index mode) or the split dump of a backup (chunk by chunk). The
// capture publishes, through the transport, the index's -schema element, then the rows in
// parts of at most initPartRows rows and initPartBytes bytes (held in memory, T18: the
// transport's bound pauses the stream, nothing is spooled), the last part with the init's
// GTID. The apply makes the destination from the -schema element and loads the parts, never
// reading the source.

const (
	defaultRangeStep = 10000
	initPartRows     = 10000
	initPartBytes    = 16 << 20
)

// rowSource is where an init's rows come from.
type rowSource interface {
	// open starts the read: the GTID the rows hold, and the earliest value of the partition
	// column ("" when not partitioned or none)
	open(ctx context.Context) (gtid, from string, err error)
	// each gives every row, its values in the destination's column order (nil a NULL)
	each(ctx context.Context, fn func(row []interface{}) error) error
	close()
}

// selectSource reads the source on one consistent snapshot. Its GTID is @@gtid_current_pos
// read just before the snapshot opens: the transactions between the two are in the rows and
// in the delta that follows, which applies them again (copy mode: whole row images), never
// one missing.
type selectSource struct {
	db   *sqlx.DB
	conn *sqlx.Conn
	// table mode: schema.table by ranges of step rows on its key; index mode: query
	schema, table string
	cols, key     []string
	step          int
	query         string
	partColumn    string
}

func (s *selectSource) open(ctx context.Context) (string, string, error) {
	var err error
	if s.conn, err = s.db.Connx(ctx); err != nil {
		return "", "", err
	}
	gtid, q, err := dbhelper.SelectGTIDCurrentPos(ctx, s.conn)
	if err != nil {
		return "", "", fmt.Errorf("%w (%s)", err, q)
	}
	if _, err := s.conn.ExecContext(ctx, "START TRANSACTION WITH CONSISTENT SNAPSHOT"); err != nil {
		return "", "", err
	}
	from := ""
	if s.partColumn != "" {
		if s.query != "" {
			from, q, err = dbhelper.SelectMinOfQuery(ctx, s.conn, s.query, s.partColumn)
		} else {
			from, q, err = dbhelper.SelectMinOfTable(ctx, s.conn, s.schema, s.table, s.partColumn)
		}
		if err != nil {
			return "", "", fmt.Errorf("%w (%s)", err, q)
		}
	}
	return gtid, from, nil
}

func (s *selectSource) each(ctx context.Context, fn func([]interface{}) error) error {
	if s.query != "" {
		rows, err := s.conn.QueryContext(ctx, "SELECT * FROM ("+s.query+") AS d")
		if err != nil {
			return err
		}
		defer rows.Close()
		_, err = scanEach(rows, nil, fn)
		return err
	}
	keyPos := make([]int, len(s.key))
	for i, k := range s.key {
		keyPos[i] = -1
		for j, c := range s.cols {
			if strings.EqualFold(c, k) {
				keyPos[i] = j
			}
		}
		if keyPos[i] < 0 {
			return fmt.Errorf("key column %s is not read", k)
		}
	}
	var after []interface{}
	for {
		rows, q, err := dbhelper.SelectTableRange(ctx, s.conn, s.schema, s.table, s.cols, s.key, after, s.step)
		if err != nil {
			return fmt.Errorf("%w (%s)", err, q)
		}
		var last []interface{}
		n, err := scanEach(rows, &last, fn)
		rows.Close()
		if err != nil {
			return err
		}
		if n < s.step {
			return nil
		}
		after = make([]interface{}, len(keyPos))
		for i, p := range keyPos {
			after[i] = last[p]
		}
	}
}

func (s *selectSource) close() {
	if s.conn != nil {
		s.conn.ExecContext(context.Background(), "ROLLBACK")
		s.conn.Close()
	}
}

// scanEach gives each row of rows to fn as text values (nil a NULL); last, when not nil, gets
// the last row.
func scanEach(rows *sql.Rows, last *[]interface{}, fn func([]interface{}) error) (int, error) {
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		r := make([]interface{}, len(vals))
		for i, v := range vals {
			if v != nil {
				r[i] = append([]byte(nil), v...)
			}
		}
		if err := fn(r); err != nil {
			return n, err
		}
		if last != nil {
			*last = r
		}
		n++
	}
	return n, rows.Err()
}

// dumpSource reads one table of a split dump (its data files, chunk by chunk, in order): no
// load on the source. Its GTID is the backup's (its metadata). The earliest value of the
// partition column needs a first pass over the table's files.
type dumpSource struct {
	dir, schema, table string
	ncols, partPos     int // the table's columns; the partition column's position, -1 none
	part               *partitioning
	files              []string
}

func (s *dumpSource) open(ctx context.Context) (string, string, error) {
	meta, err := splitdump.ReadMetadata(s.dir)
	if err != nil {
		return "", "", err
	}
	if meta.GTID == "" {
		return "", "", fmt.Errorf("the backup %s has no GTID in its metadata: the delta cannot start from it", s.dir)
	}
	set, err := splitdump.ListFiles(s.dir, false)
	if err != nil {
		return "", "", err
	}
	for _, f := range set.Data {
		b := filepath.Base(f)
		if splitdump.SchemaFromFilename(b) == s.schema && splitdump.TableFromFilename(b) == s.table {
			s.files = append(s.files, f)
		}
	}
	from := ""
	if s.part != nil && s.partPos >= 0 && len(s.files) > 0 {
		var min int64
		found := false
		err := s.each(ctx, func(r []interface{}) error {
			if o, ok := s.part.value(r[s.partPos]); ok && (!found || o < min) {
				min, found = o, true
			}
			return nil
		})
		if err != nil {
			return "", "", err
		}
		if found {
			from = s.part.literal(min)
		}
	}
	return meta.GTID, from, nil
}

func (s *dumpSource) each(ctx context.Context, fn func([]interface{}) error) error {
	for _, f := range s.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, closeAll, err := splitdump.OpenDecompressed(f)
		if err != nil {
			return err
		}
		err = forEachInsert(r, func(v []dumpValue) error {
			if len(v) != s.ncols {
				return fmt.Errorf("a row of %d values for the %d columns of %s.%s: the dump's table differs from the source's", len(v), s.ncols, s.schema, s.table)
			}
			row := make([]interface{}, len(v))
			for i, x := range v {
				if !x.Null {
					row[i] = x.Text
				}
			}
			return fn(row)
		})
		closeAll()
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

func (s *dumpSource) close() {}

// gtidContains: the GTID set a holds every transaction of b (MariaDB, per domain).
func gtidContains(a, b string) (bool, error) {
	if b == "" {
		return true, nil
	}
	sa, err := gmysql.ParseMariadbGTIDSet(a)
	if err != nil {
		return false, err
	}
	sb, err := gmysql.ParseMariadbGTIDSet(b)
	if err != nil {
		return false, err
	}
	return sa.Contain(sb), nil
}

// coversKnownDomains: the GTID b (a backup's, an init's: the source's) is not older than the
// position p on any domain b knows. A domain of p that b has not (a child's own domain carried in
// its gtid_slave_pos, never the source's) is not compared: the source had no transaction of it.
func coversKnownDomains(b, p string) (bool, error) {
	sb, err := gmysql.ParseMariadbGTIDSet(b)
	if err != nil {
		return false, err
	}
	sp, err := gmysql.ParseMariadbGTIDSet(p)
	if err != nil {
		return false, err
	}
	common := 0
	for domain, g := range sp.(*gmysql.MariadbGTIDSet).Sets {
		if ours, ok := sb.(*gmysql.MariadbGTIDSet).Sets[domain]; ok {
			if !ours.Contain(g) {
				return false, nil
			}
			common++
		}
	}
	// no domain in common (an empty GTID): nothing says b is not older
	return common > 0 || len(sp.(*gmysql.MariadbGTIDSet).Sets) == 0, nil
}

// initJob is one destination table to init: its -schema element (columns, key) without the
// GTID yet, the positions of its delete key in the rows, and its source.
type initJob struct {
	key    string // wire.InitKey
	elem   wire.SchemaElement
	keyPos []int
	src    rowSource
	backup bool
}

func positions(cols, names []string) ([]int, error) {
	out := make([]int, len(names))
	for i, n := range names {
		out[i] = -1
		for j, c := range cols {
			if strings.EqualFold(c, n) {
				out[i] = j
			}
		}
		if out[i] < 0 {
			return nil, fmt.Errorf("column %s is not in the rows", n)
		}
	}
	return out, nil
}

// copyInitJob is the init of a table-mode copy.
func copyInitJob(src *sqlx.DB, tc *tableCopy) (*initJob, error) {
	cols := tc.columnNames()
	keyPos, err := positions(cols, tc.key)
	if err != nil {
		return nil, err
	}
	j := &initJob{key: tc.initKey(), elem: tc.schemaElement("", "", ""), keyPos: keyPos}
	partPos := -1
	if tc.part != nil {
		p, _ := positions(cols, []string{tc.part.column})
		partPos = p[0]
	}
	switch strings.ToLower(tc.idx.Init) {
	case "", wire.InitSelect:
		step := tc.idx.RangeStep
		if step <= 0 {
			step = defaultRangeStep
		}
		ss := &selectSource{db: src, schema: tc.srcSchema, table: tc.srcTable, cols: cols, key: tc.pk, step: step}
		if tc.part != nil {
			ss.partColumn = tc.part.column
		}
		j.src, j.elem.Source = ss, wire.InitSelect
	case wire.InitBackup:
		if tc.idx.BackupDir == "" {
			return nil, fmt.Errorf("index %s: init backup without backup_dir (the split dump to read)", tc.idx.Name)
		}
		j.src = &dumpSource{dir: tc.idx.BackupDir, schema: tc.srcSchema, table: tc.srcTable, ncols: len(cols), partPos: partPos, part: tc.part}
		j.elem.Source, j.backup = wire.InitBackup, true
	default:
		return nil, fmt.Errorf("index %s: init %q: select or backup", tc.idx.Name, tc.idx.Init)
	}
	return j, nil
}

// indexInitJob is the init of an index (always a select: its query on a snapshot).
func indexInitJob(ctx context.Context, src *sqlx.DB, meta *indexMeta) (*initJob, error) {
	if meta.idx.Init != "" && !strings.EqualFold(meta.idx.Init, wire.InitSelect) {
		return nil, fmt.Errorf("index %s: the index mode inits by select only", meta.idx.Name)
	}
	elem, err := indexSchemaElement(ctx, src, meta)
	if err != nil {
		return nil, err
	}
	keyPos, err := positions(meta.columns, meta.key)
	if err != nil {
		return nil, err
	}
	ss := &selectSource{db: src, query: meta.idx.SQL}
	if meta.part != nil {
		ss.partColumn = meta.part.column
	}
	return &initJob{key: wire.InitKey(indexID(meta.idx), meta.idx.Schema, meta.idx.Table), elem: elem, keyPos: keyPos, src: ss}, nil
}

// indexSchemaElement describes an index's denormalized table: a column of a source table
// takes that column's type (information_schema), an expression the type its result has;
// every column but the key may be NULL (an outer join).
func indexSchemaElement(ctx context.Context, src *sqlx.DB, meta *indexMeta) (wire.SchemaElement, error) {
	idx := meta.idx
	e := wire.SchemaElement{Name: idx.Name + "-schema", Index: indexID(idx), Mode: wire.IndexModeIndex, Schema: idx.Schema, Table: idx.Table,
		Key: append([]string(nil), meta.key...), DeleteKey: meta.key, Indexes: meta.lookups(), Source: wire.InitSelect}
	inKey := map[string]bool{}
	for _, k := range meta.key {
		inKey[k] = true
	}
	described := map[string][]dbhelper.TableColumn{}
	for _, f := range meta.fields {
		c := wire.Column{Name: f.name, Nullable: !inKey[f.name]}
		if f.orgTable != "" {
			t := f.schema + "." + f.orgTable
			if _, ok := described[t]; !ok {
				cols, err := columnsOfTable(ctx, src, f.schema, f.orgTable)
				if err != nil {
					return e, fmt.Errorf("index %s: %w", idx.Name, err)
				}
				described[t] = cols
			}
			for _, sc := range described[t] {
				if strings.EqualFold(sc.Name, f.orgName) {
					c.Type, c.Charset, c.Collation = sc.ColumnType, sc.Charset.String, sc.Collation.String
				}
			}
		}
		if c.Type == "" {
			c.Type = expressionType(f)
		}
		if c.Type == "" {
			return e, fmt.Errorf("index %s: column %s: no MariaDB type for its result type %d, cast it in the query", idx.Name, f.name, f.typ)
		}
		e.Columns = append(e.Columns, c)
	}
	if meta.part != nil {
		// the partition column is in meta.key already (addPartitionKey): in the PRIMARY KEY
		e.PartitionColumn, e.PartitionPeriod = meta.part.column, meta.part.period
	}
	return e, nil
}

// expressionType is the column type of an expression of the result, from its protocol type.
func expressionType(f field) string {
	unsigned := ""
	if f.flag&gmysql.UNSIGNED_FLAG != 0 {
		unsigned = " UNSIGNED"
	}
	switch f.typ {
	case gmysql.MYSQL_TYPE_TINY, gmysql.MYSQL_TYPE_SHORT, gmysql.MYSQL_TYPE_INT24, gmysql.MYSQL_TYPE_LONG, gmysql.MYSQL_TYPE_LONGLONG:
		return "BIGINT" + unsigned
	case gmysql.MYSQL_TYPE_NEWDECIMAL, gmysql.MYSQL_TYPE_DECIMAL:
		return fmt.Sprintf("DECIMAL(65,%d)", min(int(f.decimals), 30))
	case gmysql.MYSQL_TYPE_FLOAT, gmysql.MYSQL_TYPE_DOUBLE:
		return "DOUBLE"
	case gmysql.MYSQL_TYPE_DATE, gmysql.MYSQL_TYPE_NEWDATE:
		return "DATE"
	case gmysql.MYSQL_TYPE_DATETIME, gmysql.MYSQL_TYPE_TIMESTAMP, gmysql.MYSQL_TYPE_DATETIME2, gmysql.MYSQL_TYPE_TIMESTAMP2:
		if f.decimals > 0 && f.decimals <= 6 {
			return fmt.Sprintf("DATETIME(%d)", f.decimals)
		}
		return "DATETIME"
	case gmysql.MYSQL_TYPE_TIME, gmysql.MYSQL_TYPE_TIME2:
		return "TIME"
	case gmysql.MYSQL_TYPE_YEAR:
		return "YEAR"
	case gmysql.MYSQL_TYPE_VARCHAR, gmysql.MYSQL_TYPE_VAR_STRING, gmysql.MYSQL_TYPE_STRING, gmysql.MYSQL_TYPE_ENUM, gmysql.MYSQL_TYPE_SET:
		if f.flag&gmysql.BINARY_FLAG != 0 {
			return "LONGBLOB"
		}
		return "LONGTEXT"
	case gmysql.MYSQL_TYPE_TINY_BLOB, gmysql.MYSQL_TYPE_MEDIUM_BLOB, gmysql.MYSQL_TYPE_LONG_BLOB, gmysql.MYSQL_TYPE_BLOB:
		if f.flag&gmysql.BINARY_FLAG != 0 {
			return "LONGBLOB"
		}
		return "LONGTEXT"
	case gmysql.MYSQL_TYPE_JSON:
		return "JSON"
	}
	return ""
}

// initPart is the rows of the part being filled: in memory, bounded (initPartRows,
// initPartBytes).
type initPart struct {
	rows, keys []byte
	n          int64
}

func (p *initPart) full() bool {
	return p.n >= initPartRows || len(p.rows)+len(p.keys) >= initPartBytes
}

// runInit publishes an init through the transport: its -schema element (the GTID and the
// earliest partition value set), then its parts, the last one marked; publish gives each
// manifest the next sequence. before checks the init's GTID before anything is published (a
// backup older than the feed's position is refused). It returns the init's GTID.
func runInit(ctx context.Context, j *initJob, before func(gtid string) error, publish func(wire.Manifest, map[string][]byte) error) (string, error) {
	defer j.src.close()
	gtid, from, err := j.src.open(ctx)
	if err != nil {
		return "", fmt.Errorf("init of %s.%s: %w", j.elem.Schema, j.elem.Table, err)
	}
	if err := before(gtid); err != nil {
		return "", err
	}
	elem := j.elem
	elem.GTID, elem.PartitionFrom = gtid, from
	if err := publish(wire.Manifest{Schema: &elem}, nil); err != nil {
		return "", err
	}
	part := &initPart{}
	n := 0
	flush := func(last bool) error {
		n++
		m := wire.Manifest{Init: &wire.InitPart{Index: elem.Index, Schema: elem.Schema, Table: elem.Table, Part: n, Last: last, GTID: gtid}}
		files := map[string][]byte{}
		if part.n > 0 {
			t := wire.TableFiles{Schema: elem.Schema, Table: elem.Table, KeyColumns: elem.DeleteKey, DeleteRows: part.n, InsertRows: part.n,
				Deletes: fmt.Sprintf("init-%d.deletes.tsv", n), Inserts: fmt.Sprintf("init-%d.inserts.tsv", n)}
			files[t.Deletes], files[t.Inserts] = part.keys, part.rows
			m.Tables = []wire.TableFiles{t}
		}
		part = &initPart{}
		return publish(m, files)
	}
	err = j.src.each(ctx, func(r []interface{}) error {
		if len(r) != len(elem.Columns) {
			return fmt.Errorf("a row of %d values for the %d columns of %s.%s", len(r), len(elem.Columns), elem.Schema, elem.Table)
		}
		k := make([]interface{}, len(j.keyPos))
		for i, p := range j.keyPos {
			k[i] = r[p]
		}
		part.rows = wire.EncodeTSV(part.rows, r)
		part.keys = wire.EncodeTSV(part.keys, k)
		part.n++
		if part.full() {
			return flush(false)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("init of %s.%s: %w", elem.Schema, elem.Table, err)
	}
	return gtid, flush(true)
}

// initJobs are the inits of cfg's indexes and copies the State has not published whole yet.
func initJobs(ctx context.Context, src *sqlx.DB, metas []*indexMeta, copies []*tableCopy, published map[string]string) ([]*initJob, error) {
	var jobs []*initJob
	for _, m := range metas {
		if _, ok := published[wire.InitKey(indexID(m.idx), m.idx.Schema, m.idx.Table)]; ok {
			continue
		}
		j, err := indexInitJob(ctx, src, m)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	for _, tc := range copies {
		if _, ok := published[tc.initKey()]; ok {
			continue
		}
		j, err := copyInitJob(src, tc)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// startGTID is where a feed without a position starts: the configured start, else the
// oldest backup an init reads (the delta must cover every transaction after it), else the
// source's current position (read before any snapshot of an init: nothing is missed).
func startGTID(ctx context.Context, src *sqlx.DB, cfg wire.Config, jobs []*initJob) (string, error) {
	if cfg.StartGTID != "" {
		return cfg.StartGTID, nil
	}
	oldest := ""
	for _, j := range jobs {
		d, ok := j.src.(*dumpSource)
		if !ok {
			continue
		}
		meta, err := splitdump.ReadMetadata(d.dir)
		if err != nil {
			return "", err
		}
		if meta.GTID == "" {
			return "", fmt.Errorf("the backup %s has no GTID in its metadata", d.dir)
		}
		if oldest == "" {
			oldest = meta.GTID
			continue
		}
		older, err := gtidContains(oldest, meta.GTID)
		if err != nil {
			return "", err
		}
		if older {
			oldest = meta.GTID
		} else if newer, _ := gtidContains(meta.GTID, oldest); !newer {
			return "", fmt.Errorf("the backups the inits read (%s, %s) are not on one line of GTIDs", oldest, meta.GTID)
		}
	}
	if oldest != "" {
		return oldest, nil
	}
	conn, err := src.Connx(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	g, q, err := dbhelper.SelectGTIDCurrentPos(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("%w (%s)", err, q)
	}
	return g, nil
}

// runInits publishes the inits not published yet, before the binlog is read: the delta
// starts at the State's GTID, which no init's GTID precedes (a backup older than it is refused:
// the transactions between the two would be lost).
func (c *capture) runInits(ctx context.Context, src *sqlx.DB, metas []*indexMeta, copies []*tableCopy) error {
	c.mu.Lock()
	known := map[string]bool{}
	for _, m := range metas {
		known[wire.InitKey(indexID(m.idx), m.idx.Schema, m.idx.Table)] = true
	}
	for _, tc := range copies {
		known[tc.initKey()] = true
	}
	for k := range c.state.Inits {
		if !known[k] {
			delete(c.state.Inits, k) // an index no longer configured: put back, it starts again
		}
	}
	published := maps.Clone(c.state.Inits)
	c.mu.Unlock()
	jobs, err := initJobs(ctx, src, metas, copies, published)
	if err != nil || len(jobs) == 0 {
		return err
	}
	c.mu.Lock()
	if c.state.GTID == "" {
		g, err := startGTID(ctx, src, c.cfg, jobs)
		if err != nil {
			c.mu.Unlock()
			return fmt.Errorf("the feed's start position: %w", err)
		}
		c.state.GTID, c.gtid = g, g
	}
	position := c.state.GTID
	c.mu.Unlock()
	for _, j := range jobs {
		c.describe(fmt.Sprintf("init of %s.%s (%s)", j.elem.Schema, j.elem.Table, j.elem.Source))
		before := func(gtid string) error {
			if !j.backup {
				return nil
			}
			ok, err := coversKnownDomains(gtid, position)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("init of %s.%s: the backup's GTID %s is older than the feed's position %s: take a new backup, or init by select", j.elem.Schema, j.elem.Table, gtid, position)
			}
			return nil
		}
		gtid, err := runInit(ctx, j, before, func(m wire.Manifest, files map[string][]byte) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.publishLocked(m, files)
		})
		if err != nil {
			return err
		}
		c.mu.Lock()
		if c.state.Inits == nil {
			c.state.Inits = map[string]string{}
		}
		c.state.Inits[j.key] = gtid
		err = wire.WriteState(c.statePath, c.state)
		c.mu.Unlock()
		if err != nil {
			return err
		}
	}
	c.describe("")
	return nil
}

// describe sets the status's description.
func (c *capture) describe(d string) {
	c.mu.Lock()
	c.st.Description = d
	c.mu.Unlock()
}
