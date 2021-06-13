// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package s18log

import "sync"

// Collection of log messages
// swagger:response httplog
type HttpLog struct {
	Buffer []HttpMessage `json:"buffer"`
	Len    int           `json:"len"`
	Line   int           `json:"line"`
	L      sync.Mutex    `json:"-"`
}

// Log message
// in: HttpMessage
// swagger:response httpmessage
type HttpMessage struct {
	Group     string `json:"group"`
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

func NewHttpLog(sz int) HttpLog {
	tl := HttpLog{}
	tl.Len = sz
	tl.Buffer = make([]HttpMessage, tl.Len)
	return tl
}

func (tl *HttpLog) Add(s HttpMessage) {
	tl.L.Lock()
	tl.Shift(s)
	tl.L.Unlock()
}

func (tl *HttpLog) Shift(e HttpMessage) {
	ns := make([]HttpMessage, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}
