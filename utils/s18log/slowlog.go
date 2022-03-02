// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package s18log

import (
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
)

type SlowLog struct {
	Buffer []SlowMessage
	Len    int
	Line   int
	L      sync.Mutex
}

type SlowMessage struct {
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
	Digest        string             `json:"digest"`
}

func NewSlowMessage() *SlowMessage {
	m := new(SlowMessage)
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

// var regexp.MustCompile(`\b(User@Host: \S+\[\w+\]+ @ (?:)(\w+)? \[\S*\])|(Id:.+)`)
func NewSlowLog(sz int) SlowLog {
	tl := SlowLog{}
	tl.Len = sz
	tl.Buffer = make([]SlowMessage, tl.Len)
	return tl
}

func (tl *SlowLog) Add(s *SlowMessage) {
	tl.L.Lock()
	tl.Shift(*s)
	tl.L.Unlock()
}

func (tl *SlowLog) Shift(e SlowMessage) {
	ns := make([]SlowMessage, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}

func (tl *SlowLog) ParseLine(line string, sl *SlowMessage) {

	if !headerRe.MatchString(line) {
		tl.parseQuery(line, sl)
		return
	}

	if strings.HasPrefix(line, "# Time") {
		log.Printf("match 3 %s", line)
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

		// # User@Host: root[root] @  [127.0.0.1]
		m := userRe.FindStringSubmatch(line)

		if len(m) < 3 {
			return
		}
		sl.User = m[1]
		sl.Host = m[3]
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

func (tl *SlowLog) parseQuery(line string, sl *SlowMessage) {

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
		sl.Digest = crypto.GetMD5Hash(dbhelper.GetQueryDigest(line))
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
		sl.Digest = crypto.GetMD5Hash(dbhelper.GetQueryDigest(line))
	}
}

func (tl *SlowLog) parseAdmin(line string, sl *SlowMessage) {

	sl.Admin = true
	m := adminRe.FindStringSubmatch(line)
	sl.Query = m[1]

}
