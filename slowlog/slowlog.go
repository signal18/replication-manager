// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package slowlog

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/misc"
)

type SlowLog struct {
	Buffer []Message
	Len    int
	Line   int
	L      sync.Mutex
}

type Message struct {
	Group         string             `json:"group"`
	Level         string             `json:"level"`
	Timestamp     string             `json:"timestamp"`
	Admin         bool               `json:"admin"` // true if Query is admin command
	Query         string             `json:"query"` // SQL query or admin command
	User          string             `json:"user"`
	Host          string             `json:"host"`
	Db            string             `json:"db"`
	TimeMetrics   map[string]float64 `json:"timeMetrics"`   // *_time and *_wait metrics
	NumberMetrics map[string]uint64  `json:"numberMetrics"` // most metrics
	BoolMetrics   map[string]bool    `json:"bollMetrics"`   // yes/no metrics
	RateType      string             `json:"raeType"`       // Percona Server rate limit type
	RateLimit     uint               `json:"rateLimit"`     // Percona Server rate limit value
	Text          string             `json:"text"`
}

func NewMessage() *Message {
	m := new(Message)
	m.TimeMetrics = make(map[string]float64)
	m.NumberMetrics = make(map[string]uint64)
	m.BoolMetrics = make(map[string]bool)
	return m
}

// Regular expressions to match important lines in slow log.
var timeRe = regexp.MustCompile(`Time: (\S+\s{1,2}\S+)`)
var userRe = regexp.MustCompile(`User@Host: ([^\[]+|\[[^[]+\]).*?@ (\S*) \[(.*)\]`)
var schema = regexp.MustCompile(`Schema: +(.*?) +Last_errno:`)
var headerRe = regexp.MustCompile(`^#\s+[A-Z]`)
var metricsRe = regexp.MustCompile(`(\w+): (\S+|\z)`)
var adminRe = regexp.MustCompile(`command: (.+)`)
var setRe = regexp.MustCompile(`^SET (?:last_insert_id|insert_id|timestamp)`)
var useRe = regexp.MustCompile(`^(?i)use `)

func NewSlowLog(sz int) SlowLog {
	tl := SlowLog{}
	tl.Len = sz
	tl.Buffer = make([]Message, tl.Len)
	return tl
}

func (tl *SlowLog) Add(s *Message) {
	tl.L.Lock()
	tl.Shift(*s)
	tl.L.Unlock()
}

func (tl *SlowLog) Shift(e Message) {
	ns := make([]Message, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}

func (tl *SlowLog) ParseLine(line string, sl *Message) {

	if !headerRe.MatchString(line) {
		tl.parseQuery(line, sl)
		return
	}

	if strings.HasPrefix(line, "# Time") {

		m := timeRe.FindStringSubmatch(line)
		if len(m) < 2 {
			return
		}
		sl.Timestamp = m[1]
		if userRe.MatchString(line) {
			m := userRe.FindStringSubmatch(line)
			sl.User = m[1]
			sl.Host = m[2]
		}
	} else if strings.HasPrefix(line, "# User") {

		m := userRe.FindStringSubmatch(line)
		if len(m) < 3 {
			return
		}
		sl.User = m[1]
		sl.Host = m[2]
	} else if strings.HasPrefix(line, "# admin") {
		tl.parseAdmin(line, sl)
	} else {

		submatch := schema.FindStringSubmatch(line)
		if len(submatch) == 2 {
			sl.Db = submatch[1]
		}

		m := metricsRe.FindAllStringSubmatch(line, -1)
		for _, smv := range m {
			// [String, Metric, Value], e.g. ["Query_time: 2", "Query_time", "2"]
			if strings.HasSuffix(smv[1], "_time") || strings.HasSuffix(smv[1], "_wait") {
				// microsecond value
				val, _ := strconv.ParseFloat(smv[2], 32)
				sl.TimeMetrics[misc.Camelcase(smv[1])] = float64(val)
			} else if smv[2] == "Yes" || smv[2] == "No" {
				// boolean value
				if smv[2] == "Yes" {
					sl.BoolMetrics[misc.Camelcase(smv[1])] = true
				} else {
					sl.BoolMetrics[misc.Camelcase(smv[1])] = false
				}
			} else if smv[1] == "Schema" {
				sl.Db = smv[2]
			} else if smv[1] == "Log_slow_rate_type" {
				sl.RateType = smv[2]
			} else if smv[1] == "Log_slow_rate_limit" {
				val, _ := strconv.ParseUint(smv[2], 10, 64)
				sl.RateLimit = uint(val)
			} else {
				// integer value
				val, err := strconv.ParseUint(smv[2], 10, 64)
				if err == nil {
					sl.NumberMetrics[misc.Camelcase(smv[1])] = val
				}
			}
		}
	}
}

func (tl *SlowLog) parseQuery(line string, sl *Message) {

	if strings.HasPrefix(line, "# admin") {
		return
	} else if headerRe.MatchString(line) {
		return
	}

	isUse := useRe.FindString(line)
	if isUse != "" {
		db := strings.TrimPrefix(line, isUse)
		db = strings.TrimRight(db, ";")
		db = strings.Trim(db, "`")
		sl.Db = db
		sl.Query = line
	} else if setRe.MatchString(line) {
		if strings.Contains(line, "timestamp") {
			val := strings.Split(line, "=")
			stime := strings.TrimRight(val[1], ";")
			i, err := strconv.ParseInt(stime, 10, 64)
			if err == nil {
				unixTimeUTC := time.Unix(i, 0)
				sl.Timestamp = unixTimeUTC.String()
			}
		}
	} else {
		sl.Query = line
	}
}

func (tl *SlowLog) parseAdmin(line string, sl *Message) {

	sl.Admin = true
	m := adminRe.FindStringSubmatch(line)
	sl.Query = m[1]

}
