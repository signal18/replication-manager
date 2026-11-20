// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package s18log

import "sync"

const (
	// GroupNone is the sentinel used for non-cluster/global log entries.
	GroupNone = "none"
)

// Collection of log messages
// swagger:response httplog
type HttpLog struct {
	Buffer []HttpMessage `json:"buffer"`
	Len    int           `json:"len"`
	Line   int           `json:"line"`
	// Truncated is only ever set on a one-off HttpLog built from a
	// s18log.ReadHistory result (see server/api_global.go's readLogHistory);
	// the live ring buffers never set it, so it never appears in their JSON
	// output. true means a bound (bytes/files) was hit before the on-disk
	// scan was exhausted — the response may be missing older matches.
	Truncated bool       `json:"truncated,omitempty"`
	L         sync.Mutex `json:"-"`
}

// Log message
// in: HttpMessage
// swagger:response httpmessage
type HttpMessage struct {
	Group     string `json:"group"`
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
	Module    int    `json:"module"`
}

func NewHttpLog(sz int) HttpLog {
	tl := HttpLog{}
	tl.Len = sz
	tl.Buffer = make([]HttpMessage, tl.Len)
	return tl
}

func (tl *HttpLog) Add(s HttpMessage) int {
	tl.L.Lock()
	tl.Shift(s)
	tl.L.Unlock()
	return tl.Line
}

func (tl *HttpLog) Update(line int, s HttpMessage) {
	tl.L.Lock()
	tl.Buffer[line] = s
	tl.L.Unlock()
}

func (tl *HttpLog) Shift(e HttpMessage) {
	ns := make([]HttpMessage, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}

// AddReportDrop adds s like Add and reports whether a real entry (one with a timestamp)
// fell off the end of the ring to make room for it, so a caller can count its drops
// (T18). Shift keeps Len entries of the previous buffer, so the entry that goes is the
// one at index Len, when the buffer has one.
func (tl *HttpLog) AddReportDrop(s HttpMessage) bool {
	tl.L.Lock()
	defer tl.L.Unlock()
	dropped := len(tl.Buffer) > tl.Len && tl.Buffer[tl.Len].Timestamp != ""
	tl.Shift(s)
	return dropped
}

// Entries returns a copy of the ring's real entries, newest first: the ring is
// pre-filled with empty slots (no timestamp), which are left out.
func (tl *HttpLog) Entries() []HttpMessage {
	tl.L.Lock()
	defer tl.L.Unlock()
	out := make([]HttpMessage, 0, len(tl.Buffer))
	for _, m := range tl.Buffer {
		if m.Timestamp != "" {
			out = append(out, m)
		}
	}
	return out
}

// Resize sets the ring's capacity to sz, keeping the newest entries that fit, in a fresh
// backing array so the entries it no longer holds are freed. sz <= 0 changes nothing.
func (tl *HttpLog) Resize(sz int) {
	if sz <= 0 {
		return
	}
	tl.L.Lock()
	defer tl.L.Unlock()
	nb := make([]HttpMessage, sz)
	copy(nb, tl.Buffer)
	tl.Buffer = nb
	tl.Len = sz
}
