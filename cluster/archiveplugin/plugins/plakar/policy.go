// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/PlakarKorp/kloset/locate"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

// retention is a keep-within duration ("7d", "2y5m7d3h"): calendar years, months, weeks,
// days and hours, as restic reads them (m is a month).
type retention struct {
	years, months, days, hours int
}

var retentionRe = regexp.MustCompile(`^(\d+[ymwdh])+$`)
var retentionPartRe = regexp.MustCompile(`(\d+)([ymwdh])`)

func parseRetention(s string) (retention, error) {
	var r retention
	if !retentionRe.MatchString(s) {
		return r, fmt.Errorf("invalid duration %q (want e.g. 7d or 2y5m7d3h)", s)
	}
	for _, m := range retentionPartRe.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return r, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		switch m[2] {
		case "y":
			r.years += n
		case "m":
			r.months += n
		case "w":
			r.days += 7 * n
		case "d":
			r.days += n
		case "h":
			r.hours += n
		}
	}
	return r, nil
}

// before is the start of the window of r ending at t.
func (r retention) before(t time.Time) time.Time {
	return t.AddDate(-r.years, -r.months, -r.days).Add(-time.Duration(r.hours) * time.Hour)
}

// bucketsBetween is the number of calendar buckets of a period (hour, day, week, month,
// year) from the one holding from to the one holding to, both included.
func bucketsBetween(period string, from, to time.Time) int {
	from, to = from.UTC(), to.UTC()
	switch period {
	case "hour":
		return int(to.Truncate(time.Hour).Sub(from.Truncate(time.Hour))/time.Hour) + 1
	case "day":
		f := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
		t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		return int(t.Sub(f)/(24*time.Hour)) + 1
	case "week":
		return bucketsBetween("day", from, to)/7 + 1
	case "month":
		return (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month()) + 1
	case "year":
		return to.Year() - from.Year() + 1
	}
	return 0
}

// keptByPolicy is the set of snapshots the policy keeps among items, the rest goes.
//
// The reference time is the newest snapshot, not now (as restic does): an archive whose
// backups stopped keeps its last ones instead of aging them all out.
//   - keepLast N: the N newest;
//   - keepWithin D: every snapshot of the window D;
//   - keepHourly/Daily/Weekly/Monthly/Yearly N: kloset locate periods, Keep N buckets
//     (calendar hours, days... back from the reference) with Cap 1: the newest of each;
//   - keepWithinHourly/... D: the same period over the buckets of the window D, the larger
//     of the two when both are set.
//
// Unlike restic's keep-daily N (N days that have a snapshot), a locate bucket counts
// whether it holds a snapshot or not: with a missed day, keep-daily 7 keeps 6 dailies.
func keptByPolicy(items []locate.Item, p *wire.Policy) (map[objects.MAC]struct{}, error) {
	if p == nil {
		return nil, errors.New("purge: no policy")
	}
	kept := make(map[objects.MAC]struct{})
	if len(items) == 0 {
		return kept, nil
	}
	sorted := append([]locate.Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp.After(sorted[j].Timestamp) })
	ref := sorted[0].Timestamp.UTC()

	rules := 0
	if p.KeepLast > 0 {
		rules++
		for i := 0; i < p.KeepLast && i < len(sorted); i++ {
			kept[sorted[i].ItemID] = struct{}{}
		}
	}
	if p.KeepWithin != "" {
		rules++
		r, err := parseRetention(p.KeepWithin)
		if err != nil {
			return nil, fmt.Errorf("keepWithin: %w", err)
		}
		cutoff := r.before(ref)
		for _, it := range sorted {
			if !it.Timestamp.Before(cutoff) {
				kept[it.ItemID] = struct{}{}
			}
		}
	}

	opts := locate.NewDefaultLocateOptions()
	periods := []struct {
		name   string
		count  int
		within string
		target *locate.LocatePeriod
	}{
		{"hour", p.KeepHourly, p.KeepWithinHourly, &opts.Periods.Hour},
		{"day", p.KeepDaily, p.KeepWithinDaily, &opts.Periods.Day},
		{"week", p.KeepWeekly, p.KeepWithinWeekly, &opts.Periods.Week},
		{"month", p.KeepMonthly, p.KeepWithinMonthly, &opts.Periods.Month},
		{"year", p.KeepYearly, p.KeepWithinYearly, &opts.Periods.Year},
	}
	for _, per := range periods {
		n := per.count
		if per.within != "" {
			r, err := parseRetention(per.within)
			if err != nil {
				return nil, fmt.Errorf("keepWithin %s: %w", per.name, err)
			}
			n = max(n, bucketsBetween(per.name, r.before(ref), ref))
		}
		if n > 0 {
			rules++
			*per.target = locate.LocatePeriod{Keep: n, Cap: 1}
		}
	}
	if rules == 0 {
		return nil, errors.New("purge: the policy keeps nothing, refusing to delete every snapshot")
	}
	if opts.HasPeriods() {
		periodKept, _ := opts.Match(sorted, ref)
		for id := range periodKept {
			kept[id] = struct{}{}
		}
	}
	return kept, nil
}
