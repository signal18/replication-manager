// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// The periods: a day, an ISO week from Monday, a calendar month; each partition named by
// its start, bounded by the next start; older values and NULL go to the first partition.
func TestPartitionPeriods(t *testing.T) {
	for _, c := range []struct{ period, value, start, next string }{
		{"day", "2026-11-28", "2026-11-28", "2026-11-29"},
		{"week", "2026-11-28", "2026-11-23", "2026-11-30"}, // a Saturday: its Monday
		{"week", "2026-11-23", "2026-11-23", "2026-11-30"},
		{"month", "2026-12-31", "2026-12-01", "2027-01-01"},
	} {
		p := &partitioning{column: "ts", period: c.period, ahead: 3}
		s := p.start(day(c.value))
		if s.Format("2006-01-02") != c.start || p.next(s).Format("2006-01-02") != c.next {
			t.Fatalf("%s of %s: %s, next %s", c.period, c.value, s, p.next(s))
		}
	}
	p := &partitioning{column: "ts", period: "month", ahead: 2}
	parts, err := p.plan(day("2026-09-15"), p.horizon(day("2026-11-28")))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range parts {
		got = append(got, x.Name+"<"+x.LessThan)
	}
	if want := "p20260901<2026-10-01 p20261001<2026-11-01 p20261101<2026-12-01 p20261201<2027-01-01 p20270101<2027-02-01"; strings.Join(got, " ") != want {
		t.Fatalf("plan %s", strings.Join(got, " "))
	}
	first := day("2026-09-01")
	for v, want := range map[string]string{"2026-11-28 10:11:12.5": "p20261101", "2020-01-01": "p20260901", "0000-00-00 00:00:00": "p20260901"} {
		if r := p.route([]byte(v), first); r != want {
			t.Fatalf("route %s: %s", v, r)
		}
	}
	if r := p.route(nil, first); r != "p20260901" {
		t.Fatalf("NULL: %s", r)
	}
	if s, ok := partitionStart("p20261101"); !ok || !s.Equal(day("2026-11-01")) {
		t.Fatal("partitionStart")
	}
	if _, ok := partitionStart("pmax"); ok {
		t.Fatal("pmax is not one of the CDC's")
	}
	// bounded: a daily plan of 30 years is refused, never 10000 partitions
	d := &partitioning{column: "ts", period: "day", ahead: 3}
	if _, err := d.plan(day("1996-01-01"), day("2026-11-28")); err == nil {
		t.Fatal("a plan past maxPartitions must be refused")
	}
	for _, bad := range []wire.Index{{Name: "i", PartitionColumn: "ts"}, {Name: "i", PartitionPeriod: "day"}, {Name: "i", PartitionColumn: "ts", PartitionPeriod: "year"}} {
		if _, err := newPartitioning(bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
	if p, _ := newPartitioning(wire.Index{Name: "i", PartitionColumn: "ts", PartitionPeriod: "Week"}); p == nil || p.ahead != defaultPartitionsAhead || p.period != "week" {
		t.Fatalf("%+v", p)
	}
}

func mockConn(t *testing.T) (*sqlx.DB, *sqlx.Conn, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	x := sqlx.NewDb(db, "mysql")
	conn, err := x.Connx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return x, conn, mock
}

// The partitions ahead are created before they are needed: up to the horizon, and up to the
// latest value about to be written; at most maxPartitionsAdded at once.
func TestPartitionsCreatedAhead(t *testing.T) {
	_, conn, mock := mockConn(t)
	p := &partitioning{column: "ts", period: "day", ahead: 2}
	pt := &partitionedTable{p: p, schema: "logs_p", table: "access", pos: 1, first: day("2026-11-20"), last: day("2026-11-28")}
	now := day("2026-11-28").Add(10 * time.Hour)
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `logs_p`.`access` ADD PARTITION (PARTITION `p20261129` VALUES LESS THAN ('2026-11-30'), PARTITION `p20261130` VALUES LESS THAN ('2026-12-01'))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := pt.ensure(context.Background(), conn, now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if !pt.last.Equal(day("2026-11-30")) {
		t.Fatalf("last %s", pt.last)
	}
	// nothing to add: no statement
	if _, err := pt.ensure(context.Background(), conn, now, day("2026-11-29")); err != nil {
		t.Fatal(err)
	}
	// a row of 2026-12-02: its partition first
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `logs_p`.`access` ADD PARTITION (PARTITION `p20261201` VALUES LESS THAN ('2026-12-02'), PARTITION `p20261202` VALUES LESS THAN ('2026-12-03'))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := pt.ensure(context.Background(), conn, now, pt.latest([]byte("1\t2026-12-02 01:00:00\n2\t2026-11-01 00:00:00\n"))); err != nil {
		t.Fatal(err)
	}
	// a TIMESTAMP column: bounds by UNIX_TIMESTAMP
	pt.unixTimestamp = true
	mock.ExpectExec(regexp.QuoteMeta("ADD PARTITION (PARTITION `p20261203` VALUES LESS THAN (UNIX_TIMESTAMP('2026-12-04')))")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := pt.ensure(context.Background(), conn, now, day("2026-12-03")); err != nil {
		t.Fatal(err)
	}
	// a value years ahead: refused, nothing added
	if _, err := pt.ensure(context.Background(), conn, now, day("2030-01-01")); err == nil {
		t.Fatal("a value years ahead must be refused")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The pool: every partition's job runs once, never more than threads at a time, a failed
// partition reported by name without stopping the others, each under its deadline.
func TestRunPartitions(t *testing.T) {
	var running, peak atomic.Int32
	var mu sync.Mutex
	ran := map[string]int{}
	jobs := map[string]func(context.Context) error{}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("p%02d", i)
		jobs[name] = func(ctx context.Context) error {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			mu.Lock()
			ran[name]++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			if name == "p03" {
				return errors.New("boom")
			}
			if name == "p07" {
				<-ctx.Done() // past its deadline
				return ctx.Err()
			}
			return nil
		}
	}
	errs := runPartitions(context.Background(), 3, 200*time.Millisecond, jobs)
	if peak.Load() > 3 {
		t.Fatalf("%d workers at once for 3 threads", peak.Load())
	}
	for n, c := range ran {
		if c != 1 {
			t.Fatalf("%s ran %d times", n, c)
		}
	}
	if len(ran) != 12 || len(errs) != 2 || errs["p03"] == nil || !errors.Is(errs["p07"], context.DeadlineExceeded) {
		t.Fatalf("ran %d, errors %v", len(ran), errs)
	}
	if msg := partitionErrors(errs).Error(); !strings.HasPrefix(msg, "partition p03: boom; partition p07:") {
		t.Fatal(msg)
	}
	if threadsOf(0) != defaultThreads || threadsOf(1000) != maxThreads {
		t.Fatal("threads bounds")
	}
}

// fakeSource is a row stream of n rows (id, ts).
type fakeSource struct {
	n    int
	gtid string
}

func (f *fakeSource) open(context.Context) (string, string, error) { return f.gtid, "2026-01-05", nil }
func (f *fakeSource) close()                                       {}
func (f *fakeSource) each(_ context.Context, fn func([]interface{}) error) error {
	for i := 1; i <= f.n; i++ {
		if err := fn([]interface{}{[]byte(fmt.Sprint(i)), []byte("2026-01-05 00:00:00")}); err != nil {
			return err
		}
	}
	return nil
}

// The init through the transport: its -schema element first (the GTID, the earliest value),
// then its parts, bounded in rows, each with its rows' keys to delete, the last one marked.
func TestRunInitStream(t *testing.T) {
	j := &initJob{key: "k", keyPos: []int{0, 1}, src: &fakeSource{n: initPartRows + 5, gtid: "0-1-42"},
		elem: wire.SchemaElement{Index: "logs@1", Schema: "logs_p", Table: "access", Columns: []wire.Column{{Name: "id"}, {Name: "ts"}}, Key: []string{"id", "ts"}, DeleteKey: []string{"id", "ts"}}}
	var got []wire.Manifest
	var files []map[string][]byte
	gtid, err := runInit(context.Background(), j, func(string) error { return nil }, func(m wire.Manifest, f map[string][]byte) error {
		got, files = append(got, m), append(files, f)
		return nil
	})
	if err != nil || gtid != "0-1-42" {
		t.Fatal(gtid, err)
	}
	if len(got) != 3 || got[0].Schema == nil || got[0].Schema.GTID != "0-1-42" || got[0].Schema.PartitionFrom != "2026-01-05" {
		t.Fatalf("%d elements, first %+v", len(got), got[0])
	}
	if got[1].Init == nil || got[1].Init.Last || got[1].Tables[0].InsertRows != initPartRows || got[2].Init == nil || !got[2].Init.Last || got[2].Tables[0].InsertRows != 5 {
		t.Fatalf("parts %+v %+v", got[1], got[2])
	}
	last := got[2].Tables[0]
	if string(files[2][last.Inserts]) != string(files[2][last.Deletes]) || !strings.HasPrefix(string(files[2][last.Inserts]), fmt.Sprintf("%d\t2026-01-05 00:00:00\n", initPartRows+1)) {
		t.Fatalf("part rows %q keys %q", files[2][last.Inserts], files[2][last.Deletes])
	}
	// a backup older than the feed's position: refused before anything is published
	got = nil
	j.src = &fakeSource{n: 1, gtid: "0-1-10"}
	before := func(g string) error {
		if ok, _ := gtidContains(g, "0-1-20"); !ok {
			return errors.New("older")
		}
		return nil
	}
	if _, err := runInit(context.Background(), j, before, func(m wire.Manifest, _ map[string][]byte) error { got = append(got, m); return nil }); err == nil || len(got) != 0 {
		t.Fatalf("err %v, %d published", err, len(got))
	}
	// an empty table: the -schema element and one empty last part
	got = nil
	j.src = &fakeSource{n: 0, gtid: "0-1-10"}
	if _, err := runInit(context.Background(), j, func(string) error { return nil }, func(m wire.Manifest, _ map[string][]byte) error { got = append(got, m); return nil }); err != nil || len(got) != 2 || !got[1].Init.Last || len(got[1].Tables) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if !(&initPart{n: initPartRows}).full() || !(&initPart{rows: make([]byte, initPartBytes)}).full() || (&initPart{n: 1}).full() {
		t.Fatal("part bounds")
	}
}

// The SELECT init: @@gtid_current_pos read before the snapshot opens, the earliest partition
// value inside it, then the table by ranges of its primary key.
func TestSelectSourceRanges(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT @@gtid_current_pos")).WillReturnRows(sqlmock.NewRows([]string{"g"}).AddRow("0-1-77"))
	mock.ExpectExec("START TRANSACTION WITH CONSISTENT SNAPSHOT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MIN(`ts`) FROM `logs`.`access`")).WillReturnRows(sqlmock.NewRows([]string{"m"}).AddRow("2026-01-05 00:00:00"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `id`, `ts` FROM `logs`.`access` ORDER BY `id` LIMIT 2")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ts"}).AddRow("1", "2026-01-05").AddRow("2", nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `id`, `ts` FROM `logs`.`access` WHERE (`id` > ?) ORDER BY `id` LIMIT 2")).WithArgs([]byte("2")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ts"}).AddRow("3", "2026-02-01"))
	mock.ExpectExec("ROLLBACK").WillReturnResult(sqlmock.NewResult(0, 0))
	s := &selectSource{db: sqlx.NewDb(db, "mysql"), schema: "logs", table: "access", cols: []string{"id", "ts"}, key: []string{"id"}, step: 2, partColumn: "ts"}
	gtid, from, err := s.open(context.Background())
	if err != nil || gtid != "0-1-77" || from != "2026-01-05 00:00:00" {
		t.Fatal(gtid, from, err)
	}
	var rows []string
	if err := s.each(context.Background(), func(r []interface{}) error {
		ts := "NULL"
		if r[1] != nil {
			ts = string(r[1].([]byte))
		}
		rows = append(rows, string(r[0].([]byte))+"/"+ts)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.close()
	if strings.Join(rows, " ") != "1/2026-01-05 2/NULL 3/2026-02-01" {
		t.Fatal(rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The split dump init: the backup's GTID from its metadata, the table's chunks in order
// (gzip or not), the earliest partition value from a first pass; another table's files and
// a row of another width refused.
func TestDumpSource(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, gz bool) {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if !gz {
			f.WriteString(content)
			return
		}
		w := gzip.NewWriter(f)
		w.Write([]byte(content))
		w.Close()
	}
	write("metadata", "[source]\nFile = mariadb-bin.000003\nPosition = 1234\nExecuted_Gtid_Set = 0-1-500\n", false)
	write("logs.access-schema.sql.gz", "CREATE TABLE `access` (`id` int, `ts` datetime, `msg` text);\n", true)
	write("logs.access.00000.sql.gz", "/*!40101 SET NAMES binary*/;\nINSERT INTO `access` VALUES\n(1,'2026-03-02 10:00:00','a\\tb'),\n(2,'2026-01-09 00:00:00',NULL);\n", true)
	write("logs.access.00001.sql", "INSERT INTO `access` VALUES (3,'2026-02-01 00:00:00','it''s');\n", false)
	write("logs.audit.00000.sql", "INSERT INTO `audit` VALUES (9,'2020-01-01 00:00:00','x');\n", false)
	s := &dumpSource{dir: dir, schema: "logs", table: "access", ncols: 3, partPos: 1}
	gtid, from, err := s.open(context.Background())
	if err != nil || gtid != "0-1-500" || from != "2026-01-09" {
		t.Fatal(gtid, from, err)
	}
	var got []string
	if err := s.each(context.Background(), func(r []interface{}) error {
		msg := "NULL"
		if r[2] != nil {
			msg = string(r[2].([]byte))
		}
		got = append(got, string(r[0].([]byte))+"|"+msg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "1|a\tb 2|NULL 3|it's" {
		t.Fatalf("%q", got)
	}
	s.ncols = 4
	if err := s.each(context.Background(), func([]interface{}) error { return nil }); err == nil {
		t.Fatal("a row of another width must be refused")
	}
	// no GTID in the metadata: refused
	write("metadata", "[source]\nFile = mariadb-bin.000003\nPosition = 1234\n", false)
	if _, _, err := (&dumpSource{dir: dir, schema: "logs", table: "access", ncols: 3, partPos: -1}).open(context.Background()); err == nil {
		t.Fatal("a backup without GTID must be refused")
	}
}

// The start of a feed without a position: the configured start, else the oldest backup an
// init reads, else the source's current position.
func TestStartGTID(t *testing.T) {
	dirs := map[string]string{}
	for name, g := range map[string]string{"old": "0-1-100", "new": "0-1-300"} {
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "metadata"), []byte("File = b.1\nPosition = 4\nExecuted_Gtid_Set = "+g+"\n"), 0644)
		dirs[name] = d
	}
	jobs := []*initJob{{src: &dumpSource{dir: dirs["new"]}}, {src: &selectSource{}}, {src: &dumpSource{dir: dirs["old"]}}}
	if g, err := startGTID(context.Background(), nil, wire.Config{StartGTID: "0-1-5"}, jobs); err != nil || g != "0-1-5" {
		t.Fatal(g, err)
	}
	if g, err := startGTID(context.Background(), nil, wire.Config{}, jobs); err != nil || g != "0-1-100" {
		t.Fatal(g, err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT @@gtid_current_pos")).WillReturnRows(sqlmock.NewRows([]string{"g"}).AddRow("0-1-900"))
	if g, err := startGTID(context.Background(), sqlx.NewDb(db, "mysql"), wire.Config{}, jobs[1:2]); err != nil || g != "0-1-900" {
		t.Fatal(g, err)
	}
	for _, c := range []struct {
		b, p string
		want bool
	}{{"1-11-1708965", "1-11-1708965,3058267966-15-52", true}, {"1-11-100", "1-11-101", false}, {"1-11-100,2-11-5", "2-11-6", false}, {"1-11-100", "1-11-99", true}, {"", "1-11-5", false}, {"2-11-5", "1-11-5", false}} {
		if got, err := coversKnownDomains(c.b, c.p); err != nil || got != c.want {
			t.Fatalf("backup %s covers %s: %v %v", c.b, c.p, got, err)
		}
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0-1-10", "0-1-9", true}, {"0-1-10", "0-1-10", true}, {"0-1-10", "0-1-11", false}, {"0-1-10,1-1-5", "1-1-6", false}, {"0-1-10", "", true}} {
		if got, err := gtidContains(c.a, c.b); err != nil || got != c.want {
			t.Fatalf("%s contains %s: %v %v", c.a, c.b, got, err)
		}
	}
}

// The delta of a table-mode source table: nothing before its init is done (the init holds
// it), nothing within the init's GTID, then routed to its destination; a table of the rules
// stays; a paused index gets nothing.
func TestRouteDelta(t *testing.T) {
	cfg := wire.Config{Rules: wire.RulesOf([]string{"app.users"}), Indexes: []wire.Index{{Name: "logs", Mode: "table", Tables: []string{"logs.access", "app.users"}, Schema: "logs_p"}}}
	copies, err := copyDests(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := &applier{cfg: cfg, copies: copies}
	m := wire.Manifest{GTID: "0-1-50", Tables: []wire.TableFiles{{Schema: "logs", Table: "access"}, {Schema: "app", Table: "users"}}}
	names := func(ts []wire.TableFiles) string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Schema+"."+t.Table)
		}
		return strings.Join(out, " ")
	}
	done := &wire.Applied{}
	if got := names(a.routeDelta(m, done)); got != "app.users" {
		t.Fatal("before the init:", got)
	}
	done.Inits = map[string]string{copies[0].initKey(): "0-1-60", copies[1].initKey(): "0-1-40"}
	if got := names(a.routeDelta(m, done)); got != "app.users logs_p.users" {
		t.Fatal("within the init of access:", got)
	}
	m.GTID = "0-1-61"
	if got := names(a.routeDelta(m, done)); got != "logs_p.access app.users logs_p.users" {
		t.Fatal("after the init:", got)
	}
	done.Paused = map[string]string{copies[0].id: "a DDL"}
	if got := names(a.routeDelta(m, done)); got != "app.users" {
		t.Fatal("paused:", got)
	}
	for _, bad := range []wire.Index{
		{Name: "x", Mode: "table", Tables: []string{"logs.access"}, Schema: "logs"},
		{Name: "x", Mode: "table", Tables: []string{"access"}, Schema: "p"},
		{Name: "x", Mode: "table", Tables: []string{"a.t", "b.t"}, Schema: "p"},
		{Name: "x", Mode: "table", Tables: []string{"a.t", "b.u"}, Schema: "p", Table: "one"},
	} {
		if _, err := copyDests(wire.Config{Indexes: []wire.Index{bad}}); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
}

// The rows of an init part split by partition, each row with its key line.
func TestSplitByPartition(t *testing.T) {
	pt := &partitionedTable{p: &partitioning{column: "ts", period: "month"}, pos: 1, first: day("2026-01-01")}
	rows := []byte("1\t2026-01-05 00:00:00\n2\t2026-02-01 00:00:00\n3\t\\N\n4\t2026-02-28 23:59:59\n")
	keys := []byte("1\t2026-01-05 00:00:00\n2\t2026-02-01 00:00:00\n3\t\\N\n4\t2026-02-28 23:59:59\n")
	g := splitByPartition(rows, keys, pt)
	if len(g) != 2 || g["p20260101"].n != 2 || g["p20260201"].n != 2 || string(g["p20260201"].keys) != "2\t2026-02-01 00:00:00\n4\t2026-02-28 23:59:59\n" {
		t.Fatalf("%+v", g)
	}
	if one := splitByPartition(rows, keys, nil); len(one) != 1 || one[""].n != 4 {
		t.Fatalf("%+v", one)
	}
}

// The table mode refuses what it cannot carry and keys a partitioned copy by its primary key
// and the partition column.
func TestTableCopyCheck(t *testing.T) {
	col := func(name, typ string, null bool) dbhelper.TableColumn {
		return dbhelper.TableColumn{Name: name, DataType: typ, ColumnType: typ, Nullable: null}
	}
	base := func() *tableCopy {
		return &tableCopy{copyDest: copyDest{idx: wire.Index{Name: "logs"}, srcSchema: "logs", srcTable: "access"}, pk: []string{"id"},
			columns: []dbhelper.TableColumn{col("id", "bigint", false), col("ts", "datetime", false), col("msg", "text", true)}}
	}
	tc := base()
	tc.part = &partitioning{column: "TS", period: "day", ahead: 3}
	if err := tc.check(); err != nil || strings.Join(tc.key, ",") != "id,ts" {
		t.Fatal(tc.key, err)
	}
	if tc := base(); tc.check() != nil || strings.Join(tc.key, ",") != "id" {
		t.Fatal("unpartitioned key", tc.key)
	}
	for name, mut := range map[string]func(*tableCopy){
		"no key":         func(c *tableCopy) { c.pk = nil },
		"bit":            func(c *tableCopy) { c.columns = append(c.columns, col("b", "bit", true)) },
		"generated":      func(c *tableCopy) { c.columns[2].GenerationExp = sql.NullString{String: "x+1", Valid: true} },
		"missing column": func(c *tableCopy) { c.part = &partitioning{column: "nope", period: "day"} },
		"not a time":     func(c *tableCopy) { c.part = &partitioning{column: "msg", period: "day"} },
		"nullable":       func(c *tableCopy) { c.columns[1].Nullable = true; c.part = &partitioning{column: "ts", period: "day"} },
	} {
		c := base()
		mut(c)
		if err := c.check(); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	// the capture keys the copy's rows by the destination's key
	st := &schema.Table{Schema: "logs", Name: "access", Columns: []schema.TableColumn{{Name: "id"}, {Name: "ts"}, {Name: "msg"}}, PKColumns: []int{0}}
	if k := copyKey(st, []string{"id", "ts"}, []int{0}); len(k) != 2 || k[1] != 1 {
		t.Fatal(k)
	}
	if k := copyKey(st, []string{"id", "gone"}, []int{0}); len(k) != 1 {
		t.Fatal(k)
	}
}

// The transport: a checkpoint written as a stream is read back whole, listed in order,
// freed when acknowledged; an aborted one is never seen.
func TestDiskQueueTransport(t *testing.T) {
	q := &diskQueue{dir: t.TempDir(), maxPending: mbBytes(0)}
	e := wire.SchemaElement{Name: "logs-schema", Schema: "logs_p", Table: "access"}
	if err := publish(q, wire.Manifest{Seq: 2, GTID: "0-1-1", Schema: &e}, nil); err != nil {
		t.Fatal(err)
	}
	if err := publish(q, wire.Manifest{Seq: 3, GTID: "0-1-2", Tables: []wire.TableFiles{{Schema: "a", Table: "b", Inserts: "i"}}}, map[string][]byte{"i": []byte("1\n")}); err != nil {
		t.Fatal(err)
	}
	w, err := q.Create(4)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("half"))
	w.Abort()
	if p := q.Pending(0); len(p) != 2 || p[0] != 2 || p[1] != 3 {
		t.Fatal(p)
	}
	m, _, err := readSeq(q, 2)
	if err != nil || m.Schema == nil || m.Schema.Name != "logs-schema" {
		t.Fatal(m, err)
	}
	m, files, err := readSeq(q, 3)
	if err != nil || string(files["i"]) != "1\n" {
		t.Fatal(m, err)
	}
	q.Ack(2)
	if p := q.Pending(0); len(p) != 1 || q.PendingBytes() == 0 {
		t.Fatal(p)
	}
	q.Clear()
	if q.PendingBytes() != 0 {
		t.Fatal("cleared")
	}
}

// The rows of a checkpoint written by INSERTs (no LOAD DATA LOCAL): an empty text stays an
// empty text, \N a NULL, a tab and a newline kept; batches bounded.
func TestApplyDeleteThenInsert(t *testing.T) {
	_, conn, mock := mockConn(t)
	rows := wire.EncodeTSV(nil, []interface{}{"1", "", nil})
	rows = wire.EncodeTSV(rows, []interface{}{"2", "a\tb\nc", "x"})
	keys := wire.EncodeTSV(nil, []interface{}{"1"})
	keys = wire.EncodeTSV(keys, []interface{}{"2"})
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `d`.`t` WHERE")).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `d`.`t` VALUES (?,?,?),(?,?,?)")).
		WithArgs([]byte("1"), []byte(""), nil, []byte("2"), []byte("a\tb\nc"), []byte("x")).WillReturnResult(sqlmock.NewResult(0, 2))
	tf := wire.TableFiles{Schema: "d", Table: "t", KeyColumns: []string{"id"}, Deletes: "d", Inserts: "i"}
	if q, err := ApplyDeleteThenInsert(context.Background(), conn, "d", "t", tf, map[string][]byte{"d": keys, "i": rows}, nil); err != nil {
		t.Fatal(q, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
