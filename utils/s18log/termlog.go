// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// termlog is a termbox logging on s18log package
package s18log

import (
	"sync"

	"github.com/nsf/termbox-go"
)

// Collection of log messages
// swagger:response termlog
type TermLog struct {
	Buffer []string
	Len    int
	Line   int
	L      sync.Mutex
}

func NewTermLog(sz int) TermLog {
	tl := TermLog{}
	tl.Len = sz
	tl.Buffer = make([]string, tl.Len)
	return tl
}

func (tl *TermLog) Write(b []byte) (n int, err error) {
	s := string(b)
	tl.Add(s)
	return len(b), nil
}

func (tl *TermLog) Add(s string) {
	//	ts := time.Now().Format("2006-01-02 15:04:05")
	//	s = " " + ts + " " + s
	tl.AddString(s)
}

func (tl *TermLog) AddString(s string) {
	tl.L.Lock()
	tl.Shift(s)
	tl.L.Unlock()
}

func (tl *TermLog) Shift(e string) {
	ns := make([]string, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}

func (tl *TermLog) Extend() {
	tl.Buffer = append(tl.Buffer, make([]string, tl.Len)...)
}

func (tl *TermLog) Shrink() {
	tl.Buffer = tl.Buffer[:tl.Len]
}

func (tl TermLog) Print() {
	for _, line := range tl.Buffer {
		x := 0
		for _, c := range line {
			termbox.SetCell(x, tl.Line, c, termbox.ColorWhite, termbox.ColorBlack)
			x++
		}
		tl.Line++
	}
}
