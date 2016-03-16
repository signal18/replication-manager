// termlog.go
package main

import (
	"time"

	"github.com/nsf/termbox-go"
)

type TermLog struct {
	buffer []string
	len    int
}

func NewTermLog(sz int) TermLog {
	tl := TermLog{}
	tl.len = sz
	tl.buffer = make([]string, tl.len)
	return tl
}

func (tl *TermLog) Add(s string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	s = " " + ts + " " + s
	tl.Shift(s)
}

func (tl *TermLog) Shift(e string) {
	ns := make([]string, 1)
	ns[0] = e
	tl.buffer = append(ns, tl.buffer[0:tl.len]...)
}

func (tl *TermLog) Extend() {
	tl.buffer = append(tl.buffer, make([]string, tl.len)...)
}

func (tl *TermLog) Shrink() {
	tl.buffer = tl.buffer[:tl.len]
}

func (tl TermLog) Print() {
	for _, line := range tl.buffer {
		printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, line)
		vy++
	}
}
