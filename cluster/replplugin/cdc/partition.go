// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Partitioning is a property of an index, required by its destination (CDC_CORE.md
// "Partitioning"): a column of the result and a period. The destination table is partitioned by
// RANGE on it, the first partition taking every lower value (and NULL), every row routed to its
// partition by the column's value, at the init as for the delta. Two kinds of period:
//
//   - a time: day, week (ISO, from Monday), month or year on a DATE, DATETIME or TIMESTAMP
//     column, one partition per period named p<YYYYMMDD of its start>; the next PartitionsAhead
//     periods after the current one always exist (a time series always has somewhere to write);
//     the purge (partition_purge_after_days) drops the periods long past;
//   - a range of an integer (an auto-increment id): 1M, 2M, 5M, 10M, 20M or 50M ids on an INT or
//     BIGINT column (signed or unsigned), bounds multiples of the step, one partition per step named p<its start on 14 digits> (p00000010000000 holds
//     10M to 20M), the PartitionsAhead steps after the step of the highest value written always
//     exist; no purge (an id range has no age).
//
// A period start is an ordinal: the Unix time of its UTC date, or the integer itself.

const (
	defaultPartitionsAhead = 3
	// maxPartitions: MariaDB takes 8192 partitions per table; the plan stays under it, and
	// one apply adds at most maxPartitionsAdded (a value far ahead is refused, never
	// thousands of empty partitions: F3)
	maxPartitions      = 8000
	maxPartitionsAdded = 400
	// intNameWidth: the digits of an integer partition's start in its name (they sort)
	intNameWidth = 14
	maxIntStart  = 99999999999999
)

type partitioning struct {
	column string
	period string // day, week, month, year, or an id step (1m, 2m, 5m, 10m, 20m, 50m)
	step   int64  // an integer range's step, 0 for a time
	ahead  int
	purge  int // partition_purge_after_days, 0 off
}

// newPartitioning is idx's partitioning, nil when it has none.
func newPartitioning(idx wire.Index) (*partitioning, error) {
	if idx.PartitionColumn == "" && idx.PartitionPeriod == "" {
		return nil, nil
	}
	if idx.PartitionColumn == "" {
		return nil, fmt.Errorf("index %s: partition_period without partition_column", idx.Name)
	}
	p := &partitioning{column: idx.PartitionColumn, period: strings.ToLower(idx.PartitionPeriod), ahead: idx.PartitionsAhead, purge: idx.PartitionPurgeAfterDays}
	switch p.period {
	case "day", "week", "month", "year":
	case "":
		return nil, fmt.Errorf("index %s: partition_column without partition_period (day, week, month, year, or a range of ids: %s)", idx.Name, idStepList)
	default:
		n, ok := idSteps[p.period]
		if !ok {
			return nil, fmt.Errorf("index %s: partition_period %q: day, week, month, year, or a range of ids: %s", idx.Name, idx.PartitionPeriod, idStepList)
		}
		p.step = n * 1000000
	}
	if p.purge < 0 {
		return nil, fmt.Errorf("index %s: partition_purge_after_days is 0 (off) or more", idx.Name)
	}
	if p.purge > 0 && p.step > 0 {
		return nil, fmt.Errorf("index %s: partition_purge_after_days has no meaning on a range of ids (partition_period %s)", idx.Name, idx.PartitionPeriod)
	}
	if p.ahead <= 0 {
		p.ahead = defaultPartitionsAhead
	}
	return p, nil
}

// idSteps are the id ranges a partition takes, in millions: exactly these.
var idSteps = map[string]int64{"1m": 1, "2m": 2, "5m": 5, "10m": 10, "20m": 20, "50m": 50}

// idStepList is idSteps for a message.
const idStepList = "1M, 2M, 5M, 10M, 20M or 50M"

// integer: a range of an integer column, not a time.
func (p *partitioning) integer() bool { return p.step > 0 }

// takes says whether a column of DATA_TYPE t can carry this partitioning ("" it can).
func (p *partitioning) takes(t string) string {
	switch strings.ToLower(t) {
	case "date", "datetime", "timestamp":
		if p.integer() {
			return fmt.Sprintf("a %s column takes a time period (day, week, month, year), not %s", t, p.period)
		}
		return ""
	case "int", "integer", "bigint":
		if !p.integer() {
			return fmt.Sprintf("an integer column takes a range of ids (%s), not %s", idStepList, p.period)
		}
		return ""
	case "tinyint", "smallint", "mediumint":
		return fmt.Sprintf("a %s column is too small for ranges of millions: an id range partitions an INT or a BIGINT", t)
	}
	return fmt.Sprintf("a %s column: a partition column is a DATE, DATETIME, TIMESTAMP or an integer", t)
}

func dayOrdinal(t time.Time) int64 {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix()
}

func ordinalDay(o int64) time.Time { return time.Unix(o, 0).UTC() }

// start is the start of the period of the ordinal o.
func (p *partitioning) start(o int64) int64 {
	if p.integer() {
		s := o / p.step * p.step
		if o < 0 && o%p.step != 0 {
			s -= p.step
		}
		return s
	}
	d := ordinalDay(o)
	switch p.period {
	case "week":
		return dayOrdinal(d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)))
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	case "year":
		return time.Date(d.Year(), 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	}
	return dayOrdinal(d)
}

// next is the start of the period after the one starting at s.
func (p *partitioning) next(s int64) int64 {
	if p.integer() {
		return s + p.step
	}
	d := ordinalDay(s)
	switch p.period {
	case "week":
		return dayOrdinal(d.AddDate(0, 0, 7))
	case "month":
		return dayOrdinal(d.AddDate(0, 1, 0))
	case "year":
		return dayOrdinal(d.AddDate(1, 0, 0))
	}
	return dayOrdinal(d.AddDate(0, 0, 1))
}

// value reads a row value as an ordinal; false for NULL, a zero date or another text.
func (p *partitioning) value(v interface{}) (int64, bool) {
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		return 0, false
	}
	if p.integer() {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return n, err == nil
	}
	t, ok := parseTimeValue(s)
	if !ok {
		return 0, false
	}
	return dayOrdinal(t), true
}

// name is the partition of the period starting at s.
func (p *partitioning) name(s int64) string {
	if p.integer() {
		return fmt.Sprintf("p%0*d", intNameWidth, s)
	}
	return "p" + ordinalDay(s).Format("20060102")
}

// startOf reads a partition's start from its name; false for a name of another form.
func (p *partitioning) startOf(name string) (int64, bool) {
	if len(name) < 2 || name[0] != 'p' {
		return 0, false
	}
	if p.integer() {
		if len(name) != intNameWidth+1 {
			return 0, false
		}
		n, err := strconv.ParseInt(name[1:], 10, 64)
		return n, err == nil
	}
	if len(name) != 9 {
		return 0, false
	}
	t, err := time.Parse("20060102", name[1:])
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// literal is the bound of the period starting at s as the DDL writes it.
func (p *partitioning) literal(s int64) string {
	if p.integer() {
		return strconv.FormatInt(s, 10)
	}
	return ordinalDay(s).Format("2006-01-02")
}

// label is the period starting at s, for a message.
func (p *partitioning) label(s int64) string { return p.literal(s) }

// parseTimeValue reads a DATE, DATETIME or TIMESTAMP value as the server writes it
// ('2026-11-28', '2026-11-28 10:00:00[.ffffff]'); false for NULL, a zero date or another text.
func parseTimeValue(v interface{}) (time.Time, bool) {
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		return time.Time{}, false
	}
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil || t.Year() < 1 {
		return time.Time{}, false
	}
	return t, true
}

// keptFrom is the start of the oldest period the purge keeps at now: a partition is dropped once
// its whole range ended more than purge days ago, so the period holding now - purge days is the
// oldest kept. 0 and false when the purge is off.
func (p *partitioning) keptFrom(now time.Time) (int64, bool) {
	if p.purge <= 0 || p.integer() {
		return 0, false
	}
	return p.start(dayOrdinal(now.AddDate(0, 0, -p.purge))), true
}

// purged: a row of value v is older than the purge horizon (never written: it would land in
// the first partition, which takes every older value, and undo the purge). NULL is not.
func (p *partitioning) purged(v interface{}, now time.Time) bool {
	kept, ok := p.keptFrom(now)
	if !ok {
		return false
	}
	o, ok := p.value(v)
	return ok && o < kept
}

// route is the partition a value goes to, given the destination's first partition start
// (lower values and NULL go to the first).
func (p *partitioning) route(v interface{}, first int64) string {
	o, ok := p.value(v)
	if !ok || o < first {
		return p.name(first)
	}
	return p.name(p.start(o))
}

// plan is the partitions from the period of from to the period of upTo, each bounded by the
// next period's start.
func (p *partitioning) plan(from, upTo int64) ([]dbhelper.RangePartition, error) {
	var out []dbhelper.RangePartition
	for s := p.start(from); s <= p.start(upTo); s = p.next(s) {
		if len(out) >= maxPartitions {
			return nil, fmt.Errorf("partitioning by %s from %s: more than %d partitions, choose a longer partition_period", p.period, p.label(p.start(from)), maxPartitions)
		}
		if p.integer() && s > maxIntStart {
			return nil, fmt.Errorf("partitioning by %s: %d is past the partitions' names (%d digits)", p.period, s, intNameWidth)
		}
		out = append(out, dbhelper.RangePartition{Name: p.name(s), LessThan: p.literal(p.next(s))})
	}
	return out, nil
}

// horizon is the start of the last period kept ahead: ahead periods after now's (a time), or
// after the period of the highest value top (an integer range).
func (p *partitioning) horizon(now time.Time, top int64) int64 {
	s := p.start(top)
	if !p.integer() {
		s = p.start(dayOrdinal(now))
	}
	for i := 0; i < p.ahead; i++ {
		s = p.next(s)
	}
	return s
}

// partitionedTable is a partitioned destination table as the apply knows it: its column
// position in the rows, the kind of its partitioning, and its first and last partitions.
type partitionedTable struct {
	p             *partitioning
	schema, table string
	pos           int  // the partition column in the rows
	unixTimestamp bool // a TIMESTAMP column: RANGE (UNIX_TIMESTAMP(col))
	first, last   int64
	reloaded      bool // ensure read the partitions again once in this call
}

// partitionTable partitions the new, empty destination table from the period of from (the
// earliest value of the init, "" none: the current period, or 0 for an integer range) to the
// horizon.
func partitionTable(ctx context.Context, conn *sqlx.Conn, p *partitioning, schema, table, from string) (string, error) {
	now := time.Now().UTC()
	start, ok := p.value(from)
	if !ok {
		start = 0
		if !p.integer() {
			start = dayOrdinal(now)
		}
	}
	if !p.integer() && start > dayOrdinal(now) {
		start = dayOrdinal(now)
	}
	unix, q, err := partitionKind(ctx, conn, p, schema, table)
	if err != nil {
		return q, err
	}
	parts, err := p.plan(start, p.horizon(now, start))
	if err != nil {
		return "", err
	}
	clause, err := dbhelper.RangePartitionClause(p.column, unix, parts)
	if err != nil {
		return "", err
	}
	return dbhelper.PartitionTable(ctx, conn, schema, table, clause)
}

// partitionKind: the column's type decides the partitioning (RANGE COLUMNS on a DATE, DATETIME
// or an integer, RANGE (UNIX_TIMESTAMP()) on a TIMESTAMP, which RANGE COLUMNS does not take);
// a type that does not go with the period is refused.
func partitionKind(ctx context.Context, conn *sqlx.Conn, p *partitioning, schema, table string) (bool, string, error) {
	t, q, err := dbhelper.GetColumnDataType(ctx, conn, schema, table, p.column)
	if err != nil {
		return false, q, err
	}
	if t == "" {
		return false, q, fmt.Errorf("%s.%s has no column %s to partition by", schema, table, p.column)
	}
	if why := p.takes(t); why != "" {
		return false, q, fmt.Errorf("%s.%s.%s: %s", schema, table, p.column, why)
	}
	return t == "timestamp", q, nil
}

// openPartitioned reads a partitioned destination table's partitions on the target.
func openPartitioned(ctx context.Context, conn *sqlx.Conn, p *partitioning, schema, table string, pos int) (*partitionedTable, error) {
	unix, q, err := partitionKind(ctx, conn, p, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, q)
	}
	names, q, err := dbhelper.GetPartitionNames(ctx, conn, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, q)
	}
	pt := &partitionedTable{p: p, schema: schema, table: table, pos: pos, unixTimestamp: unix}
	for i, n := range names {
		s, ok := p.startOf(n)
		if !ok {
			return nil, fmt.Errorf("%s.%s: partition %s is not one of the CDC's for a period of %s", schema, table, n, p.period)
		}
		if i == 0 || s < pt.first {
			pt.first = s
		}
		if i == 0 || s > pt.last {
			pt.last = s
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s.%s is not partitioned: the index says it is (partition_column %s), rebuild it", schema, table, p.column)
	}
	return pt, nil
}

// ensure adds the partitions up to the horizon and up to the period of upTo (the highest value
// about to be written; ok false: none), at most maxPartitionsAdded at once, after the last one
// read on the target (openPartitioned). A value needing more is refused: the apply stops on that
// checkpoint with the value in its error (WARN0243), never writes it elsewhere; the operator
// fixes the row or raises the period.
func (pt *partitionedTable) ensure(ctx context.Context, conn *sqlx.Conn, now time.Time, upTo int64, ok bool) (string, error) {
	top := pt.last
	if ok && upTo > top {
		top = upTo
	}
	target := pt.p.horizon(now, top)
	if pt.p.integer() && !ok {
		target = pt.last // no value written: an id range moves with its values only
	}
	if ok {
		if s := pt.p.start(upTo); s > target {
			target = s
		}
	}
	if target <= pt.last {
		return "", nil
	}
	var parts []dbhelper.RangePartition
	for s := pt.p.next(pt.last); s <= target; s = pt.p.next(s) {
		if len(parts) >= maxPartitionsAdded || (pt.p.integer() && s > maxIntStart) {
			return "", fmt.Errorf("%s.%s: a value of %s up to %s needs more than %d new partitions", pt.schema, pt.table, pt.p.column, pt.p.label(pt.p.start(upTo)), maxPartitionsAdded)
		}
		parts = append(parts, dbhelper.RangePartition{Name: pt.p.name(s), LessThan: pt.p.literal(pt.p.next(s))})
	}
	q, err := dbhelper.AddRangePartitions(ctx, conn, pt.schema, pt.table, pt.unixTimestamp, parts)
	var me *mysql.MySQLError
	if err != nil && errors.As(err, &me) && (me.Number == 1517 || me.Number == 1493) && !pt.reloaded {
		// a partition of the plan exists already (made by hand, or the cache is stale: 1517
		// same name, 1493 not increasing): the partitions read again, once
		pt.reloaded = true
		defer func() { pt.reloaded = false }()
		if rerr := pt.reload(ctx, conn); rerr != nil {
			return q, fmt.Errorf("%w; reading its partitions again: %v", err, rerr)
		}
		return pt.ensure(ctx, conn, now, upTo, ok)
	}
	if err != nil {
		return q, err
	}
	pt.last = target
	return q, nil
}

// reload reads the destination's partitions again: its first and last.
func (pt *partitionedTable) reload(ctx context.Context, conn *sqlx.Conn) error {
	fresh, err := openPartitioned(ctx, conn, pt.p, pt.schema, pt.table, pt.pos)
	if err != nil {
		return err
	}
	pt.first, pt.last = fresh.first, fresh.last
	return nil
}

// purge drops the partitions whose whole range ended more than purge days ago: never the last
// remaining one, never the one being written (its range has not ended), at most
// maxPartitionsAdded at once. It returns the statement run ("" none) and the partitions dropped.
func (pt *partitionedTable) purge(ctx context.Context, conn *sqlx.Conn, now time.Time) (string, []string, error) {
	kept, on := pt.p.keptFrom(now)
	if !on || pt.first >= kept {
		return "", nil, nil
	}
	names, q, err := dbhelper.GetPartitionNames(ctx, conn, pt.schema, pt.table)
	if err != nil {
		return q, nil, err
	}
	var drop []string
	first, firstSet := int64(0), false
	for i, n := range names {
		s, ok := pt.p.startOf(n)
		if !ok {
			return "", nil, fmt.Errorf("%s.%s: partition %s is not one of the CDC's", pt.schema, pt.table, n)
		}
		if s < kept && pt.p.next(s) <= dayOrdinal(now) && i < len(names)-1 && len(drop) < maxPartitionsAdded {
			drop = append(drop, n)
			continue
		}
		if !firstSet {
			first, firstSet = s, true
		}
	}
	if len(drop) == 0 {
		return "", nil, nil
	}
	q, err = dbhelper.DropPartitions(ctx, conn, pt.schema, pt.table, drop)
	if err != nil {
		return q, nil, err
	}
	pt.first = first
	return q, drop, nil
}

// latest is the highest partition-column value of rows (a TSV file of the destination's rows);
// false when none has one.
func (pt *partitionedTable) latest(rows []byte) (int64, bool) {
	var max int64
	found := false
	forEachTSV(rows, func(fields [][]byte) {
		if pt.pos < len(fields) && fields[pt.pos] != nil {
			if o, ok := pt.p.value(fields[pt.pos]); ok && (!found || o > max) {
				max, found = o, true
			}
		}
	})
	return max, found
}

// forEachTSV calls fn with the fields of each line of a TSV file (wire.EncodeTSV).
func forEachTSV(b []byte, fn func(fields [][]byte)) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		var line []byte
		if i < 0 {
			line, b = b, nil
		} else {
			line, b = b[:i], b[i+1:]
		}
		if len(line) > 0 {
			fn(wire.DecodeTSV(line))
		}
	}
}
