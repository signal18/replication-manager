// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Partitioning is a property of an index, required by its destination (CDC_CORE.md
// "Partitioning"): a time column of the result and a period. The destination table is
// partitioned by RANGE on it, one partition per period named p<YYYYMMDD of its start>, the
// first one taking every older value (and NULL); the next PartitionsAhead periods after the
// current one always exist, so a time series always has somewhere to write. Every row goes to
// its partition by the column's value, at the init as for the delta. The periods are civil
// dates: a week starts on Monday (ISO).

const (
	defaultPartitionsAhead = 3
	// maxPartitions: MariaDB takes 8192 partitions per table; the plan stays under it, and
	// one apply adds at most maxPartitionsAdded (a value far in the future is refused, never
	// thousands of empty partitions: F3)
	maxPartitions      = 8000
	maxPartitionsAdded = 400
)

type partitioning struct {
	column string
	period string // day, week, month
	ahead  int
}

// newPartitioning is idx's partitioning, nil when it has none.
func newPartitioning(idx wire.Index) (*partitioning, error) {
	if idx.PartitionColumn == "" && idx.PartitionPeriod == "" {
		return nil, nil
	}
	if idx.PartitionColumn == "" {
		return nil, fmt.Errorf("index %s: partition_period without partition_column", idx.Name)
	}
	p := &partitioning{column: idx.PartitionColumn, period: strings.ToLower(idx.PartitionPeriod), ahead: idx.PartitionsAhead}
	switch p.period {
	case "day", "week", "month":
	case "":
		return nil, fmt.Errorf("index %s: partition_column without partition_period (day, week, month)", idx.Name)
	default:
		return nil, fmt.Errorf("index %s: partition_period %q: day, week or month", idx.Name, idx.PartitionPeriod)
	}
	if p.ahead <= 0 {
		p.ahead = defaultPartitionsAhead
	}
	return p, nil
}

// start is the start of t's period.
func (p *partitioning) start(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch p.period {
	case "week":
		wd := (int(d.Weekday()) + 6) % 7 // Monday 0
		return d.AddDate(0, 0, -wd)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return d
}

// next is the start of the period after the one starting at s.
func (p *partitioning) next(s time.Time) time.Time {
	switch p.period {
	case "week":
		return s.AddDate(0, 0, 7)
	case "month":
		return s.AddDate(0, 1, 0)
	}
	return s.AddDate(0, 0, 1)
}

func partitionName(s time.Time) string { return "p" + s.Format("20060102") }

// partitionStart reads a partition's start from its name; false for a name of another form.
func partitionStart(name string) (time.Time, bool) {
	if len(name) != 9 || name[0] != 'p' {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102", name[1:])
	return t, err == nil
}

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

// route is the partition a value goes to, given the destination's first partition start
// (older values and NULL go to the first).
func (p *partitioning) route(v interface{}, first time.Time) string {
	t, ok := parseTimeValue(v)
	if !ok || t.Before(first) {
		return partitionName(first)
	}
	return partitionName(p.start(t))
}

// plan is the partitions from the period of from to the period of upTo, each bounded by
// the next period's start.
func (p *partitioning) plan(from, upTo time.Time) ([]dbhelper.RangePartition, error) {
	var out []dbhelper.RangePartition
	for s := p.start(from); !s.After(p.start(upTo)); s = p.next(s) {
		if len(out) >= maxPartitions {
			return nil, fmt.Errorf("partitioning by %s from %s: more than %d partitions, choose a longer partition_period", p.period, from.Format("2006-01-02"), maxPartitions)
		}
		out = append(out, dbhelper.RangePartition{Name: partitionName(s), LessThan: p.next(s).Format("2006-01-02")})
	}
	return out, nil
}

// horizon is the start of the last period kept ahead of now.
func (p *partitioning) horizon(now time.Time) time.Time {
	s := p.start(now)
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
	first, last   time.Time
}

// partitionTable partitions the new, empty destination table from the period of from (zero:
// now) to the horizon.
func partitionTable(ctx context.Context, conn *sqlx.Conn, p *partitioning, schema, table string, from time.Time) (string, error) {
	now := time.Now().UTC()
	if from.IsZero() || from.After(now) {
		from = now
	}
	unix, q, err := partitionKind(ctx, conn, p, schema, table)
	if err != nil {
		return q, err
	}
	parts, err := p.plan(from, p.horizon(now))
	if err != nil {
		return "", err
	}
	clause, err := dbhelper.RangePartitionClause(p.column, unix, parts)
	if err != nil {
		return "", err
	}
	return dbhelper.PartitionTable(ctx, conn, schema, table, clause)
}

// partitionKind: the column's type decides the partitioning (RANGE COLUMNS on a DATE or
// DATETIME, RANGE (UNIX_TIMESTAMP()) on a TIMESTAMP); any other type is refused.
func partitionKind(ctx context.Context, conn *sqlx.Conn, p *partitioning, schema, table string) (bool, string, error) {
	t, q, err := dbhelper.GetColumnDataType(ctx, conn, schema, table, p.column)
	if err != nil {
		return false, q, err
	}
	switch t {
	case "date", "datetime":
		return false, q, nil
	case "timestamp":
		return true, q, nil
	case "":
		return false, q, fmt.Errorf("%s.%s has no column %s to partition by", schema, table, p.column)
	}
	return false, q, fmt.Errorf("%s.%s.%s is a %s: a partition column is a DATE, DATETIME or TIMESTAMP", schema, table, p.column, t)
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
	for _, n := range names {
		s, ok := partitionStart(n)
		if !ok {
			return nil, fmt.Errorf("%s.%s: partition %s is not one of the CDC's (p<YYYYMMDD>)", schema, table, n)
		}
		if pt.first.IsZero() || s.Before(pt.first) {
			pt.first = s
		}
		if s.After(pt.last) {
			pt.last = s
		}
	}
	if pt.first.IsZero() {
		return nil, fmt.Errorf("%s.%s is not partitioned: the index says it is (partition_column %s), rebuild it", schema, table, p.column)
	}
	return pt, nil
}

// ensure adds the partitions up to the horizon and up to the period of upTo (the latest value
// about to be written), at most maxPartitionsAdded at once.
func (pt *partitionedTable) ensure(ctx context.Context, conn *sqlx.Conn, now, upTo time.Time) (string, error) {
	target := pt.p.horizon(now)
	if s := pt.p.start(upTo); s.After(target) {
		target = s
	}
	if !target.After(pt.last) {
		return "", nil
	}
	var parts []dbhelper.RangePartition
	for s := pt.p.next(pt.last); !s.After(target); s = pt.p.next(s) {
		if len(parts) >= maxPartitionsAdded {
			return "", fmt.Errorf("%s.%s: a value of %s up to %s needs more than %d new partitions", pt.schema, pt.table, pt.p.column, upTo.Format("2006-01-02"), maxPartitionsAdded)
		}
		parts = append(parts, dbhelper.RangePartition{Name: partitionName(s), LessThan: pt.p.next(s).Format("2006-01-02")})
	}
	q, err := dbhelper.AddRangePartitions(ctx, conn, pt.schema, pt.table, pt.unixTimestamp, parts)
	if err != nil {
		return q, err
	}
	pt.last = pt.p.start(target)
	return q, nil
}

// latest is the latest partition-column value of rows (a TSV file of the destination's rows).
func (pt *partitionedTable) latest(rows []byte) time.Time {
	var max time.Time
	forEachTSV(rows, func(fields [][]byte) {
		if pt.pos < len(fields) {
			if t, ok := parseTimeValue(fields[pt.pos]); ok && t.After(max) {
				max = t
			}
		}
	})
	return max
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
