// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scale

// TickOptions specifies constraints for constructing scale ticks.
//
// A Ticks method will return the ticks at the lowest level (largest
// number of ticks) that satisfies all of the constraints. The exact
// meaning of the tick level differs between scale types, but for all
// scales higher tick levels result in ticks that are further apart
// (fewer ticks in a given interval). In general, the minor ticks are
// the ticks from one level below the major ticks.
type TickOptions struct {
	// Max is the maximum number of major ticks to return.
	Max int

	// MinLevel and MaxLevel are the minimum and maximum tick
	// levels to accept, respectively. If they are both 0, there is
	// no limit on acceptable tick levels.
	MinLevel, MaxLevel int
}

// A Ticker computes tick marks for a scale. The "level" of the ticks
// controls how many ticks there are and how closely they are spaced.
// Higher levels have fewer ticks, while lower levels have more ticks.
// For example, on a numerical scale, one could have ticks at every
// n*(10^level).
type Ticker interface {
	// CountTicks returns the number of ticks at level in this
	// scale's input range. This is equivalent to
	// len(TicksAtLevel(level)), but should be much more
	// efficient. CountTicks is a weakly monotonically decreasing
	// function of level.
	CountTicks(level int) int

	// TicksAtLevel returns a slice of "nice" tick values in
	// increasing order at level in this scale's input range.
	// Typically, TicksAtLevel(l+1) is a subset of
	// TicksAtLevel(l). That is, higher levels remove ticks from
	// lower levels.
	TicksAtLevel(level int) interface{}
}

// FindLevel returns the lowest level that satisfies the constraints
// given by o:
//
// * ticker.CountTicks(level) <= o.Max
//
// * o.MinLevel <= level <= o.MaxLevel (if MinLevel and MaxLevel != 0).
//
// If the constraints cannot be satisfied, it returns 0, false.
//
// guess is the level to start the optimization at.
func (o *TickOptions) FindLevel(ticker Ticker, guess int) (int, bool) {
	minLevel, maxLevel := o.MinLevel, o.MaxLevel
	if minLevel == 0 && maxLevel == 0 {
		minLevel, maxLevel = -1000, 1000
	} else if minLevel > maxLevel {
		return 0, false
	}
	if o.Max < 1 {
		return 0, false
	}

	// Start with the initial guess.
	l := guess
	if l < minLevel {
		l = minLevel
	} else if l > maxLevel {
		l = maxLevel
	}

	// Optimize count against o.Max.
	if ticker.CountTicks(l) <= o.Max {
		// We're satisfying the o.Max and min/maxLevel
		// constraints. count is monotonically decreasing, so
		// decrease level to increase the count until we
		// violate either o.Max or minLevel.
		for l--; l >= minLevel && ticker.CountTicks(l) <= o.Max; l-- {
		}
		// We went one too far.
		l++
	} else {
		// We're over o.Max. Increase level to decrease the
		// count until we go below o.Max. This may cause us to
		// violate maxLevel.
		for l++; l <= maxLevel && ticker.CountTicks(l) > o.Max; l++ {
		}
		if l > maxLevel {
			// We can't satisfy both o.Max and maxLevel.
			return 0, false
		}
	}

	// At this point l is the lowest value that satisfies the
	// o.Max, minLevel, and maxLevel constraints.

	return l, true
}
