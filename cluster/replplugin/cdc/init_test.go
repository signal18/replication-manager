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

// d is the ordinal of a date.
func d(s string) int64 { return dayOrdinal(day(s)) }

// The periods: a day, an ISO week from Monday, a calendar month, a year; each partition named by
// its start, bounded by the next start; older values and NULL go to the first partition.
func TestPartitionPeriods(t *testing.T) {
	for _, c := range []struct{ period, value, start, next string }{
		{"day", "2026-11-28", "2026-11-28", "2026-11-29"},
		{"week", "2026-11-28", "2026-11-23", "2026-11-30"}, // a Saturday: its Monday
		{"week", "2026-11-23", "2026-11-23", "2026-11-30"},
		{"month", "2026-12-31", "2026-12-01", "2027-01-01"},
		{"year", "2026-11-28", "2026-01-01", "2027-01-01"},
	} {
		p := &partitioning{column: "ts", period: c.period, ahead: 3}
		s := p.start(d(c.value))
		if p.literal(s) != c.start || p.literal(p.next(s)) != c.next {
			t.Fatalf("%s of %s: %s, next %s", c.period, c.value, p.literal(s), p.literal(p.next(s)))
		}
	}
	p := &partitioning{column: "ts", period: "month", ahead: 2}
	parts, err := p.plan(d("2026-09-15"), p.horizon(day("2026-11-28"), 0))
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
	first := d("2026-09-01")
	for v, want := range map[string]string{"2026-11-28 10:11:12.5": "p20261101", "2020-01-01": "p20260901", "0000-00-00 00:00:00": "p20260901"} {
		if r := p.route([]byte(v), first); r != want {
			t.Fatalf("route %s: %s", v, r)
		}
	}
	if r := p.route(nil, first); r != "p20260901" {
		t.Fatalf("NULL: %s", r)
	}
	if s, ok := p.startOf("p20261101"); !ok || s != d("2026-11-01") {
		t.Fatal("startOf")
	}
	if _, ok := p.startOf("pmax"); ok {
		t.Fatal("pmax is not one of the CDC's")
	}
	// bounded: a daily plan of 30 years is refused, never 10000 partitions
	dd := &partitioning{column: "ts", period: "day", ahead: 3}
	if _, err := dd.plan(d("1996-01-01"), d("2026-11-28")); err == nil {
		t.Fatal("a plan past maxPartitions must be refused")
	}
	for _, bad := range []wire.Index{{Name: "i", PartitionColumn: "ts"}, {Name: "i", PartitionPeriod: "day"}, {Name: "i", PartitionColumn: "ts", PartitionPeriod: "decade"},
		{Name: "i", PartitionColumn: "id", PartitionPeriod: "100M"}, {Name: "i", PartitionColumn: "id", PartitionPeriod: "3M"}, {Name: "i", PartitionColumn: "id", PartitionPeriod: "M"},
		{Name: "i", PartitionColumn: "id", PartitionPeriod: "10M", PartitionPurgeAfterDays: 30}} {
		if _, err := newPartitioning(bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
	if p, _ := newPartitioning(wire.Index{Name: "i", PartitionColumn: "ts", PartitionPeriod: "Week"}); p == nil || p.ahead != defaultPartitionsAhead || p.period != "week" || p.integer() {
		t.Fatalf("%+v", p)
	}
	for period, step := range map[string]int64{"1M": 1000000, "2M": 2000000, "5m": 5000000, "10M": 10000000, "20M": 20000000, "50M": 50000000} {
		if p, err := newPartitioning(wire.Index{Name: "i", PartitionColumn: "id", PartitionPeriod: period}); err != nil || p.step != step {
			t.Fatalf("%s: %+v %v", period, p, err)
		}
	}
	// the column's type goes with the period
	tp := &partitioning{period: "month"}
	ip := &partitioning{period: "10m", step: 10000000}
	for typ, want := range map[string][2]bool{"datetime": {true, false}, "timestamp": {true, false}, "int": {false, true}, "bigint": {false, true}, "smallint": {false, false}, "tinyint": {false, false}, "varchar": {false, false}} {
		if (tp.takes(typ) == "") != want[0] || (ip.takes(typ) == "") != want[1] {
			t.Fatalf("%s: time %q, ids %q", typ, tp.takes(typ), ip.takes(typ))
		}
	}
}

// An id range: partitions of <n>M ids named by their start on 14 digits, the first one taking
// every lower value, partitions_ahead steps after the highest id written.
func TestPartitionIDRange(t *testing.T) {
	p, err := newPartitioning(wire.Index{Name: "i", PartitionColumn: "id", PartitionPeriod: "10M", PartitionsAhead: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p.name(10000000) != "p00000010000000" || p.start(19999999) != 10000000 || p.next(10000000) != 20000000 || p.literal(20000000) != "20000000" {
		t.Fatal(p.name(10000000), p.start(19999999))
	}
	if s, ok := p.startOf("p00000010000000"); !ok || s != 10000000 {
		t.Fatal(s, ok)
	}
	if _, ok := p.startOf("p20261101"); ok {
		t.Fatal("a date name is not an id range's")
	}
	parts, err := p.plan(1, p.horizon(time.Time{}, 25000000))
	if err != nil || len(parts) != 5 || parts[0].Name != "p00000000000000" || parts[0].LessThan != "10000000" || parts[4].Name != "p00000040000000" {
		t.Fatal(parts, err)
	}
	if r := p.route([]byte("35000001"), 0); r != "p00000030000000" {
		t.Fatal(r)
	}
	if r := p.route([]byte("-5"), 0); r != "p00000000000000" {
		t.Fatal(r)
	}
	_, conn, mock := mockConn(t)
	pt := &partitionedTable{p: p, schema: "app", table: "events", pos: 0, first: 0, last: 40000000}
	// no row written: an id range does not move
	if q, err := pt.ensure(context.Background(), conn, time.Now(), 0, false); err != nil || q != "" {
		t.Fatal(q, err)
	}
	// id 41000000 written: 2 steps after its own
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `app`.`events` ADD PARTITION (PARTITION `p00000050000000` VALUES LESS THAN (60000000), PARTITION `p00000060000000` VALUES LESS THAN (70000000))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	top, ok := pt.latest([]byte("41000000\tx\n12\ty\n"))
	if _, err := pt.ensure(context.Background(), conn, time.Now(), top, ok); err != nil || pt.last != 60000000 {
		t.Fatal(err, pt.last)
	}
	// no purge on an id range
	if q, dropped, err := pt.purge(context.Background(), conn, time.Now()); err != nil || q != "" || dropped != nil {
		t.Fatal(q, dropped, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
	pt := &partitionedTable{p: p, schema: "logs_p", table: "access", pos: 1, first: d("2026-11-20"), last: d("2026-11-28")}
	now := day("2026-11-28").Add(10 * time.Hour)
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `logs_p`.`access` ADD PARTITION (PARTITION `p20261129` VALUES LESS THAN ('2026-11-30'), PARTITION `p20261130` VALUES LESS THAN ('2026-12-01'))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := pt.ensure(context.Background(), conn, now, 0, false); err != nil {
		t.Fatal(err)
	}
	if pt.last != d("2026-11-30") {
		t.Fatalf("last %s", p.literal(pt.last))
	}
	// nothing to add: no statement
	if _, err := pt.ensure(context.Background(), conn, now, d("2026-11-29"), true); err != nil {
		t.Fatal(err)
	}
	// a row of 2026-12-02: its partition first
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `logs_p`.`access` ADD PARTITION (PARTITION `p20261201` VALUES LESS THAN ('2026-12-02'), PARTITION `p20261202` VALUES LESS THAN ('2026-12-03'))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	top, ok := pt.latest([]byte("1\t2026-12-02 01:00:00\n2\t2026-11-01 00:00:00\n"))
	if _, err := pt.ensure(context.Background(), conn, now, top, ok); err != nil {
		t.Fatal(err)
	}
	// a TIMESTAMP column: bounds by UNIX_TIMESTAMP
	pt.unixTimestamp = true
	mock.ExpectExec(regexp.QuoteMeta("ADD PARTITION (PARTITION `p20261203` VALUES LESS THAN (UNIX_TIMESTAMP('2026-12-04')))")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := pt.ensure(context.Background(), conn, now, d("2026-12-03"), true); err != nil {
		t.Fatal(err)
	}
	// a value years ahead: refused, nothing added
	if _, err := pt.ensure(context.Background(), conn, now, d("2030-01-01"), true); err == nil {
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
	s := &dumpSource{dir: dir, schema: "logs", table: "access", ncols: 3, partPos: 1, part: &partitioning{column: "ts", period: "day"}}
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
	pt := &partitionedTable{p: &partitioning{column: "ts", period: "month"}, pos: 1, first: d("2026-01-01")}
	rows := []byte("1\t2026-01-05 00:00:00\n2\t2026-02-01 00:00:00\n3\t\\N\n4\t2026-02-28 23:59:59\n")
	keys := []byte("1\t2026-01-05 00:00:00\n2\t2026-02-01 00:00:00\n3\t\\N\n4\t2026-02-28 23:59:59\n")
	g, _ := splitByPartition(rows, keys, pt, day("2026-03-01"))
	if len(g) != 2 || g["p20260101"].n != 2 || g["p20260201"].n != 2 || string(g["p20260201"].keys) != "2\t2026-02-01 00:00:00\n4\t2026-02-28 23:59:59\n" {
		t.Fatalf("%+v", g)
	}
	if one, _ := splitByPartition(rows, keys, nil, day("2026-03-01")); len(one) != 1 || one[""].n != 4 {
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

// One source table copied by two indexes (two destinations, one by SELECT, one from a
// backup): its delta goes to each destination once that one's init is done; the capture keys
// its rows by the primary key and the partition column of every copy.
func TestTwoCopiesOfOneTable(t *testing.T) {
	cfg := wire.Config{Indexes: []wire.Index{
		{Name: "sel", Mode: "table", Tables: []string{"logs.access"}, Schema: "logs_sel", PartitionColumn: "ts", PartitionPeriod: "week"},
		{Name: "bak", Mode: "table", Tables: []string{"logs.access"}, Schema: "logs_bak", Init: "backup", PartitionColumn: "day", PartitionPeriod: "month"},
	}}
	copies, err := copyDests(cfg)
	if err != nil || len(copies) != 2 {
		t.Fatal(copies, err)
	}
	a := &applier{cfg: cfg, copies: copies}
	m := wire.Manifest{GTID: "0-1-9", Tables: []wire.TableFiles{{Schema: "logs", Table: "access"}}}
	done := &wire.Applied{Inits: map[string]string{copies[1].initKey(): "0-1-5"}}
	if got := a.routeDelta(m, done); len(got) != 1 || got[0].Schema != "logs_bak" {
		t.Fatal(got)
	}
	done.Inits[copies[0].initKey()] = "0-1-5"
	if got := a.routeDelta(m, done); len(got) != 2 || got[0].Schema != "logs_sel" || got[1].Schema != "logs_bak" {
		t.Fatal(got)
	}
	c := &capture{copies: map[string][]*tableCopy{"logs.access": {{key: []string{"id", "ts"}}, {key: []string{"id", "day"}}}}}
	if k := c.copyKeyNames("logs", "access"); strings.Join(k, ",") != "id,ts,day" {
		t.Fatal(k)
	}
}

// An index partitioned: the partition column joins the destination's key (the -schema
// element's PRIMARY KEY, the delete key); a NULL-able column or an expression is refused.
func TestIndexPartitionKey(t *testing.T) {
	saved := columnsOfTable
	defer func() { columnsOfTable = saved }()
	columnsOfTable = func(_ context.Context, _ *sqlx.DB, schema, table string) ([]dbhelper.TableColumn, error) {
		return []dbhelper.TableColumn{{Name: "o_id", ColumnType: "int(11)"}, {Name: "o_entry_d", DataType: "datetime", ColumnType: "datetime"}, {Name: "o_note", DataType: "datetime", ColumnType: "datetime", Nullable: true}}, nil
	}
	meta := func() *indexMeta {
		return &indexMeta{idx: wire.Index{Name: "o", Schema: "dn", Table: "o"}, key: []string{"o_id"}, keyPos: []int{0}, columns: []string{"o_id", "o_entry_d", "o_note", "x"},
			fields: []field{{name: "o_id", schema: "s", orgTable: "orders", orgName: "o_id"}, {name: "o_entry_d", schema: "s", orgTable: "orders", orgName: "o_entry_d"},
				{name: "o_note", schema: "s", orgTable: "orders", orgName: "o_note"}, {name: "x", typ: 8}},
			tables: []sourceTable{{schema: "s", table: "orders", pk: []string{"o_id"}, cols: []string{"o_id"}}}}
	}
	m := meta()
	m.part = &partitioning{column: "o_entry_d", period: "month", ahead: 3}
	if err := m.addPartitionKey(context.Background(), nil); err != nil || strings.Join(m.key, ",") != "o_id,o_entry_d" || len(m.keyPos) != 2 || m.keyPos[1] != 1 {
		t.Fatal(m.key, m.keyPos, err)
	}
	e, err := indexSchemaElement(context.Background(), nil, m)
	if err != nil || strings.Join(e.Key, ",") != "o_id,o_entry_d" || strings.Join(e.DeleteKey, ",") != "o_id,o_entry_d" || e.Columns[1].Nullable || !e.Columns[2].Nullable {
		t.Fatalf("%+v %v", e, err)
	}
	for _, col := range []string{"o_note", "x", "nope"} {
		m := meta()
		m.part = &partitioning{column: col, period: "month"}
		if err := m.addPartitionKey(context.Background(), nil); err == nil {
			t.Fatalf("partition column %s must be refused", col)
		}
	}
}

// A source table without a primary key, keyed by a unique key of NOT NULL columns: canal knows
// only a PRIMARY, the capture finds its rows by the resolved key.
func TestRowKeyWithoutPrimaryKey(t *testing.T) {
	tb := &schema.Table{Schema: "logs", Name: "audit", Columns: []schema.TableColumn{{Name: "a"}, {Name: "uid"}, {Name: "seq"}}}
	c := &capture{rowKeys: map[string][]string{"logs.audit": {"uid", "seq"}}}
	c.rowKey(tb)
	if len(tb.PKColumns) != 2 || tb.PKColumns[0] != 1 || tb.PKColumns[1] != 2 {
		t.Fatal(tb.PKColumns)
	}
	other := &schema.Table{Schema: "logs", Name: "other", Columns: []schema.TableColumn{{Name: "a"}}}
	c.rowKey(other)
	if len(other.PKColumns) != 0 {
		t.Fatal(other.PKColumns)
	}
}

// The purge: a partition whose whole range ended more than partition_purge_after_days ago is
// dropped (never the last one, never the one being written), and the rows older than that
// horizon are left out of the init and the delta, counted.
func TestPartitionPurge(t *testing.T) {
	p := &partitioning{column: "ts", period: "month", ahead: 1, purge: 40}
	now := day("2026-11-28")
	if k, on := p.keptFrom(now); !on || k != d("2026-10-01") { // now - 40 days = 2026-10-19: October kept
		t.Fatal(p.literal(k), on)
	}
	if !p.purged([]byte("2026-09-30 23:59:59"), now) || p.purged([]byte("2026-10-01 00:00:00"), now) || p.purged(nil, now) {
		t.Fatal("purge horizon")
	}
	if (&partitioning{period: "month"}).purged([]byte("2000-01-01"), now) {
		t.Fatal("no purge when off")
	}
	_, conn, mock := mockConn(t)
	pt := &partitionedTable{p: p, schema: "logs_p", table: "access", pos: 1, first: d("2026-08-01"), last: d("2026-12-01")}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT PARTITION_NAME FROM information_schema.PARTITIONS")).WillReturnRows(sqlmock.NewRows([]string{"n"}).
		AddRow("p20260801").AddRow("p20260901").AddRow("p20261001").AddRow("p20261101").AddRow("p20261201"))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `logs_p`.`access` DROP PARTITION `p20260801`, `p20260901`")).WillReturnResult(sqlmock.NewResult(0, 0))
	q, dropped, err := pt.purge(context.Background(), conn, now)
	if err != nil || len(dropped) != 2 || pt.first != d("2026-10-01") || !strings.Contains(q, "DROP PARTITION") {
		t.Fatal(q, dropped, err, pt.first)
	}
	// nothing more to drop: no statement
	if q, dropped, err := pt.purge(context.Background(), conn, now); err != nil || q != "" || len(dropped) != 0 {
		t.Fatal(q, dropped, err)
	}
	// never the last remaining partition, even past the horizon
	old := &partitionedTable{p: p, schema: "logs_p", table: "old", first: d("2020-01-01"), last: d("2020-01-01")}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT PARTITION_NAME FROM information_schema.PARTITIONS")).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow("p20200101"))
	if q, dropped, err := old.purge(context.Background(), conn, now); err != nil || q != "" || len(dropped) != 0 {
		t.Fatal(q, dropped, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	// the rows past the horizon are left out, of a part as of the delta
	pt.first = d("2026-10-01")
	rows := []byte("1\t2026-09-30 10:00:00\n2\t2026-10-02 10:00:00\n3\t2026-11-27 10:00:00\n")
	g, skipped := splitByPartition(rows, rows, pt, now)
	if skipped != 1 || g["p20261001"].n != 1 || g["p20261101"].n != 1 || len(g) != 2 {
		t.Fatal(skipped, g)
	}
	kept, skipped := withoutPurged(rows, pt, now)
	if skipped != 1 || string(kept) != "2\t2026-10-02 10:00:00\n3\t2026-11-27 10:00:00\n" {
		t.Fatalf("%d %q", skipped, kept)
	}
	if _, err := newPartitioning(wire.Index{Name: "i", PartitionColumn: "ts", PartitionPeriod: "year", PartitionPurgeAfterDays: -1}); err == nil {
		t.Fatal("a negative purge must be refused")
	}
}
