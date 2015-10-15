// termlog.go
package main

import (
	"github.com/nsf/termbox-go"
	"time"
)

type TermLog []string

func NewTermLog(sz int) TermLog {
	tl := make(TermLog, sz)
	return tl
}

func (tl *TermLog) Add(s string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	s = " " + ts + " " + s
	*tl = shift(*tl, s)
}

func (tl TermLog) Print() {
	for _, line := range tl {
		printTb(0, vy, termbox.ColorWhite, termbox.ColorBlack, line)
		vy++
	}
}
